// Package vault is the password / credential / API key store: a KDBX 4 database (the KeePass
// format, so KeePassXC and friends can open it) behind a small, hard-to-misuse API.
//
// The design and the reasons are in docs/plan-vault.md and docs/vault-v0-poc-report.md. The short
// version: the file on disk is always the encrypted KDBX; this package only ever holds the
// decrypted contents while a Vault value is open (unlocked); everything that touches the file
// goes through Save, which writes atomically, keeps the previous generation and takes a snapshot.
package vault

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	g "github.com/tobischo/gokeepasslib/v3"
	w "github.com/tobischo/gokeepasslib/v3/wrappers"
)

// Limits on what we are willing to read, independent of the vendored library's own caps.
const (
	MaxFileBytes       = 64 << 20 // a database file
	MaxAttachmentBytes = 5 << 20  // one attachment
	MaxEntries         = 50000
	MaxFieldBytes      = 256 << 10 // one text field
)

// Errors callers can match on.
var (
	ErrWrongCredentials = errors.New("vault: wrong password or key file, or the file is damaged")
	ErrTooLarge         = errors.New("vault: file or content too large")
	ErrUnsupported      = errors.New("vault: unsupported or unsafe database parameters")
	ErrLocked           = errors.New("vault: locked")
	ErrNotFound         = errors.New("vault: not found")
	ErrExists           = errors.New("vault: a vault already exists there")
	ErrInvalid          = errors.New("vault: invalid input")
	ErrBroken           = errors.New("vault: in-memory state is inconsistent, lock and unlock again")
	ErrDifferentVault   = errors.New("vault: that file is a different vault, not another copy of this one")
	ErrReview           = errors.New("vault: that file would remove many entries and needs your approval")
)

// Creds are the master credentials: a password and optionally the contents of a key file.
// The slices are zeroed by Vault.Close; callers should not keep copies.
type Creds struct {
	Password []byte
	KeyFile  []byte
}

func (c Creds) lib() (*g.DBCredentials, error) {
	if len(c.Password) == 0 {
		return nil, fmt.Errorf("%w: the master password must not be empty", ErrInvalid)
	}
	if len(c.KeyFile) == 0 {
		return g.NewPasswordCredentials(string(c.Password)), nil
	}
	return g.NewPasswordAndKeyDataCredentials(string(c.Password), c.KeyFile)
}

func (c Creds) clone() Creds {
	return Creds{Password: append([]byte(nil), c.Password...), KeyFile: append([]byte(nil), c.KeyFile...)}
}

func (c *Creds) wipe() {
	clear(c.Password)
	clear(c.KeyFile)
	c.Password, c.KeyFile = nil, nil
}

// KDF is the Argon2d cost used when a database is created or re-keyed.
type KDF struct {
	MemoryMiB   uint64 `json:"memory_mib"`
	Iterations  uint64 `json:"iterations"`
	Parallelism uint32 `json:"parallelism"`
}

// Presets. Standard is about 0.2 s on a desktop (docs/vault-v0-poc-report.md); phones are slower.
var (
	KDFStandard = KDF{MemoryMiB: 64, Iterations: 3, Parallelism: 2}
	KDFHigh     = KDF{MemoryMiB: 128, Iterations: 4, Parallelism: 4}
	KDFLow      = KDF{MemoryMiB: 32, Iterations: 3, Parallelism: 2}
	// KDFTest is for unit tests only: fast and weak.
	KDFTest = KDF{MemoryMiB: 1, Iterations: 1, Parallelism: 1}
)

func (k KDF) valid() bool {
	return k.MemoryMiB >= 1 && k.MemoryMiB <= g.MaxArgon2MemoryBytes>>20 && k.Iterations >= 1 && k.Iterations <= g.MaxArgon2Iterations &&
		k.Parallelism >= 1 && k.Parallelism <= g.MaxArgon2Parallelism && k.MemoryMiB*k.Iterations <= g.MaxArgon2Work
}

