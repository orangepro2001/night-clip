// Package vsync copies the password vault between the user's own paired devices.
//
// What travels is always the encrypted KDBX file, never a decrypted entry: a device receives the
// other's file, keeps it aside, and merges it into its own vault with the KeePass rules of
// core/vault/merge.go once the vault is unlocked. The transport is the same mutually pinned TLS
// connection the rest of the app uses; the listening side serves three routes, the dialing side
// polls and pushes (docs/plan-vault.md §5).
//
// Nothing is shared with a peer unless the user switched "vault sync" on for it on this device, and
// the other device has to do the same.
package vsync

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"nightclip/core/peer"
	"nightclip/core/store"
	"nightclip/core/vault"
)

// States of the sync with one peer, as shown to the user.
const (
	StateIdle          = "idle"
	StatePendingUnlock = "pending_unlock" // the other device's changes wait until this vault is unlocked
	StateNeedsPassword = "needs_password" // the other device's file needs a different master password
	StateReview        = "review"         // a guard stopped the merge: unrelated vault, or many deletions
	StatePeerOff       = "peer_off"       // the other device has not enabled vault sync for us
	StateError         = "error"
)

const (
	// MaxFileBytes is the largest vault file we accept from a peer.
	MaxFileBytes = vault.MaxFileBytes
	// MaxRemovals: one sync that would delete more entries than this waits for the user's approval.
	MaxRemovals = 5
	// RemovalBudget: all the deletions applied from one peer within RemovalWindow; more waits for approval.
	// A compromised device that knows the master password cannot empty the vault five entries at a time.
	RemovalBudget = 15
	RemovalWindow = 24 * time.Hour
	// MaxOverwrites: one sync that would overwrite more entries than this waits for approval.
	MaxOverwrites = 50
	incomingDir   = "incoming"
)

type removal struct {
	at time.Time
	n  int
}

var peerIDRe = regexp.MustCompile(`^dev_[a-z2-7]{26}$`)

// Provider gives the service the vault of this device.
type Provider interface {
	// Current returns the unlocked vault, or nil while it is locked.
	Current() *vault.Vault
	// Dir is the vault directory (holds vault.kdbx).
	Dir() string
}

// Timing can be shortened by tests.
type Timing struct {
	Poll      time.Duration // how often a dialing device asks the other for its file hash
	HTTP      time.Duration // per request
	MinPutGap time.Duration // at most one upload per peer in this time
}

func (t Timing) withDefaults() Timing {
	if t.Poll <= 0 {
		t.Poll = 15 * time.Second
	}
	if t.HTTP <= 0 {
		t.HTTP = 60 * time.Second
	}
	if t.MinPutGap <= 0 {
		t.MinPutGap = 2 * time.Second
	}
	return t
}

// Config wires the service.
type Config struct {
	Engine   *peer.Engine
	DB       *store.DB
	Provider Provider
	Emit     func(map[string]any)
	Now      func() time.Time
	Timing   Timing
}

// Service is the sync engine; create it before the peer engine starts (it registers HTTP routes).
type Service struct {
	e      *peer.Engine
	db     *store.DB
	prov   Provider
	emit   func(map[string]any)
	now    func() time.Time
	timing Timing

	procMu  sync.Mutex           // one merge at a time
	putSem  chan struct{}        // one upload body is read at a time: a peer cannot queue gigabytes in memory
	failed  map[string]string    // peer -> sha256 of a stashed file that could not be merged yet (wrong password, needs review)
	budget  map[string][]removal // peer -> deletions applied from it recently
	mu      sync.Mutex
	lastPut map[string]time.Time
	kick    chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
}

