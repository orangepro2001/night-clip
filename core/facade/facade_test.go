package facade

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"nightclip/core/proto"
	"nightclip/core/store"
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

// testKeyB64 is a fixed 32-byte key, for tests only.
func testKeyB64(seed byte) string {
	k := make([]byte, 32)
	for i := range k {
		k[i] = seed + byte(i)
	}
	return base64.StdEncoding.EncodeToString(k)
}

// configFor builds a valid Init config for dir; mutate edits the raw map first.
func configFor(t *testing.T, dir string, mutate func(map[string]any)) string {
	t.Helper()
	m := map[string]any{
		"data_dir":    dir,
		"db_key_b64":  testKeyB64(1),
		"device_name": "Tony-PC",
		"platform":    "windows",
		"listen":      true,
	}
	if mutate != nil {
		mutate(m)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// newCore returns a core initialised on a fresh temp dir.
func newCore(t *testing.T) (*Core, string) {
	t.Helper()
	dir := t.TempDir()
	c := New()
	if err := c.Init(configFor(t, dir, nil)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(c.Shutdown)
	return c, dir
}

func deviceID(t *testing.T, c *Core) string {
	t.Helper()
	r := decode(t, c.Call("status", "{}"))
	if !r.OK {
		t.Fatalf("status failed: %+v", r)
	}
	var st struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.Unmarshal(r.Result, &st); err != nil || st.DeviceID == "" {
		t.Fatalf("bad status %s (%v)", r.Result, err)
	}
	return st.DeviceID
}

func errorCode(err error) string {
	var pe *proto.Error
	if errors.As(err, &pe) {
		return string(pe.Code)
	}
	return ""
}

func TestStatusBeforeInitFails(t *testing.T) {
	r := decode(t, New().Call("status", "{}"))
	if r.OK || r.Error.Code != "NOT_INITIALIZED" {
		t.Fatalf("got %+v", r)
	}
}

func TestInitStatusAndStartedEvent(t *testing.T) {
	c, _ := newCore(t)
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

func TestInitRejectsBadConfig(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		cfg  string
		code string
	}{
		{"not json", `not json`, "INVALID"},
		{"unknown platform", configFor(t, dir, func(m map[string]any) { m["platform"] = "linux" }), "INVALID"},
		{"missing device name", configFor(t, dir, func(m map[string]any) { delete(m, "device_name") }), "INVALID"},
		{"missing data_dir", configFor(t, dir, func(m map[string]any) { delete(m, "data_dir") }), "INVALID"},
		{"relative data_dir", configFor(t, dir, func(m map[string]any) { m["data_dir"] = "relative/dir" }), "INVALID"},
		{"missing key", configFor(t, dir, func(m map[string]any) { delete(m, "db_key_b64") }), "INVALID"},
		{"bad base64", configFor(t, dir, func(m map[string]any) { m["db_key_b64"] = "!!!not-base64!!!" }), "INVALID"},
		{"short key", configFor(t, dir, func(m map[string]any) {
			m["db_key_b64"] = base64.StdEncoding.EncodeToString(make([]byte, 16))
		}), "INVALID"},
		{"long key", configFor(t, dir, func(m map[string]any) {
			m["db_key_b64"] = base64.StdEncoding.EncodeToString(make([]byte, 33))
		}), "INVALID"},
		{"data_dir is a file", func() string {
			file := filepath.Join(dir, "plainfile")
			if err := writeFile(file); err != nil {
				t.Fatal(err)
			}
			return configFor(t, filepath.Join(file, "sub"), nil)
		}(), "DB_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			err := c.Init(tc.cfg)
			if err == nil {
				c.Shutdown()
				t.Fatal("expected an error")
			}
			if got := errorCode(err); got != tc.code {
				t.Fatalf("code = %q (%v), want %q", got, err, tc.code)
			}
			if got := c.NextEvent(20); got != "" {
				t.Fatalf("failed Init emitted %q", got)
			}
			if r := decode(t, c.Call("status", "{}")); r.OK || r.Error.Code != "NOT_INITIALIZED" {
				t.Fatalf("status after failed Init = %+v", r)
			}
		})
	}
}

func TestInitAcceptsUnpaddedBase64Key(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	c := New()
	defer c.Shutdown()
	if err := c.Init(configFor(t, t.TempDir(), func(m map[string]any) { m["db_key_b64"] = key })); err != nil {
		t.Fatalf("unpadded key rejected: %v", err)
	}
}

func TestInitAcceptsMacOS(t *testing.T) {
	c := New()
	defer c.Shutdown()
	if err := c.Init(configFor(t, t.TempDir(), func(m map[string]any) { m["platform"] = "macos" })); err != nil {
		t.Fatalf("macos rejected: %v", err)
	}
}

func TestDoubleInitFails(t *testing.T) {
	c, dir := newCore(t)
	if err := c.Init(configFor(t, dir, nil)); errorCode(err) != "INVALID" {
		t.Fatalf("second Init = %v, want INVALID", err)
	}
	// The first Init's identity must be untouched.
	_ = deviceID(t, c)
}

func TestFailedInitCanBeRetried(t *testing.T) {
	dir := t.TempDir()
	c := New()
	defer c.Shutdown()
	if err := c.Init(configFor(t, dir, func(m map[string]any) { delete(m, "db_key_b64") })); err == nil {
		t.Fatal("expected failure")
	}
	if err := c.Init(configFor(t, dir, nil)); err != nil {
		t.Fatalf("retry after failed Init: %v", err)
	}
	_ = deviceID(t, c)
}

// The point of the store: restarting with the same data dir and key yields the
// same device identity.
func TestDeviceIDIsStableAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	first := New()
	if err := first.Init(configFor(t, dir, nil)); err != nil {
		t.Fatal(err)
	}
	id1 := deviceID(t, first)
	ident1 := decode(t, first.Call("identity.get", "{}"))
	first.Shutdown()

	second := New()
	defer second.Shutdown()
	if err := second.Init(configFor(t, dir, nil)); err != nil {
		t.Fatal(err)
	}
	if id2 := deviceID(t, second); id2 != id1 {
		t.Fatalf("device_id changed across restart: %s -> %s", id1, id2)
	}
	ident2 := decode(t, second.Call("identity.get", "{}"))
	if string(ident1.Result) != string(ident2.Result) {
		t.Fatalf("identity.get changed across restart:\n%s\n%s", ident1.Result, ident2.Result)
	}
}

func TestDifferentDataDirsGetDifferentIdentities(t *testing.T) {
	a, _ := newCore(t)
	b, _ := newCore(t)
	if deviceID(t, a) == deviceID(t, b) {
		t.Fatal("two data dirs share a device_id")
	}
}

func TestWrongKeyIsKeyInvalidAndEmitsNothing(t *testing.T) {
	dir := t.TempDir()
	first := New()
	if err := first.Init(configFor(t, dir, nil)); err != nil {
		t.Fatal(err)
	}
	first.Shutdown()

	c := New()
	err := c.Init(configFor(t, dir, func(m map[string]any) { m["db_key_b64"] = testKeyB64(77) }))
	if errorCode(err) != "KEY_INVALID" {
		t.Fatalf("Init with the wrong key = %v, want KEY_INVALID", err)
	}
	if got := c.NextEvent(20); got != "" {
		t.Fatalf("failed Init emitted %q", got)
	}
	// The failed attempt must not have left the database locked or half-open.
	again := New()
	defer again.Shutdown()
	if err := again.Init(configFor(t, dir, nil)); err != nil {
		t.Fatalf("correct key after a failed attempt: %v", err)
	}
}

func TestPlatformMismatchAgainstStoredIdentity(t *testing.T) {
	dir := t.TempDir()
	first := New()
	if err := first.Init(configFor(t, dir, nil)); err != nil {
		t.Fatal(err)
	}
	first.Shutdown()

	c := New()
	err := c.Init(configFor(t, dir, func(m map[string]any) { m["platform"] = "android" }))
	if errorCode(err) != "INVALID" {
		t.Fatalf("Init with another platform = %v, want INVALID", err)
	}
}

func TestShutdownClosesStoreAndRejectsFurtherUse(t *testing.T) {
	dir := t.TempDir()
	c := New()
	if err := c.Init(configFor(t, dir, nil)); err != nil {
		t.Fatal(err)
	}
	_ = deviceID(t, c)

	c.Shutdown()
	c.Shutdown() // idempotent

	for _, m := range []string{"status", "identity.get", "version"} {
		if r := decode(t, c.Call(m, "{}")); r.OK || r.Error.Code != "NOT_INITIALIZED" {
			t.Fatalf("%s after Shutdown = %+v, want NOT_INITIALIZED", m, r)
		}
	}
	if err := c.Init(configFor(t, dir, nil)); errorCode(err) != "INVALID" {
		t.Fatalf("Init after Shutdown = %v, want INVALID", err)
	}

	// The database is closed: a fresh handle can open it and sees the identity.
	db, err := store.Open(dir, mustKey(t))
	if err != nil {
		t.Fatalf("reopening after Shutdown: %v", err)
	}
	defer db.Close()
	if _, err := db.LoadOrCreateIdentity("Tony-PC", "windows"); err != nil {
		t.Fatalf("identity not persisted: %v", err)
	}
}

func mustKey(t *testing.T) []byte {
	t.Helper()
	k, err := base64.StdEncoding.DecodeString(testKeyB64(1))
	if err != nil {
		t.Fatal(err)
	}
	return k
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

func writeFile(path string) error { return os.WriteFile(path, []byte("x"), 0o600) }
