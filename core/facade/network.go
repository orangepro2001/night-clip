package facade

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"nightclip/core/clip"
	"nightclip/core/pairing"
	"nightclip/core/peer"
	"nightclip/core/proto"
)

// netState is everything the networked features add to a Core.
type netState struct {
	eng  *peer.Engine
	pair *pairing.Manager
	clip *clip.Service

	joinMu sync.Mutex
	joins  map[string]context.CancelFunc

	// test hooks (set by in-package tests before Init; zero in production)
	hooks  peer.Hooks
	timing peer.Timing
	pairT  pairing.Timings
}

// handler adapts the peer layer's callbacks to the services.
type handler struct{ c *Core }

func (h handler) OnEvent(peerID string, env *proto.Envelope) {
	if n := h.c.net(); n != nil && env.Kind == proto.KindClipboard {
		n.clip.OnEvent(peerID, env)
	}
}

func (h handler) OnAck(peerID, eventID, result, reason string) {
	if n := h.c.net(); n != nil {
		n.clip.OnAck(peerID, eventID, result, reason)
	}
}

func (h handler) OnPeerState(string, string) {} // the engine already emits peer.state

func (c *Core) net() *netState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.n
}

// startNetwork builds and starts the engine and the services. c.mu is held by Init.
func (c *Core) startNetwork(cfg Config) (*netState, error) {
	n := &netState{joins: map[string]context.CancelFunc{}, hooks: c.hooks, timing: c.timing, pairT: c.pairT}
	n.eng = peer.New(peer.Config{
		Identity: c.id, Store: c.db, Listen: cfg.Listen, Port: cfg.Port, Platform: cfg.Platform,
		Handler: handler{c}, Emit: c.emit, Hooks: n.hooks, Timing: n.timing,
	})
	n.pair = pairing.NewManager(n.eng, c.db, c.id, c.emit, nil, n.pairT)
	n.clip = clip.New(n.eng, c.db, c.emit, nil)
	if err := n.eng.Start(); err != nil {
		return nil, err
	}
	return n, nil
}

func (n *netState) close() {
	n.joinMu.Lock()
	for _, cancel := range n.joins {
		cancel()
	}
	n.joinMu.Unlock()
	n.eng.Close()
}

// parseParams parses method parameters; empty params mean "{}".
func parseParams(paramsJSON string, out any) error {
	if strings.TrimSpace(paramsJSON) == "" {
		paramsJSON = "{}"
	}
	if err := json.Unmarshal([]byte(paramsJSON), out); err != nil {
		return proto.Errorf(proto.CodeInvalid, "params: %v", err)
	}
	return nil
}

// networkCall handles the methods backed by the engine and services. handled is
// false when method is not one of them.
func (c *Core) networkCall(method, params string) (result any, handled bool, err error) {
	switch {
	case strings.HasPrefix(method, "pair."), strings.HasPrefix(method, "peers."), strings.HasPrefix(method, "clip."),
		strings.HasPrefix(method, "inbox."), strings.HasPrefix(method, "settings."), method == "diagnostics.get":
	default:
		return nil, false, nil
	}
	n := c.net()
	if n == nil {
		return nil, true, proto.Errorf(proto.CodeNotInitialized, "call Init first")
	}
	switch method {
	case "settings.get":
		result, err = c.settingsGet(n, params)
	case "settings.set":
		result, err = c.settingsSet(n, params)
	case "pair.begin":
		result, err = c.pairBegin(n)
	case "pair.cancel":
		var p struct {
			SessionID string `json:"session_id"`
		}
		if err = parseParams(params, &p); err == nil {
			n.pair.Cancel(p.SessionID)
			result = map[string]any{}
		}
	case "pair.pending":
		result = map[string]any{"pending": orEmpty(n.pair.Pending())}
	case "pair.confirm":
		var p struct {
			PairID string `json:"pair_id"`
			Accept bool   `json:"accept"`
		}
		if err = parseParams(params, &p); err == nil {
			if err = n.pair.Confirm(p.PairID, p.Accept); err == nil {
				result = map[string]any{}
			}
		}
	case "pair.join":
		result, err = c.pairJoin(n, params)
	case "peers.list":
		result = map[string]any{"peers": n.eng.PeerStatuses()}
	case "peers.update":
		result, err = c.peersUpdate(n, params)
	case "peers.remove":
		result, err = c.peersRemove(n, params)
	case "clip.local_changed":
		var p struct {
			Text string `json:"text"`
		}
		if err = parseParams(params, &p); err == nil {
			result, err = n.clip.LocalChanged(p.Text)
		}
	case "clip.send":
		var p struct {
			Text string   `json:"text"`
			To   []string `json:"to"`
		}
		if err = parseParams(params, &p); err == nil {
			var sent []clip.Sent
			if sent, err = n.clip.Send(p.Text, p.To); err == nil {
				result = map[string]any{"sent": sent}
			}
		}
	case "clip.mark_applied", "clip.dismiss":
		var p struct {
			EventID string `json:"event_id"`
		}
		if err = parseParams(params, &p); err == nil {
			if method == "clip.dismiss" {
				err = n.clip.Dismiss(p.EventID)
			} else {
				err = n.clip.MarkApplied(p.EventID)
			}
			if err == nil {
				result = map[string]any{}
			}
		}
	case "inbox.list":
		var p struct {
			Limit int `json:"limit"`
		}
		if err = parseParams(params, &p); err == nil {
			var items []clip.Item
			if items, err = n.clip.Inbox(p.Limit); err == nil {
				result = map[string]any{"items": items}
			}
		}
	case "inbox.get":
		var p struct {
			EventID string `json:"event_id"`
		}
		if err = parseParams(params, &p); err == nil {
			result, err = n.clip.GetInbox(p.EventID)
		}
	case "inbox.clear":
		if err = n.clip.ClearInbox(); err == nil {
			result = map[string]any{}
		}
	case "diagnostics.get":
		result = c.diagnostics(n)
	default:
		return nil, true, proto.Errorf(proto.CodeMethodNotFound, "unknown method %q", method)
	}
	return result, true, err
}

