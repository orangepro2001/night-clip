package peer

import (
	"fmt"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"nightclip/core/proto"
)

func TestConnectAndExchangeBothWays(t *testing.T) {
	win, pix := pairWin(t, nil)

	e1 := pix.sendClip(win, "hello from the phone")
	e2 := win.sendClip(pix, "hello from the pc")
	e3 := pix.sendClip(win, "second from the phone")

	waitUntil(t, "windows got 2 events", func() bool { return win.rec.eventCount() == 2 })
	waitUntil(t, "phone got 1 event", func() bool { return pix.rec.eventCount() == 1 })
	if ids := win.rec.eventIDs(); ids[0] != e1 || ids[1] != e3 {
		t.Fatalf("windows received %v, want [%s %s] in order", ids, e1, e3)
	}
	if ids := pix.rec.eventIDs(); ids[0] != e2 {
		t.Fatalf("phone received %v, want [%s]", ids, e2)
	}
	env := win.rec.events[0]
	if env.SourceDeviceID != pix.id.DeviceID || env.TargetDeviceID != win.id.DeviceID || env.Sequence != 1 || win.rec.events[1].Sequence != 2 {
		t.Fatalf("envelope = %+v", env)
	}
	var p proto.ClipboardPayload
	if err := jsonUnmarshal(env.Payload, &p); err != nil || p.Content != "hello from the phone" {
		t.Fatalf("payload = %+v (%v)", p, err)
	}

	// each side learns that its events were received
	for _, c := range []struct {
		sender *node
		event  string
	}{{pix, e1}, {pix, e3}, {win, e2}} {
		waitUntil(t, "ack for "+c.event[:8], func() bool { a, ok := c.sender.rec.ackFor(c.event); return ok && a.result == ResultReceived })
		row, _, _ := c.sender.db.GetOutbox(c.event)
		if row.State != "acked" || row.AckResult != ResultReceived {
			t.Fatalf("outbox row = %+v", row)
		}
	}

	// bookkeeping: cursors, next sequence, inbox
	if lk, _, _ := win.db.GetLink(pix.id.DeviceID); lk.RecvCursor != 2 || lk.NextSeqOut != 2 {
		t.Fatalf("windows link = %+v", lk)
	}
	if lk, _, _ := pix.db.GetLink(win.id.DeviceID); lk.RecvCursor != 1 || lk.NextSeqOut != 3 {
		t.Fatalf("phone link = %+v", lk)
	}
	if row, found, _ := win.db.GetInbox(e1); !found || row.State != "received" {
		t.Fatalf("inbox row = %+v found %v", row, found)
	}
	st := win.e.PeerStatuses()
	if len(st) != 1 || st[0].State != StateConnected || st[0].Pending != 0 || st[0].RecvCursor != 2 {
		t.Fatalf("status = %+v", st)
	}
}

func TestOfflineQueueIsDeliveredInOrderOnConnect(t *testing.T) {
	win := newNode(t, "win", "windows", true, nil)
	pix := newNode(t, "pixel", "android", false, nil)
	win.know(pix, 0, "")
	win.start()
	waitUntil(t, "listen", func() bool { return win.e.ListenPort() != 0 })

	// events are queued while the phone is not running at all
	var want []string
	for i := 0; i < 5; i++ {
		want = append(want, win.sendClip(pix, fmt.Sprintf("queued %d", i)))
	}
	if st := win.e.PeerStatuses(); st[0].Pending != 5 || st[0].State != StateOffline {
		t.Fatalf("status = %+v", st)
	}

	pix.know(win, win.e.ListenPort(), "")
	pix.start()
	waitUntil(t, "5 events", func() bool { return pix.rec.eventCount() == 5 })
	got := pix.rec.eventIDs()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
	// the sender saw them all acknowledged
	waitUntil(t, "all acked", func() bool { n, _ := win.db.CountPendingOutbox(pix.id.DeviceID); return n == 0 })
}

