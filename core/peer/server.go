package peer

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"nightclip/core/identity"
	"nightclip/core/proto"
	"nightclip/core/store"
)

type route struct {
	pattern string
	handler http.Handler
	public  bool // reachable by an unpaired (but TLS-authenticated) certificate
}

// Handle registers an HTTP handler on the mTLS server. Private routes (public
// == false) answer 403 NOT_PAIRED unless the client certificate belongs to a
// paired peer; public routes are for the pairing handshake only. Call it before
// Start. A handler can read the caller with PeerFromContext.
func (e *Engine) Handle(pattern string, h http.Handler, public bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.routes = append(e.routes, route{pattern, h, public})
}

type ctxKey int

const callerKey ctxKey = 1

// Caller is who is on the other end of an authenticated connection.
type Caller struct {
	Fingerprint string
	Peer        *store.Peer // nil when the certificate is not (or no longer) paired
}

// PeerFromContext returns the caller of a request served by the engine.
func PeerFromContext(ctx context.Context) Caller {
	c, _ := ctx.Value(callerKey).(Caller)
	return c
}

type listener struct {
	addr netip.Addr
	ln   net.Listener
	srv  *http.Server
}

func (l *listener) close() { _ = l.srv.Close() }

// ListenPort returns the port of the first active listener (0 if none). Tests
// that bind port 0 use it to learn where to dial.
func (e *Engine) ListenPort() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, l := range e.listeners {
		if ta, ok := l.ln.Addr().(*net.TCPAddr); ok {
			return ta.Port
		}
	}
	return 0
}

// ListenError returns why binding a tailnet address last failed, or nil when
// every wanted address is being listened on.
func (e *Engine) ListenError() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.listenErr
}

// ListenAddrs returns the addresses currently being listened on.
func (e *Engine) ListenAddrs() []netip.Addr {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]netip.Addr, 0, len(e.listeners))
	for a := range e.listeners {
		out = append(out, a)
	}
	return out
}

func (e *Engine) wantedAddrs() ([]netip.Addr, error) {
	if e.hooks.ListenAddrs != nil {
		return e.hooks.ListenAddrs(), nil
	}
	addrs, err := tailnetAddrs()
	if err != nil {
		err = fmt.Errorf("list network addresses: %w", err)
	}
	return addrs, err
}

// listenLoop keeps one TLS listener per tailnet address, rebinding when
// Tailscale brings an interface up or takes it down. It never binds 0.0.0.0.
func (e *Engine) listenLoop() {
	defer e.wg.Done()
	t := time.NewTicker(e.timing.Rebind)
	defer t.Stop()
	for {
		e.reconcileListeners()
		select {
		case <-e.ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (e *Engine) reconcileListeners() {
	wanted, enumErr := e.wantedAddrs()
	want := map[netip.Addr]bool{}
	for _, a := range wanted {
		want[a.Unmap()] = true
	}

	e.mu.Lock()
	var drop []*listener
	for a, l := range e.listeners {
		if !want[a] {
			drop = append(drop, l)
			delete(e.listeners, a)
		}
	}
	var add []netip.Addr
	for a := range want {
		if _, ok := e.listeners[a]; !ok {
			add = append(add, a)
		}
	}
	e.mu.Unlock()

	for _, l := range drop {
		l.close()
	}
	bindErr := enumErr
	for _, a := range add {
		l, err := e.listenOn(a)
		if err != nil {
			bindErr = err
			e.emit(map[string]any{"type": "core.warning", "code": string(proto.CodeListenFailed), "addr": a.String(),
				"port": e.cfg.Port, "error": err.Error()})
			continue
		}
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			l.close()
			return
		}
		e.listeners[a] = l
		e.mu.Unlock()
	}

	e.mu.Lock()
	// NO_TAILNET only when there is no tailnet address at all; an address we
	// cannot bind is LISTEN_FAILED (port taken, or reserved by Windows).
	none := len(e.listeners) == 0 && len(want) == 0
	changed := none != e.noTailnet
	e.noTailnet = none
	if bindErr != nil || len(e.listeners) == len(want) {
		e.listenErr = bindErr
	}
	e.mu.Unlock()
	if changed && none {
		e.emit(map[string]any{"type": "core.warning", "code": string(proto.CodeNoTailnet)})
	}
	if len(drop) > 0 || len(add) > 0 {
		addrs := []string{}
		for _, a := range e.ListenAddrs() {
			addrs = append(addrs, a.String())
		}
		e.emit(map[string]any{"type": "core.listening", "addrs": addrs, "port": e.ListenPort()})
	}
}

func (e *Engine) listenOn(a netip.Addr) (*listener, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(a.String(), strconv.Itoa(e.cfg.Port)))
	if err != nil {
		return nil, err
	}
	srv := &http.Server{
		Handler:           e.httpHandler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          log.New(io.Discard, "", 0), // handshake noise from strangers is not worth logging
	}
	l := &listener{addr: a, ln: ln, srv: srv}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		_ = srv.Serve(tls.NewListener(limitListener(ln, maxConns), e.tlsCfg))
	}()
	return l, nil
}

