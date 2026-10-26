package pipe

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// stubCall mimics facade.Core.Call.
func stubCall(method, params string) string {
	switch method {
	case "status":
		return `{"ok":true,"result":{"params":` + params + `}}`
	case "clip.send":
		return `{"ok":false,"error":{"code":"NOT_PAIRED","message":"nobody","retryable":false}}`
	case "inbox.list":
		return `{"ok":true,"result":{"blob":"` + strings.Repeat("x", 300_000) + `"}}`
	}
	return `{"ok":true,"result":{}}`
}

type client struct {
	t *testing.T
	c io.ReadWriteCloser
	r *bufio.Reader
}

func dial(t *testing.T, addr string) *client {
	t.Helper()
	c, err := Dial(addr)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return &client{t: t, c: c, r: bufio.NewReaderSize(c, 1<<20)}
}

func (c *client) line(s string) map[string]json.RawMessage {
	c.t.Helper()
	if _, err := io.WriteString(c.c, s+"\n"); err != nil {
		c.t.Fatalf("write: %v", err)
	}
	return c.read()
}

func (c *client) read() map[string]json.RawMessage {
	c.t.Helper()
	done := make(chan struct{})
	var out map[string]json.RawMessage
	var line []byte
	var err error
	go func() { line, err = c.r.ReadBytes('\n'); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		c.t.Fatal("no reply within 5 s")
	}
	if err != nil {
		c.t.Fatalf("read: %v", err)
	}
	if e := json.Unmarshal(line, &out); e != nil {
		c.t.Fatalf("bad reply %q: %v", line, e)
	}
	return out
}

func start(t *testing.T) (string, *Server) {
	t.Helper()
	addr := testAddress(t)
	l, err := Listen(addr)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	s := &Server{Call: stubCall}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after cancellation")
		}
		s.Close()
	})
	return addr, s
}

func TestRequestReplyAndIDEcho(t *testing.T) {
	addr, _ := start(t)
	c := dial(t, addr)

	r := c.line(`{"id":"abc","method":"status","params":{"x":1}}`)
	if string(r["id"]) != `"abc"` || string(r["ok"]) != "true" || !strings.Contains(string(r["result"]), `"x":1`) {
		t.Fatalf("reply = %v", r)
	}
	// ids of any JSON type come back as sent; params are optional or null
	for _, id := range []string{`7`, `"s"`, `null`, `{"a":1}`} {
		r := c.line(`{"id":` + id + `,"method":"status"}`)
		if string(r["id"]) != id || string(r["ok"]) != "true" {
			t.Fatalf("id %s -> %v", id, r)
		}
	}
	r = c.line(`{"id":1,"method":"status","params":null}`)
	if !strings.Contains(string(r["result"]), `"params":{}`) {
		t.Fatalf("null params = %v", r)
	}
	// core errors pass through untouched
	r = c.line(`{"id":2,"method":"clip.send","params":{"text":"hi"}}`)
	if string(r["ok"]) != "false" || !strings.Contains(string(r["error"]), "NOT_PAIRED") {
		t.Fatalf("error reply = %v", r)
	}
	// a large reply arrives whole
	r = c.line(`{"id":3,"method":"inbox.list"}`)
	if len(r["result"]) < 300_000 {
		t.Fatalf("large reply truncated: %d bytes", len(r["result"]))
	}
}

func TestOnlyWhitelistedMethodsAreReachable(t *testing.T) {
	addr, _ := start(t)
	c := dial(t, addr)
	for _, m := range []string{"pair.begin", "pair.join", "pair.confirm", "peers.remove", "peers.update", "update.config", "update.download",
		"shutdown", "settings.set", "clip.local_changed", "clip.mark_applied", "inbox.clear", "nonsense"} {
		r := c.line(`{"id":"` + m + `","method":"` + m + `"}`)
		if string(r["ok"]) != "false" || !strings.Contains(string(r["error"]), "METHOD_NOT_FOUND") || string(r["id"]) != `"`+m+`"` {
			t.Errorf("%s was not refused: %v", m, r)
		}
	}
	for m := range Allowed {
		r := c.line(`{"id":1,"method":"` + m + `"}`)
		if string(r["ok"]) != "true" && !strings.Contains(string(r["error"]), "NOT_PAIRED") {
			t.Errorf("%s should reach the core: %v", m, r)
		}
	}
	// the list itself: nothing that changes trust or configuration of updates
	for _, banned := range []string{"pair.begin", "pair.join", "pair.confirm", "pair.cancel", "peers.remove", "peers.update", "update.config", "shutdown"} {
		if Allowed[banned] {
			t.Errorf("%s must never be allowed over the pipe", banned)
		}
	}
}

