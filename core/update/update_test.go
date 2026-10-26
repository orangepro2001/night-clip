package update

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nightclip/core/proto"
)

// ---- helpers ----------------------------------------------------------------

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func payloadBytes(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i*7)
	}
	return b
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func sampleManifest() *Manifest {
	return &Manifest{
		Platform: PlatformAndroid, VersionCode: 12, VersionName: "0.1.12",
		File: "clipbridge-android-0.1.12-12.apk", Size: 1000, SHA256: strings.Repeat("ab", 32),
		MinVersionCode: 5, Notes: "fixes", PublishedAt: "2026-10-26T10:00:00Z",
	}
}

func code(err error) string {
	var pe *proto.Error
	if errors.As(err, &pe) {
		return string(pe.Code)
	}
	return ""
}

// release is a published release directory plus the keys that signed it.
type release struct {
	dir  string
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

func newRelease(t *testing.T) *release {
	t.Helper()
	pub, priv := newKey(t)
	return &release{dir: t.TempDir(), pub: pub, priv: priv}
}

func (r *release) publish(t *testing.T, platform string, versionCode int64, name string, content []byte, force bool) *Manifest {
	t.Helper()
	ext := packageExt[platform]
	src := filepath.Join(t.TempDir(), "pkg"+ext)
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Publish(PublishOptions{
		Dir: r.dir, Platform: platform, Source: src, VersionCode: versionCode,
		VersionName: name, Notes: "notes " + name, Force: force, Key: r.priv,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return m
}

func (r *release) client(base string, platform string) *Client {
	return &Client{BaseURL: base, Platform: platform, Keys: []ed25519.PublicKey{r.pub}}
}

func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s.URL
}

// ---- manifest ----------------------------------------------------------------

func TestManifestSignVerifyAndTamper(t *testing.T) {
	pub, priv := newKey(t)
	keys := []ed25519.PublicKey{pub}
	m := sampleManifest()
	if err := m.Sign(priv); err != nil {
		t.Fatal(err)
	}
	if err := m.Verify(keys); err != nil {
		t.Fatalf("fresh signature rejected: %v", err)
	}

	tampers := map[string]func(*Manifest){
		"versionCode":    func(m *Manifest) { m.VersionCode++ },
		"versionName":    func(m *Manifest) { m.VersionName = "9.9.9" },
		"file":           func(m *Manifest) { m.File = "evil.apk" },
		"size":           func(m *Manifest) { m.Size++ },
		"sha256":         func(m *Manifest) { m.SHA256 = strings.Repeat("cd", 32) },
		"minVersionCode": func(m *Manifest) { m.MinVersionCode = 0 },
		"notes":          func(m *Manifest) { m.Notes = "click here" },
		"publishedAt":    func(m *Manifest) { m.PublishedAt = "2026-10-27T10:00:00Z" },
		"platform":       func(m *Manifest) { m.Platform = PlatformWindows },
	}
	for field, mutate := range tampers {
		t.Run(field, func(t *testing.T) {
			c := *m
			mutate(&c)
			if err := c.Verify(keys); code(err) != "UPDATE_REJECTED" {
				t.Fatalf("tampered %s: err = %v, want UPDATE_REJECTED", field, err)
			}
		})
	}

	other, _ := newKey(t)
	if err := m.Verify([]ed25519.PublicKey{other}); code(err) != "UPDATE_REJECTED" {
		t.Fatalf("untrusted key accepted: %v", err)
	}
	if err := m.Verify(nil); code(err) != "UPDATE_REJECTED" {
		t.Fatalf("no trusted keys must reject: %v", err)
	}
	if err := m.Verify([]ed25519.PublicKey{other, pub}); err != nil {
		t.Fatalf("any trusted key should do: %v", err)
	}
	bad := *m
	bad.Signature = "!!!"
	if err := bad.Verify(keys); code(err) != "UPDATE_REJECTED" {
		t.Fatalf("garbage signature: %v", err)
	}
}

func TestManifestValidate(t *testing.T) {
	cases := map[string]func(*Manifest){
		"unknown platform":  func(m *Manifest) { m.Platform = "ios" },
		"zero version":      func(m *Manifest) { m.VersionCode = 0 },
		"empty name":        func(m *Manifest) { m.VersionName = "" },
		"newline in name":   func(m *Manifest) { m.VersionName = "1\nplatform=windows" },
		"path in file":      func(m *Manifest) { m.File = "../x.apk" },
		"slash in file":     func(m *Manifest) { m.File = "a/b.apk" },
		"dotfile":           func(m *Manifest) { m.File = ".hidden" },
		"zero size":         func(m *Manifest) { m.Size = 0 },
		"huge size":         func(m *Manifest) { m.Size = MaxPackageBytes + 1 },
		"short sha":         func(m *Manifest) { m.SHA256 = "abc" },
		"uppercase sha":     func(m *Manifest) { m.SHA256 = strings.Repeat("AB", 32) },
		"min above version": func(m *Manifest) { m.MinVersionCode = m.VersionCode + 1 },
		"negative min":      func(m *Manifest) { m.MinVersionCode = -1 },
		"bad date":          func(m *Manifest) { m.PublishedAt = "yesterday" },
		"newline in date":   func(m *Manifest) { m.PublishedAt = "2026-10-26T10:00:00Z\nx" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m := sampleManifest()
			mutate(m)
			if err := m.Validate(); code(err) != "UPDATE_REJECTED" {
				t.Fatalf("err = %v, want UPDATE_REJECTED", err)
			}
		})
	}
	if err := sampleManifest().Validate(); err != nil {
		t.Fatalf("sample manifest invalid: %v", err)
	}
}

func TestMandatory(t *testing.T) {
	m := sampleManifest() // min 5
	if !m.Mandatory(4) || m.Mandatory(5) || m.Mandatory(11) {
		t.Fatal("Mandatory must hold only below minVersionCode")
	}
}

func TestKeyFileRoundTripAndNoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k")
	pubB64, err := GenerateKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no POSIX mode bits; the user profile ACL protects the file there.
	if st, _ := os.Stat(path); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, want 0600", st.Mode().Perm())
	}
	priv, err := LoadPrivateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	pub, err := ParsePublicKey(pubB64)
	if err != nil || !bytes.Equal(priv.Public().(ed25519.PublicKey), pub) {
		t.Fatalf("public key does not match the written private key (%v)", err)
	}
	if _, err := GenerateKeyFile(path); err == nil {
		t.Fatal("GenerateKeyFile must not overwrite an existing key")
	}
	if _, err := ParsePublicKey("nope"); err == nil {
		t.Fatal("bad public key accepted")
	}
}

