package sshagent

import (
	"crypto/rand"
	"errors"
	"io"
	"sort"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Proc is one process in the chain that made a request (the client first,
// then its parents). It is for display only: a same-user process can name
// itself anything.
type Proc struct {
	PID  uint32 `json:"pid"`
	Name string `json:"name"`
	Path string `json:"path,omitempty"`
}

// Results and refusal reasons in Use.
const (
	ResultSigned  = "signed"
	ResultRefused = "refused"

	RefusedForwarding = "forwarding"  // the request came through ssh -A and forwarding is off
	RefusedUnknownKey = "unknown_key" // the key is not (or no longer) loaded
	RefusedNoKeys     = "no_keys"     // nothing is loaded at all
	RefusedHost       = "host"        // the key is bound to other hosts than the one the client talks to
	RefusedError      = "error"       // signing failed
)

// Use describes one signing request.
type Use struct {
	At          time.Time
	Fingerprint string
	Title       string
	EntryID     string
	Client      []Proc
	HostKey     ssh.PublicKey // the server, when the client told us (session-bind or hostbound userauth)
	User        string
	Kind        string // KindUserauth, KindSSHSig, KindOther
	Namespace   string
	Forwarded   bool
	Result      string
	Reason      string
}

// Hooks connect the server to the rest of the core. All are optional and
// must not block for long: they run on the client's connection.
type Hooks struct {
	AllowForwarding func() bool
	OnUse           func(Use)
	OnKeysNeeded    func(client []Proc)
	Client          func(conn io.ReadWriteCloser) []Proc
	Now             func() time.Time
	// KnownHosts maps a server's host key to names for keys bound to hosts
	// (Key.Hosts). Nil means only fingerprint patterns can match.
	KnownHosts *KnownHosts
}

// Listener is what the server accepts connections from (core/pipe's
// listeners satisfy it).
type Listener interface {
	Accept() (io.ReadWriteCloser, error)
	Close() error
}

// MaxConnections caps concurrent clients; more are closed on accept.
const MaxConnections = 64

const maxBindsPerConn = 16

var errRefused = errors.New("sshagent: refused")

// Server accepts agent connections.
type Server struct {
	l     Listener
	ring  *Keyring
	hooks Hooks

	mu     sync.Mutex
	conns  map[io.ReadWriteCloser]struct{}
	closed bool
	wg     sync.WaitGroup

	// hostKeys remembers, per server host key, which keys signed for it, so
	// List can offer those first (in memory only).
	hostMu   sync.Mutex
	hostKeys map[string]map[string]uint64
	useSeq   uint64
}

// Serve starts accepting on l in the background.
func Serve(l Listener, ring *Keyring, h Hooks) *Server {
	if h.Now == nil {
		h.Now = time.Now
	}
	s := &Server{l: l, ring: ring, hooks: h, conns: map[io.ReadWriteCloser]struct{}{}, hostKeys: map[string]map[string]uint64{}}
	s.wg.Add(1)
	go s.acceptLoop()
	return s
}

// Connections returns the number of connected clients.
func (s *Server) Connections() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.conns)
}

// Close stops accepting, disconnects every client and waits for them.
func (s *Server) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	conns := make([]io.ReadWriteCloser, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	_ = s.l.Close()
	for _, c := range conns {
		_ = c.Close()
	}
	s.wg.Wait()
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.l.Accept()
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			if conn != nil {
				_ = conn.Close()
			}
			return
		}
		if err != nil {
			time.Sleep(50 * time.Millisecond)
			continue
		}
		s.mu.Lock()
		if len(s.conns) >= MaxConnections || s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.conns[conn] = struct{}{}
		s.wg.Add(1)
		s.mu.Unlock()
		go s.serveConn(conn)
	}
}

func (s *Server) serveConn(conn io.ReadWriteCloser) {
	defer s.wg.Done()
	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()
	defer func() { _ = recover() }() // a bug in one connection must not take the core down
	_ = agent.ServeAgent(&connAgent{s: s, conn: conn}, conn)
}

// connAgent is the agent one client connection talks to.
type connAgent struct {
	s     *Server
	conn  io.ReadWriteCloser
	once  sync.Once
	procs []Proc
	binds []sessionBind
}

var _ agent.ExtendedAgent = (*connAgent)(nil)

func (a *connAgent) client() []Proc {
	a.once.Do(func() {
		if a.s.hooks.Client != nil {
			a.procs = a.s.hooks.Client(a.conn)
		}
	})
	return a.procs
}

func (a *connAgent) forwarded() bool {
	for _, b := range a.binds {
		if b.Forwarding {
			return true
		}
	}
	return false
}

func (a *connAgent) boundHost() ssh.PublicKey {
	if len(a.binds) == 0 {
		return nil
	}
	return a.binds[len(a.binds)-1].HostKey
}

func (a *connAgent) forwardingAllowed() bool {
	return a.s.hooks.AllowForwarding != nil && a.s.hooks.AllowForwarding()
}

func (a *connAgent) List() ([]*agent.Key, error) {
	if a.forwarded() && !a.forwardingAllowed() {
		return []*agent.Key{}, nil
	}
	keys, _, _ := a.s.ring.Snapshot()
	if len(keys) == 0 {
		if a.s.hooks.OnKeysNeeded != nil {
			a.s.hooks.OnKeysNeeded(a.client())
		}
		return []*agent.Key{}, nil
	}
	if hk := a.boundHost(); hk != nil {
		// The client said which server it talks to: offer only the keys meant for it, so a
		// server that allows few attempts is not tried with every key first.
		keys = a.s.forHost(keys, hk)
		used := a.s.usedFor(hk)
		sort.SliceStable(keys, func(i, j int) bool {
			return used[keys[i].Fingerprint] > used[keys[j].Fingerprint]
		})
	}
	out := make([]*agent.Key, 0, len(keys))
	for _, k := range keys {
		comment := k.Title
		if comment == "" {
			comment = k.Comment
		}
		out = append(out, &agent.Key{Format: k.Public.Type(), Blob: k.Public.Marshal(), Comment: comment})
	}
	return out, nil
}

