// Package pairing introduces two devices to each other: a one-time secret
// (psk) travels out of band (QR code or pasted link), the joiner proves it
// knows the secret over a TLS connection whose certificates it binds into the
// proof, and the user on the listening device confirms a 6-digit code that both
// sides computed independently (docs/architecture.md §3.2).
package pairing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"nightclip/core/proto"
)

const (
	proofLabel = "clipbridge-pair-v1"
	sidLabel   = "clipbridge-pair-sid-v1"
	// PSKBytes is the size of the one-time secret.
	PSKBytes = 16
)

var b64 = base64.RawURLEncoding

// Link is the content of the pairing QR code: where to connect, which
// certificate to expect, and the one-time secret.
type Link struct {
	Host string
	Port int
	FP   string // optional: certificate fingerprint of the listener (absent = trust on first use)
	PSK  []byte
	Exp  time.Time
}

// String renders clipbridge://pair?v=1&host=..&port=..&fp=..&psk=..&exp=..
func (l Link) String() string {
	q := url.Values{}
	q.Set("v", "1")
	q.Set("host", l.Host)
	q.Set("port", strconv.Itoa(l.Port))
	if l.FP != "" {
		q.Set("fp", l.FP)
	}
	q.Set("psk", b64.EncodeToString(l.PSK))
	q.Set("exp", strconv.FormatInt(l.Exp.Unix(), 10))
	return "clipbridge://pair?" + q.Encode()
}

// ParseLink is strict on purpose: it comes from a QR code or the clipboard.
// Duplicated parameters, a wrong scheme/version, a host that is not an IP
// address, a malformed fingerprint or secret and expired links are all errors.
func ParseLink(s string, now time.Time) (Link, error) {
	bad := func(format string, a ...any) (Link, error) {
		return Link{}, proto.Errorf(proto.CodeInvalid, "pairing link: "+format, a...)
	}
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme != "clipbridge" || u.Host != "pair" || u.Path != "" && u.Path != "/" {
		return bad("not a clipbridge://pair link")
	}
	q := u.Query()
	one := func(k string) (string, bool) {
		v := q[k]
		if len(v) != 1 {
			return "", false
		}
		return v[0], true
	}
	if v, ok := one("v"); !ok || v != "1" {
		return bad("unsupported version")
	}
	host, ok := one("host")
	if !ok {
		return bad("missing host")
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return bad("host must be an IP address")
	}
	ps, ok := one("port")
	port, err := strconv.Atoi(ps)
	if !ok || err != nil || port < 1 || port > 65535 {
		return bad("bad port")
	}
	l := Link{Host: addr.Unmap().String(), Port: port}
	if fp, present := q["fp"]; present {
		if len(fp) != 1 || !validFP(fp[0]) {
			return bad("bad fp")
		}
		l.FP = fp[0]
	}
	pskS, ok := one("psk")
	if !ok {
		return bad("missing psk")
	}
	l.PSK, err = b64.DecodeString(pskS)
	if err != nil || len(l.PSK) != PSKBytes {
		return bad("bad psk")
	}
	es, ok := one("exp")
	exp, err := strconv.ParseInt(es, 10, 64)
	if !ok || err != nil {
		return bad("bad exp")
	}
	l.Exp = time.Unix(exp, 0)
	if !now.Before(l.Exp) {
		return Link{}, proto.Errorf(proto.CodePairingExpired, "this pairing link has expired")
	}
	return l, nil
}

func validFP(s string) bool {
	if len(s) != 43 {
		return false
	}
	b, err := b64.DecodeString(s)
	return err == nil && len(b) == sha256.Size
}

// field writes a length-prefixed field (2-byte big-endian length), so that
// ("ab","c") and ("a","bc") can never produce the same byte string.
func field(buf []byte, s string) []byte {
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(s)))
	return append(buf, s...)
}

// Proof is what the joiner sends to show it holds the psk:
// base64url(HMAC-SHA256(psk, label || deviceID || certFP || serverFP || nonce)),
// every field length-prefixed. serverFP is the certificate fingerprint the
// joiner saw on its TLS connection, so a relay that terminates TLS with its own
// certificate produces a proof the real listener rejects.
func Proof(psk []byte, deviceID, certFP, serverFP, nonce string) string {
	m := hmac.New(sha256.New, psk)
	buf := field(nil, proofLabel)
	buf = field(buf, deviceID)
	buf = field(buf, certFP)
	buf = field(buf, serverFP)
	buf = field(buf, nonce)
	m.Write(buf)
	return b64.EncodeToString(m.Sum(nil))
}

// ProofValid compares proofs in constant time.
func ProofValid(got, psk []byte, deviceID, certFP, serverFP, nonce string) bool {
	want := Proof(psk, deviceID, certFP, serverFP, nonce)
	return hmac.Equal(got, []byte(want))
}

// ServerProof is what the listener sends back so the joiner can tell that it is
// talking to the device that owns the pairing code and not to somebody who merely
// answered on that address: base64url(HMAC-SHA256(psk, label || state || serverFP ||
// clientFP || nonce || sas || deviceID)), every field length-prefixed. Without it
// a trust-on-first-use joiner (a link without a fingerprint) could be paired with
// any impostor, because SAS inputs are all public.
func ServerProof(psk []byte, state, serverFP, clientFP, nonce, sas, deviceID string) string {
	m := hmac.New(sha256.New, psk)
	buf := field(nil, proofLabel+"-server")
	buf = field(buf, state)
	buf = field(buf, serverFP)
	buf = field(buf, clientFP)
	buf = field(buf, nonce)
	buf = field(buf, sas)
	buf = field(buf, deviceID)
	m.Write(buf)
	return b64.EncodeToString(m.Sum(nil))
}

// SessionID lets the listener find the session a request belongs to without the
// request revealing the psk. Attackers who do not know the psk cannot compute it.
func SessionID(psk []byte) string {
	m := hmac.New(sha256.New, psk)
	m.Write([]byte(sidLabel))
	return b64.EncodeToString(m.Sum(nil)[:9])
}

// SAS is the short authentication string both users compare: sha256 over the two
// certificate fingerprints in sorted order (so both sides compute the same
// value regardless of who is who) and the nonce, first 20 bits, as six decimal
// digits. A relay would have to present different certificates to the two
// sides, which makes the two codes differ.
func SAS(fpA, fpB, nonce string) string {
	lo, hi := fpA, fpB
	if lo > hi {
		lo, hi = hi, lo
	}
	buf := field(nil, proofLabel+"-sas")
	buf = field(buf, lo)
	buf = field(buf, hi)
	buf = field(buf, nonce)
	sum := sha256.Sum256(buf)
	v := uint32(sum[0])<<12 | uint32(sum[1])<<4 | uint32(sum[2])>>4 // top 20 bits
	return fmt.Sprintf("%06d", v%1_000_000)
}

// NewNonce returns 16 random bytes, base64url.
func NewNonce() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return b64.EncodeToString(b[:])
}

// NewPSK returns a fresh one-time secret.
func NewPSK() []byte {
	b := make([]byte, PSKBytes)
	_, _ = rand.Read(b)
	return b
}
