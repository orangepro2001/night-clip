package pairing

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"nightclip/core/identity"
	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
)

var loopback = []netip.Addr{netip.MustParseAddr("127.0.0.1")}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type noopHandler struct {
	mu     sync.Mutex
	events []string
}

func (h *noopHandler) OnEvent(peerID string, env *proto.Envelope) {
	var p proto.ClipboardPayload
	_ = json.Unmarshal(env.Payload, &p)
	h.mu.Lock()
	h.events = append(h.events, p.Content)
	h.mu.Unlock()
}
func (h *noopHandler) OnAck(string, string, string, string) {}
func (h *noopHandler) OnPeerState(string, string)           {}
func (h *noopHandler) got() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.events...)
}

// dev is one device with a pairing manager.
type dev struct {
	t        *testing.T
	name     string
	platform string
	id       *identity.Identity
	db       *store.DB
	e        *peer.Engine
	m        *Manager
	h        *noopHandler
	clk      *clock

	evMu   sync.Mutex
	events []map[string]any
}

func newDev(t *testing.T, name, platform string, listen bool, clk *clock, tm Timings) *dev {
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
	d := &dev{t: t, name: name, platform: platform, id: id, db: db, h: &noopHandler{}, clk: clk}
	emit := func(ev map[string]any) { d.evMu.Lock(); d.events = append(d.events, ev); d.evMu.Unlock() }
	d.e = peer.New(peer.Config{
		Identity: id, Store: db, Listen: listen, Platform: platform, Handler: d.h, Emit: emit, Now: clk.Now,
		Timing: peer.Timing{Ping: 100 * time.Millisecond, Idle: 700 * time.Millisecond, BackoffBase: 40 * time.Millisecond,
			BackoffMax: 200 * time.Millisecond, Rebind: 30 * time.Millisecond, Handshake: 2 * time.Second, DialTimeout: time.Second},
		Hooks: peer.Hooks{AllowLoopback: true, RandomPort: true, ListenAddrs: func() []netip.Addr { return loopback }},
	})
	d.m = NewManager(d.e, db, id, emit, clk.Now, tm)
	if err := d.e.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.e.Close)
	if listen {
		waitUntil(t, name+" to listen", func() bool { return d.e.ListenPort() != 0 })
	}
	return d
}

