package vault

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

const bitwardenFixture = `{
  "encrypted": false,
  "folders": [{"id":"mail","name":"Personal/Email"}],
  "items":[
    {"type":1,"name":"Email login","folderId":"mail","notes":"Line 1\nLine 2",
     "login":{"username":"test-user","password":"  keep spaces  ","totp":"JBSWY3DPEHPK3PXP",
              "uris":[{"uri":"https://primary.example.net","match":null},
                      {"uri":"http://secondary.example.org","match":0},
                      {"uri":"https://disabled.example.com","match":5},
                      {"uri":"androidapp://com.example.fixture"}],
              "fido2Credentials":[{}]},
     "fields":[{"name":"Reference","value":"first","type":0},
               {"name":"Reference","value":"second","type":1},
               {"name":"Selected","value":true,"type":2},
               {"name":"Copy of password","type":3,"linkedId":101},
               {"name":"URL","value":"https://other.example.io","type":0},
               {"name":"AndroidApp","value":"com.example.other","type":0},
               {"name":"otp","value":"literal","type":0}]},
    {"type":2,"name":"Note","notes":"note body","fields":[{"name":"Public ref","value":"ID"}]},
    {"type":3,"name":"Card","card":{"number":"00001234","code":"000","brand":null}},
    {"type":4,"name":"Identity","identity":{"firstName":"Test","email":"a@example.org"}},
    {"type":5,"name":"SSH","sshKey":{"privateKey":"fixture-only","publicKey":"public"}},
    {"type":2,"name":"Deleted","notes":"ignored","deletedDate":"2025-05-01T00:00:00Z"},
    {"type":99,"name":"Unsupported"}
  ]
}`

func TestImportBitwardenJSON(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.AddEntry(Entry{Title: "Existing", Password: "unrelated"}); err != nil {
		t.Fatal(err)
	}
	report, err := v.ImportBitwardenJSON(strings.NewReader("\ufeff" + bitwardenFixture))
	if err != nil || report != (BitwardenImportResult{Added: 5, Skipped: 2, PasskeysSkipped: 1}) {
		t.Fatalf("import = %+v, %v", report, err)
	}
	check, err := Open(v.dir, v.creds)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()

	items, err := check.List(Filter{})
	if err != nil || len(items) != 6 {
		t.Fatalf("list size = %d, %v", len(items), err)
	}
	byTitle := make(map[string]Entry)
	for _, item := range items {
		e, err := check.Get(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		byTitle[item.Title] = e
	}
	if byTitle["Existing"].Password != "unrelated" {
		t.Fatal("existing entry changed")
	}
	login := byTitle["Email login"]
	if login.URL != "https://primary.example.net" || login.Password != "  keep spaces  " || login.TOTP == "" || login.Notes != "Line 1\nLine 2" {
		t.Fatal("login values changed during import")
	}
	checkField := func(e Entry, name, value string, protected bool) {
		t.Helper()
		for _, f := range e.Fields {
			if f.Name == name && f.Value == value && f.Protected == protected {
				return
			}
		}
		t.Errorf("field %q missing or changed on %q", name, e.Title)
	}
	checkField(login, "URL 2", "http://secondary.example.org", false)
	checkField(login, "Bitwarden URI 3 (match 5)", "https://disabled.example.com", false)
	checkField(login, "URL 4", "androidapp://com.example.fixture", false)
	checkField(login, "Reference", "first", false)
	checkField(login, "Reference (2)", "second", true)
	checkField(login, "Copy of password", "  keep spaces  ", true)
	checkField(login, "Selected", "true", false)
	checkField(login, "Bitwarden: URL", "https://other.example.io", false)
	checkField(login, "Bitwarden: AndroidApp", "com.example.other", false)
	checkField(login, "Bitwarden: otp", "literal", false)
	checkField(byTitle["Note"], "Public ref", "ID", false)
	checkField(byTitle["Card"], "Card: number", "00001234", true)
	checkField(byTitle["Identity"], "Identity: firstName", "Test", true)
	checkField(byTitle["SSH"], "SSH key: privateKey", "fixture-only", true)
	for _, name := range []string{"Note", "Card", "Identity", "SSH"} {
		if byTitle[name].Kind != KindNote {
			t.Errorf("%s is not a secure note", name)
		}
	}
	groups, _ := check.Groups()
	foundFolder := false
	for _, group := range groups {
		if group.ID == login.GroupID && strings.HasSuffix(group.Path, "Personal / Email") {
			foundFolder = true
		}
	}
	if !foundFolder {
		t.Fatal("missing nested Bitwarden folder")
	}
	for _, tc := range []struct {
		url, pkg string
		wantURL  string
		score    int
	}{
		{"https://primary.example.net", "", "https://primary.example.net", 3},
		{"http://secondary.example.org", "", "http://secondary.example.org", 3},
		{"", "com.example.fixture", "https://primary.example.net", 4},
		{url: "https://disabled.example.com"},
		{url: "https://other.example.io"},
		{pkg: "com.example.other"},
	} {
		matches, err := check.FindMatches(tc.url, tc.pkg)
		if err != nil || (tc.score == 0 && len(matches) != 0) ||
			(tc.score != 0 && (len(matches) != 1 || matches[0].ID != login.ID || matches[0].Score != tc.score || matches[0].URL != tc.wantURL)) {
			t.Errorf("match %s %s: %v, %v", tc.url, tc.pkg, matches, err)
		}
	}
}

func TestBitwardenImportRejectsInvalidAndPreservesVaultOnSaveFailure(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.ImportBitwardenJSON(strings.NewReader(`{"encrypted":true,"data":"ciphertext"}`)); !errors.Is(err, ErrBitwardenEncrypted) {
		t.Fatalf("encrypted import error = %v", err)
	}
	for _, data := range []string{`{}`, `{"items":[`, `{"items":[]}{}`} {
		if _, err := v.ImportBitwardenJSON(strings.NewReader(data)); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid import error = %v", err)
		}
	}
	if _, err := v.AddEntry(Entry{Title: "Existing"}); err != nil {
		t.Fatal(err)
	}
	groups, _ := v.Groups()
	dir := v.dir
	v.dir = filepath.Join(dir, "absent")
	_, err := v.ImportBitwardenJSON(strings.NewReader(bitwardenFixture))
	v.dir = dir
	if err == nil {
		t.Fatal("import unexpectedly saved into a missing directory")
	}
	items, err := v.List(Filter{})
	nowGroups, _ := v.Groups()
	if err != nil || len(items) != 1 || items[0].Title != "Existing" || len(nowGroups) != len(groups) {
		t.Fatal("failed import left entries or folders in memory")
	}
}
