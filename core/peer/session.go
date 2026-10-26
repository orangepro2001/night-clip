package peer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"nightclip/core/proto"
	"nightclip/core/store"
)

var (
	errIdle    = errors.New("no frames received for too long")
	errGoaway  = errors.New("peer said goodbye")
	errHello   = errors.New("bad hello")
	errReplace = errors.New("replaced by a newer connection")
)

// session is one live WebSocket to one peer, on either side of the dial.
type session struct {
	e      *Engine
	l      *link
	conn   *websocket.Conn
	peer   store.Peer
	dialer bool
	ctx    context.Context
	cancel context.CancelFunc

	lastRecv atomic.Int64 // unix nanoseconds of the last received frame
	started  time.Time

	sendMu      sync.Mutex // guards sentThrough and serialises flushes
	sentThrough int64      // highest sequence already sent (or skipped) on this connection
}

func (e *Engine) newSession(l *link, conn *websocket.Conn, p store.Peer, dialer bool) *session {
	ctx, cancel := context.WithCancel(e.ctx)
	s := &session{e: e, l: l, conn: conn, peer: p, dialer: dialer, ctx: ctx, cancel: cancel, started: time.Now()}
	conn.SetReadLimit(MaxFrameBytes)
	s.lastRecv.Store(time.Now().UnixNano())
	return s
}

// ---- handshake ----------------------------------------------------------------

// handshakeDialer sends hello and waits for hello_ack.
func (s *session) handshakeDialer(localAddrs []string) error {
	ctx, cancel := context.WithTimeout(s.ctx, s.e.timing.Handshake)
	defer cancel()
	if err := s.writeCtx(ctx, helloFrame{
		Type: frameHello, DeviceID: s.e.id.DeviceID, Name: s.e.id.Name,
		TailnetAddrs: localAddrs, RecvCursor: s.l.recvCursor(),
	}); err != nil {
		return err
	}
	raw, err := s.readCtx(ctx)
	if err != nil {
		return err
	}
	var ack helloAckFrame
	if json.Unmarshal(raw, &ack) != nil || ack.Type != frameHelloAck || ack.DeviceID != s.peer.DeviceID {
		return errHello
	}
	s.sentThrough = ack.RecvCursor
	return nil
}

// handshakeListener reads hello and answers hello_ack.
func (s *session) handshakeListener(remote netip.Addr) error {
	ctx, cancel := context.WithTimeout(s.ctx, s.e.timing.Handshake)
	defer cancel()
	raw, err := s.readCtx(ctx)
	if err != nil {
		return err
	}
	var h helloFrame
	// The authenticated certificate decides who this is; a hello that claims another
	// identity is a protocol violation, not a way to impersonate.
	if json.Unmarshal(raw, &h) != nil || h.Type != frameHello || h.DeviceID != s.peer.DeviceID {
		return errHello
	}
	addrs := s.e.tailnetStrings(append([]string{remote.String()}, h.TailnetAddrs...))
	_ = s.e.db.TouchPeer(s.peer.DeviceID, addrs, s.e.now().Unix())
	s.sentThrough = h.RecvCursor
	return s.writeCtx(ctx, helloAckFrame{
		Type: frameHelloAck, DeviceID: s.e.id.DeviceID, ServerTime: s.e.now().UTC().Format(time.RFC3339),
		RecvCursor: s.l.recvCursor(), MaxFrameBytes: MaxFrameBytes,
	})
}

// ---- I/O helpers ----------------------------------------------------------------

func (s *session) writeCtx(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.conn.Write(ctx, websocket.MessageText, b)
}

// write sends one frame; a failure ends the session (the reader notices too).
func (s *session) write(v any) {
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()
	if err := s.writeCtx(ctx, v); err != nil {
		s.cancel()
	}
}

func (s *session) readCtx(ctx context.Context) ([]byte, error) {
	_, data, err := s.conn.Read(ctx)
	if err == nil {
		s.lastRecv.Store(time.Now().UnixNano())
	}
	return data, err
}

