// Package intro is transitive trust between the user's own devices ("trust introductions"):
// when two paired devices connect, each tells the other about the devices it has paired with,
// and the receiver adds them as paired without a pairing session of its own. Pairing a new
// device with any one of them is therefore enough; the rest of the mesh learns it on their next
// connection, and existing meshes fill in after an update.
//
// Rules (docs/adr/008-trust-introductions.md):
//   - only a paired peer can introduce, over its authenticated link (the envelope is the
//     ordinary control event, so an old core ignores it);
//   - a device this one removed (its fingerprint is revoked here) is never re-added, and a
//     device already known by id or fingerprint is left as it is;
//   - two devices that only dial (Android) are not introduced to each other: they could
//     never connect;
//   - the setting trust_introductions ("on" | "off", default on) turns accepting introductions
//     off; sending them is harmless and stays on.
package intro

import (
	"encoding/json"
	"net/netip"
	"regexp"
	"time"

	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// SettingTrustIntroductions is the core setting that lets this device accept introduced peers.
const SettingTrustIntroductions = "trust_introductions"

// Action is the control action carrying introductions.
const Action = "introduce"

// MaxPeers bounds one introduction (a personal mesh is a handful of devices).
const MaxPeers = 64

// sendTTL: an introduction is only worth delivering while the link that triggered it is up.
const sendTTL = 10 * time.Minute

var fingerprintRe = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// Peer is what one device tells another about a third.
type Peer struct {
	DeviceID     string   `json:"device_id"`
	Name         string   `json:"name"`
	Platform     string   `json:"platform"`
	CertFP       string   `json:"cert_fp"`
	TailnetAddrs []string `json:"tailnet_addrs"`
	Port         int      `json:"port"`
}

type payload struct {
	Action string `json:"action"`
	Peers  []Peer `json:"peers"`
}

// Engine is the part of peer.Engine this package uses (an interface for tests).
type Engine interface {
	Send(peerID string, kind proto.Kind, payload any, o peer.SendOptions) (string, error)
	PeerAdded(p store.Peer)
	AddrAllowed(a netip.Addr) bool
	Platform() string
}

// Service sends and accepts introductions.
type Service struct {
	e    Engine
	db   *store.DB
	self string // our device id
	emit func(map[string]any)
	now  func() time.Time
}

// New builds the service; self is this device's id.
func New(e Engine, db *store.DB, self string, emit func(map[string]any), now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{e: e, db: db, self: self, emit: emit, now: now}
}

// Enabled reports whether this device accepts introductions (default on).
func (s *Service) Enabled() bool {
	v, found, err := s.db.GetSetting(SettingTrustIntroductions)
	return err != nil || !found || v != "off"
}

// ValidateSetting checks a value for SettingTrustIntroductions.
func ValidateSetting(value string) error {
	if value != "on" && value != "off" {
		return proto.Errorf(proto.CodeInvalid, "trust_introductions must be on or off")
	}
	return nil
}

// Setting returns the effective value.
func (s *Service) Setting() string {
	if s.Enabled() {
		return "on"
	}
	return "off"
}

// OnPeerState introduces our other peers to a peer whose link just came up.
func (s *Service) OnPeerState(peerID, state string) {
	if state == peer.StateConnected {
		go s.IntroduceTo(peerID)
	}
}

// IntroduceTo sends target every other paired device worth knowing. Best effort.
func (s *Service) IntroduceTo(target string) {
	tp, found, err := s.db.GetPeer(target)
	if err != nil || !found || tp.State != "paired" {
		return
	}
	all, err := s.db.ListPeers()
	if err != nil {
		return
	}
	var list []Peer
	for _, p := range all {
		if p.State != "paired" || p.DeviceID == target || p.CertFP == tp.CertFP || !canMeet(tp.Platform, p.Platform) {
			continue
		}
		list = append(list, Peer{DeviceID: p.DeviceID, Name: p.Name, Platform: p.Platform, CertFP: p.CertFP, TailnetAddrs: p.TailnetAddrs, Port: p.Port})
		if len(list) == MaxPeers {
			break
		}
	}
	if len(list) == 0 {
		return
	}
	_, _ = s.e.Send(target, proto.KindControl, payload{Action: Action, Peers: list}, peer.SendOptions{TTL: sendTTL})
}

// canMeet: at least one of the two listens, otherwise they could never connect.
func canMeet(a, b string) bool { return proto.DesktopPlatform(a) || proto.DesktopPlatform(b) }

// OnControl handles an introduction from peerID; it reports whether env was one.
func (s *Service) OnControl(peerID string, env *proto.Envelope) bool {
	var p payload
	if json.Unmarshal(env.Payload, &p) != nil || p.Action != Action {
		return false
	}
	if !s.Enabled() {
		return true
	}
	from, found, err := s.db.GetPeer(peerID)
	if err != nil || !found || from.State != "paired" {
		return true // only a device we trust may vouch for others
	}
	if len(p.Peers) > MaxPeers {
		p.Peers = p.Peers[:MaxPeers]
	}
	for _, ip := range p.Peers {
		if added, ok := s.accept(ip); ok {
			s.e.PeerAdded(added)
			s.emit(map[string]any{"type": "peer.introduced", "device_id": added.DeviceID, "name": added.Name,
				"platform": added.Platform, "via_device_id": from.DeviceID, "via_name": from.Name})
		}
	}
	return true
}

// accept validates one introduced device and stores it as paired; ok=false leaves the database untouched.
func (s *Service) accept(ip Peer) (store.Peer, bool) {
	if !proto.ValidDeviceID(ip.DeviceID) || ip.DeviceID == s.self || !fingerprintRe.MatchString(ip.CertFP) ||
		!proto.KnownPlatform(ip.Platform) || len(ip.Name) > 64 || ip.Port < 0 || ip.Port > 65535 {
		return store.Peer{}, false
	}
	if !canMeet(s.e.Platform(), ip.Platform) {
		return store.Peer{}, false
	}
	if revoked, err := s.db.IsRevoked(ip.CertFP); err != nil || revoked {
		return store.Peer{}, false // removed here once: only a pairing by the user brings it back
	}
	if _, found, err := s.db.GetPeer(ip.DeviceID); err != nil || found {
		return store.Peer{}, false
	}
	if _, found, err := s.db.FindPeerByFingerprint(ip.CertFP); err != nil || found {
		return store.Peer{}, false
	}
	var addrs []string
	for _, a := range ip.TailnetAddrs {
		if pa, err := netip.ParseAddr(a); err == nil && s.e.AddrAllowed(pa) {
			addrs = append(addrs, pa.Unmap().String())
		}
		if len(addrs) == 8 {
			break
		}
	}
	if proto.DesktopPlatform(ip.Platform) && (len(addrs) == 0 || ip.Port == 0) {
		return store.Peer{}, false // we would have nowhere to dial
	}
	name := ip.Name
	if name == "" {
		name = "device"
	}
	p := store.Peer{
		DeviceID: ip.DeviceID, Name: name, Platform: ip.Platform, CertFP: ip.CertFP, TailnetAddrs: addrs, Port: ip.Port,
		Role: "auto", State: "paired", ApplyMode: "notify", PairedAt: s.now().Unix(), VaultSync: true,
	}
	if err := s.db.PutPeer(p); err != nil {
		return store.Peer{}, false
	}
	return p, true
}
