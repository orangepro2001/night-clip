// Package facade is the single entry point shared by the Windows DLL, the
// Android gomobile binding and (later) the named-pipe server:
//
//	Call(method, paramsJSON) -> {"ok":true,"result":...} | {"ok":false,"error":{...}}
//	NextEvent(timeoutMs)     -> event JSON, or "" on timeout
//
// Shells never reach into other core packages. See docs/api-spec.md §7.
package facade

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"nightclip/core/identity"
	"nightclip/core/pairing"
	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
	"nightclip/core/update"
	"nightclip/core/vsync"
	"nightclip/core/xfer"
)

// Version is the core version reported by the "version" method.
const Version = "0.0.1"

const eventQueueSize = 1024

// Config is the JSON passed to Init (docs/api-spec.md §7.2).
type Config struct {
	DataDir    string `json:"data_dir"`   // absolute path; holds clipbridge.db
	DBKeyB64   string `json:"db_key_b64"` // base64 of exactly 32 bytes
	DeviceName string `json:"device_name"`
	Platform   string `json:"platform"` // "windows" | "android"
	Listen     bool   `json:"listen"`
	Port       int    `json:"port"`

	// Self-update (docs/update.md). AppVersionCode is the installed build's
	// monotonically increasing versionCode; 0 marks a development build and
	// disables updates. UpdateURL is the build-time default server; a user
	// override stored via update.config takes precedence.
	AppVersionCode int64  `json:"app_version_code"`
	AppVersionName string `json:"app_version_name"`
	UpdateURL      string `json:"update_url"`

	// Pipe, when set, is where the local control channel listens (Windows: the named
	// pipe \\.\pipe\clipbridge.v1; tests: a Unix socket path). Empty disables it.
	Pipe string `json:"pipe"`
}

// Core is one running core instance. Exactly one is expected per process.
type Core struct {
	mu      sync.RWMutex
	cfg     Config
	id      *identity.Identity
	db      *store.DB
	upd     *update.Manager
	n       *netState
	ctl     *controlChannel
	vs      vaultState
	started bool
	closed  bool

	events  chan []byte
	dropped atomic.Uint64
	done    chan struct{}

	// Events from the network layer that happen while Init is still running are held
	// until core.started has been emitted, so a shell always sees core.started first.
	gateMu   sync.Mutex
	gateOpen bool
	held     []map[string]any

	// Test-only knobs, set by in-package tests before Init; zero in production.
	hooks  peer.Hooks
	timing peer.Timing
	pairT  pairing.Timings
	xferT  xfer.Timing
	syncT  vsync.Timing
}

// testEngine, set only by this package's tests (see network_test.go), makes
// every Core they create talk over loopback on random ports instead of binding
// the machine's real tailnet addresses. It is nil in production.
var testEngine *struct {
	hooks  peer.Hooks
	timing peer.Timing
	pairT  pairing.Timings
	xferT  xfer.Timing
	syncT  vsync.Timing
}

// New returns an uninitialised Core.
func New() *Core {
	c := &Core{events: make(chan []byte, eventQueueSize), done: make(chan struct{})}
	if testEngine != nil {
		c.hooks, c.timing, c.pairT, c.xferT, c.syncT = testEngine.hooks, testEngine.timing, testEngine.pairT, testEngine.xferT, testEngine.syncT
	}
	return c
}

// Init configures the core. It is an error to call it twice.
func (c *Core) Init(configJSON string) (err error) {
	defer recoverTo(&err)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return proto.Errorf(proto.CodeInvalid, "core has been shut down")
	}
	if c.started {
		return proto.Errorf(proto.CodeInvalid, "already initialised")
	}
	var cfg Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return proto.Errorf(proto.CodeInvalid, "config: %v", err)
	}
	if cfg.Platform != "windows" && cfg.Platform != "android" {
		return proto.Errorf(proto.CodeInvalid, "platform must be windows or android")
	}
	if cfg.DeviceName == "" {
		return proto.Errorf(proto.CodeInvalid, "device_name is required")
	}
	if cfg.Port == 0 {
		cfg.Port = 47821
	}
	key, err := parseDBKey(cfg)
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.DataDir, key)
	if err != nil {
		return err
	}
	id, err := db.LoadOrCreateIdentity(cfg.DeviceName, cfg.Platform)
	if err != nil {
		_ = db.Close()
		return err
	}
	upd, err := c.newUpdateManager(cfg, db)
	if err != nil {
		_ = db.Close()
		return err
	}
	c.cfg, c.id, c.db = cfg, id, db
	n, err := c.startNetwork(cfg)
	if err != nil {
		_ = db.Close()
		return err
	}
	c.n, c.upd, c.started = n, upd, true
	c.startControlChannel(cfg.Pipe)
	upd.Run()
	c.emit(map[string]any{"type": "core.started", "device_id": id.DeviceID})
	c.openGate()
	return nil
}

