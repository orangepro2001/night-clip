package peer

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"nightclip/core/identity"
	"nightclip/core/proto"
)

// rawClient speaks the wire protocol by hand so tests can send what a correct
// peer never would.
type rawClient struct {
	t    *testing.T
	conn *websocket.Conn
}

func rawConnect(t *testing.T, port int, id *identity.Identity, serverFP string) (*rawClient, *http.Response, error) {
	t.Helper()
	ctx, cancel := ctxTimeout(3 * time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "wss://127.0.0.1:"+strconv.Itoa(port)+"/v1/ws", &websocket.DialOptions{
		HTTPClient:   HTTPClient(ClientTLS(id, serverFP), time.Second),
		Subprotocols: []string{Subprotocol},
	})
	if err != nil {
		return nil, resp, err
	}
	conn.SetReadLimit(MaxFrameBytes)
	c := &rawClient{t: t, conn: conn}
	t.Cleanup(func() { conn.CloseNow() })
	return c, resp, nil
}

func (c *rawClient) send(v any) {
	c.t.Helper()
	ctx, cancel := ctxTimeout(2 * time.Second)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, mustJSON(c.t, v)); err != nil {
		c.t.Fatalf("raw write: %v", err)
	}
}

// next reads one frame. Note that a cancelled read closes the connection, so
// callers use generous timeouts and treat an error as "nothing arrived".
func (c *rawClient) next(d time.Duration) (map[string]any, error) {
	ctx, cancel := ctxTimeout(d)
	defer cancel()
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		c.t.Fatalf("bad frame %q", data)
	}
	return m, nil
}

// until reads frames until one of type typ shows up (pings are skipped).
func (c *rawClient) until(typ string, d time.Duration) map[string]any {
	c.t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		m, err := c.next(time.Until(deadline))
		if err != nil {
			c.t.Fatalf("waiting for %q: %v", typ, err)
		}
		if m["type"] == typ {
			return m
		}
	}
	c.t.Fatalf("no %q frame within %s", typ, d)
	return nil
}

func (c *rawClient) hello(deviceID string, cursor int64) map[string]any {
	c.t.Helper()
	c.send(helloFrame{Type: frameHello, DeviceID: deviceID, Name: "raw", RecvCursor: cursor})
	return c.until(frameHelloAck, 3*time.Second)
}

// envelope builds a valid envelope from src to dst; mod can corrupt it.
func envelope(t *testing.T, src, dst string, seq uint64, text string, mod func(*proto.Envelope)) json.RawMessage {
	t.Helper()
	payload := clip(text)
	canon, err := proto.Canonical(payload)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := proto.ContentHash(json.RawMessage(canon))
	now := time.Now().UTC().Truncate(time.Second)
	env := proto.Envelope{
		Version: 1, EventID: newEventID(), Kind: proto.KindClipboard, SourceDeviceID: src, TargetDeviceID: dst,
		Sequence: seq, CreatedAt: now, ExpiresAt: now.Add(time.Hour), ContentHash: hash, Payload: canon,
	}
	if mod != nil {
		mod(&env)
	}
	return mustJSON(t, env)
}

func (c *rawClient) event(env json.RawMessage) { c.send(eventFrame{Type: frameEvent, Envelope: env}) }

func envID(env json.RawMessage) string {
	var e struct {
		EventID string `json:"event_id"`
	}
	_ = json.Unmarshal(env, &e)
	return e.EventID
}

// winWithRawPeer starts a Windows node that knows `pix`'s identity; the test
// then connects "as" pix with a raw client.
func winWithRawPeer(t *testing.T) (win, pix *node, port int) {
	t.Helper()
	win = newNode(t, "win", "windows", true, nil)
	pix = newNode(t, "pixel", "android", false, nil) // never started: only its identity is used
	win.know(pix, 0, "")
	win.start()
	waitUntil(t, "listen", func() bool { return win.e.ListenPort() != 0 })
	return win, pix, win.e.ListenPort()
}

// ---- who may connect ------------------------------------------------------------