func (d *dev) event(typ string) map[string]any {
	d.evMu.Lock()
	defer d.evMu.Unlock()
	for _, ev := range d.events {
		if ev["type"] == typ {
			return ev
		}
	}
	return nil
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

func newClock() *clock { return &clock{t: time.Now().UTC().Truncate(time.Second)} }

func fastTimings() Timings { return Timings{RateBurst: 100} }

// joiner runs Join in the background and exposes the SAS and the outcome.
type joiner struct {
	sas  chan string
	done chan struct{}
	peer store.Peer
	err  error
}

func (d *dev) startJoin(link Link) *joiner {
	j := &joiner{sas: make(chan string, 1), done: make(chan struct{})}
	go func() {
		defer close(j.done)
		j.peer, j.err = Join(context.Background(), JoinParams{
			Link: link, Identity: d.id, Platform: d.platform, Listen: d.platform == "windows", ListenPort: d.e.ListenPort(),
			AllowAddr: d.e.AddrAllowed, Now: d.clk.Now, Poll: 20 * time.Millisecond, Timeout: 5 * time.Second,
			OnSAS: func(s string) { j.sas <- s },
		})
	}()
	return j
}

func (j *joiner) wait(t *testing.T) {
	t.Helper()
	select {
	case <-j.done:
	case <-time.After(8 * time.Second):
		t.Fatal("Join did not finish")
	}
}

func codeOf(err error) string {
	if pe, ok := err.(*proto.Error); ok {
		return string(pe.Code)
	}
	return ""
}

// pairViaQR does the whole happy path and returns the pending request id.
func pairViaQR(t *testing.T, win, pix *dev) {
	t.Helper()
	b, err := win.m.Begin()
	if err != nil {
		t.Fatal(err)
	}
	link, err := ParseLink(b.Link, win.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	j := pix.startJoin(link)
	waitUntil(t, "pair.request", func() bool { return win.event("pair.request") != nil })
	req := win.event("pair.request")
	select {
	case sas := <-j.sas:
		if sas != req["sas"] {
			t.Fatalf("the two devices show different codes: %s vs %v", sas, req["sas"])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("joiner never learned the code")
	}
	if err := win.m.Confirm(req["pair_id"].(string), true); err != nil {
		t.Fatal(err)
	}
	j.wait(t)
	if j.err != nil {
		t.Fatalf("Join: %v", j.err)
	}
	if err := pix.db.PutPeer(j.peer); err != nil {
		t.Fatal(err)
	}
	pix.e.PeerAdded(j.peer)
}

func connected(d *dev, other *dev) bool {
	for _, s := range d.e.PeerStatuses() {
		if s.DeviceID == other.id.DeviceID && s.State == peer.StateConnected {
			return true
		}
	}
	return false
}

func TestPairWithQRThenExchangeAClipboardEvent(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, fastTimings())
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	pairViaQR(t, win, pix)

	// both sides stored each other, with the right pins
	wp, found, _ := win.db.GetPeer(pix.id.DeviceID)
	if !found || wp.State != "paired" || wp.CertFP != pix.id.Fingerprint() || wp.Name != "pixel" || wp.Platform != "android" {
		t.Fatalf("windows' record of the phone = %+v", wp)
	}
	pp, found, _ := pix.db.GetPeer(win.id.DeviceID)
	if !found || pp.CertFP != win.id.Fingerprint() || pp.Port != win.e.ListenPort() || pp.TailnetAddrs[0] != "127.0.0.1" || pp.Platform != "windows" {
		t.Fatalf("phone's record of windows = %+v", pp)
	}
	if win.event("pair.result")["state"] != StateAccepted {
		t.Fatalf("events: %v", win.events)
	}

	// and the engines connect on their own and talk
	waitUntil(t, "link up", func() bool { return connected(pix, win) && connected(win, pix) })
	if _, err := win.e.Send(pix.id.DeviceID, proto.KindClipboard, map[string]any{"mime_type": proto.MimeText, "content": "first words", "size": 11}, peer.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "event arrives", func() bool { return len(pix.h.got()) == 1 })
	if pix.h.got()[0] != "first words" {
		t.Fatalf("got %v", pix.h.got())
	}
}

func TestPairWithoutAFingerprintStillWorksAndIsBound(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, fastTimings())
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	b, _ := win.m.Begin()
	link, _ := ParseLink(b.Link, clk.Now())
	link.FP = "" // typed in by hand: only host, port and the secret
	j := pix.startJoin(link)
	waitUntil(t, "pair.request", func() bool { return win.event("pair.request") != nil })
	if err := win.m.Confirm(win.event("pair.request")["pair_id"].(string), true); err != nil {
		t.Fatal(err)
	}
	j.wait(t)
	if j.err != nil {
		t.Fatal(j.err)
	}
	// TOFU learned the real fingerprint and device id from the handshake
	if j.peer.CertFP != win.id.Fingerprint() || j.peer.DeviceID != win.id.DeviceID {
		t.Fatalf("peer = %+v", j.peer)
	}
}

func TestPairWinToWinStoresTheJoinersPort(t *testing.T) {
	clk := newClock()
	a := newDev(t, "alpha", "windows", true, clk, fastTimings())
	b := newDev(t, "bravo", "windows", true, clk, fastTimings())
	pairViaQR(t, a, b)
	p, _, _ := a.db.GetPeer(b.id.DeviceID)
	if p.Port != b.e.ListenPort() || p.Platform != "windows" {
		t.Fatalf("alpha's record of bravo = %+v (bravo listens on %d)", p, b.e.ListenPort())
	}
	// exactly one side dials (the smaller device_id); either way they end up connected
	waitUntil(t, "link up", func() bool { return connected(a, b) && connected(b, a) })
}

func TestRejectingEndsThePairingForBoth(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, fastTimings())
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	bg, _ := win.m.Begin()
	link, _ := ParseLink(bg.Link, clk.Now())
	j := pix.startJoin(link)
	waitUntil(t, "request", func() bool { return win.event("pair.request") != nil })
	if err := win.m.Confirm(win.event("pair.request")["pair_id"].(string), false); err != nil {
		t.Fatal(err)
	}
	j.wait(t)
	if j.err == nil || codeOf(j.err) != "PAIRING_FAILED" || !strings.Contains(j.err.Error(), "declined") {
		t.Fatalf("joiner err = %v", j.err)
	}
	if peers, _ := win.db.ListPeers(); len(peers) != 0 {
		t.Fatalf("a rejected device was stored: %+v", peers)
	}
	// the secret is spent: the same QR code cannot be used again
	j2 := pix.startJoin(link)
	j2.wait(t)
	if j2.err == nil || codeOf(j2.err) != "PAIRING_FAILED" {
		t.Fatalf("reusing the code: err = %v", j2.err)
	}
}

