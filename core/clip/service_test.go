package clip

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"nightclip/core/internal/testnet"
	"nightclip/core/peer"
	"nightclip/core/proto"
)

type handler struct{ s *Service }

func (h handler) OnEvent(p string, e *proto.Envelope) { h.s.OnEvent(p, e) }
func (h handler) OnAck(p, e, r, reason string)        { h.s.OnAck(p, e, r, reason) }
func (h handler) OnPeerState(string, string)          {}

// rig is a connected Windows + Android pair, each with a clip.Service.
type rig struct {
	win, pix       *testnet.Device
	winSvc, pixSvc *Service
	clock          *testnet.Clock
}

func newRig(t *testing.T, winMode, pixMode string) *rig {
	t.Helper()
	clock := testnet.NewClock()
	r := &rig{clock: clock}
	r.win = testnet.New(t, "win", "windows", true, clock, nil)
	r.pix = testnet.New(t, "pixel", "android", false, clock, nil)
	r.winSvc = New(r.win.Engine, r.win.DB, r.win.Emit, clock.Now)
	r.pixSvc = New(r.pix.Engine, r.pix.DB, r.pix.Emit, clock.Now)
	r.win.Proxy.Set(handler{r.winSvc})
	r.pix.Proxy.Set(handler{r.pixSvc})
	testnet.Connect(t, r.win, r.pix, winMode, pixMode)
	return r
}

func (r *rig) captureOn(t *testing.T, d *testnet.Device) {
	t.Helper()
	if err := d.DB.SetSetting(SettingCapture, "on"); err != nil {
		t.Fatal(err)
	}
}

func received(d *testnet.Device) []map[string]any {
	var out []map[string]any
	for _, ev := range d.Events() {
		if ev["type"] == "clip.received" {
			out = append(out, ev)
		}
	}
	return out
}

func TestCaptureIsOnByDefaultAndAnExplicitOffIsKept(t *testing.T) {
	r := newRig(t, "notify", "notify")
	if err := r.win.DB.SetSetting(SettingCapture, "off"); err != nil {
		t.Fatal(err)
	}
	res, err := r.winSvc.LocalChanged("copied text")
	if err != nil || res.Suppressed != SuppressCaptureOff || len(res.Sent) != 0 {
		t.Fatalf("LocalChanged with capture off = %+v, %v", res, err)
	}
	time.Sleep(60 * time.Millisecond)
	if len(received(r.pix)) != 0 {
		t.Fatal("something was sent with capture off")
	}
	// nothing chosen at all: copying just works
	other := newRig(t, "notify", "notify")
	if got, err := other.winSvc.LocalChanged("never asked"); err != nil || len(got.Sent) != 1 {
		t.Fatalf("unset capture must send: %+v, %v", got, err)
	}
	r.captureOn(t, r.win)
	res, err = r.winSvc.LocalChanged("copied text")
	if err != nil || len(res.Sent) != 1 || res.Sent[0].DeviceID != r.pix.ID.DeviceID || res.Sent[0].Name != "pixel" {
		t.Fatalf("LocalChanged = %+v, %v", res, err)
	}
	testnet.WaitUntil(t, "phone receives", func() bool { return len(received(r.pix)) == 1 })
	ev := received(r.pix)[0]
	if ev["text"] != "copied text" || ev["from"] != r.win.ID.DeviceID || ev["from_name"] != "win" || ev["sensitive"] != false || ev["decision"] != DecisionNotify {
		t.Fatalf("clip.received = %v", ev)
	}
}