func TestUnpairedCertificateReachesOnlyPublicRoutes(t *testing.T) {
	win := newNode(t, "win", "windows", true, nil)
	win.e.Handle("GET /v1/pair-probe", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := PeerFromContext(r.Context())
		_, _ = w.Write([]byte("fp=" + c.Fingerprint))
		if c.Peer != nil {
			t.Error("a stranger must not be resolved to a peer")
		}
	}), true)
	win.start()
	waitUntil(t, "listen", func() bool { return win.e.ListenPort() != 0 })

	stranger, err := identity.Generate("stranger")
	if err != nil {
		t.Fatal(err)
	}
	var cap Capture
	client := HTTPClient(CaptureTLS(stranger, &cap), time.Second)
	base := "https://127.0.0.1:" + strconv.Itoa(win.e.ListenPort())

	for _, path := range []string{"/v1/ws", "/healthz"} {
		resp, err := client.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Error struct{ Code string } `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != 403 || body.Error.Code != "NOT_PAIRED" {
			t.Fatalf("GET %s = %d %q, want 403 NOT_PAIRED", path, resp.StatusCode, body.Error.Code)
		}
	}
	resp, err := client.Get(base + "/v1/pair-probe")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("public route = %v / %v", resp, err)
	}
	resp.Body.Close()
	if cap.FP() != win.id.Fingerprint() {
		t.Fatalf("captured fingerprint %q, want the server's %q", cap.FP(), win.id.Fingerprint())
	}
	if win.rec.eventCount() != 0 {
		t.Fatal("stranger delivered something")
	}
}

func TestPairedCertificateCanReachPrivateRoutes(t *testing.T) {
	win, pix, port := winWithRawPeer(t)
	client := HTTPClient(ClientTLS(pix.id, win.id.Fingerprint()), time.Second)
	resp, err := client.Get("https://127.0.0.1:" + strconv.Itoa(port) + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz = %v / %v", resp, err)
	}
	var body struct {
		DeviceID string `json:"device_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body.DeviceID != win.id.DeviceID {
		t.Fatalf("healthz device_id = %q", body.DeviceID)
	}
}

func TestSourceOutsideTheTailnetIsRefusedEvenWhenPaired(t *testing.T) {
	win := newNode(t, "win", "windows", true, nil, func(c *Config) { c.Hooks.AllowLoopback = false })
	pix := newNode(t, "pixel", "android", false, nil)
	win.know(pix, 0, "")
	win.start()
	waitUntil(t, "listen", func() bool { return win.e.ListenPort() != 0 })

	_, resp, err := rawConnect(t, win.e.ListenPort(), pix.id, win.id.Fingerprint())
	if err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("connect from a non-tailnet address: resp %v err %v, want 403", resp, err)
	}
	client := HTTPClient(ClientTLS(pix.id, win.id.Fingerprint()), time.Second)
	r, err := client.Get("https://127.0.0.1:" + strconv.Itoa(win.e.ListenPort()) + "/healthz")
	if err != nil || r.StatusCode != 403 {
		t.Fatalf("healthz from a non-tailnet address = %v / %v", r, err)
	}
	r.Body.Close()
}

func TestRevokedFingerprintIsRefusedEverywhere(t *testing.T) {
	win, pix, port := winWithRawPeer(t)
	if err := win.db.AddRevokedFP(pix.id.Fingerprint(), 1); err != nil {
		t.Fatal(err)
	}
	_, resp, err := rawConnect(t, port, pix.id, win.id.Fingerprint())
	if err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("revoked fingerprint connect: resp %v err %v", resp, err)
	}
}

