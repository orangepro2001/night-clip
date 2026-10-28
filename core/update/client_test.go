package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nightclip/core/update/updatetest"
	"relhub/pkg/relproto"
)

func TestValidateBaseURL(t *testing.T) {
	good := map[string]string{
		"https://host.ts.net/release":     "https://host.ts.net/release",
		"  https://host.ts.net/release/ ": "https://host.ts.net/release",
		"http://127.0.0.1:18790":          "http://127.0.0.1:18790",
		"http://localhost:18790/release":  "http://localhost:18790/release",
		"http://[::1]:18790":              "http://[::1]:18790",
	}
	for in, want := range good {
		if got, err := ValidateBaseURL(in); err != nil || got != want {
			t.Errorf("ValidateBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "host/release", "ftp://host/release", "http://host.ts.net/release", "http://192.168.1.5/release",
		"https://user:pw@host/release", "https://host/release?x=1", "https://host/release#f", "https://",
	} {
		if got, err := ValidateBaseURL(in); err == nil {
			t.Errorf("ValidateBaseURL(%q) = %q, want an error", in, got)
		}
	}
}

func TestCompiledInTrustedKeysAreValid(t *testing.T) {
	// Guards against shipping with a placeholder or an empty trust list, which
	// would make every update unverifiable.
	if len(trustedKeysB64) == 0 || len(TrustedKeys()) != len(trustedKeysB64) {
		t.Fatalf("trusted keys: %d configured, %d parse", len(trustedKeysB64), len(TrustedKeys()))
	}
}

func TestClientCheckAndDownload(t *testing.T) {
	f := updatetest.New(t)
	body := payloadBytes(300_000, 9)
	m := f.Publish(PlatformAndroid, 4, "0.1.4", body, false)
	c := fakeClient(f, serve(t, f.Handler()), PlatformAndroid)
	c.UserAgent = "clipbridge/0.1.3"

	got, err := c.Check(context.Background(), 3)
	if err != nil || got.VersionCode != 4 || got.Notes != "notes 0.1.4" {
		t.Fatalf("Check = %+v, %v", got, err)
	}
	if len(f.Checks()) != 1 || f.Checks()[0] != 3 {
		t.Fatalf("the service must be told the installed version (it counts checks per version): %v", f.Checks())
	}
	if f.Agents()[0] != "clipbridge/0.1.3" {
		t.Fatalf("User-Agent = %q", f.Agents()[0])
	}

	dir := t.TempDir()
	var last, calls int64
	path, err := c.Download(context.Background(), got, dir, func(rcv, total int64) {
		if total != m.Size || rcv < last {
			t.Errorf("progress went wrong: %d/%d after %d", rcv, total, last)
		}
		last = rcv
		calls++
	})
	if err != nil {
		t.Fatal(err)
	}
	if last != m.Size || calls == 0 {
		t.Fatalf("progress ended at %d after %d calls", last, calls)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, body) {
		t.Fatal("downloaded bytes differ")
	}
	if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
		t.Fatal(".part left behind")
	}

	// A verified file already present is returned without any request.
	c.BaseURL = "http://127.0.0.1:1"
	if p2, err := c.Download(context.Background(), got, dir, nil); err != nil || p2 != path {
		t.Fatalf("cached download = %q, %v", p2, err)
	}
}

func TestClientCheckErrors(t *testing.T) {
	f := updatetest.New(t)
	ctx := context.Background()

	// Nothing published, and an app that is not registered: both mean "no release".
	base := serve(t, f.Handler())
	if _, err := fakeClient(f, base, PlatformAndroid).Check(ctx, 1); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("empty server: %v, want ErrNoRelease", err)
	}
	f.Publish(PlatformAndroid, 1, "0.1.1", payloadBytes(10, 1), false)
	other := fakeClient(f, base, PlatformAndroid)
	other.App = "someone-else"
	if _, err := other.Check(ctx, 1); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("unregistered app: %v, want ErrNoRelease", err)
	}

	otherKey, _ := newKey(t)
	c := &Client{BaseURL: base, Platform: PlatformAndroid, Keys: []ed25519.PublicKey{otherKey}}
	if _, err := c.Check(ctx, 1); code(err) != "UPDATE_REJECTED" {
		t.Fatalf("untrusted signature: %v", err)
	}
	if _, err := (&Client{BaseURL: base, Platform: PlatformAndroid}).Check(ctx, 1); code(err) != "UPDATE_REJECTED" {
		t.Fatalf("a client without trusted keys must refuse everything (the SDK would skip verification): %v", err)
	}

	// A correctly signed manifest for another platform or another app is refused.
	win := f.Publish(PlatformWindows, 1, "0.1.1", payloadBytes(10, 1), false)
	otherApp := f.PublishWith(PlatformAndroid, 2, "0.1.2", payloadBytes(10, 1), false, func(m *relproto.Manifest) { m.App = "nightdesk" })
	for name, m := range map[string]*Manifest{"another platform": win, "another app": otherApp} {
		crossed := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(relproto.CheckResult{Manifest: m})
		}))
		if _, err := fakeClient(f, crossed, PlatformAndroid).Check(ctx, 1); code(err) != "UPDATE_REJECTED" {
			t.Errorf("%s: %v", name, err)
		}
	}

	// A signed manifest that announces an absurd package is refused before any download.
	f2 := updatetest.New(t)
	f2.PublishWith(PlatformAndroid, 1, "0.1.1", payloadBytes(10, 1), false, func(m *relproto.Manifest) { m.Size = MaxPackageBytes + 1 })
	if _, err := fakeClient(f2, serve(t, f2.Handler()), PlatformAndroid).Check(ctx, 0); code(err) != "UPDATE_REJECTED" {
		t.Fatalf("oversized package: %v", err)
	}

	for name, h := range map[string]http.HandlerFunc{
		"not json": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
		"5xx":      func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", 500) },
		"503":      func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", 503) },
		// A proxy without the /release mount answers 404 without the service's JSON envelope:
		// that is a broken feed, which must not look like "no update".
		"proxy 404": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
	} {
		if _, err := fakeClient(f, serve(t, h), PlatformAndroid).Check(ctx, 1); code(err) != "UPDATE_FAILED" {
			t.Errorf("%s: err = %v, want UPDATE_FAILED", name, err)
		}
	}
	if _, err := fakeClient(f, "http://127.0.0.1:1", PlatformAndroid).Check(ctx, 1); code(err) != "UPDATE_FAILED" {
		t.Fatalf("connection refused: %v", err)
	}
}