// parseDBKey validates the storage fields of cfg and returns the 32-byte
// database key. The padded and unpadded standard base64 forms are both
// accepted so shells need not care which their platform produces.
func parseDBKey(cfg Config) ([]byte, error) {
	if cfg.DataDir == "" {
		return nil, proto.Errorf(proto.CodeInvalid, "data_dir is required")
	}
	if !filepath.IsAbs(cfg.DataDir) {
		return nil, proto.Errorf(proto.CodeInvalid, "data_dir must be an absolute path")
	}
	if cfg.DBKeyB64 == "" {
		return nil, proto.Errorf(proto.CodeInvalid, "db_key_b64 is required")
	}
	key, err := base64.StdEncoding.DecodeString(cfg.DBKeyB64)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(cfg.DBKeyB64)
	}
	if err != nil {
		return nil, proto.Errorf(proto.CodeInvalid, "db_key_b64 is not valid base64")
	}
	if len(key) != 32 {
		return nil, proto.Errorf(proto.CodeInvalid, "db_key_b64 must decode to exactly 32 bytes")
	}
	return key, nil
}

// Shutdown stops the core, closes the store and wakes any NextEvent callers.
// It is idempotent; every method answers NOT_INITIALIZED afterwards.
func (c *Core) Shutdown() {
	// Only the state change happens under the lock. Closing the components waits for
	// goroutines (pipe handlers, transfers) that may be inside Call -> dispatch, which
	// needs the read lock: holding the write lock here would deadlock against them.
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.done)
	upd, ctl, n, db := c.upd, c.ctl, c.n, c.db
	c.mu.Unlock()

	if ctl != nil { // first: no new outside requests
		ctl.close()
	}
	c.closeVault()
	if upd != nil {
		upd.Close()
	}
	if n != nil {
		n.close()
	}
	if db != nil { // last: everything above may still write
		_ = db.Close()
	}
}

// Call dispatches a method. It never panics and always returns JSON.
func (c *Core) Call(method, paramsJSON string) (out string) {
	defer func() {
		if r := recover(); r != nil {
			logPanic(r)
			out = failure(proto.Errorf(proto.CodeInternal, "internal error"))
		}
	}()
	result, err := c.dispatch(method, paramsJSON)
	if err != nil {
		return failure(err)
	}
	b, err := json.Marshal(map[string]any{"ok": true, "result": result})
	if err != nil {
		return failure(err)
	}
	return string(b)
}

// NextEvent blocks up to timeoutMs for the next event; "" means timeout or
// shutdown.
func (c *Core) NextEvent(timeoutMs int) string {
	if timeoutMs < 0 {
		timeoutMs = 0
	}
	t := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer t.Stop()
	select {
	case ev := <-c.events:
		return string(ev)
	case <-t.C:
		return ""
	case <-c.done:
		return ""
	}
}

