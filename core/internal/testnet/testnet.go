// Package testnet builds small, real device networks for tests: each Device has
// its own database, identity and peer.Engine, talking mutual TLS over loopback.
// It is imported by _test files only.
package testnet

import (
	"net/netip"
	"sync"
	"testing"
	"time"

	"nightclip/core/identity"
	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// Clock is a manually advanced clock shared by the devices of one test.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

func NewClock() *Clock { return &Clock{t: time.Now().UTC().Truncate(time.Second)} }

func (c *Clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// Proxy is a peer.Handler whose target is set after the engine exists (the
// engine needs a handler to be built, the handler needs the engine).
type Proxy struct {
	mu sync.Mutex
	H  peer.Handler
}

func (p *Proxy) Set(h peer.Handler) { p.mu.Lock(); p.H = h; p.mu.Unlock() }
func (p *Proxy) get() peer.Handler  { p.mu.Lock(); defer p.mu.Unlock(); return p.H }
func (p *Proxy) OnEvent(peerID string, env *proto.Envelope) {
	if h := p.get(); h != nil {
		h.OnEvent(peerID, env)
	}
}
func (p *Proxy) OnAck(peerID, eventID, result, reason string) {
	if h := p.get(); h != nil {
		h.OnAck(peerID, eventID, result, reason)
	}
}
func (p *Proxy) OnPeerState(peerID, state string) {
	if h := p.get(); h != nil {
		h.OnPeerState(peerID, state)
	}
}

// Device is one simulated phone or PC.
type Device struct {
	T        *testing.T
	Name     string
	Platform string
	ID       *identity.Identity
	DB       *store.DB
	Engine   *peer.Engine
	Proxy    *Proxy
	Clock    *Clock

	evMu   sync.Mutex
	events []map[string]any
}

var Loopback = []netip.Addr{netip.MustParseAddr("127.0.0.1")}

// FastTiming makes every engine interval short.
var FastTiming = peer.Timing{
	Ping: 100 * time.Millisecond, Idle: 700 * time.Millisecond, Handshake: 2 * time.Second,
	DialTimeout: time.Second, BackoffBase: 40 * time.Millisecond, BackoffMax: 320 * time.Millisecond,
	StableAfter: 200 * time.Millisecond, Rebind: 30 * time.Millisecond, Sweep: time.Hour,
}

// New creates a device but does not start its engine, so callers can register
// routes first. extra, if non-nil, may adjust the engine config.
func New(t *testing.T, name, platform string, listen bool, clock *Clock, extra func(*peer.Config)) *Device {
	t.Helper()
	key := make([]byte, 32)
	copy(key, name)
	db, err := store.Open(t.TempDir(), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	id, err := db.LoadOrCreateIdentity(name, platform)
	if err != nil {
		t.Fatal(err)
	}
	d := &Device{T: t, Name: name, Platform: platform, ID: id, DB: db, Proxy: &Proxy{}, Clock: clock}
	cfg := peer.Config{
		Identity: id, Store: db, Listen: listen, Platform: platform, Handler: d.Proxy, Timing: FastTiming,
		Emit:  func(ev map[string]any) { d.evMu.Lock(); d.events = append(d.events, ev); d.evMu.Unlock() },
		Hooks: peer.Hooks{AllowLoopback: true, RandomPort: true, ListenAddrs: func() []netip.Addr { return Loopback }},
	}
	if clock != nil {
		cfg.Now = clock.Now
	}
	if extra != nil {
		extra(&cfg)
	}
	d.Engine = peer.New(cfg)
	t.Cleanup(d.Engine.Close)
	return d
}

// Emit lets tests (and services under test) record events like the engine does.
func (d *Device) Emit(ev map[string]any) {
	d.evMu.Lock()
	d.events = append(d.events, ev)
	d.evMu.Unlock()
}

// Events returns a copy of everything emitted so far.
func (d *Device) Events() []map[string]any {
	d.evMu.Lock()
	defer d.evMu.Unlock()
	return append([]map[string]any(nil), d.events...)
}

// Find returns the first emitted event of the given type matching all fields.
func (d *Device) Find(typ string, match map[string]any) map[string]any {
	d.evMu.Lock()
	defer d.evMu.Unlock()
	for _, ev := range d.events {
		if ev["type"] != typ {
			continue
		}
		ok := true
		for k, v := range match {
			if ev[k] != v {
				ok = false
			}
		}
		if ok {
			return ev
		}
	}
	return nil
}

// Count counts emitted events of a type.
func (d *Device) Count(typ string) int {
	d.evMu.Lock()
	defer d.evMu.Unlock()
	n := 0
	for _, ev := range d.events {
		if ev["type"] == typ {
			n++
		}
	}
	return n
}

// Start starts the engine and, for listeners, waits until it is listening.
func (d *Device) Start(listen bool) {
	d.T.Helper()
	if err := d.Engine.Start(); err != nil {
		d.T.Fatal(err)
	}
	if listen {
		WaitUntil(d.T, d.Name+" to listen", func() bool { return d.Engine.ListenPort() != 0 })
	}
}

// Know stores other as a paired peer of d (as pairing would). port is where
// other listens, 0 if d never dials it.
func (d *Device) Know(other *Device, port int, applyMode string) store.Peer {
	d.T.Helper()
	if applyMode == "" {
		applyMode = "notify"
	}
	p := store.Peer{
		DeviceID: other.ID.DeviceID, Name: other.Name, Platform: other.Platform, CertFP: other.ID.Fingerprint(),
		TailnetAddrs: []string{"127.0.0.1"}, Port: port, Role: "auto", State: "paired", ApplyMode: applyMode,
		PairedAt: time.Now().Unix(),
	}
	if err := d.DB.PutPeer(p); err != nil {
		d.T.Fatal(err)
	}
	return p
}

// State returns the engine's view of the link to other.
func (d *Device) State(other *Device) string {
	for _, s := range d.Engine.PeerStatuses() {
		if s.DeviceID == other.ID.DeviceID {
			return s.State
		}
	}
	return ""
}

// WaitState waits for the link to other to reach a state.
func (d *Device) WaitState(other *Device, want string) {
	d.T.Helper()
	WaitUntil(d.T, d.Name+": link to "+other.Name+" is "+want, func() bool { return d.State(other) == want })
}

// Connect pairs a listening device with a dialing one (both directions stored)
// and starts both engines. The Windows side must not be started yet.
func Connect(t *testing.T, win, pix *Device, winMode, pixMode string) {
	t.Helper()
	win.Know(pix, 0, winMode)
	win.Start(true)
	pix.Know(win, win.Engine.ListenPort(), pixMode)
	pix.Start(false)
	pix.WaitState(win, peer.StateConnected)
	win.WaitState(pix, peer.StateConnected)
}

// WaitUntil polls cond for up to 6 seconds.
func WaitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}