func TestClientResumesPartialDownload(t *testing.T) {
	f := updatetest.New(t)
	body := payloadBytes(100_000, 3)
	m := f.Publish(PlatformAndroid, 2, "0.1.2", body, false)
	c := fakeClient(f, serve(t, f.Handler()), PlatformAndroid)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, m.File+".part"), body[:40_000], 0o600)

	var first int64 = -1
	path, err := c.Download(context.Background(), m, dir, func(rcv, _ int64) {
		if first < 0 {
			first = rcv
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Ranges()) != 1 || f.Ranges()[0] != "bytes=40000-" {
		t.Fatalf("Range headers = %q, want one bytes=40000-", f.Ranges())
	}
	if f.IfRanges()[0] != `"`+m.SHA256+`"` {
		t.Fatalf("If-Range = %q: a resume must be tied to the build it started on", f.IfRanges()[0])
	}
	if first <= 40_000 {
		t.Fatalf("first progress %d did not continue from the partial file", first)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, body) {
		t.Fatal("resumed file differs")
	}
}

func TestClientHandlesServerIgnoringRange(t *testing.T) {
	f := updatetest.New(t)
	f.IgnoreRange(true)
	body := payloadBytes(50_000, 8)
	m := f.Publish(PlatformAndroid, 2, "0.1.2", body, false)
	c := fakeClient(f, serve(t, f.Handler()), PlatformAndroid)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, m.File+".part"), body[:10_000], 0o600)
	path, err := c.Download(context.Background(), m, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, body) {
		t.Fatal("200-after-Range must replace the partial file, not append to it")
	}
}

func TestClientRestartsWhenPartialIsLargerOrUnsatisfiable(t *testing.T) {
	f := updatetest.New(t)
	body := payloadBytes(20_000, 2)
	m := f.Publish(PlatformAndroid, 2, "0.1.2", body, false)
	real := f.Handler()
	var refusals atomic.Int64
	c := fakeClient(f, serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Range") != "" && refusals.Add(1) == 1 {
			http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		real.ServeHTTP(w, req)
	})), PlatformAndroid)

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, m.File+".part"), payloadBytes(30_000, 1), 0o600) // longer than the release
	path, err := c.Download(context.Background(), m, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, body) {
		t.Fatal("oversized partial file was not discarded")
	}

	dir2 := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir2, m.File+".part"), body[:5_000], 0o600) // the server answers 416 once
	path, err = c.Download(context.Background(), m, dir2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, body) || refusals.Load() != 1 {
		t.Fatalf("416 must restart from zero (refusals=%d)", refusals.Load())
	}
}

