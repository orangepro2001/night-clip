package sshagenthost

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"moonpassword/core/pipe"
	"moonpassword/core/sshagent"
	"moonpassword/core/sshkey"
)

const (
	ownerWait  = 30 * time.Second
	updateWait = 2 * time.Minute
)

// Host owns the public SSH socket and signing keys. Loss of its owner closes it,
// except for the bounded handover explicitly requested by the updater.
type Host struct {
	ownerPID atomic.Uint32
	ring     *sshagent.Keyring
	known    *sshagent.KnownHosts
	events   chan Event
	forward  atomic.Bool

	guard                           sync.Mutex
	locked, suspended, disconnected bool
	expire                          *time.Timer

	mu             sync.Mutex
	done           chan struct{}
	owner          net.Conn
	attachUntil    time.Time
	handoverReady  bool
	handingOver    bool
	handoverExpiry time.Time
	closed         bool

	// Only the authenticated owner (one request at a time) changes the listener.
	public                         *sshagent.Server
	state, publicName, publicError string
}

func NewHost(known *sshagent.KnownHosts) *Host {
	h := &Host{ring: sshagent.NewKeyring(nil), known: known, events: make(chan Event, 128), done: make(chan struct{}), state: "stopped"}
	h.ring.OnExpire = func() { h.event(Event{Type: "expired", Reason: "grace period over"}) }
	return h
}

func (h *Host) event(e Event) {
	select {
	case h.events <- e:
	default:
	}
}

func (h *Host) Done() <-chan struct{} { return h.done }

// Session applies Windows session and power notifications even with no UI connected.
func (h *Host) Session(reason string, blocked bool) {
	h.guard.Lock()
	defer h.guard.Unlock()
	switch reason {
	case "suspend":
		h.suspended = blocked
	case "disconnect":
		h.disconnected = blocked
	default:
		h.locked = blocked
	}
	if blocked && h.ring.Clear() > 0 {
		h.event(Event{Type: "cleared", Reason: reason})
	}
}

func (h *Host) Close() {
	h.mu.Lock()
	h.closeLocked()
	h.mu.Unlock()
}

func (h *Host) closeLocked() {
	if h.closed {
		return
	}
	h.closed = true
	h.guard.Lock()
	h.locked = true
	if h.expire != nil {
		h.expire.Stop()
	}
	h.ring.Clear()
	h.guard.Unlock()
	close(h.done)
	if h.owner != nil {
		_ = h.owner.Close()
	}
}

func (h *Host) waitForOwnerLocked(wait time.Duration) {
	until := time.Now().Add(wait)
	h.attachUntil = until
	time.AfterFunc(wait, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.owner == nil && h.attachUntil.Equal(until) {
			h.closeLocked()
		}
	})
}

func (h *Host) Serve(control net.Listener, secret []byte) error {
	h.mu.Lock()
	h.waitForOwnerLocked(ownerWait)
	h.mu.Unlock()

	go func() {
		<-h.done
		_ = control.Close()
	}()
	var wg sync.WaitGroup
	defer func() {
		h.Close()
		wg.Wait()
		if h.public != nil {
			h.public.Close()
		}
	}()
	pending := make(chan struct{}, 8)
	for {
		conn, err := control.Accept()
		if err != nil {
			select {
			case <-h.done:
				return nil
			default:
				return err
			}
		}
		select {
		case pending <- struct{}{}:
			wg.Go(func() {
				defer func() { <-pending }()
				h.serveOwner(conn, secret)
			})
		default:
			_ = conn.Close()
		}
	}
}

func (h *Host) serveOwner(conn net.Conn, secret []byte) {
	defer conn.Close()
	ch, err := authenticate(conn, secret, true)
	if err != nil {
		return
	}

	h.mu.Lock()
	if h.closed || h.owner != nil {
		h.mu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(callTimeout))
		_ = ch.send(response{Busy: true})
		return
	}
	h.owner = conn
	h.attachUntil = time.Time{}
	h.handoverReady, h.handingOver = false, false
	h.handoverExpiry = time.Time{}
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.owner = nil
		if h.handingOver && !h.closed {
			h.guard.Lock()
			// The old UI's idle deadline carries over. Do not extend an existing grace period.
			_, state, _ := h.ring.Snapshot()
			if state == sshagent.StateLoaded && !h.handoverExpiry.IsZero() {
				h.grace(h.handoverExpiry)
			}
			h.guard.Unlock()
			h.waitForOwnerLocked(updateWait)
		} else {
			h.closeLocked()
		}
	}()

	if err := h.reply(ch, response{Status: h.status()}); err != nil {
		return
	}
	for {
		var req request
		if err := ch.read(&req); err != nil {
			return
		}
		out := h.handle(&req)
		for _, k := range req.Keys {
			clear(k.PEM)
		}
		if err := h.reply(ch, out); err != nil {
			return
		}
	}
}