func TestLocalChangeFilters(t *testing.T) {
	r := newRig(t, "notify", "notify")
	r.captureOn(t, r.win)
	expect := func(text, wantSuppressed string) {
		t.Helper()
		res, err := r.winSvc.LocalChanged(text)
		if err != nil || res.Suppressed != wantSuppressed {
			t.Fatalf("LocalChanged(%.20q) = %+v, %v; want suppressed %q", text, res, err, wantSuppressed)
		}
	}
	expect("", SuppressEmpty)
	expect("  \r\n\t ", SuppressEmpty)
	expect(strings.Repeat("a", proto.MaxClipboardBytes+1), SuppressTooLarge)
	expect("first", "")
	expect("first", SuppressDuplicate) // one copy often fires several notifications
	r.clock.Advance(5 * time.Second)
	expect("first", "") // the user copied it again on purpose
	// no paired devices at all
	solo := testnet.New(t, "solo", "windows", true, r.clock, nil)
	soloSvc := New(solo.Engine, solo.DB, solo.Emit, r.clock.Now)
	_ = solo.DB.SetSetting(SettingCapture, "on")
	if res, _ := soloSvc.LocalChanged("anything"); res.Suppressed != SuppressNoPeers {
		t.Fatalf("no peers: %+v", res)
	}
}

func TestAppliedTextIsNotSentBackAsAnEcho(t *testing.T) {
	r := newRig(t, "auto", "auto")
	r.captureOn(t, r.win)
	r.captureOn(t, r.pix)

	if _, err := r.winSvc.LocalChanged("from the pc"); err != nil {
		t.Fatal(err)
	}
	testnet.WaitUntil(t, "phone receives", func() bool { return len(received(r.pix)) == 1 })
	ev := received(r.pix)[0]
	if ev["decision"] != DecisionAuto {
		t.Fatalf("decision = %v", ev["decision"])
	}
	// the phone's shell writes the text to its clipboard, then reports it...
	if err := r.pixSvc.MarkApplied(ev["event_id"].(string)); err != nil {
		t.Fatal(err)
	}
	// ...and the clipboard change notification it causes must not bounce back
	if res, _ := r.pixSvc.LocalChanged("from the pc"); res.Suppressed != SuppressEcho || len(res.Sent) != 0 {
		t.Fatalf("echo not recognised: %+v", res)
	}
	// a late notification (slow phone, wake from sleep) is still an echo: no time limit
	r.clock.Advance(10 * time.Minute)
	if res, _ := r.pixSvc.LocalChanged("from the pc"); res.Suppressed != SuppressEcho || len(res.Sent) != 0 {
		t.Fatalf("late echo: %+v", res)
	}
	// text that differs (here: a trailing newline) is not the echo; it is a genuine copy
	if res, _ := r.pixSvc.LocalChanged("from the pc\r\n"); res.Suppressed == SuppressEcho {
		t.Fatal("different text (a trailing newline) must not be treated as the echo")
	}
	// the user really copied something else, so the echo memory is gone and copying
	// the old text again afterwards is a deliberate act and goes out
	if res, _ := r.pixSvc.LocalChanged("something else"); len(res.Sent) != 1 {
		t.Fatalf("genuine copy: %+v", res)
	}
	r.clock.Advance(10 * time.Second)
	if res, _ := r.pixSvc.LocalChanged("from the pc"); res.Suppressed != "" || len(res.Sent) != 1 {
		t.Fatalf("deliberate re-copy: %+v", res)
	}
	// the sender hears that it was applied
	testnet.WaitUntil(t, "applied ack on the pc", func() bool {
		return r.win.Find("clip.ack", map[string]any{"result": "applied"}) != nil
	})
}

func TestCRLFAndLFAreTheSameTextOnTheWire(t *testing.T) {
	r := newRig(t, "notify", "notify")
	r.captureOn(t, r.win)
	if _, err := r.winSvc.LocalChanged("line1\r\nline2"); err != nil {
		t.Fatal(err)
	}
	testnet.WaitUntil(t, "received", func() bool { return len(received(r.pix)) == 1 })
	if got := received(r.pix)[0]["text"]; got != "line1\nline2" {
		t.Fatalf("text on the wire = %q", got)
	}
}