func TestClientRejectsWrongBytes(t *testing.T) {
	f := updatetest.New(t)
	m := f.Publish(PlatformAndroid, 2, "0.1.2", payloadBytes(20_000, 2), false)

	tests := map[string]http.HandlerFunc{
		"same size, different content": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payloadBytes(20_000, 77)) },
		"too long":                     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payloadBytes(25_000, 2)) },
		"another build": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Content-SHA256", sha([]byte("x")))
			_, _ = w.Write(payloadBytes(20_000, 2))
		},
	}
	for name, h := range tests {
		t.Run(name, func(t *testing.T) {
			c := fakeClient(f, serve(t, h), PlatformAndroid)
			dir := t.TempDir()
			_, err := c.Download(context.Background(), m, dir, nil)
			if code(err) != "HASH_MISMATCH" {
				t.Fatalf("err = %v, want HASH_MISMATCH", err)
			}
			if entries, _ := os.ReadDir(dir); len(entries) != 0 {
				t.Fatalf("bad download left files behind: %v", entries)
			}
		})
	}

	t.Run("truncated keeps part for resume", func(t *testing.T) {
		c := fakeClient(f, serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "20000")
			_, _ = w.Write(payloadBytes(8_000, 2))
			// returning early with a declared length shorter than sent → connection error
		})), PlatformAndroid)
		dir := t.TempDir()
		_, err := c.Download(context.Background(), m, dir, nil)
		if code(err) != "UPDATE_FAILED" {
			t.Fatalf("err = %v, want UPDATE_FAILED", err)
		}
		if st, err := os.Stat(filepath.Join(dir, m.File+".part")); err != nil || st.Size() != 8_000 {
			t.Fatalf(".part = %v, %v; want 8000 bytes kept", st, err)
		}
	})

	t.Run("withdrawn release", func(t *testing.T) {
		c := fakeClient(f, serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			updatetest.APIError(w, 404, relproto.CodeNotFound, "release 2 was withdrawn")
		})), PlatformAndroid)
		if _, err := c.Download(context.Background(), m, t.TempDir(), nil); code(err) != "UPDATE_FAILED" {
			t.Fatalf("err = %v, want UPDATE_FAILED", err)
		}
	})
}

// A runaway server must not fill the disk: the body is cut one byte past the
// announced size, which the SDK then reports as a mismatch.
func TestClientCapsAnEndlessBody(t *testing.T) {
	f := updatetest.New(t)
	m := f.Publish(PlatformAndroid, 2, "0.1.2", payloadBytes(20_000, 2), false)
	var sent atomic.Int64
	c := fakeClient(f, serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		chunk := payloadBytes(8_192, 2)
		for req.Context().Err() == nil {
			n, err := w.Write(chunk)
			sent.Add(int64(n))
			if err != nil || sent.Load() > 50<<20 {
				return
			}
		}
	})), PlatformAndroid)
	dir := t.TempDir()
	if _, err := c.Download(context.Background(), m, dir, nil); code(err) != "HASH_MISMATCH" {
		t.Fatalf("err = %v, want HASH_MISMATCH", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files left behind: %v", entries)
	}
	if sent.Load() > 5<<20 {
		t.Fatalf("the client kept reading an endless body (%d bytes sent)", sent.Load())
	}
}

func TestClientStallThenResume(t *testing.T) {
	f := updatetest.New(t)
	body := payloadBytes(40_000, 6)
	m := f.Publish(PlatformAndroid, 2, "0.1.2", body, false)
	real := f.Handler()
	var stall atomic.Bool
	stall.Store(true)
	c := fakeClient(f, serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasSuffix(req.URL.Path, "/download") || !stall.Load() {
			real.ServeHTTP(w, req)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = w.Write(body[:10_000])
		w.(http.Flusher).Flush()
		<-req.Context().Done() // send nothing more until the client gives up
	})), PlatformAndroid)
	c.Stall = 150 * time.Millisecond
	dir := t.TempDir()

	start := time.Now()
	if _, err := c.Download(context.Background(), m, dir, nil); code(err) != "UPDATE_FAILED" {
		t.Fatalf("stalled download: err = %v, want UPDATE_FAILED", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("stall guard did not fire promptly")
	}
	if st, err := os.Stat(filepath.Join(dir, m.File+".part")); err != nil || st.Size() == 0 {
		t.Fatalf(".part = %v, %v; the bytes that did arrive must be kept", st, err)
	}
	stall.Store(false)
	path, err := c.Download(context.Background(), m, dir, nil)
	if err != nil {
		t.Fatalf("resume after stall: %v", err)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, body) {
		t.Fatal("resumed file differs")
	}
}

