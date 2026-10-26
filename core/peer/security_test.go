package peer

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"nightclip/core/proto"
)

// controlEnvelope is a valid control event whose payload and lifetime the caller can bend.
func controlEnvelope(t *testing.T, src, dst string, seq uint64, pad int, life time.Duration) json.RawMessage {
	t.Helper()
	payload := map[string]any{"action": "noop", "pad": strings.Repeat("A", pad)}
	canon, err := proto.Canonical(payload)
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := proto.ContentHash(json.RawMessage(canon))
	now := time.Now().UTC().Truncate(time.Second)
	return mustJSON(t, proto.Envelope{
		Version: 1, EventID: newEventID(), Kind: proto.KindControl, SourceDeviceID: src, TargetDeviceID: dst,
		Sequence: seq, CreatedAt: now, ExpiresAt: now.Add(life), ContentHash: hash, Payload: canon,
	})
}

// Security review: a paired peer sending only control/file events must not be able to fill
// the receiver's database with large or long-lived rows.
func TestOversizedAndLongLivedControlEventsAreRefusedAndNotStored(t *testing.T) {
	win, pix, port := winWithRawPeer(t)
	c, _, err := rawConnect(t, port, pix.id, win.id.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	c.hello(pix.id.DeviceID, 0)

	big := controlEnvelope(t, pix.id.DeviceID, win.id.DeviceID, 1, 100_000, time.Hour)
	c.event(big)
	if a := c.until(frameAck, 2*time.Second); a["event_id"] != envID(big) || a["result"] != "error" || a["reason"] != "TOO_LARGE" {
		t.Fatalf("oversized control: ack = %v", a)
	}
	forever := controlEnvelope(t, pix.id.DeviceID, win.id.DeviceID, 2, 10, 400*24*time.Hour)
	c.event(forever)
	if a := c.until(frameAck, 2*time.Second); a["event_id"] != envID(forever) || a["result"] != "error" || a["reason"] != "INVALID" {
		t.Fatalf("long-lived control: ack = %v", a)
	}
	small := controlEnvelope(t, pix.id.DeviceID, win.id.DeviceID, 3, 10, time.Hour)
	c.event(small)
	if a := c.until(frameAck, 2*time.Second); a["event_id"] != envID(small) || a["result"] != "received" {
		t.Fatalf("small control: ack = %v", a)
	}
	rows, _ := win.db.ListInbox(0)
	if len(rows) != 1 {
		t.Fatalf("inbox holds %d rows; only the small event may be stored", len(rows))
	}
}

func TestTheInboxIsBoundedForEveryKind(t *testing.T) {
	old := maxInboxRows
	maxInboxRows = 40
	t.Cleanup(func() { maxInboxRows = old })
	win, pix, port := winWithRawPeer(t)
	c, _, err := rawConnect(t, port, pix.id, win.id.Fingerprint())
	if err != nil {
		t.Fatal(err)
	}
	c.hello(pix.id.DeviceID, 0)
	n := maxInboxRows + 20
	for i := 1; i <= n; i++ {
		c.event(controlEnvelope(t, pix.id.DeviceID, win.id.DeviceID, uint64(i), 1, time.Hour))
		c.until(frameAck, 2*time.Second) // keep the connection drained
	}
	waitUntil(t, "all events processed", func() bool {
		l, _, _ := win.db.GetLink(pix.id.DeviceID)
		return l.RecvCursor >= int64(n)
	})
	rows, _ := win.db.ListInbox(0)
	if len(rows) > maxInboxRows {
		t.Fatalf("inbox holds %d rows, cap is %d", len(rows), maxInboxRows)
	}
}
