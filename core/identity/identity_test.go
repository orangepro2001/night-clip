package identity

import (
	"crypto/x509"
	"testing"
	"time"

	"nightclip/core/proto"
)

func TestGenerateDeviceIDAndFingerprint(t *testing.T) {
	id, err := Generate("Tony-PC")
	if err != nil {
		t.Fatal(err)
	}
	if !proto.ValidDeviceID(id.DeviceID) {
		t.Fatalf("device id %q does not match protocol pattern", id.DeviceID)
	}
	cert, err := x509.ParseCertificate(id.CertDER)
	if err != nil {
		t.Fatal(err)
	}
	if got := DeviceID(cert.RawSubjectPublicKeyInfo); got != id.DeviceID {
		t.Fatalf("device id not derived from SPKI: %s vs %s", got, id.DeviceID)
	}
	if cert.NotAfter.Sub(time.Now()) < 9*365*24*time.Hour {
		t.Fatal("certificate lifetime too short")
	}
	if len(id.Fingerprint()) != 43 { // base64url(32 bytes), no padding
		t.Fatalf("unexpected fingerprint length %d", len(id.Fingerprint()))
	}
}

func TestStorageRoundTripKeepsIdentity(t *testing.T) {
	id, err := Generate("phone")
	if err != nil {
		t.Fatal(err)
	}
	pk, err := id.PrivateKeyPKCS8()
	if err != nil {
		t.Fatal(err)
	}
	key, err := ParsePrivateKeyPKCS8(pk)
	if err != nil {
		t.Fatal(err)
	}
	again, err := FromParts("phone", id.CertDER, key)
	if err != nil {
		t.Fatal(err)
	}
	if again.DeviceID != id.DeviceID || again.Fingerprint() != id.Fingerprint() {
		t.Fatal("identity changed across round trip")
	}
}

func TestFromPartsRejectsMismatchedKey(t *testing.T) {
	a, _ := Generate("a")
	b, _ := Generate("b")
	bk, _ := b.PrivateKeyPKCS8()
	key, _ := ParsePrivateKeyPKCS8(bk)
	if _, err := FromParts("a", a.CertDER, key); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestDistinctIdentitiesHaveDistinctIDs(t *testing.T) {
	a, _ := Generate("a")
	b, _ := Generate("b")
	if a.DeviceID == b.DeviceID || a.Fingerprint() == b.Fingerprint() {
		t.Fatal("collision")
	}
}
