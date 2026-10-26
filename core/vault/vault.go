package vault

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	g "github.com/tobischo/gokeepasslib/v3"
)

// FileName is the vault inside its directory; Prev is the generation before the last save and
// BackupDir holds the snapshots (see backup.go).
const (
	FileName  = "vault.kdbx"
	PrevName  = "vault.kdbx.prev"
	BackupDir = "backups"
)

// Vault is an open (unlocked) database. All methods are safe for concurrent use. Close it as soon as
// the user locks: it wipes the credentials and drops the decrypted contents.
type Vault struct {
	mu     sync.Mutex
	dir    string
	db     *g.Database
	creds  Creds
	closed bool
	broken bool
	now    func() time.Time

	lastSnapshot     time.Time
	lastSyncSnapshot time.Time
	onSaved          func()
}

// SetOnSaved registers a function called (in its own goroutine) after every successful save. The
// sync service uses it to push the new file to the other devices.
func (v *Vault) SetOnSaved(f func()) {
	v.mu.Lock()
	v.onSaved = f
	v.mu.Unlock()
}

// FileHash returns the SHA-256 (hex) and size of the vault file in dir, and false if there is none.
// It works while the vault is locked: the file is ciphertext.
func FileHash(dir string) (hash string, size int64, ok bool) {
	data, err := readFileLimited(filepath.Join(dir, FileName))
	if err != nil {
		return "", 0, false
	}
	sum := fingerprint(data)
	const hexd = "0123456789abcdef"
	b := make([]byte, 64)
	for i, x := range sum {
		b[i*2], b[i*2+1] = hexd[x>>4], hexd[x&15]
	}
	return string(b), int64(len(data)), true
}

// Exists reports whether dir holds a vault file.
func Exists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, FileName))
	return err == nil
}

// Create makes a new vault in dir (created if needed, mode 0700). It fails if one exists.
func Create(dir string, c Creds, kdf KDF, name string) (*Vault, error) {
	if name == "" {
		name = "ClipBridge"
	}
	if Exists(dir) {
		return nil, ErrExists
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("vault: create directory: %w", err)
	}
	db, err := newDatabase(c, kdf, name)
	if err != nil {
		return nil, err
	}
	v := &Vault{dir: dir, db: db, creds: c.clone(), now: time.Now}
	if err := v.saveLocked("create"); err != nil {
		v.closeLocked()
		return nil, err
	}
	return v, nil
}

// Open unlocks the vault in dir. The error for a wrong password, a wrong key file and a damaged
// file is the same on purpose.
func Open(dir string, c Creds) (*Vault, error) {
	data, err := readFileLimited(filepath.Join(dir, FileName))
	if err != nil {
		return nil, err
	}
	db, upgraded, err := decodeEx(data, c)
	if err != nil {
		return nil, err
	}
	v := &Vault{dir: dir, db: db, creds: c.clone(), now: time.Now}
	weak := !upgraded && weakKDF(db)
	if weak {
		// A file whose key derivation is below our floor (made by another program, or by a peer that
		// chose a cheap one) is written back with the standard cost on the spot.
		raiseKDF(v.db)
	}
	if upgraded || weak {
		// An older KDBX 3.x file (for instance one made by KeePassXC's defaults): write it as KDBX 4
		// right away; the original is kept as a snapshot and as the previous generation.
		if err := v.saveLocked("upgrade"); err != nil {
			v.closeLocked()
			return nil, err
		}
	}
	return v, nil
}

func readFileLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("vault: open file: %w", err)
	}
	defer f.Close()
	return readAllLimited(f, MaxFileBytes)
}

// Close wipes the credentials and the decrypted database. Strings handed out earlier stay in memory
// until the garbage collector reuses them: Go cannot wipe strings, see docs/plan-vault.md §4.4.
func (v *Vault) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.closeLocked()
}

func (v *Vault) closeLocked() {
	if v.closed {
		return
	}
	v.closed = true
	v.creds.wipe()
	v.db = nil
}

// Dir is the vault directory.
func (v *Vault) Dir() string { return v.dir }

func (v *Vault) path() string { return filepath.Join(v.dir, FileName) }

// usable returns an error if the vault cannot be used. v.mu held.
func (v *Vault) usable() error {
	if v.closed || v.db == nil {
		return ErrLocked
	}
	if v.broken {
		return ErrBroken
	}
	return nil
}