// New registers the routes on the engine.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Emit == nil {
		cfg.Emit = func(map[string]any) {}
	}
	s := &Service{e: cfg.Engine, db: cfg.DB, prov: cfg.Provider, emit: cfg.Emit, now: cfg.Now, timing: cfg.Timing.withDefaults(),
		lastPut: map[string]time.Time{}, kick: make(chan struct{}, 1), putSem: make(chan struct{}, 1),
		failed: map[string]string{}, budget: map[string][]removal{}}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.e.Handle("GET /v1/vault/head", http.HandlerFunc(s.serveHead), false)
	s.e.Handle("GET /v1/vault/file", http.HandlerFunc(s.serveFile), false)
	s.e.Handle("PUT /v1/vault/file", http.HandlerFunc(s.servePut), false)
	return s
}

// Start begins polling.
func (s *Service) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	s.wg.Add(1)
	go s.loop()
}

// Close stops the loop.
func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

// Kick asks for a sync round now: the local file changed, a link came up, a setting changed.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// OnPeerState is the engine's peer.state hook.
func (s *Service) OnPeerState(peerID, state string) {
	if state == peer.StateConnected {
		s.Kick()
	}
}

func (s *Service) dir() string { return s.prov.Dir() }

// incomingPath is where a peer's file waits. Peer ids are validated: a made-up id must never reach
// the file system ("../vault" would be the live vault).
func (s *Service) incomingPath(peerID string) string {
	if !peerIDRe.MatchString(peerID) {
		return ""
	}
	return filepath.Join(s.dir(), incomingDir, peerID+".kdbx")
}

func fileSHA(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *Service) loop() {
	defer s.wg.Done()
	t := time.NewTicker(s.timing.Poll)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
		s.round()
	}
}

// round syncs with every enabled peer that we dial.
func (s *Service) round() {
	peers, err := s.db.ListPeers()
	if err != nil {
		return
	}
	connected := map[string]bool{}
	for _, st := range s.e.PeerStatuses() {
		connected[st.DeviceID] = st.State == peer.StateConnected
	}
	for _, p := range peers {
		if s.ctx.Err() != nil {
			return
		}
		if p.State != "paired" || !p.VaultSync || !connected[p.DeviceID] || !s.e.WeDial(p) {
			continue
		}
		s.syncPeer(p)
	}
}

// ---- state --------------------------------------------------------------------------------------

func (s *Service) setState(peerID, state, detail string, mutate func(*store.VaultSyncRow)) {
	row, _, _ := s.db.GetVaultSync(peerID)
	row.PeerID = peerID
	prev := row.State
	row.State, row.Detail = state, detail
	if mutate != nil {
		mutate(&row)
	}
	_ = s.db.PutVaultSync(row)
	if prev != state || detail != "" {
		name := peerID
		if p, found, _ := s.db.GetPeer(peerID); found {
			name = p.Name
		}
		s.emit(map[string]any{"type": "vault.sync", "device_id": peerID, "name": name, "state": state, "detail": detail})
	}
}

func fileBytes(dir string) ([]byte, string, bool) {
	hash, _, ok := vault.FileHash(dir)
	if !ok {
		return nil, "", false
	}
	b, err := os.ReadFile(filepath.Join(dir, vault.FileName))
	if err != nil {
		return nil, "", false
	}
	return b, hash, true
}

// ---- client side (the dialing device) -----------------------------------------------------------

