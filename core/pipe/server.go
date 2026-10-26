// Package pipe is the local control channel of a running core: JSON Lines over
// a Windows named pipe (\\.\pipe\clipbridge.v1, current user only) or, for
// development and tests on other systems, a Unix domain socket (mode 0600).
// docs/api-spec.md §8.
package pipe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// MaxLine bounds one request line.
const MaxLine = 1 << 20

// Allowed lists the methods reachable over the pipe. Pairing, update settings and
// anything that changes who is trusted stay inside the GUI, where the user is
// asked: another process of the same user must not be able to pair a device.
var Allowed = map[string]bool{
	"version": true, "status": true, "identity.get": true, "diagnostics.get": true,
	"peers.list": true, "clip.send": true, "inbox.list": true, "inbox.get": true,
	"transfers.list": true, "file.offer": true, "file.accept": true, "file.reject": true, "file.cancel": true,
	"settings.get": true, "pair.pending": true,
}

// Server answers requests with Call (typically facade.Core.Call).
type Server struct {
	Call func(method, paramsJSON string) string

	mu    sync.Mutex
	conns map[io.Closer]struct{}
	wg    sync.WaitGroup
}

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// ServeConn reads requests from c until it closes. Each line is one request; each
// gets exactly one reply line carrying the same "id". A bad line gets an error
// reply and the connection stays usable.
func (s *Server) ServeConn(c io.ReadWriteCloser) {
	s.track(c, true)
	defer s.track(c, false)
	defer c.Close()

	r := bufio.NewReaderSize(c, 64<<10)
	for {
		line, err := readLine(r)
		if err != nil {
			if errors.Is(err, errLineTooLong) {
				_ = writeReply(c, nil, errorBody("INVALID", "request line is too long"))
			}
			return
		}
		if len(line) == 0 {
			continue
		}
		var req request
		if json.Unmarshal(line, &req) != nil || req.Method == "" {
			if writeReply(c, nil, errorBody("INVALID", "each line must be a JSON object with a method")) != nil {
				return
			}
			continue
		}
		if !Allowed[req.Method] {
			if writeReply(c, req.ID, errorBody("METHOD_NOT_FOUND", "this method is not available over the local pipe")) != nil {
				return
			}
			continue
		}
		params := "{}"
		if len(req.Params) > 0 && string(req.Params) != "null" {
			params = string(req.Params)
		}
		if writeReply(c, req.ID, json.RawMessage(s.Call(req.Method, params))) != nil {
			return
		}
	}
}

// Close drops every open connection and waits for their handlers.
func (s *Server) Close() {
	s.mu.Lock()
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Server) track(c io.Closer, add bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conns == nil {
		s.conns = map[io.Closer]struct{}{}
	}
	if add {
		s.conns[c] = struct{}{}
		s.wg.Add(1)
		return
	}
	delete(s.conns, c)
	s.wg.Done()
}

var errLineTooLong = errors.New("line too long")

// readLine reads one line without ever buffering more than MaxLine bytes.
func readLine(r *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		out = append(out, chunk...)
		if len(out) > MaxLine {
			return nil, errLineTooLong
		}
		if !isPrefix {
			return out, nil
		}
	}
}

func errorBody(code, msg string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"ok": false, "error": map[string]any{"code": code, "message": msg, "retryable": false}})
	return b
}

// writeReply splices the request id into a Core.Call reply and writes one line.
func writeReply(w io.Writer, id json.RawMessage, reply json.RawMessage) error {
	var m map[string]json.RawMessage
	if json.Unmarshal(reply, &m) != nil {
		m = map[string]json.RawMessage{"ok": []byte("false"), "error": []byte(`{"code":"INTERNAL","message":"bad reply"}`)}
	}
	if len(id) == 0 {
		id = []byte("null")
	}
	m["id"] = id
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// Listener accepts connections until Close.
type Listener interface {
	Accept() (io.ReadWriteCloser, error)
	Close() error
}

// Serve accepts on l and serves every connection in its own goroutine until ctx
// is cancelled or l fails.
func (s *Server) Serve(ctx context.Context, l Listener) error {
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.ServeConn(c)
	}
}
