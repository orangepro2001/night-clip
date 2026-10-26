package clip

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// Settings keys (stored in the settings table; see Service.Setting).
const (
	SettingCapture       = "capture"        // "on" | "off"; unset means off until the user chooses
	SettingTTL           = "clip_ttl_s"     // seconds an event may wait for an offline peer
	SettingHistoryLimit  = "history_limit"  // inbox rows kept
	SettingSendSensitive = "send_sensitive" // "on" (default) | "off"
	SettingApplyMode     = "default_apply_mode"
)

const (
	defaultTTL       = 15 * time.Minute
	defaultHistory   = 200
	suppressWindow   = 3 * time.Second // after applying remote text, the same text read back is an echo
	duplicateWindow  = 3 * time.Second // one copy can fire several clipboard notifications
	maxInboxTextList = 500
)

// Service implements the clipboard behaviour on top of a peer.Engine.
type Service struct {
	e    *peer.Engine
	db   *store.DB
	emit func(map[string]any)
	now  func() time.Time

	mu           sync.Mutex
	lastSentHash string
	lastSentAt   time.Time
}

// New builds the service. emit receives clip.* events for the shell.
func New(e *peer.Engine, db *store.DB, emit func(map[string]any), now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	if emit == nil {
		emit = func(map[string]any) {}
	}
	return &Service{e: e, db: db, emit: emit, now: now}
}

// ---- settings ------------------------------------------------------------------

// Setting returns a setting with its default applied.
func (s *Service) Setting(key string) string {
	v, found, _ := s.db.GetSetting(key)
	if found {
		return v
	}
	switch key {
	case SettingCapture:
		return "off"
	case SettingTTL:
		return strconv.Itoa(int(defaultTTL.Seconds()))
	case SettingHistoryLimit:
		return strconv.Itoa(defaultHistory)
	case SettingSendSensitive:
		return "on"
	case SettingApplyMode:
		return "notify"
	}
	return ""
}

func (s *Service) ttl() time.Duration {
	if n, err := strconv.Atoi(s.Setting(SettingTTL)); err == nil && n >= 30 && n <= 86400 {
		return time.Duration(n) * time.Second
	}
	return defaultTTL
}

func (s *Service) historyLimit() int {
	if n, err := strconv.Atoi(s.Setting(SettingHistoryLimit)); err == nil && n >= 10 && n <= 5000 {
		return n
	}
	return defaultHistory
}

// ValidateSetting reports whether value is acceptable for key.
func ValidateSetting(key, value string) error {
	bad := func() error { return proto.Errorf(proto.CodeInvalid, "invalid value for %s", key) }
	switch key {
	case SettingCapture, SettingSendSensitive:
		if value != "on" && value != "off" {
			return bad()
		}
	case SettingTTL:
		if n, err := strconv.Atoi(value); err != nil || n < 30 || n > 86400 {
			return bad()
		}
	case SettingHistoryLimit:
		if n, err := strconv.Atoi(value); err != nil || n < 10 || n > 5000 {
			return bad()
		}
	case SettingApplyMode:
		if value != "notify" && value != "manual" && value != "auto" {
			return bad()
		}
	default:
		return proto.Errorf(proto.CodeInvalid, "unknown setting %q", key)
	}
	return nil
}

// ---- sending ------------------------------------------------------------------

// Sent names one queued event.
type Sent struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	EventID  string `json:"event_id"`
}

// LocalResult is the answer to LocalChanged.
type LocalResult struct {
	Sent       []Sent `json:"sent"`
	Suppressed string `json:"suppressed,omitempty"`
}

// Suppression reasons.
const (
	SuppressCaptureOff = "capture_off"
	SuppressEmpty      = "empty"
	SuppressEcho       = "echo"
	SuppressDuplicate  = "duplicate"
	SuppressNoPeers    = "no_peers"
	SuppressTooLarge   = "too_large"
	SuppressSensitive  = "sensitive_blocked"
)

func (s *Service) pairedPeers() []store.Peer {
	all, _ := s.db.ListPeers()
	out := all[:0:0]
	for _, p := range all {
		if p.State == "paired" {
			out = append(out, p)
		}
	}
	return out
}