type head struct {
	Present bool   `json:"present"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}

func (s *Service) syncPeer(p store.Peer) {
	ep, err := s.e.Endpoint(p)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, s.timing.HTTP)
	defer cancel()

	var h head
	var lastErr error
	var base string
	for _, b := range ep.Bases {
		if lastErr = getJSON(ctx, ep.Client, b+"/v1/vault/head", &h); lastErr == nil {
			base = b
			break
		}
	}
	if lastErr != nil {
		var pe *peerError
		if errors.As(lastErr, &pe) && pe.status == http.StatusForbidden {
			s.setState(p.DeviceID, StatePeerOff, "the other device has not switched vault sync on for this device", nil)
		}
		return
	}
	row, _, _ := s.db.GetVaultSync(p.DeviceID)
	local, localHash, haveLocal := fileBytes(s.dir())
	if !s.stillEnabled(p.DeviceID) {
		return
	}

	// 1. nothing here, something there: join
	if !haveLocal && h.Present {
		data, err := getBytes(ctx, ep.Client, base+"/v1/vault/file")
		if err != nil || !s.stillEnabled(p.DeviceID) {
			return
		}
		s.adopt(p, data, h.SHA256)
		return
	}
	// 2. their file changed since we last looked: fetch, keep aside, merge
	if h.Present && h.SHA256 != row.SeenHash {
		data, err := getBytes(ctx, ep.Client, base+"/v1/vault/file")
		if err != nil || !s.stillEnabled(p.DeviceID) {
			return
		}
		s.receive(p.DeviceID, data, h.SHA256)
		local, localHash, haveLocal = fileBytes(s.dir())
		row, _, _ = s.db.GetVaultSync(p.DeviceID)
	}
	// 3. our file changed since we last sent it (switching sync off stops a round already under way)
	if !s.stillEnabled(p.DeviceID) {
		return
	}
	if haveLocal && localHash != row.SentHash && localHash != h.SHA256 {
		if err := put(ctx, ep.Client, base+"/v1/vault/file", local); err != nil {
			var pe *peerError
			if errors.As(err, &pe) && pe.status == http.StatusForbidden {
				s.setState(p.DeviceID, StatePeerOff, "the other device has not switched vault sync on for this device", nil)
			}
			return
		}
		s.setState(p.DeviceID, s.currentState(p.DeviceID), "", func(r *store.VaultSyncRow) { r.SentHash, r.LastOKAt = localHash, s.now().Unix() })
	} else if haveLocal && localHash == h.SHA256 {
		s.setState(p.DeviceID, s.currentState(p.DeviceID), "", func(r *store.VaultSyncRow) { r.SentHash, r.SeenHash, r.LastOKAt = localHash, localHash, s.now().Unix() })
	}
}

// stillEnabled re-reads the peer: the user may have switched vault sync off while a round was running.
func (s *Service) stillEnabled(peerID string) bool {
	p, found, err := s.db.GetPeer(peerID)
	return err == nil && found && p.State == "paired" && p.VaultSync
}

func (s *Service) currentState(peerID string) string {
	row, _, _ := s.db.GetVaultSync(peerID)
	if row.State == StatePeerOff || row.State == StateError {
		return StateIdle
	}
	return row.State
}

type peerError struct {
	status int
	msg    string
}

func (e *peerError) Error() string {
	return fmt.Sprintf("vault sync: peer answered %d %s", e.status, e.msg)
}

func do(ctx context.Context, c *http.Client, method, url string, body []byte) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, &peerError{status: resp.StatusCode, msg: string(b)}
	}
	return resp, nil
}

func getJSON(ctx context.Context, c *http.Client, url string, out any) error {
	resp, err := do(ctx, c, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(out)
}

func getBytes(ctx context.Context, c *http.Client, url string) ([]byte, error) {
	resp, err := do(ctx, c, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxFileBytes {
		return nil, vault.ErrTooLarge
	}
	return b, nil
}

func put(ctx context.Context, c *http.Client, url string, body []byte) error {
	resp, err := do(ctx, c, http.MethodPut, url, body)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// ---- receiving (both directions end here) ---------------------------------------------------------------

var kdbxMagic = []byte{0x03, 0xd9, 0xa2, 0x9a, 0x67, 0xfb, 0x4b, 0xb5}

func looksLikeKDBX(b []byte) bool { return len(b) > 64 && bytes.Equal(b[:8], kdbxMagic) }

// adopt makes a received file this device's vault when there was none ("join the vault of ...").
func (s *Service) adopt(p store.Peer, data []byte, hash string) {
	s.procMu.Lock()
	defer s.procMu.Unlock()
	if !looksLikeKDBX(data) || vault.Exists(s.dir()) {
		return
	}
	// A file that merely starts with the right bytes, or asks for an absurd key derivation, is not a vault.
	if err := vault.CheckPeerKDF(data); err != nil {
		s.setState(p.DeviceID, StateError, "the other device's vault file was refused: "+err.Error(), nil)
		return
	}
	if err := os.MkdirAll(s.dir(), 0o700); err != nil {
		return
	}
	path := filepath.Join(s.dir(), vault.FileName)
	if err := writeAtomicAs(path, path+".join.tmp", data); err != nil {
		return
	}
	got, _, _ := vault.FileHash(s.dir())
	s.setState(p.DeviceID, StateIdle, "", func(r *store.VaultSyncRow) { r.SeenHash, r.SentHash, r.LastOKAt = got, got, s.now().Unix() })
	s.emit(map[string]any{"type": "vault.joined", "device_id": p.DeviceID, "name": p.Name})
}

func writeAtomic(path string, data []byte) error { return writeAtomicAs(path, path+".tmp", data) }

func writeAtomicAs(path, tmp string, data []byte) error {
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// receive keeps the peer's file aside and merges it when the vault is open. remoteHash is what the
// peer's file hashes to (empty: compute).
func (s *Service) receive(peerID string, data []byte, remoteHash string) {
	s.procMu.Lock()
	defer s.procMu.Unlock()
	if s.incomingPath(peerID) == "" {
		return
	}
	if !looksLikeKDBX(data) {
		s.setState(peerID, StateError, "the other device sent something that is not a vault file", nil)
		return
	}
	delete(s.failed, peerID) // a new file deserves a new try
	if err := os.MkdirAll(filepath.Join(s.dir(), incomingDir), 0o700); err != nil {
		return
	}
	if err := writeAtomic(s.incomingPath(peerID), data); err != nil {
		return
	}
	s.mergeStash(peerID, remoteHash, vault.Creds{}, false)
}

// removalsLeft is how many more deletions may be applied from this peer right now.
func (s *Service) removalsLeft(peerID string) int {
	now := s.now()
	kept, used := s.budget[peerID][:0], 0
	for _, r := range s.budget[peerID] {
		if now.Sub(r.at) < RemovalWindow {
			kept = append(kept, r)
			used += r.n
		}
	}
	s.budget[peerID] = kept
	left := RemovalBudget - used
	if left < 0 {
		left = 0
	}
	return left
}

// mergeStash merges the peer's stashed file into the open vault. procMu held. force skips the
// "this exact file already failed" memory (the user supplied a password or approved).
func (s *Service) mergeStash(peerID, remoteHash string, c vault.Creds, approve bool) {
	seen := func(r *store.VaultSyncRow) {
		if remoteHash != "" {
			r.SeenHash = remoteHash
		}
	}
	path := s.incomingPath(peerID)
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	sum := fileSHA(data)
	force := approve || len(c.Password) > 0
	if !force && s.failed[peerID] == sum {
		return // the same file failed before and nothing has changed: do not pay for the key derivation again
	}
	v := s.prov.Current()
	if v == nil {
		s.setState(peerID, StatePendingUnlock, "the other device's changes are merged when you unlock the vault", seen)
		return
	}
	opts := vault.MergeOptions{PeerFile: true, RequireSameVault: !approve}
	if !approve {
		left := s.removalsLeft(peerID)
		if left > MaxRemovals {
			left = MaxRemovals
		}
		opts.LimitRemoved, opts.MaxRemoved = true, left
		opts.LimitUpdated, opts.MaxUpdated = true, MaxOverwrites
	}
	st, err := v.MergeGuarded(data, c, "sync", opts)
	switch {
	case err == nil:
		delete(s.failed, peerID)
		_ = os.Remove(path)
		if st.EntriesRemoved > 0 {
			s.budget[peerID] = append(s.budget[peerID], removal{s.now(), st.EntriesRemoved})
		}
		s.setState(peerID, StateIdle, "", func(r *store.VaultSyncRow) { seen(r); r.LastOKAt = s.now().Unix() })
		if st.Changed() {
			s.emit(map[string]any{"type": "vault.changed", "reason": "sync", "device_id": peerID, "stats": st})
		}
	case errors.Is(err, vault.ErrLocked):
		s.setState(peerID, StatePendingUnlock, "the other device's changes are merged when you unlock the vault", seen)
	case errors.Is(err, vault.ErrWrongCredentials):
		s.failed[peerID] = sum
		s.setState(peerID, StateNeedsPassword, "the other device's vault uses a different master password: enter it to merge", seen)
	case errors.Is(err, vault.ErrDifferentVault):
		s.failed[peerID] = sum
		s.setState(peerID, StateReview, "the other device has a different vault: approve to merge both", seen)
	case errors.Is(err, vault.ErrReview):
		s.failed[peerID] = sum
		s.setState(peerID, StateReview, "the other device's copy would delete or overwrite many entries: review and approve", seen)
	case errors.Is(err, vault.ErrUnsupported), errors.Is(err, vault.ErrTooLarge):
		s.failed[peerID] = sum
		s.setState(peerID, StateError, "the other device's file was refused: "+err.Error(), seen)
	default:
		s.setState(peerID, StateError, err.Error(), seen)
	}
}

// OnUnlocked merges whatever other devices sent while the vault was locked.
func (s *Service) OnUnlocked() {
	s.procMu.Lock()
	entries, _ := os.ReadDir(filepath.Join(s.dir(), incomingDir))
	for _, e := range entries {
		id := e.Name()
		if filepath.Ext(id) != ".kdbx" {
			continue
		}
		id = id[:len(id)-len(".kdbx")]
		if p, found, _ := s.db.GetPeer(id); !found || p.State != "paired" || !p.VaultSync {
			_ = os.Remove(filepath.Join(s.dir(), incomingDir, e.Name()))
			continue
		}
		s.mergeStash(id, "", vault.Creds{}, false)
	}
	s.procMu.Unlock()
	s.Kick()
}

// Resolve finishes a stuck sync: the master password of the other device's vault (needs_password)
// and/or the user's approval (review). An approval names the SHA-256 of the file the user looked at
// (see Status): if the stashed file has changed since, nothing is merged.
func (s *Service) Resolve(peerID string, c vault.Creds, approve bool, sha string) error {
	s.procMu.Lock()
	defer s.procMu.Unlock()
	path := s.incomingPath(peerID)
	if path == "" {
		return vault.ErrNotFound
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return vault.ErrNotFound
	}
	if approve && (sha == "" || sha != fileSHA(data)) {
		return vault.ErrReview // the file changed since it was reviewed, or no file was named
	}
	if s.prov.Current() == nil {
		return vault.ErrLocked
	}
	s.mergeStash(peerID, "", c, approve)
	row, _, _ := s.db.GetVaultSync(peerID)
	if row.State == StateNeedsPassword && len(c.Password) > 0 {
		return vault.ErrWrongCredentials
	}
	s.Kick()
	return nil
}

// ---- server side (the listening device) ----------------------------------------------------------

func (s *Service) caller(w http.ResponseWriter, r *http.Request) (store.Peer, bool) {
	c := peer.PeerFromContext(r.Context())
	if c.Peer == nil {
		writeErr(w, http.StatusForbidden, "NOT_PAIRED", "this device is not paired")
		return store.Peer{}, false
	}
	// the record may have changed since the connection was admitted
	p, found, err := s.db.GetPeer(c.Peer.DeviceID)
	if err != nil || !found || p.State != "paired" {
		writeErr(w, http.StatusForbidden, "NOT_PAIRED", "this device is not paired")
		return store.Peer{}, false
	}
	if !p.VaultSync {
		writeErr(w, http.StatusForbidden, "VAULT_SYNC_OFF", "vault sync is not enabled for this device")
		return store.Peer{}, false
	}
	return p, true
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func (s *Service) serveHead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.caller(w, r); !ok {
		return
	}
	hash, size, present := vault.FileHash(s.dir())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(head{Present: present, SHA256: hash, Size: size})
}

func (s *Service) serveFile(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.caller(w, r); !ok {
		return
	}
	data, hash, ok := fileBytes(s.dir())
	if !ok {
		writeErr(w, http.StatusNotFound, "VAULT_NOT_FOUND", "no vault here")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Vault-SHA256", hash)
	_, _ = w.Write(data)
}

func (s *Service) servePut(w http.ResponseWriter, r *http.Request) {
	p, ok := s.caller(w, r)
	if !ok {
		return
	}
	// One body is read and judged at a time: waiting requests are turned away at once instead of
	// holding up to 64 MiB each in memory.
	select {
	case s.putSem <- struct{}{}:
		defer func() { <-s.putSem }()
	default:
		w.Header().Set("Retry-After", "5")
		writeErr(w, http.StatusTooManyRequests, "RATE_LIMITED", "another upload is being processed")
		return
	}
	s.mu.Lock()
	if last, seen := s.lastPut[p.DeviceID]; seen && s.now().Sub(last) < s.timing.MinPutGap {
		s.mu.Unlock()
		w.Header().Set("Retry-After", "2")
		writeErr(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many uploads")
		return
	}
	s.lastPut[p.DeviceID] = s.now()
	s.mu.Unlock()

	r.Body = http.MaxBytesReader(w, r.Body, MaxFileBytes+1)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, "TOO_LARGE", "too large")
		return
	}
	if !looksLikeKDBX(data) {
		writeErr(w, http.StatusBadRequest, "INVALID", "not a vault file")
		return
	}
	if err := vault.CheckPeerKDF(data); err != nil {
		writeErr(w, http.StatusBadRequest, "INVALID", "key derivation parameters refused")
		return
	}
	if !vault.Exists(s.dir()) {
		s.adopt(p, data, "")
	} else {
		s.receive(p.DeviceID, data, "")
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"accepted":true}`))
}