func TestDialerRefusesAServerWithTheWrongCertificate(t *testing.T) {
	win := newNode(t, "win", "windows", true, nil)
	pix := newNode(t, "pixel", "android", false, nil)
	imposter, _ := identity.Generate("imposter")
	win.know(pix, 0, "")
	win.start()
	waitUntil(t, "listen", func() bool { return win.e.ListenPort() != 0 })
	p := pix.know(win, win.e.ListenPort(), "")
	p.CertFP = imposter.Fingerprint() // what the phone has pinned is not what the PC presents
	if err := pix.db.PutPeer(p); err != nil {
		t.Fatal(err)
	}
	pix.start()

	waitUntil(t, "security warning", func() bool { return pix.emitted("core.warning", "FINGERPRINT_MISMATCH") > 0 })
	time.Sleep(150 * time.Millisecond)
	if st := pix.linkState(win.id.DeviceID); st == StateConnected {
		t.Fatal("connected to a server whose certificate is not the pinned one")
	}
	if win.e.getLink(pix.id.DeviceID).session() != nil {
		t.Fatal("the server saw a completed session")
	}
	// the TLS layer reports the pin failure itself
	_, _, err := rawConnect(t, win.e.ListenPort(), pix.id, imposter.Fingerprint())
	if err == nil || !strings.Contains(err.Error(), ErrFingerprint.Error()) {
		t.Fatalf("raw dial err = %v, want the fingerprint pin error", err)
	}
	if !errors.Is(err, ErrFingerprint) && !strings.Contains(err.Error(), "pinned fingerprint") {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestADialerTheServerDoesNotKnowIsToldNotPairedAndStopsRetrying(t *testing.T) {
	win := newNode(t, "win", "windows", true, nil)
	pix := newNode(t, "pixel", "android", false, nil)
	win.start() // knows nobody
	waitUntil(t, "listen", func() bool { return win.e.ListenPort() != 0 })
	rec := pix.know(win, win.e.ListenPort(), "")
	rec.PairedAt = 1 // long ago: this is not the "just paired" grace window
	if err := pix.db.PutPeer(rec); err != nil {
		t.Fatal(err)
	}
	pix.start()
	pix.waitState(win.id.DeviceID, StateUnpaired)

	time.Sleep(200 * time.Millisecond)
	l := pix.e.getLink(win.id.DeviceID)
	l.mu.Lock()
	dialing := l.dialCancel != nil
	l.mu.Unlock()
	if dialing {
		t.Fatal("the dial loop kept running after NOT_PAIRED")
	}

	// the user pairs again; Reconnect brings the link up
	win.know(pix, 0, "")
	if err := pix.e.Reconnect(win.id.DeviceID); err != nil {
		t.Fatal(err)
	}
	pix.waitState(win.id.DeviceID, StateConnected)
}

// ---- what a peer may send --------------------------------------------------------

func TestForgedAndMalformedEventsAreRejected(t *testing.T) {
	win, pix, port := winWithRawPeer(t)
	c, _, err := rawConnect(t, port, pix.id, win.id.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	c.hello(pix.id.DeviceID, 0)
	other := "dev_" + strings.Repeat("a", 26)

	// 1. an envelope that claims to come from somebody else
	c.event(envelope(t, other, win.id.DeviceID, 1, "forged", nil))
	if f := c.until(frameError, 2*time.Second); f["code"] != "SOURCE_MISMATCH" {
		t.Fatalf("error frame = %v", f)
	}
	// 2. an envelope for another device
	m := envelope(t, pix.id.DeviceID, other, 1, "misdirected", nil)
	c.event(m)
	if a := c.until(frameAck, 2*time.Second); a["event_id"] != envID(m) || a["result"] != "ignored" || a["reason"] != "target_mismatch" {
		t.Fatalf("ack = %v", a)
	}
	// 3. a payload that does not match its content_hash
	bad := envelope(t, pix.id.DeviceID, win.id.DeviceID, 1, "tampered", func(e *proto.Envelope) {
		e.Payload = json.RawMessage(mustJSON(t, clip("something else")))
	})
	c.event(bad)
	if a := c.until(frameAck, 2*time.Second); a["event_id"] != envID(bad) || a["result"] != "error" || a["reason"] != "HASH_MISMATCH" {
		t.Fatalf("ack = %v", a)
	}
	// 4. garbage
	c.send(map[string]any{"type": "event", "envelope": "not an object"})
	if f := c.until(frameError, 2*time.Second); f["code"] != "INVALID" {
		t.Fatalf("error frame = %v", f)
	}
	if err := c.conn.Write(context.Background(), websocket.MessageText, []byte("{not json")); err != nil {
		t.Fatal(err)
	}
	if f := c.until(frameError, 2*time.Second); f["code"] != "INVALID" {
		t.Fatalf("error frame = %v", f)
	}

	if win.rec.eventCount() != 0 {
		t.Fatalf("%d forged events reached the application", win.rec.eventCount())
	}
	// the corrupted event used up sequence 1 on the receiver, so a good seq 2 is next in line
	good := envelope(t, pix.id.DeviceID, win.id.DeviceID, 2, "genuine", nil)
	c.event(good)
	if a := c.until(frameAck, 2*time.Second); a["event_id"] != envID(good) || a["result"] != "received" {
		t.Fatalf("ack = %v", a)
	}
	waitUntil(t, "delivered", func() bool { return win.rec.eventCount() == 1 })
}

func TestOutOfOrderEventsAreDeliveredInOrder(t *testing.T) {
	win, pix, port := winWithRawPeer(t)
	c, _, _ := rawConnect(t, port, pix.id, win.id.Fingerprint())
	c.hello(pix.id.DeviceID, 0)

	e1 := envelope(t, pix.id.DeviceID, win.id.DeviceID, 1, "one", nil)
	e2 := envelope(t, pix.id.DeviceID, win.id.DeviceID, 2, "two", nil)
	e3 := envelope(t, pix.id.DeviceID, win.id.DeviceID, 3, "three", nil)
	c.event(e3)
	c.event(e2)
	time.Sleep(60 * time.Millisecond)
	if win.rec.eventCount() != 0 {
		t.Fatal("events were delivered before the gap closed")
	}
	c.event(e1)
	waitUntil(t, "three events", func() bool { return win.rec.eventCount() == 3 })
	got := win.rec.eventIDs()
	if got[0] != envID(e1) || got[1] != envID(e2) || got[2] != envID(e3) {
		t.Fatalf("delivery order %v", got)
	}
	if lk, _, _ := win.db.GetLink(pix.id.DeviceID); lk.RecvCursor != 3 {
		t.Fatalf("cursor = %d", lk.RecvCursor)
	}
}

func TestReorderBufferOverflowAsksForAResync(t *testing.T) {
	win, pix, port := winWithRawPeer(t)
	c, _, _ := rawConnect(t, port, pix.id, win.id.Fingerprint())
	c.hello(pix.id.DeviceID, 0)
	for seq := uint64(2); seq <= uint64(2+maxReorder); seq++ { // seq 1 never arrives
		c.event(envelope(t, pix.id.DeviceID, win.id.DeviceID, seq, "late", nil))
	}
	f := c.until(frameResync, 3*time.Second)
	if f["after"].(float64) != 0 {
		t.Fatalf("resync = %v, want after 0", f)
	}
	if win.rec.eventCount() != 0 {
		t.Fatal("buffered events must not be delivered while seq 1 is missing")
	}
}

func TestSkipFrameClosesAGapAndReleasesWhatWasWaiting(t *testing.T) {
	win, pix, port := winWithRawPeer(t)
	c, _, _ := rawConnect(t, port, pix.id, win.id.Fingerprint())
	c.hello(pix.id.DeviceID, 0)

	e3 := envelope(t, pix.id.DeviceID, win.id.DeviceID, 3, "waiting", nil)
	c.event(e3) // sequences 1 and 2 are missing
	time.Sleep(50 * time.Millisecond)
	c.send(skipFrame{Type: frameSkip, Through: 2}) // the sender says: 1 and 2 will never come
	waitUntil(t, "buffered event released", func() bool { return win.rec.eventCount() == 1 })
	if lk, _, _ := win.db.GetLink(pix.id.DeviceID); lk.RecvCursor != 3 {
		t.Fatalf("cursor = %d, want 3", lk.RecvCursor)
	}
	// a skip into the past is harmless; a skip past the buffer moves the cursor
	c.send(skipFrame{Type: frameSkip, Through: 1})
	c.send(skipFrame{Type: frameSkip, Through: 9})
	e10 := envelope(t, pix.id.DeviceID, win.id.DeviceID, 10, "after the jump", nil)
	c.event(e10)
	waitUntil(t, "event after jump", func() bool { return win.rec.eventCount() == 2 })
}

func TestReusedSequenceWithANewEventIDIsIgnored(t *testing.T) {
	win, pix, port := winWithRawPeer(t)
	c, _, _ := rawConnect(t, port, pix.id, win.id.Fingerprint())
	c.hello(pix.id.DeviceID, 0)
	c.event(envelope(t, pix.id.DeviceID, win.id.DeviceID, 1, "first", nil))
	waitUntil(t, "first", func() bool { return win.rec.eventCount() == 1 })
	dup := envelope(t, pix.id.DeviceID, win.id.DeviceID, 1, "same sequence, other event", nil)
	c.event(dup)
	for {
		a := c.until(frameAck, 2*time.Second)
		if a["event_id"] == envID(dup) {
			if a["result"] != "ignored" || a["reason"] != "sequence_reused" {
				t.Fatalf("ack = %v", a)
			}
			break
		}
	}
	if win.rec.eventCount() != 1 {
		t.Fatal("a reused sequence was delivered")
	}
}

func TestOversizedFrameDropsTheConnection(t *testing.T) {
	win, pix, port := winWithRawPeer(t)
	c, _, _ := rawConnect(t, port, pix.id, win.id.Fingerprint())
	c.hello(pix.id.DeviceID, 0)
	c.conn.SetReadLimit(4 << 20)
	huge := make([]byte, MaxFrameBytes+1024)
	for i := range huge {
		huge[i] = 'a'
	}
	ctx, cancel := ctxTimeout(3 * time.Second)
	defer cancel()
	_ = c.conn.Write(ctx, websocket.MessageText, huge)
	// The server may have queued ordinary events (or a goaway) around the oversized frame. The
	// authoritative outcome is that the link goes offline; do not treat a queued frame as acceptance.
	waitUntil(t, "link offline", func() bool { return win.linkState(pix.id.DeviceID) == StateOffline })
}

func TestSilentConnectionIsDroppedAfterTheIdleTimeout(t *testing.T) {
	win, pix, port := winWithRawPeer(t)
	c, _, _ := rawConnect(t, port, pix.id, win.id.Fingerprint())
	c.hello(pix.id.DeviceID, 0)
	win.waitState(pix.id.DeviceID, StateConnected)
	// the client now says nothing at all; pings from the server go unanswered
	start := time.Now()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.next(time.Until(deadline)); err != nil {
			break
		}
	}
	if took := time.Since(start); took < fastTiming.Idle-100*time.Millisecond || took > 3*time.Second {
		t.Fatalf("dropped after %s, want about the idle timeout %s", took, fastTiming.Idle)
	}
	win.waitState(pix.id.DeviceID, StateOffline)
}

func TestHelloForAnotherIdentityIsRefused(t *testing.T) {
	win, pix, port := winWithRawPeer(t)
	c, _, _ := rawConnect(t, port, pix.id, win.id.Fingerprint())
	c.send(helloFrame{Type: frameHello, DeviceID: "dev_" + strings.Repeat("b", 26)})
	if _, err := c.next(2 * time.Second); err == nil {
		t.Fatal("a hello claiming another device_id was answered")
	}
	if win.linkState(pix.id.DeviceID) == StateConnected {
		t.Fatal("link marked connected after a bad hello")
	}
}

func TestANewConnectionReplacesTheOldOne(t *testing.T) {
	win, pix, port := winWithRawPeer(t)
	first, _, _ := rawConnect(t, port, pix.id, win.id.Fingerprint())
	first.hello(pix.id.DeviceID, 0)
	win.waitState(pix.id.DeviceID, StateConnected)

	second, _, _ := rawConnect(t, port, pix.id, win.id.Fingerprint())
	second.hello(pix.id.DeviceID, 0)

	g := first.until(frameGoaway, 3*time.Second)
	if g["reason"] != "replaced" {
		t.Fatalf("goaway = %v", g)
	}
	// the survivor is fully functional and the link stayed up
	time.Sleep(50 * time.Millisecond)
	if win.linkState(pix.id.DeviceID) != StateConnected {
		t.Fatal("the replaced session took the link down")
	}
	second.event(envelope(t, pix.id.DeviceID, win.id.DeviceID, 1, "via the new connection", nil))
	waitUntil(t, "delivered", func() bool { return win.rec.eventCount() == 1 })
}

// ---- pairing lifecycle, listener -----------------------------------------------

func TestRevocationReachesTheOtherSideAndSticks(t *testing.T) {
	win, pix := pairWin(t, nil)
	if err := win.e.RevokePeer(pix.id.DeviceID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "phone learns of the revocation", func() bool { return pix.emitted("peer.revoked", "") == 1 })
	pix.waitState(win.id.DeviceID, StateRevoked)
	win.waitState(pix.id.DeviceID, StateRevoked)

	for _, n := range []*node{win, pix} {
		p, _, _ := n.db.GetPeer(map[*node]string{win: pix.id.DeviceID, pix: win.id.DeviceID}[n])
		if p.State != "revoked" {
			t.Fatalf("%s still holds the peer as %q", n.name, p.State)
		}
		if st := n.e.PeerStatuses(); st[0].State != StateRevoked {
			t.Fatalf("%s status = %+v", n.name, st)
		}
	}
	if revoked, _ := win.db.IsRevoked(pix.id.Fingerprint()); !revoked {
		t.Fatal("the revoked fingerprint is not blocked on the windows side")
	}
	if revoked, _ := pix.db.IsRevoked(win.id.Fingerprint()); !revoked {
		t.Fatal("the revoked fingerprint is not blocked on the phone")
	}
	if _, err := win.e.Send(pix.id.DeviceID, proto.KindClipboard, clip("nope"), SendOptions{}); err == nil {
		t.Fatal("sending to a revoked peer succeeded")
	}
	// and it does not come back by itself
	time.Sleep(200 * time.Millisecond)
	if win.e.getLink(pix.id.DeviceID).session() != nil || pix.e.getLink(win.id.DeviceID).session() != nil {
		t.Fatal("a session exists after revocation")
	}
}

func TestListenerFollowsTheTailnetAddresses(t *testing.T) {
	var addrs atomic.Pointer[[]netip.Addr]
	none := []netip.Addr{}
	addrs.Store(&none)
	win := newNode(t, "win", "windows", true, nil, func(c *Config) {
		c.Hooks.ListenAddrs = func() []netip.Addr { return *addrs.Load() }
	})
	win.start()

	waitUntil(t, "NO_TAILNET warning", func() bool { return win.emitted("core.warning", "NO_TAILNET") == 1 })
	if win.e.ListenPort() != 0 {
		t.Fatal("listening without any tailnet address")
	}
	time.Sleep(100 * time.Millisecond)
	if n := win.emitted("core.warning", "NO_TAILNET"); n != 1 {
		t.Fatalf("NO_TAILNET repeated %d times; it must fire once per outage", n)
	}

	addrs.Store(&loopback)
	waitUntil(t, "listener appears", func() bool { return win.e.ListenPort() != 0 })
	port := win.e.ListenPort()
	if c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second); err != nil {
		t.Fatalf("not reachable: %v", err)
	} else {
		c.Close()
	}
	if got := win.e.ListenAddrs(); len(got) != 1 || got[0].String() != "127.0.0.1" {
		t.Fatalf("listening on %v", got)
	}

	addrs.Store(&none)
	waitUntil(t, "listener closes", func() bool { return win.e.ListenPort() == 0 })
	waitUntil(t, "warning again", func() bool { return win.emitted("core.warning", "NO_TAILNET") == 2 })
	if c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 300*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("the old address still accepts connections")
	}
}

