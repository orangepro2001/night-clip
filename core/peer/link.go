package peer

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"nightclip/core/proto"
	"nightclip/core/store"
)

// link is everything we know about the connection to one paired peer: its
// state, the current session, and the receive-side sequencing (cursor plus a
// small out-of-order buffer). Exactly one session is attached at a time.
type link struct {
	e      *Engine
	peerID string
	kick   chan struct{} // wakes the sending pump of the current session

	mu          sync.Mutex
	sess        *session
	state       string
	dialCancel  context.CancelFunc // non-nil while a dial loop runs
	dialGen     uint64
	revokedByUs bool

	recvMu       sync.Mutex
	cursor       int64
	cursorLoaded bool
	buf          map[int64]*pending
}

// pending is one received event waiting for its turn in the sequence.
type pending struct {
	env    *proto.Envelope
	apply  bool   // store it and hand it to the application
	result string // ack result when !apply
	reason string
}

func (e *Engine) getLink(peerID string) *link {
	e.mu.Lock()
	defer e.mu.Unlock()
	l, ok := e.links[peerID]
	if !ok {
		l = &link{e: e, peerID: peerID, kick: make(chan struct{}, 1), state: StateOffline, buf: map[int64]*pending{}}
		e.links[peerID] = l
	}
	return l
}

func (l *link) wake() {
	select {
	case l.kick <- struct{}{}:
	default:
	}
}

func (l *link) setState(state string) {
	l.mu.Lock()
	changed := l.state != state
	l.state = state
	l.mu.Unlock()
	if changed {
		l.e.emit(map[string]any{"type": "peer.state", "device_id": l.peerID, "state": state})
		if h := l.e.cfg.Handler; h != nil {
			h.OnPeerState(l.peerID, state)
		}
	}
}

func (l *link) currentState() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

// attach makes s the live session, replacing (and closing) any earlier one:
// only one WebSocket per peer at a time (docs/api-spec.md §4.3).
func (l *link) attach(s *session) {
	l.mu.Lock()
	old := l.sess
	l.sess = s
	l.mu.Unlock()
	if old != nil {
		old.goaway("replaced")
	}
	l.recvMu.Lock()
	l.cursorLoaded = false // re-read from the database; the buffer belongs to the old connection
	l.buf = map[int64]*pending{}
	l.recvMu.Unlock()
	l.setState(StateConnected)
	l.wake()
}

// detach is called when s ended. The link only goes offline if s was still the
// live session (a replaced session must not clobber its successor).
func (l *link) detach(s *session) {
	l.mu.Lock()
	live := l.sess == s
	if live {
		l.sess = nil
	}
	revoked := l.revokedByUs
	l.mu.Unlock()
	if live && !revoked {
		l.setState(StateOffline)
	}
}

func (l *link) session() *session {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sess
}

// closeSession ends the live session politely (best effort).
func (l *link) closeSession(reason string, _ int) {
	if s := l.session(); s != nil {
		s.goaway(reason)
	}
}

// loadCursorLocked reads the persisted cursor once per session. recvMu held.
func (l *link) loadCursorLocked() {
	if l.cursorLoaded {
		return
	}
	if lk, _, err := l.e.db.GetLink(l.peerID); err == nil {
		l.cursor = lk.RecvCursor
	}
	l.cursorLoaded = true
}

// recvCursor returns the highest sequence received in order.
func (l *link) recvCursor() int64 {
	l.recvMu.Lock()
	defer l.recvMu.Unlock()
	l.loadCursorLocked()
	return l.cursor
}

var errStoreFailed = errors.New("could not store a received event")

// sequence puts one received event into order. The in-order events it releases
// are stored, acknowledged and handed to the application.
func (l *link) sequence(s *session, it *pending) error {
	seq := int64(it.env.Sequence)

	l.recvMu.Lock()
	l.loadCursorLocked()
	var ready []*pending
	switch {
	case seq <= l.cursor:
		l.recvMu.Unlock()
		s.ack(it.env.EventID, ResultIgnored, "sequence_reused")
		return nil
	case seq == l.cursor+1:
		ready = append(ready, it)
		l.cursor = seq
		ready = l.drainLocked(ready)
	default:
		if _, dup := l.buf[seq]; dup {
			l.recvMu.Unlock()
			s.ack(it.env.EventID, ResultIgnored, ReasonDuplicate)
			return nil
		}
		if len(l.buf) >= maxReorder {
			after := l.cursor
			l.recvMu.Unlock()
			s.write(resyncFrame{Type: frameResync, After: after})
			return nil
		}
		l.buf[seq] = it
		l.recvMu.Unlock()
		return nil
	}
	delivered, err := l.persistLocked(s, ready)
	l.recvMu.Unlock()
	l.deliver(delivered)
	return err
}