// ---- status for the shells ----------------------------------------------------------------------------

// PeerStatus is one device in the vault sync list.
type PeerStatus struct {
	DeviceID        string `json:"device_id"`
	Name            string `json:"name"`
	Enabled         bool   `json:"enabled"`
	Connected       bool   `json:"connected"`
	State           string `json:"state"`
	Detail          string `json:"detail"`
	LastOKAt        int64  `json:"last_ok_at"`
	PendingIncoming bool   `json:"pending_incoming"`
	IncomingSHA256  string `json:"incoming_sha256,omitempty"` // what an approval must name
}

// Status lists every paired device with its vault sync state.
func (s *Service) Status() []PeerStatus {
	peers, _ := s.db.ListPeers()
	conn := map[string]bool{}
	for _, st := range s.e.PeerStatuses() {
		conn[st.DeviceID] = st.State == peer.StateConnected
	}
	out := []PeerStatus{}
	for _, p := range peers {
		if p.State != "paired" {
			continue
		}
		row, _, _ := s.db.GetVaultSync(p.DeviceID)
		var sha string
		_, err := os.Stat(s.incomingPath(p.DeviceID))
		if err == nil {
			if b, rerr := os.ReadFile(s.incomingPath(p.DeviceID)); rerr == nil {
				sha = fileSHA(b)
			}
		}
		st := PeerStatus{DeviceID: p.DeviceID, Name: p.Name, Enabled: p.VaultSync, Connected: conn[p.DeviceID], State: row.State,
			Detail: row.Detail, LastOKAt: row.LastOKAt, PendingIncoming: err == nil, IncomingSHA256: sha}
		if !p.VaultSync {
			st.State, st.Detail = StateIdle, ""
		}
		out = append(out, st)
	}
	return out
}

// Forget drops what was kept for a peer (it was unpaired or sync was switched off).
func (s *Service) Forget(peerID string) {
	s.procMu.Lock()
	defer s.procMu.Unlock()
	if p := s.incomingPath(peerID); p != "" {
		_ = os.Remove(p)
	}
	delete(s.failed, peerID)
	delete(s.budget, peerID)
	_ = s.db.PutVaultSync(store.VaultSyncRow{PeerID: peerID})
}