func (h *Host) reply(ch *channel, res response) error {
	_ = ch.SetWriteDeadline(time.Now().Add(callTimeout))
	err := ch.send(res)
	_ = ch.SetWriteDeadline(time.Time{})
	return err
}

func (h *Host) status() Status {
	keys, st, expires := h.ring.Snapshot()
	out := Status{State: h.state, Pipe: h.publicName, Error: h.publicError, KeysState: st, Expires: expires, Generation: h.ring.Gen()}
	if h.public != nil {
		out.Connections = h.public.Connections()
	}
	for _, k := range keys {
		out.Keys = append(out.Keys, KeyInfo{k.Public.Marshal(), k.EntryID, k.Title, k.Comment, k.Hosts})
	}
	return out
}

func (h *Host) handle(req *request) response {
	var out response
	switch req.Op {
	case "configure":
		h.ownerPID.Store(req.OwnerPID)
		h.configure(req.Pipe, req.Forward)
	case "poll":
		for len(h.events) > 0 {
			out.Events = append(out.Events, <-h.events)
		}
	case "status":
	case "replace":
		out.Changed, out.Error = h.replace(req.Generation, req.Keys)
	case "clear":
		out.Cleared = h.ring.Clear()
	case "grace":
		h.guard.Lock()
		out.Changed = h.grace(req.Until)
		h.guard.Unlock()
	case "handover":
		h.mu.Lock()
		h.handoverReady = !h.closed && h.public != nil
		h.handoverExpiry = req.Until
		out.Changed = h.handoverReady
		h.mu.Unlock()
	case "commit_handover":
		h.mu.Lock()
		h.handingOver = h.handoverReady && !h.closed
		out.Changed = h.handingOver
		h.mu.Unlock()
	case "cancel_handover":
		h.mu.Lock()
		h.handoverReady, h.handingOver = false, false
		h.mu.Unlock()
	default:
		out.Error = "unsupported SSH Agent control request"
	}
	out.Status = h.status()
	return out
}

func (h *Host) configure(name string, forwarding bool) {
	h.forward.Store(forwarding)
	if h.public != nil && h.publicName == name {
		return
	}
	if h.public != nil {
		h.public.Close()
		h.public = nil
	}
	h.publicName, h.publicError = name, ""
	l, err := pipe.Listen(name)
	if err != nil {
		h.state, h.publicError = "error", err.Error()
		if PipeTaken(err) {
			h.state = "conflict"
		}
		return
	}
	h.state = "listening"
	h.public = sshagent.Serve(l, h.ring, sshagent.Hooks{
		AllowForwarding: h.forward.Load,
		KnownHosts:      h.known,
		Client: func(c io.ReadWriteCloser) []sshagent.Proc {
			pid, ok := pipe.ClientPID(c)
			if !ok {
				return nil
			}
			return sshagent.ClientChain(pid)
		},
		OnKeysNeeded: func(c []sshagent.Proc) {
			if len(c) == 0 || c[0].PID != h.ownerPID.Load() {
				h.event(Event{Type: "needed", Client: c})
			}
		},
		OnUse: func(u sshagent.Use) {
			var blob []byte
			if u.HostKey != nil {
				blob = u.HostKey.Marshal()
				u.HostKey = nil
			}
			h.event(Event{Type: "use", Use: u, HostKey: blob})
		},
	})
}

func PipeTaken(err error) bool {
	var errno syscall.Errno
	return errors.Is(err, os.ErrExist) || errors.As(err, &errno) && (errno == 5 || errno == 231)
}

func (h *Host) replace(gen uint64, inputs []KeyInput) (bool, string) {
	keys := make([]*sshagent.Key, 0, len(inputs))
	for _, item := range inputs {
		k, err := sshkey.Parse(item.PEM, nil)
		if err != nil {
			sshagent.Wipe(keys)
			return false, "could not load a key into the SSH Agent"
		}
		key, err := sshagent.NewKey(k.Signer, item.EntryID, item.Title, k.Comment)
		if err != nil {
			sshagent.Wipe(keys)
			return false, "could not load a key into the SSH Agent"
		}
		key.Hosts = item.Hosts
		keys = append(keys, key)
	}
	h.guard.Lock()
	defer h.guard.Unlock()
	if h.locked || h.suspended || h.disconnected {
		sshagent.Wipe(keys)
		return false, ""
	}
	changed := h.ring.Replace(gen, keys)
	if changed && h.expire != nil {
		h.expire.Stop()
	}
	return changed, ""
}

// grace is called with guard held.
func (h *Host) grace(until time.Time) bool {
	if !until.After(time.Now()) {
		h.ring.Clear()
		return false
	}
	if !h.ring.Grace(until) {
		return false
	}
	if h.expire != nil {
		h.expire.Stop()
	}
	h.expire = time.AfterFunc(time.Until(until)+time.Millisecond, func() { h.ring.Snapshot() })
	return true
}
