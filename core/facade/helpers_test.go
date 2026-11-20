package facade

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// node is a Core driven purely through its public JSON API, with every event it emits recorded.
type node struct {
	t    *testing.T
	c    *Core
	name string

	mu     sync.Mutex
	events []map[string]any
}

func startNode(t *testing.T, name, platform string) *node {
	t.Helper()
	c := New()
	cfg := configFor(t, t.TempDir(), func(m map[string]any) {
		m["device_name"] = name
		m["platform"] = platform
		m["db_key_b64"] = testKeyB64(byte(len(name)))
	})
	if err := c.Init(cfg); err != nil {
		t.Fatalf("Init %s: %v", name, err)
	}
	n := &node{t: t, c: c, name: name}
	go func() {
		for {
			select {
			case <-c.done:
				return
			default:
			}
			raw := c.NextEvent(50)
			if raw == "" {
				continue
			}
			var ev map[string]any
			if json.Unmarshal([]byte(raw), &ev) == nil {
				n.mu.Lock()
				n.events = append(n.events, ev)
				n.mu.Unlock()
			}
		}
	}()
	t.Cleanup(c.Shutdown)
	return n
}

// call runs a method and fails the test unless it succeeded.
func (n *node) call(method string, params any) map[string]any {
	n.t.Helper()
	r := n.try(method, params)
	if !r.OK {
		n.t.Fatalf("%s.%s failed: %s", n.name, method, r.Error.Code)
	}
	var out map[string]any
	if len(r.Result) > 0 {
		_ = json.Unmarshal(r.Result, &out)
	}
	return out
}

func (n *node) try(method string, params any) reply {
	n.t.Helper()
	b, _ := json.Marshal(params)
	if params == nil {
		b = []byte("{}")
	}
	return decode(n.t, n.c.Call(method, string(b)))
}

func (n *node) errorCode(method string, params any) string {
	n.t.Helper()
	r := n.try(method, params)
	if r.OK {
		n.t.Fatalf("%s.%s unexpectedly succeeded", n.name, method)
	}
	return r.Error.Code
}

func (n *node) find(typ string, match map[string]any) map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, ev := range n.events {
		if ev["type"] != typ {
			continue
		}
		ok := true
		for k, v := range match {
			if ev[k] != v {
				ok = false
			}
		}
		if ok {
			return ev
		}
	}
	return nil
}

func (n *node) waitEvent(typ string, match map[string]any) map[string]any {
	n.t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if ev := n.find(typ, match); ev != nil {
			return ev
		}
		time.Sleep(5 * time.Millisecond)
	}
	n.t.Fatalf("%s: no %s event %v", n.name, typ, match)
	return nil
}

func randomHex() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}