func TestValidateBaseURL(t *testing.T) {
	ok := map[string]string{
		"https://host.example/clipbridge":  "https://host.example/clipbridge",
		"https://host.example/clipbridge/": "https://host.example/clipbridge",
		"http://127.0.0.1:8080":            "http://127.0.0.1:8080",
		"http://localhost:8080/x":          "http://localhost:8080/x",
		"http://[::1]:8080":                "http://[::1]:8080",
	}
	for in, want := range ok {
		got, err := ValidateBaseURL(in)
		if err != nil || got != want {
			t.Errorf("ValidateBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "not a url", "http://example.com", "http://100.64.0.1:80", "ftp://x/", "https://u:p@host/",
		"https://host/?a=b", "https://host/#frag", "file:///etc/passwd",
	} {
		if _, err := ValidateBaseURL(in); code(err) != "INVALID" {
			t.Errorf("ValidateBaseURL(%q) err = %v, want INVALID", in, err)
		}
	}
}

// ---- publish + server --------------------------------------------------------

func TestPublishRules(t *testing.T) {
	r := newRelease(t)
	apk := payloadBytes(2048, 1)
	m1 := r.publish(t, PlatformAndroid, 10, "0.1.10", apk, false)
	if m1.MinVersionCode != 0 || m1.File != "clipbridge-android-0.1.10-10.apk" || m1.SHA256 != sha(apk) || m1.Size != 2048 {
		t.Fatalf("manifest = %+v", m1)
	}
	if _, err := VerifyPublished(r.dir, PlatformAndroid, []ed25519.PublicKey{r.pub}); err != nil {
		t.Fatalf("VerifyPublished: %v", err)
	}

	src := filepath.Join(t.TempDir(), "p.apk")
	_ = os.WriteFile(src, apk, 0o644)
	for _, c := range []int64{10, 9} {
		if _, err := Publish(PublishOptions{Dir: r.dir, Platform: PlatformAndroid, Source: src, VersionCode: c, VersionName: "x", Key: r.priv}); code(err) != "INVALID" {
			t.Fatalf("publishing code %d over 10: err = %v, want INVALID", c, err)
		}
	}
	if _, err := Publish(PublishOptions{Dir: r.dir, Platform: PlatformWindows, Source: src, VersionCode: 1, VersionName: "x", Key: r.priv}); code(err) != "INVALID" {
		t.Fatalf("wrong extension: err = %v, want INVALID", err)
	}
	if _, err := Publish(PublishOptions{Dir: r.dir, Platform: "ios", Source: src, VersionCode: 1, VersionName: "x", Key: r.priv}); code(err) != "INVALID" {
		t.Fatalf("unknown platform: err = %v", err)
	}
	if _, err := Publish(PublishOptions{Dir: r.dir, Platform: PlatformAndroid, Source: src, VersionCode: 11, VersionName: "../x", Key: r.priv}); code(err) != "INVALID" {
		t.Fatalf("bad versionName: err = %v", err)
	}

	// Force raises the floor; a later normal release keeps it.
	m2 := r.publish(t, PlatformAndroid, 11, "0.1.11", payloadBytes(10, 2), true)
	if m2.MinVersionCode != 11 {
		t.Fatalf("forced min = %d, want 11", m2.MinVersionCode)
	}
	m3 := r.publish(t, PlatformAndroid, 12, "0.1.12", payloadBytes(10, 3), false)
	if m3.MinVersionCode != 11 {
		t.Fatalf("min after normal release = %d, want 11 kept", m3.MinVersionCode)
	}
	// Platforms are independent.
	w := r.publish(t, PlatformWindows, 3, "0.1.3", payloadBytes(10, 4), false)
	if w.Platform != PlatformWindows || w.VersionCode != 3 {
		t.Fatalf("windows manifest = %+v", w)
	}
}

func TestPublishPrunesOldPackages(t *testing.T) {
	r := newRelease(t)
	for i := int64(1); i <= 6; i++ {
		r.publish(t, PlatformAndroid, i, fmt.Sprintf("0.1.%d", i), payloadBytes(16, byte(i)), false)
		// distinct mtimes so "newest" is well defined
		_ = os.Chtimes(filepath.Join(r.dir, "android", "files", fmt.Sprintf("clipbridge-android-0.1.%d-%d.apk", i, i)),
			time.Now(), time.Now().Add(time.Duration(i)*time.Minute))
	}
	entries, _ := os.ReadDir(filepath.Join(r.dir, "android", "files"))
	if len(entries) != 3 {
		t.Fatalf("%d packages kept, want 3", len(entries))
	}
	if _, err := VerifyPublished(r.dir, PlatformAndroid, []ed25519.PublicKey{r.pub}); err != nil {
		t.Fatalf("current release lost by pruning: %v", err)
	}
}

func TestVerifyPublishedDetectsCorruptionAndWrongKey(t *testing.T) {
	r := newRelease(t)
	m := r.publish(t, PlatformAndroid, 1, "0.1.1", payloadBytes(100, 1), false)
	other, _ := newKey(t)
	if _, err := VerifyPublished(r.dir, PlatformAndroid, []ed25519.PublicKey{other}); code(err) != "UPDATE_REJECTED" {
		t.Fatalf("wrong key: %v", err)
	}
	pkg := filepath.Join(r.dir, "android", "files", m.File)
	_ = os.WriteFile(pkg, payloadBytes(100, 99), 0o644)
	if _, err := VerifyPublished(r.dir, PlatformAndroid, []ed25519.PublicKey{r.pub}); code(err) != "HASH_MISMATCH" {
		t.Fatalf("corrupted package: %v", err)
	}
	if _, err := VerifyPublished(r.dir, PlatformWindows, []ed25519.PublicKey{r.pub}); err == nil {
		t.Fatal("unpublished platform verified")
	}
}

func TestHandlerRoutesRangeAndSafety(t *testing.T) {
	r := newRelease(t)
	body := payloadBytes(5000, 5)
	m := r.publish(t, PlatformAndroid, 7, "0.1.7", body, false)
	base := serve(t, NewHandler(r.dir, ""))

	get := func(path string, hdr map[string]string) (*http.Response, []byte) {
		req, _ := http.NewRequest("GET", base+path, nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, b
	}

	if resp, b := get("/healthz", nil); resp.StatusCode != 200 || strings.TrimSpace(string(b)) != "ok" {
		t.Fatalf("healthz = %d %q", resp.StatusCode, b)
	}
	resp, b := get("/v1/android/latest", nil)
	if resp.StatusCode != 200 || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("latest = %d, cache %q", resp.StatusCode, resp.Header.Get("Cache-Control"))
	}
	var got Manifest
	if err := json.Unmarshal(b, &got); err != nil || got.SHA256 != m.SHA256 {
		t.Fatalf("latest body %s (%v)", b, err)
	}

	resp, b = get("/v1/android/files/"+m.File, nil)
	if resp.StatusCode != 200 || !bytes.Equal(b, body) || resp.Header.Get("X-Content-SHA256") != m.SHA256 {
		t.Fatalf("download = %d, %d bytes, sha header %q", resp.StatusCode, len(b), resp.Header.Get("X-Content-SHA256"))
	}
	resp, b = get("/v1/android/files/"+m.File, map[string]string{"Range": "bytes=4000-"})
	if resp.StatusCode != 206 || !bytes.Equal(b, body[4000:]) || resp.Header.Get("Content-Range") != "bytes 4000-4999/5000" {
		t.Fatalf("range = %d, %d bytes, Content-Range %q", resp.StatusCode, len(b), resp.Header.Get("Content-Range"))
	}

	for _, p := range []string{
		"/v1/ios/latest", "/v1/windows/latest", "/v1/android/files/nope.apk",
		"/v1/android/files/..%2Flatest.json", "/v1/android/files/%2e%2e", "/v1/android/files/.hidden",
		"/v1/android/files/a%2Fb", "/etc/passwd", "/v1/", "/v1/android/",
	} {
		if resp, _ := get(p, nil); resp.StatusCode != 404 && resp.StatusCode != 400 && resp.StatusCode != 301 {
			t.Errorf("GET %s = %d, want a refusal", p, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest("POST", base+"/v1/android/latest", nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != 405 {
		t.Errorf("POST latest = %v / %v, want 405", resp, err)
	}
}

func TestHandlerPrefix(t *testing.T) {
	r := newRelease(t)
	r.publish(t, PlatformAndroid, 1, "0.1.1", payloadBytes(10, 1), false)
	base := serve(t, NewHandler(r.dir, "/clipbridge/"))
	for path, want := range map[string]int{"/clipbridge/v1/android/latest": 200, "/clipbridge/healthz": 200, "/v1/android/latest": 404} {
		resp, err := http.Get(base + path)
		if err != nil || resp.StatusCode != want {
			t.Errorf("GET %s = %v / %v, want %d", path, resp, err, want)
		}
	}
}

// ---- client ------------------------------------------------------------------

func TestClientLatestAndDownload(t *testing.T) {
	r := newRelease(t)
	body := payloadBytes(300_000, 9)
	m := r.publish(t, PlatformAndroid, 4, "0.1.4", body, false)
	c := r.client(serve(t, NewHandler(r.dir, "")), PlatformAndroid)

	got, err := c.Latest(context.Background())
	if err != nil || got.VersionCode != 4 {
		t.Fatalf("Latest = %+v, %v", got, err)
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
	data, _ := os.ReadFile(path)
	if !bytes.Equal(data, body) {
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

func TestClientLatestErrors(t *testing.T) {
	r := newRelease(t)
	ctx := context.Background()

	if _, err := r.client(serve(t, NewHandler(r.dir, "")), PlatformAndroid).Latest(ctx); !errors.Is(err, ErrNoRelease) {
		t.Fatalf("empty server: %v, want ErrNoRelease", err)
	}

	r.publish(t, PlatformAndroid, 1, "0.1.1", payloadBytes(10, 1), false)
	base := serve(t, NewHandler(r.dir, ""))

	other, _ := newKey(t)
	c := &Client{BaseURL: base, Platform: PlatformAndroid, Keys: []ed25519.PublicKey{other}}
	if _, err := c.Latest(ctx); code(err) != "UPDATE_REJECTED" {
		t.Fatalf("untrusted signature: %v", err)
	}

	// A correctly signed windows manifest served on the android path is refused.
	r.publish(t, PlatformWindows, 1, "0.1.1", payloadBytes(10, 1), false)
	win, _ := os.ReadFile(filepath.Join(r.dir, "windows", "latest.json"))
	crossed := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(win) }))
	if _, err := r.client(crossed, PlatformAndroid).Latest(ctx); code(err) != "UPDATE_REJECTED" {
		t.Fatalf("cross-platform manifest: %v", err)
	}

	for name, h := range map[string]http.HandlerFunc{
		"not json": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
		"5xx":      func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "x", 500) },
		"huge":     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(bytes.Repeat([]byte("a"), 100_000)) },
	} {
		_, err := r.client(serve(t, h), PlatformAndroid).Latest(ctx)
		want := map[string]string{"not json": "UPDATE_REJECTED", "5xx": "UPDATE_FAILED", "huge": "UPDATE_REJECTED"}[name]
		if code(err) != want {
			t.Errorf("%s: err = %v, want %s", name, err, want)
		}
	}
	if _, err := r.client("http://127.0.0.1:1", PlatformAndroid).Latest(ctx); code(err) != "UPDATE_FAILED" {
		t.Fatalf("connection refused: %v", err)
	}
}

// rangeRecorder wraps the real handler and records Range headers; with
// ignoreRange it drops them so the server answers 200.
type rangeRecorder struct {
	h           http.Handler
	mu          sync.Mutex
	ranges      []string
	ignoreRange bool
}

func (rr *rangeRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if strings.Contains(req.URL.Path, "/files/") {
		rr.mu.Lock()
		rr.ranges = append(rr.ranges, req.Header.Get("Range"))
		rr.mu.Unlock()
		if rr.ignoreRange {
			req.Header.Del("Range")
		}
	}
	rr.h.ServeHTTP(w, req)
}

func TestClientResumesPartialDownload(t *testing.T) {
	r := newRelease(t)
	body := payloadBytes(100_000, 3)
	m := r.publish(t, PlatformAndroid, 2, "0.1.2", body, false)
	rr := &rangeRecorder{h: NewHandler(r.dir, "")}
	c := r.client(serve(t, rr), PlatformAndroid)
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
	if len(rr.ranges) != 1 || rr.ranges[0] != "bytes=40000-" {
		t.Fatalf("Range headers = %q, want one bytes=40000-", rr.ranges)
	}
	if first <= 40_000 {
		t.Fatalf("first progress %d did not continue from the partial file", first)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, body) {
		t.Fatal("resumed file differs")
	}
}

func TestClientHandlesServerIgnoringRange(t *testing.T) {
	r := newRelease(t)
	body := payloadBytes(50_000, 8)
	m := r.publish(t, PlatformAndroid, 2, "0.1.2", body, false)
	c := r.client(serve(t, &rangeRecorder{h: NewHandler(r.dir, ""), ignoreRange: true}), PlatformAndroid)
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
	r := newRelease(t)
	body := payloadBytes(20_000, 2)
	m := r.publish(t, PlatformAndroid, 2, "0.1.2", body, false)
	c := r.client(serve(t, NewHandler(r.dir, "")), PlatformAndroid)
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, m.File+".part"), payloadBytes(30_000, 1), 0o600) // longer than the release
	path, err := c.Download(context.Background(), m, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, body) {
		t.Fatal("oversized partial file was not discarded")
	}
}

func TestClientRejectsWrongBytes(t *testing.T) {
	r := newRelease(t)
	m := r.publish(t, PlatformAndroid, 2, "0.1.2", payloadBytes(20_000, 2), false)

	tests := map[string]http.HandlerFunc{
		"same size, different content": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payloadBytes(20_000, 77)) },
		"too long":                     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payloadBytes(25_000, 2)) },
	}
	for name, h := range tests {
		t.Run(name, func(t *testing.T) {
			c := r.client(serve(t, h), PlatformAndroid)
			dir := t.TempDir()
			_, err := c.Download(context.Background(), m, dir, nil)
			if code(err) != "HASH_MISMATCH" {
				t.Fatalf("err = %v, want HASH_MISMATCH", err)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Fatalf("bad download left files behind: %v", entries)
			}
		})
	}

	t.Run("truncated keeps part for resume", func(t *testing.T) {
		c := r.client(serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
}

func TestClientStallThenResume(t *testing.T) {
	r := newRelease(t)
	body := payloadBytes(40_000, 6)
	m := r.publish(t, PlatformAndroid, 2, "0.1.2", body, false)
	real := NewHandler(r.dir, "")
	var stall atomic.Bool
	stall.Store(true)
	c := r.client(serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.Contains(req.URL.Path, "/files/") || !stall.Load() {
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
	r := newRelease(t)
	m := r.publish(t, PlatformAndroid, 2, "0.1.2", payloadBytes(100, 1), false)
	evil := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payloadBytes(100, 1)) }))
	front := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, evil+req.URL.Path, http.StatusFound)
	}))
	c := r.client(front, PlatformAndroid)
	if _, err := c.Download(context.Background(), m, t.TempDir(), nil); code(err) != "UPDATE_FAILED" {
		t.Fatalf("cross-host redirect followed: %v", err)
	}
}

