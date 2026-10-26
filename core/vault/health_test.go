package vault

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestHealthReport(t *testing.T) {
	v := newTestVault(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	v.now = func() time.Time { return clock }
	_, _ = v.AddEntry(Entry{Title: "weak one", Password: "123456"})
	_, _ = v.AddEntry(Entry{Title: "shared A", Password: "Xk9#mQ2$vL7@nR4!zz"})
	_, _ = v.AddEntry(Entry{Title: "shared B", Password: "Xk9#mQ2$vL7@nR4!zz"})
	_, _ = v.AddEntry(Entry{Title: "expired", Password: "Yk8#mQ2$vL7@nR4!zz", ExpiresAt: clock.Add(-time.Hour).Unix()})
	_, _ = v.AddEntry(Entry{Title: "a note", Kind: KindNote, Notes: "n"})
	trash, _ := v.AddEntry(Entry{Title: "in trash", Password: "123456"})
	_, _ = v.Delete(trash)
	clock = clock.Add(400 * 24 * time.Hour)
	h, err := v.Health()
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Weak) != 1 || h.Weak[0].Title != "weak one" || len(h.Reused) != 2 || h.Total != 4 || len(h.Old) != 4 {
		t.Fatalf("health = %+v", h)
	}
	if len(h.Expired) != 1 || h.Expired[0].Title != "expired" || h.NoTOTP != 4 {
		t.Fatalf("health = %+v", h)
	}
}

func TestCSVRoundTripAndForeignHeaders(t *testing.T) {
	v := newTestVault(t)
	gid, _ := v.AddGroup("", "Work")
	_, _ = v.AddEntry(Entry{Title: "csv one", Username: "u", Password: "p,1\"q", URL: "https://x", Notes: "multi\nline", GroupID: gid, Tags: []string{"a", "b"}, Kind: KindAPIKey, TOTP: "JBSWY3DPEHPK3PXP"})
	var buf bytes.Buffer
	if err := v.ExportCSV(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(buf.String(), "Group,Title,Username,Password,") || !strings.Contains(buf.String(), "Work/csv one") && !strings.Contains(buf.String(), "Work,csv one") {
		t.Fatalf("export:\n%s", buf.String())
	}
	w := newTestVault(t)
	added, skipped, err := w.ImportCSV(&buf)
	if err != nil || added != 1 || skipped != 0 {
		t.Fatalf("import = %d %d %v", added, skipped, err)
	}
	l, _ := w.List(Filter{})
	e, _ := w.Get(l[0].ID)
	if e.Password != "p,1\"q" || e.Notes != "multi\nline" || e.Kind != KindAPIKey || e.TOTP != "JBSWY3DPEHPK3PXP" || strings.Join(e.Tags, ",") != "a,b" {
		t.Fatalf("round trip: %+v", e)
	}
	if gs, _ := w.Groups(); len(gs) < 2 {
		t.Fatalf("group not created: %+v", gs)
	}

	// a Bitwarden-style and a Chrome-style export
	bw := "folder,favorite,type,name,notes,fields,reprompt,login_uri,login_username,login_password,login_totp\nPersonal/Mail,0,login,Gmail,,,0,https://mail.google.com,me,pw1,otpauth://totp/G?secret=JBSWY3DPEHPK3PXP\n,,login,,,,,https://nopass.example,x,,\n"
	added, skipped, err = w.ImportCSV(strings.NewReader(bw))
	if err != nil || added != 2 || skipped != 0 { // the second row has no title: its URL is used
		t.Fatalf("bitwarden = %d %d %v", added, skipped, err)
	}
	chrome := "name,url,username,password,note\nsite.example,https://site.example,bob,secret,from chrome\n"
	if added, _, err = w.ImportCSV(strings.NewReader(chrome)); err != nil || added != 1 {
		t.Fatalf("chrome = %d %v", added, err)
	}
	if _, _, err = w.ImportCSV(strings.NewReader("a,b\n1,2\n")); err == nil {
		t.Fatal("CSV without recognisable columns accepted")
	}
	got, _ := w.List(Filter{Query: "gmail"})
	ge, _ := w.Get(got[0].ID)
	gs, _ := w.Groups()
	var path string
	for _, g := range gs {
		if g.ID == ge.GroupID {
			path = g.Path
		}
	}
	if ge.TOTP == "" || !strings.HasSuffix(path, "Personal / Mail") {
		t.Fatalf("gmail: %+v in %q", ge, path)
	}
}