// KDFInfo reports the Argon2d cost the file currently uses.
func (v *Vault) KDFInfo() (KDF, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return KDF{}, err
	}
	kp := v.db.Header.FileHeaders.KdfParameters
	if kp == nil {
		return KDF{}, ErrUnsupported
	}
	return KDF{MemoryMiB: kp.Memory >> 20, Iterations: kp.Iterations, Parallelism: kp.Parallelism}, nil
}

// Save writes the vault to disk now (every mutating method already does).
func (v *Vault) Save() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	return v.saveLocked("save")
}

// commit persists a mutation. v.mu held.
func (v *Vault) commit(reason string) error { return v.saveLocked(reason) }

// saveLocked encodes with fresh random values, snapshots what is on disk, keeps it as .prev and
// replaces vault.kdbx atomically. v.mu held.
func (v *Vault) saveLocked(reason string) error {
	if err := v.usable(); err != nil {
		return err
	}
	out, err := encode(v.db)
	if err != nil {
		if errors.Is(err, ErrBroken) {
			v.broken = true
		}
		return err
	}
	// Prove the new bytes open with the same credentials before they replace anything.
	check, err := decode(out, v.creds)
	if err != nil {
		return fmt.Errorf("vault: refusing to save, the new file does not read back: %w", err)
	}
	_ = check

	path := v.path()
	if old, err := os.ReadFile(path); err == nil {
		v.snapshotLocked(old, reason)
		if err := writeFileSync(path+".prev.tmp", old); err == nil {
			_ = os.Rename(path+".prev.tmp", filepath.Join(v.dir, PrevName))
		}
	}
	tmp := path + ".tmp"
	if err := writeFileSync(tmp, out); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("vault: replace file: %w", err)
	}
	syncDir(v.dir)
	if v.onSaved != nil {
		go v.onSaved()
	}
	return nil
}

func writeFileSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("vault: write file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("vault: write file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("vault: sync file: %w", err)
	}
	return f.Close()
}

// syncDir flushes the directory entry of a rename; best effort (not supported on Windows).
func syncDir(dir string) {
	if f, err := os.Open(dir); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
}

func fingerprint(b []byte) [32]byte { return sha256.Sum256(b) }

// binUUIDFor derives the recycle bin's id from the vault's root group, so replicas of one vault that
// each create their bin on their own still end up with the same group.
func binUUIDFor(root g.UUID) g.UUID {
	sum := sha256.Sum256(append([]byte("clipbridge/recycle-bin/"), root[:]...))
	var u g.UUID
	copy(u[:], sum[:16])
	return u
}

// ChangeCredentials re-keys the vault: new master password / key file and optionally a new KDF
// cost (zero KDF keeps the current one). Everything is re-encrypted with fresh random values.
// Old snapshots keep the old credentials; the caller should tell the user.
func (v *Vault) ChangeCredentials(c Creds, kdf KDF) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	lc, err := c.lib()
	if err != nil {
		return err
	}
	prevCreds, prevCredsLib := v.creds, v.db.Credentials
	prevKDF := *v.db.Header.FileHeaders.KdfParameters
	if kdf != (KDF{}) {
		if !kdf.valid() {
			return invalid("key derivation cost out of range")
		}
		kp := v.db.Header.FileHeaders.KdfParameters
		kp.Memory, kp.Iterations, kp.Parallelism = kdf.MemoryMiB<<20, kdf.Iterations, kdf.Parallelism
	}
	v.db.Credentials = lc
	v.creds = c.clone()
	now := v.nowWrap()
	v.db.Content.Meta.MasterKeyChanged = &now
	if err := v.saveLocked("rekey"); err != nil {
		v.db.Credentials, v.creds = prevCredsLib, prevCreds
		*v.db.Header.FileHeaders.KdfParameters = prevKDF
		return err
	}
	prevCreds.wipe()
	return nil
}

// VerifyCredentials reports whether c is the vault's current master credentials, without touching
// the file (used to re-confirm the master password before revealing or exporting).
func (v *Vault) VerifyCredentials(c Creds) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.usable() != nil || len(c.Password) == 0 {
		return false
	}
	okPw := subtle.ConstantTimeCompare(c.Password, v.creds.Password) == 1
	okKey := subtle.ConstantTimeCompare(c.KeyFile, v.creds.KeyFile) == 1
	return okPw && okKey
}