// ---- manager -----------------------------------------------------------------

type events struct {
	mu sync.Mutex
	l  []map[string]any
}

func (e *events) emit(ev map[string]any) { e.mu.Lock(); e.l = append(e.l, ev); e.mu.Unlock() }
func (e *events) types() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, ev := range e.l {
		out = append(out, ev["type"].(string))
	}
	return out
}
func (e *events) count(typ string) int {
	n := 0
	for _, t := range e.types() {
		if t == typ {
			n++
		}
	}
	return n
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func newManager(t *testing.T, c *Client, current int64, auto bool, ev *events) (*Manager, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "updates")
	m := NewManager(Options{Client: c, Dir: dir, CurrentCode: current, CurrentName: "cur", Auto: auto, Emit: ev.emit})
	t.Cleanup(m.Close)
	return m, dir
}

func TestManagerManualFlow(t *testing.T) {
	r := newRelease(t)
	body := payloadBytes(120_000, 4)
	r.publish(t, PlatformAndroid, 9, "0.1.9", body, false)
	ev := &events{}
	m, dir := newManager(t, r.client(serve(t, NewHandler(r.dir, "")), PlatformAndroid), 5, false, ev)

	if s := m.Status(); !s.Enabled || s.State != StateIdle || s.CurrentCode != 5 {
		t.Fatalf("initial = %+v", s)
	}
	s, err := m.Check(context.Background())
	if err != nil || s.State != StateAvailable || s.Latest.VersionCode != 9 || s.Mandatory {
		t.Fatalf("after Check = %+v, %v", s, err)
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
	r := newRelease(t)
	r.publish(t, PlatformAndroid, 9, "0.1.9", payloadBytes(50_000, 4), false)
	rr := &rangeRecorder{h: NewHandler(r.dir, "")}
	ev := &events{}
	m, _ := newManager(t, r.client(serve(t, rr), PlatformAndroid), 5, true, ev)

	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "auto ready", func() bool { return m.Status().State == StateReady })

	downloads := len(rr.ranges)
	s, err := m.Check(context.Background()) // already on disk: no second download
	if err != nil || s.State != StateReady {
		t.Fatalf("re-check = %+v, %v", s, err)
	}
	if len(rr.ranges) != downloads {
		t.Fatalf("re-check downloaded again (%d -> %d requests)", downloads, len(rr.ranges))
	}
}