func TestACodeCanOnlyBeUsedOnce(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, fastTimings())
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	other := newDev(t, "tablet", "android", false, clk, fastTimings())
	b, _ := win.m.Begin()
	link, _ := ParseLink(b.Link, clk.Now())
	j := pix.startJoin(link)
	waitUntil(t, "request", func() bool { return win.event("pair.request") != nil })

	// while the first request waits for the user, a second device with the same code is turned away
	j2 := other.startJoin(link)
	j2.wait(t)
	if j2.err == nil || codeOf(j2.err) != "PAIRING_FAILED" {
		t.Fatalf("second joiner err = %v", j2.err)
	}
	if err := win.m.Confirm(win.event("pair.request")["pair_id"].(string), true); err != nil {
		t.Fatal(err)
	}
	j.wait(t)
	if j.err != nil {
		t.Fatal(j.err)
	}
	// after success the code is dead for everyone
	j3 := other.startJoin(link)
	j3.wait(t)
	if j3.err == nil {
		t.Fatal("a spent code paired a third device")
	}
}

func TestWrongSecretIsRefusedAndNothingIsStored(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, fastTimings())
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	b, _ := win.m.Begin()
	link, _ := ParseLink(b.Link, clk.Now())
	link.PSK = NewPSK() // a different secret: unknown session
	j := pix.startJoin(link)
	j.wait(t)
	if j.err == nil || codeOf(j.err) != "PAIRING_FAILED" {
		t.Fatalf("err = %v", j.err)
	}
	if win.event("pair.request") != nil {
		t.Fatal("the user was bothered by a request with the wrong secret")
	}
}

// postPair sends a hand-made request as `d` (a valid certificate, any body).
func postPair(t *testing.T, d *dev, win *dev, req pairRequest) (int, string) {
	t.Helper()
	client := peer.HTTPClient(peer.ClientTLS(d.id, win.id.Fingerprint()), time.Second)
	body, _ := json.Marshal(req)
	resp, err := client.Post("https://127.0.0.1:"+strconv.Itoa(win.e.ListenPort())+"/v1/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var e struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&e)
	return resp.StatusCode, e.Error.Code
}

func goodRequest(d *dev, win *dev, psk []byte, nonce string) pairRequest {
	return pairRequest{
		SessionID: SessionID(psk), DeviceID: d.id.DeviceID, Name: d.name, Platform: d.platform, CertFP: d.id.Fingerprint(),
		Nonce: nonce, Proof: Proof(psk, d.id.DeviceID, d.id.Fingerprint(), win.id.Fingerprint(), nonce),
	}
}

func TestThreeWrongProofsKillTheSession(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, fastTimings())
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	b, _ := win.m.Begin()
	link, _ := ParseLink(b.Link, clk.Now())
	nonce := NewNonce()

	bad := goodRequest(pix, win, link.PSK, nonce)
	bad.Proof = Proof(NewPSK(), pix.id.DeviceID, pix.id.Fingerprint(), win.id.Fingerprint(), nonce) // right session id, wrong proof
	for i := 0; i < 3; i++ {
		if status, code := postPair(t, pix, win, bad); status != 403 || code != "PAIRING_FAILED" {
			t.Fatalf("attempt %d = %d %s", i, status, code)
		}
	}
	// the genuine proof now fails too: the session is gone
	if status, code := postPair(t, pix, win, goodRequest(pix, win, link.PSK, nonce)); status != 403 || code != "PAIRING_FAILED" {
		t.Fatalf("after three failures = %d %s, want the session dead", status, code)
	}
	if win.event("pair.request") != nil {
		t.Fatal("a request reached the user")
	}
}

