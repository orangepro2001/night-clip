package update

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"nightclip/core/update/updatetest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newManager(t *testing.T, c *Client, current int64, auto bool, ev *events) (*Manager, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "updates")
	m := NewManager(Options{Client: c, Dir: dir, CurrentCode: current, CurrentName: "cur", Auto: auto, Emit: ev.emit})
	t.Cleanup(m.Close)
	return m, dir
}

func TestManagerManualFlow(t *testing.T) {
	f := updatetest.New(t)
	body := payloadBytes(120_000, 4)
	f.Publish(PlatformAndroid, 9, "0.1.9", body, false)
	ev := &events{}
	m, dir := newManager(t, fakeClient(f, serve(t, f.Handler()), PlatformAndroid), 5, false, ev)

	if s := m.Status(); !s.Enabled || s.State != StateIdle || s.CurrentCode != 5 {
		t.Fatalf("initial = %+v", s)
	}
	s, err := m.Check(context.Background())
	if err != nil || s.State != StateAvailable || s.Latest.VersionCode != 9 || s.Mandatory {
		t.Fatalf("after Check = %+v, %v", s, err)
	}
	if len(f.Checks()) != 1 || f.Checks()[0] != 5 {
		t.Fatalf("the service must see the installed versionCode: %v", f.Checks())
	}
	if ev.count("update.available") != 1 {
		t.Fatalf("events = %v", ev.types())
	}
	time.Sleep(50 * time.Millisecond)
	if st := m.Status().State; st != StateAvailable {
		t.Fatalf("auto is off but state = %s", st)
	}

	if _, err := m.Download(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ready", func() bool { return m.Status().State == StateReady })
	s = m.Status()
	data, _ := os.ReadFile(s.ReadyPath)
	if !bytes.Equal(data, body) || filepath.Dir(s.ReadyPath) != dir {
		t.Fatalf("ready path %q holds the wrong data", s.ReadyPath)
	}
	if ev.count("update.ready") != 1 || ev.count("update.progress") == 0 {
		t.Fatalf("events = %v", ev.types())
	}
	if _, err := m.Download(); err != nil || m.Status().State != StateReady { // idempotent
		t.Fatalf("second Download changed things: %v", err)
	}
}

func TestManagerAutoDownloadsAndReusesFile(t *testing.T) {
	f := updatetest.New(t)
	f.Publish(PlatformAndroid, 9, "0.1.9", payloadBytes(50_000, 4), false)
	ev := &events{}
	m, _ := newManager(t, fakeClient(f, serve(t, f.Handler()), PlatformAndroid), 5, true, ev)

	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "auto ready", func() bool { return m.Status().State == StateReady })

	downloads := f.Downloads()
	s, err := m.Check(context.Background()) // already on disk: no second download
	if err != nil || s.State != StateReady {
		t.Fatalf("re-check = %+v, %v", s, err)
	}
	if f.Downloads() != downloads {
		t.Fatalf("re-check downloaded again (%d -> %d requests)", downloads, f.Downloads())
	}
}

func TestManagerReadyFileFromPreviousSession(t *testing.T) {
	f := updatetest.New(t)
	body := payloadBytes(30_000, 4)
	mf := f.Publish(PlatformAndroid, 9, "0.1.9", body, false)
	ev := &events{}
	m, dir := newManager(t, fakeClient(f, serve(t, f.Handler()), PlatformAndroid), 5, false, ev)
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, mf.File), body, 0o600)

	s, err := m.Check(context.Background())
	if err != nil || s.State != StateReady || s.ReadyPath != filepath.Join(dir, mf.File) {
		t.Fatalf("Check with a verified package on disk = %+v, %v", s, err)
	}
}