func orEmpty[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}

// ---- settings ------------------------------------------------------------------

var settingKeys = []string{clip.SettingCapture, clip.SettingTTL, clip.SettingHistoryLimit, clip.SettingSendSensitive, clip.SettingApplyMode}

func (c *Core) settingsGet(n *netState, params string) (any, error) {
	var p struct {
		Key string `json:"key"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	if p.Key != "" {
		if err := clip.ValidateSetting(p.Key, n.clip.Setting(p.Key)); err != nil {
			return nil, proto.Errorf(proto.CodeInvalid, "unknown setting %q", p.Key)
		}
		return map[string]any{"key": p.Key, "value": n.clip.Setting(p.Key)}, nil
	}
	all := map[string]string{}
	for _, k := range settingKeys {
		all[k] = n.clip.Setting(k)
	}
	return map[string]any{"settings": all}, nil
}

func (c *Core) settingsSet(n *netState, params string) (any, error) {
	var p struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	if err := clip.ValidateSetting(p.Key, p.Value); err != nil {
		return nil, err
	}
	if err := c.db.SetSetting(p.Key, p.Value); err != nil {
		return nil, err
	}
	return map[string]any{"key": p.Key, "value": p.Value}, nil
}

// ---- pairing ------------------------------------------------------------------

func (c *Core) pairBegin(n *netState) (any, error) {
	b, err := n.pair.Begin()
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"session_id": b.SessionID, "link": b.Link, "host": b.Host, "port": b.Port,
		"expires_at": b.ExpiresAt.UTC().Format(time.RFC3339),
	}
	// The shells draw the code themselves from this matrix, so neither needs a QR library.
	if q, qerr := qrcode.New(b.Link, qrcode.Medium); qerr == nil {
		bm := q.Bitmap()
		rows := make([]string, len(bm))
		for y, row := range bm {
			var sb strings.Builder
			for _, on := range row {
				if on {
					sb.WriteByte('1')
				} else {
					sb.WriteByte('0')
				}
			}
			rows[y] = sb.String()
		}
		out["qr"] = map[string]any{"size": len(bm), "rows": rows}
	}
	return out, nil
}

func (c *Core) pairJoin(n *netState, params string) (any, error) {
	var p struct {
		Link string `json:"link"`
		Host string `json:"host"`
		Port int    `json:"port"`
		PSK  string `json:"psk"`
		FP   string `json:"fp"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	var link pairing.Link
	var err error
	if p.Link != "" {
		link, err = pairing.ParseLink(p.Link, time.Now())
	} else {
		link, err = pairing.ParseLink(manualLink(p.Host, p.Port, p.PSK, p.FP), time.Now())
	}
	if err != nil {
		return nil, err
	}
	n.joinMu.Lock()
	if len(n.joins) > 0 {
		n.joinMu.Unlock()
		return nil, proto.Errorf(proto.CodeRateLimited, "a pairing is already in progress")
	}
	id := randomHex()
	ctx, cancel := context.WithCancel(context.Background())
	n.joins[id] = cancel
	n.joinMu.Unlock()

	go func() {
		defer func() {
			cancel()
			n.joinMu.Lock()
			delete(n.joins, id)
			n.joinMu.Unlock()
		}()
		listenPort := 0
		if c.cfg.Listen {
			listenPort = n.eng.ListenPort()
		}
		peerRec, jerr := pairing.Join(ctx, pairing.JoinParams{
			Link: link, Identity: c.id, Platform: c.cfg.Platform, Listen: c.cfg.Listen, ListenPort: listenPort,
			AllowAddr: n.eng.AddrAllowed,
			OnSAS: func(sas string) {
				c.emit(map[string]any{"type": "pair.sas", "join_id": id, "sas": sas})
			},
		})
		if jerr == nil && peerRec.DeviceID == c.id.DeviceID {
			jerr = proto.Errorf(proto.CodePairingFailed, "that is this device")
		}
		if jerr != nil {
			code, msg := string(proto.CodeInternal), jerr.Error()
			if pe, ok := jerr.(*proto.Error); ok {
				code, msg = string(pe.Code), pe.Message
			}
			c.emit(map[string]any{"type": "pair.result", "join_id": id, "state": "failed", "role": "joiner", "code": code, "message": msg})
			return
		}
		peerRec.ApplyMode = n.clip.Setting(clip.SettingApplyMode)
		if err := c.db.PutPeer(peerRec); err != nil {
			c.emit(map[string]any{"type": "pair.result", "join_id": id, "state": "failed", "role": "joiner", "code": string(proto.CodeDBUnavailable), "message": err.Error()})
			return
		}
		n.eng.PeerAdded(peerRec)
		c.emit(map[string]any{"type": "pair.result", "join_id": id, "state": "accepted", "role": "joiner",
			"device_id": peerRec.DeviceID, "name": peerRec.Name})
	}()
	return map[string]any{"join_id": id}, nil
}

func manualLink(host string, port int, psk, fp string) string {
	// Rebuild a link so manual entry goes through the same strict parser as a QR code.
	l := "clipbridge://pair?v=1&host=" + host + "&port=" + strconv.Itoa(port) + "&psk=" + psk +
		"&exp=" + strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)
	if fp != "" {
		l += "&fp=" + fp
	}
	return l
}

