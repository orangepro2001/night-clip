package vault

import "testing"

func TestBrowserLoginsExactOrigin(t *testing.T) {
	v := newTestVault(t)
	id, err := v.AddEntry(Entry{Kind: KindLogin, Title: "Test", Username: "tester", Password: "synthetic",
		URL: "https://example.test/", Fields: []Field{{Name: "URL 2", Value: "https://login.example.test:8443"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"https://example.test", "https://login.example.test:8443"} {
		got, err := v.BrowserLogins(origin, "")
		if err != nil || len(got) != 1 || got[0].ID != id || got[0].Password != "" {
			t.Fatalf("unsafe or missing match for %s: count=%d err=%v", origin, len(got), err)
		}
		got, err = v.BrowserLogins(origin, id)
		if err != nil || len(got) != 1 || got[0].Password != "synthetic" {
			t.Fatalf("approved match missing for %s: %v", origin, err)
		}
	}
	for _, origin := range []string{"https://foo.example.test", "https://login.example.test", "https://example.test:444", "https://example.test.evil.test"} {
		got, err := v.BrowserLogins(origin, id)
		if err != nil || len(got) != 0 {
			t.Fatalf("origin mismatch accepted: %s", origin)
		}
	}
	if _, err := v.BrowserLogins("http://example.test", id); err == nil {
		t.Fatal("external HTTP origin accepted")
	}
	if _, err := v.Delete(id); err != nil {
		t.Fatal(err)
	}
	if got, err := v.BrowserLogins("https://example.test", id); err != nil || len(got) != 0 {
		t.Fatal("deleted entry matched")
	}
}