func TestBadLinesGetAnErrorAndTheConnectionSurvives(t *testing.T) {
	addr, _ := start(t)
	c := dial(t, addr)
	for _, bad := range []string{`not json`, `[]`, `{"id":1}`, `{"id":1,"method":""}`, `"just a string"`} {
		r := c.line(bad)
		if string(r["ok"]) != "false" || !strings.Contains(string(r["error"]), "INVALID") {
			t.Errorf("%q -> %v", bad, r)
		}
	}
	if _, err := io.WriteString(c.c, "\n\n"); err != nil { // blank lines are ignored
		t.Fatal(err)
	}
	if r := c.line(`{"id":9,"method":"status"}`); string(r["ok"]) != "true" {
		t.Fatalf("connection unusable after bad lines: %v", r)
	}
}

func TestAnOversizedLineIsRefused(t *testing.T) {
	addr, _ := start(t)
	c := dial(t, addr)
	written := make(chan struct{})
	go func() {
		defer close(written)
		_, _ = io.WriteString(c.c, `{"id":1,"method":"status","params":{"p":"`+strings.Repeat("a", MaxLine+10)+`"}}`+"\n")
	}()
	if runtime.GOOS == "windows" {
		// A pipe handle is synchronous: a read cannot overlap a write that is still pending
		// on the same handle, so let the write fail (the server hangs up) before reading.
		<-written
	}
	r := c.read()
	if !strings.Contains(string(r["error"]), "too long") {
		t.Fatalf("reply = %v", r)
	}
	if _, err := c.r.ReadBytes('\n'); err == nil {
		t.Fatal("the connection stayed open after an oversized line")
	}
}

func TestManyConcurrentClients(t *testing.T) {
	addr, _ := start(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := Dial(addr)
			if err != nil {
				t.Errorf("client %d: %v", i, err)
				return
			}
			defer c.Close()
			r := bufio.NewReader(c)
			for j := 0; j < 15; j++ {
				id := fmt.Sprintf("%d-%d", i, j)
				if _, err := io.WriteString(c, `{"id":"`+id+`","method":"status"}`+"\n"); err != nil {
					t.Errorf("write: %v", err)
					return
				}
				line, err := r.ReadBytes('\n')
				if err != nil || !strings.Contains(string(line), `"id":"`+id+`"`) {
					t.Errorf("client %d request %d: %q %v", i, j, line, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestSecondServerOnTheSameAddressIsRefused(t *testing.T) {
	skipIfNoSquatProtection(t)
	addr, _ := start(t)
	if l, err := Listen(addr); err == nil {
		l.Close()
		t.Fatal("a second listener on a live address succeeded; a squatter could take over the control channel")
	}
}

func TestCloseDropsConnectionsAndFreesTheAddress(t *testing.T) {
	addr := testAddress(t)
	l, err := Listen(addr)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{Call: stubCall}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, l) }()
	c := dial(t, addr)
	if r := c.line(`{"id":1,"method":"status"}`); string(r["ok"]) != "true" {
		t.Fatal(r)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
	s.Close()
	if _, err := c.r.ReadBytes('\n'); err == nil {
		t.Fatal("the connection survived Close")
	}
	// the address can be used again right away
	l2, err := Listen(addr)
	if err != nil {
		t.Fatalf("address not released: %v", err)
	}
	l2.Close()
}
