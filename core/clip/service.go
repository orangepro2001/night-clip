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
	// SettingSensitiveDetection turns off the LooksSensitive heuristic. Explicitly marked
	// secrets (the password vault) are never sent by the automatic path regardless.
	SettingSensitiveDetection = "sensitive_detection" // "on" (default) | "off"
	SettingApplyMode          = store.DefaultApplyModeKey
	SettingPausedUntil        = "paused_until" // unix seconds; while now < value nothing is sent or applied automatically
)

const (
	defaultTTL       = 15 * time.Minute
	defaultHistory   = 200
	suppressWindow   = 3 * time.Second  // after applying remote text, the same text read back is an echo (persisted; the ring below has no time limit)
	liveWindow       = 20 * time.Second // older events (offline catch-up, slow links) are never applied automatically
	echoRingSize     = 32
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
	// echoRing holds the hashes of texts this device wrote to its own clipboard on a
	// peer's behalf. A clipboard notification that reads one of them back is an echo no
	// matter how late it arrives (slow phones, wake from sleep); without this a text
	// could bounce between three devices. It is emptied by the next genuine local copy.
	echoRing []string
	// secretRing holds hashes of texts the shell copied out of the password vault. They are never
	// sent anywhere by the automatic path, however often the clipboard is read. Unlike echoRing it
	// survives genuine copies of other things; entries expire after secretTTL.
	secretRing map[string]time.Time
}

const secretTTL = 2 * time.Minute // a copy from the vault is cleared after 20 s; this covers copies made by hand

// MarkSecret registers text that the shell is about to put on the clipboard from the password
// vault, so the clipboard notification that follows is not broadcast to the paired devices.
func (s *Service) MarkSecret(text string) {
	h := Hash(Normalize(text))
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.secretRing == nil {
		s.secretRing = map[string]time.Time{}
	}
	for k, t := range s.secretRing { // forget what has expired
		if now.Sub(t) > secretTTL {
			delete(s.secretRing, k)
		}
	}
	for len(s.secretRing) >= 64 { // full of live marks: drop the oldest, never all of them
		var oldest string
		var ot time.Time
		for k, t := range s.secretRing {
			if oldest == "" || t.Before(ot) {
				oldest, ot = k, t
			}
		}
		delete(s.secretRing, oldest)
	}
	s.secretRing[h] = now
}

func (s *Service) isSecret(hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.secretRing[hash]
	return ok && s.now().Sub(t) <= secretTTL
}

func (s *Service) ringAdd(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.echoRing {
		if h == hash {
			return
		}
	}
	if len(s.echoRing) >= echoRingSize {
		s.echoRing = s.echoRing[1:]
	}
	s.echoRing = append(s.echoRing, hash)
}

func (s *Service) ringHas(hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.echoRing {
		if h == hash {
			return true
		}
	}
	return false
}

func (s *Service) ringClear() {
	s.mu.Lock()
	s.echoRing = nil
	s.mu.Unlock()
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
		// On by default: with no paired device nothing is sent anyway, and once the user
		// pairs one the whole point is that copying just works. An explicit "off" is kept.
		return "on"
	case SettingTTL:
		return strconv.Itoa(int(defaultTTL.Seconds()))
	case SettingHistoryLimit:
		return strconv.Itoa(defaultHistory)
	case SettingSendSensitive:
		return "on"
	case SettingSensitiveDetection:
		return "on"
	case SettingApplyMode:
		return store.DefaultApplyMode // for devices paired from now on; existing peers keep their own choice
	case SettingPausedUntil:
		return "0"
	}
	return ""
}

// detectsSensitive reports whether this device applies the LooksSensitive heuristic.
// It is per device: the peer's flag is still honored only when this switch is on.
func (s *Service) detectsSensitive() bool { return s.Setting(SettingSensitiveDetection) != "off" }

// looksSensitive is LooksSensitive gated by the user's switch. Explicitly marked secrets
// (vault copies) never reach the send path; the switch only stops guessing.
func (s *Service) looksSensitive(text string) bool {
	return s.detectsSensitive() && LooksSensitive(text)
}