func TestManagerReadyFileFromPreviousSession(t *testing.T) {
	r := newRelease(t)
	body := payloadBytes(30_000, 4)
	mf := r.publish(t, PlatformAndroid, 9, "0.1.9", body, false)
	ev := &events{}
	m, dir := newManager(t, r.client(serve(t, NewHandler(r.dir, "")), PlatformAndroid), 5, false, ev)
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, mf.File), body, 0o600)

	s, err := m.Check(context.Background())
	if err != nil || s.State != StateReady || s.ReadyPath != filepath.Join(dir, mf.File) {
		t.Fatalf("Check with a verified package on disk = %+v, %v", s, err)
	}
}

func TestManagerUpToDateDisabledAndMandatory(t *testing.T) {
	r := newRelease(t)
	base := serve(t, NewHandler(r.dir, ""))
	ev := &events{}

	// nothing published
	m, _ := newManager(t, r.client(base, PlatformAndroid), 5, false, ev)
	if s, err := m.Check(context.Background()); err != nil || s.State != StateUpToDate {
		t.Fatalf("empty server: %+v, %v", s, err)
	}

	r.publish(t, PlatformAndroid, 9, "0.1.9", payloadBytes(100, 1), true) // forced: min 9

	// same version installed → up to date, stale packages pruned
	m2, dir2 := newManager(t, r.client(base, PlatformAndroid), 9, false, ev)
	_ = os.MkdirAll(dir2, 0o700)
	_ = os.WriteFile(filepath.Join(dir2, "clipbridge-android-0.1.8-8.apk"), []byte("old"), 0o600)
	_ = os.WriteFile(filepath.Join(dir2, "x.apk.part"), []byte("old"), 0o600)
	if s, err := m2.Check(context.Background()); err != nil || s.State != StateUpToDate {
		t.Fatalf("current version: %+v, %v", s, err)
	}
	if entries, _ := os.ReadDir(dir2); len(entries) != 0 {
		t.Fatalf("stale packages not pruned: %v", entries)
	}

	// older install is flagged mandatory
	m3, _ := newManager(t, r.client(base, PlatformAndroid), 5, false, ev)
	if s, err := m3.Check(context.Background()); err != nil || !s.Mandatory || s.State != StateAvailable {
		t.Fatalf("older install: %+v, %v", s, err)
	}

	// dev build (code 0) and no-server managers are disabled
	for name, mgr := range map[string]*Manager{
		"dev build": NewManager(Options{Client: r.client(base, PlatformAndroid), Dir: t.TempDir(), CurrentCode: 0}),
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

func TestManagerDownloadWithoutAvailableRelease(t *testing.T) {
	r := newRelease(t)
	m, _ := newManager(t, r.client(serve(t, NewHandler(r.dir, "")), PlatformAndroid), 5, false, &events{})
	if _, err := m.Download(); code(err) != "INVALID" {
		t.Fatalf("Download before Check: err = %v, want INVALID", err)
	}
}

func TestManagerErrorsAreRecordedAndRecoverable(t *testing.T) {
	r := newRelease(t)
	r.publish(t, PlatformAndroid, 9, "0.1.9", payloadBytes(1000, 1), false)
	real := NewHandler(r.dir, "")
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
	m, _ := newManager(t, r.client(srv, PlatformAndroid), 5, true, ev)

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
	r := newRelease(t)
	r.publish(t, PlatformAndroid, 9, "0.1.9", payloadBytes(5000, 1), false)
	real := NewHandler(r.dir, "")
	var corrupt atomic.Bool
	corrupt.Store(true)
	ev := &events{}
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if corrupt.Load() && strings.Contains(req.URL.Path, "/files/") {
			_, _ = w.Write(payloadBytes(5000, 99))
			return
		}
		real.ServeHTTP(w, req)
	}))
	m, _ := newManager(t, r.client(srv, PlatformAndroid), 5, false, ev)
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
	r := newRelease(t)
	r.publish(t, PlatformAndroid, 9, "0.1.9", payloadBytes(2000, 1), false)
	ev := &events{}
	m := NewManager(Options{
		Client: r.client(serve(t, NewHandler(r.dir, "")), PlatformAndroid), Dir: t.TempDir(),
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
	r := newRelease(t)
	body := payloadBytes(40_000, 1)
	r.publish(t, PlatformAndroid, 9, "0.1.9", body, false)
	real := NewHandler(r.dir, "")
	started := make(chan struct{}, 1)
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !strings.Contains(req.URL.Path, "/files/") {
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
	m, _ := newManager(t, r.client(srv, PlatformAndroid), 5, true, ev)
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
	r := newRelease(t)
	r.publish(t, PlatformAndroid, 9, "0.1.9", payloadBytes(500, 1), false)
	base := serve(t, NewHandler(r.dir, ""))
	m, _ := newManager(t, r.client(base, PlatformAndroid), 5, false, &events{})
	if s, _ := m.Check(context.Background()); s.State != StateAvailable {
		t.Fatalf("state = %s", s.State)
	}
	m.SetClient(nil)
	if s := m.Status(); s.Enabled || s.State != StateIdle || s.Latest != nil {
		t.Fatalf("after SetClient(nil) = %+v", s)
	}
	m.SetClient(r.client(base, PlatformAndroid))
	if !m.Enabled() {
		t.Fatal("SetClient with a client must enable updates")
	}
}

// Guards against shipping with the keygen placeholder or an empty trust list,
// which would make every update unverifiable (or, worse, trust nothing silently).
func TestCompiledInTrustedKeysAreValid(t *testing.T) {
	if len(trustedKeysB64) == 0 || len(TrustedKeys()) != len(trustedKeysB64) {
		t.Fatalf("trusted keys: %d configured, %d parse", len(trustedKeysB64), len(TrustedKeys()))
	}
}

func TestHandlerStableDownloadAndIndex(t *testing.T) {
	r := newRelease(t)
	body := payloadBytes(3_000_000, 11)
	m := r.publish(t, PlatformAndroid, 7, "0.1.7", body, false)
	base := serve(t, NewHandler(r.dir, "/clipbridge"))

	resp, err := http.Get(base + "/clipbridge/v1/android/download")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !bytes.Equal(got, body) {
		t.Fatalf("download = %d, %d bytes", resp.StatusCode, len(got))
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="`+m.File+`"` {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	if resp.Header.Get("X-Content-SHA256") != m.SHA256 {
		t.Fatal("download lacks X-Content-SHA256")
	}

	req, _ := http.NewRequest("GET", base+"/clipbridge/v1/android/download", nil)
	req.Header.Set("Range", "bytes=2999000-")
	part, err := http.DefaultClient.Do(req)
	if err != nil || part.StatusCode != 206 {
		t.Fatalf("range on download = %v / %v", part, err)
	}
	part.Body.Close()

	// Unpublished platform and unknown platform: 404, never a directory listing.
	for _, p := range []string{"/clipbridge/v1/windows/download", "/clipbridge/v1/ios/download"} {
		if resp, err := http.Get(base + p); err != nil || resp.StatusCode != 404 {
			t.Errorf("GET %s = %v / %v, want 404", p, resp, err)
		}
	}

	resp, err = http.Get(base + "/clipbridge/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(page)
	for _, want := range []string{m.File, m.SHA256, `href="v1/android/download"`, "Not published yet", "2.9 MiB"} {
		if !strings.Contains(html, want) {
			t.Errorf("index page lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<script") {
		t.Error("index page must not contain scripts")
	}
}

func TestIndexEscapesNotes(t *testing.T) {
	r := newRelease(t)
	src := filepath.Join(t.TempDir(), "p.apk")
	_ = os.WriteFile(src, payloadBytes(10, 1), 0o644)
	if _, err := Publish(PublishOptions{Dir: r.dir, Platform: PlatformAndroid, Source: src, VersionCode: 1,
		VersionName: "0.1.1", Notes: `<script>alert(1)</script>`, Key: r.priv}); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(serve(t, NewHandler(r.dir, "")) + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(page), "<script>alert") {
		t.Fatalf("release notes were not escaped:\n%s", page)
	}
}

// ---- watch (real-time notification) -------------------------------------------

func TestWatchEndpoint(t *testing.T) {
	r := newRelease(t)
	base := serve(t, NewHandler(r.dir, ""))

	get := func(path string) (*http.Response, time.Duration) {
		start := time.Now()
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp, time.Since(start)
	}

	// nothing published: 204 after the wait
	if resp, took := get("/v1/android/watch?code=0&wait=0.3"); resp.StatusCode != 204 || took < 250*time.Millisecond {
		t.Fatalf("empty watch = %d after %s", resp.StatusCode, took)
	}

	r.publish(t, PlatformAndroid, 5, "0.1.5", payloadBytes(100, 1), false)
	// newer than what the caller knows: answered at once
	if resp, took := get("/v1/android/watch?code=4&wait=5"); resp.StatusCode != 200 || took > time.Second {
		t.Fatalf("immediate watch = %d after %s", resp.StatusCode, took)
	}
	// already knows it: waits, then 204
	if resp, took := get("/v1/android/watch?code=5&wait=0.3"); resp.StatusCode != 204 || took < 250*time.Millisecond {
		t.Fatalf("up-to-date watch = %d after %s", resp.StatusCode, took)
	}

	// a release published while the request is parked is delivered within the poll period
	done := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Get(base + "/v1/android/watch?code=5&wait=10")
		if err != nil {
			t.Error(err)
		}
		done <- resp
	}()
	time.Sleep(300 * time.Millisecond)
	published := time.Now()
	m := r.publish(t, PlatformAndroid, 6, "0.1.6", payloadBytes(100, 2), false)
	select {
	case resp := <-done:
		defer resp.Body.Close()
		var got Manifest
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || got.VersionCode != 6 || got.SHA256 != m.SHA256 {
			t.Fatalf("watch delivered %+v (%v)", got, err)
		}
		if lag := time.Since(published); lag > 2*time.Second {
			t.Fatalf("delivery lag %s", lag)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not return after a publish")
	}

	for _, p := range []string{
		"/v1/android/watch", "/v1/android/watch?code=abc", "/v1/android/watch?code=-1",
		"/v1/android/watch?code=1&wait=x", "/v1/android/watch?code=1&wait=-2",
	} {
		if resp, _ := get(p); resp.StatusCode != 400 {
			t.Errorf("GET %s = %d, want 400", p, resp.StatusCode)
		}
	}
	if resp, _ := get("/v1/ios/watch?code=1"); resp.StatusCode != 404 {
		t.Errorf("unknown platform = %d, want 404", resp.StatusCode)
	}
	if activeWatchers.Load() != 0 {
		t.Errorf("%d watchers leaked", activeWatchers.Load())
	}
}

func TestWatchReleasesWhenClientDisconnects(t *testing.T) {
	r := newRelease(t)
	base := serve(t, NewHandler(r.dir, ""))
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/v1/android/watch?code=0&wait=30", nil)
	go func() { _, _ = http.DefaultClient.Do(req) }()
	waitFor(t, "watcher to park", func() bool { return activeWatchers.Load() == 1 })
	cancel()
	waitFor(t, "watcher to be released", func() bool { return activeWatchers.Load() == 0 })
}

func TestClientWatch(t *testing.T) {
	r := newRelease(t)
	base := serve(t, NewHandler(r.dir, ""))
	c := r.client(base, PlatformAndroid)
	ctx := context.Background()

	if m, err := c.Watch(ctx, 0, 200*time.Millisecond); m != nil || err != nil {
		t.Fatalf("empty server: %v, %v; want nil, nil", m, err)
	}
	r.publish(t, PlatformAndroid, 3, "0.1.3", payloadBytes(50, 1), false)
	m, err := c.Watch(ctx, 2, time.Second)
	if err != nil || m == nil || m.VersionCode != 3 {
		t.Fatalf("Watch = %+v, %v", m, err)
	}
	if m, err := c.Watch(ctx, 3, 200*time.Millisecond); m != nil || err != nil {
		t.Fatalf("known version: %v, %v; want nil, nil", m, err)
	}

	other, _ := newKey(t)
	bad := &Client{BaseURL: base, Platform: PlatformAndroid, Keys: []ed25519.PublicKey{other}}
	if _, err := bad.Watch(ctx, 0, time.Second); code(err) != "UPDATE_REJECTED" {
		t.Fatalf("untrusted signature via watch: %v", err)
	}

	old := serve(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) }))
	if _, err := r.client(old, PlatformAndroid).Watch(ctx, 0, time.Second); !errors.Is(err, ErrWatchUnsupported) {
		t.Fatalf("old server: %v, want ErrWatchUnsupported", err)
	}
	if _, err := r.client("http://127.0.0.1:1", PlatformAndroid).Watch(ctx, 0, time.Second); code(err) != "UPDATE_FAILED" {
		t.Fatalf("unreachable: %v", err)
	}
	cctx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	defer cancel()
	if _, err := c.Watch(cctx, 3, 5*time.Second); err == nil {
		t.Fatal("cancelled watch returned no error")
	}
}

func TestManagerReceivesAPublishInRealTime(t *testing.T) {
	r := newRelease(t)
	r.publish(t, PlatformAndroid, 5, "0.1.5", payloadBytes(500, 1), false)
	base := serve(t, NewHandler(r.dir, ""))
	ev := &events{}
	m := NewManager(Options{
		Client: r.client(base, PlatformAndroid), Dir: t.TempDir(), CurrentCode: 5, Auto: true, Emit: ev.emit,
		FirstDelay: 10 * time.Millisecond, WatchWait: time.Second, Interval: time.Hour,
	})
	m.Run()
	t.Cleanup(m.Close)
	waitFor(t, "first check", func() bool { return m.Status().CheckedAt != 0 })
	if m.Status().State != StateUpToDate {
		t.Fatalf("state = %s, want up_to_date", m.Status().State)
	}

	published := time.Now()
	r.publish(t, PlatformAndroid, 6, "0.1.6", payloadBytes(500, 2), false)
	waitFor(t, "ready", func() bool { return m.Status().State == StateReady })
	if lag := time.Since(published); lag > 3*time.Second {
		t.Fatalf("release reached the device after %s; the interval is 1h so this must be the watch", lag)
	}
	if s := m.Status(); s.Latest.VersionCode != 6 || ev.count("update.available") != 1 || ev.count("update.ready") != 1 {
		t.Fatalf("status %+v, events %v", s, ev.types())
	}

	// a second release is picked up the same way
	r.publish(t, PlatformAndroid, 7, "0.1.7", payloadBytes(500, 3), false)
	waitFor(t, "second release", func() bool {
		s := m.Status()
		return s.Latest != nil && s.Latest.VersionCode == 7 && s.State == StateReady
	})
}

func TestManagerFallsBackToPollingWithoutWatch(t *testing.T) {
	r := newRelease(t)
	r.publish(t, PlatformAndroid, 5, "0.1.5", payloadBytes(500, 1), false)
	real := NewHandler(r.dir, "")
	var watchCalls atomic.Int64
	base := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, "/watch") {
			watchCalls.Add(1)
			http.NotFound(w, req) // an older cbdist
			return
		}
		real.ServeHTTP(w, req)
	}))
	m := NewManager(Options{
		Client: r.client(base, PlatformAndroid), Dir: t.TempDir(), CurrentCode: 5, Auto: true, Emit: (&events{}).emit,
		FirstDelay: 10 * time.Millisecond, Interval: 100 * time.Millisecond,
	})
	m.Run()
	t.Cleanup(m.Close)
	waitFor(t, "first check", func() bool { return m.Status().CheckedAt != 0 })

	r.publish(t, PlatformAndroid, 6, "0.1.6", payloadBytes(500, 2), false)
	waitFor(t, "polled release", func() bool { return m.Status().State == StateReady })
	if n := watchCalls.Load(); n != 1 {
		t.Fatalf("watch was tried %d times, want exactly once before falling back", n)
	}
}

func TestManagerBacksOffAfterFailuresAndRecovers(t *testing.T) {
	r := newRelease(t)
	r.publish(t, PlatformAndroid, 6, "0.1.6", payloadBytes(500, 2), false)
	real := NewHandler(r.dir, "")
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
		Client: r.client(base, PlatformAndroid), Dir: t.TempDir(), CurrentCode: 5, Auto: true, Emit: (&events{}).emit,
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

// Security review: an https download must not be moved to http by a redirect.
func TestRedirectToAnotherSchemeIsRefused(t *testing.T) {
	first, _ := http.NewRequest("GET", "https://tony-ai-pc-1.example/clipbridge/v1/android/latest", nil)
	same, _ := http.NewRequest("GET", "https://tony-ai-pc-1.example/clipbridge/v1/android/latest/", nil)
	downgrade, _ := http.NewRequest("GET", "http://tony-ai-pc-1.example/clipbridge/v1/android/latest", nil)
	if err := defaultHTTP.CheckRedirect(same, []*http.Request{first}); err != nil {
		t.Fatalf("same-scheme redirect refused: %v", err)
	}
	if err := defaultHTTP.CheckRedirect(downgrade, []*http.Request{first}); err == nil {
		t.Fatal("https -> http redirect was followed")
	}
}

// Security review: a validly signed but older manifest must not move a device that has
// already seen a newer release.
func TestManagerRefusesAnOlderReleaseThanItAnnouncedBefore(t *testing.T) {
	r := newRelease(t)
	r.publish(t, PlatformAndroid, 9, "0.1.9", payloadBytes(500, 1), false)
	high := int64(0)
	dir := filepath.Join(t.TempDir(), "updates")
	m := NewManager(Options{
		Client: r.client(serve(t, NewHandler(r.dir, "")), PlatformAndroid), Dir: dir, CurrentCode: 5, CurrentName: "cur",
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
	m.SetClient(r.client(serve(t, NewHandler(r.dir, "")), PlatformAndroid))
	if high != 0 {
		t.Fatalf("changing the server must reset the high-water mark, got %d", high)
	}
}
