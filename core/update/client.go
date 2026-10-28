package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"nightclip/core/proto"
	"relhub/pkg/relclient"
	"relhub/pkg/relproto"
)

const stallTimeout = 60 * time.Second

// MaxWatchWait is the longest one watch request may block (release-publish
// answers 204 after 50 s, below the usual 60 s proxy idle limit).
const MaxWatchWait = time.Duration(relproto.MaxWatchSeconds) * time.Second

// ErrNoRelease is returned by Check when the service has nothing published
// for the app and platform (not yet registered, or no stable release).
var ErrNoRelease = errors.New("no release published")

// ErrWatchUnsupported means the service cannot answer watch for this app
// (it is not registered yet): fall back to polling.
var ErrWatchUnsupported = errors.New("server does not support watch")

// Progress is called with the bytes on disk so far and the total.
type Progress func(received, total int64)

// Client talks to a release-publish service for one platform of one app. It
// wraps the vendored SDK (relhub/pkg/relclient) and adds what a phone or a
// desktop needs on top: only https (or loopback http), no redirect off the
// configured host, a stalled download is aborted and resumed, and a build
// without trusted keys can never accept an update.
type Client struct {
	BaseURL  string // e.g. https://host/release ; see ValidateBaseURL
	App      string // release-publish app id; empty: AppID
	Platform string
	Keys     []ed25519.PublicKey
	HTTP     *http.Client // nil: a default client with sane timeouts

	UserAgent string        // sent to the service (it logs it); empty: relclient's default
	Stall     time.Duration // overrides stallTimeout (tests)
	RetryWait time.Duration // first pause between download attempts (default 1 s; tests)
}

// ValidateBaseURL accepts https URLs, and http only for a loopback host
// (development against a local release-publish). It returns the URL without a
// trailing slash.
func ValidateBaseURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", proto.Errorf(proto.CodeInvalid, "update url is not a valid URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", proto.Errorf(proto.CodeInvalid, "update url must not carry credentials, a query or a fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopbackHost(u.Hostname()) {
			return "", proto.Errorf(proto.CodeInvalid, "update url must be https (http is only allowed for loopback)")
		}
	default:
		return "", proto.Errorf(proto.CodeInvalid, "update url must be https")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// refuseOffsiteRedirect stops a redirect (an open redirect on the service or a
// compromised proxy) from moving a request off the configured server or from
// https to http.
func refuseOffsiteRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 3 {
		return errors.New("too many redirects")
	}
	if req.URL.Host != via[0].URL.Host {
		return errors.New("redirect to another host refused")
	}
	if req.URL.Scheme != via[0].URL.Scheme {
		return errors.New("redirect to another scheme refused") // https -> http would drop the transport security
	}
	return nil
}

func newTransport(headerTimeout time.Duration) *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: headerTimeout,
		MaxIdleConns:          2,
		IdleConnTimeout:       90 * time.Second,
	}
}

var (
	defaultHTTP = &http.Client{Transport: newTransport(30 * time.Second), CheckRedirect: refuseOffsiteRedirect}
	// watchHTTP allows a response header to arrive up to 20 s after the
	// service's longest hold.
	watchHTTP = &http.Client{Transport: newTransport(MaxWatchWait + 20*time.Second), CheckRedirect: refuseOffsiteRedirect}
)

func (c *Client) stall() time.Duration {
	if c.Stall > 0 {
		return c.Stall
	}
	return stallTimeout
}

func (c *Client) app() string {
	if c.App != "" {
		return c.App
	}
	return AppID
}

func (c *Client) target() relclient.Target {
	return relclient.Target{App: c.app(), Platform: c.Platform}
}

// rel builds an SDK client over hc. The SDK treats an empty key list as
// "check the shape only"; an app must never run that way.
func (c *Client) rel(hc *http.Client) (*relclient.Client, error) {
	if len(c.Keys) == 0 {
		return nil, rejected("no trusted release key is built into this app")
	}
	opts := []relclient.Option{relclient.WithHTTPClient(hc), relclient.WithTrustedKeys(c.Keys...)}
	if c.UserAgent != "" {
		opts = append(opts, relclient.WithUserAgent(c.UserAgent))
	}
	rc, err := relclient.New(c.BaseURL, opts...)
	if err != nil {
		return nil, proto.Errorf(proto.CodeInvalid, "update url: %v", err)
	}
	return rc, nil
}