func TestPolicyFollowsThePeerSetting(t *testing.T) {
	for _, tc := range []struct{ mode, text, want string }{
		{"notify", "plain words", DecisionNotify},
		{"manual", "plain words", DecisionManual},
		{"auto", "plain words", DecisionAuto},
		{"auto", "P@ssw0rd!2024", DecisionManual}, // sensitive: never automatic
		{"notify", "P@ssw0rd!2024", DecisionNotify},
	} {
		t.Run(tc.mode+"/"+tc.text, func(t *testing.T) {
			r := newRig(t, "notify", tc.mode) // the phone's setting for the PC decides how the phone treats PC events
			if _, err := r.winSvc.Send(tc.text, nil); err != nil {
				t.Fatal(err)
			}
			testnet.WaitUntil(t, "received", func() bool { return len(received(r.pix)) == 1 })
			ev := received(r.pix)[0]
			if ev["decision"] != tc.want {
				t.Fatalf("decision = %v, want %s", ev["decision"], tc.want)
			}
			if wantSens := tc.text == "P@ssw0rd!2024"; ev["sensitive"] != wantSens {
				t.Fatalf("sensitive = %v", ev["sensitive"])
			}
		})
	}
}

func TestSensitiveFlagTravelsAndSendCanBeBlocked(t *testing.T) {
	r := newRig(t, "notify", "auto")
	r.captureOn(t, r.win)
	if _, err := r.winSvc.LocalChanged("ghp_" + strings.Repeat("a1B2c3", 7)); err != nil {
		t.Fatal(err)
	}
	testnet.WaitUntil(t, "received", func() bool { return len(received(r.pix)) == 1 })
	if ev := received(r.pix)[0]; ev["sensitive"] != true || ev["decision"] != DecisionManual {
		t.Fatalf("token event = %v", ev)
	}
	r.clock.Advance(10 * time.Second)
	_ = r.win.DB.SetSetting(SettingSendSensitive, "off")
	if res, _ := r.winSvc.LocalChanged("ghp_" + strings.Repeat("z9Y8x7", 7)); res.Suppressed != SuppressSensitive {
		t.Fatalf("send_sensitive=off: %+v", res)
	}
}

func TestSensitiveDetectionCanBeDisabled(t *testing.T) {
	r := newRig(t, "notify", "auto")
	r.captureOn(t, r.win)
	for _, d := range []*testnet.Device{r.win, r.pix} {
		if err := d.DB.SetSetting(SettingSensitiveDetection, "off"); err != nil {
			t.Fatal(err)
		}
	}
	if res, err := r.winSvc.LocalChanged("P@ssw0rd!2024"); err != nil || len(res.Sent) != 1 {
		t.Fatalf("detection off: %+v, %v", res, err)
	}
	testnet.WaitUntil(t, "received with detection off", func() bool { return len(received(r.pix)) == 1 })
	if ev := received(r.pix)[0]; ev["sensitive"] != false || ev["decision"] != DecisionAuto {
		t.Fatalf("detection off event = %v", ev)
	}

	// The receiver's switch decides for its own display: a peer that still guesses does not
	// force the hidden label here.
	_ = r.win.DB.SetSetting(SettingSensitiveDetection, "on")
	r.clock.Advance(10 * time.Second)
	if _, err := r.winSvc.Send("P@ssw0rd!2024", nil); err != nil {
		t.Fatal(err)
	}
	testnet.WaitUntil(t, "second event", func() bool { return len(received(r.pix)) == 2 })
	if ev := received(r.pix)[1]; ev["sensitive"] != false || ev["decision"] != DecisionAuto {
		t.Fatalf("receiver detection off = %v", ev)
	}
}