// An address that exists but whose port cannot be bound (taken, or reserved
// by Windows) is LISTEN_FAILED with the reason, never NO_TAILNET.
func TestBindFailureIsListenFailedNotNoTailnet(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	win := newNode(t, "win", "windows", true, nil, func(c *Config) {
		c.Hooks.RandomPort = false
		c.Port = busy.Addr().(*net.TCPAddr).Port
	})
	win.start()

	waitUntil(t, "LISTEN_FAILED warning", func() bool { return win.emitted("core.warning", "LISTEN_FAILED") > 0 })
	if win.e.ListenError() == nil {
		t.Fatal("ListenError is nil while the bind fails")
	}
	if n := win.emitted("core.warning", "NO_TAILNET"); n != 0 {
		t.Fatalf("NO_TAILNET emitted %d times although a tailnet address exists", n)
	}

	busy.Close()
	waitUntil(t, "listener appears", func() bool { return win.e.ListenPort() != 0 })
	if err := win.e.ListenError(); err != nil {
		t.Fatalf("ListenError still %v after the bind succeeded", err)
	}
}

// When the usual port is taken (WSL mirrored networking reserves a block that
// can hold it), the listener moves to the fallback port and a dialer that only
// knows the usual port still connects, then remembers where it found the peer.
func TestBlockedPortFallsBackAndDialersFollow(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	spare, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	primary, fallback := busy.Addr().(*net.TCPAddr).Port, spare.Addr().(*net.TCPAddr).Port
	spare.Close()
	ports := func(c *Config) { c.Hooks.RandomPort, c.Port, c.FallbackPort = false, primary, fallback }

	win := newNode(t, "win", "windows", true, nil, ports)
	pix := newNode(t, "pixel", "android", false, nil, ports)
	win.know(pix, 0, "")
	win.start()
	waitUntil(t, "listening on the fallback port", func() bool { return win.e.ListenPort() == fallback })
	if err := win.e.ListenError(); err != nil {
		t.Fatalf("ListenError = %v although the fallback port is bound", err)
	}

	pix.know(win, primary, "")
	pix.start()
	pix.waitState(win.id.DeviceID, StateConnected)
	waitUntil(t, "the new port to be stored", func() bool {
		p, _, _ := pix.db.GetPeer(win.id.DeviceID)
		return p.Port == fallback
	})
	if got := pix.e.peerPorts(fallback); len(got) != 2 || got[0] != fallback || got[1] != primary {
		t.Fatalf("ports to try after the move = %v", got)
	}
}

