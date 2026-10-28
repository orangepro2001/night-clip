package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"relhub/pkg/relproto"
)

// realRelhub runs the installed release-publish binary on a random loopback
// port with a throwaway data directory, so nothing here touches the real
// service. It is the check that the vendored SDK, the fake server in these
// tests and the real server still agree on the wire format.
type realRelhub struct {
	t    *testing.T
	bin  string
	data string
	url  string
	pub  ed25519.PublicKey
}

var listeningRe = regexp.MustCompile(`msg=listening addr=(127\.0\.0\.1:\d+)`)

// startRealRelhub skips the test when no `relhub` binary is available
// (RELHUB_BIN, else PATH): CI machines do not have one.
func startRealRelhub(t *testing.T) *realRelhub {
	t.Helper()
	bin := os.Getenv("RELHUB_BIN")
	if bin == "" {
		bin, _ = exec.LookPath("relhub")
	}
	if bin == "" {
		t.Skip("no relhub binary (set RELHUB_BIN or install release-publish) — skipping the real-server test")
	}
	r := &realRelhub{t: t, bin: bin, data: filepath.Join(t.TempDir(), "data")}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "serve", "-addr", "127.0.0.1:0")
	cmd.Env = r.env()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })

	addr := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if m := listeningRe.FindStringSubmatch(sc.Text()); m != nil {
				addr <- m[1]
				break
			}
		}
		for sc.Scan() { // keep draining so the server never blocks on a full pipe
		}
	}()
	select {
	case a := <-addr:
		r.url = "http://" + a
	case <-time.After(10 * time.Second):
		t.Fatal("relhub did not report its address")
	}

	resp, err := http.Get(r.url + "/v1/info")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var info relproto.Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil || len(info.Keys) == 0 {
		t.Fatalf("info: %+v, %v", info, err)
	}
	if r.pub, err = ParsePublicKey(info.Keys[0].PublicKey); err != nil {
		t.Fatal(err)
	}
	r.run("app", "create", "-id", AppID, "-name", "ClipBridge")
	return r
}

// env isolates the child from any RELHUB_* settings of the developer's shell
// (the real service's data directory in particular).
func (r *realRelhub) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "RELHUB_") {
			env = append(env, kv)
		}
	}
	env = append(env, "RELHUB_DATA="+r.data)
	if r.url != "" {
		env = append(env, "RELHUB_URL="+r.url)
	}
	return env
}

func (r *realRelhub) run(args ...string) string {
	r.t.Helper()
	cmd := exec.Command(r.bin, args...)
	cmd.Env = r.env()
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		r.t.Fatalf("relhub %s: %v\n%s", strings.Join(args, " "), err, out.String())
	}
	return out.String()
}

// publish uploads a package the way scripts/release-local.sh does.
func (r *realRelhub) publish(platform string, versionCode int64, name string, body []byte, extra ...string) {
	r.t.Helper()
	ext := map[string]string{PlatformAndroid: ".apk", PlatformWindows: ".zip"}[platform]
	file := filepath.Join(r.t.TempDir(), "pkg"+ext)
	if err := os.WriteFile(file, body, 0o644); err != nil {
		r.t.Fatal(err)
	}
	args := []string{"publish", "-app", AppID, "-platform", platform, "-file", file,
		"-version", name, "-code", fmt.Sprint(versionCode), "-notes", "notes " + name}
	r.run(append(args, extra...)...)
}

func (r *realRelhub) client(platform string) *Client {
	return &Client{BaseURL: r.url, Platform: platform, Keys: []ed25519.PublicKey{r.pub}, UserAgent: "clipbridge-test/1", RetryWait: time.Millisecond}
}