// Paused reports whether the user paused sync and the pause has not ended yet.
func (s *Service) Paused() bool {
	n, err := strconv.ParseInt(s.Setting(SettingPausedUntil), 10, 64)
	return err == nil && n > 0 && s.now().Unix() < n
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
	case SettingCapture, SettingSendSensitive, SettingSensitiveDetection:
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
	case SettingPausedUntil:
		if n, err := strconv.ParseInt(value, 10, 64); err != nil || n < 0 || n > 4102444800 {
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
	SuppressPaused     = "paused"
	SuppressVault      = "vault_origin" // the text came out of the password vault: never broadcast
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

// broadcastPeers are the paired devices an automatic send reaches: the ones the user
// has not switched off.
func (s *Service) broadcastPeers() []store.Peer {
	all := s.pairedPeers()
	out := all[:0:0]
	for _, p := range all {
		if !p.SendDisabled {
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
	if s.Paused() {
		res.Suppressed = SuppressPaused
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
	if s.isSecret(hash) {
		res.Suppressed = SuppressVault
		return res, nil
	}
	st, err := s.db.GetClipState()
	if err != nil {
		return res, err
	}
	if (hash == st.LastAppliedHash && now.Unix() < st.SuppressUntil) || s.ringHas(hash) {
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
	peers := s.broadcastPeers()
	if len(peers) == 0 {
		res.Suppressed = SuppressNoPeers
		return res, nil
	}
	sensitive := s.looksSensitive(norm)
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
	s.ringClear() // the clipboard has moved on: older echoes are stale
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
		// Naming a device is an explicit choice; "everyone" leaves out switched-off ones.
		if contains(to, p.DeviceID) || (len(to) == 0 && !p.SendDisabled) {
			targets = append(targets, p)
		}
	}
	if len(targets) == 0 {
		return nil, proto.Errorf(proto.CodeNotPaired, "no paired device to send to")
	}
	return s.sendTo(targets, norm, s.looksSensitive(norm))
}

// sendTo queues the text for every target. Each link gets its own event_id: ids are
// primary keys in both databases, so sharing one across peers would make every send
// after the first fail, and would let one peer pre-claim an id another peer later uses.
// Failures are per peer: a device that cannot be queued for does not stop the others,
// and the caller sees exactly who was reached.
func (s *Service) sendTo(peers []store.Peer, norm string, sensitive bool) ([]Sent, error) {
	payload := map[string]any{"mime_type": proto.MimeText, "content": norm, "size": len(norm)}
	out := []Sent{}
	var firstErr error
	for _, p := range peers {
		id, err := s.e.Send(p.DeviceID, proto.KindClipboard, payload, peer.SendOptions{TTL: s.ttl(), Sensitive: sensitive})
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
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
	sensitive := s.detectsSensitive() && (env.Sensitive || LooksSensitive(text))
	pr, _, _ := s.db.GetPeer(peerID)
	decision := Decide(pr.ApplyMode, sensitive)
	// Only text that arrives right after it was copied may overwrite the clipboard on its
	// own. What waited in an outbox (the device was offline, asleep, the link was slow)
	// is old news by now; it is offered, not applied. The same while sync is paused.
	live := s.now().Sub(env.CreatedAt) <= liveWindow
	if decision == DecisionAuto && (!live || s.Paused()) {
		decision = DecisionManual
	}
	_, _ = s.db.PruneInbox(s.historyLimit())
	s.emit(map[string]any{
		"type": "clip.received", "event_id": env.EventID, "from": peerID, "from_name": pr.Name,
		"text": text, "sensitive": sensitive, "decision": decision, "live": live, "received_at": s.now().Unix(),
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

// BeginApply is called by the shell immediately BEFORE it writes the event's text
// to the clipboard. It opens the echo-suppression window first, so even a clipboard
// notification that fires while the write is still in progress cannot bounce the
// text back to its sender. MarkApplied must still be called after a successful write.
func (s *Service) BeginApply(eventID string) error {
	row, err := s.applyRow(eventID)
	if err != nil {
		return err
	}
	return s.recordEcho(row)
}

// MarkApplied is called by the shell after it wrote the event's text to the
// clipboard. It records the echo-suppression state (idempotent with BeginApply),
// then tells the sender.
func (s *Service) MarkApplied(eventID string) error {
	row, err := s.applyRow(eventID)
	if err != nil {
		return err
	}
	if err := s.recordEcho(row); err != nil {
		return err
	}
	return s.e.MarkInbox(row.PeerID, eventID, peer.ResultApplied, "")
}

func (s *Service) applyRow(eventID string) (store.InboxRow, error) {
	row, found, err := s.db.GetInbox(eventID)
	if err != nil {
		return store.InboxRow{}, err
	}
	if !found || row.Kind != string(proto.KindClipboard) {
		return store.InboxRow{}, proto.Errorf(proto.CodeInvalid, "no received clipboard event %s", eventID)
	}
	return row, nil
}

func (s *Service) recordEcho(row store.InboxRow) error {
	var p proto.ClipboardPayload
	if err := json.Unmarshal(row.Payload, &p); err != nil {
		return proto.Errorf(proto.CodeInternal, "stored event is unreadable")
	}
	st, err := s.db.GetClipState()
	if err != nil {
		return err
	}
	st.LastAppliedEventID = row.EventID
	st.LastAppliedHash = Hash(Normalize(p.Content))
	s.ringAdd(st.LastAppliedHash)
	st.SuppressUntil = s.now().Add(suppressWindow).Unix() + 1 // second resolution: round up
	return s.db.SetClipState(st)
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
		Sensitive: s.detectsSensitive() && (r.Sensitive || LooksSensitive(p.Content)), State: r.State,
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
