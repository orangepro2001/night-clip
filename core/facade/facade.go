// Package facade is the single entry point shared by the Windows DLL, the
// Android gomobile binding and (later) the named-pipe server:
//
//	Call(method, paramsJSON) -> {"ok":true,"result":...} | {"ok":false,"error":{...}}
//	NextEvent(timeoutMs)     -> event JSON, or "" on timeout
//
// Shells never reach into other core packages. See docs/api-spec.md §7.
package facade

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"nightclip/core/identity"
	"nightclip/core/proto"
)

// Version is the core version reported by the "version" method.
const Version = "0.0.1"

const eventQueueSize = 256

// Config is the JSON passed to Init (docs/api-spec.md §7.2).
type Config struct {
	DataDir    string `json:"data_dir"`
	DBKeyB64   string `json:"db_key_b64"`
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
	// TODO(phase0/store): load or persist the identity via the encrypted
	// store. Until then the identity is ephemeral per process.
	id, err := identity.Generate(cfg.DeviceName)
	if err != nil {
		return err
	}
	c.cfg, c.id, c.started = cfg, id, true
	c.emit(map[string]any{"type": "core.started", "device_id": id.DeviceID})
	return nil
}

// Shutdown stops the core and wakes any NextEvent callers.
func (c *Core) Shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	close(c.done)
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
// Replace with the structured logger once the store package lands.
func logPanic(r any) {
	fmt.Fprintf(os.Stderr, "clipbridge-core: recovered panic: %v\n%s\n", r, debug.Stack())
}
