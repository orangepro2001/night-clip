package vault

import "testing"

func TestFindMatchesIsStrict(t *testing.T) {
	v := newTestVault(t)
	add := func(e Entry) string {
		id, err := v.AddEntry(e)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	exact := add(Entry{Title: "login host", URL: "https://login.example.com/signin", Password: "p"})
	root := add(Entry{Title: "root domain", URL: "https://example.com", Password: "p"})
	other := add(Entry{Title: "other tld", URL: "https://example.org", Password: "p"})
	evil := add(Entry{Title: "evil lookalike", URL: "https://example.com.evil.net", Password: "p"})
	dash := add(Entry{Title: "dash lookalike", URL: "https://evil-example.com", Password: "p"})
	app := add(Entry{Title: "android app", URL: "androidapp://com.example.app", Password: "p"})
	appField := add(Entry{Title: "android field", Fields: []Field{{Name: "AndroidApp", Value: "com.example.app"}}, Password: "p"})
	add(Entry{Title: "a note", Kind: KindNote, URL: "https://example.com", Notes: "n"})
	trash := add(Entry{Title: "in trash", URL: "https://example.com", Password: "p"})
	_, _ = v.Delete(trash)
	multi := add(Entry{Title: "extra url", URL: "https://unrelated.test", Fields: []Field{{Name: "URL2", Value: "https://shop.example.com"}}, Password: "p"})

	ids := func(ms []Match) map[string]int {
		out := map[string]int{}
		for _, m := range ms {
			out[m.ID] = m.Score
		}
		return out
	}
	got, err := v.FindMatches("https://login.example.com/path?x=1", "")
	if err != nil {
		t.Fatal(err)
	}
	m := ids(got)
	if m[exact] != 3 || m[root] != 2 || m[multi] != 2 || len(m) != 3 {
		t.Fatalf("login.example.com matched %v", m)
	}
	if got[0].ID != exact {
		t.Fatalf("the exact host must come first: %+v", got)
	}
	// the page of a look-alike must never get the real site's credentials
	got, _ = v.FindMatches("https://example.com.evil.net/login", "")
	if m := ids(got); len(m) != 1 || m[evil] != 3 {
		t.Fatalf("example.com.evil.net matched %v", m)
	}
	got, _ = v.FindMatches("evil-example.com", "")
	if m := ids(got); len(m) != 1 || m[dash] != 3 {
		t.Fatalf("evil-example.com matched %v", m)
	}
	got, _ = v.FindMatches("https://example.org", "")
	if m := ids(got); len(m) != 1 || m[other] != 3 {
		t.Fatalf("example.org matched %v", m)
	}
	// public suffixes with several labels
	co := add(Entry{Title: "co.uk site", URL: "https://shop.example.co.uk", Password: "p"})
	got, _ = v.FindMatches("https://www.example.co.uk/", "")
	if m := ids(got); m[co] != 2 {
		t.Fatalf("example.co.uk matched %v", m)
	}
	got, _ = v.FindMatches("https://other.co.uk", "")
	if len(got) != 0 {
		t.Fatalf("co.uk must not be a registrable domain: %+v", got)
	}
	// apps
	got, _ = v.FindMatches("", "com.example.app")
	if m := ids(got); len(m) != 2 || m[app] != 4 || m[appField] != 4 {
		t.Fatalf("android package matched %v", m)
	}
	if got, _ = v.FindMatches("", "com.example.app.evil"); len(got) != 0 {
		t.Fatalf("a different package matched: %+v", got)
	}
	if got, _ = v.FindMatches("", ""); len(got) != 0 {
		t.Fatalf("empty query matched: %+v", got)
	}
	// IPs and single labels match exactly
	ip := add(Entry{Title: "router", URL: "http://192.168.1.1/admin", Password: "p"})
	if got, _ = v.FindMatches("http://192.168.1.1", ""); len(got) != 1 || got[0].ID != ip {
		t.Fatalf("ip: %+v", got)
	}
	if got, _ = v.FindMatches("http://192.168.1.2", ""); len(got) != 0 {
		t.Fatalf("other ip: %+v", got)
	}
}

// Review finding 10: scheme and port decide too.
func TestFindMatchesChecksSchemeAndPort(t *testing.T) {
	v := newTestVault(t)
	good, _ := v.AddEntry(Entry{Title: "good", URL: "https://good.com/login", Password: "p"})
	legacy, _ := v.AddEntry(Entry{Title: "legacy http", URL: "http://old.example.net", Password: "p"})
	ported, _ := v.AddEntry(Entry{Title: "ported", URL: "https://app.example.org:8443", Password: "p"})
	lan, _ := v.AddEntry(Entry{Title: "router", URL: "https://192.168.0.1", Password: "p"})
	has := func(page, id string) bool {
		got, _ := v.FindMatches(page, "")
		for _, m := range got {
			if m.ID == id {
				return true
			}
		}
		return false
	}
	for page, want := range map[string]bool{
		"https://good.com/": true, "https://sub.good.com": true, "good.com": true,
		"http://good.com/": false, "http://good.com:1337/": false, "https://good.com:1337/": false,
		"ftp://good.com": false, "file://good.com/x": false, "javascript://good.com/%0aalert(1)": false,
	} {
		if has(page, good) != want {
			t.Errorf("page %q for an https entry: got %v, want %v", page, !want, want)
		}
	}
	if !has("http://old.example.net/", legacy) || !has("https://old.example.net/", legacy) {
		t.Error("an entry saved for http must fit its http page (and the https page, which is safer)")
	}
	if !has("https://app.example.org:8443/x", ported) || has("https://app.example.org/x", ported) || has("https://app.example.org:9000/", ported) {
		t.Error("an explicit port must match exactly")
	}
	if !has("http://192.168.0.1/", lan) {
		t.Error("plain http on the local network may use an https entry")
	}
	if has("http://203.0.113.9/", lan) {
		t.Error("another host matched an IP entry")
	}
}
