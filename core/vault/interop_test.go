package vault

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Interoperability with real KeePassXC (keepassxc-cli). Skipped when it is not installed:
// the plan's V1 gate is that KeePassXC opens, edits and re-saves our files and the reverse.

func kpcli(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("keepassxc-cli", args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		return out.String() + errb.String(), err
	}
	return out.String(), nil
}

func needKeePassXC(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("keepassxc-cli"); err != nil {
		t.Skip("keepassxc-cli not installed")
	}
}

const interopPW = "interop pässwörd 密码"

func TestKeePassXCReadsOurFile(t *testing.T) {
	needKeePassXC(t)
	dir := t.TempDir()
	v, err := Create(dir, creds(interopPW), KDFStandard, "Interop")
	if err != nil {
		t.Fatal(err)
	}
	gid, _ := v.AddGroup("", "Work")
	e := richEntry()
	e.GroupID = gid
	id, err := v.AddEntry(e)
	if err != nil {
		t.Fatal(err)
	}
	_ = v.AddAttachment(id, "note.txt", []byte("attached text"))
	_, _ = v.AddEntry(Entry{Title: "secret thing", Kind: KindSecret, Password: "pin-1234"})
	// edit twice so there is history, then trash one
	_ = v.UpdateEntry(id, withPassword(e, "second"))
	_ = v.UpdateEntry(id, withPassword(e, "third"))
	tid, _ := v.AddEntry(Entry{Title: "to trash", Password: "x"})
	_, _ = v.Delete(tid)
	v.Close()
	db := filepath.Join(dir, FileName)

	out, err := kpcli(t, interopPW+"\n", "ls", "-R", db)
	if err != nil {
		t.Fatalf("keepassxc-cli ls: %v\n%s", err, out)
	}
	for _, want := range []string{"Work/", "OpenAI prod", "secret thing", "Recycle Bin/", "to trash"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls misses %q:\n%s", want, out)
		}
	}
	out, err = kpcli(t, interopPW+"\n", "show", "-s", "--all", "--show-attachments", db, "Work/OpenAI prod")
	if err != nil {
		t.Fatalf("show: %v\n%s", err, out)
	}
	for _, want := range []string{"UserName: svc-prod", "Password: third", "URL: https://api.example.com/v1", "Notes: line1", "Key ID: key_123", "Signing secret: whsec_abc", "Tags: ai,prod", "note.txt"} {
		if !strings.Contains(out, want) {
			t.Errorf("show misses %q:\n%s", want, out)
		}
	}
	// KeePassXC computes the TOTP from our "otp" field
	out, err = kpcli(t, interopPW+"\n", "show", "--totp", db, "Work/OpenAI prod")
	if err != nil || !regexpDigits(out) {
		t.Fatalf("totp: %v %q", err, out)
	}
	exported := filepath.Join(t.TempDir(), "out.txt")
	if out, err := kpcli(t, interopPW+"\n", "attachment-export", db, "Work/OpenAI prod", "note.txt", exported); err != nil {
		t.Fatalf("attachment-export: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(exported); string(b) != "attached text" {
		t.Fatalf("attachment = %q", b)
	}
	if out, err := kpcli(t, interopPW+"\n", "db-info", db); err != nil || !strings.Contains(out, "Cipher") {
		t.Fatalf("db-info: %v\n%s", err, out)
	}
}

func withPassword(e Entry, pw string) Entry { e.Password = pw; return e }