func (c *Client) httpOr(def *http.Client) *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return def
}

// accept applies this app's own limits to a manifest the SDK has verified.
func (c *Client) accept(m *Manifest) (*Manifest, error) {
	switch {
	case m == nil:
		return nil, rejected("the server sent no manifest")
	case !ValidPlatform(m.Platform):
		return nil, rejected("unknown platform %q", m.Platform)
	case m.Size > MaxPackageBytes:
		return nil, rejected("size %d out of range", m.Size)
	}
	return m, nil
}

// Check asks for the current stable release. installed is the running
// versionCode: the service counts checks per installed version (relhub stats).
// The manifest is returned even when it is not newer. It returns ErrNoRelease
// when nothing is published and a *proto.Error otherwise.
func (c *Client) Check(ctx context.Context, installed int64) (*Manifest, error) {
	rc, err := c.rel(c.httpOr(defaultHTTP))
	if err != nil {
		return nil, err
	}
	res, err := rc.Check(ctx, c.target(), installed)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrNoRelease
		}
		return nil, mapError("manifest request", err)
	}
	return c.accept(res.Manifest)
}

// Watch blocks until the service offers a release newer than `known`, and
// returns its verified manifest; it returns (nil, nil) when `wait` elapsed
// with nothing new (ask again) and ErrWatchUnsupported when the app is not
// registered.
func (c *Client) Watch(ctx context.Context, known int64, wait time.Duration) (*Manifest, error) {
	if wait <= 0 || wait > MaxWatchWait {
		wait = MaxWatchWait
	}
	rc, err := c.rel(c.httpOr(watchHTTP))
	if err != nil {
		return nil, err
	}
	res, err := rc.Watch(ctx, c.target(), known, wait)
	if err != nil {
		if isNotFound(err) {
			return nil, ErrWatchUnsupported
		}
		return nil, mapError("watch request", err)
	}
	if res == nil {
		return nil, nil
	}
	return c.accept(res.Manifest)
}

// Download fetches the package described by m into dir and returns its final
// path. It resumes a previous <file>.part, verifies size and SHA-256 and only
// then renames it into place, so a path returned (or found already present)
// is always a verified file. m must come from Check or Watch.
func (c *Client) Download(ctx context.Context, m *Manifest, dir string, progress Progress) (string, error) {
	if _, err := c.accept(m); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", failed("create update dir: %v", err)
	}
	final := filepath.Join(dir, m.File)
	if VerifyFile(final, m) == nil {
		return final, nil
	}
	base := c.httpOr(defaultHTTP)
	// A response never needs more than the whole package; one extra byte is
	// let through so the SDK reports an overlong body as a checksum mismatch.
	guarded := &guardTransport{rt: transportOf(base), idle: c.stall(), maxBody: m.Size + 1}
	rc, err := c.rel(&http.Client{Transport: guarded, CheckRedirect: base.CheckRedirect})
	if err != nil {
		return "", err
	}
	err = rc.Download(ctx, *m, final, &relclient.DownloadOptions{Progress: progress, RetryWait: c.RetryWait})
	if err != nil {
		if isNotFound(err) {
			return "", failed("server no longer has %s (withdrawn, or a newer release replaced it)", m.File)
		}
		return "", mapError("download", err)
	}
	return final, nil
}

func transportOf(hc *http.Client) http.RoundTripper {
	if hc.Transport != nil {
		return hc.Transport
	}
	return http.DefaultTransport
}

