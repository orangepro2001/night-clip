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
	"nightclip/core/proto"
	"nightclip/core/store"
)

// Version is the core version reported by the "version" method.
const Version = "0.0.1"

const eventQueueSize = 256

// Config is the JSON passed to Init (docs/api-spec.md §7.2).
type Config struct {
	DataDir    string `json:"data_dir"`   // absolute path; holds clipbridge.db
	DBKeyB64   string `json:"db_key_b64"` // base64 of exactly 32 bytes
	DeviceName string `json:"device_name"`
	Platform   string `json:"platform"` // "windows" | "android"
	Listen     bool   `json:"listen"`
	Port       int    `json:"port"`
}

// Core is one running core instance. Exactly one is expected per process.
type Core struct {
	mu      sync.RWMutex
	cfg     Config
	id      *identity.Identity
	db      *store.DB
	started bool
	closed  bool

	events  chan []byte
	dropped atomic.Uint64
	done    chan struct{}
}

// New returns an uninitialised Core.
func New() *Core {
	return &Core{events: make(chan []byte, eventQueueSize), done: make(chan struct{})}
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
	c.cfg, c.id, c.db, c.started = cfg, id, db, true
	c.emit(map[string]any{"type": "core.started", "device_id": id.DeviceID})
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
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.done)
	if c.db != nil {
		_ = c.db.Close()
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
	default:
		return nil, proto.Errorf(proto.CodeMethodNotFound, "unknown method %q", method)
	}
}

func (c *Core) status() (any, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !c.started {
		return nil, proto.Errorf(proto.CodeNotInitialized, "call Init first")
	}
	return map[string]any{
		"device_id":      c.id.DeviceID,
		"device_name":    c.cfg.DeviceName,
		"platform":       c.cfg.Platform,
		"listen":         c.cfg.Listen,
		"port":           c.cfg.Port,
		"peers":          []any{},
		"events_dropped": c.dropped.Load(),
	}, nil
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
