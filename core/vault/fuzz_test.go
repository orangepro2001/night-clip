package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The parsers that read data from outside: the database file (also arriving from other devices and
// from backups) and imported CSV. Run longer with: go test -fuzz=FuzzDecode -fuzztime=5m ./vault

func seedFile(f *testing.F) []byte {
	dir := f.TempDir()
	v, err := Create(dir, creds("fuzz password"), KDFTest, "Fuzz")
	if err != nil {
		f.Fatal(err)
	}
	_, _ = v.AddEntry(Entry{Title: "t", Username: "u", Password: "p", Notes: "n", TOTP: "JBSWY3DPEHPK3PXP", Fields: []Field{{Name: "k", Value: "v", Protected: true}}})
	id, _ := v.AddEntry(Entry{Title: "att"})
	_ = v.AddAttachment(id, "a.bin", []byte("attachment"))
	_, _ = v.Delete(id)
	v.Close()
	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		f.Fatal(err)
	}
	return b
}

func FuzzDecode(f *testing.F) {
	seed := seedFile(f)
	f.Add(seed)
	f.Add(seed[:len(seed)/2])
	f.Add(append(bytes.Clone(seed), 0, 0, 0))
	f.Add([]byte{0x03, 0xd9, 0xa2, 0x9a, 0x67, 0xfb, 0x4b, 0xb5, 0, 0, 4, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		db, err := decode(data, creds("fuzz password"))
		if err == nil && db == nil {
			t.Fatal("no error and no database")
		}
	})
}

// A file that passes the header checks but whose payload is attacker-chosen: only possible with
// the key, i.e. from a paired device that knows the master password. Mutations of the valid file
// that keep the HMAC valid cannot be made by a fuzzer, so this fuzzes the XML/inner structure by
// feeding mutated plaintext structures through our own writer instead.
func FuzzEntryRoundTrip(f *testing.F) {
	f.Add("title", "user", "pw", "https://x", "notes", "a;b", "field", "value", true, "JBSWY3DPEHPK3PXP")
	f.Add("<&>\"'", "\x00\x01", "日本語🔐", "", "\r\n", ";;,,", "Title", "x", false, "otpauth://totp/x?secret=AAAAAAAAAAAAAAAA")
	f.Fuzz(func(t *testing.T, title, user, pw, url, notes, tags, fname, fval string, prot bool, totp string) {
		v, err := Create(t.TempDir(), creds("fuzz password"), KDFTest, "F")
		if err != nil {
			t.Fatal(err)
		}
		defer v.Close()
		e := Entry{Title: title, Username: user, Password: pw, URL: url, Notes: notes, Tags: []string{tags}, TOTP: totp,
			Fields: []Field{{Name: fname, Value: fval, Protected: prot}}}
		id, err := v.AddEntry(e)
		if err != nil {
			return // rejected input is fine
		}
		got, err := v.Get(id)
		if err != nil {
			t.Fatalf("stored but unreadable: %v", err)
		}
		if got.Title != title || got.Password != pw && !xmlLossy(pw) || got.URL != url && !xmlLossy(url) {
			// XML cannot carry every control character; only compare when the input is clean
			if !xmlLossy(title + pw + url) {
				t.Fatalf("round trip changed the entry: %+v", got)
			}
		}
		_, _, _ = v.TOTP(id) // must not panic on any spec string
		// and the file must still open
		dir := v.Dir()
		v.Close()
		w, err := Open(dir, creds("fuzz password"))
		if err != nil {
			t.Fatalf("our own file does not reopen: %v", err)
		}
		w.Close()
	})
}

// xmlLossy reports whether s contains characters XML 1.0 cannot represent (the KDBX XML layer drops or
// replaces them), so an exact round trip is not expected.
func xmlLossy(s string) bool {
	for _, r := range s {
		if r == 0 || r < 0x20 && r != '\t' && r != '\n' && r != '\r' || r == 0xFFFE || r == 0xFFFF || r == 0xFFFD || r >= 0xD800 && r <= 0xDFFF {
			return true
		}
	}
	return !validUTF8(s)
}

func validUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

func FuzzImportCSV(f *testing.F) {
	f.Add("Title,Username,Password\nA,b,c\n")
	f.Add("name,url,username,password,note\n\"x,\"\"y\",https://z,u,p,\"multi\nline\"\n")
	f.Add("\ufefffolder,login_uri,login_username,login_password,login_totp\nA/B/C,http://x,u,p,otpauth://totp/x?secret=JBSWY3DPEHPK3PXP\n")
	f.Fuzz(func(t *testing.T, data string) {
		v, err := Create(t.TempDir(), creds("fuzz password"), KDFTest, "F")
		if err != nil {
			t.Fatal(err)
		}
		defer v.Close()
		_, _, _ = v.ImportCSV(bytes.NewReader([]byte(data)))
	})
}
