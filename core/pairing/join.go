package pairing

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"nightclip/core/identity"
	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// JoinParams describes one attempt to pair with a listening device.
type JoinParams struct {
	Link     Link // host, port, psk and (from a QR code) the expected fingerprint
	Identity *identity.Identity
	Platform string
	// What we tell the other side about ourselves.
	Listen     bool
	ListenPort int
	Addrs      []string

	// AllowAddr vets Link.Host (tailnet only; loopback in tests).
	AllowAddr func(netip.Addr) bool
	Now       func() time.Time
	Poll      time.Duration // how often to ask whether the user decided (default 1 s)
	Timeout   time.Duration // overall limit (default 130 s)
	DialWait  time.Duration // connect + handshake limit (default 8 s)
	// OnSAS is called once the listener accepted the request, with the code both
	// users must compare.
	OnSAS func(sas string)
}

func (p *JoinParams) defaults() {
	if p.Now == nil {
		p.Now = time.Now
	}
	if p.Poll <= 0 {
		p.Poll = time.Second
	}
	if p.Timeout <= 0 {
		p.Timeout = 130 * time.Second
	}
	if p.DialWait <= 0 {
		p.DialWait = 8 * time.Second
	}
}

// ErrRejected means the user on the other device said no.
var ErrRejected = proto.Errorf(proto.CodePairingFailed, "the other device declined the pairing")

// Join pairs with the device described by p.Link. It returns the peer to store.
// Nothing is persisted here: the caller saves the result and starts the link.
func Join(ctx context.Context, p JoinParams) (store.Peer, error) {
	p.defaults()
	if addr, err := netip.ParseAddr(p.Link.Host); err != nil || (p.AllowAddr != nil && !p.AllowAddr(addr)) {
		return store.Peer{}, proto.Errorf(proto.CodeInvalid, "pairing link: %q is not a tailnet address", p.Link.Host)
	}
	if !p.Now().Before(p.Link.Exp) {
		return store.Peer{}, proto.Errorf(proto.CodePairingExpired, "this pairing link has expired")
	}
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()

	var cap peer.Capture
	client := peer.HTTPClient(peer.ClientTLSCapture(p.Identity, p.Link.FP, &cap), p.DialWait)
	base := "https://" + net.JoinHostPort(p.Link.Host, strconv.Itoa(p.Link.Port))
	nonce := NewNonce()

	// 1. introduce ourselves; the proof binds the fingerprint we actually see
	accepted, err := post(ctx, client, base+"/v1/pair", func() pairRequest {
		serverFP := cap.FP() // known after the first handshake; for a pinned link it equals Link.FP
		if serverFP == "" {
			serverFP = p.Link.FP
		}
		return pairRequest{
			SessionID: SessionID(p.Link.PSK), DeviceID: p.Identity.DeviceID, Name: p.Identity.Name, Platform: p.Platform,
			CertFP: p.Identity.Fingerprint(), TailnetAddrs: p.Addrs, Nonce: nonce,
			Port:  portIf(p.Listen, p.ListenPort),
			Proof: Proof(p.Link.PSK, p.Identity.DeviceID, p.Identity.Fingerprint(), serverFP, nonce),
		}
	}, p.Link.FP == "")
	if err != nil {
		return store.Peer{}, err
	}
	// 2. the code we compute from what we saw must equal the one the listener shows
	mySAS := SAS(cap.FP(), p.Identity.Fingerprint(), nonce)
	if accepted.SAS != mySAS {
		return store.Peer{}, proto.Errorf(proto.CodePairingFailed,
			"the confirmation codes differ: someone may be intercepting the connection")
	}
	// 2b. the device that answered must know the pairing code, not just have answered
	own := p.Identity.Fingerprint()
	if !proofOK(accepted.Proof, p.Link.PSK, StateWaiting, cap.FP(), own, nonce, mySAS, cap.DeviceID()) {
		return store.Peer{}, proto.Errorf(proto.CodePairingFailed,
			"the device that answered did not prove it holds the pairing code")
	}
	if p.OnSAS != nil {
		p.OnSAS(mySAS)
	}

	// 3. wait for the user on the other device
	pollURL := base + "/v1/pair/" + accepted.PairID
	for {
		select {
		case <-ctx.Done():
			return store.Peer{}, proto.Errorf(proto.CodePairingExpired, "nobody confirmed the pairing in time")
		case <-time.After(p.Poll):
		}
		var st pairResponse
		if err := getJSON(ctx, client, pollURL, &st); err != nil {
			var pe *proto.Error
			if errors.As(err, &pe) && pe.Code == proto.CodePairingExpired {
				return store.Peer{}, err
			}
			continue // transient: keep asking until the overall timeout
		}
		if !proofOK(st.Proof, p.Link.PSK, st.State, cap.FP(), own, nonce, mySAS, cap.DeviceID()) {
			return store.Peer{}, proto.Errorf(proto.CodePairingFailed,
				"the device that answered did not prove it holds the pairing code")
		}
		switch st.State {
		case StateWaiting:
		case StateRejected:
			return store.Peer{}, ErrRejected
		case StateExpired:
			return store.Peer{}, proto.Errorf(proto.CodePairingExpired, "the pairing request expired")
		case StateAccepted:
			return finish(p, &cap, st)
		}
	}
}

