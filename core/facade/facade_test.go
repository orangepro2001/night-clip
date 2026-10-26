package facade

import (
	"encoding/json"
	"testing"
	"time"
)

type reply struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  struct {
		Code string `json:"code"`
	} `json:"error"`
}

func decode(t *testing.T, s string) reply {
	t.Helper()
	var r reply
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		t.Fatalf("bad reply %q: %v", s, err)
	}
	return r
}

const cfg = `{"device_name":"Tony-PC","platform":"windows","listen":true}`

func TestStatusBeforeInitFails(t *testing.T) {
	r := decode(t, New().Call("status", "{}"))
	if r.OK || r.Error.Code != "NOT_INITIALIZED" {
		t.Fatalf("got %+v", r)
	}
}

func TestInitStatusAndStartedEvent(t *testing.T) {
	c := New()
	if err := c.Init(cfg); err != nil {
		t.Fatal(err)
	}
	r := decode(t, c.Call("status", "{}"))
	if !r.OK {
		t.Fatalf("status failed: %+v", r)
	}
	var st struct {
		DeviceID string `json:"device_id"`
		Port     int    `json:"port"`
	}
	if err := json.Unmarshal(r.Result, &st); err != nil || st.Port != 47821 || st.DeviceID == "" {
		t.Fatalf("bad status %s (%v)", r.Result, err)
	}
	var ev struct{ Type, DeviceID string }
	raw := c.NextEvent(100)
	if err := json.Unmarshal([]byte(raw), &ev); err != nil || ev.Type != "core.started" {
		t.Fatalf("expected core.started, got %q", raw)
	}
}

func TestInitRejectsBadConfigAndDoubleInit(t *testing.T) {
	for _, bad := range []string{`not json`, `{"device_name":"x","platform":"linux"}`, `{"platform":"windows"}`} {
		if err := New().Init(bad); err == nil {
			t.Fatalf("expected error for %s", bad)
		}
	}
	c := New()
	if err := c.Init(cfg); err != nil {
		t.Fatal(err)
	}
	if err := c.Init(cfg); err == nil {
		t.Fatal("second Init should fail")
	}
}

func TestUnknownMethod(t *testing.T) {
	r := decode(t, New().Call("nope", ""))
	if r.OK || r.Error.Code != "METHOD_NOT_FOUND" {
		t.Fatalf("got %+v", r)
	}
}

func TestNextEventTimeoutAndShutdown(t *testing.T) {
	c := New()
	start := time.Now()
	if got := c.NextEvent(30); got != "" {
		t.Fatalf("expected timeout, got %q", got)
	}
	if time.Since(start) < 25*time.Millisecond {
		t.Fatal("returned before timeout")
	}
	go func() { time.Sleep(20 * time.Millisecond); c.Shutdown() }()
	start = time.Now()
	if got := c.NextEvent(5000); got != "" || time.Since(start) > time.Second {
		t.Fatalf("shutdown did not wake NextEvent (got %q after %s)", got, time.Since(start))
	}
}

func TestEventQueueOverflowDropsAndCounts(t *testing.T) {
	c := New()
	for i := 0; i < eventQueueSize+10; i++ {
		c.emit(map[string]any{"type": "x"})
	}
	if c.dropped.Load() != 10 {
		t.Fatalf("dropped = %d, want 10", c.dropped.Load())
	}
}

func TestCallRecoversFromPanic(t *testing.T) {
	// Dereferencing a nil *Core inside dispatch panics; Call must convert it.
	var nilCore *Core
	r := decode(t, nilCore.Call("status", ""))
	if r.OK || r.Error.Code != "INTERNAL" {
		t.Fatalf("got %+v", r)
	}
}