// goaway tells the peer we are leaving and tears the session down.
func (s *session) goaway(reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = s.writeCtx(ctx, goawayFrame{Type: frameGoaway, Reason: reason, ReconnectAfterM: 1000})
	cancel()
	s.cancel()
}

// ---- main loop ----------------------------------------------------------------

// run attaches the session and serves it until it ends. It always closes the
// connection.
func (s *session) run() error {
	defer func() {
		s.cancel()
		_ = s.conn.Close(websocket.StatusNormalClosure, "")
		s.conn.CloseNow()
		s.l.detach(s)
	}()
	s.l.attach(s)

	readErr := make(chan error, 1)
	go func() { readErr <- s.readLoop() }()

	ping := time.NewTicker(s.e.timing.Ping)
	defer ping.Stop()
	idle := time.NewTicker(max(s.e.timing.Idle/4, 10*time.Millisecond))
	defer idle.Stop()

	s.flush()
	for {
		select {
		case err := <-readErr:
			return err
		case <-s.ctx.Done():
			select { // prefer the reader's reason (goaway / error) when it is already known
			case err := <-readErr:
				return err
			case <-time.After(20 * time.Millisecond):
			}
			return s.ctx.Err()
		case <-s.l.kick:
			s.flush()
		case <-ping.C:
			s.write(pingFrame{Type: framePing, TS: s.e.now().UTC().Format(time.RFC3339)})
		case <-idle.C:
			if time.Since(time.Unix(0, s.lastRecv.Load())) > s.e.timing.Idle {
				return errIdle
			}
		}
	}
}

func (s *session) readLoop() error {
	for {
		raw, err := s.readCtx(s.ctx)
		if err != nil {
			return err
		}
		if err := s.handleFrame(raw); err != nil {
			return err
		}
	}
}

func (s *session) handleFrame(raw []byte) error {
	var head frameHead
	if json.Unmarshal(raw, &head) != nil {
		s.sendError(string(proto.CodeInvalid), "malformed frame")
		return nil
	}
	switch head.Type {
	case framePing:
		s.write(pingFrame{Type: framePong, TS: s.e.now().UTC().Format(time.RFC3339)})
	case framePong:
	case frameEvent:
		var f eventFrame
		if json.Unmarshal(raw, &f) == nil {
			return s.onEvent(f.Envelope)
		}
	case frameAck:
		var f ackFrame
		if json.Unmarshal(raw, &f) == nil {
			s.onAck(f)
		}
	case frameSkip:
		var f skipFrame
		if json.Unmarshal(raw, &f) == nil && f.Through > 0 {
			return s.l.skipThrough(s, f.Through)
		}
	case frameResync:
		var f resyncFrame
		if json.Unmarshal(raw, &f) == nil && f.After >= 0 {
			s.resync(f.After)
		}
	case frameGoaway:
		return errGoaway
	case frameError:
		var f errorFrame
		if json.Unmarshal(raw, &f) == nil {
			s.e.emit(map[string]any{"type": "peer.error", "device_id": s.peer.DeviceID, "code": f.Code})
		}
	case frameHello, frameHelloAck:
		// only meaningful during the handshake
	default:
		// unknown frame types are ignored so the protocol can grow
	}
	return nil
}

func (s *session) sendError(code, msg string) {
	s.write(errorFrame{Type: frameError, Code: code, Message: msg})
}

// ---- sending ----------------------------------------------------------------