func proofOK(got string, psk []byte, state, serverFP, clientFP, nonce, sas, deviceID string) bool {
	return hmac.Equal([]byte(got), []byte(ServerProof(psk, state, serverFP, clientFP, nonce, sas, deviceID)))
}

func portIf(listen bool, port int) int {
	if listen {
		return port
	}
	return 0
}

// finish verifies what the listener claims about itself against the certificate
// it presented, and builds the peer record.
func finish(p JoinParams, cap *peer.Capture, st pairResponse) (store.Peer, error) {
	if st.DeviceID == "" || st.DeviceID != cap.DeviceID() {
		return store.Peer{}, proto.Errorf(proto.CodePairingFailed, "the other device's identity does not match its certificate")
	}
	addrs := []string{p.Link.Host}
	for _, a := range st.TailnetAddrs {
		if pa, err := netip.ParseAddr(a); err == nil && (p.AllowAddr == nil || p.AllowAddr(pa)) && a != p.Link.Host {
			addrs = append(addrs, pa.Unmap().String())
		}
	}
	name := st.Name
	if name == "" || len(name) > 64 {
		name = "device"
	}
	plat := st.Platform
	if plat != "windows" && plat != "android" {
		plat = "windows"
	}
	return store.Peer{
		DeviceID: st.DeviceID, Name: name, Platform: plat, CertFP: cap.FP(),
		TailnetAddrs: addrs, Port: p.Link.Port, Role: "auto", State: "paired", ApplyMode: "notify",
		PairedAt: p.Now().Unix(), VaultSync: true,
	}, nil
}

// post sends the pairing request and decodes the 202 answer or a pairing error.
func post(ctx context.Context, c *http.Client, url string, build func() pairRequest, firstTOFU bool) (pairResponse, error) {
	// With trust on first use the very first handshake is needed to learn the
	// server's fingerprint before a proof that includes it can be built.
	if firstTOFU {
		if err := probe(ctx, c, url); err != nil {
			return pairResponse{}, err
		}
	}
	body, _ := json.Marshal(build())
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return pairResponse{}, connectError(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode != http.StatusAccepted {
		return pairResponse{}, decodeError(resp.StatusCode, raw)
	}
	var out pairResponse
	if json.Unmarshal(raw, &out) != nil || out.PairID == "" {
		return pairResponse{}, proto.Errorf(proto.CodePairingFailed, "unexpected answer from the other device")
	}
	return out, nil
}

// probe makes one throw-away request so the TLS handshake records the server
// certificate (for the TOFU case where the link has no fingerprint).
func probe(ctx context.Context, c *http.Client, postURL string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(postURL, "/v1/pair")+"/v1/pair/probe", nil)
	resp, err := c.Do(req)
	if err != nil {
		return connectError(err)
	}
	resp.Body.Close()
	return nil
}

func getJSON(ctx context.Context, c *http.Client, url string, out any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := c.Do(req)
	if err != nil {
		return connectError(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	if resp.StatusCode != http.StatusOK {
		return decodeError(resp.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}

func connectError(err error) error {
	if errors.Is(err, peer.ErrFingerprint) || strings.Contains(err.Error(), peer.ErrFingerprint.Error()) {
		return proto.Errorf(proto.CodePairingFailed, "the device that answered is not the one in the pairing code")
	}
	return proto.Errorf(proto.CodePeerOffline, "cannot reach the other device: %v", err)
}

func decodeError(status int, raw []byte) error {
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error.Code != "" {
		return proto.Errorf(proto.Code(e.Error.Code), "%s", e.Error.Message)
	}
	return proto.Errorf(proto.CodePairingFailed, "the other device answered HTTP %d", status)
}
