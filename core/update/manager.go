package update

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nightclip/core/proto"
)

// State is the update state machine's position.
type State string

const (
	StateIdle        State = "idle"        // never checked
	StateChecking    State = "checking"    //
	StateUpToDate    State = "up_to_date"  // nothing newer is published
	StateAvailable   State = "available"   // newer release found, not downloaded
	StateDownloading State = "downloading" //
	StateReady       State = "ready"       // verified package on disk, waiting for the shell to install
	StateError       State = "error"       // last check or download failed
)

// Status is the snapshot returned by update.status.
type Status struct {
	Enabled     bool       `json:"enabled"`
	Auto        bool       `json:"auto"`
	State       State      `json:"state"`
	CurrentCode int64      `json:"current_code"`
	CurrentName string     `json:"current_name"`
	Latest      *Release   `json:"latest,omitempty"`
	Mandatory   bool       `json:"mandatory"`
	Received    int64      `json:"received"`
	Total       int64      `json:"total"`
	ReadyPath   string     `json:"ready_path,omitempty"`
	CheckedAt   int64      `json:"checked_at,omitempty"`
	Error       *ErrorInfo `json:"error,omitempty"`
}

// Release is the part of a manifest worth showing to the user.
type Release struct {
	VersionCode int64  `json:"version_code"`
	VersionName string `json:"version_name"`
	Size        int64  `json:"size"`
	Notes       string `json:"notes,omitempty"`
	PublishedAt string `json:"published_at,omitempty"`
}

// ErrorInfo is a stable code plus a message.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Options configures a Manager.
type Options struct {
	Client      *Client // nil: updates are disabled
	Dir         string  // where packages are downloaded
	CurrentCode int64   // installed versionCode; 0 disables updates (dev build)
	CurrentName string
	Auto        bool // download automatically after a check finds a release
	Emit        func(event map[string]any)

	FirstDelay time.Duration // before the first background check (default 20s)
	Interval   time.Duration // between successful checks (default 6h)
	RetryAfter time.Duration // after a failed check (default 30m)
}

// Manager owns update state. All methods are safe for concurrent use.
type Manager struct {
	opt Options

	mu       sync.Mutex
	client   *Client
	auto     bool
	st       Status
	latest   *Manifest
	dlCancel context.CancelFunc
	dlDone   chan struct{}

	runCancel context.CancelFunc
	closed    bool
	wg        sync.WaitGroup
}

// NewManager builds a manager; call Run to start the background loop.
func NewManager(opt Options) *Manager {
	if opt.FirstDelay <= 0 {
		opt.FirstDelay = 20 * time.Second
	}
	if opt.Interval <= 0 {
		opt.Interval = 6 * time.Hour
	}
	if opt.RetryAfter <= 0 {
		opt.RetryAfter = 30 * time.Minute
	}
	if opt.Emit == nil {
		opt.Emit = func(map[string]any) {}
	}
	m := &Manager{opt: opt, client: opt.Client, auto: opt.Auto}
	m.st = Status{State: StateIdle, CurrentCode: opt.CurrentCode, CurrentName: opt.CurrentName}
	return m
}

func (m *Manager) enabledLocked() bool { return m.client != nil && m.opt.CurrentCode > 0 }

// Enabled reports whether a server and a current version are configured.
func (m *Manager) Enabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enabledLocked()
}

// Status returns a snapshot.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}

func (m *Manager) snapshotLocked() Status {
	s := m.st
	s.Enabled = m.enabledLocked()
	s.Auto = m.auto
	if m.latest != nil {
		s.Latest = &Release{
			VersionCode: m.latest.VersionCode, VersionName: m.latest.VersionName,
			Size: m.latest.Size, Notes: m.latest.Notes, PublishedAt: m.latest.PublishedAt,
		}
		s.Mandatory = m.latest.Mandatory(m.opt.CurrentCode)
	}
	return s
}

// SetAuto changes whether a found release is downloaded automatically.
func (m *Manager) SetAuto(auto bool) {
	m.mu.Lock()
	m.auto = auto
	m.mu.Unlock()
}

// SetClient swaps the server (nil disables updates) and forgets what was
// learned from the old one. A running download is cancelled.
func (m *Manager) SetClient(c *Client) {
	m.cancelDownload()
	m.mu.Lock()
	m.client = c
	m.latest = nil
	m.st = Status{State: StateIdle, CurrentCode: m.opt.CurrentCode, CurrentName: m.opt.CurrentName}
	m.mu.Unlock()
}

// Check asks the server for the newest release and updates the state. It
// starts a download when auto is on. Errors are returned and also recorded.
func (m *Manager) Check(ctx context.Context) (Status, error) {
	m.mu.Lock()
	if !m.enabledLocked() {
		m.mu.Unlock()
		return Status{}, proto.Errorf(proto.CodeUpdateDisabled, "updates are not configured in this build")
	}
	if m.st.State == StateDownloading {
		s := m.snapshotLocked()
		m.mu.Unlock()
		return s, nil
	}
	client := m.client
	m.st.State = StateChecking
	m.st.Error = nil
	m.mu.Unlock()

	latest, err := client.Latest(ctx)

	m.mu.Lock()
	if m.client != client { // server changed while we were asking
		s := m.snapshotLocked()
		m.mu.Unlock()
		return s, nil
	}
	m.st.CheckedAt = time.Now().Unix()
	switch {
	case errors.Is(err, ErrNoRelease):
		m.settleUpToDateLocked()
		err = nil
	case err != nil:
		m.setErrorLocked(err)
	case latest.VersionCode <= m.opt.CurrentCode:
		m.settleUpToDateLocked()
	default:
		m.foundLocked(latest)
	}
	s := m.snapshotLocked()
	startDownload := err == nil && m.auto && s.State == StateAvailable
	m.mu.Unlock()

	if err != nil {
		return s, err
	}
	if s.State == StateAvailable {
		m.opt.Emit(map[string]any{
			"type": "update.available", "version_code": s.Latest.VersionCode,
			"version_name": s.Latest.VersionName, "mandatory": s.Mandatory, "notes": s.Latest.Notes,
		})
	}
	if startDownload {
		return m.Download()
	}
	return s, nil
}

