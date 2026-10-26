package vault

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func creds(pw string) Creds { return Creds{Password: []byte(pw)} }

func newTestVault(t *testing.T) *Vault {
	t.Helper()
	v, err := Create(filepath.Join(t.TempDir(), "v"), creds("correct horse battery staple"), KDFTest, "Test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	return v
}

func richEntry() Entry {
	return Entry{
		Kind: KindAPIKey, Title: "OpenAI prod", Username: "svc-prod", Password: "sk-live-ÄÖ 密钥 \"quoted\" <&>", URL: "https://api.example.com/v1",
		Notes: "line1\nline2\r\nline3", Tags: []string{"prod", "ai"},
		Fields: []Field{{Name: "Key ID", Value: "key_123", Protected: false}, {Name: "Signing secret", Value: "whsec_abc", Protected: true}},
		TOTP:   "otpauth://totp/Ex:me?secret=JBSWY3DPEHPK3PXP&issuer=Ex",
	}
}

func TestCreateOpenRoundTrip(t *testing.T) {
	v := newTestVault(t)
	dir := v.Dir()
	gid, err := v.AddGroup("", "Work")
	if err != nil {
		t.Fatal(err)
	}
	e := richEntry()
	e.GroupID = gid
	e.ExpiresAt = time.Now().Add(48 * time.Hour).Unix()
	id, err := v.AddEntry(e)
	if err != nil {
		t.Fatal(err)
	}
	v.Close()
	if _, err := v.Get(id); !errors.Is(err, ErrLocked) {
		t.Fatalf("closed vault: %v", err)
	}

	v2, err := Open(dir, creds("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()
	got, err := v2.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != e.Title || got.Password != e.Password || got.Notes != e.Notes || got.URL != e.URL || got.Username != e.Username ||
		got.TOTP != e.TOTP || got.Kind != KindAPIKey || got.GroupID != gid || got.ExpiresAt != e.ExpiresAt {
		t.Fatalf("round trip lost data:\n got %+v\nwant %+v", got, e)
	}
	if strings.Join(got.Tags, ",") != "ai,prod" || len(got.Fields) != 2 || got.Fields[1].Name != "Signing secret" || !got.Fields[1].Protected || got.Fields[0].Protected {
		t.Fatalf("tags/fields: %+v %+v", got.Tags, got.Fields)
	}
	groups, _ := v2.Groups()
	if len(groups) != 2 || groups[1].Path != "Test / Work" || groups[1].Entries != 1 {
		t.Fatalf("groups = %+v", groups)
	}
}

func TestWrongCredentialsAndDamageLookTheSame(t *testing.T) {
	v := newTestVault(t)
	dir := v.Dir()
	v.Close()
	if _, err := Open(dir, creds("wrong")); !errors.Is(err, ErrWrongCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := Open(dir, Creds{Password: []byte("correct horse battery staple"), KeyFile: []byte("not the key")}); !errors.Is(err, ErrWrongCredentials) {
		t.Fatalf("unexpected key file: %v", err)
	}
	if _, err := Open(dir, Creds{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty password: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	for _, cut := range []int{0, 5, 100, len(data) / 2, len(data) - 40} {
		_ = os.WriteFile(filepath.Join(dir, FileName), data[:cut], 0o600)
		if _, err := Open(dir, creds("correct horse battery staple")); err == nil {
			t.Fatalf("truncated to %d bytes opened", cut)
		}
	}
	flipped := bytes.Clone(data)
	flipped[len(flipped)/2] ^= 0x40
	_ = os.WriteFile(filepath.Join(dir, FileName), flipped, 0o600)
	if _, err := Open(dir, creds("correct horse battery staple")); !errors.Is(err, ErrWrongCredentials) {
		t.Fatalf("flipped byte: %v", err)
	}
	if _, err := Open(filepath.Join(dir, "nothing-here"), creds("x")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := Create(dir, creds("x"), KDFTest, ""); !errors.Is(err, ErrExists) {
		t.Fatalf("create over existing: %v", err)
	}
}

// Every save must use fresh random values: the same (key, IV) pair twice under ChaCha20 would leak
// the XOR of two plaintexts (docs/vault-v0-poc-report.md, R5).
func TestEverySaveUsesFreshRandomValues(t *testing.T) {
	v := newTestVault(t)
	id, err := v.AddEntry(Entry{Title: "a", Password: "secret-A"})
	if err != nil {
		t.Fatal(err)
	}
	read := func() []byte { b, _ := os.ReadFile(filepath.Join(v.Dir(), FileName)); return b }
	first := read()
	if err := v.UpdateEntry(id, Entry{Title: "a", Password: "secret-B"}); err != nil {
		t.Fatal(err)
	}
	second := read()
	// signature (12 bytes) + the first header fields hold cipher id, compression, master seed, KDF parameters (with salt), IV
	const hdr = 160
	if bytes.Equal(first[12:hdr], second[12:hdr]) {
		t.Fatal("header random values repeated across saves")
	}
	// the master seed itself: find it by parsing the header fields (id 4)
	if seed(first) == seed(second) {
		t.Fatal("master seed repeated across saves")
	}
	if iv(first) == iv(second) {
		t.Fatal("encryption IV repeated across saves")
	}
}

func headerField(f []byte, want byte) string {
	p := 12
	for p+5 <= len(f) {
		id := f[p]
		n := int(f[p+1]) | int(f[p+2])<<8 | int(f[p+3])<<16 | int(f[p+4])<<24
		p += 5
		if id == want {
			return string(f[p : p+n])
		}
		if id == 0 {
			break
		}
		p += n
	}
	return ""
}
func seed(f []byte) string { return headerField(f, 4) }
func iv(f []byte) string   { return headerField(f, 7) }

func TestSaveKeepsPreviousGenerationAndSnapshots(t *testing.T) {
	v := newTestVault(t)
	clock := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	v.now = func() time.Time { return clock }
	id, _ := v.AddEntry(Entry{Title: "one", Password: "p1"})
	clock = clock.Add(time.Minute)
	if err := v.UpdateEntry(id, Entry{Title: "one", Password: "p2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(v.Dir(), PrevName)); err != nil {
		t.Fatal("no .prev generation")
	}
	list := ListBackups(v.Dir())
	if len(list) < 2 {
		t.Fatalf("backups = %+v", list)
	}
	// the newest snapshot is the state before the last change
	var snap string
	for _, b := range list {
		if b.Reason == "save" {
			snap = b.Name
			break
		}
	}
	chk, err := VerifyBackup(v.Dir(), snap, creds("correct horse battery staple"))
	if err != nil || chk.Entries != 1 {
		t.Fatalf("VerifyBackup = %+v, %v", chk, err)
	}
	if _, err := VerifyBackup(v.Dir(), "../../etc/passwd", creds("x")); err == nil {
		t.Fatal("path traversal accepted as a backup name")
	}
	if _, err := VerifyBackup(v.Dir(), snap, creds("nope")); !errors.Is(err, ErrWrongCredentials) {
		t.Fatalf("backup with wrong password: %v", err)
	}
}

func TestHistoryTrashAndPermanentDelete(t *testing.T) {
	v := newTestVault(t)
	id, _ := v.AddEntry(Entry{Title: "site", Username: "me", Password: "v1"})
	for _, pw := range []string{"v2", "v3"} {
		if err := v.UpdateEntry(id, Entry{Title: "site", Username: "me", Password: pw}); err != nil {
			t.Fatal(err)
		}
	}
	// saving identical content adds nothing
	if err := v.UpdateEntry(id, Entry{Title: "site", Username: "me", Password: "v3"}); err != nil {
		t.Fatal(err)
	}
	h, _ := v.History(id)
	if len(h) != 2 {
		t.Fatalf("history = %+v", h)
	}
	if err := v.RestoreVersion(id, 0); err != nil {
		t.Fatal(err)
	}
	if e, _ := v.Get(id); e.Password != "v1" || e.History != 3 {
		t.Fatalf("after restore: %+v", e)
	}

	perm, err := v.Delete(id)
	if err != nil || perm {
		t.Fatalf("first delete: %v %v", perm, err)
	}
	if l, _ := v.List(Filter{}); len(l) != 0 {
		t.Fatalf("trashed entry listed: %+v", l)
	}
	if l, _ := v.List(Filter{OnlyTrash: true}); len(l) != 1 || !l[0].InTrash {
		t.Fatalf("trash = %+v", l)
	}
	if err := v.RestoreEntry(id); err != nil {
		t.Fatal(err)
	}
	if l, _ := v.List(Filter{}); len(l) != 1 {
		t.Fatalf("restored entry missing: %+v", l)
	}
	_, _ = v.Delete(id)
	if perm, _ := v.Delete(id); !perm {
		t.Fatal("second delete must be permanent")
	}
	if _, err := v.Get(id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after purge: %v", err)
	}
	if len(v.db.Content.Root.DeletedObjects) != 1 {
		t.Fatalf("deletion not recorded for sync: %+v", v.db.Content.Root.DeletedObjects)
	}
}

func TestAttachments(t *testing.T) {
	v := newTestVault(t)
	id, _ := v.AddEntry(Entry{Title: "with file"})
	data := bytes.Repeat([]byte{0xAB, 0x00, 0xFF}, 100000)
	if err := v.AddAttachment(id, "key.pem", data); err != nil {
		t.Fatal(err)
	}
	if err := v.AddAttachment(id, "../evil", data); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad name: %v", err)
	}
	if err := v.AddAttachment(id, "big", make([]byte, MaxAttachmentBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too big: %v", err)
	}
	dir := v.Dir()
	v.Close()
	v2, err := Open(dir, creds("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()
	got, err := v2.Attachment(id, "key.pem")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("attachment lost: %v (%d bytes)", err, len(got))
	}
	if e, _ := v2.Get(id); len(e.Attachments) != 1 || e.Attachments[0].Size != int64(len(data)) {
		t.Fatalf("attachment info = %+v", e.Attachments)
	}
	if err := v2.RemoveAttachment(id, "key.pem"); err != nil {
		t.Fatal(err)
	}
	if _, err := v2.Attachment(id, "key.pem"); !errors.Is(err, ErrNotFound) {
		t.Fatal("attachment still there")
	}
}

func TestSearchFilterAndValidation(t *testing.T) {
	v := newTestVault(t)
	_, _ = v.AddEntry(Entry{Title: "GitHub", Username: "tony", URL: "https://github.com", Tags: []string{"dev"}})
	_, _ = v.AddEntry(Entry{Kind: KindAPIKey, Title: "Stripe", Password: "sk_test_1", Fields: []Field{{Name: "Account", Value: "acct_9", Protected: false}, {Name: "Hidden", Value: "findme-secret", Protected: true}}})
	_, _ = v.AddEntry(Entry{Kind: KindNote, Title: "Wifi", Notes: "router password in the drawer"})
	count := func(f Filter) int { l, _ := v.List(f); return len(l) }
	if count(Filter{Query: "git"}) != 1 || count(Filter{Query: "ACCT_9"}) != 1 || count(Filter{Query: "drawer"}) != 1 || count(Filter{Kind: KindAPIKey}) != 1 || count(Filter{Tag: "DEV"}) != 1 {
		t.Fatal("search/filter wrong")
	}
	if count(Filter{Query: "findme-secret"}) != 0 || count(Filter{Query: "sk_test_1"}) != 0 {
		t.Fatal("search must never look into protected values")
	}
	for _, bad := range []Entry{
		{Title: ""}, {Title: "x", Kind: "weird"}, {Title: "x", Fields: []Field{{Name: "password", Value: "y"}}},
		{Title: "x", Fields: []Field{{Name: "a"}, {Name: "A"}}}, {Title: strings.Repeat("a", MaxFieldBytes+1)},
	} {
		if _, err := v.AddEntry(bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if _, err := v.AddEntry(Entry{Title: "x", GroupID: "00000000000000000000000000000000"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown group: %v", err)
	}
}

func TestChangeCredentials(t *testing.T) {
	v := newTestVault(t)
	dir := v.Dir()
	id, _ := v.AddEntry(Entry{Title: "t", Password: "p"})
	if !v.VerifyCredentials(creds("correct horse battery staple")) || v.VerifyCredentials(creds("nope")) {
		t.Fatal("VerifyCredentials")
	}
	if err := v.ChangeCredentials(Creds{Password: []byte("new master"), KeyFile: []byte("some key file bytes")}, KDF{}); err != nil {
		t.Fatal(err)
	}
	v.Close()
	if _, err := Open(dir, creds("correct horse battery staple")); !errors.Is(err, ErrWrongCredentials) {
		t.Fatal("old password still works")
	}
	v2, err := Open(dir, Creds{Password: []byte("new master"), KeyFile: []byte("some key file bytes")})
	if err != nil {
		t.Fatal(err)
	}
	defer v2.Close()
	if e, err := v2.Get(id); err != nil || e.Password != "p" {
		t.Fatalf("after rekey: %+v %v", e, err)
	}
	// the snapshot taken before the re-key still opens with the OLD password
	var old string
	for _, b := range ListBackups(dir) {
		if b.Reason == "rekey" {
			old = b.Name
		}
	}
	if old == "" {
		t.Fatal("no snapshot before the re-key")
	}
	if _, err := VerifyBackup(dir, old, creds("correct horse battery staple")); err != nil {
		t.Fatalf("pre-rekey snapshot: %v", err)
	}
}

// An attacker without the key can still write any KDF parameters into a header and fix up its
// SHA-256 (only the HMAC after it needs the key, and that is checked after the key derivation).
func TestLimitsRejectHostileKDFParameters(t *testing.T) {
	v := newTestVault(t)
	dir := v.Dir()
	v.Close()
	orig, _ := os.ReadFile(filepath.Join(dir, FileName))

	// header layout (KDBX 4): 12-byte signature, then id(1) len(4) data fields until id 0; then SHA-256(header)
	end := 12
	for {
		id := orig[end]
		n := int(orig[end+1]) | int(orig[end+2])<<8 | int(orig[end+3])<<16 | int(orig[end+4])<<24
		end += 5 + n
		if id == 0 {
			break
		}
	}
	patch := func(item byte, key string, value uint64) []byte {
		data := bytes.Clone(orig)
		p := 12
		for {
			id := data[p]
			n := int(data[p+1]) | int(data[p+2])<<8 | int(data[p+3])<<16 | int(data[p+4])<<24
			p += 5
			if id == 11 { // KDF parameters: a variant dictionary of type(1) keylen(4) key vallen(4) val
				q := p + 2 // version
				for data[q] != 0 {
					typ := data[q]
					kl := int(data[q+1]) | int(data[q+2])<<8
					k := string(data[q+5 : q+5+kl])
					vl := int(data[q+5+kl]) | int(data[q+6+kl])<<8
					vo := q + 5 + kl + 4
					if k == key && typ == item {
						for b := 0; b < vl; b++ {
							data[vo+b] = byte(value >> (8 * b))
						}
					}
					q = vo + vl
				}
				break
			}
			p += n
		}
		sum := sha256sum(data[:end])
		copy(data[end:end+32], sum[:])
		return data
	}
	cases := map[string][]byte{
		"4 GiB memory":      patch(0x05, "M", 4<<30),
		"2^32-1 iterations": patch(0x05, "I", 1<<32-1),
		"512 lanes":         patch(0x04, "P", 512),
	}
	for name, data := range cases {
		_ = os.WriteFile(filepath.Join(dir, FileName), data, 0o600)
		start := time.Now()
		_, err := Open(dir, creds("correct horse battery staple"))
		if time.Since(start) > 3*time.Second {
			t.Errorf("%s: took %s, the cost was not refused up front", name, time.Since(start))
		}
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: err = %v, want ErrUnsupported", name, err)
		}
	}
}

func sha256sum(b []byte) [32]byte { return fingerprint(b) }

func TestMergeGuardedStopsUnrelatedVaultsAndMassDeletion(t *testing.T) {
	a, clock := baseVault(t)
	var ids []string
	for i := 0; i < 8; i++ {
		id, _ := a.AddEntry(Entry{Title: fmt.Sprintf("e%d", i), Password: "p"})
		ids = append(ids, id)
	}
	b := fork(t, a, clock)
	for _, id := range ids[:6] { // b deletes six entries for good
		_, _ = b.Delete(id)
		_, _ = b.Delete(id)
	}
	bFile, _ := os.ReadFile(b.path())
	if _, err := a.MergeGuarded(bFile, Creds{}, "sync", MergeOptions{RequireSameVault: true, MaxRemoved: 5}); !errors.Is(err, ErrReview) {
		t.Fatalf("mass deletion: %v", err)
	}
	if l, _ := a.List(Filter{}); len(l) != 8 {
		t.Fatalf("a refused merge changed the vault: %d entries", len(l))
	}
	if st, err := a.MergeGuarded(bFile, Creds{}, "sync", MergeOptions{RequireSameVault: true}); err != nil || st.EntriesRemoved != 6 {
		t.Fatalf("approved merge: %+v %v", st, err)
	}

	other, _ := Create(filepath.Join(t.TempDir(), "o"), creds("correct horse battery staple"), KDFTest, "Other")
	defer other.Close()
	_, _ = other.AddEntry(Entry{Title: "foreign", Password: "x"})
	oFile, _ := os.ReadFile(other.path())
	if _, err := a.MergeGuarded(oFile, Creds{}, "sync", MergeOptions{RequireSameVault: true}); !errors.Is(err, ErrDifferentVault) {
		t.Fatalf("unrelated vault: %v", err)
	}
	if st, err := a.MergeGuarded(oFile, Creds{}, "sync", MergeOptions{}); err != nil || st.EntriesAdded != 1 {
		t.Fatalf("explicit union: %+v %v", st, err)
	}
	h, size, ok := FileHash(a.Dir())
	if !ok || len(h) != 64 || size <= 0 {
		t.Fatalf("FileHash = %q %d %v", h, size, ok)
	}
}