func TestConcurrentSendsArriveOnceAndInSequenceOrder(t *testing.T) {
	win, pix := pairWin(t, nil)
	const n = 60
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := pix.e.Send(win.id.DeviceID, proto.KindClipboard, clip(fmt.Sprintf("m%d", i)), SendOptions{}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	waitUntil(t, "all delivered", func() bool { return win.rec.eventCount() == n })
	seqs := make([]int, 0, n)
	seen := map[string]bool{}
	for _, e := range win.rec.events {
		if seen[e.EventID] {
			t.Fatalf("event %s delivered twice", e.EventID)
		}
		seen[e.EventID] = true
		seqs = append(seqs, int(e.Sequence))
	}
	if !sort.IntsAreSorted(seqs) || seqs[0] != 1 || seqs[n-1] != n {
		t.Fatalf("delivery order is not the sequence order: %v", seqs)
	}
}

func TestRepeatedDeliveryIsIdempotent(t *testing.T) {
	win, pix := pairWin(t, nil)
	e := pix.sendClip(win, "once")
	waitUntil(t, "delivered", func() bool { return win.rec.eventCount() == 1 })
	waitUntil(t, "acked", func() bool { return pix.rec.ackCount(e) == 1 })

	// The receiver asks for a replay of everything (resync after 0): the sender
	// resends, the receiver must neither redeliver nor stay silent.
	pix.e.getLink(win.id.DeviceID).session().resync(0)
	waitUntil(t, "second ack", func() bool { return pix.rec.ackCount(e) == 2 })
	time.Sleep(50 * time.Millisecond)
	if win.rec.eventCount() != 1 {
		t.Fatalf("handler saw the event %d times", win.rec.eventCount())
	}
	if lk, _, _ := win.db.GetLink(pix.id.DeviceID); lk.RecvCursor != 1 {
		t.Fatalf("cursor = %d", lk.RecvCursor)
	}
}

func TestApplyAckTravelsBackToTheSender(t *testing.T) {
	win, pix := pairWin(t, nil)
	e := pix.sendClip(win, "apply me")
	waitUntil(t, "delivered", func() bool { return win.rec.eventCount() == 1 })

	if err := win.e.MarkInbox(pix.id.DeviceID, e, ResultApplied, ""); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "applied ack", func() bool {
		row, _, _ := pix.db.GetOutbox(e)
		return row.AckResult == ResultApplied
	})
	if row, _, _ := win.db.GetInbox(e); row.State != "applied" || row.AppliedAt == 0 {
		t.Fatalf("inbox row = %+v", row)
	}
	if err := win.e.MarkInbox(pix.id.DeviceID, e, "bogus", ""); err == nil {
		t.Fatal("unknown result accepted")
	}
}