func TestExplicitSendIgnoresCaptureAndEchoChecks(t *testing.T) {
	r := newRig(t, "notify", "notify")
	// capture is off, yet an explicit send goes out
	sent, err := r.winSvc.Send("sent by hand", nil)
	if err != nil || len(sent) != 1 {
		t.Fatalf("Send = %+v, %v", sent, err)
	}
	testnet.WaitUntil(t, "received", func() bool { return len(received(r.pix)) == 1 })
	// to a specific device only
	if _, err := r.winSvc.Send("x", []string{"dev_" + strings.Repeat("a", 26)}); err == nil {
		t.Fatal("sending to a device that is not paired succeeded")
	}
	if _, err := r.winSvc.Send("   ", nil); err == nil {
		t.Fatal("empty text accepted")
	}
	if _, err := r.winSvc.Send(strings.Repeat("a", proto.MaxClipboardBytes+1), nil); err == nil {
		t.Fatal("oversized text accepted")
	}
}

func TestDismissIsReportedBack(t *testing.T) {
	r := newRig(t, "notify", "notify")
	if _, err := r.winSvc.Send("not interested", nil); err != nil {
		t.Fatal(err)
	}
	testnet.WaitUntil(t, "received", func() bool { return len(received(r.pix)) == 1 })
	id := received(r.pix)[0]["event_id"].(string)
	if err := r.pixSvc.Dismiss(id); err != nil {
		t.Fatal(err)
	}
	testnet.WaitUntil(t, "ignored ack", func() bool {
		return r.win.Find("clip.ack", map[string]any{"result": "ignored", "reason": "user_dismissed"}) != nil
	})
	if err := r.pixSvc.Dismiss("5b8f0e5c-3f6e-4a8b-9f32-3d4c7f2a1e90"); err == nil {
		t.Fatal("dismissing an unknown event succeeded")
	}
	if err := r.pixSvc.MarkApplied("5b8f0e5c-3f6e-4a8b-9f32-3d4c7f2a1e90"); err == nil {
		t.Fatal("applying an unknown event succeeded")
	}
}

func TestInboxHistory(t *testing.T) {
	r := newRig(t, "notify", "notify")
	for i := 0; i < 3; i++ {
		if _, err := r.winSvc.Send(fmt.Sprintf("note %d", i), nil); err != nil {
			t.Fatal(err)
		}
		r.clock.Advance(time.Second)
	}
	testnet.WaitUntil(t, "3 events", func() bool { return len(received(r.pix)) == 3 })

	items, err := r.pixSvc.Inbox(0)
	if err != nil || len(items) != 3 {
		t.Fatalf("Inbox = %+v, %v", items, err)
	}
	if items[0].Text != "note 2" || items[2].Text != "note 0" || items[0].FromName != "win" || items[0].State != "received" {
		t.Fatalf("order/contents = %+v", items)
	}
	if two, _ := r.pixSvc.Inbox(2); len(two) != 2 {
		t.Fatalf("limit ignored: %d", len(two))
	}
	got, err := r.pixSvc.GetInbox(items[1].EventID)
	if err != nil || got.Text != "note 1" {
		t.Fatalf("GetInbox = %+v, %v", got, err)
	}
	if err := r.pixSvc.MarkApplied(items[1].EventID); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.pixSvc.GetInbox(items[1].EventID); got.State != "applied" || got.AppliedAt == 0 {
		t.Fatalf("after apply = %+v", got)
	}
	if err := r.pixSvc.ClearInbox(); err != nil {
		t.Fatal(err)
	}
	if items, _ := r.pixSvc.Inbox(0); len(items) != 0 {
		t.Fatalf("%d items after clear", len(items))
	}
	if _, err := r.pixSvc.GetInbox("5b8f0e5c-3f6e-4a8b-9f32-3d4c7f2a1e90"); err == nil {
		t.Fatal("GetInbox of an unknown event succeeded")
	}
}

func TestHistoryIsBounded(t *testing.T) {
	r := newRig(t, "notify", "notify")
	_ = r.pix.DB.SetSetting(SettingHistoryLimit, "10")
	for i := 0; i < 14; i++ {
		if _, err := r.winSvc.Send(fmt.Sprintf("n%d", i), nil); err != nil {
			t.Fatal(err)
		}
		r.clock.Advance(time.Second)
		want := i + 1
		testnet.WaitUntil(t, "delivery", func() bool { return len(received(r.pix)) == want })
	}
	rows, _ := r.pix.DB.ListInbox(0)
	// pruning runs on arrival, before the newest row is stored, so the bound is limit+1
	if len(rows) > 11 || len(rows) < 10 {
		t.Fatalf("%d rows kept with a limit of 10", len(rows))
	}
}