// ---- peers ------------------------------------------------------------------

func (c *Core) peersUpdate(n *netState, params string) (any, error) {
	var p struct {
		DeviceID  string  `json:"device_id"`
		Name      *string `json:"name"`
		ApplyMode *string `json:"apply_mode"`
		Role      *string `json:"role"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	rec, found, err := c.db.GetPeer(p.DeviceID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, proto.Errorf(proto.CodeNotPaired, "no such device %s", p.DeviceID)
	}
	roleChanged := false
	if p.Name != nil {
		name := strings.TrimSpace(*p.Name)
		if name == "" || len(name) > 64 {
			return nil, proto.Errorf(proto.CodeInvalid, "name must be 1-64 characters")
		}
		rec.Name = name
	}
	if p.ApplyMode != nil {
		if err := clip.ValidateSetting(clip.SettingApplyMode, *p.ApplyMode); err != nil {
			return nil, err
		}
		rec.ApplyMode = *p.ApplyMode
	}
	if p.Role != nil {
		switch *p.Role {
		case "auto", "dialer", "listener":
		default:
			return nil, proto.Errorf(proto.CodeInvalid, "role must be auto, dialer or listener")
		}
		roleChanged = rec.Role != *p.Role
		rec.Role = *p.Role
	}
	if err := c.db.PutPeer(rec); err != nil {
		return nil, err
	}
	if roleChanged && rec.State == "paired" {
		_ = n.eng.Reconnect(rec.DeviceID)
	}
	return map[string]any{}, nil
}

func (c *Core) peersRemove(n *netState, params string) (any, error) {
	var p struct {
		DeviceID string `json:"device_id"`
		Forget   bool   `json:"forget"` // also delete the record (the fingerprint stays blocked)
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	if err := n.eng.RevokePeer(p.DeviceID); err != nil {
		return nil, err
	}
	if p.Forget {
		if err := c.db.DeletePeer(p.DeviceID); err != nil {
			return nil, err
		}
	}
	return map[string]any{}, nil
}

// ---- diagnostics ------------------------------------------------------------------

func (c *Core) diagnostics(n *netState) any {
	addrs := []string{}
	for _, a := range n.eng.ListenAddrs() {
		addrs = append(addrs, a.String())
	}
	fp := c.id.Fingerprint()
	return map[string]any{
		"core": Version, "protocol": "clipbridge/v1", "device_id": c.id.DeviceID,
		"fingerprint_prefix": fp[:8], "platform": c.cfg.Platform, "listen": c.cfg.Listen,
		"listening_addrs": addrs, "port": n.eng.ListenPort(),
		"peers": n.eng.PeerStatuses(), "events_dropped": c.dropped.Load(),
		"update": c.upd.Status(),
	}
}

func randomHex() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
