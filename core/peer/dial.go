package peer

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"nightclip/core/proto"
	"nightclip/core/store"
)

var errNotPaired = errors.New("the peer does not know this device any more")

// pairingGrace is how long after pairing a NOT_PAIRED answer is treated as "the
// other side has not finished saving us yet" and retried, instead of as "we were
// removed". Between two Windows PCs the listener may start dialing the moment the
// user confirms, a little before the joiner has stored the new peer.
const pairingGrace = 2 * time.Minute

// shouldDial applies the direction rules of docs/architecture.md §4.1: Android
// only dials; between two listening cores the smaller device_id dials; a
// per-peer role override wins.
func (e *Engine) shouldDial(p store.Peer) bool {
	switch p.Role {
	case "dialer":
		return true
	case "listener":
		return false
	}
	if !e.cfg.Listen {
		return true
	}
	return p.Platform == "windows" && e.id.DeviceID < p.DeviceID
}

// startPeer makes the engine responsible for a paired peer: it creates the link
// and, if this side dials, starts the reconnect loop.
func (e *Engine) startPeer(p store.Peer) {
	l := e.getLink(p.DeviceID)
	if e.shouldDial(p) {
		l.startDial()
	}
}

func (l *link) startDial() {
	l.mu.Lock()
	if l.dialCancel != nil || l.e.ctx == nil || l.e.ctx.Err() != nil {
		l.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(l.e.ctx)
	l.dialCancel = cancel
	l.dialGen++
	gen := l.dialGen
	l.mu.Unlock()
	l.e.wg.Add(1)
	go l.dialLoop(ctx, gen)
}

func (l *link) stopDial() {
	l.mu.Lock()
	cancel := l.dialCancel
	l.dialCancel = nil
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// dialLoop keeps a connection to the peer alive with exponential backoff
// (1 s, 2 s, 4 s ... 60 s, jittered). A peer that answers 403 NOT_PAIRED is not
// retried: re-pairing is a user decision.
func (l *link) dialLoop(ctx context.Context, gen uint64) {
	e := l.e
	defer e.wg.Done()
	defer func() {
		l.mu.Lock()
		if l.dialGen == gen {
			l.dialCancel = nil
		}
		l.mu.Unlock()
	}()

	fails := 0
	for ctx.Err() == nil {
		p, found, err := e.db.GetPeer(l.peerID)
		if err != nil || !found || p.State != "paired" || !e.shouldDial(p) {
			return
		}
		if l.currentState() != StateConnected {
			l.setState(StateConnecting)
		}
		began := time.Now()
		s, err := e.dialOnce(ctx, l, p)
		if err == nil {
			err = s.run()
			if time.Since(began) >= e.timing.StableAfter {
				fails = 0
			}
		}
		switch {
		case ctx.Err() != nil:
			return
		case errors.Is(err, errNotPaired) && e.now().Unix()-p.PairedAt >= int64(pairingGrace.Seconds()):
			l.setState(StateUnpaired)
			return
		case errors.Is(err, ErrFingerprint):
			e.emit(map[string]any{"type": "core.warning", "code": "FINGERPRINT_MISMATCH", "device_id": l.peerID})
		}
		l.setState(StateOffline)
		wait := jitter(e.backoff(fails))
		fails++
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// jitter spreads d by up to ±20% so that devices do not reconnect in lockstep.
func jitter(d time.Duration) time.Duration {
	return d - d/5 + time.Duration(randInt63n(int64(d)*2/5+1))
}

// dialOnce opens one authenticated WebSocket to the first reachable address.
func (e *Engine) dialOnce(ctx context.Context, l *link, p store.Peer) (*session, error) {
	addrs := e.tailnetStrings(p.TailnetAddrs)
	if len(addrs) == 0 {
		return nil, proto.Errorf(proto.CodeNoTailnet, "no tailnet address known for %s", p.Name)
	}
	port := p.Port
	if port == 0 {
		port = DefaultPort
	}

	var lastErr error
	for _, a := range addrs {
		var local string
		client := &http.Client{Transport: &http.Transport{
			TLSClientConfig: ClientTLS(e.id, p.CertFP),
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				c, err := (&net.Dialer{Timeout: e.timing.DialTimeout}).DialContext(ctx, network, addr)
				if err == nil {
					if ta, ok := c.LocalAddr().(*net.TCPAddr); ok {
						local = ta.IP.String()
					}
				}
				return c, err
			},
			TLSHandshakeTimeout: e.timing.DialTimeout,
			ForceAttemptHTTP2:   false,
			TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		}}
		dctx, cancel := context.WithTimeout(ctx, e.timing.DialTimeout+e.timing.Handshake)
		conn, resp, err := websocket.Dial(dctx, "wss://"+net.JoinHostPort(a, strconv.Itoa(port))+"/v1/ws",
			&websocket.DialOptions{HTTPClient: client, Subprotocols: []string{Subprotocol}})
		cancel()
		if err != nil {
			client.CloseIdleConnections()
			if resp != nil && (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized) {
				return nil, errNotPaired
			}
			lastErr = err
			continue
		}
		s := e.newSession(l, conn, p, true)
		var mine []string
		if ap, err := netip.ParseAddr(local); err == nil && e.addrAllowed(ap) {
			mine = []string{ap.Unmap().String()}
		}
		for _, a := range e.ListenAddrs() {
			mine = append(mine, a.String())
		}
		if err := s.handshakeDialer(mine); err != nil {
			s.cancel()
			conn.CloseNow()
			lastErr = err
			continue
		}
		// The address that worked goes first next time.
		rest := make([]string, 0, len(addrs))
		for _, o := range addrs {
			if o != a {
				rest = append(rest, o)
			}
		}
		_ = e.db.TouchPeer(p.DeviceID, append([]string{a}, rest...), e.now().Unix())
		return s, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no address reachable")
	}
	if strings.Contains(lastErr.Error(), ErrFingerprint.Error()) {
		return nil, ErrFingerprint
	}
	return nil, lastErr
}

// serveWS is the listener side of /v1/ws; only paired callers get here.
func (e *Engine) serveWS(w http.ResponseWriter, r *http.Request) {
	caller := PeerFromContext(r.Context())
	if caller.Peer == nil {
		writeJSONError(w, http.StatusForbidden, proto.CodeNotPaired, "this device is not paired")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{Subprotocol}})
	if err != nil {
		return // Accept already wrote the HTTP error
	}
	if conn.Subprotocol() != Subprotocol {
		_ = conn.Close(websocket.StatusPolicyViolation, "subprotocol clipbridge.v1 required")
		return
	}
	l := e.getLink(caller.Peer.DeviceID)
	s := e.newSession(l, conn, *caller.Peer, false)
	remote, _ := remoteAddr(r)
	if err := s.handshakeListener(remote); err != nil {
		s.cancel()
		_ = conn.Close(websocket.StatusPolicyViolation, "bad hello")
		conn.CloseNow()
		return
	}
	_ = s.run()
}