func TestEventsForAnOfflinePeerExpireInsteadOfArrivingLate(t *testing.T) {
	clock := testnet.NewClock()
	win := testnet.New(t, "win", "windows", true, clock, nil)
	pix := testnet.New(t, "pixel", "android", false, clock, nil)
	winSvc := New(win.Engine, win.DB, win.Emit, clock.Now)
	pixSvc := New(pix.Engine, pix.DB, pix.Emit, clock.Now)
	win.Proxy.Set(handler{winSvc})
	pix.Proxy.Set(handler{pixSvc})
	_ = win.DB.SetSetting(SettingTTL, "60")
	win.Know(pix, 0, "notify")
	win.Start(true)

	if _, err := winSvc.Send("too late", nil); err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Minute) // the phone stays away longer than the TTL
	if _, err := winSvc.Send("fresh", nil); err != nil {
		t.Fatal(err)
	}
	pix.Know(win, win.Engine.ListenPort(), "notify")
	pix.Start(false)
	testnet.WaitUntil(t, "fresh event", func() bool { return len(received(pix)) == 1 })
	time.Sleep(100 * time.Millisecond)
	if got := received(pix); len(got) != 1 || got[0]["text"] != "fresh" {
		t.Fatalf("received %v; the expired event must not arrive", got)
	}
}

func TestSettingsDefaultsAndOverrides(t *testing.T) {
	r := newRig(t, "notify", "notify")
	s := r.winSvc
	if s.Setting(SettingCapture) != "on" || s.Setting(SettingTTL) != "900" || s.Setting(SettingHistoryLimit) != "200" ||
		s.Setting(SettingSendSensitive) != "on" || s.Setting(SettingSensitiveDetection) != "on" || s.Setting(SettingApplyMode) != "auto" || s.Setting(SettingPausedUntil) != "0" || s.Setting("nope") != "" {
		t.Fatal("defaults changed")
	}
	_ = r.win.DB.SetSetting(SettingTTL, "120")
	if s.ttl() != 2*time.Minute {
		t.Fatalf("ttl = %s", s.ttl())
	}
	_ = r.win.DB.SetSetting(SettingTTL, "garbage")
	if s.ttl() != defaultTTL {
		t.Fatalf("a corrupt ttl must fall back to the default, got %s", s.ttl())
	}
	_ = peer.StateConnected
}

// One PC paired with two phones: the text must reach both, each with its own event id.
func TestSendReachesEveryPairedPeer(t *testing.T) {
	clock := testnet.NewClock()
	win := testnet.New(t, "win", "windows", true, clock, nil)
	p1 := testnet.New(t, "pix1", "android", false, clock, nil)
	p2 := testnet.New(t, "pix2", "android", false, clock, nil)
	winSvc := New(win.Engine, win.DB, win.Emit, clock.Now)
	win.Proxy.Set(handler{winSvc})
	p1.Proxy.Set(handler{New(p1.Engine, p1.DB, p1.Emit, clock.Now)})
	p2.Proxy.Set(handler{New(p2.Engine, p2.DB, p2.Emit, clock.Now)})
	win.Know(p1, 0, "notify")
	win.Know(p2, 0, "notify")
	win.Start(true)
	p1.Know(win, win.Engine.ListenPort(), "notify")
	p2.Know(win, win.Engine.ListenPort(), "notify")
	p1.Start(false)
	p2.Start(false)
	p1.WaitState(win, "connected")
	p2.WaitState(win, "connected")

	sent, err := winSvc.Send("hello to both", nil)
	if err != nil || len(sent) != 2 {
		t.Fatalf("expected the text to be queued for 2 peers, got %+v (%v)", sent, err)
	}
	if sent[0].EventID == sent[1].EventID {
		t.Fatal("two links must not share an event id")
	}
	testnet.WaitUntil(t, "both phones receive", func() bool { return len(received(p1)) == 1 && len(received(p2)) == 1 })

	// capture-driven sends fan out as well
	_ = win.DB.SetSetting(SettingCapture, "on")
	res, err := winSvc.LocalChanged("copied on the pc")
	if err != nil || len(res.Sent) != 2 {
		t.Fatalf("LocalChanged reached %d devices (%v)", len(res.Sent), err)
	}
}

