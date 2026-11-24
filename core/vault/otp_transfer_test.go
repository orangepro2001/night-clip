package vault

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

func otpTestMigration(idx, count int, kind uint64, name string, key []byte) string {
	str := func(b []byte, field protowire.Number, value []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(b, field, protowire.BytesType), value)
	}
	num := func(b []byte, field protowire.Number, value uint64) []byte {
		return protowire.AppendVarint(protowire.AppendTag(b, field, protowire.VarintType), value)
	}
	p := str(nil, 1, key)
	p = str(p, 2, []byte(name))
	p = str(p, 3, []byte("Example"))
	p = num(p, 4, 2) // SHA256
	p = num(p, 5, 2) // eight digits
	p = num(p, 6, kind)
	payload := str(nil, 1, p)
	payload = num(payload, 2, 1)
	payload = num(payload, 3, uint64(count))
	payload = num(payload, 4, uint64(idx))
	payload = num(payload, 5, 917)
	return "otpauth-migration://offline?data=" + url.QueryEscape(base64.StdEncoding.EncodeToString(payload))
}

func TestOTPTransferPreservesParametersAndEntries(t *testing.T) {
	v := newTestVault(t)
	id, err := v.AddEntry(Entry{Kind: KindLogin, Title: "Keep this password", Password: "synthetic password",
		TOTP: "otpauth://totp/Service%3Aalice%40example.test?secret=JBSWY3DPEHPK3PXP&issuer=Service&digits=7&period=45&algorithm=SHA512"})
	if err != nil {
		t.Fatal(err)
	}
	out, skipped, err := v.ExportOTP(id)
	if err != nil || len(out) != 1 || skipped != 0 {
		t.Fatalf("export count=%d skipped=%d err=%v", len(out), skipped, err)
	}
	if out[0].Title != "Service" || out[0].Username != "alice@example.test" {
		t.Fatal("account name was lost")
	}
	spec, err := ParseTOTP(out[0].Link)
	if err != nil || spec.Digits != 7 || spec.Period != 45 || spec.Algorithm != "SHA512" {
		t.Fatal("OTP parameters were lost")
	}
	r, err := v.ImportOTP([]string{out[0].Link}, "")
	if err != nil || r.Added != 0 || r.Duplicates != 1 {
		t.Fatalf("repeat import: %+v %v", r, err)
	}
	e, err := v.Get(id)
	if err != nil || e.Password != "synthetic password" {
		t.Fatal("duplicate import changed an existing login")
	}
	v2 := newTestVault(t)
	r, err = v2.ImportOTP([]string{out[0].Link}, "")
	if err != nil || r.Added != 1 {
		t.Fatalf("migration: %+v %v", r, err)
	}
	copy, _, err := v2.ExportOTP("")
	if err != nil || len(copy) != 1 {
		t.Fatal("migrated account missing")
	}
	reparsed, err := ParseTOTP(copy[0].Link)
	if err != nil {
		t.Fatal("migrated OTP cannot be parsed")
	}
	at := time.Unix(1720000000, 0)
	want, left := spec.Code(at)
	got, next := reparsed.Code(at)
	if got != want || next != left {
		t.Fatal("migrated TOTP differs")
	}
}

func TestOTPGoogleMigrationBatchAndHOTP(t *testing.T) {
	v := newTestVault(t)
	first := otpTestMigration(0, 2, 2, "Example:alice", []byte("synthetic fixture 1"))
	second := otpTestMigration(1, 2, 1, "Example:bob", []byte("synthetic fixture 2"))
	wrong := otpTestMigration(0, 2, 2, "Example:other", []byte("synthetic fixture 3"))
	if _, err := v.ImportOTP([]string{first}, ""); !errors.Is(err, ErrOTPIncomplete) {
		t.Fatalf("partial batch: %v", err)
	}
	if _, err := v.ImportOTP([]string{first, wrong, second}, ""); err == nil {
		t.Fatal("accepted conflicting QR codes")
	}
	out, _, err := v.ExportOTP("")
	if err != nil || len(out) != 0 {
		t.Fatal("partial batch was written")
	}
	r, err := v.ImportOTP([]string{second, first, first}, "")
	if err != nil || r.Added != 1 || r.Unsupported != 1 {
		t.Fatalf("complete migration: %+v %v", r, err)
	}
	out, _, err = v.ExportOTP("")
	if err != nil || len(out) != 1 || out[0].Title != "Example" || out[0].Username != "alice" ||
		!strings.Contains(out[0].Link, "digits=8") || !strings.Contains(out[0].Link, "algorithm=SHA256") {
		t.Fatal("migration was missing supported account data")
	}
}
