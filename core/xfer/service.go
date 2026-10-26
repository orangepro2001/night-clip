package xfer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// Settings keys.
const (
	SettingDownloadDir  = "download_dir"   // where received files are saved
	SettingMaxFileBytes = "max_file_bytes" // refuse larger offers
	defaultMaxFileBytes = int64(4) << 30   // 4 GiB
	offerTTL            = 24 * time.Hour   // docs/architecture.md §4.5
	spaceHeadroom       = int64(16) << 20  // keep this much free beyond the file
	copyBuffer          = 256 << 10
)

// Transfer states.
const (
	StateHashing      = "hashing"
	StateOffered      = "offered"
	StateAccepted     = "accepted"
	StateTransferring = "transferring"
	StateDone         = "done"
	StateFailed       = "failed"
	StateCancelled    = "cancelled"
	StateRejected     = "rejected"
	StateExpired      = "expired"
)

func terminal(state string) bool {
	switch state {
	case StateDone, StateFailed, StateCancelled, StateRejected, StateExpired:
		return true
	}
	return false
}

// Direction values.
const (
	DirIn  = "in"
	DirOut = "out"
)

// Mode values: who moves the bytes.
const (
	ModePull = "pull" // the receiver GETs from the sender's server
	ModePush = "push" // the sender PUTs to the receiver's server
)

// Timing holds the intervals; zero values take the defaults.
type Timing struct {
	ProgressEvery time.Duration // between file.progress events (250 ms)
	RetryBase     time.Duration // first retry delay after a broken transfer (1 s)
	RetryMax      time.Duration // ... doubling up to this (30 s)
	Stall         time.Duration // a transfer with no bytes for this long is retried (60 s)
	Sweep         time.Duration // expiry sweeps (1 min)
}

func (t Timing) withDefaults() Timing {
	def := func(v *time.Duration, d time.Duration) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&t.ProgressEvery, 250*time.Millisecond)
	def(&t.RetryBase, time.Second)
	def(&t.RetryMax, 30*time.Second)
	def(&t.Stall, 60*time.Second)
	def(&t.Sweep, time.Minute)
	return t
}

// Hooks are test-only; production leaves them nil.
type Hooks struct {
	// Chunk is called before each chunk of a transfer moves, with the offset the
	// chunk starts at. A non-nil error aborts that attempt like a dropped connection.
	Chunk func(id string, offset int64) error
}

// Config configures a Service.
type Config struct {
	Engine   *peer.Engine
	DB       *store.DB
	DataDir  string // the core's data directory (outgoing copies of non-seekable sources live here)
	Platform string // "windows" | "android": decides the default save directory
	Emit     func(map[string]any)
	Now      func() time.Time
	Timing   Timing
	Hooks    Hooks
}

// Service is the file-transfer engine.
type Service struct {
	e       *peer.Engine
	db      *store.DB
	dataDir string
	plat    string
	emit    func(map[string]any)
	now     func() time.Time
	timing  Timing
	hooks   Hooks

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	tasks   map[string]context.CancelFunc // running download/upload/hash goroutines
	sources map[string]*os.File           // out transfers backed by a descriptor handed to us
	rates   map[string]*meter
	started bool
	closed  bool
}

// New builds the service and registers its HTTP routes on the engine, so call it
// before Engine.Start. Call Start after the engine started.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Emit == nil {
		cfg.Emit = func(map[string]any) {}
	}
	s := &Service{
		e: cfg.Engine, db: cfg.DB, dataDir: cfg.DataDir, plat: cfg.Platform, emit: cfg.Emit, now: cfg.Now,
		timing: cfg.Timing.withDefaults(), hooks: cfg.Hooks,
		tasks: map[string]context.CancelFunc{}, sources: map[string]*os.File{}, rates: map[string]*meter{},
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.e.Handle("GET /v1/files/{id}", routeFunc(s.serveGet), false)
	s.e.Handle("HEAD /v1/files/{id}", routeFunc(s.serveHead), false)
	s.e.Handle("PUT /v1/files/{id}", routeFunc(s.servePut), false)
	return s
}

// Start resumes transfers that were running when the core last stopped and
// begins sweeping expired ones.
func (s *Service) Start() {
	s.mu.Lock()
	if s.started || s.closed {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()

	// An outgoing file that was handed to us as a descriptor cannot be reopened:
	// those transfers cannot resume.
	if rows, err := s.db.ListTransfersInStates(StateHashing, StateOffered, StateAccepted, StateTransferring); err == nil {
		for _, t := range rows {
			switch {
			case t.Direction == DirOut && t.Path == "":
				s.fail(t, "SOURCE_LOST", "the file was handed over as a descriptor and the app restarted")
			case t.Direction == DirOut && t.State == StateHashing:
				s.startHash(t)
			case t.Direction == DirIn && t.Mode == ModePull && (t.State == StateAccepted || t.State == StateTransferring):
				s.startDownload(t.ID)
			case t.Direction == DirOut && t.Mode == ModePush && t.State == StateTransferring:
				s.startUpload(t.ID)
			}
		}
	}
	s.wg.Add(1)
	go s.sweepLoop()
}

// Close stops every running transfer. Partial files stay for the next start.
func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	s.wg.Wait()
	s.mu.Lock()
	for id, f := range s.sources {
		_ = f.Close()
		delete(s.sources, id)
	}
	s.mu.Unlock()
}

// ---- settings ------------------------------------------------------------------

// MaxFileBytes returns the largest offer this device accepts.
func (s *Service) MaxFileBytes() int64 {
	if v, found, _ := s.db.GetSetting(SettingMaxFileBytes); found {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 1<<20 {
			return n
		}
	}
	return defaultMaxFileBytes
}