// maxConns bounds simultaneous TCP connections per listening address: every legitimate
// peer needs a handful, while anyone on the tailnet can open sockets without a certificate.
const maxConns = 256

// limitListener makes Accept wait while maxConns connections are already open.
func limitListener(ln net.Listener, n int) net.Listener {
	return &limited{Listener: ln, sem: make(chan struct{}, n)}
}

type limited struct {
	net.Listener
	sem chan struct{}
}

func (l *limited) Accept() (net.Conn, error) {
	l.sem <- struct{}{}
	c, err := l.Listener.Accept()
	if err != nil {
		<-l.sem
		return nil, err
	}
	return &limitedConn{Conn: c, release: func() { <-l.sem }}, nil
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error {
	c.once.Do(c.release)
	return c.Conn.Close()
}

// httpHandler builds the route table: the built-in routes plus whatever was
// registered with Handle, all behind the source-address and fingerprint checks.
func (e *Engine) httpHandler() http.Handler {
	mux := http.NewServeMux()
	e.mu.Lock()
	routes := append([]route(nil), e.routes...)
	e.mu.Unlock()
	routes = append(routes,
		route{"GET /v1/ws", http.HandlerFunc(e.serveWS), false},
		route{"GET /healthz", http.HandlerFunc(e.serveHealth), false},
	)
	for _, rt := range routes {
		rt := rt
		mux.Handle(rt.pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller, ok := e.authenticate(w, r)
			if !ok {
				return
			}
			if !rt.public && caller.Peer == nil {
				writeJSONError(w, http.StatusForbidden, proto.CodeNotPaired, "this device is not paired")
				return
			}
			rt.handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), callerKey, caller)))
		}))
	}
	return mux
}

// authenticate applies the connection admission rules of docs/architecture.md §3.3:
// the source must be a tailnet address, a client certificate must be present,
// and a revoked fingerprint gets no peer identity (so only public routes are open to it).
func (e *Engine) authenticate(w http.ResponseWriter, r *http.Request) (Caller, bool) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil || !e.addrAllowed(ap.Addr()) {
		writeJSONError(w, http.StatusForbidden, proto.CodeNotPaired, "source address is not on the tailnet")
		return Caller{}, false
	}
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		writeJSONError(w, http.StatusUnauthorized, proto.CodeNotPaired, "client certificate required")
		return Caller{}, false
	}
	fp := identity.Fingerprint(r.TLS.PeerCertificates[0].Raw)
	caller := Caller{Fingerprint: fp}
	revoked, err := e.db.IsRevoked(fp)
	if err != nil {
		// A database hiccup is not "this device is not paired": the dialer would give up for good.
		writeJSONError(w, http.StatusServiceUnavailable, proto.CodeDBUnavailable, "temporarily unavailable")
		return Caller{}, false
	}
	if revoked {
		// A removed device may still knock on the public pairing routes (the user decides
		// there, and sees that it was removed before); every private route stays closed
		// because Peer stays nil.
		return caller, true
	}
	p, found, err := e.db.FindPeerByFingerprint(fp)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, proto.CodeDBUnavailable, "temporarily unavailable")
		return Caller{}, false
	}
	// The pinned fingerprint fixes the certificate; its key must also be the one the device id was
	// derived from (a record that came by introduction carries the introducer's word for the id).
	if found && p.State == "paired" && identity.DeviceID(r.TLS.PeerCertificates[0].RawSubjectPublicKeyInfo) == p.DeviceID {
		caller.Peer = &p
	}
	return caller, true
}

func (e *Engine) serveHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "device_id": e.id.DeviceID})
}

func writeJSONError(w http.ResponseWriter, status int, code proto.Code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg, "retryable": false}})
}