func TestExpiredEventsAreSkippedAndDoNotStallTheLink(t *testing.T) {
	clock := newClock()
	win := newNode(t, "win", "windows", true, clock)
	pix := newNode(t, "pixel", "android", false, clock)
	win.know(pix, 0, "")
	win.start()
	waitUntil(t, "listen", func() bool { return win.e.ListenPort() != 0 })

	// while the phone is away: two short-lived events and one long-lived one
	short1, err := win.e.Send(pix.id.DeviceID, proto.KindClipboard, clip("old 1"), SendOptions{TTL: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	short2, _ := win.e.Send(pix.id.DeviceID, proto.KindClipboard, clip("old 2"), SendOptions{TTL: 2 * time.Second})
	long, _ := win.e.Send(pix.id.DeviceID, proto.KindClipboard, clip("still good"), SendOptions{TTL: time.Hour})
	clock.Advance(10 * time.Second)
	win.e.sweep() // marks the two as expired

	if row, _, _ := win.db.GetOutbox(short1); row.State != "expired" {
		t.Fatalf("short1 = %+v", row)
	}
	pix.know(win, win.e.ListenPort(), "")
	pix.start()
	waitUntil(t, "the live event arrives", func() bool { return pix.rec.eventCount() == 1 })
	if ids := pix.rec.eventIDs(); ids[0] != long {
		t.Fatalf("received %v, want only %s", ids, long)
	}
	// the cursor jumped over the expired sequences 1 and 2
	waitUntil(t, "cursor 3", func() bool { return pix.e.getLink(win.id.DeviceID).recvCursor() == 3 })
	if _, found, _ := pix.db.GetInbox(short2); found {
		t.Fatal("an expired event reached the inbox")
	}
	// and the link keeps working afterwards
	win.sendClip(pix, "next")
	waitUntil(t, "next event", func() bool { return pix.rec.eventCount() == 2 })
}

func TestReceiverRefusesAnEventThatExpiredInTransit(t *testing.T) {
	sendClock, recvClock := newClock(), newClock()
	win := newNode(t, "win", "windows", true, recvClock)
	pix := newNode(t, "pixel", "android", false, sendClock)
	win.know(pix, 0, "")
	win.start()
	waitUntil(t, "listen", func() bool { return win.e.ListenPort() != 0 })
	pix.know(win, win.e.ListenPort(), "")
	pix.start()
	pix.waitState(win.id.DeviceID, StateConnected)

	recvClock.Advance(time.Minute) // the receiver's clock runs ahead of the event's lifetime
	e, err := pix.e.Send(win.id.DeviceID, proto.KindClipboard, clip("stale"), SendOptions{TTL: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "expired ack", func() bool { a, ok := pix.rec.ackFor(e); return ok && a.result == ResultExpired })
	if win.rec.eventCount() != 0 {
		t.Fatal("an expired event was delivered")
	}
	// it still consumed its sequence on the receiver, so the next one is accepted
	pix.e.Send(win.id.DeviceID, proto.KindClipboard, clip("fresh"), SendOptions{TTL: time.Hour})
	waitUntil(t, "fresh event", func() bool { return win.rec.eventCount() == 1 })
}

func TestReconnectResumesFromTheCursor(t *testing.T) {
	win, pix := pairWin(t, nil)
	pix.sendClip(win, "before")
	waitUntil(t, "first", func() bool { return win.rec.eventCount() == 1 })

	// the windows side drops the connection; the phone reconnects on its own
	win.e.getLink(pix.id.DeviceID).session().cancel()
	waitUntil(t, "link down", func() bool { return pix.linkState(win.id.DeviceID) != StateConnected })
	during := pix.sendClip(win, "during the outage")
	pix.waitState(win.id.DeviceID, StateConnected)
	waitUntil(t, "queued event delivered", func() bool { return win.rec.eventCount() == 2 })

	ids := win.rec.eventIDs()
	if ids[1] != during || len(ids) != 2 {
		t.Fatalf("received %v", ids)
	}
	// windows can send again over the new connection
	win.sendClip(pix, "welcome back")
	waitUntil(t, "downstream", func() bool { return pix.rec.eventCount() == 1 })
}

func TestSendFailuresConsumeNoSequence(t *testing.T) {
	win, pix := pairWin(t, nil)
	if _, err := pix.e.Send("dev_unknownunknownunknownun", proto.KindClipboard, clip("x"), SendOptions{}); err == nil {
		t.Fatal("send to an unknown peer succeeded")
	}
	big := string(make([]byte, proto.MaxClipboardBytes+1))
	if _, err := pix.e.Send(win.id.DeviceID, proto.KindClipboard, clip(big), SendOptions{}); err == nil {
		t.Fatal("oversized clipboard accepted")
	}
	if _, err := pix.e.Send(win.id.DeviceID, proto.KindClipboard, map[string]any{"mime_type": "image/png", "content": "x", "size": 1}, SendOptions{}); err == nil {
		t.Fatal("unsupported mime type accepted")
	}
	pix.sendClip(win, "ok")
	waitUntil(t, "delivered", func() bool { return win.rec.eventCount() == 1 })
	if win.rec.events[0].Sequence != 1 {
		t.Fatalf("sequence = %d; failed sends must not burn sequences", win.rec.events[0].Sequence)
	}
}

func TestBackoffGrowsWhileThePeerIsUnreachable(t *testing.T) {
	// a server that accepts and slams the door: every dial attempt is visible
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	var times []time.Time
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			times = append(times, time.Now())
			mu.Unlock()
			c.Close()
		}
	}()

	win := newNode(t, "win", "windows", true, nil)
	pix := newNode(t, "pixel", "android", false, nil, func(c *Config) {
		c.Timing.BackoffBase = 60 * time.Millisecond
		c.Timing.BackoffMax = 480 * time.Millisecond
	})
	pix.know(win, ln.Addr().(*net.TCPAddr).Port, "")
	pix.start()

	waitUntil(t, "6 attempts", func() bool { mu.Lock(); defer mu.Unlock(); return len(times) >= 6 })
	mu.Lock()
	defer mu.Unlock()
	var gaps []time.Duration
	for i := 1; i < len(times); i++ {
		gaps = append(gaps, times[i].Sub(times[i-1]))
	}
	// 60, 120, 240, 480, 480 ms (±20% jitter): later gaps are clearly longer than the first
	if gaps[3] < gaps[0]*2 || gaps[4] > 700*time.Millisecond {
		t.Fatalf("gaps = %v; expected exponential growth capped at ~480ms", gaps)
	}
	if st := pix.linkState(win.id.DeviceID); st == StateConnected {
		t.Fatalf("state = %s", st)
	}
}
