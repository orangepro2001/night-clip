package facade

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nightclip/core/update"
	"nightclip/core/update/updatetest"
	"relhub/pkg/relproto"
)

// updateFixture serves one windows release signed with a throwaway key (a
// fake release-publish) and points the facade's trust list at that key.
type updateFixture struct {
	url  string
	name string
	code int64
	body []byte
	srv  *updatetest.Server
}

func newUpdateFixture(t *testing.T, code int64, force bool) *updateFixture {
	t.Helper()
	srv := updatetest.New(t)
	old := updateKeys
	updateKeys = func() []ed25519.PublicKey { return []ed25519.PublicKey{srv.PublicKey()} }
	t.Cleanup(func() { updateKeys = old })

	body := make([]byte, 64_000)
	for i := range body {
		body[i] = byte(i * 13)
	}
	name := "0.1." + string(rune('0'+code%10))
	srv.PublishWith(update.PlatformWindows, code, name, body, force, func(m *relproto.Manifest) { m.Notes = "hello" })
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)
	return &updateFixture{url: hs.URL, name: name, code: code, body: body, srv: srv}
}

func updateCfg(t *testing.T, dir string, mutate func(map[string]any)) string {
	return configFor(t, dir, mutate)
}

func nextEventOfType(t *testing.T, c *Core, typ string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw := c.NextEvent(200)
		if raw == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatalf("bad event %q", raw)
		}
		if ev["type"] == typ {
			return ev
		}
	}
	t.Fatalf("no %s event", typ)
	return nil
}

