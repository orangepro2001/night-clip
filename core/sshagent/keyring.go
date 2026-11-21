// Package sshagent serves SSH keys held by the core over the ssh-agent
// protocol. Keys never leave the process: clients get public keys and
// signatures, and every attempt to add, remove or lock keys is refused.
//
// The key set (Keyring) is independent of the vault. The facade loads it
// when the vault unlocks and after each save, lets it outlive an idle lock
// for a grace period, and clears it on a manual lock, a session lock,
// suspend and shutdown.
package sshagent

import (
	"crypto/ed25519"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Key is one loaded key.
type Key struct {
	useMu       sync.RWMutex
	revoked     bool
	Signer      ssh.Signer
	Public      ssh.PublicKey
	EntryID     string
	Title       string
	Comment     string
	Fingerprint string
	// Hosts, when not empty, limits the key to these servers (host name
	// patterns or "SHA256:" host key fingerprints, see KnownHosts.Matches).
	Hosts []string

	// seed is zeroed when the key is dropped (Ed25519 only: RSA and ECDSA
	// keys live in big.Int values that cannot be wiped reliably).
	seed ed25519.PrivateKey
}

// NewKey wraps a private key (as returned by sshkey) for the keyring.
func NewKey(priv any, entryID, title, comment string) (*Key, error) {
	var seed ed25519.PrivateKey
	if e, ok := priv.(ed25519.PrivateKey); ok {
		// Own copy: wiping it must not touch a key someone else still holds.
		seed = append(ed25519.PrivateKey(nil), e...)
		priv = seed
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	return &Key{Signer: s, Public: s.PublicKey(), EntryID: entryID, Title: title, Comment: comment,
		Fingerprint: ssh.FingerprintSHA256(s.PublicKey()), seed: seed}, nil
}

func (k *Key) wipe() {
	k.useMu.Lock()
	defer k.useMu.Unlock()
	k.revoked = true
	for i := range k.seed {
		k.seed[i] = 0
	}
}

// Keyring states.
const (
	StateEmpty  = "empty"
	StateLoaded = "loaded" // the vault is unlocked
	StateGrace  = "grace"  // the vault locked from idle; keys stay until ExpiresAt
)

// Keyring holds the loaded keys. Gen changes on every Clear and Grace so a
// reload that started before a lock cannot put keys back afterwards.
type Keyring struct {
	mu      sync.Mutex
	keys    []*Key
	state   string
	expires time.Time
	gen     uint64
	now     func() time.Time

	// OnExpire is called (without the lock) when a grace period runs out on
	// access. The facade also arms a timer; whichever comes first reports it.
	OnExpire func()
}

// NewKeyring returns an empty keyring. now may be nil (time.Now).
func NewKeyring(now func() time.Time) *Keyring {
	if now == nil {
		now = time.Now
	}
	return &Keyring{state: StateEmpty, now: now}
}

// Gen returns the current generation; pass it to Replace.
func (r *Keyring) Gen() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gen
}

// Replace installs a freshly loaded key set if nothing cleared or locked the
// keyring since gen was read. It reports whether it did.
func (r *Keyring) Replace(gen uint64, keys []*Key) bool {
	r.mu.Lock()
	if r.gen != gen {
		r.mu.Unlock()
		for _, k := range keys {
			k.wipe()
		}
		return false
	}
	old := r.keys
	r.keys, r.state, r.expires = keys, StateLoaded, time.Time{}
	r.mu.Unlock()
	wipeExcept(old, keys)
	return true
}

// Clear drops every key and returns how many there were.
func (r *Keyring) Clear() int {
	r.mu.Lock()
	old := r.keys
	r.keys, r.state, r.expires = nil, StateEmpty, time.Time{}
	r.gen++
	r.mu.Unlock()
	wipeExcept(old, nil)
	return len(old)
}

// Grace keeps the current keys until the given wall-clock time. It returns
// false (and does nothing) when there are no keys.
func (r *Keyring) Grace(until time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gen++
	if len(r.keys) == 0 {
		r.state = StateEmpty
		return false
	}
	r.state, r.expires = StateGrace, until.Round(0)
	return true
}

// Snapshot returns the usable keys and the state, expiring a grace period
// whose time has passed.
func (r *Keyring) Snapshot() ([]*Key, string, time.Time) {
	r.mu.Lock()
	if r.state == StateGrace && !r.now().Round(0).Before(r.expires) {
		old := r.keys
		r.keys, r.state, r.expires = nil, StateEmpty, time.Time{}
		r.gen++
		cb := r.OnExpire
		r.mu.Unlock()
		wipeExcept(old, nil)
		if cb != nil {
			cb()
		}
		return nil, StateEmpty, time.Time{}
	}
	keys := append([]*Key(nil), r.keys...)
	st, exp := r.state, r.expires
	r.mu.Unlock()
	return keys, st, exp
}

// Find returns the key with the given public key blob, if loaded.
func (r *Keyring) Find(blob []byte) *Key {
	keys, _, _ := r.Snapshot()
	for _, k := range keys {
		if string(k.Public.Marshal()) == string(blob) {
			return k
		}
	}
	return nil
}

// wipeExcept zeroes the Ed25519 seeds of keys in old that are not in keep.
// Wipe discards newly built keys that were never installed in a ring.
func Wipe(keys []*Key) {
	for _, key := range keys {
		key.wipe()
	}
}

func wipeExcept(old, keep []*Key) {
	kept := map[*Key]bool{}
	for _, k := range keep {
		kept[k] = true
	}
	for _, k := range old {
		if !kept[k] {
			k.wipe()
		}
	}
}