// DownloadDir returns where accepted files go by default.
func (s *Service) DownloadDir() string {
	if v, found, _ := s.db.GetSetting(SettingDownloadDir); found && filepath.IsAbs(v) {
		return v
	}
	if s.plat == "windows" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Downloads", "ClipBridge")
		}
	}
	return filepath.Join(s.dataDir, "received") // Android: the shell moves finished files to MediaStore
}

// ValidateSetting checks the file-related settings.
func ValidateSetting(key, value string) error {
	switch key {
	case SettingDownloadDir:
		if !filepath.IsAbs(value) {
			return proto.Errorf(proto.CodeInvalid, "download_dir must be an absolute path")
		}
	case SettingMaxFileBytes:
		if n, err := strconv.ParseInt(value, 10, 64); err != nil || n < 1<<20 || n > 1<<40 {
			return proto.Errorf(proto.CodeInvalid, "max_file_bytes must be between 1 MiB and 1 TiB")
		}
	default:
		return proto.Errorf(proto.CodeInvalid, "unknown setting %q", key)
	}
	return nil
}

// ---- model ------------------------------------------------------------------

// Info is a transfer as shown to the shells.
type Info struct {
	ID        string `json:"transfer_id"`
	PeerID    string `json:"peer_id"`
	PeerName  string `json:"peer_name"`
	Direction string `json:"direction"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	State     string `json:"state"`
	Offset    int64  `json:"offset"`
	RateBPS   int64  `json:"rate_bps"`
	Error     string `json:"error,omitempty"`
	Path      string `json:"path,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
}

func (s *Service) info(t store.Transfer) Info {
	p, _, _ := s.db.GetPeer(t.PeerID)
	path := ""
	if t.Direction == DirIn && t.State == StateDone {
		path = t.Path
	}
	return Info{
		ID: t.ID, PeerID: t.PeerID, PeerName: p.Name, Direction: t.Direction, Name: t.Name, Size: t.Size,
		SHA256: t.SHA256, State: t.State, Offset: t.Offset, RateBPS: s.rate(t.ID), Error: t.Error, Path: path,
		MimeType: t.MimeType, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
	}
}

// List returns transfers, newest first.
func (s *Service) List(limit int) ([]Info, error) {
	rows, err := s.db.ListTransfers(limit)
	if err != nil {
		return nil, err
	}
	out := make([]Info, 0, len(rows))
	for _, t := range rows {
		out = append(out, s.info(t))
	}
	return out, nil
}

// ---- helpers ------------------------------------------------------------------

var (
	transferIDRe = regexp.MustCompile(`^tr_[0-9a-f]{16}$`)
	sha256HexRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func newTransferID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "tr_" + hex.EncodeToString(b[:])
}

func (s *Service) get(id string) (store.Transfer, error) {
	t, found, err := s.db.GetTransfer(id)
	if err != nil {
		return store.Transfer{}, err
	}
	if !found {
		return store.Transfer{}, proto.Errorf(proto.CodeInvalid, "no such transfer %s", id)
	}
	return t, nil
}

// directionOf returns "in" or "out" for a transfer ("" when unknown).
func (s *Service) directionOf(id string) string {
	if t, found, _ := s.db.GetTransfer(id); found {
		return t.Direction
	}
	return ""
}

func (s *Service) setState(t store.Transfer, state string) store.Transfer {
	_ = s.db.SetTransferState(t.ID, state, "")
	t.State, t.Error = state, ""
	s.emit(map[string]any{"type": "file.state", "transfer_id": t.ID, "direction": t.Direction, "state": state})
	return t
}

// fail ends a transfer with an error code and tells the shells.
func (s *Service) fail(t store.Transfer, code, msg string) {
	if terminal(t.State) {
		return
	}
	_ = s.db.SetTransferState(t.ID, StateFailed, code)
	s.dropSource(t.ID)
	s.emit(map[string]any{"type": "file.failed", "transfer_id": t.ID, "code": code, "message": msg, "direction": t.Direction})
	s.emit(map[string]any{"type": "file.state", "transfer_id": t.ID, "direction": t.Direction, "state": StateFailed})
}

func (s *Service) control(peerID string, payload map[string]any) {
	_, _ = s.e.Send(peerID, proto.KindControl, payload, peer.SendOptions{TTL: 10 * time.Minute})
}

func mimeFor(name string) string {
	if t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); t != "" {
		return t
	}
	return "application/octet-stream"
}

// hashReader copies r to a sha256 and returns the hex digest and byte count.
func hashReader(ctx context.Context, r io.Reader) (string, int64, error) {
	h := sha256.New()
	buf := make([]byte, copyBuffer)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return "", n, err
		}
		m, err := r.Read(buf)
		if m > 0 {
			h.Write(buf[:m])
			n += int64(m)
		}
		if err == io.EOF {
			return hex.EncodeToString(h.Sum(nil)), n, nil
		}
		if err != nil {
			return "", n, err
		}
	}
}

var errTransient = errors.New("transient transfer error")

// chunkHook runs the test hook, if any.
func (s *Service) chunkHook(id string, offset int64) error {
	if s.hooks.Chunk != nil {
		return s.hooks.Chunk(id, offset)
	}
	return nil
}

func routeFunc(f http.HandlerFunc) http.Handler { return f }

// decodePayload unmarshals an event payload.
func decodePayload(env *proto.Envelope, v any) bool { return json.Unmarshal(env.Payload, v) == nil }