func (m *Manager) settleUpToDateLocked() {
	m.latest = nil
	m.st.State = StateUpToDate
	m.st.ReadyPath = ""
	m.st.Received, m.st.Total = 0, 0
	m.pruneLocked("")
}

// foundLocked records a newer release; if its package is already on disk and
// verifies, the state goes straight to ready.
func (m *Manager) foundLocked(latest *Manifest) {
	m.latest = latest
	m.st.Total = latest.Size
	m.st.Received = 0
	m.st.ReadyPath = ""
	m.st.State = StateAvailable
	final := filepath.Join(m.opt.Dir, latest.File)
	if VerifyFile(final, latest) == nil {
		m.st.State = StateReady
		m.st.ReadyPath = final
		m.st.Received = latest.Size
		m.pruneLocked(latest.File)
	}
}

func (m *Manager) setErrorLocked(err error) {
	var pe *proto.Error
	if !errors.As(err, &pe) {
		pe = proto.Errorf(proto.CodeUpdateFailed, "%v", err)
	}
	m.st.State = StateError
	m.st.Error = &ErrorInfo{Code: string(pe.Code), Message: pe.Message}
}

// Download starts fetching the announced release in the background (a no-op
// if it is already downloading or ready) and returns the new status.
func (m *Manager) Download() (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.enabledLocked() || m.closed {
		return Status{}, proto.Errorf(proto.CodeUpdateDisabled, "updates are not configured in this build")
	}
	switch m.st.State {
	case StateDownloading, StateReady:
		return m.snapshotLocked(), nil
	}
	if m.latest == nil || m.latest.VersionCode <= m.opt.CurrentCode {
		return Status{}, proto.Errorf(proto.CodeInvalid, "no update is available to download")
	}

	latest, client := m.latest, m.client
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.dlCancel, m.dlDone = cancel, done
	m.st.State = StateDownloading
	m.st.Error = nil
	m.st.Received, m.st.Total = 0, latest.Size
	m.wg.Add(1)
	go m.download(ctx, client, latest, done)
	return m.snapshotLocked(), nil
}

func (m *Manager) download(ctx context.Context, client *Client, latest *Manifest, done chan struct{}) {
	defer m.wg.Done()
	defer close(done)

	var lastEmit time.Time
	progress := func(received, total int64) {
		m.mu.Lock()
		m.st.Received, m.st.Total = received, total
		m.mu.Unlock()
		if received == total || time.Since(lastEmit) >= 250*time.Millisecond {
			lastEmit = time.Now()
			m.opt.Emit(map[string]any{"type": "update.progress", "received": received, "total": total})
		}
	}
	path, err := client.Download(ctx, latest, m.opt.Dir, progress)

	m.mu.Lock()
	if m.latest != latest { // superseded (server changed)
		m.mu.Unlock()
		return
	}
	if err != nil {
		if ctx.Err() != nil { // cancelled by SetClient/Close, not a failure
			m.st.State, m.st.Error = StateAvailable, nil
			m.mu.Unlock()
			return
		}
		// The release is still announced, so the user can retry.
		m.setErrorLocked(err)
		info := *m.st.Error
		m.mu.Unlock()
		m.opt.Emit(map[string]any{"type": "update.error", "code": info.Code, "message": info.Message})
		return
	}
	m.st.State = StateReady
	m.st.ReadyPath = path
	m.st.Received = latest.Size
	m.pruneLocked(latest.File)
	ev := map[string]any{
		"type": "update.ready", "version_code": latest.VersionCode, "version_name": latest.VersionName,
		"path": path, "mandatory": latest.Mandatory(m.opt.CurrentCode),
	}
	m.mu.Unlock()
	m.opt.Emit(ev)
}

func (m *Manager) cancelDownload() {
	m.mu.Lock()
	cancel, done := m.dlCancel, m.dlDone
	m.dlCancel, m.dlDone = nil, nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// pruneLocked deletes downloaded packages other than keep ("" keeps none).
func (m *Manager) pruneLocked(keep string) {
	entries, err := os.ReadDir(m.opt.Dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == keep || name == keep+".part" {
			continue
		}
		if strings.HasSuffix(name, ".part") || ValidFileName(name) {
			_ = os.Remove(filepath.Join(m.opt.Dir, name))
		}
	}
}

// Run starts the background loop: a first check after FirstDelay, then every
// Interval (RetryAfter after a failure), until Close. It returns immediately.
func (m *Manager) Run() {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.runCancel = cancel
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		wait := m.opt.FirstDelay
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter(wait)):
			}
			wait = m.opt.Interval
			if !m.Enabled() {
				continue
			}
			cctx, ccancel := context.WithTimeout(ctx, 30*time.Second)
			_, err := m.Check(cctx)
			ccancel()
			if err != nil {
				wait = m.opt.RetryAfter
			}
		}
	}()
}

// Close stops the loop and any download, and waits for them.
func (m *Manager) Close() {
	m.mu.Lock()
	cancel := m.runCancel
	m.runCancel = nil
	m.closed = true
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.cancelDownload()
	m.wg.Wait()
}

// jitter spreads d by ±10% so devices do not check in lockstep.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	return d + time.Duration((rand.Float64()*0.2-0.1)*float64(d))
}