func TestClientRefusesCrossHostRedirect(t *testing.T) {
	f := updatetest.New(t)
	body := payloadBytes(100, 1)
	m := f.Publish(PlatformAndroid, 2, "0.1.2", body, false)
	var reached atomic.Bool
	evil := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached.Store(true); _, _ = w.Write(body) }))
	front := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, evil+req.URL.Path, http.StatusFound)
	}))
	c := fakeClient(f, front, PlatformAndroid)
	if _, err := c.Download(context.Background(), m, t.TempDir(), nil); code(err) != "UPDATE_FAILED" {
		t.Fatalf("cross-host redirect followed: %v", err)
	}
	if _, err := c.Check(context.Background(), 1); code(err) != "UPDATE_FAILED" {
		t.Fatalf("cross-host redirect on check: %v", err)
	}
	if reached.Load() {
		t.Fatal("the redirect target was contacted")
	}
}

// Security review: an https download must not be moved to http by a redirect.
func TestRedirectToAnotherSchemeIsRefused(t *testing.T) {
	first, _ := http.NewRequest("GET", "https://tony-ai-pc-1.example/release/v1/apps/clipbridge/check", nil)
	same, _ := http.NewRequest("GET", "https://tony-ai-pc-1.example/release/v1/apps/clipbridge/check/", nil)
	downgrade, _ := http.NewRequest("GET", "http://tony-ai-pc-1.example/release/v1/apps/clipbridge/check", nil)
	for name, hc := range map[string]*http.Client{"default": defaultHTTP, "watch": watchHTTP} {
		if err := hc.CheckRedirect(same, []*http.Request{first}); err != nil {
			t.Fatalf("%s: same-scheme redirect refused: %v", name, err)
		}
		if err := hc.CheckRedirect(downgrade, []*http.Request{first}); err == nil {
			t.Fatalf("%s: https -> http redirect was followed", name)
		}
	}
}

func TestClientWatch(t *testing.T) {
	f := updatetest.New(t)
	base := serve(t, f.Handler())
	c := fakeClient(f, base, PlatformAndroid)
	ctx := context.Background()

	if m, err := c.Watch(ctx, 0, 200*time.Millisecond); m != nil || err != nil {
		t.Fatalf("nothing published: %v, %v; want nil, nil", m, err)
	}
	f.Publish(PlatformAndroid, 3, "0.1.3", payloadBytes(50, 1), false)
	m, err := c.Watch(ctx, 2, time.Second)
	if err != nil || m == nil || m.VersionCode != 3 {
		t.Fatalf("Watch = %+v, %v", m, err)
	}
	if m, err := c.Watch(ctx, 3, 200*time.Millisecond); m != nil || err != nil {
		t.Fatalf("known version: %v, %v; want nil, nil", m, err)
	}

	// A release published while the request is parked arrives at once.
	got := make(chan *Manifest, 1)
	go func() {
		m, _ := c.Watch(ctx, 3, 10*time.Second)
		got <- m
	}()
	time.Sleep(200 * time.Millisecond)
	f.Publish(PlatformAndroid, 4, "0.1.4", payloadBytes(50, 2), false)
	select {
	case m := <-got:
		if m == nil || m.VersionCode != 4 {
			t.Fatalf("parked watch delivered %+v", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a parked watch did not return after a publish")
	}

	otherKey, _ := newKey(t)
	bad := &Client{BaseURL: base, Platform: PlatformAndroid, Keys: []ed25519.PublicKey{otherKey}}
	if _, err := bad.Watch(ctx, 0, time.Second); code(err) != "UPDATE_REJECTED" {
		t.Fatalf("untrusted signature via watch: %v", err)
	}

	unregistered := fakeClient(f, base, PlatformAndroid)
	unregistered.App = "someone-else"
	if _, err := unregistered.Watch(ctx, 0, time.Second); !errors.Is(err, ErrWatchUnsupported) {
		t.Fatalf("unregistered app: %v, want ErrWatchUnsupported", err)
	}
	proxy404 := serve(t, http.NotFoundHandler())
	if _, err := fakeClient(f, proxy404, PlatformAndroid).Watch(ctx, 0, time.Second); code(err) != "UPDATE_FAILED" {
		t.Fatalf("a 404 that is not the service's own must be an error, not 'no watch': %v", err)
	}
	if _, err := fakeClient(f, "http://127.0.0.1:1", PlatformAndroid).Watch(ctx, 0, time.Second); code(err) != "UPDATE_FAILED" {
		t.Fatalf("unreachable: %v", err)
	}
	cctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err := c.Watch(cctx, 4, 5*time.Second); err == nil {
		t.Fatal("cancelled watch returned no error")
	}
}