func TestProductionAddressDiscoveryOnlyReturnsTailnetAddresses(t *testing.T) {
	for _, a := range TailnetAddrs() {
		if !InTailnet(a) {
			t.Fatalf("TailnetAddrs returned %s", a)
		}
	}
}

func TestEngineRefusesToStartTwiceAndClosesCleanly(t *testing.T) {
	win, _ := pairWin(t, nil)
	if err := win.e.Start(); err == nil {
		t.Fatal("second Start accepted")
	}
	win.e.Close()
	win.e.Close() // idempotent
	if win.e.ListenPort() != 0 {
		t.Fatal("still listening after Close")
	}
}

// Between two PCs the listener may dial the instant the user confirms, before the
// joiner has stored it. A NOT_PAIRED answer right after pairing must be retried.
func TestNotPairedRightAfterPairingIsRetried(t *testing.T) {
	win := newNode(t, "win", "windows", true, nil)
	pix := newNode(t, "pixel", "android", false, nil)
	win.start() // does not know the phone yet
	waitUntil(t, "listen", func() bool { return win.e.ListenPort() != 0 })
	pix.know(win, win.e.ListenPort(), "") // PairedAt = now: inside the grace window
	pix.start()

	time.Sleep(250 * time.Millisecond) // several refused attempts
	if st := pix.linkState(win.id.DeviceID); st == StateUnpaired || st == StateConnected {
		t.Fatalf("state = %s; it must keep retrying while the pairing is fresh", st)
	}
	win.know(pix, 0, "") // the other side finishes saving us
	pix.waitState(win.id.DeviceID, StateConnected)
}