func TestManagerUpToDateDisabledAndMandatory(t *testing.T) {
	f := updatetest.New(t)
	base := serve(t, f.Handler())
	ev := &events{}

	// nothing published
	m, _ := newManager(t, fakeClient(f, base, PlatformAndroid), 5, false, ev)
	if s, err := m.Check(context.Background()); err != nil || s.State != StateUpToDate {
		t.Fatalf("empty server: %+v, %v", s, err)
	}

	f.Publish(PlatformAndroid, 9, "0.1.9", payloadBytes(100, 1), true) // forced: min 9

	// same version installed → up to date, stale packages pruned
	m2, dir2 := newManager(t, fakeClient(f, base, PlatformAndroid), 9, false, ev)
	_ = os.MkdirAll(dir2, 0o700)
	_ = os.WriteFile(filepath.Join(dir2, "clipbridge-0.1.8-android.apk"), []byte("old"), 0o600)
	_ = os.WriteFile(filepath.Join(dir2, "x.apk.part"), []byte("old"), 0o600)
	if s, err := m2.Check(context.Background()); err != nil || s.State != StateUpToDate {
		t.Fatalf("current version: %+v, %v", s, err)
	}
	if entries, _ := os.ReadDir(dir2); len(entries) != 0 {
		t.Fatalf("stale packages not pruned: %v", entries)
	}

	// older install is flagged mandatory
	m3, _ := newManager(t, fakeClient(f, base, PlatformAndroid), 5, false, ev)
	if s, err := m3.Check(context.Background()); err != nil || !s.Mandatory || s.State != StateAvailable {
		t.Fatalf("older install: %+v, %v", s, err)
	}

	// dev build (code 0) and no-server managers are disabled
	for name, mgr := range map[string]*Manager{
		"dev build": NewManager(Options{Client: fakeClient(f, base, PlatformAndroid), Dir: t.TempDir(), CurrentCode: 0}),
		"no client": NewManager(Options{Dir: t.TempDir(), CurrentCode: 5}),
	} {
		if mgr.Enabled() {
			t.Errorf("%s: Enabled() = true", name)
		}
		if _, err := mgr.Check(context.Background()); code(err) != "UPDATE_DISABLED" {
			t.Errorf("%s: Check err = %v, want UPDATE_DISABLED", name, err)
		}
		if _, err := mgr.Download(); code(err) != "UPDATE_DISABLED" {
			t.Errorf("%s: Download err = %v, want UPDATE_DISABLED", name, err)
		}
	}
}

// A proxy that lost the /release mount answers 404 with no JSON envelope. The device must show an
// error, not "up to date": otherwise a reset `tailscale serve` silently stops every update.
func TestManagerBrokenFeedIsAnErrorNotUpToDate(t *testing.T) {
	f := updatetest.New(t)
	m, _ := newManager(t, fakeClient(f, serve(t, http.NotFoundHandler()), PlatformAndroid), 5, true, &events{})
	s, err := m.Check(context.Background())
	if code(err) != "UPDATE_FAILED" || s.State != StateError || s.Error == nil {
		t.Fatalf("Check against a proxy 404 = %+v, %v; want an UPDATE_FAILED error state", s, err)
	}
}

func TestManagerDownloadWithoutAvailableRelease(t *testing.T) {
	f := updatetest.New(t)
	m, _ := newManager(t, fakeClient(f, serve(t, f.Handler()), PlatformAndroid), 5, false, &events{})
	if _, err := m.Download(); code(err) != "INVALID" {
		t.Fatalf("Download before Check: err = %v, want INVALID", err)
	}
}

func TestManagerErrorsAreRecordedAndRecoverable(t *testing.T) {
	f := updatetest.New(t)
	f.Publish(PlatformAndroid, 9, "0.1.9", payloadBytes(1000, 1), false)
	real := f.Handler()
	var down atomic.Bool
	down.Store(true)
	ev := &events{}
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if down.Load() {
			http.Error(w, "down", 503)
			return
		}
		real.ServeHTTP(w, req)
	}))
	m, _ := newManager(t, fakeClient(f, srv, PlatformAndroid), 5, true, ev)

	s, err := m.Check(context.Background())
	if code(err) != "UPDATE_FAILED" || s.State != StateError || s.Error == nil || s.Error.Code != "UPDATE_FAILED" {
		t.Fatalf("failing check: %+v, %v", s, err)
	}
	if ev.count("update.error") != 0 {
		t.Fatal("a failed check must not raise update.error (it would spam while offline)")
	}
	down.Store(false)
	if s, err := m.Check(context.Background()); err != nil || s.State == StateError {
		t.Fatalf("recovery: %+v, %v", s, err)
	}
	waitFor(t, "ready after recovery", func() bool { return m.Status().State == StateReady })
}

