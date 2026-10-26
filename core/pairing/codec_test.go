package pairing

import (
	"strings"
	"testing"
	"time"
)

var (
	fpA = strings.Repeat("A", 43)
	fpB = strings.Repeat("B", 43)
)

func TestKnownAnswerVectors(t *testing.T) {
	// Values produced by an independent Python implementation of the same spec
	// (length-prefixed fields, HMAC-SHA256, first 20 bits of SHA-256 mod 10^6).
	psk := make([]byte, PSKBytes)
	for i := range psk {
		psk[i] = byte(i)
	}
	nonce := strings.Repeat("n", 22)
	if got := SAS(fpA, fpB, nonce); got != "314328" {
		t.Fatalf("SAS = %s", got)
	}
	if got := SAS(fpB, fpA, nonce); got != "314328" {
		t.Fatalf("SAS must not depend on who is A: %s", got)
	}
	dev := "dev_" + strings.Repeat("a", 26)
	if got := Proof(psk, dev, fpA, fpB, nonce); got != "cpeI1TGJCS1FkdhqzlcGNN3ybhqWzvyqVVZ6KGG1V_o" {
		t.Fatalf("Proof = %s", got)
	}
	if got := SessionID(psk); got != "doLqTVRqFm2N" {
		t.Fatalf("SessionID = %s", got)
	}
	if got := b64.EncodeToString(psk); got != "AAECAwQFBgcICQoLDA0ODw" {
		t.Fatalf("psk encoding = %s", got)
	}
}

func TestSASProperties(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		n := NewNonce()
		s := SAS(fpA, fpB, n)
		if len(s) != 6 || strings.Trim(s, "0123456789") != "" {
			t.Fatalf("SAS %q is not six digits", s)
		}
		seen[s] = true
	}
	if len(seen) < 150 {
		t.Fatalf("only %d distinct codes in 200 draws", len(seen))
	}
	base := SAS(fpA, fpB, "n1")
	if SAS(fpA, fpB, "n2") == base || SAS(fpA, strings.Repeat("C", 43), "n1") == base {
		t.Fatal("the code must depend on the nonce and on both fingerprints")
	}
}

func TestProofBindsEveryField(t *testing.T) {
	psk := NewPSK()
	base := Proof(psk, "dev_a", "fp1", "fp2", "nonce")
	variants := map[string]string{
		"device": Proof(psk, "dev_b", "fp1", "fp2", "nonce"),
		"client": Proof(psk, "dev_a", "fpX", "fp2", "nonce"),
		"server": Proof(psk, "dev_a", "fp1", "fpX", "nonce"),
		"nonce":  Proof(psk, "dev_a", "fp1", "fp2", "other"),
		"psk":    Proof(NewPSK(), "dev_a", "fp1", "fp2", "nonce"),
	}
	for name, v := range variants {
		if v == base {
			t.Errorf("changing the %s did not change the proof", name)
		}
	}
	// field boundaries are part of the encoding
	if Proof(psk, "ab", "c", "d", "e") == Proof(psk, "a", "bc", "d", "e") {
		t.Fatal("ambiguous concatenation: (ab,c) and (a,bc) share a proof")
	}
	if !ProofValid([]byte(base), psk, "dev_a", "fp1", "fp2", "nonce") || ProofValid([]byte(base+"x"), psk, "dev_a", "fp1", "fp2", "nonce") {
		t.Fatal("ProofValid disagrees with Proof")
	}
	if SessionID(psk) == SessionID(NewPSK()) || SessionID(psk) != SessionID(psk) {
		t.Fatal("SessionID must be deterministic per psk and differ between psks")
	}
	if strings.Contains(SessionID(psk), b64.EncodeToString(psk)[:6]) {
		t.Fatal("SessionID leaks the psk")
	}
}

func TestLinkRoundTripAndStrictParsing(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	psk := NewPSK()
	l := Link{Host: "100.101.146.117", Port: 47821, FP: fpA, PSK: psk, Exp: now.Add(5 * time.Minute)}
	got, err := ParseLink(l.String(), now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Host != l.Host || got.Port != l.Port || got.FP != l.FP || string(got.PSK) != string(psk) || !got.Exp.Equal(l.Exp) {
		t.Fatalf("round trip = %+v", got)
	}
	// without an fp (manual entry) the link is still valid
	noFP := Link{Host: "fd7a:115c:a1e0::1", Port: 1, PSK: psk, Exp: now.Add(time.Minute)}
	if got, err := ParseLink(noFP.String(), now); err != nil || got.FP != "" || got.Host != "fd7a:115c:a1e0::1" {
		t.Fatalf("no-fp link = %+v, %v", got, err)
	}

	good := l.String()
	pskS := b64.EncodeToString(psk)
	bad := map[string]string{
		"wrong scheme":      strings.Replace(good, "clipbridge://", "https://", 1),
		"wrong host":        strings.Replace(good, "//pair", "//other", 1),
		"version 2":         strings.Replace(good, "v=1", "v=2", 1),
		"no version":        strings.Replace(good, "&v=1", "", 1),
		"host not an ip":    strings.Replace(good, "100.101.146.117", "example.com", 1),
		"port 0":            strings.Replace(good, "port=47821", "port=0", 1),
		"port too big":      strings.Replace(good, "port=47821", "port=70000", 1),
		"port not a number": strings.Replace(good, "port=47821", "port=abc", 1),
		"short fp":          strings.Replace(good, "fp="+fpA, "fp="+fpA[:40], 1),
		"fp not base64url":  strings.Replace(good, "fp="+fpA, "fp="+strings.Repeat("!", 43), 1),
		"short psk":         strings.Replace(good, "psk="+pskS, "psk="+pskS[:10], 1),
		"missing psk":       strings.Replace(good, "psk="+pskS+"&", "", 1),
		"duplicate psk":     good + "&psk=" + pskS,
		"duplicate host":    good + "&host=100.64.0.1",
		"bad exp":           strings.Replace(good, "exp=", "exp=x", 1),
		"empty":             "",
		"garbage":           "not a url at all",
	}
	for name, s := range bad {
		if _, err := ParseLink(s, now); err == nil {
			t.Errorf("%s: accepted %q", name, s)
		}
	}
	// expiry is its own error code so the UI can say "ask for a new code"
	_, err = ParseLink(l.String(), now.Add(10*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "PAIRING_EXPIRED") {
		t.Fatalf("expired link err = %v", err)
	}
	// extra unknown parameters are tolerated (forward compatibility)
	if _, err := ParseLink(good+"&future=1", now); err != nil {
		t.Fatalf("unknown extra parameter rejected: %v", err)
	}
}

func TestLimiterRefillsAndIsPerKey(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	l := newLimiter(3, 10*time.Second, func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if !l.allow("a") {
			t.Fatalf("request %d refused inside the burst", i)
		}
	}
	if l.allow("a") {
		t.Fatal("4th request allowed")
	}
	if !l.allow("b") {
		t.Fatal("another source must have its own bucket")
	}
	now = now.Add(10 * time.Second)
	if !l.allow("a") || l.allow("a") {
		t.Fatal("exactly one token should have refilled after one interval")
	}
	now = now.Add(time.Hour)
	for i := 0; i < 3; i++ {
		if !l.allow("a") {
			t.Fatal("the bucket should be full again, capped at the burst")
		}
	}
	if l.allow("a") {
		t.Fatal("refill must be capped at the burst size")
	}
}
