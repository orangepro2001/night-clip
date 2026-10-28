// Package updatetest is a fake release-publish service for tests. It speaks
// the part of the REST API that the update client uses (check, watch,
// version-pinned download with Range/If-Range), signs manifests with a
// throwaway key and records what it was asked.
//
// It documents, and lets tests bend, the wire format; that the format is the
// real one is checked against the installed `relhub` binary by
// update.TestAgainstRealRelhub. It must not import package update (update's own
// tests use it).
package updatetest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"relhub/pkg/relproto"
)

// AppID is the app the fake serves (update.AppID; a test in package update
// keeps the two equal).
const AppID = "clipbridge"

// Server is the fake service. Use Handler with httptest.NewServer.
type Server struct {
	t    testing.TB
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey

	mu          sync.Mutex
	releases    map[string][]*release // platform -> ascending versionCode
	wake        chan struct{}         // closed (and replaced) by every publish
	checks      []int64
	ranges      []string
	ifRanges    []string
	agents      []string
	ignoreRange bool
}

type release struct {
	m    relproto.Manifest
	body []byte
}

// New creates a server with a fresh signing key and no releases.
func New(t testing.TB) *Server {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{t: t, pub: pub, priv: priv, releases: map[string][]*release{}, wake: make(chan struct{})}
}

// PublicKey is the key the manifests are signed with: the one a client must trust.
func (s *Server) PublicKey() ed25519.PublicKey { return s.pub }

// Publish adds a release to the stable channel. force raises the minimum
// version to this release, like `relhub publish -force`.
func (s *Server) Publish(platform string, versionCode int64, name string, body []byte, force bool) *relproto.Manifest {
	return s.PublishWith(platform, versionCode, name, body, force, nil)
}

// PublishWith lets a test bend the manifest before it is signed.
func (s *Server) PublishWith(platform string, versionCode int64, name string, body []byte, force bool, mutate func(*relproto.Manifest)) *relproto.Manifest {
	s.t.Helper()
	ext := map[string]string{"android": ".apk", "windows": ".zip", "macos": ".zip"}[platform]
	sum := sha256.Sum256(body)
	m := relproto.Manifest{
		App: AppID, Platform: platform, Channel: relproto.DefaultChannel,
		VersionCode: versionCode, VersionName: name,
		File: fmt.Sprintf("%s-%s-%s%s", AppID, name, platform, ext),
		Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:]), Notes: "notes " + name,
		PublishedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if force {
		m.MinVersionCode = versionCode
	}
	if mutate != nil {
		mutate(&m)
	}
	if err := m.Sign(s.priv); err != nil {
		s.t.Fatalf("sign: %v", err)
	}
	s.mu.Lock()
	s.releases[platform] = append(s.releases[platform], &release{m: m, body: body})
	close(s.wake)
	s.wake = make(chan struct{})
	s.mu.Unlock()
	out := m
	return &out
}

// IgnoreRange makes downloads answer 200 with the whole body even to a Range request.
func (s *Server) IgnoreRange(on bool) { s.mu.Lock(); s.ignoreRange = on; s.mu.Unlock() }

// Checks returns the installed versionCode of every check request so far.
func (s *Server) Checks() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.checks...)
}

// Ranges returns the Range header of every download request so far.
func (s *Server) Ranges() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ranges...)
}

// IfRanges returns the If-Range header of every download request so far.
func (s *Server) IfRanges() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.ifRanges...)
}

// Agents returns the User-Agent of every request so far.
func (s *Server) Agents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.agents...)
}

// Downloads counts the download requests so far.
func (s *Server) Downloads() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.ranges) }

// APIError writes release-publish's error envelope.
func APIError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(relproto.ErrorBody{Error: &relproto.Error{Code: code, Message: msg}})
}

func (s *Server) latestLocked(platform string) *release {
	l := s.releases[platform]
	if len(l) == 0 {
		return nil
	}
	return l[len(l)-1]
}

func result(r *release, have int64) relproto.CheckResult {
	m := r.m
	return relproto.CheckResult{
		App: m.App, Platform: m.Platform, Channel: m.Channel, CurrentVersionCode: have,
		UpdateAvailable: m.VersionCode > have, Mandatory: m.VersionCode > have && m.Mandatory(have),
		Manifest: &m, DownloadPath: m.DownloadPath(),
	}
}

// Handler serves the API at the root of its URL.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/apps/{app}/check", s.check)
	mux.HandleFunc("GET /v1/apps/{app}/watch", s.watch)
	mux.HandleFunc("GET /v1/apps/{app}/{platform}/releases/{code}/download", s.download)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.agents = append(s.agents, r.UserAgent())
		s.mu.Unlock()
		mux.ServeHTTP(w, r)
	})
}

func queryInt(r *http.Request, key string) (int64, bool) {
	n, err := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
	return n, err == nil && n >= 0
}

func (s *Server) check(w http.ResponseWriter, r *http.Request) {
	have, ok := queryInt(r, "versionCode")
	if !ok {
		APIError(w, 400, relproto.CodeInvalid, "bad versionCode")
		return
	}
	s.mu.Lock()
	s.checks = append(s.checks, have)
	var rel *release
	if r.PathValue("app") == AppID {
		rel = s.latestLocked(r.URL.Query().Get("platform"))
	}
	s.mu.Unlock()
	if rel == nil {
		APIError(w, 404, relproto.CodeNotFound, "nothing published")
		return
	}
	_ = json.NewEncoder(w).Encode(result(rel, have))
}

func (s *Server) watch(w http.ResponseWriter, r *http.Request) {
	have, ok := queryInt(r, "versionCode")
	wait, err := strconv.ParseFloat(r.URL.Query().Get("wait"), 64)
	if !ok || err != nil || wait < 0 {
		APIError(w, 400, relproto.CodeInvalid, "bad request")
		return
	}
	if r.PathValue("app") != AppID {
		APIError(w, 404, relproto.CodeNotFound, "unknown app")
		return
	}
	platform := r.URL.Query().Get("platform")
	deadline := time.NewTimer(time.Duration(wait * float64(time.Second)))
	defer deadline.Stop()
	for {
		s.mu.Lock()
		rel, wake := s.latestLocked(platform), s.wake
		s.mu.Unlock()
		if rel != nil && rel.m.VersionCode > have {
			_ = json.NewEncoder(w).Encode(result(rel, have))
			return
		}
		select {
		case <-wake:
		case <-deadline.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	code, err := strconv.ParseInt(r.PathValue("code"), 10, 64)
	if err != nil {
		APIError(w, 400, relproto.CodeInvalid, "bad code")
		return
	}
	s.mu.Lock()
	var rel *release
	for _, c := range s.releases[r.PathValue("platform")] {
		if c.m.VersionCode == code {
			rel = c
		}
	}
	s.ranges = append(s.ranges, r.Header.Get("Range"))
	s.ifRanges = append(s.ifRanges, r.Header.Get("If-Range"))
	ignore := s.ignoreRange
	s.mu.Unlock()
	if rel == nil {
		APIError(w, 404, relproto.CodeNotFound, "no such release")
		return
	}
	if ignore {
		r.Header.Del("Range")
	}
	w.Header().Set("ETag", `"`+rel.m.SHA256+`"`)
	w.Header().Set("X-Content-SHA256", rel.m.SHA256)
	http.ServeContent(w, r, rel.m.File, time.Time{}, bytes.NewReader(rel.body))
}
