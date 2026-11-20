package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nightclip/core/sshkey"
)

const testKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nnot parsed by the vault package\n-----END OPENSSH PRIVATE KEY-----\n"

func addSSHEntry(t *testing.T, v *Vault, title string) string {
	t.Helper()
	id, err := v.AddEntryWithAttachments(Entry{Kind: KindSSH, Title: title}, []NamedData{
		{Name: "id_ed25519", Data: []byte(testKey)},
		{Name: sshkey.SettingsAttachment, Data: sshkey.NewAgentSettings("id_ed25519", true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestAddEntryWithAttachmentsIsOneSaveWithoutHistory(t *testing.T) {
	v := newTestVault(t)
	id := addSSHEntry(t, v, "GitHub")
	e, err := v.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if e.Kind != KindSSH || e.History != 0 || len(e.Attachments) != 2 {
		t.Fatalf("%+v", e)
	}
	if _, err := v.AddEntryWithAttachments(Entry{Title: "dup"}, []NamedData{{Name: "a", Data: nil}, {Name: "a"}}); err == nil {
		t.Fatal("duplicate attachment names accepted")
	}
	if _, err := v.AddEntryWithAttachments(Entry{Title: "bad"}, []NamedData{{Name: "../x"}}); err == nil {
		t.Fatal("bad attachment name accepted")
	}
}

func TestKeeAgentEntryWithoutKindReadsAsSSH(t *testing.T) {
	v := newTestVault(t)
	id := addSSHEntry(t, v, "From KeePassXC")
	// KeePassXC never writes ClipBridge.Kind; an older ClipBridge writes "login".
	for _, kind := range []string{"", KindLogin} {
		v.mu.Lock()
		le, _, _, _ := v.findEntry(id)
		if kind == "" {
			delCustom(le, cdKind)
		} else {
			setCustom(le, cdKind, kind)
		}
		v.mu.Unlock()
		e, _ := v.Get(id)
		if e.Kind != KindSSH {
			t.Fatalf("kind %q read as %q", kind, e.Kind)
		}
	}
	// A plain login with an unrelated attachment stays a login.
	lid, _ := v.AddEntryWithAttachments(Entry{Title: "site"}, []NamedData{{Name: "note.txt", Data: []byte("x")}})
	if e, _ := v.Get(lid); e.Kind != KindLogin {
		t.Fatalf("login read as %q", e.Kind)
	}
	// Explicit kinds other than login still win over the attachment.
	v.mu.Lock()
	le, _, _, _ := v.findEntry(id)
	setCustom(le, cdKind, KindSecret)
	v.mu.Unlock()
	if e, _ := v.Get(id); e.Kind != KindSecret {
		t.Fatalf("explicit kind lost: %q", e.Kind)
	}
}

func TestUpdateKeepsSSHKindWhenClientSendsLogin(t *testing.T) {
	v := newTestVault(t)
	id := addSSHEntry(t, v, "GitHub")
	e, _ := v.Get(id)
	e.Kind = KindLogin
	e.Notes = "edited by an old client"
	if err := v.UpdateEntry(id, e); err != nil {
		t.Fatal(err)
	}
	got, _ := v.Get(id)
	if got.Kind != KindSSH || got.Notes != e.Notes || len(got.Attachments) != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestSSHCandidatesReturnCopies(t *testing.T) {
	v := newTestVault(t)
	id := addSSHEntry(t, v, "GitHub")
	_, _ = v.AddEntry(Entry{Title: "not ssh", Password: "p"})
	cs, err := v.SSHCandidates("")
	if err != nil || len(cs) != 1 {
		t.Fatalf("%v %v", cs, err)
	}
	c := cs[0]
	if c.ID != id || c.KeyName != "id_ed25519" || string(c.Key) != testKey || c.Settings == nil || c.InTrash {
		t.Fatalf("%+v", c)
	}
	for i := range c.Key {
		c.Key[i] = 0
	}
	if b, _ := v.Attachment(id, "id_ed25519"); string(b) != testKey {
		t.Fatal("candidate aliases the vault's buffer")
	}
	if _, err := v.Delete(id); err != nil {
		t.Fatal(err)
	}
	cs, _ = v.SSHCandidates(id)
	if len(cs) != 1 || !cs[0].InTrash {
		t.Fatalf("trash: %+v", cs)
	}
}

func TestSSHCandidateWithoutSettingsUsesTheOnlyAttachment(t *testing.T) {
	v := newTestVault(t)
	id, _ := v.AddEntryWithAttachments(Entry{Kind: KindSSH, Title: "k"}, []NamedData{{Name: "key", Data: []byte(testKey)}})
	cs, _ := v.SSHCandidates(id)
	if len(cs) != 1 || cs[0].KeyName != "key" || cs[0].Settings != nil {
		t.Fatalf("%+v", cs)
	}
	two, _ := v.AddEntryWithAttachments(Entry{Kind: KindSSH, Title: "k2"}, []NamedData{{Name: "a", Data: []byte("1")}, {Name: "b", Data: []byte("2")}})
	cs, _ = v.SSHCandidates(two)
	if len(cs) != 1 || cs[0].KeyName != "" {
		t.Fatalf("ambiguous entry picked %q", cs[0].KeyName)
	}
}

func TestReplaceAttachmentsAddsOneHistoryItem(t *testing.T) {
	v := newTestVault(t)
	id := addSSHEntry(t, v, "GitHub")
	off, _ := sshkey.SetAgentEnabled(sshkey.NewAgentSettings("id_ed25519", true), false)
	if err := v.ReplaceAttachments(id, []NamedData{{Name: sshkey.SettingsAttachment, Data: off}, {Name: "old", Data: nil}}); err != nil {
		t.Fatal(err)
	}
	e, _ := v.Get(id)
	if e.History != 1 || len(e.Attachments) != 2 {
		t.Fatalf("%+v", e)
	}
	b, _ := v.Attachment(id, sshkey.SettingsAttachment)
	if s, _ := sshkey.ParseAgentSettings(b); s.Enabled() {
		t.Fatal("settings not replaced")
	}
}

func TestSSHEntryMergesBothWays(t *testing.T) {
	a, clock := baseVault(t)
	id := addSSHEntry(t, a, "GitHub")
	b := fork(t, a, clock)
	off, _ := sshkey.SetAgentEnabled(sshkey.NewAgentSettings("id_ed25519", true), false)
	if err := b.ReplaceAttachments(id, []NamedData{{Name: sshkey.SettingsAttachment, Data: off}}); err != nil {
		t.Fatal(err)
	}
	merge(t, a, b)
	merge(t, b, a)
	if state(a) != state(b) {
		t.Fatal("vaults differ after merging both ways")
	}
	for _, v := range []*Vault{a, b} {
		cs, _ := v.SSHCandidates(id)
		if len(cs) != 1 || cs[0].Kind != KindSSH || string(cs[0].Key) != testKey {
			t.Fatalf("%+v", cs)
		}
		if s, _ := sshkey.ParseAgentSettings(cs[0].Settings); s.Enabled() {
			t.Fatal("the later settings change did not win")
		}
	}
}

func TestSSHEntriesSkippedByHealthAndMatch(t *testing.T) {
	v := newTestVault(t)
	id := addSSHEntry(t, v, "GitHub")
	e, _ := v.Get(id)
	e.Password = "a"
	e.URL = "https://github.com"
	if err := v.UpdateEntry(id, e); err != nil {
		t.Fatal(err)
	}
	h, _ := v.Health()
	if h.Total != 0 || len(h.Weak) != 0 {
		t.Fatalf("%+v", h)
	}
	if m, _ := v.FindMatches("https://github.com", ""); len(m) != 0 {
		t.Fatalf("%+v", m)
	}
}

func TestKeePassXCExportsSSHAttachmentsUnchanged(t *testing.T) {
	needKeePassXC(t)
	dir := t.TempDir()
	v, err := Create(dir, creds(interopPW), KDFStandard, "Interop")
	if err != nil {
		t.Fatal(err)
	}
	settings := sshkey.NewAgentSettings("id_ed25519", true)
	if _, err := v.AddEntryWithAttachments(Entry{Kind: KindSSH, Title: "ssh key"}, []NamedData{
		{Name: "id_ed25519", Data: []byte(testKey)},
		{Name: sshkey.SettingsAttachment, Data: settings},
	}); err != nil {
		t.Fatal(err)
	}
	v.Close()
	db := filepath.Join(dir, FileName)
	for name, want := range map[string][]byte{"id_ed25519": []byte(testKey), sshkey.SettingsAttachment: settings} {
		out := filepath.Join(t.TempDir(), "x")
		if msg, err := kpcli(t, interopPW+"\n", "attachment-export", db, "ssh key", name, out); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, msg)
		}
		if got, _ := os.ReadFile(out); !bytes.Equal(got, want) {
			t.Fatalf("%s differs after KeePassXC export", name)
		}
	}
}

func TestSSHHostsRoundTripWithoutHistory(t *testing.T) {
	a, clock := baseVault(t)
	id := addSSHEntry(t, a, "GitHub")
	if err := a.SetSSHHosts(id, []string{"github.com", "*.corp.example"}); err != nil {
		t.Fatal(err)
	}
	cs, _ := a.SSHCandidates(id)
	if strings.Join(cs[0].Hosts, ",") != "github.com,*.corp.example" {
		t.Fatalf("%v", cs[0].Hosts)
	}
	if e, _ := a.Get(id); e.History != 0 {
		t.Fatalf("history %d", e.History)
	}
	b := fork(t, a, clock)
	if err := b.SetSSHHosts(id, nil); err != nil {
		t.Fatal(err)
	}
	merge(t, a, b)
	if cs, _ := a.SSHCandidates(id); len(cs[0].Hosts) != 0 {
		t.Fatalf("clearing the hosts did not merge: %v", cs[0].Hosts)
	}
	if err := a.SetSSHHosts("nope", nil); err != ErrNotFound {
		t.Fatalf("%v", err)
	}
}