// LocalChanged is called by the shell when the user copied something. The core
// decides whether it is sent: capture must be on, the text must not be empty,
// too large, an echo of text we just applied, or a repeat of the last send.
func (s *Service) LocalChanged(text string) (LocalResult, error) {
	res := LocalResult{Sent: []Sent{}}
	if s.Setting(SettingCapture) != "on" {
		res.Suppressed = SuppressCaptureOff
		return res, nil
	}
	norm := Normalize(text)
	if strings.TrimSpace(norm) == "" {
		res.Suppressed = SuppressEmpty
		return res, nil
	}
	if len(norm) > proto.MaxClipboardBytes {
		res.Suppressed = SuppressTooLarge
		return res, nil
	}
	hash := Hash(norm)
	now := s.now()
	st, err := s.db.GetClipState()
	if err != nil {
		return res, err
	}
	if hash == st.LastAppliedHash && now.Unix() < st.SuppressUntil {
		res.Suppressed = SuppressEcho
		return res, nil
	}
	s.mu.Lock()
	dup := hash == s.lastSentHash && now.Sub(s.lastSentAt) < duplicateWindow
	s.mu.Unlock()
	if dup {
		res.Suppressed = SuppressDuplicate
		return res, nil
	}
	peers := s.pairedPeers()
	if len(peers) == 0 {
		res.Suppressed = SuppressNoPeers
		return res, nil
	}
	sensitive := LooksSensitive(norm)
	if sensitive && s.Setting(SettingSendSensitive) == "off" {
		res.Suppressed = SuppressSensitive
		return res, nil
	}

	res.Sent, err = s.sendTo(peers, norm, sensitive)
	if err != nil {
		return res, err
	}
	s.mu.Lock()
	s.lastSentHash, s.lastSentAt = hash, now
	s.mu.Unlock()
	st.LastLocalSentHash = hash
	_ = s.db.SetClipState(st)
	return res, nil
}