// ---- automatic broadcast (docs/plan-auto-broadcast.md) ----------------------------------

func (r *rig) feed(t *testing.T, d *testnet.Device, svc *Service, from *testnet.Device, id, text string, age time.Duration) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"mime_type": proto.MimeText, "content": text, "size": len(text)})
	svc.OnEvent(from.ID.DeviceID, &proto.Envelope{
		EventID: id, Kind: proto.KindClipboard, CreatedAt: r.clock.Now().Add(-age), Payload: payload,
	})
}

func lastReceived(t *testing.T, d *testnet.Device) map[string]any {
	t.Helper()
	got := received(d)
	if len(got) == 0 {
		t.Fatal("no clip.received event")
	}
	return got[len(got)-1]
}

func TestOnlyFreshEventsAreAppliedAutomatically(t *testing.T) {
	r := newRig(t, "auto", "auto")
	r.feed(t, r.pix, r.pixSvc, r.win, "fresh", "just copied", 2*time.Second)
	if ev := lastReceived(t, r.pix); ev["decision"] != DecisionAuto || ev["live"] != true {
		t.Fatalf("fresh event: %v", ev)
	}
	// it waited in an outbox while the phone was offline: offered, never written on its own
	r.feed(t, r.pix, r.pixSvc, r.win, "stale", "copied ten minutes ago", 10*time.Minute)
	if ev := lastReceived(t, r.pix); ev["decision"] != DecisionManual || ev["live"] != false {
		t.Fatalf("stale event must be downgraded to manual: %v", ev)
	}
	// a peer set to notify stays at notify either way
	r2 := newRig(t, "auto", "notify")
	r2.feed(t, r2.pix, r2.pixSvc, r2.win, "n", "x", 0)
	if ev := lastReceived(t, r2.pix); ev["decision"] != DecisionNotify {
		t.Fatalf("notify peer: %v", ev)
	}
}

func TestPauseStopsSendingAndAutomaticApply(t *testing.T) {
	r := newRig(t, "auto", "auto")
	until := r.clock.Now().Add(30 * time.Minute).Unix()
	if err := r.win.DB.SetSetting(SettingPausedUntil, strconv.FormatInt(until, 10)); err != nil {
		t.Fatal(err)
	}
	if res, _ := r.winSvc.LocalChanged("while paused"); res.Suppressed != SuppressPaused || len(res.Sent) != 0 {
		t.Fatalf("paused send: %+v", res)
	}
	// explicit sends are the user's own act and still work
	if _, err := r.winSvc.Send("explicit", nil); err != nil {
		t.Fatalf("explicit send while paused: %v", err)
	}
	// incoming text is offered, not written
	_ = r.pix.DB.SetSetting(SettingPausedUntil, strconv.FormatInt(until, 10))
	r.feed(t, r.pix, r.pixSvc, r.win, "p1", "arrives while paused", 0)
	if ev := lastReceived(t, r.pix); ev["decision"] != DecisionManual {
		t.Fatalf("paused receive: %v", ev)
	}
	// the pause ends by itself
	r.clock.Advance(31 * time.Minute)
	if res, _ := r.winSvc.LocalChanged("after the pause"); res.Suppressed != "" || len(res.Sent) != 1 {
		t.Fatalf("after pause: %+v", res)
	}
	r.feed(t, r.pix, r.pixSvc, r.win, "p2", "arrives after the pause", 0)
	if ev := lastReceived(t, r.pix); ev["decision"] != DecisionAuto {
		t.Fatalf("after pause receive: %v", ev)
	}
	for _, bad := range []string{"-1", "soon", "99999999999"} {
		if ValidateSetting(SettingPausedUntil, bad) == nil {
			t.Errorf("paused_until %q accepted", bad)
		}
	}
}