// An event id that already belongs to another device's event must not be treated as
// "a duplicate of mine": the second sender is told, and the first sender's event is
// untouched.
func TestAnEventIDOwnedByAnotherDeviceIsNotMerged(t *testing.T) {
	win := newNode(t, "win", "windows", true, nil)
	a := newNode(t, "a", "android", false, nil)
	b := newNode(t, "b", "android", false, nil)
	win.know(a, 0, "")
	win.know(b, 0, "")
	win.start()
	waitUntil(t, "listen", func() bool { return win.e.ListenPort() != 0 })

	ca, _, err := rawConnect(t, win.e.ListenPort(), a.id, win.id.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	ca.hello(a.id.DeviceID, 0)
	first := envelope(t, a.id.DeviceID, win.id.DeviceID, 1, "from a", nil)
	ca.event(first)
	waitUntil(t, "a's event stored", func() bool { return win.rec.eventCount() == 1 })

	cb, _, err := rawConnect(t, win.e.ListenPort(), b.id, win.id.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	cb.hello(b.id.DeviceID, 0)
	stolen := envelope(t, b.id.DeviceID, win.id.DeviceID, 1, "from b", func(e *proto.Envelope) { e.EventID = envID(first) })
	cb.event(stolen)
	ack := cb.until(frameAck, 2*time.Second)
	if ack["result"] != "error" || ack["reason"] != "event_id_conflict" {
		t.Fatalf("ack = %v", ack)
	}
	if win.rec.eventCount() != 1 {
		t.Fatal("the conflicting event reached the application")
	}
	row, _, _ := win.db.GetInbox(envID(first))
	if row.PeerID != a.id.DeviceID {
		t.Fatalf("the stored event now belongs to %s", row.PeerID)
	}
}
