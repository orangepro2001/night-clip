package pairing

import (
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"nightclip/core/identity"
	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// Tunables, overridable in tests.
type Timings struct {
	SessionTTL  time.Duration // how long a QR code stays valid (5 min)
	ConfirmTTL  time.Duration // how long the user has to confirm a request (120 s)
	KeepResult  time.Duration // how long an accepted/rejected result stays pollable (60 s)
	MaxFailures int           // wrong proofs before a session dies (3)
	MaxOpen     int           // simultaneously open sessions (3)
	RateBurst   int           // pairing requests per source address ...
	RateEvery   time.Duration // ... refilled at one per this interval
}

func (t Timings) withDefaults() Timings {
	if t.SessionTTL <= 0 {
		t.SessionTTL = 5 * time.Minute
	}
	if t.ConfirmTTL <= 0 {
		t.ConfirmTTL = 120 * time.Second
	}
	if t.KeepResult <= 0 {
		t.KeepResult = 60 * time.Second
	}
	if t.MaxFailures <= 0 {
		t.MaxFailures = 3
	}
	if t.MaxOpen <= 0 {
		t.MaxOpen = 3
	}
	if t.RateBurst <= 0 {
		t.RateBurst = 6
	}
	if t.RateEvery <= 0 {
		t.RateEvery = 10 * time.Second
	}
	return t
}

// Pair states reported to the joiner and in events.
const (
	StateWaiting  = "waiting_confirm"
	StateAccepted = "accepted"
	StateRejected = "rejected"
	StateExpired  = "expired"
)

// Manager is the listening side of pairing: it hands out one-time sessions,
// serves POST /v1/pair and GET /v1/pair/{id}, and applies the user's decision.
type Manager struct {
	e       *peer.Engine
	db      *store.DB
	id      *identity.Identity
	emit    func(map[string]any)
	now     func() time.Time
	timings Timings
	limiter *limiter

	mu       sync.Mutex
	sessions map[string]*session // by session id (derived from the psk)
	pending  map[string]*request // by pair_id
}

type session struct {
	sid      string
	psk      []byte
	expires  time.Time
	failures int
	claimed  string // pair_id of the request currently using this session
}

type request struct {
	pairID     string
	sid        string
	psk        []byte // for the proofs sent back to the joiner
	nonce      string
	wasRemoved bool // this fingerprint was unpaired earlier; the user is told
	peer       store.Peer
	fp         string // certificate that made the request; only it may poll
	sas        string
	state      string
	expires    time.Time
	decidedAt  time.Time
}

// NewManager registers the pairing routes on e. Call it before e.Start().
func NewManager(e *peer.Engine, db *store.DB, id *identity.Identity, emit func(map[string]any), now func() time.Time, t Timings) *Manager {
	if now == nil {
		now = time.Now
	}
	if emit == nil {
		emit = func(map[string]any) {}
	}
	t = t.withDefaults()
	m := &Manager{
		e: e, db: db, id: id, emit: emit, now: now, timings: t,
		limiter:  newLimiter(t.RateBurst, t.RateEvery, now),
		sessions: map[string]*session{}, pending: map[string]*request{},
	}
	e.Handle("POST /v1/pair", http.HandlerFunc(m.servePair), true)
	e.Handle("GET /v1/pair/{id}", http.HandlerFunc(m.servePoll), true)
	return m
}

// BeginResult is what the UI needs to show a pairing code.
type BeginResult struct {
	SessionID string    `json:"session_id"`
	Link      string    `json:"link"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Begin opens a pairing session: a fresh psk valid for SessionTTL. It fails with
// NO_TAILNET when this device has no tailnet address, LISTEN_FAILED when it has
// one but cannot bind the port (nothing to connect to either way).
func (m *Manager) Begin() (BeginResult, error) {
	addrs := m.e.ListenAddrs()
	port := m.e.ListenPort()
	if len(addrs) == 0 || port == 0 {
		if err := m.e.ListenError(); err != nil {
			return BeginResult{}, proto.Errorf(proto.CodeListenFailed, "cannot listen on the tailnet address: %v", err)
		}
		return BeginResult{}, proto.Errorf(proto.CodeNoTailnet, "this device has no tailnet address to pair on")
	}
	host := addrs[0]
	for _, a := range addrs { // prefer IPv4: it is what a person can type if scanning fails
		if a.Is4() {
			host = a
			break
		}
	}
	if p := m.e.ListenPortOf(host); p != 0 {
		port = p
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeLocked()
	if len(m.sessions) >= m.timings.MaxOpen {
		return BeginResult{}, proto.Errorf(proto.CodeRateLimited, "too many pairing sessions are open; cancel one first")
	}
	psk := NewPSK()
	s := &session{sid: SessionID(psk), psk: psk, expires: m.now().Add(m.timings.SessionTTL)}
	m.sessions[s.sid] = s
	link := Link{Host: host.String(), Port: port, FP: m.id.Fingerprint(), PSK: psk, Exp: s.expires}
	return BeginResult{SessionID: s.sid, Link: link.String(), Host: link.Host, Port: port, ExpiresAt: s.expires}, nil
}

// Cancel closes a session (and a request waiting on it).
func (m *Manager) Cancel(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sessionID]; ok {
		if s.claimed != "" {
			if r := m.pending[s.claimed]; r != nil && r.state == StateWaiting {
				r.state, r.decidedAt = StateRejected, m.now()
			}
		}
		delete(m.sessions, sessionID)
	}
}

// PendingInfo describes a request waiting for the user.
type PendingInfo struct {
	PairID   string    `json:"pair_id"`
	Name     string    `json:"name"`
	Platform string    `json:"platform"`
	DeviceID string    `json:"device_id"`
	SAS      string    `json:"sas"`
	Expires  time.Time `json:"expires_at"`
}

// Pending lists requests the user still has to confirm.
func (m *Manager) Pending() []PendingInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.purgeLocked()
	var out []PendingInfo
	for _, r := range m.pending {
		if r.state == StateWaiting {
			out = append(out, PendingInfo{r.pairID, r.peer.Name, r.peer.Platform, r.peer.DeviceID, r.sas, r.expires})
		}
	}
	return out
}

// Confirm applies the user's decision on a pending request. Accepting stores the
// peer and starts its link; the psk is spent either way.
func (m *Manager) Confirm(pairID string, accept bool) error {
	m.mu.Lock()
	m.purgeLocked()
	r := m.pending[pairID]
	if r == nil {
		m.mu.Unlock()
		return proto.Errorf(proto.CodePairingExpired, "this pairing request is gone or has expired")
	}
	if r.state == StateExpired {
		m.mu.Unlock()
		return proto.Errorf(proto.CodePairingExpired, "this pairing request has expired")
	}
	if r.state != StateWaiting {
		m.mu.Unlock()
		return proto.Errorf(proto.CodeInvalid, "this pairing request was already answered")
	}
	p := r.peer
	delete(m.sessions, r.sid) // the psk is single-use, whatever the answer
	if !accept {
		r.state, r.decidedAt = StateRejected, m.now()
		m.mu.Unlock()
		m.emit(map[string]any{"type": "pair.result", "pair_id": pairID, "state": StateRejected, "role": "listener"})
		return nil
	}
	m.mu.Unlock()

	// A device that was unpaired earlier may be paired again: the user just confirmed it
	// on purpose, so its fingerprint stops being blocked.
	if err := m.db.RemoveRevokedFP(p.CertFP); err != nil {
		return err
	}

	// Persist before telling the joiner "accepted": the joiner must never believe it is
	// paired while we have forgotten about it.
	p.PairedAt = m.now().Unix()
	p.ApplyMode = m.db.NewPeerApplyMode()
	if err := m.db.PutPeer(p); err != nil {
		return err
	}
	m.mu.Lock()
	r.state, r.decidedAt = StateAccepted, m.now()
	m.mu.Unlock()
	m.e.PeerAdded(p)
	m.emit(map[string]any{"type": "pair.result", "pair_id": pairID, "state": StateAccepted, "role": "listener",
		"device_id": p.DeviceID, "name": p.Name})
	return nil
}

// purgeLocked drops everything that expired. m.mu held.
func (m *Manager) purgeLocked() {
	now := m.now()
	for sid, s := range m.sessions {
		if !now.Before(s.expires) && s.claimed == "" {
			delete(m.sessions, sid)
		}
	}
	for id, r := range m.pending {
		switch {
		case r.state == StateWaiting && !now.Before(r.expires):
			r.state, r.decidedAt = StateExpired, now
			delete(m.sessions, r.sid)
			m.emit(map[string]any{"type": "pair.result", "pair_id": id, "state": StateExpired, "role": "listener"})
		case r.state != StateWaiting && now.Sub(r.decidedAt) > m.timings.KeepResult:
			delete(m.pending, id)
		}
	}
}

// ---- HTTP ----------------------------------------------------------------------

type pairRequest struct {
	SessionID    string   `json:"sid"`
	DeviceID     string   `json:"device_id"`
	Name         string   `json:"name"`
	Platform     string   `json:"platform"`
	CertFP       string   `json:"cert_fp"`
	TailnetAddrs []string `json:"tailnet_addrs"`
	Port         int      `json:"port"`
	Nonce        string   `json:"nonce"`
	Proof        string   `json:"proof"`
}

type pairResponse struct {
	PairID string `json:"pair_id,omitempty"`
	SAS    string `json:"sas,omitempty"`
	State  string `json:"state"`
	// Proof authenticates the listener to the joiner (see ServerProof).
	Proof string `json:"proof,omitempty"`
	// accepted:
	DeviceID     string   `json:"device_id,omitempty"`
	Name         string   `json:"name,omitempty"`
	Platform     string   `json:"platform,omitempty"`
	TailnetAddrs []string `json:"tailnet_addrs,omitempty"`
	Port         int      `json:"port,omitempty"`
}

func fail(w http.ResponseWriter, status int, code proto.Code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg, "retryable": false}})
}

func (m *Manager) servePair(w http.ResponseWriter, r *http.Request) {
	caller := peer.PeerFromContext(r.Context())
	ip := remoteIP(r)
	if !m.limiter.allow(ip) {
		w.Header().Set("Retry-After", "10")
		fail(w, http.StatusTooManyRequests, proto.CodeRateLimited, "too many pairing attempts; wait a moment")
		return
	}
	var req pairRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
	if dec.Decode(&req) != nil {
		fail(w, http.StatusBadRequest, proto.CodeInvalid, "malformed pairing request")
		return
	}
	// The certificate on this TLS connection is the identity being introduced; the
	// body may not claim a different one.
	cert := r.TLS.PeerCertificates[0]
	if req.CertFP != caller.Fingerprint || req.DeviceID != identity.DeviceID(cert.RawSubjectPublicKeyInfo) ||
		!proto.ValidDeviceID(req.DeviceID) || req.DeviceID == m.id.DeviceID {
		fail(w, http.StatusForbidden, proto.CodePairingFailed, "pairing failed")
		return
	}
	if req.Name == "" || len(req.Name) > 64 || !proto.KnownPlatform(req.Platform) || len(req.Nonce) < 16 || len(req.Nonce) > 64 {
		fail(w, http.StatusBadRequest, proto.CodeInvalid, "malformed pairing request")
		return
	}
	removedBefore, _ := m.db.IsRevoked(req.CertFP)

	m.mu.Lock()
	m.purgeLocked()
	s := m.sessions[req.SessionID]
	if s == nil {
		m.mu.Unlock()
		fail(w, http.StatusForbidden, proto.CodePairingFailed, "pairing failed")
		return
	}
	if !m.now().Before(s.expires) {
		delete(m.sessions, s.sid)
		m.mu.Unlock()
		fail(w, http.StatusGone, proto.CodePairingExpired, "this pairing code has expired")
		return
	}
	if !ProofValid([]byte(req.Proof), s.psk, req.DeviceID, req.CertFP, m.id.Fingerprint(), req.Nonce) {
		s.failures++
		if s.failures >= m.timings.MaxFailures {
			delete(m.sessions, s.sid)
		}
		m.mu.Unlock()
		fail(w, http.StatusForbidden, proto.CodePairingFailed, "pairing failed")
		return
	}
	if s.claimed != "" {
		if prev := m.pending[s.claimed]; prev != nil && prev.state == StateWaiting {
			m.mu.Unlock()
			fail(w, http.StatusConflict, proto.CodePairingFailed, "this pairing code is already in use")
			return
		}
	}

	addrs := []string{}
	if ap, ok := remoteAddr(r); ok {
		addrs = append(addrs, ap.String())
	}
	for _, a := range req.TailnetAddrs {
		if pa, err := netip.ParseAddr(a); err == nil && m.e.AddrAllowed(pa) {
			addrs = append(addrs, pa.Unmap().String())
		}
	}
	port := 0
	if req.Port > 0 && req.Port < 65536 {
		port = req.Port
	}
	rq := &request{
		pairID: randomID(), sid: s.sid, fp: req.CertFP, state: StateWaiting, psk: s.psk, nonce: req.Nonce, wasRemoved: removedBefore,
		sas:     SAS(m.id.Fingerprint(), req.CertFP, req.Nonce),
		expires: m.now().Add(m.timings.ConfirmTTL),
		peer: store.Peer{
			DeviceID: req.DeviceID, Name: req.Name, Platform: req.Platform, CertFP: req.CertFP,
			TailnetAddrs: dedupe(addrs), Port: port, Role: "auto", State: "paired", ApplyMode: "notify", VaultSync: true,
		},
	}
	m.pending[rq.pairID] = rq
	s.claimed = rq.pairID
	m.mu.Unlock()

	m.emit(map[string]any{"type": "pair.request", "pair_id": rq.pairID, "name": req.Name, "platform": req.Platform,
		"device_id": req.DeviceID, "sas": rq.sas, "expires_at": rq.expires.UTC().Format(time.RFC3339),
		"previously_removed": removedBefore})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(pairResponse{
		PairID: rq.pairID, SAS: rq.sas, State: StateWaiting,
		Proof: ServerProof(rq.psk, StateWaiting, m.id.Fingerprint(), rq.fp, rq.nonce, rq.sas, m.id.DeviceID),
	})
}

func (m *Manager) servePoll(w http.ResponseWriter, r *http.Request) {
	caller := peer.PeerFromContext(r.Context())
	m.mu.Lock()
	m.purgeLocked()
	rq := m.pending[r.PathValue("id")]
	if rq == nil || rq.fp != caller.Fingerprint {
		m.mu.Unlock()
		fail(w, http.StatusNotFound, proto.CodePairingExpired, "unknown or expired pairing request")
		return
	}
	resp := pairResponse{PairID: rq.pairID, State: rq.state,
		Proof: ServerProof(rq.psk, rq.state, m.id.Fingerprint(), rq.fp, rq.nonce, rq.sas, m.id.DeviceID)}
	m.mu.Unlock()
	if resp.State == StateAccepted {
		resp.DeviceID, resp.Name, resp.Platform = m.id.DeviceID, m.id.Name, platformOf(m.e)
		for _, a := range m.e.ListenAddrs() {
			resp.TailnetAddrs = append(resp.TailnetAddrs, a.String())
		}
		resp.Port = m.e.ListenPort()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func platformOf(e *peer.Engine) string { return e.Platform() }

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return b64.EncodeToString(b[:])
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func remoteAddr(r *http.Request) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	return ap.Addr().Unmap(), true
}

func remoteIP(r *http.Request) string {
	if a, ok := remoteAddr(r); ok {
		return a.String()
	}
	return r.RemoteAddr
}