// newDatabase builds an empty KDBX 4 database with one root group.
func newDatabase(c Creds, kdf KDF, name string) (*g.Database, error) {
	if !kdf.valid() {
		return nil, fmt.Errorf("%w: key derivation cost out of range", ErrInvalid)
	}
	lc, err := c.lib()
	if err != nil {
		return nil, err
	}
	db := g.NewDatabase(g.WithDatabaseKDBXVersion40())
	db.Credentials = lc
	kp := db.Header.FileHeaders.KdfParameters
	kp.Memory = kdf.MemoryMiB << 20
	kp.Iterations = kdf.Iterations
	kp.Parallelism = kdf.Parallelism
	reseed(db)

	root := g.NewGroup()
	root.Name = name
	db.Content.Root = &g.RootData{Groups: []g.Group{root}}
	db.Content.Meta.DatabaseName = name
	db.Content.Meta.Generator = "ClipBridge"
	db.Content.Meta.RecycleBinEnabled = w.NewBoolWrapper(true)
	return db, nil
}

// reseed replaces every random value of the file header and the inner stream. The library re-uses
// the opened file's values on every save, which would repeat the (key, IV) pair of the ChaCha20
// stream across file generations and backups. Must be called while the protected values are plain.
func reseed(db *g.Database) {
	fh := db.Header.FileHeaders
	_, _ = rand.Read(fh.MasterSeed)
	_, _ = rand.Read(fh.EncryptionIV)
	if fh.KdfParameters != nil {
		_, _ = rand.Read(fh.KdfParameters.Salt[:])
	}
	if db.Content.InnerHeader != nil {
		_, _ = rand.Read(db.Content.InnerHeader.InnerRandomStreamKey)
	}
}

// decode parses and decrypts a database file; protected values come back in plain text.
// A panic inside the parser (the library has no recover of its own) becomes an error.
func decode(data []byte, c Creds) (*g.Database, error) {
	db, _, err := decodeEx(data, c)
	return db, err
}

// decodeEx is decode plus a flag telling that the file was a KDBX 3.x database that has been
// converted in memory to KDBX 4 (what KeePassXC's own default still produces); the next save
// writes it as KDBX 4 with Argon2d, after a snapshot of the original.
func decodeEx(data []byte, c Creds) (db *g.Database, upgraded bool, err error) {
	if len(data) > MaxFileBytes {
		return nil, false, ErrTooLarge
	}
	lc, err := c.lib()
	if err != nil {
		return nil, false, err
	}
	defer func() {
		if r := recover(); r != nil {
			db, upgraded, err = nil, false, fmt.Errorf("%w: the file could not be parsed", ErrWrongCredentials)
		}
	}()
	db = g.NewDatabase()
	db.Credentials = lc
	if err := g.NewDecoder(bytes.NewReader(data)).Decode(db); err != nil {
		switch {
		case errors.Is(err, g.ErrLimitExceeded), errors.Is(err, g.ErrUnsupportedKDF):
			return nil, false, fmt.Errorf("%w: %v", ErrUnsupported, err)
		default:
			return nil, false, ErrWrongCredentials // never say whether it was the password or damage
		}
	}
	if err := db.UnlockProtectedEntries(); err != nil {
		return nil, false, ErrWrongCredentials
	}
	if db.Content == nil || db.Content.Root == nil || len(db.Content.Root.Groups) == 0 || db.Content.Meta == nil {
		return nil, false, fmt.Errorf("%w: the database has no root group", ErrWrongCredentials)
	}
	if !db.Header.IsKdbx4() {
		nd, err := upgradeToV4(db, c)
		if err != nil {
			return nil, false, err
		}
		return nd, true, nil
	}
	return db, false, nil
}

// encode serialises the database with fresh random values. The protected values are plain on entry
// and again on return. If locking or unlocking fails the plaintext state is unknown and the
// returned error is ErrBroken: the caller must discard the database.
func encode(db *g.Database) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, ErrBroken
		}
	}()
	reseed(db)
	if err := db.LockProtectedEntries(); err != nil {
		return nil, ErrBroken
	}
	var buf bytes.Buffer
	encErr := g.NewEncoder(&buf).Encode(db)
	if err := db.UnlockProtectedEntries(); err != nil {
		return nil, ErrBroken
	}
	if encErr != nil {
		return nil, fmt.Errorf("vault: encode: %w", encErr)
	}
	if buf.Len() > MaxFileBytes {
		return nil, ErrTooLarge
	}
	return buf.Bytes(), nil
}

// ---- small helpers over the library's entry type -----------------------------------------

func entryGet(e *g.Entry, key string) string {
	for i := range e.Values {
		if e.Values[i].Key == key {
			return e.Values[i].Value.Content
		}
	}
	return ""
}