func TestExpiredCodeIsReportedAsExpired(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, fastTimings())
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	b, _ := win.m.Begin()
	link, _ := ParseLink(b.Link, clk.Now())
	// the joiner's own check
	clk.Add(6 * time.Minute)
	j := pix.startJoin(link)
	j.wait(t)
	if codeOf(j.err) != "PAIRING_EXPIRED" {
		t.Fatalf("joiner err = %v", j.err)
	}
	// and the listener's check, bypassing the joiner's
	link.Exp = clk.Now().Add(time.Hour)
	if status, code := postPair(t, pix, win, goodRequest(pix, win, link.PSK, NewNonce())); status != 403 || code != "PAIRING_FAILED" {
		// the purge already dropped the session: indistinguishable from an unknown code, by design
		t.Fatalf("expired session answered %d %s", status, code)
	}
}

func TestUnconfirmedRequestsExpire(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, fastTimings())
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	b, _ := win.m.Begin()
	link, _ := ParseLink(b.Link, clk.Now())
	j := pix.startJoin(link)
	waitUntil(t, "request", func() bool { return win.event("pair.request") != nil })
	id := win.event("pair.request")["pair_id"].(string)
	if len(win.m.Pending()) != 1 {
		t.Fatalf("pending = %+v", win.m.Pending())
	}
	clk.Add(3 * time.Minute) // nobody answered within 120 s
	if err := win.m.Confirm(id, true); codeOf(err) != "PAIRING_EXPIRED" {
		t.Fatalf("late confirm err = %v", err)
	}
	j.wait(t)
	if codeOf(j.err) != "PAIRING_EXPIRED" {
		t.Fatalf("joiner err = %v", j.err)
	}
	if peers, _ := win.db.ListPeers(); len(peers) != 0 {
		t.Fatal("an expired request produced a peer")
	}
	if win.event("pair.result")["state"] != StateExpired {
		t.Fatalf("events = %v", win.events)
	}
}

func TestPinnedFingerprintStopsAnImposter(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, fastTimings())
	mallory := newDev(t, "mallory", "windows", true, clk, fastTimings())
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	b, _ := win.m.Begin()
	link, _ := ParseLink(b.Link, clk.Now())
	link.Port = mallory.e.ListenPort() // the code says win, but mallory answers the address
	j := pix.startJoin(link)
	j.wait(t)
	if codeOf(j.err) != "PAIRING_FAILED" || !strings.Contains(j.err.Error(), "not the one in the pairing code") {
		t.Fatalf("err = %v", j.err)
	}
	if mallory.event("pair.request") != nil {
		t.Fatal("the imposter received the secret-bound request")
	}
}

func TestACertificateCannotClaimAnotherDeviceID(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, fastTimings())
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	victim := newDev(t, "victim", "android", false, clk, fastTimings())
	b, _ := win.m.Begin()
	link, _ := ParseLink(b.Link, clk.Now())
	nonce := NewNonce()
	req := goodRequest(pix, win, link.PSK, nonce)
	req.DeviceID = victim.id.DeviceID
	req.Proof = Proof(link.PSK, victim.id.DeviceID, pix.id.Fingerprint(), win.id.Fingerprint(), nonce)
	if status, code := postPair(t, pix, win, req); status != 403 || code != "PAIRING_FAILED" {
		t.Fatalf("spoofed device_id = %d %s", status, code)
	}
	req = goodRequest(pix, win, link.PSK, nonce)
	req.CertFP = victim.id.Fingerprint()
	if status, _ := postPair(t, pix, win, req); status != 403 {
		t.Fatalf("spoofed cert_fp = %d", status)
	}
}

func TestPairingRequestsAreRateLimitedPerSource(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, Timings{RateBurst: 4, RateEvery: time.Hour})
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	b, _ := win.m.Begin()
	link, _ := ParseLink(b.Link, clk.Now())
	limited := 0
	for i := 0; i < 8; i++ {
		bad := goodRequest(pix, win, link.PSK, NewNonce())
		bad.SessionID = "nope"
		if status, code := postPair(t, pix, win, bad); status == 429 {
			limited++
			if code != "RATE_LIMITED" {
				t.Fatalf("429 with code %q", code)
			}
		}
	}
	if limited != 4 {
		t.Fatalf("%d of 8 requests were limited, want 4 (burst of 4)", limited)
	}
}