// flush sends every queued event the peer has not seen, in sequence order. A
// gap in the sequence (events that expired or were purged) is announced with a
// skip frame first, so the receiver never waits for an event that will not come.
func (s *session) flush() {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	scan := s.sentThrough
	for s.ctx.Err() == nil {
		rows, err := s.e.db.ListOutbox(s.peer.DeviceID, scan, 100)
		if err != nil || len(rows) == 0 {
			return
		}
		now := s.e.now().Unix()
		for _, r := range rows {
			scan = r.Sequence
			if r.State == "expired" || r.ExpiresAt <= now {
				continue // never sent late; the next event carries the skip
			}
			if r.Sequence > s.sentThrough+1 {
				s.write(skipFrame{Type: frameSkip, Through: r.Sequence - 1})
				s.sentThrough = r.Sequence - 1
			}
			s.write(eventFrame{Type: frameEvent, Envelope: json.RawMessage(r.Envelope)})
			if s.ctx.Err() != nil {
				return
			}
			_ = s.e.db.MarkOutboxSent(r.EventID)
			s.sentThrough = r.Sequence
		}
	}
}

// resync rewinds the send position after the peer asked for a replay.
func (s *session) resync(after int64) {
	s.sendMu.Lock()
	if after < s.sentThrough {
		s.sentThrough = after
	}
	s.sendMu.Unlock()
	s.l.wake()
}

// ---- receiving ----------------------------------------------------------------

func (s *session) ack(eventID, result, reason string) {
	f := ackFrame{Type: frameAck, EventID: eventID, Result: result, AckedAt: s.e.now().UTC().Format(time.RFC3339)}
	if reason != "" {
		f.Reason = &reason
	}
	s.write(f)
}

// onEvent implements docs/architecture.md §4.3, in that order.
func (s *session) onEvent(raw json.RawMessage) error {
	var env proto.Envelope
	if json.Unmarshal(raw, &env) != nil {
		s.sendError(string(proto.CodeInvalid), "malformed envelope")
		return nil
	}
	// 1. who: the certificate decides, the envelope must agree
	if env.SourceDeviceID != s.peer.DeviceID {
		s.sendError(string(proto.CodeSourceMismatch), "source_device_id does not match the connection")
		return nil
	}
	// 2. for us
	if env.TargetDeviceID != s.e.id.DeviceID {
		s.ack(env.EventID, ResultIgnored, "target_mismatch")
		return nil
	}
	// 3. already known: answer again, change nothing (acks are idempotent)
	if row, found, err := s.e.db.GetInbox(env.EventID); err == nil && found {
		result, reason := ResultReceived, ""
		switch row.State {
		case "applied":
			result = ResultApplied
		case "ignored":
			result, reason = ResultIgnored, ReasonDuplicate
		case "expired":
			result = ResultExpired
		case "error":
			result = ResultError
		}
		s.ack(env.EventID, result, reason)
		return nil
	}
	// 4. valid: version, ids, expiry, content_hash, payload shape
	it := &pending{env: &env, apply: true}
	if err := env.Validate(s.e.now()); err != nil {
		var pe *proto.Error
		it.apply = false
		if errors.As(err, &pe) && pe.Code == proto.CodeExpired {
			it.result = ResultExpired
		} else {
			it.result, it.reason = ResultError, string(pe.Code)
		}
		if env.Sequence < 1 {
			s.ack(env.EventID, it.result, it.reason)
			return nil
		}
	}
	// 5. order, store, ack, deliver
	if err := s.l.sequence(s, it); err != nil {
		return err
	}
	return nil
}

func (s *session) onAck(f ackFrame) {
	row, found, err := s.e.db.GetOutbox(f.EventID)
	if err != nil || !found || row.PeerID != s.peer.DeviceID {
		return // not ours: ignore silently
	}
	reason := ""
	if f.Reason != nil {
		reason = *f.Reason
	}
	_ = s.e.db.MarkOutboxAcked(f.EventID, f.Result, s.e.now().Unix())
	if h := s.e.cfg.Handler; h != nil {
		h.OnAck(s.peer.DeviceID, f.EventID, f.Result, reason)
	}
}

// remoteAddr extracts the peer's IP from an http.Request.
func remoteAddr(r *http.Request) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	return ap.Addr().Unmap(), true
}