// isNotFound reports a not_found that the service itself answered, i.e. with its JSON error
// envelope: the app is not registered, or the platform has no release yet. A 404 from anything
// else (a proxy without the /release mount, a wrong URL) is a broken feed, not "nothing to
// update", and must stay visible as an error; without a JSON envelope the SDK reports it with
// the HTTP status line as the message ("404 Not Found: …").
func isNotFound(err error) bool {
	var re *relproto.Error
	return errors.As(err, &re) && re.Code == relproto.CodeNotFound && !strings.HasPrefix(re.Message, "404 ")
}

// mapError turns SDK and transport errors into the stable codes the shells
// show: a refused manifest (bad signature, wrong app, malformed) is
// UPDATE_REJECTED, a package that does not match its manifest is HASH_MISMATCH,
// everything else (network, 5xx) is UPDATE_FAILED and worth retrying.
func mapError(what string, err error) error {
	var re *relproto.Error
	switch {
	case errors.Is(err, relclient.ErrChecksum):
		return proto.Errorf(proto.CodeHashMismatch, "%v", err)
	case errors.As(err, &re) && re.Code == relproto.CodeInvalid:
		return rejected("%s", re.Message)
	case errors.Is(err, context.Canceled):
		return failed("%s cancelled", what)
	}
	return failed("%s: %v", what, err)
}

// guardTransport protects a download from a server that misbehaves:
//
//   - a response whose body goes quiet for idle is aborted. A phone that walks
//     out of Wi-Fi leaves the connection open and silent, and without this the
//     download would hang until the OS gives up. The .part file is kept and the
//     SDK resumes it;
//   - a body longer than maxBody is cut off, so a runaway server cannot fill
//     the disk (the SDK itself writes whatever arrives).
//
// It is only used for downloads: a watch is silent by design.
type guardTransport struct {
	rt      http.RoundTripper
	idle    time.Duration
	maxBody int64
}

func (s *guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	g := &stallGuard{idle: s.idle, cancel: cancel}
	g.timer = time.AfterFunc(s.idle, g.fire)
	resp, err := s.rt.RoundTrip(req.WithContext(ctx))
	if err != nil {
		g.timer.Stop()
		cancel()
		if g.fired.Load() {
			err = g.err()
		}
		return nil, err
	}
	resp.Body = &guardBody{ReadCloser: resp.Body, g: g, left: s.maxBody}
	return resp, nil
}

type stallGuard struct {
	idle   time.Duration
	timer  *time.Timer
	cancel context.CancelFunc
	fired  atomic.Bool
}

func (g *stallGuard) fire() { g.fired.Store(true); g.cancel() }

func (g *stallGuard) err() error { return stalledError{g.idle} }

type guardBody struct {
	io.ReadCloser
	g    *stallGuard
	left int64
}

func (b *guardBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.ReadCloser.Read(p)
	b.left -= int64(n)
	if n > 0 {
		b.g.timer.Reset(b.g.idle)
	}
	if err != nil && err != io.EOF && b.g.fired.Load() {
		err = b.g.err()
	}
	return n, err
}

func (b *guardBody) Close() error {
	b.g.timer.Stop()
	err := b.ReadCloser.Close()
	b.g.cancel()
	return err
}

// stalledError is a timeout, so the SDK treats it as a network error and
// resumes from the .part file.
type stalledError struct{ idle time.Duration }

func (e stalledError) Error() string   { return fmt.Sprintf("download stalled: no data for %s", e.idle) }
func (e stalledError) Timeout() bool   { return true }
func (e stalledError) Temporary() bool { return true }

// VerifyFile reports nil when path exists with exactly the announced size and
// SHA-256.
func VerifyFile(path string, m *Manifest) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() != m.Size {
		return proto.Errorf(proto.CodeHashMismatch, "%s is %d bytes, expected %d", filepath.Base(path), st.Size(), m.Size)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return proto.Errorf(proto.CodeHashMismatch, "%s does not match the published SHA-256", filepath.Base(path))
	}
	return nil
}

func failed(format string, args ...any) error {
	return proto.Errorf(proto.CodeUpdateFailed, format, args...)
}

func rejected(format string, args ...any) error {
	return proto.Errorf(proto.CodeUpdateRejected, "manifest: "+format, args...)
}