func (a *connAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	return a.SignWithFlags(key, data, 0)
}

func (a *connAgent) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	req := classify(data)
	use := Use{
		At: a.s.hooks.Now(), Client: a.client(), Kind: req.Kind, User: req.User, Namespace: req.Namespace,
		Forwarded: a.forwarded(), HostKey: req.HostKey, Fingerprint: ssh.FingerprintSHA256(key),
	}
	if use.HostKey == nil {
		use.HostKey = a.boundHost()
	}
	refuse := func(reason string) (*ssh.Signature, error) {
		use.Result, use.Reason = ResultRefused, reason
		a.s.report(use)
		return nil, errRefused
	}
	if use.Forwarded && !a.forwardingAllowed() {
		return refuse(RefusedForwarding)
	}
	k := a.s.ring.Find(key.Marshal())
	if k == nil {
		if keys, _, _ := a.s.ring.Snapshot(); len(keys) == 0 {
			return refuse(RefusedNoKeys)
		}
		return refuse(RefusedUnknownKey)
	}
	use.Title, use.EntryID = k.Title, k.EntryID
	if len(k.Hosts) > 0 && req.Kind == KindUserauth && use.HostKey != nil && !a.s.hostAllowed(k, use.HostKey) {
		return refuse(RefusedHost)
	}

	sig, err := k.sign(data, flags)
	if err != nil {
		return refuse(RefusedError)
	}
	if use.HostKey != nil {
		a.s.markUsed(use.HostKey, k.Fingerprint)
	}
	use.Result = ResultSigned
	a.s.report(use)
	return sig, nil
}

// sign protects private material from being wiped during an in-flight signature.
func (k *Key) sign(data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	k.useMu.RLock()
	defer k.useMu.RUnlock()
	if k.revoked {
		return nil, errRefused
	}
	var sig *ssh.Signature
	var err error
	if k.Public.Type() == ssh.KeyAlgoRSA {
		algo := ssh.KeyAlgoRSA
		switch {
		case flags&agent.SignatureFlagRsaSha512 != 0:
			algo = ssh.KeyAlgoRSASHA512
		case flags&agent.SignatureFlagRsaSha256 != 0:
			algo = ssh.KeyAlgoRSASHA256
		}
		as, ok := k.Signer.(ssh.AlgorithmSigner)
		if !ok {
			return nil, errRefused
		}
		sig, err = as.SignWithAlgorithm(rand.Reader, data, algo)
	} else {
		sig, err = k.Signer.Sign(rand.Reader, data)
	}
	return sig, err
}

func (a *connAgent) Extension(extensionType string, contents []byte) ([]byte, error) {
	if extensionType != "session-bind@openssh.com" {
		return nil, agent.ErrExtensionUnsupported
	}
	if len(a.binds) >= maxBindsPerConn {
		return nil, errRefused
	}
	b, err := parseSessionBind(contents)
	if err != nil {
		return nil, err
	}
	a.binds = append(a.binds, b)
	return nil, nil
}

func (a *connAgent) Add(agent.AddedKey) error       { return errRefused }
func (a *connAgent) Remove(ssh.PublicKey) error     { return errRefused }
func (a *connAgent) RemoveAll() error               { return errRefused }
func (a *connAgent) Lock([]byte) error              { return errRefused }
func (a *connAgent) Unlock([]byte) error            { return errRefused }
func (a *connAgent) Signers() ([]ssh.Signer, error) { return nil, errRefused }

// forHost keeps the keys that are not bound to hosts, or are bound to hk.
func (s *Server) forHost(keys []*Key, hk ssh.PublicKey) []*Key {
	out := keys[:0:0]
	for _, k := range keys {
		if len(k.Hosts) == 0 || s.hostAllowed(k, hk) {
			out = append(out, k)
		}
	}
	return out
}

func (s *Server) hostAllowed(k *Key, hk ssh.PublicKey) bool {
	if s.hooks.KnownHosts == nil {
		return (*KnownHosts)(nil).Matches(hk, k.Hosts)
	}
	return s.hooks.KnownHosts.Matches(hk, k.Hosts)
}

func (s *Server) report(u Use) {
	if s.hooks.OnUse != nil {
		s.hooks.OnUse(u)
	}
}

func (s *Server) usedFor(hk ssh.PublicKey) map[string]uint64 {
	s.hostMu.Lock()
	defer s.hostMu.Unlock()
	out := map[string]uint64{}
	for fp, t := range s.hostKeys[ssh.FingerprintSHA256(hk)] {
		out[fp] = t
	}
	return out
}

// markUsed records that a key signed for a host; later uses rank higher.
func (s *Server) markUsed(hk ssh.PublicKey, keyFP string) {
	s.hostMu.Lock()
	defer s.hostMu.Unlock()
	h := ssh.FingerprintSHA256(hk)
	if s.hostKeys[h] == nil {
		if len(s.hostKeys) >= 1024 {
			s.hostKeys = map[string]map[string]uint64{}
		}
		s.hostKeys[h] = map[string]uint64{}
	}
	s.useSeq++
	s.hostKeys[h][keyFP] = s.useSeq
}
