package notes

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"nightclip/core/store"
)

// clock is a settable time source shared by the devices of a test.
type clock struct {
	mu sync.Mutex
	ms int64
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.UnixMilli(c.ms)
}

func (c *clock) set(ms int64) {
	c.mu.Lock()
	c.ms = ms
	c.mu.Unlock()
}

func (c *clock) tick() {
	c.mu.Lock()
	c.ms += 1000
	c.mu.Unlock()
}

// device is one simulated device: its own database and notes service.
type device struct {
	t      *testing.T
	id     string
	db     *store.DB
	svc    *Service
	mu     sync.Mutex
	events []map[string]any
}

func testKey(seed byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

func deviceID(i int) string { return "dev_" + strings.Repeat("a", 25) + string(rune('a'+i)) }

func newDevice(t *testing.T, i int, clk *clock) *device {
	t.Helper()
	db, err := store.Open(t.TempDir(), testKey(byte(i+1)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	d := &device{t: t, id: deviceID(i), db: db}
	d.svc = New(Config{DB: db, DeviceID: d.id, Now: clk.Now, Emit: func(ev map[string]any) {
		d.mu.Lock()
		d.events = append(d.events, ev)
		d.mu.Unlock()
	}})
	t.Cleanup(d.svc.Close)
	return d
}

// pair makes the devices know each other as paired peers (the sync code checks this).
func pair(t *testing.T, devs ...*device) {
	t.Helper()
	for _, a := range devs {
		for _, b := range devs {
			if a == b {
				continue
			}
			err := a.db.PutPeer(store.Peer{DeviceID: b.id, Name: "peer-" + b.id[len(b.id)-1:], Platform: "windows", CertFP: "fp-" + a.id + "-" + b.id,
				Port: 47821, Role: "auto", State: "paired", ApplyMode: "notify", PairedAt: 1})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func (d *device) eventsOf(typ string) []map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []map[string]any
	for _, e := range d.events {
		if e["type"] == typ {
			out = append(out, e)
		}
	}
	return out
}

// localRemote lets a dialing device talk to another device's service in process. Every call goes
// through JSON, so the wire shapes are exercised too.
type localRemote struct{ s *Service }

func roundTrip[T any](in any, out *T) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func (l localRemote) head(context.Context) (rep headReply, err error) {
	r, err := l.s.doHead()
	if err != nil {
		return rep, err
	}
	err = roundTrip(r, &rep)
	return
}

func (l localRemote) digest(_ context.Context, after string) (rep digestReply, err error) {
	r, err := l.s.doDigest(after)
	if err != nil {
		return rep, err
	}
	err = roundTrip(r, &rep)
	return
}

func (l localRemote) pull(_ context.Context, req pullRequest) (rep pullReply, err error) {
	var in pullRequest
	if err = roundTrip(req, &in); err != nil {
		return
	}
	r, err := l.s.doPull(in)
	if err != nil {
		return rep, err
	}
	err = roundTrip(r, &rep)
	return
}

func (l localRemote) push(_ context.Context, req pushRequest) (rep pushReply, err error) {
	var in pushRequest
	if err = roundTrip(req, &in); err != nil {
		return
	}
	r, err := l.s.doPush(in)
	if err != nil {
		return rep, err
	}
	err = roundTrip(r, &rep)
	return
}

// syncWith runs one full round in which d dials other.
func (d *device) syncWith(other *device) {
	d.t.Helper()
	d.svc.runRound(context.Background(), other.id, localRemote{other.svc}, true)
	if row, _, _ := d.db.GetNoteSync(other.id); row.State != StateIdle {
		d.t.Fatalf("sync %s -> %s ended in state %q: %s", d.id[len(d.id)-1:], other.id[len(other.id)-1:], row.State, row.Detail)
	}
}

// view is what a device shows for every note: used to compare devices.
type view struct {
	Head    string
	Title   string
	Body    string
	Deleted bool
}

func (d *device) snapshot() map[string]view {
	d.t.Helper()
	out := map[string]view{}
	for _, deleted := range []bool{false, true} {
		cursor := ""
		for {
			page, err := d.svc.List(100, cursor, deleted)
			if err != nil {
				d.t.Fatal(err)
			}
			for _, it := range page.Items {
				g, err := d.svc.Get(it.NoteID, "")
				if err != nil {
					d.t.Fatal(err)
				}
				out[it.NoteID] = view{Head: g.RevID, Title: g.Title, Body: g.Body, Deleted: g.Deleted}
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
	}
	return out
}

func (d *device) mustCreate(title, body string) string {
	d.t.Helper()
	r, err := d.svc.Create(title, body)
	if err != nil {
		d.t.Fatal(err)
	}
	return r.NoteID
}

func strp(s string) *string { return &s }
