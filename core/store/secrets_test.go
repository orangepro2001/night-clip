package store

import (
	"bytes"
	"testing"
)

func TestSecretsRoundTripAndAreSealed(t *testing.T) {
	dir := t.TempDir()
	db := openAt(t, dir, testKey(1))
	if _, found, err := db.GetSecret("refresh"); err != nil || found {
		t.Fatalf("empty store: found=%v err=%v", found, err)
	}
	if err := db.SetSecret("refresh", []byte("rt-one")); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSecret("refresh", []byte("rt-two")); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.GetSecret("refresh")
	if err != nil || !found || string(got) != "rt-two" {
		t.Fatalf("got %q found=%v err=%v", got, found, err)
	}
	var raw []byte
	if err := db.sql.QueryRow(`SELECT value_enc FROM secrets WHERE name = 'refresh'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("rt-two")) {
		t.Fatal("the secret is stored in the clear")
	}
	if err := db.DeleteSecret("refresh"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteSecret("refresh"); err != nil {
		t.Fatal("deleting an absent secret failed:", err)
	}
	if _, found, _ := db.GetSecret("refresh"); found {
		t.Fatal("deleted secret still there")
	}
}

func TestSecretsDoNotOpenUnderAnotherKeyOrRow(t *testing.T) {
	dir := t.TempDir()
	db := openAt(t, dir, testKey(1))
	if err := db.SetSecret("a", []byte("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSecret("b", []byte("beta")); err != nil {
		t.Fatal(err)
	}
	// a sealed value moved to another row must not open there
	if _, err := db.sql.Exec(`UPDATE secrets SET value_enc = (SELECT value_enc FROM secrets WHERE name = 'a') WHERE name = 'b'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.GetSecret("b"); errCode(t, err) != "KEY_INVALID" {
		t.Fatalf("moved row: err = %v, want KEY_INVALID", err)
	}
	db.Close()
	// the wrong key is refused when the database is opened, before any secret is read
	if other, err := Open(dir, testKey(9)); errCode(t, err) != "KEY_INVALID" {
		if other != nil {
			other.Close()
		}
		t.Fatalf("wrong key: err = %v, want KEY_INVALID", err)
	}
}

func TestSecretsHaveASizeLimit(t *testing.T) {
	db := openTestDB(t)
	if err := db.SetSecret("big", make([]byte, MaxSecretBytes+1)); errCode(t, err) != "TOO_LARGE" {
		t.Fatalf("err = %v, want TOO_LARGE", err)
	}
	if err := db.SetSecret("", []byte("x")); errCode(t, err) != "INVALID" {
		t.Fatalf("empty name: err = %v, want INVALID", err)
	}
}