func (c *Core) dispatch(method, paramsJSON string) (any, error) {
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return nil, proto.Errorf(proto.CodeNotInitialized, "core has been shut down")
	}
	switch method {
	case "version":
		return map[string]any{"core": Version, "protocol": "clipbridge/v1"}, nil
	case "status":
		return c.status()
	case "identity.get":
		return c.identity()
	case "update.status", "update.check", "update.download", "update.config":
		return c.updateCall(method, paramsJSON)
	case "vault.status", "vault.create", "vault.unlock", "vault.lock", "vault.verify_master", "vault.change_master",
		"vault.generate", "vault.strength", "vault.backup_list", "vault.backup_verify", "vault.backup_now",
		"vault.list", "vault.get", "vault.reveal", "vault.copy", "vault.totp", "vault.add", "vault.update", "vault.delete",
		"vault.restore_entry", "vault.empty_trash", "vault.move", "vault.groups", "vault.group_add", "vault.group_rename",
		"vault.group_delete", "vault.history", "vault.history_restore", "vault.attach_add", "vault.attach_get",
		"vault.attach_remove", "vault.health", "vault.restore_preview", "vault.restore", "vault.import_file",
		"vault.import_csv", "vault.export_file", "vault.export_csv",
		"vault.sync_status", "vault.sync_enable", "vault.sync_resolve", "vault.sync_now":
		return c.vaultCall(method, paramsJSON)
	default:
		if result, handled, err := c.networkCall(method, paramsJSON); handled {
			return result, err
		}
		return nil, proto.Errorf(proto.CodeMethodNotFound, "unknown method %q", method)
	}
}

func (c *Core) status() (any, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.started {
		return nil, proto.Errorf(proto.CodeNotInitialized, "call Init first")
	}
	st := map[string]any{
		"device_id":      c.id.DeviceID,
		"device_name":    c.cfg.DeviceName,
		"platform":       c.cfg.Platform,
		"listen":         c.cfg.Listen,
		"port":           c.cfg.Port,
		"peers":          []any{},
		"events_dropped": c.dropped.Load(),
	}
	if c.n != nil {
		st["peers"] = c.n.eng.PeerStatuses()
		st["capture"] = c.n.clip.Setting("capture")
		// false until the user has answered the first-run question, so a shell can tell
		// "never asked" from "asked and said no" (both read as capture = off)
		_, configured, _ := c.db.GetSetting("capture")
		st["capture_configured"] = configured
		addrs := []string{}
		for _, a := range c.n.eng.ListenAddrs() {
			addrs = append(addrs, a.String())
		}
		st["listening_addrs"] = addrs
		st["listening_port"] = c.n.eng.ListenPort()
	}
	return st, nil
}

func (c *Core) identity() (any, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.started {
		return nil, proto.Errorf(proto.CodeNotInitialized, "call Init first")
	}
	return map[string]any{
		"device_id":   c.id.DeviceID,
		"fingerprint": c.id.Fingerprint(),
	}, nil
}

// emitNet is what the engine and the services emit through: it waits for
// core.started when Init has not finished yet.
func (c *Core) emitNet(ev map[string]any) {
	c.gateMu.Lock()
	if !c.gateOpen {
		c.held = append(c.held, ev)
		c.gateMu.Unlock()
		return
	}
	c.gateMu.Unlock()
	c.emit(ev)
}

// openGate releases the held events in order and lets later ones through directly.
func (c *Core) openGate() {
	c.gateMu.Lock()
	held := c.held
	c.held, c.gateOpen = nil, true
	c.gateMu.Unlock()
	for _, ev := range held {
		c.emit(ev)
	}
}

// emit queues an event for NextEvent; if the queue is full the event is
// dropped and counted, so a stuck shell can never block the core.
func (c *Core) emit(ev map[string]any) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	select {
	case c.events <- b:
	default:
		c.dropped.Add(1)
	}
}

func failure(err error) string {
	pe, ok := err.(*proto.Error)
	if !ok {
		pe = proto.Errorf(proto.CodeInternal, "%v", err)
	}
	b, _ := json.Marshal(map[string]any{"ok": false, "error": pe})
	return string(b)
}

func recoverTo(err *error) {
	if r := recover(); r != nil {
		logPanic(r)
		*err = proto.Errorf(proto.CodeInternal, "internal error")
	}
}

// logPanic keeps the panic value and stack out of any user-visible message.
// TODO: replace with a structured file logger (stderr is lost in GUI processes).
func logPanic(r any) {
	fmt.Fprintf(os.Stderr, "clipbridge-core: recovered panic: %v\n%s\n", r, debug.Stack())
}