func entryHas(e *g.Entry, key string) bool {
	for i := range e.Values {
		if e.Values[i].Key == key {
			return true
		}
	}
	return false
}

func entrySet(e *g.Entry, key, value string, protected bool) {
	for i := range e.Values {
		if e.Values[i].Key == key {
			e.Values[i].Value = g.V{Content: value, Protected: w.NewBoolWrapper(protected)}
			return
		}
	}
	e.Values = append(e.Values, g.ValueData{Key: key, Value: g.V{Content: value, Protected: w.NewBoolWrapper(protected)}})
}

func entryDel(e *g.Entry, key string) {
	out := e.Values[:0]
	for _, v := range e.Values {
		if v.Key != key {
			out = append(out, v)
		}
	}
	e.Values = out
}

func uuidHex(u g.UUID) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, 32)
	for i, x := range u {
		b[i*2], b[i*2+1] = hexd[x>>4], hexd[x&15]
	}
	return string(b)
}

func parseUUID(s string) (g.UUID, bool) {
	var u g.UUID
	if len(s) != 32 {
		return u, false
	}
	for i := 0; i < 16; i++ {
		hi, ok1 := unhex(s[i*2])
		lo, ok2 := unhex(s[i*2+1])
		if !ok1 || !ok2 {
			return g.UUID{}, false
		}
		u[i] = hi<<4 | lo
	}
	return u, true
}

func unhex(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	}
	return 0, false
}

func unix(t *w.TimeWrapper) int64 {
	if t == nil || t.Time.IsZero() {
		return 0
	}
	return t.Time.Unix()
}

func timeWrap(sec int64) *w.TimeWrapper {
	t := w.Now()
	t.Time = time.Unix(sec, 0).UTC()
	return &t
}

// readAllLimited reads r but fails with ErrTooLarge past max bytes.
func readAllLimited(r io.Reader, max int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, ErrTooLarge
	}
	return b, nil
}

func wrapInvalid(format string, a ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, a...)...)
}

func (v *Vault) nowWrap() w.TimeWrapper { return *timeWrap(v.now().Unix()) }

// upgradeToV4 moves the contents of a KDBX 3.x database (plain values, as after decode) into a fresh
// KDBX 4 database that uses the standard Argon2d cost, keeping the same credentials.
func upgradeToV4(old *g.Database, c Creds) (*g.Database, error) {
	nd, err := newDatabase(c, KDFStandard, old.Content.Meta.DatabaseName)
	if err != nil {
		return nil, err
	}
	adoptContent(nd, old)
	return nd, nil
}

// adoptContent replaces the groups, entries and settings of nd with those of old. Attachments move
// into nd's inner header (they live in the XML meta block in KDBX 3) and every reference, including
// those inside history items, is renumbered. old must not be used afterwards.
func adoptContent(nd, old *g.Database) {
	remap := map[int]int{}
	mapBinary := func(id int) (int, bool) {
		if n, ok := remap[id]; ok {
			return n, true
		}
		b := old.FindBinary(id)
		if b == nil {
			return 0, false
		}
		content, err := b.GetContentBytes()
		if err != nil || len(content) > MaxAttachmentBytes {
			return 0, false
		}
		n := nd.AddBinary(content).ID
		remap[id] = n
		return n, true
	}
	var fix func(es []g.Entry)
	fix = func(es []g.Entry) {
		for i := range es {
			refs := es[i].Binaries[:0]
			for _, r := range es[i].Binaries {
				if n, ok := mapBinary(r.Value.ID); ok {
					r.Value.ID = n
					refs = append(refs, r)
				}
			}
			es[i].Binaries = refs
			for h := range es[i].Histories {
				fix(es[i].Histories[h].Entries)
			}
		}
	}
	var rec func(gr *g.Group)
	rec = func(gr *g.Group) {
		fix(gr.Entries)
		for i := range gr.Groups {
			rec(&gr.Groups[i])
		}
	}
	for i := range old.Content.Root.Groups {
		rec(&old.Content.Root.Groups[i])
	}
	meta := old.Content.Meta
	meta.Binaries = nil
	meta.Generator = "ClipBridge"
	nd.Content.Meta = meta
	nd.Content.Root = old.Content.Root
}