// ReasonDuplicate is the ack reason for an event we already hold.
const ReasonDuplicate = "duplicate"

// drainLocked appends buffered events that became contiguous. recvMu held.
func (l *link) drainLocked(ready []*pending) []*pending {
	for {
		nxt, ok := l.buf[l.cursor+1]
		if !ok {
			return ready
		}
		delete(l.buf, l.cursor+1)
		l.cursor++
		ready = append(ready, nxt)
	}
}

// skipThrough applies a skip frame: nothing <= through will arrive any more.
func (l *link) skipThrough(s *session, through int64) error {
	l.recvMu.Lock()
	l.loadCursorLocked()
	if through <= l.cursor {
		l.recvMu.Unlock()
		return nil
	}
	// Anything buffered below the new cursor is still delivered, in order.
	var ready []*pending
	keys := make([]int64, 0, len(l.buf))
	for k := range l.buf {
		if k <= through {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		ready = append(ready, l.buf[k])
		delete(l.buf, k)
	}
	l.cursor = through
	ready = l.drainLocked(ready)
	delivered, err := l.persistLocked(s, ready)
	if err == nil {
		err = l.e.db.SetRecvCursor(l.peerID, l.cursor)
	}
	l.recvMu.Unlock()
	l.deliver(delivered)
	return err
}

// persistLocked stores the released events (inbox first, cursor second, so a
// crash can only cause a harmless re-delivery) and acknowledges them.
func (l *link) persistLocked(s *session, ready []*pending) ([]*proto.Envelope, error) {
	var delivered []*proto.Envelope
	for _, it := range ready {
		env := it.env
		if !it.apply {
			s.ack(env.EventID, it.result, it.reason)
			continue
		}
		row := store.InboxRow{
			EventID: env.EventID, PeerID: l.peerID, Sequence: int64(env.Sequence), Kind: string(env.Kind),
			Payload: []byte(env.Payload), ReceivedAt: l.e.now().Unix(), ExpiresAt: env.ExpiresAt.Unix(),
			Sensitive: env.Sensitive,
		}
		inserted, err := l.e.db.InsertInbox(row)
		switch {
		case err != nil:
			// The database refused the row. Forget the in-memory cursor and recycle the
			// connection: the sender will resend from the persisted cursor.
			l.cursorLoaded = false
			return delivered, errStoreFailed
		case !inserted:
			s.ack(env.EventID, ResultIgnored, ReasonDuplicate)
		default:
			s.ack(env.EventID, ResultReceived, "")
			delivered = append(delivered, env)
		}
	}
	if len(delivered) > 0 {
		// Whatever the kind, the received-events table stays bounded; the clipboard history
		// limit (smaller) is applied by the clip service on top of this.
		_, _ = l.e.db.PruneInbox(maxInboxRows)
	}
	if len(ready) > 0 {
		if err := l.e.db.SetRecvCursor(l.peerID, l.cursor); err != nil {
			return delivered, errStoreFailed
		}
	}
	return delivered, nil
}

// maxInboxRows is the hard cap on stored received events of all kinds.
var maxInboxRows = 2000 // a var only so tests can lower it

// deliver hands stored events to the application, in order, outside recvMu.
func (l *link) deliver(envs []*proto.Envelope) {
	for _, env := range envs {
		if env.Kind == proto.KindControl && l.e.handleControl(l, env) {
			continue
		}
		if h := l.e.cfg.Handler; h != nil {
			h.OnEvent(l.peerID, env)
		}
	}
}

// backoff returns the n-th (0-based) reconnect delay: base, 2x, 4x ... capped.
func (e *Engine) backoff(n int) time.Duration {
	d := e.timing.BackoffBase << min(n, 20)
	return min(d, e.timing.BackoffMax)
}
