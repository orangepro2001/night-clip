package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nightclip/core/proto"
)

const (
	maxManifestBytes = 64 << 10
	stallTimeout     = 60 * time.Second
)

// ErrNoRelease is returned by Latest when the server has nothing published
// for the platform.
var ErrNoRelease = errors.New("no release published")

// Client talks to a distribution server (cbdist) for one platform.
type Client struct {
	BaseURL  string // e.g. https://host/clipbridge ; see ValidateBaseURL
	Platform string
	Keys     []ed25519.PublicKey
	HTTP     *http.Client // nil: a default client with sane timeouts
	// Stall overrides stallTimeout (tests).
	Stall time.Duration
}

// ValidateBaseURL accepts https URLs, and http only for a loopback host
// (development against a local cbdist). It returns the URL without a
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

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTP
}

var defaultHTTP = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          2,
		IdleConnTimeout:       30 * time.Second,
	},
	// Following a redirect to another host would let an open redirect move
	// the download off the configured server.
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return errors.New("too many redirects")
		}
		if req.URL.Host != via[0].URL.Host {
			return errors.New("redirect to another host refused")
		}
		return nil
	},
}

func (c *Client) endpoint(parts ...string) string {
	return c.BaseURL + "/v1/" + c.Platform + "/" + strings.Join(parts, "/")
}

// Latest fetches and verifies the current manifest. It returns ErrNoRelease
// on 404, a *proto.Error otherwise.
func (c *Client) Latest(ctx context.Context) (*Manifest, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("latest"), nil)
	if err != nil {
		return nil, proto.Errorf(proto.CodeInvalid, "update url: %v", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, failed("manifest request: %v", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNoRelease
	case resp.StatusCode != http.StatusOK:
		return nil, failed("manifest request: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, failed("manifest read: %v", err)
	}
	if len(body) > maxManifestBytes {
		return nil, rejected("manifest too large")
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, rejected("not valid JSON")
	}
	if err := m.Verify(c.Keys); err != nil {
		return nil, err
	}
	if m.Platform != c.Platform {
		return nil, rejected("manifest is for %q, not %q", m.Platform, c.Platform)
	}
	return &m, nil
}

// ErrWatchUnsupported means the server has no watch endpoint (an older cbdist).
var ErrWatchUnsupported = errors.New("server does not support watch")

// watchHTTP allows a response header to arrive up to a minute after the request
// (the server holds a watch for up to MaxWatchWait).
var watchHTTP = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: MaxWatchWait + 20*time.Second,
		MaxIdleConns:          1,
		IdleConnTimeout:       90 * time.Second,
	},
	CheckRedirect: defaultHTTP.CheckRedirect,
}

// Watch blocks until the server has a release newer than `known`, and returns
// its verified manifest; it returns (nil, nil) when `wait` elapsed with
// nothing new (ask again) and ErrWatchUnsupported when the route is missing.
func (c *Client) Watch(ctx context.Context, known int64, wait time.Duration) (*Manifest, error) {
	if wait <= 0 || wait > MaxWatchWait {
		wait = MaxWatchWait
	}
	url := fmt.Sprintf("%s?code=%d&wait=%s", c.endpoint("watch"), known, strconv.FormatFloat(wait.Seconds(), 'f', -1, 64))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, proto.Errorf(proto.CodeInvalid, "update url: %v", err)
	}
	hc := watchHTTP
	if c.HTTP != nil {
		hc = c.HTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, failed("watch request: %v", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return nil, ErrWatchUnsupported
	case http.StatusOK:
	default:
		return nil, failed("watch request: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, failed("watch read: %v", err)
	}
	if len(body) > maxManifestBytes {
		return nil, rejected("manifest too large")
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, rejected("not valid JSON")
	}
	if err := m.Verify(c.Keys); err != nil {
		return nil, err
	}
	if m.Platform != c.Platform {
		return nil, rejected("manifest is for %q, not %q", m.Platform, c.Platform)
	}
	return &m, nil
}

// Progress is called with the bytes on disk so far and the total.
type Progress func(received, total int64)

// Download fetches the package described by m into dir and returns its final
// path. It resumes a previous <file>.part, verifies size and SHA-256 and only
// then renames it into place, so a path returned (or found already present)
// is always a verified file. m must already be verified.
func (c *Client) Download(ctx context.Context, m *Manifest, dir string, progress Progress) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", failed("create update dir: %v", err)
	}
	final := filepath.Join(dir, m.File)
	if VerifyFile(final, m) == nil {
		return final, nil
	}
	part := final + ".part"

	for attempt := 0; attempt < 2; attempt++ {
		restart, err := c.fetchInto(ctx, m, part, progress)
		if err != nil {
			return "", err
		}
		if restart {
			continue
		}
		if err := VerifyFile(part, m); err != nil {
			_ = os.Remove(part)
			return "", err
		}
		if err := os.Rename(part, final); err != nil {
			return "", failed("finalise download: %v", err)
		}
		return final, nil
	}
	return "", failed("server keeps rejecting the resume request")
}