// Send is the user explicitly sending text (button, share sheet): it ignores
// the capture switch and the echo/duplicate checks. to limits the targets;
// empty means every paired device.
func (s *Service) Send(text string, to []string) ([]Sent, error) {
	norm := Normalize(text)
	if strings.TrimSpace(norm) == "" {
		return nil, proto.Errorf(proto.CodeInvalid, "there is nothing to send")
	}
	if err := CheckSize(norm); err != nil {
		return nil, err
	}
	var targets []store.Peer
	for _, p := range s.pairedPeers() {
		if len(to) == 0 || contains(to, p.DeviceID) {
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		return nil, proto.Errorf(proto.CodeNotPaired, "no paired device to send to")
	}
	return s.sendTo(targets, norm, LooksSensitive(norm))
}

func (s *Service) sendTo(peers []store.Peer, norm string, sensitive bool) ([]Sent, error) {
	payload := map[string]any{"mime_type": proto.MimeText, "content": norm, "size": len(norm)}
	// One event_id for every target: the same content is the same event
	// (docs/api-spec.md §2).
	var eventID string
	out := []Sent{}
	var firstErr error
	for _, p := range peers {
		id, err := s.e.Send(p.DeviceID, proto.KindClipboard, payload,
			peer.SendOptions{TTL: s.ttl(), Sensitive: sensitive, EventID: eventID})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		eventID = id
		out = append(out, Sent{DeviceID: p.DeviceID, Name: p.Name, EventID: id})
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ---- receiving ------------------------------------------------------------------

// OnEvent handles an accepted clipboard event from the peer layer.
func (s *Service) OnEvent(peerID string, env *proto.Envelope) {
	if env.Kind != proto.KindClipboard {
		return
	}
	var p proto.ClipboardPayload
	if json.Unmarshal(env.Payload, &p) != nil {
		return
	}
	text := Normalize(p.Content)
	sensitive := env.Sensitive || LooksSensitive(text)
	pr, _, _ := s.db.GetPeer(peerID)
	decision := Decide(pr.ApplyMode, sensitive)
	_, _ = s.db.PruneInbox(s.historyLimit())
	s.emit(map[string]any{
		"type": "clip.received", "event_id": env.EventID, "from": peerID, "from_name": pr.Name,
		"text": text, "sensitive": sensitive, "decision": decision, "received_at": s.now().Unix(),
	})
}

// OnAck reports that the peer acknowledged one of our events.
func (s *Service) OnAck(peerID, eventID, result, reason string) {
	ev := map[string]any{"type": "clip.ack", "event_id": eventID, "from": peerID, "result": result}
	if reason != "" {
		ev["reason"] = reason
	}
	s.emit(ev)
}

// MarkApplied is called by the shell after it wrote the event's text to the
// clipboard. It records the echo-suppression state first (so the clipboard
// notification the write is about to cause is recognised), then tells the sender.
func (s *Service) MarkApplied(eventID string) error {
	row, found, err := s.db.GetInbox(eventID)
	if err != nil {
		return err
	}
	if !found || row.Kind != string(proto.KindClipboard) {
		return proto.Errorf(proto.CodeInvalid, "no received clipboard event %s", eventID)
	}
	var p proto.ClipboardPayload
	if err := json.Unmarshal(row.Payload, &p); err != nil {
		return proto.Errorf(proto.CodeInternal, "stored event is unreadable")
	}
	st, err := s.db.GetClipState()
	if err != nil {
		return err
	}
	st.LastAppliedEventID = eventID
	st.LastAppliedHash = Hash(Normalize(p.Content))
	st.SuppressUntil = s.now().Add(suppressWindow).Unix() + 1 // second resolution: round up
	if err := s.db.SetClipState(st); err != nil {
		return err
	}
	return s.e.MarkInbox(row.PeerID, eventID, peer.ResultApplied, "")
}

// Dismiss records that the user declined an event.
func (s *Service) Dismiss(eventID string) error {
	row, found, err := s.db.GetInbox(eventID)
	if err != nil {
		return err
	}
	if !found {
		return proto.Errorf(proto.CodeInvalid, "no received event %s", eventID)
	}
	return s.e.MarkInbox(row.PeerID, eventID, peer.ResultIgnored, "user_dismissed")
}

// ---- history ------------------------------------------------------------------

// Item is one inbox entry for the UI.
type Item struct {
	EventID    string `json:"event_id"`
	From       string `json:"from"`
	FromName   string `json:"from_name"`
	Text       string `json:"text"`
	Sensitive  bool   `json:"sensitive"`
	State      string `json:"state"`
	ReceivedAt int64  `json:"received_at"`
	ExpiresAt  int64  `json:"expires_at"`
	AppliedAt  int64  `json:"applied_at,omitempty"`
}

func (s *Service) item(r store.InboxRow, names map[string]string) (Item, bool) {
	if r.Kind != string(proto.KindClipboard) {
		return Item{}, false
	}
	var p proto.ClipboardPayload
	if json.Unmarshal(r.Payload, &p) != nil {
		return Item{}, false
	}
	return Item{
		EventID: r.EventID, From: r.PeerID, FromName: names[r.PeerID], Text: Normalize(p.Content),
		Sensitive: r.Sensitive || LooksSensitive(p.Content), State: r.State,
		ReceivedAt: r.ReceivedAt, ExpiresAt: r.ExpiresAt, AppliedAt: r.AppliedAt,
	}, true
}

func (s *Service) names() map[string]string {
	m := map[string]string{}
	all, _ := s.db.ListPeers()
	for _, p := range all {
		m[p.DeviceID] = p.Name
	}
	return m
}

// Inbox lists received clipboard events, newest first.
func (s *Service) Inbox(limit int) ([]Item, error) {
	if limit <= 0 || limit > maxInboxTextList {
		limit = maxInboxTextList
	}
	rows, err := s.db.ListInbox(0)
	if err != nil {
		return nil, err
	}
	names := s.names()
	out := []Item{}
	for _, r := range rows {
		if it, ok := s.item(r, names); ok {
			out = append(out, it)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

// GetInbox returns one received event.
func (s *Service) GetInbox(eventID string) (Item, error) {
	row, found, err := s.db.GetInbox(eventID)
	if err != nil {
		return Item{}, err
	}
	if found {
		if it, ok := s.item(row, s.names()); ok {
			return it, nil
		}
	}
	return Item{}, proto.Errorf(proto.CodeInvalid, "no received clipboard event %s", eventID)
}

// ClearInbox deletes the history.
func (s *Service) ClearInbox() error { return s.db.ClearInbox() }