func TestManagerDownloadFailureEmitsErrorAndAllowsRetry(t *testing.T) {
	f := updatetest.New(t)
	f.Publish(PlatformAndroid, 9, "0.1.9", payloadBytes(5000, 1), false)
	real := f.Handler()
	var corrupt atomic.Bool
	corrupt.Store(true)
	ev := &events{}
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if corrupt.Load() && strings.HasSuffix(req.URL.Path, "/download") {
			_, _ = w.Write(payloadBytes(5000, 99))
			return
		}
		real.ServeHTTP(w, req)
	}))
	m, _ := newManager(t, fakeClient(f, srv, PlatformAndroid), 5, false, ev)
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Download(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "error state", func() bool { return m.Status().State == StateError })
	if s := m.Status(); s.Error.Code != "HASH_MISMATCH" || s.Latest == nil {
		t.Fatalf("status after corrupt download = %+v", s)
	}
	if ev.count("update.error") != 1 {
		t.Fatalf("events = %v", ev.types())
	}
	corrupt.Store(false)
	if _, err := m.Download(); err != nil {
		t.Fatalf("retry: %v", err)
	}
	waitFor(t, "ready after retry", func() bool { return m.Status().State == StateReady })
}

func TestManagerBackgroundLoop(t *testing.T) {
	f := updatetest.New(t)
	f.Publish(PlatformAndroid, 9, "0.1.9", payloadBytes(2000, 1), false)
	ev := &events{}
	m := NewManager(Options{
		Client: fakeClient(f, serve(t, f.Handler()), PlatformAndroid), Dir: t.TempDir(),
		CurrentCode: 5, Auto: true, Emit: ev.emit,
		FirstDelay: 10 * time.Millisecond, Interval: 20 * time.Millisecond,
	})
	m.Run()
	t.Cleanup(m.Close)
	waitFor(t, "background ready", func() bool { return m.Status().State == StateReady })
	if ev.count("update.available") != 1 || ev.count("update.ready") != 1 {
		t.Fatalf("events = %v", ev.types())
	}
	m.Close()
	m.Close() // idempotent
	if _, err := m.Download(); err == nil {
		t.Fatal("Download after Close must fail")
	}
}

func TestManagerCloseCancelsStalledDownload(t *testing.T) {
	f := updatetest.New(t)
	body := payloadBytes(40_000, 1)
	f.Publish(PlatformAndroid, 9, "0.1.9", body, false)
	real := f.Handler()
	started := make(chan struct{}, 1)
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.HasSuffix(req.URL.Path, "/download") {
			real.ServeHTTP(w, req)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = w.Write(body[:1000])
		w.(http.Flusher).Flush()
		select {
		case started <- struct{}{}:
		default:
		}
		<-req.Context().Done()
	}))
	ev := &events{}
	m, _ := newManager(t, fakeClient(f, srv, PlatformAndroid), 5, true, ev)
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-started
	done := make(chan struct{})
	go func() { m.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung on a stalled download")
	}
	if ev.count("update.error") != 0 {
		t.Fatal("cancelling a download on Close must not look like a failure")
	}
}

func TestManagerSetClientResets(t *testing.T) {
	f := updatetest.New(t)
	f.Publish(PlatformAndroid, 9, "0.1.9", payloadBytes(500, 1), false)
	base := serve(t, f.Handler())
	m, _ := newManager(t, fakeClient(f, base, PlatformAndroid), 5, false, &events{})
	if s, _ := m.Check(context.Background()); s.State != StateAvailable {
		t.Fatalf("state = %s", s.State)
	}
	m.SetClient(nil)
	if s := m.Status(); s.Enabled || s.State != StateIdle || s.Latest != nil {
		t.Fatalf("after SetClient(nil) = %+v", s)
	}
	m.SetClient(fakeClient(f, base, PlatformAndroid))
	if !m.Enabled() {
		t.Fatal("SetClient with a client must enable updates")
	}
}

func TestManagerReceivesAPublishInRealTime(t *testing.T) {
	f := updatetest.New(t)
	f.Publish(PlatformAndroid, 5, "0.1.5", payloadBytes(500, 1), false)
	base := serve(t, f.Handler())
	ev := &events{}
	m := NewManager(Options{
		Client: fakeClient(f, base, PlatformAndroid), Dir: t.TempDir(), CurrentCode: 5, Auto: true, Emit: ev.emit,
		FirstDelay: 10 * time.Millisecond, WatchWait: time.Second, Interval: time.Hour,
	})
	m.Run()
	t.Cleanup(m.Close)
	waitFor(t, "first check", func() bool { return m.Status().CheckedAt != 0 })
	if m.Status().State != StateUpToDate {
		t.Fatalf("state = %s, want up_to_date", m.Status().State)
	}

	published := time.Now()
	f.Publish(PlatformAndroid, 6, "0.1.6", payloadBytes(500, 2), false)
	waitFor(t, "ready", func() bool { return m.Status().State == StateReady })
	if lag := time.Since(published); lag > 3*time.Second {
		t.Fatalf("release reached the device after %s; the interval is 1h so this must be the watch", lag)
	}
	if s := m.Status(); s.Latest.VersionCode != 6 || ev.count("update.available") != 1 || ev.count("update.ready") != 1 {
		t.Fatalf("status %+v, events %v", s, ev.types())
	}

	// a second release is picked up the same way
	f.Publish(PlatformAndroid, 7, "0.1.7", payloadBytes(500, 3), false)
	waitFor(t, "second release", func() bool {
		s := m.Status()
		return s.Latest != nil && s.Latest.VersionCode == 7 && s.State == StateReady
	})
}