// fetchInto appends the missing bytes to part. restart is true when the
// server could not serve the requested range and part was discarded.
func (c *Client) fetchInto(ctx context.Context, m *Manifest, part string, progress Progress) (restart bool, err error) {
	offset := int64(0)
	if st, err := os.Stat(part); err == nil {
		offset = st.Size()
	}
	if offset > m.Size {
		_ = os.Remove(part)
		offset = 0
	}
	if offset == m.Size {
		return false, nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	guard := time.AfterFunc(c.stall(), cancel)
	defer guard.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("files", m.File), nil)
	if err != nil {
		return false, failed("download request: %v", err)
	}
	if offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return false, failed("download request: %v", err)
	}
	defer resp.Body.Close()

	flags := os.O_WRONLY | os.O_CREATE
	switch resp.StatusCode {
	case http.StatusOK:
		offset = 0 // server ignored the range: take the whole body
		flags |= os.O_TRUNC
	case http.StatusPartialContent:
		start, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != offset || total != m.Size {
			_ = os.Remove(part)
			return true, nil
		}
		flags |= os.O_APPEND
	case http.StatusRequestedRangeNotSatisfiable:
		_ = os.Remove(part)
		return true, nil
	case http.StatusNotFound:
		return false, failed("server no longer has %s (a newer release may have been published)", m.File)
	default:
		return false, failed("download request: HTTP %d", resp.StatusCode)
	}

	f, err := os.OpenFile(part, flags, 0o600)
	if err != nil {
		return false, failed("open %s: %v", part, err)
	}
	defer f.Close()

	received := offset
	buf := make([]byte, 64<<10)
	body := io.LimitReader(resp.Body, m.Size-offset+1) // +1 detects an overlong body
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			guard.Reset(c.stall())
			if received+int64(n) > m.Size {
				_ = f.Close()
				_ = os.Remove(part)
				return false, proto.Errorf(proto.CodeHashMismatch, "server sent more than the announced %d bytes", m.Size)
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				return false, failed("write %s: %v", part, werr)
			}
			received += int64(n)
			if progress != nil {
				progress(received, m.Size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			// Keep the .part: the next attempt resumes from here.
			if ctx.Err() != nil {
				return false, failed("download stalled or cancelled after %d of %d bytes", received, m.Size)
			}
			return false, failed("download interrupted after %d of %d bytes: %v", received, m.Size, rerr)
		}
	}
	if received != m.Size {
		return false, failed("download ended after %d of %d bytes", received, m.Size)
	}
	return false, nil
}

func (c *Client) stall() time.Duration {
	if c.Stall > 0 {
		return c.Stall
	}
	return stallTimeout
}

// parseContentRange parses "bytes START-END/TOTAL".
func parseContentRange(h string) (start, total int64, ok bool) {
	var end int64
	if _, err := fmt.Sscanf(h, "bytes %d-%d/%d", &start, &end, &total); err != nil {
		return 0, 0, false
	}
	return start, total, end >= start && end < total
}

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
