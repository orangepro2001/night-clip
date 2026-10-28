package peer

import (
	"context"
	"crypto/tls"
	"net/netip"
	"sync"
	"time"

	"nightclip/core/identity"
	"nightclip/core/proto"
	"nightclip/core/store"
)

const (
	// Subprotocol is the WebSocket subprotocol of the event channel.
	Subprotocol = "clipbridge.v1"
	// DefaultPort is the default listening port (docs/api-spec.md §1.1).
	DefaultPort = 47821
	// MaxFrameBytes is the largest WebSocket message either side accepts. The
	// spec's 64 KiB is for clipboard *text*; a worst-case JSON escaping of 64 KiB
	// of control characters is ~6x that, so frames get generous headroom.
	MaxFrameBytes = 512 << 10
	// DefaultTTL is the lifetime of a clipboard event (docs/architecture.md §4.5).
	DefaultTTL = 15 * time.Minute
	// maxReorder is how many out-of-order events a link buffers before it asks
	// the sender to resync.
	maxReorder = 64
)

// Handler receives what the peer layer accepted. Calls for one peer arrive in
// sequence order; they must not block for long.
type Handler interface {
	// OnEvent is called once per new, valid, in-order event that was stored in
	// the inbox and acknowledged as "received".
	OnEvent(peerID string, env *proto.Envelope)
	// OnAck is called when peerID acknowledged one of our events.
	OnAck(peerID, eventID, result, reason string)
	// OnPeerState is called when a link changes state (see State* constants).
	OnPeerState(peerID, state string)
}

// Link states reported to Handler.OnPeerState and in PeerStatus.
const (
	StateOffline    = "offline"
	StateConnecting = "connecting"
	StateConnected  = "connected"
	StateUnpaired   = "unpaired" // the other side no longer knows us: re-pair
	StateRevoked    = "revoked"
)

// Hooks are test-only switches. They are set in Go code, never from JSON.
type Hooks struct {
	AllowLoopback bool                // treat 127.0.0.0/8 and ::1 as tailnet
	RandomPort    bool                // bind port 0 (the OS picks) instead of Config.Port
	ListenAddrs   func() []netip.Addr // replaces TailnetAddrs()
}

// Timing holds every interval; zero values take the defaults below.
type Timing struct {
	Ping        time.Duration // 25 s
	Idle        time.Duration // 60 s without any frame: the link is dead
	Handshake   time.Duration // 10 s for hello / hello_ack
	DialTimeout time.Duration // 5 s per address
	BackoffBase time.Duration // 1 s
	BackoffMax  time.Duration // 60 s
	StableAfter time.Duration // 10 s: a link that lived this long resets the backoff
	Rebind      time.Duration // 5 s between listener address checks
	Sweep       time.Duration // 30 s between expiry sweeps
}

func (t Timing) withDefaults() Timing {
	def := func(v *time.Duration, d time.Duration) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&t.Ping, 25*time.Second)
	def(&t.Idle, 60*time.Second)
	def(&t.Handshake, 10*time.Second)
	def(&t.DialTimeout, 5*time.Second)
	def(&t.BackoffBase, time.Second)
	def(&t.BackoffMax, 60*time.Second)
	def(&t.StableAfter, 10*time.Second)
	def(&t.Rebind, 5*time.Second)
	def(&t.Sweep, 30*time.Second)
	return t
}

// Config configures an Engine.
type Config struct {
	Identity *identity.Identity
	Store    *store.DB
	Listen   bool   // Windows: accept connections on tailnet addresses; Android: dial only
	Port     int    // 0 = DefaultPort (tests pass a port and read it back with ListenPort)
	Platform string // "windows" | "android" | "macos"
	Handler  Handler
	// Emit receives engine events as JSON-ready maps (core.warning, ...).
	Emit   func(map[string]any)
	Now    func() time.Time
	Timing Timing
	Hooks  Hooks
}

// Engine owns the listener, the dialers and one link per paired peer.
type Engine struct {
	cfg    Config
	id     *identity.Identity
	db     *store.DB
	hooks  Hooks
	timing Timing
	tlsCfg *tls.Config

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	links     map[string]*link
	listeners map[netip.Addr]*listener
	routes    []route
	started   bool
	closed    bool
	noTailnet bool // a NO_TAILNET warning is currently active
}

// New builds an engine; call Handle for extra routes, then Start.
func New(cfg Config) *Engine {
	if cfg.Port == 0 && !cfg.Hooks.RandomPort {
		cfg.Port = DefaultPort
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Emit == nil {
		cfg.Emit = func(map[string]any) {}
	}
	e := &Engine{
		cfg: cfg, id: cfg.Identity, db: cfg.Store, hooks: cfg.Hooks,
		timing: cfg.Timing.withDefaults(),
		links:  map[string]*link{}, listeners: map[netip.Addr]*listener{},
	}
	e.tlsCfg = ServerTLS(cfg.Identity)
	return e
}

func (e *Engine) now() time.Time { return e.cfg.Now() }

// Platform returns "windows", "android" or "macos" as configured.
func (e *Engine) Platform() string { return e.cfg.Platform }

func (e *Engine) emit(ev map[string]any) { e.cfg.Emit(ev) }

// Start begins listening (if configured) and dialing every paired peer.
func (e *Engine) Start() error {
	e.mu.Lock()
	if e.started || e.closed {
		e.mu.Unlock()
		return proto.Errorf(proto.CodeInvalid, "engine already started")
	}
	e.started = true
	e.ctx, e.cancel = context.WithCancel(context.Background())
	e.mu.Unlock()

	peers, err := e.db.ListPeers()
	if err != nil {
		return err
	}
	for _, p := range peers {
		if p.State == "paired" {
			e.startPeer(p)
		}
	}
	if e.cfg.Listen {
		e.wg.Add(1)
		go e.listenLoop()
	}
	e.wg.Add(1)
	go e.sweepLoop()
	return nil
}

// Close stops everything and waits for the goroutines.
func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	cancel := e.cancel
	links := make([]*link, 0, len(e.links))
	for _, l := range e.links {
		links = append(links, l)
	}
	listeners := make([]*listener, 0, len(e.listeners))
	for _, l := range e.listeners {
		listeners = append(listeners, l)
	}
	e.listeners = map[netip.Addr]*listener{}
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, l := range links {
		l.closeSession("shutdown", 1000)
	}
	for _, l := range listeners {
		l.close()
	}
	e.wg.Wait()
}

// sweepLoop expires and purges old rows.
func (e *Engine) sweepLoop() {
	defer e.wg.Done()
	t := time.NewTicker(e.timing.Sweep)
	defer t.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-t.C:
			e.sweep()
		}
	}
}

func (e *Engine) sweep() {
	now := e.now().Unix()
	if n, err := e.db.ExpireOutbox(now); err == nil && n > 0 {
		e.emit(map[string]any{"type": "outbox.expired", "count": n})
	}
	_, _ = e.db.ExpireInbox(now)
	_, _ = e.db.PurgeOutbox(now - int64((24 * time.Hour).Seconds()))
}