func TestManagerFallsBackToPollingWithoutWatch(t *testing.T) {
	f := updatetest.New(t)
	f.Publish(PlatformAndroid, 5, "0.1.5", payloadBytes(500, 1), false)
	real := f.Handler()
	var watchCalls atomic.Int64
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/watch") {
			watchCalls.Add(1)
			updatetest.APIError(w, 404, "not_found", "unknown app") // the app is not registered yet
			return
		}
		real.ServeHTTP(w, req)
	}))
	m := NewManager(Options{
		Client: fakeClient(f, base, PlatformAndroid), Dir: t.TempDir(), CurrentCode: 5, Auto: true, Emit: (&events{}).emit,
		FirstDelay: 10 * time.Millisecond, Interval: 100 * time.Millisecond,
	})
	m.Run()
	t.Cleanup(m.Close)
	waitFor(t, "first check", func() bool { return m.Status().CheckedAt != 0 })

	f.Publish(PlatformAndroid, 6, "0.1.6", payloadBytes(500, 2), false)
	waitFor(t, "polled release", func() bool { return m.Status().State == StateReady })
	if n := watchCalls.Load(); n != 1 {
		t.Fatalf("watch was tried %d times, want exactly once before falling back", n)
	}
}

func TestManagerBacksOffAfterFailuresAndRecovers(t *testing.T) {
	f := updatetest.New(t)
	f.Publish(PlatformAndroid, 6, "0.1.6", payloadBytes(500, 2), false)
	real := f.Handler()
	var down atomic.Bool
	down.Store(true)
	var calls atomic.Int64
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if down.Load() {
			http.Error(w, "down", 503)
			return
		}
		real.ServeHTTP(w, req)
	}))
	m := NewManager(Options{
		Client: fakeClient(f, base, PlatformAndroid), Dir: t.TempDir(), CurrentCode: 5, Auto: true, Emit: (&events{}).emit,
		FirstDelay: 10 * time.Millisecond, Backoff: 100 * time.Millisecond, RetryAfter: 400 * time.Millisecond,
		WatchWait: time.Second,
	})
	m.Run()
	t.Cleanup(m.Close)

	time.Sleep(1200 * time.Millisecond)
	// backoff 100,200,400,400... ms: about 5 attempts in 1.2 s, never a hot loop
	if n := calls.Load(); n < 3 || n > 12 {
		t.Fatalf("%d requests in 1.2 s while down; backoff is not working", n)
	}
	down.Store(false)
	waitFor(t, "recovery", func() bool { return m.Status().State == StateReady })
}

// Security review: a validly signed but older manifest must not move a device that has
// already seen a newer release.
func TestManagerRefusesAnOlderReleaseThanItAnnouncedBefore(t *testing.T) {
	f := updatetest.New(t)
	f.Publish(PlatformAndroid, 9, "0.1.9", payloadBytes(500, 1), false)
	high := int64(0)
	dir := filepath.Join(t.TempDir(), "updates")
	base := serve(t, f.Handler())
	m := NewManager(Options{
		Client: fakeClient(f, base, PlatformAndroid), Dir: dir, CurrentCode: 5, CurrentName: "cur",
		LoadHighWater: func() int64 { return high }, StoreHighWater: func(c int64) { high = c },
	})
	t.Cleanup(m.Close)
	if s, err := m.Check(context.Background()); err != nil || s.State != StateAvailable || high != 9 {
		t.Fatalf("first check = %+v, %v, high=%d", s, err, high)
	}
	high = 12 // the server announced 12 earlier; now it serves 9 again (a replay)
	s, err := m.Check(context.Background())
	if err == nil || s.State != StateError {
		t.Fatalf("a rollback to 9 after 12 was accepted: %+v, %v", s, err)
	}
	m.SetClient(fakeClient(f, base, PlatformAndroid))
	if high != 0 {
		t.Fatalf("changing the server must reset the high-water mark, got %d", high)
	}
}