func TestSendDisabledPeersAreLeftOutOfBroadcasts(t *testing.T) {
	r := newRig(t, "notify", "notify")
	p, _, _ := r.win.DB.GetPeer(r.pix.ID.DeviceID)
	p.SendDisabled = true
	if err := r.win.DB.PutPeer(p); err != nil {
		t.Fatal(err)
	}
	if res, _ := r.winSvc.LocalChanged("copied"); res.Suppressed != SuppressNoPeers || len(res.Sent) != 0 {
		t.Fatalf("broadcast reached a switched-off device: %+v", res)
	}
	if _, err := r.winSvc.Send("to everyone", nil); err == nil {
		t.Fatal("send to everyone must not reach a switched-off device")
	}
	// naming the device is an explicit choice
	if sent, err := r.winSvc.Send("to the phone", []string{r.pix.ID.DeviceID}); err != nil || len(sent) != 1 {
		t.Fatalf("explicit send to a switched-off device: %v, %v", sent, err)
	}
}

// Three devices A, B, C all paired with each other. A copies; B and C each apply it. Their
// own clipboard notifications arrive late (well after the old 3 s window) and must not make
// them broadcast it again: nothing may bounce.
func TestNothingBouncesBetweenThreeDevices(t *testing.T) {
	clock := testnet.NewClock()
	mk := func(name string) (*testnet.Device, *Service) {
		d := testnet.New(t, name, "windows", true, clock, nil)
		svc := New(d.Engine, d.DB, d.Emit, clock.Now)
		d.Proxy.Set(handler{svc})
		return d, svc
	}
	a, aSvc := mk("a")
	b, bSvc := mk("b")
	c, cSvc := mk("c")
	devs := []*testnet.Device{a, b, c}
	for _, d := range devs {
		d.Start(true)
	}
	for _, x := range devs { // a full mesh: everyone is paired with everyone
		for _, y := range devs {
			if x != y {
				x.Engine.PeerAdded(x.Know(y, y.Engine.ListenPort(), "auto"))
			}
		}
	}
	for _, x := range devs {
		for _, y := range devs {
			if x != y {
				x.WaitState(y, peer.StateConnected)
			}
		}
	}

	if res, err := aSvc.LocalChanged("from a"); err != nil || len(res.Sent) != 2 {
		t.Fatalf("a broadcast = %+v, %v", res, err)
	}
	testnet.WaitUntil(t, "b and c receive", func() bool { return len(received(b)) == 1 && len(received(c)) == 1 })
	for _, x := range []struct {
		d   *testnet.Device
		svc *Service
	}{{b, bSvc}, {c, cSvc}} {
		ev := received(x.d)[0]
		if ev["decision"] != DecisionAuto {
			t.Fatalf("decision on %s = %v", x.d.ID.DeviceID, ev["decision"])
		}
		if err := x.svc.MarkApplied(ev["event_id"].(string)); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(2 * time.Minute) // the shells read their clipboards very late
	for _, svc := range []*Service{bSvc, cSvc} {
		if res, _ := svc.LocalChanged("from a"); res.Suppressed != SuppressEcho || len(res.Sent) != 0 {
			t.Fatalf("a late echo was broadcast again: %+v", res)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if len(received(a)) != 0 || len(received(b)) != 1 || len(received(c)) != 1 {
		t.Fatalf("events bounced: a=%d b=%d c=%d", len(received(a)), len(received(b)), len(received(c)))
	}
}
