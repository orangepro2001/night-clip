package peer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"sync"
	"testing"
	"time"

	"nightclip/core/identity"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// fastTiming makes every interval short enough for tests to run in milliseconds.
var fastTiming = Timing{
	Ping: 100 * time.Millisecond, Idle: 700 * time.Millisecond, Handshake: 2 * time.Second,
	DialTimeout: time.Second, BackoffBase: 40 * time.Millisecond, BackoffMax: 320 * time.Millisecond,
	StableAfter: 200 * time.Millisecond, Rebind: 30 * time.Millisecond, Sweep: time.Hour,
}

var loopback = []netip.Addr{netip.MustParseAddr("127.0.0.1")}

// fakeClock is a manually advanced clock shared by the engines of one test.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock { return &fakeClock{t: time.Now().UTC().Truncate(time.Second)} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// recorder is the Handler of a test node.
type recorder struct {
	mu     sync.Mutex
	events []*proto.Envelope
	acks   []ackRec
	states []string
}

type ackRec struct{ peer, event, result, reason string }

func (r *recorder) OnEvent(peerID string, env *proto.Envelope) {
	r.mu.Lock()
	r.events = append(r.events, env)
	r.mu.Unlock()
}
func (r *recorder) OnAck(peerID, eventID, result, reason string) {
	r.mu.Lock()
	r.acks = append(r.acks, ackRec{peerID, eventID, result, reason})
	r.mu.Unlock()
}
func (r *recorder) OnPeerState(peerID, state string) {
	r.mu.Lock()
	r.states = append(r.states, peerID+":"+state)
	r.mu.Unlock()
}
func (r *recorder) eventCount() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.events) }
func (r *recorder) eventIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, e := range r.events {
		out = append(out, e.EventID)
	}
	return out
}
func (r *recorder) ackFor(event string) (ackRec, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, a := range r.acks {
		if a.event == event {
			return a, true
		}
	}
	return ackRec{}, false
}
func (r *recorder) ackCount(event string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, a := range r.acks {
		if a.event == event {
			n++
		}
	}
	return n
}

// node is one device: identity, database, engine, recorder.
type node struct {
	t      *testing.T
	name   string
	id     *identity.Identity
	db     *store.DB
	e      *Engine
	rec    *recorder
	clock  *fakeClock
	emitMu sync.Mutex
	emits  []map[string]any
}

type nodeOpt func(*Config)

func newNode(t *testing.T, name, platform string, listen bool, clock *fakeClock, opts ...nodeOpt) *node {
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
	n := &node{t: t, name: name, id: id, db: db, rec: &recorder{}, clock: clock}
	cfg := Config{
		Identity: id, Store: db, Listen: listen, Platform: platform, Handler: n.rec, Timing: fastTiming,
		Emit:  func(ev map[string]any) { n.emitMu.Lock(); n.emits = append(n.emits, ev); n.emitMu.Unlock() },
		Hooks: Hooks{AllowLoopback: true, ListenAddrs: func() []netip.Addr { return loopback }},
	}
	if clock != nil {
		cfg.Now = clock.Now
	}
	for _, o := range opts {
		o(&cfg)
	}
	n.e = New(cfg)
	t.Cleanup(n.e.Close)
	return n
}

func (n *node) start() {
	n.t.Helper()
	if err := n.e.Start(); err != nil {
		n.t.Fatal(err)
	}
}

func (n *node) emitted(typ, code string) int {
	n.emitMu.Lock()
	defer n.emitMu.Unlock()
	c := 0
	for _, ev := range n.emits {
		if ev["type"] == typ && (code == "" || ev["code"] == code) {
			c++
		}
	}
	return c
}

// know records `other` as a paired peer of n. port is where other listens
// (0 when n never dials it).
func (n *node) know(other *node, port int, role string) store.Peer {
	n.t.Helper()
	if role == "" {
		role = "auto"
	}
	p := store.Peer{
		DeviceID: other.id.DeviceID, Name: other.name, Platform: platformOf(other), CertFP: other.id.Fingerprint(),
		TailnetAddrs: []string{"127.0.0.1"}, Port: port, Role: role, State: "paired", ApplyMode: "notify",
		PairedAt: time.Now().Unix(),
	}
	if err := n.db.PutPeer(p); err != nil {
		n.t.Fatal(err)
	}
	return p
}

func platformOf(n *node) string {
	if n.e.cfg.Platform != "" {
		return n.e.cfg.Platform
	}
	return "windows"
}

func (n *node) linkState(peerID string) string {
	l := n.e.getLink(peerID)
	return l.currentState()
}

func (n *node) waitState(peerID, want string) {
	n.t.Helper()
	waitUntil(n.t, fmt.Sprintf("%s: link to %s is %s (now %s)", n.name, peerID[:8], want, n.linkState(peerID)),
		func() bool { return n.linkState(peerID) == want })
}

func waitUntil(t *testing.T, what string, cond func() bool) {
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

func clip(text string) map[string]any {
	return map[string]any{"mime_type": proto.MimeText, "content": text, "size": len(text)}
}

func (n *node) sendClip(to *node, text string) string {
	n.t.Helper()
	id, err := n.e.Send(to.id.DeviceID, proto.KindClipboard, clip(text), SendOptions{})
	if err != nil {
		n.t.Fatalf("%s.Send: %v", n.name, err)
	}
	return id
}

// pairWin sets up the usual topology: a Windows listener and an Android dialer
// that know each other. It returns started nodes once the link is up.
func pairWin(t *testing.T, clock *fakeClock) (win, android *node) {
	t.Helper()
	win = newNode(t, "win", "windows", true, clock)
	android = newNode(t, "pixel", "android", false, clock)
	win.know(android, 0, "")
	win.start()
	waitUntil(t, "windows to listen", func() bool { return win.e.ListenPort() != 0 })
	android.know(win, win.e.ListenPort(), "")
	android.start()
	android.waitState(win.id.DeviceID, StateConnected)
	win.waitState(android.id.DeviceID, StateConnected)
	return win, android
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ctxTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
