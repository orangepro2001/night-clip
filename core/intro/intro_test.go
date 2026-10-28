package intro_test

import (
	"testing"
	"time"

	"nightclip/core/internal/testnet"
	"nightclip/core/intro"
	"nightclip/core/peer"
	"nightclip/core/proto"
)

// handler routes what the intro service needs (control events, link states) like the facade does.
type handler struct{ s *intro.Service }

func (h handler) OnEvent(peerID string, env *proto.Envelope) {
	if env.Kind == proto.KindControl {
		h.s.OnControl(peerID, env)
	}
}
func (h handler) OnAck(string, string, string, string) {}
func (h handler) OnPeerState(peerID, state string)     { h.s.OnPeerState(peerID, state) }

func device(t *testing.T, name, platform string, listen bool) (*testnet.Device, *intro.Service) {
	d := testnet.New(t, name, platform, listen, nil, nil)
	s := intro.New(d.Engine, d.DB, d.ID.DeviceID, d.Emit, nil)
	d.Proxy.Set(handler{s})
	return d, s
}

// A (Windows) is paired with the phone B and with the Mac C; B and C have never met.
// Once both are connected to A, they learn each other from A and connect directly.
func TestPeersOfAPeerAreTrustedAndConnect(t *testing.T) {
	a, _ := device(t, "alpha", "windows", true)
	b, _ := device(t, "phone", "android", false)
	c, _ := device(t, "mac", "macos", true)

	c.Start(true)
	a.Know(b, 0, "")
	a.Know(c, c.Engine.ListenPort(), "")
	a.Start(true)
	c.Know(a, a.Engine.ListenPort(), "")
	b.Know(a, a.Engine.ListenPort(), "")
	b.Start(false)

	testnet.WaitUntil(t, "phone learns the mac", func() bool { _, ok, _ := b.DB.GetPeer(c.ID.DeviceID); return ok })
	testnet.WaitUntil(t, "mac learns the phone", func() bool { _, ok, _ := c.DB.GetPeer(b.ID.DeviceID); return ok })
	b.WaitState(c, peer.StateConnected)
	c.WaitState(b, peer.StateConnected)

	got, _, _ := b.DB.GetPeer(c.ID.DeviceID)
	if got.CertFP != c.ID.Fingerprint() || got.Platform != "macos" || got.State != "paired" || got.Port != c.Engine.ListenPort() {
		t.Fatalf("phone's record of the mac = %+v", got)
	}
	if ev := b.Find("peer.introduced", map[string]any{"device_id": c.ID.DeviceID}); ev == nil || ev["via_device_id"] != a.ID.DeviceID {
		t.Fatalf("no peer.introduced event naming the introducer: %v", b.Events())
	}
}

// A device removed here (its fingerprint revoked) is not brought back by an introduction,
// and with trust_introductions off nothing is added at all.
func TestRevokedAndSwitchedOffAreRespected(t *testing.T) {
	a, _ := device(t, "alpha", "windows", true)
	b, _ := device(t, "phone", "android", false)
	c, _ := device(t, "mac", "macos", true)
	d, _ := device(t, "desk", "windows", true)

	c.Start(true)
	d.Start(true)
	a.Know(b, 0, "")
	a.Know(c, c.Engine.ListenPort(), "")
	a.Know(d, d.Engine.ListenPort(), "")
	a.Start(true)
	if err := b.DB.AddRevokedFP(c.ID.Fingerprint(), time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	b.Know(a, a.Engine.ListenPort(), "")
	b.Start(false)

	testnet.WaitUntil(t, "phone learns the desk", func() bool { _, ok, _ := b.DB.GetPeer(d.ID.DeviceID); return ok })
	if _, ok, _ := b.DB.GetPeer(c.ID.DeviceID); ok {
		t.Fatal("a device the phone removed came back through an introduction")
	}

	// A second phone with the switch off learns nobody.
	e, _ := device(t, "phone2", "android", false)
	if err := e.DB.SetSetting(intro.SettingTrustIntroductions, "off"); err != nil {
		t.Fatal(err)
	}
	a.Know(e, 0, "")
	e.Know(a, a.Engine.ListenPort(), "")
	e.Start(false)
	e.WaitState(a, peer.StateConnected)
	time.Sleep(500 * time.Millisecond)
	if peers, _ := e.DB.ListPeers(); len(peers) != 1 {
		t.Fatalf("with introductions off the phone has %d peers, want only alpha", len(peers))
	}
}

// Two phones only dial; introducing them to each other would be useless.
func TestTwoPhonesAreNotIntroduced(t *testing.T) {
	a, _ := device(t, "alpha", "windows", true)
	b, _ := device(t, "phone", "android", false)
	c, _ := device(t, "phone2", "android", false)
	a.Know(b, 0, "")
	a.Know(c, 0, "")
	a.Start(true)
	b.Know(a, a.Engine.ListenPort(), "")
	c.Know(a, a.Engine.ListenPort(), "")
	b.Start(false)
	c.Start(false)
	b.WaitState(a, peer.StateConnected)
	c.WaitState(a, peer.StateConnected)
	time.Sleep(500 * time.Millisecond)
	if _, ok, _ := b.DB.GetPeer(c.ID.DeviceID); ok {
		t.Fatal("phone was told about another phone")
	}
}

func TestValidateSetting(t *testing.T) {
	for v, ok := range map[string]bool{"on": true, "off": true, "": false, "yes": false} {
		if (intro.ValidateSetting(v) == nil) != ok {
			t.Errorf("%q: ok=%v", v, !ok)
		}
	}
}