func result(t *testing.T, c *Core, method, params string) map[string]any {
	t.Helper()
	r := decode(t, c.Call(method, params))
	if !r.OK {
		t.Fatalf("%s failed: %+v", method, r)
	}
	var out map[string]any
	if err := json.Unmarshal(r.Result, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestUpdateDisabledWithoutServerOrVersion(t *testing.T) {
	c, _ := newCore(t) // no update_url, no app_version_code
	st := result(t, c, "update.status", "{}")
	if st["enabled"] != false || st["state"] != "idle" {
		t.Fatalf("status = %v", st)
	}
	for _, m := range []string{"update.check", "update.download"} {
		if r := decode(t, c.Call(m, "{}")); r.OK || r.Error.Code != "UPDATE_DISABLED" {
			t.Fatalf("%s = %+v, want UPDATE_DISABLED", m, r)
		}
	}

	// A dev build (version 0) stays off even with a server configured.
	fx := newUpdateFixture(t, 9, false)
	d := New()
	defer d.Shutdown()
	if err := d.Init(updateCfg(t, t.TempDir(), func(m map[string]any) { m["update_url"] = fx.url; m["platform"] = "windows" })); err != nil {
		t.Fatal(err)
	}
	if r := decode(t, d.Call("update.check", "{}")); r.OK || r.Error.Code != "UPDATE_DISABLED" {
		t.Fatalf("dev build update.check = %+v", r)
	}
}

func TestUpdateCheckDownloadReadyThroughFacade(t *testing.T) {
	fx := newUpdateFixture(t, 9, false)
	dir := t.TempDir()
	c := New()
	defer c.Shutdown()
	if err := c.Init(updateCfg(t, dir, func(m map[string]any) {
		m["update_url"] = fx.url
		m["app_version_code"] = 5
		m["app_version_name"] = "0.1.5"
	})); err != nil {
		t.Fatal(err)
	}
	// auto is on by default: update.check finds the release and fetches it.
	st := result(t, c, "update.check", "{}")
	if st["enabled"] != true || st["auto"] != true {
		t.Fatalf("status = %v", st)
	}
	latest := st["latest"].(map[string]any)
	if latest["version_code"].(float64) != 9 || latest["notes"] != "hello" {
		t.Fatalf("latest = %v", latest)
	}

	// The service is told who is asking: the installed version (it counts checks per
	// version, `relhub stats`) and the app version in the User-Agent (its access log).
	if got := fx.srv.Checks(); len(got) == 0 || got[0] != 5 {
		t.Fatalf("installed versionCode sent to the service = %v, want 5 first", got)
	}
	if got := fx.srv.Agents(); len(got) == 0 || got[0] != "clipbridge/0.1.5" {
		t.Fatalf("User-Agent = %v", got)
	}

	ev := nextEventOfType(t, c, "update.ready")
	path := ev["path"].(string)
	if ev["version_code"].(float64) != 9 || ev["mandatory"] != false || !strings.HasPrefix(path, filepath.Join(dir, "updates")) {
		t.Fatalf("update.ready = %v", ev)
	}
	if data, _ := os.ReadFile(path); string(data) != string(fx.body) {
		t.Fatal("downloaded package differs from the published one")
	}
	if st := result(t, c, "update.status", "{}"); st["state"] != "ready" || st["ready_path"] != path {
		t.Fatalf("status after ready = %v", st)
	}
}

func TestUpdateManualModeWaitsForDownload(t *testing.T) {
	fx := newUpdateFixture(t, 9, true) // mandatory for old installs
	c := New()
	defer c.Shutdown()
	if err := c.Init(updateCfg(t, t.TempDir(), func(m map[string]any) {
		m["update_url"] = fx.url
		m["app_version_code"] = 5
	})); err != nil {
		t.Fatal(err)
	}
	if cfg := result(t, c, "update.config", `{"auto":false}`); cfg["auto"] != false {
		t.Fatalf("config = %v", cfg)
	}
	st := result(t, c, "update.check", "{}")
	if st["state"] != "available" || st["mandatory"] != true {
		t.Fatalf("status = %v", st)
	}
	av := nextEventOfType(t, c, "update.available")
	if av["mandatory"] != true || av["version_code"].(float64) != 9 {
		t.Fatalf("update.available = %v", av)
	}
	time.Sleep(100 * time.Millisecond)
	if st := result(t, c, "update.status", "{}"); st["state"] != "available" {
		t.Fatalf("manual mode downloaded on its own: %v", st)
	}
	result(t, c, "update.download", "{}")
	nextEventOfType(t, c, "update.ready")
}

func TestUpdateSettingsPersistAcrossRestarts(t *testing.T) {
	fx := newUpdateFixture(t, 9, false)
	dir := t.TempDir()
	cfg := func(m map[string]any) {
		m["update_url"] = "https://default.example/clipbridge"
		m["app_version_code"] = 5
	}

	c := New()
	if err := c.Init(updateCfg(t, dir, cfg)); err != nil {
		t.Fatal(err)
	}
	if got := result(t, c, "update.config", "{}"); got["url"] != "https://default.example/clipbridge" || got["auto"] != true {
		t.Fatalf("defaults = %v", got)
	}
	result(t, c, "update.config", `{"auto":false,"url":"`+fx.url+`"}`)
	c.Shutdown()

	d := New()
	defer d.Shutdown()
	if err := d.Init(updateCfg(t, dir, cfg)); err != nil {
		t.Fatal(err)
	}
	got := result(t, d, "update.config", "{}")
	if got["auto"] != false || got["url"] != fx.url || got["default_url"] != "https://default.example/clipbridge" {
		t.Fatalf("settings after restart = %v", got)
	}
	if st := result(t, d, "update.check", "{}"); st["state"] != "available" {
		t.Fatalf("override server not used after restart: %v", st)
	}
	// An empty url restores the build-time default.
	got = result(t, d, "update.config", `{"url":""}`)
	if got["url"] != "https://default.example/clipbridge" {
		t.Fatalf("after reset = %v", got)
	}
}

func TestUpdateConfigValidation(t *testing.T) {
	c, _ := newCore(t)
	for _, params := range []string{`{"url":"http://example.com/x"}`, `{"url":"ftp://x"}`, `{"url":"https://u:p@h/"}`, `not json`, `{"auto":"yes"}`} {
		if r := decode(t, c.Call("update.config", params)); r.OK || r.Error.Code != "INVALID" {
			t.Errorf("update.config %s = %+v, want INVALID", params, r)
		}
	}
	// Nothing was persisted by the rejected calls.
	if got := result(t, c, "update.config", "{}"); got["url"] != "" || got["auto"] != true {
		t.Fatalf("config after rejects = %v", got)
	}
}

func TestUpdateRejectsUntrustedServer(t *testing.T) {
	fx := newUpdateFixture(t, 9, false)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	updateKeys = func() []ed25519.PublicKey { return []ed25519.PublicKey{other} } // fixture key no longer trusted
	c := New()
	defer c.Shutdown()
	if err := c.Init(updateCfg(t, t.TempDir(), func(m map[string]any) {
		m["update_url"] = fx.url
		m["app_version_code"] = 5
	})); err != nil {
		t.Fatal(err)
	}
	if r := decode(t, c.Call("update.check", "{}")); r.OK || r.Error.Code != "UPDATE_REJECTED" {
		t.Fatalf("update.check with an untrusted signature = %+v", r)
	}
	if st := result(t, c, "update.status", "{}"); st["state"] != "error" {
		t.Fatalf("status = %v", st)
	}
}

func TestUpdateCallsAfterShutdown(t *testing.T) {
	c, _ := newCore(t)
	c.Shutdown()
	if r := decode(t, c.Call("update.status", "{}")); r.OK || r.Error.Code != "NOT_INITIALIZED" {
		t.Fatalf("update.status after Shutdown = %+v", r)
	}
	if r := decode(t, New().Call("update.check", "{}")); r.OK || r.Error.Code != "NOT_INITIALIZED" {
		t.Fatalf("update.check before Init = %+v", r)
	}
}
