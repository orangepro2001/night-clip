package store

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"

	"nightclip/core/proto"
)

func testKey(seed byte) []byte {
	k := make([]byte, keySize)
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
}

func openAt(t *testing.T, dir string, key []byte) *DB {
	t.Helper()
	db, err := Open(dir, key)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func openTestDB(t *testing.T) *DB {
	t.Helper()
	return openAt(t, t.TempDir(), testKey(1))
}

// errCode extracts the stable proto code, or "" for non-proto errors.
func errCode(t *testing.T, err error) string {
	t.Helper()
	var pe *proto.Error
	if errors.As(err, &pe) {
		return string(pe.Code)
	}
	return ""
}

func TestOpenCreatesSchemaAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	db := openAt(t, dir, testKey(1))
	for _, table := range []string{
		"settings", "identity", "peers", "links", "outbox", "inbox",
		"transfers", "clip_state", "revoked_fps", "schema_version",
	} {
		var n int
		if err := db.sql.QueryRow(
			`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&n); err != nil {
			t.Fatalf("look up %s: %v", table, err)
		}
		if n != 1 {
			t.Fatalf("table %s missing", table)
		}
	}
	var version int
	if err := db.sql.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Fatalf("schema version = %d, want %d", version, len(migrations))
	}

	// Reopening must not re-run or corrupt anything.
	db.Close()
	db = openAt(t, dir, testKey(1))
	if err := db.sql.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != len(migrations) {
		t.Fatalf("after reopen schema version = %d, want %d", version, len(migrations))
	}

	var mode string
	if err := db.sql.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestOpenRejectsBadKey(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33} {
		_, err := Open(t.TempDir(), make([]byte, n))
		if got := errCode(t, err); got != "INVALID" {
			t.Fatalf("len %d: code = %q (%v), want INVALID", n, got, err)
		}
	}
}

func TestOpenRejectsNewerSchema(t *testing.T) {
	dir := t.TempDir()
	db := openAt(t, dir, testKey(1))
	if _, err := db.sql.Exec(`UPDATE schema_version SET version = ?`, len(migrations)+1); err != nil {
		t.Fatal(err)
	}
	db.Close()

	_, err := Open(dir, testKey(1))
	if got := errCode(t, err); got != "DB_UNAVAILABLE" {
		t.Fatalf("code = %q (%v), want DB_UNAVAILABLE", got, err)
	}
}

func TestSealRoundTripAndTamper(t *testing.T) {
	db := openTestDB(t)
	aad := rowAAD("outbox", "envelope_enc", "evt_1")

	blob, err := db.seal([]byte("top secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("top secret")) {
		t.Fatal("sealed blob contains the plaintext")
	}
	plain, err := db.unseal(blob, aad)
	if err != nil || string(plain) != "top secret" {
		t.Fatalf("unseal = %q, %v", plain, err)
	}

	// A second seal must not reuse the nonce.
	other, err := db.seal([]byte("top secret"), aad)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(blob[:12], other[:12]) {
		t.Fatal("nonce reused")
	}

	for _, tc := range []struct {
		name string
		blob []byte
		aad  []byte
	}{
		{"flipped byte", flipLast(blob), aad},
		{"other aad", blob, rowAAD("inbox", "payload_enc", "evt_1")},
		{"truncated", blob[:len(blob)-1], aad},
		{"too short", blob[:5], aad},
	} {
		if _, err := db.unseal(tc.blob, tc.aad); !errors.Is(err, errNotAuthentic) {
			t.Fatalf("%s: err = %v, want errNotAuthentic", tc.name, err)
		}
	}
}

func flipLast(b []byte) []byte {
	out := bytes.Clone(b)
	out[len(out)-1] ^= 0xff
	return out
}

func TestConcurrentSettingsAccess(t *testing.T) {
	db := openTestDB(t)
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", i)
			for n := 0; n < 20; n++ {
				if err := db.SetSetting(key, fmt.Sprint(n)); err != nil {
					errs <- err
					return
				}
				if _, _, err := db.GetSetting(key); err != nil {
					errs <- err
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// Sanity check that the driver really is SQLite and the file exists.
func TestDatabaseFileCreated(t *testing.T) {
	dir := t.TempDir()
	db := openAt(t, dir, testKey(1))
	if db.Path() == "" {
		t.Fatal("empty path")
	}
	var v string
	if err := db.sql.QueryRow(`SELECT sqlite_version()`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v == "" {
		t.Fatal("no sqlite version")
	}
	var tx string
	if err := db.sql.QueryRow(`PRAGMA foreign_keys`).Scan(&tx); err != nil {
		t.Fatal(err)
	}
	if tx != "1" {
		t.Fatalf("foreign_keys = %q, want 1", tx)
	}
}