func TestAgainstRealRelhub(t *testing.T) {
	r := startRealRelhub(t)
	ctx := context.Background()

	// Nothing published yet: the app is registered, the platform is empty.
	if _, err := r.client(PlatformAndroid).Check(ctx, 1); err != ErrNoRelease {
		t.Fatalf("empty platform: %v, want ErrNoRelease", err)
	}

	// A registered app with nothing published on a platform is not an error for watch: it waits.
	if got, err := r.client(PlatformWindows).Watch(ctx, 0, 300*time.Millisecond); got != nil || err != nil {
		t.Fatalf("watch on an empty platform = %v, %v; want nil, nil", got, err)
	}

	apk := payloadBytes(200_000, 5)
	r.publish(PlatformAndroid, 7, "0.1.7", apk)
	r.publish(PlatformWindows, 7, "0.1.7", payloadBytes(30_000, 6))

	c := r.client(PlatformAndroid)
	m, err := c.Check(ctx, 3)
	if err != nil || m.VersionCode != 7 || m.VersionName != "0.1.7" || m.Notes != "notes 0.1.7" || m.Mandatory(3) {
		t.Fatalf("Check = %+v, %v", m, err)
	}
	if m.App != AppID || m.Platform != PlatformAndroid || m.Channel != "stable" {
		t.Fatalf("manifest identity = %s/%s/%s", m.App, m.Platform, m.Channel)
	}
	if w, err := r.client(PlatformWindows).Check(ctx, 3); err != nil || w.VersionCode != 7 || w.File == m.File {
		t.Fatalf("windows Check = %+v, %v", w, err)
	}

	// Download with resume from a partial file.
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, m.File+".part"), apk[:60_000], 0o600)
	path, err := c.Download(ctx, m, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, apk) {
		t.Fatal("downloaded package differs")
	}

	// A signature from any other key is refused.
	otherKey, _ := newKey(t)
	if _, err := (&Client{BaseURL: r.url, Platform: PlatformAndroid, Keys: []ed25519.PublicKey{otherKey}}).Check(ctx, 3); code(err) != "UPDATE_REJECTED" {
		t.Fatalf("untrusted key: %v", err)
	}

	// Watch: nothing newer → nil after the wait; a publish while parked is delivered.
	if got, err := c.Watch(ctx, 7, 300*time.Millisecond); got != nil || err != nil {
		t.Fatalf("idle watch = %v, %v", got, err)
	}
	delivered := make(chan *Manifest, 1)
	go func() {
		got, _ := c.Watch(ctx, 7, 10*time.Second)
		delivered <- got
	}()
	time.Sleep(300 * time.Millisecond)
	r.publish(PlatformAndroid, 8, "0.1.8", payloadBytes(1_000, 1), "-force")
	select {
	case got := <-delivered:
		if got == nil || got.VersionCode != 8 || got.MinVersionCode != 8 || !got.Mandatory(7) {
			t.Fatalf("watch delivered %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a parked watch did not return after a publish")
	}

	// The whole stack: manager auto-download of the forced release.
	ev := &events{}
	mgr, _ := newManager(t, c, 7, true, ev)
	s, err := mgr.Check(ctx)
	if err != nil || s.Latest == nil || s.Latest.VersionCode != 8 || !s.Mandatory {
		t.Fatalf("manager Check = %+v, %v", s, err)
	}
	waitFor(t, "manager ready", func() bool { return mgr.Status().State == StateReady })

	// The service counted the checks by installed version (this is what makes `relhub stats` useful).
	if stats := r.run("stats", "-app", AppID); !strings.Contains(stats, "stable/7") {
		t.Fatalf("relhub stats does not show checks from the installed version:\n%s", stats)
	}

	// An app that is not registered looks like "no release" to check and "no watch" to the loop.
	ghost := r.client(PlatformAndroid)
	ghost.App = "not-registered"
	if _, err := ghost.Check(ctx, 1); err != ErrNoRelease {
		t.Fatalf("unregistered app Check: %v", err)
	}
	if _, err := ghost.Watch(ctx, 1, time.Second); err != ErrWatchUnsupported {
		t.Fatalf("unregistered app Watch: %v", err)
	}
}