func TestOnlyTheRequesterMayPoll(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, fastTimings())
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	nosy := newDev(t, "nosy", "android", false, clk, fastTimings())
	b, _ := win.m.Begin()
	link, _ := ParseLink(b.Link, clk.Now())
	j := pix.startJoin(link)
	waitUntil(t, "request", func() bool { return win.event("pair.request") != nil })
	id := win.event("pair.request")["pair_id"].(string)

	client := peer.HTTPClient(peer.ClientTLS(nosy.id, win.id.Fingerprint()), time.Second)
	resp, err := client.Get("https://127.0.0.1:" + strconv.Itoa(win.e.ListenPort()) + "/v1/pair/" + id)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("a different certificate polling someone else's request = %d, want 404", resp.StatusCode)
	}
	_ = win.m.Confirm(id, false)
	j.wait(t)
}

func TestCancelAndLimitsOnOpenSessions(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, Timings{MaxOpen: 2, RateBurst: 100})
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	b1, err := win.m.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := win.m.Begin(); err != nil {
		t.Fatal(err)
	}
	if _, err := win.m.Begin(); codeOf(err) != "RATE_LIMITED" {
		t.Fatalf("third session err = %v", err)
	}
	win.m.Cancel(b1.SessionID)
	if _, err := win.m.Begin(); err != nil {
		t.Fatalf("after Cancel: %v", err)
	}
	link, _ := ParseLink(b1.Link, clk.Now())
	j := pix.startJoin(link)
	j.wait(t)
	if j.err == nil {
		t.Fatal("a cancelled code still pairs")
	}
}

func TestBeginNeedsATailnetAddress(t *testing.T) {
	clk := newClock()
	pix := newDev(t, "pixel", "android", false, clk, fastTimings()) // Android does not listen
	if _, err := pix.m.Begin(); codeOf(err) != "NO_TAILNET" {
		t.Fatalf("Begin without a listener: %v", err)
	}
}

func TestJoinRejectsNonTailnetHostsAndDeadHosts(t *testing.T) {
	clk := newClock()
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	link := Link{Host: "8.8.8.8", Port: 47821, PSK: NewPSK(), Exp: clk.Now().Add(time.Minute)}
	j := pix.startJoin(link)
	j.wait(t)
	if codeOf(j.err) != "INVALID" {
		t.Fatalf("public address err = %v", j.err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	link = Link{Host: "127.0.0.1", Port: port, PSK: NewPSK(), Exp: clk.Now().Add(time.Minute)}
	j = pix.startJoin(link)
	j.wait(t)
	if codeOf(j.err) != "PEER_OFFLINE" {
		t.Fatalf("closed port err = %v", j.err)
	}
}

func TestRePairingAnExistingDeviceUpdatesItsRecord(t *testing.T) {
	clk := newClock()
	win := newDev(t, "win", "windows", true, clk, fastTimings())
	pix := newDev(t, "pixel", "android", false, clk, fastTimings())
	pairViaQR(t, win, pix)
	if err := win.db.SetPeerState(pix.id.DeviceID, "revoked"); err != nil { // the user removed it earlier
		t.Fatal(err)
	}
	// a revoked fingerprint is a permanent no
	b, _ := win.m.Begin()
	link, _ := ParseLink(b.Link, clk.Now())
	_ = win.db.AddRevokedFP(pix.id.Fingerprint(), 1)
	j := pix.startJoin(link)
	j.wait(t)
	if j.err == nil {
		t.Fatal("a revoked fingerprint paired again")
	}
}

func TestJoinAndManagerAgreeOnTheHTTPShape(t *testing.T) {
	// guards the JSON field names the two sides share
	b, _ := json.Marshal(pairRequest{SessionID: "s", DeviceID: "d", Name: "n", Platform: "p", CertFP: "c", Nonce: "x", Proof: "y", Port: 1, TailnetAddrs: []string{"a"}})
	for _, k := range []string{`"sid"`, `"device_id"`, `"name"`, `"platform"`, `"cert_fp"`, `"nonce"`, `"proof"`, `"port"`, `"tailnet_addrs"`} {
		if !bytes.Contains(b, []byte(k)) {
			t.Errorf("request lacks %s: %s", k, b)
		}
	}
	_ = http.StatusOK
}
