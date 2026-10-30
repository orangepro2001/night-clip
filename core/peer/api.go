package peer

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"time"

	"nightclip/core/proto"
	"nightclip/core/store"
)

func randInt63n(n int64) int64 {
	if n <= 0 {
		return 0
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	return int64(binary.BigEndian.Uint64(b[:])>>1) % n
}

// newEventID returns a random UUIDv4.
func newEventID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// SendOptions tune one outgoing event.
type SendOptions struct {
	TTL       time.Duration // 0: by kind (clipboard 15 min, file 24 h, control 5 min)
	Sensitive bool
	EventID   string // empty: a fresh UUIDv4. Ids are unique per event: never reuse one across peers
}

func defaultTTL(kind proto.Kind) time.Duration {
	switch kind {
	case proto.KindFile:
		return 24 * time.Hour
	case proto.KindControl:
		return 5 * time.Minute
	}
	return DefaultTTL
}

// Send queues one event for peerID and returns its event_id. The event gets the
// next sequence of the link, is written to the encrypted outbox and, if the peer
// is connected, sent at once; otherwise it waits (until it expires).
func (e *Engine) Send(peerID string, kind proto.Kind, payload any, o SendOptions) (string, error) {
	p, found, err := e.db.GetPeer(peerID)
	if err != nil {
		return "", err
	}
	if !found || p.State != "paired" {
		return "", proto.Errorf(proto.CodeNotPaired, "no paired device %s", peerID)
	}
	canon, err := proto.Canonical(payload)
	if err != nil {
		return "", proto.Errorf(proto.CodeInvalid, "payload: %v", err)
	}
	raw := json.RawMessage(canon)
	hash, err := proto.ContentHash(raw)
	if err != nil {
		return "", proto.Errorf(proto.CodeInvalid, "payload: %v", err)
	}
	ttl := o.TTL
	if ttl <= 0 {
		ttl = defaultTTL(kind)
	}
	id := o.EventID
	if id == "" {
		id = newEventID()
	}
	created := e.now().UTC().Truncate(time.Second)
	expires := created.Add(ttl)

	row, err := e.db.EnqueueOutbox(peerID, func(seq int64) (store.OutboxRow, error) {
		env := proto.Envelope{
			Version: proto.Version, EventID: id, Kind: kind,
			SourceDeviceID: e.id.DeviceID, TargetDeviceID: peerID, Sequence: uint64(seq),
			CreatedAt: created, ExpiresAt: expires, ContentHash: hash, Sensitive: o.Sensitive, Payload: raw,
		}
		if err := env.Validate(created); err != nil {
			return store.OutboxRow{}, err
		}
		b, err := json.Marshal(env)
		if err != nil {
			return store.OutboxRow{}, err
		}
		return store.OutboxRow{
			EventID: id, Kind: string(kind), Envelope: b,
			CreatedAt: created.Unix(), ExpiresAt: expires.Unix(),
		}, nil
	})
	if err != nil {
		return "", err
	}
	_ = row
	e.getLink(peerID).wake()
	return id, nil
}

// MarkInbox records what the application did with a received event
// (applied / ignored / error) and tells the sender if the link is up.
func (e *Engine) MarkInbox(peerID, eventID, result, reason string) error {
	state := map[string]string{ResultApplied: "applied", ResultIgnored: "ignored", ResultError: "error", ResultExpired: "expired"}[result]
	if state == "" {
		return proto.Errorf(proto.CodeInvalid, "unknown result %q", result)
	}
	if err := e.db.SetInboxState(eventID, state, e.now().Unix()); err != nil {
		return err
	}
	if s := e.getLink(peerID).session(); s != nil {
		s.ack(eventID, result, reason)
	}
	return nil
}

// PeerStatus is the live view of one peer for status and diagnostics.
type PeerStatus struct {
	DeviceID   string   `json:"device_id"`
	Name       string   `json:"name"`
	Platform   string   `json:"platform"`
	State      string   `json:"state"`
	Addrs      []string `json:"tailnet_addrs"`
	Pending    int      `json:"outbox_pending"`
	RecvCursor int64    `json:"recv_cursor"`
	NextSeqOut int64    `json:"next_seq_out"`
	LastSeenAt int64    `json:"last_seen_at"`
	ApplyMode  string   `json:"apply_mode"`
	// SendEnabled is false when the user keeps automatic broadcasts away from this device.
	SendEnabled bool `json:"send_enabled"`
}

// PeerStatuses lists every known peer, revoked ones included.
func (e *Engine) PeerStatuses() []PeerStatus {
	peers, err := e.db.ListPeers()
	if err != nil {
		return nil
	}
	out := make([]PeerStatus, 0, len(peers))
	for _, p := range peers {
		st := PeerStatus{
			DeviceID: p.DeviceID, Name: p.Name, Platform: p.Platform, State: StateOffline,
			Addrs: p.TailnetAddrs, LastSeenAt: p.LastSeenAt, ApplyMode: p.ApplyMode, SendEnabled: !p.SendDisabled,
		}
		if st.Addrs == nil {
			st.Addrs = []string{}
		}
		if p.State == "revoked" {
			st.State = StateRevoked
		} else {
			e.mu.Lock()
			l := e.links[p.DeviceID]
			e.mu.Unlock()
			if l != nil {
				st.State = l.currentState()
			}
		}
		st.Pending, _ = e.db.CountPendingOutbox(p.DeviceID)
		lk, _, _ := e.db.GetLink(p.DeviceID)
		st.RecvCursor, st.NextSeqOut = lk.RecvCursor, lk.NextSeqOut
		out = append(out, st)
	}
	return out
}

// PeerAdded starts serving a newly paired peer.
func (e *Engine) PeerAdded(p store.Peer) {
	if p.State == "paired" {
		e.startPeer(p)
	}
}

// Reconnect restarts the dial loop of a peer (after "unpaired", or on demand).
func (e *Engine) Reconnect(peerID string) error {
	p, found, err := e.db.GetPeer(peerID)
	if err != nil {
		return err
	}
	if !found || p.State != "paired" {
		return proto.Errorf(proto.CodeNotPaired, "no paired device %s", peerID)
	}
	l := e.getLink(peerID)
	l.stopDial()
	if l.currentState() == StateUnpaired {
		l.setState(StateOffline)
	}
	e.startPeer(p)
	return nil
}

// RevokePeer ends the pairing: it tells the peer (best effort), marks the peer
// revoked, blocks its fingerprint for good and drops the connection.
func (e *Engine) RevokePeer(peerID string) error {
	p, found, err := e.db.GetPeer(peerID)
	if err != nil {
		return err
	}
	if !found {
		return proto.Errorf(proto.CodeNotPaired, "no such device %s", peerID)
	}
	l := e.getLink(peerID)
	if p.State == "paired" && l.session() != nil {
		if id, err := e.Send(peerID, proto.KindControl, map[string]any{"action": "revoke"}, SendOptions{TTL: time.Minute}); err == nil {
			deadline := time.Now().Add(1500 * time.Millisecond)
			for time.Now().Before(deadline) {
				if r, ok, _ := e.db.GetOutbox(id); ok && r.State == "acked" {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
		}
	}
	l.mu.Lock()
	l.revokedByUs = true
	l.mu.Unlock()
	if err := e.db.SetPeerState(peerID, "revoked"); err != nil {
		return err
	}
	if err := e.db.AddRevokedFP(p.CertFP, e.now().Unix()); err != nil {
		return err
	}
	l.stopDial()
	l.closeSession("revoked", 1000)
	l.setState(StateRevoked)
	return nil
}

// handleControl consumes control events the engine itself owns. It reports
// whether the event was consumed.
func (e *Engine) handleControl(l *link, env *proto.Envelope) bool {
	var c struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(env.Payload, &c) != nil {
		return false
	}
	if c.Action != "revoke" {
		return false
	}
	p, found, err := e.db.GetPeer(l.peerID)
	if err != nil || !found {
		return true
	}
	_ = e.db.SetPeerState(l.peerID, "revoked")
	_ = e.db.AddRevokedFP(p.CertFP, e.now().Unix())
	l.mu.Lock()
	l.revokedByUs = true
	l.mu.Unlock()
	l.stopDial()
	e.emit(map[string]any{"type": "peer.revoked", "device_id": l.peerID, "name": p.Name})
	l.setState(StateRevoked)
	// Let the ack for this event reach the sender before the connection closes.
	go func() {
		time.Sleep(200 * time.Millisecond)
		l.closeSession("revoked", 1000)
	}()
	return true
}

// WeDial reports whether this device is the dialing side of the link to p (it
// opens the connection), which decides who serves file bytes: the listener
// serves pulls, the dialer pushes.
func (e *Engine) WeDial(p store.Peer) bool { return e.shouldDial(p) }

// PeerEndpoint is a way to reach a peer's HTTPS server.
type PeerEndpoint struct {
	Client *http.Client
	Bases  []string // https://host:port, best address first
}

// Endpoint returns an mTLS client pinned to the peer's certificate and the base
// URLs of its server (tailnet addresses only). It is how file transfers reach the
// peer outside the WebSocket.
func (e *Engine) Endpoint(p store.Peer) (PeerEndpoint, error) {
	addrs := e.tailnetStrings(p.TailnetAddrs)
	if len(addrs) == 0 || p.Port == 0 {
		return PeerEndpoint{}, proto.Errorf(proto.CodePeerOffline, "no address known for %s", p.Name)
	}
	ep := PeerEndpoint{Client: HTTPClient(ClientTLS(e.id, p.CertFP), e.timing.DialTimeout)}
	for _, t := range dialTargets(addrs, e.peerPorts(p.Port)) {
		ep.Bases = append(ep.Bases, "https://"+net.JoinHostPort(t.addr, strconv.Itoa(t.port)))
	}
	return ep, nil
}
