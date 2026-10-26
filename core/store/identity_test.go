package store

import (
	"bytes"
	"testing"

	"nightclip/core/identity"
)

func TestIdentityPersistsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	key := testKey(7)

	db := openAt(t, dir, key)
	first, err := db.LoadOrCreateIdentity("Tony-PC", "windows")
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	if first.DeviceID == "" || first.Fingerprint() == "" {
		t.Fatal("empty identity fields")
	}
	db.Close()

	second, err := openAt(t, dir, key).LoadOrCreateIdentity("Tony-PC", "windows")
	if err != nil {
		t.Fatalf("load identity: %v", err)
	}
	if second.DeviceID != first.DeviceID {
		t.Fatalf("device_id changed: %q -> %q", first.DeviceID, second.DeviceID)
	}
	if second.Fingerprint() != first.Fingerprint() {
		t.Fatalf("fingerprint changed: %q -> %q", first.Fingerprint(), second.Fingerprint())
	}
	if !bytes.Equal(second.CertDER, first.CertDER) {
		t.Fatal("certificate DER changed")
	}
	if second.Name != "Tony-PC" {
		t.Fatalf("name = %q", second.Name)
	}
}

func TestIdentityPrivateKeyIsSealedAtRest(t *testing.T) {
	db := openTestDB(t)
	id, err := db.LoadOrCreateIdentity("PC", "windows")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := id.PrivateKeyPKCS8()
	if err != nil {
		t.Fatal(err)
	}
	var blob, certDER []byte
	if err := db.sql.QueryRow(`SELECT key_blob_enc, cert_der FROM identity`).Scan(&blob, &certDER); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, plain) {
		t.Fatal("stored private key blob contains the PKCS#8 plaintext")
	}
	if !bytes.Equal(certDER, id.CertDER) {
		t.Fatal("certificate DER was not stored verbatim")
	}
}

func TestIdentityWrongKeyIsRejected(t *testing.T) {
	dir := t.TempDir()
	db := openAt(t, dir, testKey(1))
	if _, err := db.LoadOrCreateIdentity("PC", "windows"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	other := openAt(t, dir, testKey(99))
	if _, err := other.LoadOrCreateIdentity("PC", "windows"); errCode(t, err) != "KEY_INVALID" {
		t.Fatalf("code = %q (%v), want KEY_INVALID", errCode(t, err), err)
	}
}

func TestIdentityPlatformMismatchIsRejected(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.LoadOrCreateIdentity("PC", "windows"); err != nil {
		t.Fatal(err)
	}
	_, err := db.LoadOrCreateIdentity("PC", "android")
	if got := errCode(t, err); got != "INVALID" {
		t.Fatalf("code = %q (%v), want INVALID", got, err)
	}
}

func TestIdentityNameIsUpdated(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.LoadOrCreateIdentity("old-name", "windows"); err != nil {
		t.Fatal(err)
	}
	id, err := db.LoadOrCreateIdentity("new-name", "windows")
	if err != nil {
		t.Fatal(err)
	}
	if id.Name != "new-name" {
		t.Fatalf("identity name = %q", id.Name)
	}
	var stored string
	if err := db.sql.QueryRow(`SELECT name FROM identity`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "new-name" {
		t.Fatalf("stored name = %q", stored)
	}
}

func TestIdentityCorruptCertificateIsRejected(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.LoadOrCreateIdentity("PC", "windows"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`UPDATE identity SET cert_der = ?`, []byte("not a certificate")); err != nil {
		t.Fatal(err)
	}
	_, err := db.LoadOrCreateIdentity("PC", "windows")
	if got := errCode(t, err); got != "DB_UNAVAILABLE" {
		t.Fatalf("code = %q (%v), want DB_UNAVAILABLE", got, err)
	}
}

func TestIdentityTruncatedKeyBlobIsRejected(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.LoadOrCreateIdentity("PC", "windows"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`UPDATE identity SET key_blob_enc = ?`, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	_, err := db.LoadOrCreateIdentity("PC", "windows")
	if got := errCode(t, err); got != "KEY_INVALID" {
		t.Fatalf("code = %q (%v), want KEY_INVALID", got, err)
	}
}

// A stored row whose certificate does not match its key must never be used.
func TestIdentityCertificateKeyMismatchIsRejected(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.LoadOrCreateIdentity("PC", "windows"); err != nil {
		t.Fatal(err)
	}
	other, err := identity.Generate("PC")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`UPDATE identity SET cert_der = ?`, other.CertDER); err != nil {
		t.Fatal(err)
	}
	_, err = db.LoadOrCreateIdentity("PC", "windows")
	if got := errCode(t, err); got != "DB_UNAVAILABLE" {
		t.Fatalf("code = %q (%v), want DB_UNAVAILABLE", got, err)
	}
}