func regexpDigits(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 6 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func TestKeePassXCEditsAndWeReadItBack(t *testing.T) {
	needKeePassXC(t)
	dir := t.TempDir()
	v, err := Create(dir, creds(interopPW), KDFLow, "Interop")
	if err != nil {
		t.Fatal(err)
	}
	id, _ := v.AddEntry(Entry{Title: "ours", Username: "u", Password: "p", Kind: KindAPIKey})
	v.Close()
	db := filepath.Join(dir, FileName)

	// KeePassXC adds an entry (db password, then entry password on stdin), edits ours, and trashes one
	if out, err := kpcli(t, interopPW+"\nentry-pw-1\n", "add", "-u", "kp-user", "-p", "--url", "https://kp.example", db, "Added by KeePassXC"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	if out, err := kpcli(t, interopPW+"\n", "edit", "-u", "edited-user", "--notes", "edited by xc", db, "ours"); err != nil {
		t.Fatalf("edit: %v\n%s", err, out)
	}
	v2, err := Open(dir, creds(interopPW))
	if err != nil {
		t.Fatalf("we cannot open what KeePassXC saved: %v", err)
	}
	l, _ := v2.List(Filter{})
	if len(l) != 2 {
		t.Fatalf("entries = %+v", l)
	}
	e, _ := v2.Get(id)
	if e.Username != "edited-user" || e.Notes != "edited by xc" || e.Kind != KindAPIKey || e.Password != "p" {
		t.Fatalf("KeePassXC's edit: %+v", e)
	}
	var added Entry
	for _, s := range l {
		if s.Title == "Added by KeePassXC" {
			added, _ = v2.Get(s.ID)
		}
	}
	if added.Username != "kp-user" || added.Password != "entry-pw-1" || added.URL != "https://kp.example" {
		t.Fatalf("entry added by KeePassXC: %+v", added)
	}
	// we save it again (fresh seeds, KeePassXC's data kept) and KeePassXC still opens it
	if err := v2.UpdateEntry(id, withPassword(e, "p2")); err != nil {
		t.Fatal(err)
	}
	v2.Close()
	out, err := kpcli(t, interopPW+"\n", "show", "-s", db, "ours")
	if err != nil || !strings.Contains(out, "Password: p2") || !strings.Contains(out, "UserName: edited-user") {
		t.Fatalf("KeePassXC after our re-save: %v\n%s", err, out)
	}
	if out, err := kpcli(t, interopPW+"\n", "rm", db, "Added by KeePassXC"); err != nil { // moves to the recycle bin
		t.Fatalf("rm: %v\n%s", err, out)
	}
	v3, err := Open(dir, creds(interopPW))
	if err != nil {
		t.Fatal(err)
	}
	defer v3.Close()
	if l, _ := v3.List(Filter{}); len(l) != 1 {
		t.Fatalf("after KeePassXC rm: %+v", l)
	}
	if l, _ := v3.List(Filter{OnlyTrash: true}); len(l) != 1 || l[0].Title != "Added by KeePassXC" {
		t.Fatalf("KeePassXC's recycle bin not recognised: %+v", l)
	}
}

func TestWeOpenAKeePassXCCreatedDatabase(t *testing.T) {
	needKeePassXC(t)
	dir := t.TempDir()
	db := filepath.Join(dir, FileName)
	if out, err := kpcli(t, interopPW+"\n"+interopPW+"\n", "db-create", "-p", db); err != nil {
		t.Fatalf("db-create: %v\n%s", err, out)
	}
	if out, err := kpcli(t, interopPW+"\npw-from-xc\n", "add", "-u", "xc-user", "-p", db, "Made by XC"); err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}
	att := filepath.Join(t.TempDir(), "att.bin")
	_ = os.WriteFile(att, []byte("kdbx3 attachment"), 0o600)
	if out, err := kpcli(t, interopPW+"\n", "attachment-import", db, "Made by XC", "att.bin", att); err != nil {
		t.Fatalf("attachment-import: %v\n%s", err, out)
	}
	if h, _ := os.ReadFile(db); h[10] != 3 { // KeePassXC's default is still KDBX 3.1
		t.Skipf("this KeePassXC already creates KDBX %d; the 3.x upgrade path is not exercised", h[10])
	}
	v, err := Open(dir, creds(interopPW))
	if err != nil {
		t.Fatalf("KeePassXC-created database: %v", err)
	}
	if h, _ := os.ReadFile(db); h[10] != 4 {
		t.Fatalf("a KDBX 3 file must be written back as KDBX 4, major version is %d", h[10])
	}
	upgraded := false
	for _, b := range ListBackups(dir) {
		if b.Reason == "upgrade" {
			upgraded = true
		}
	}
	if !upgraded {
		t.Fatal("no snapshot of the original KDBX 3 file")
	}
	if a, err := v.Attachment(firstID(t, v), "att.bin"); err != nil || string(a) != "kdbx3 attachment" {
		t.Fatalf("attachment through the upgrade: %q %v", a, err)
	}
	l, _ := v.List(Filter{})
	if len(l) != 1 || l[0].Title != "Made by XC" {
		t.Fatalf("entries: %+v", l)
	}
	e, _ := v.Get(l[0].ID)
	if e.Password != "pw-from-xc" || e.Username != "xc-user" || e.Kind != KindLogin {
		t.Fatalf("entry: %+v", e)
	}
	id, _ := v.AddEntry(Entry{Title: "ours", Password: "x"})
	_ = id
	v.Close()
	if out, err := kpcli(t, interopPW+"\n", "ls", db); err != nil || !strings.Contains(out, "ours") || !strings.Contains(out, "Made by XC") {
		t.Fatalf("KeePassXC after our save: %v\n%s", err, out)
	}
}

func firstID(t *testing.T, v *Vault) string {
	t.Helper()
	l, _ := v.List(Filter{})
	if len(l) == 0 {
		t.Fatal("no entries")
	}
	return l[0].ID
}
