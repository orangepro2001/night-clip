package vault

import (
	"net"
	"net/url"
	"sort"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// Matching an entry to a web site or an Android app for autofill. It is deliberately strict: a
// credential is offered only when the registrable domain (the public suffix plus one label) is the
// same, never because one name contains another ("example.com.evil.net" and "evil-example.com" do
// not match example.com).

// Match is an entry that fits, with how well.
type Match struct {
	Summary
	Score int `json:"score"` // 3 same host, 2 same registrable domain, 4 Android package
}

// target is where a page or a stored URL points: scheme, host and effective port.
type target struct {
	scheme, host string
	port         string
}

// parseTarget reads "https://User@Login.Example.com:8443/path" or "login.example.com". A value without a
// scheme counts as https. ok is false for anything that is not a plain http(s) address.
func parseTarget(s string) (t target, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return t, false
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return t, false
	}
	t.scheme = strings.ToLower(u.Scheme)
	if t.scheme != "http" && t.scheme != "https" {
		return t, false
	}
	t.host = strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if t.host == "" {
		return t, false
	}
	// the port only when it is not the default of its scheme: a stored http://site and the page
	// https://site are the same place, :8443 is not
	t.port = u.Port()
	if t.port == map[string]string{"http": "80", "https": "443"}[t.scheme] {
		t.port = ""
	}
	return t, true
}

// matchHost is the host part only (used by tests and callers that only care about the name).
func matchHost(s string) string {
	t, _ := parseTarget(s)
	return t.host
}

// isLocalHost: names and addresses where plain http is not an exposure to the network.
func isLocalHost(h string) bool {
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// schemeAllows says whether credentials stored for an entry URL may be offered on a page. An https page
// may use an entry saved with http or https (nothing is exposed); an http page only gets entries saved
// for http, or anything when the host is on the local machine or network.
func schemeAllows(page, entry target) bool {
	if page.scheme == "https" {
		return true
	}
	return entry.scheme == "http" || isLocalHost(page.host)
}

// registrable returns the eTLD+1 of host, or host itself for IP addresses, single labels and names
// the public suffix list does not know.
func registrable(host string) string {
	if host == "" || net.ParseIP(host) != nil {
		return host
	}
	if d, err := publicsuffix.EffectiveTLDPlusOne(host); err == nil {
		return d
	}
	return host
}

// FindMatches returns the entries that fit a page (url) or an app (package), best first. Entries in the
// recycle bin and notes never match.
func (v *Vault) FindMatches(pageURL, androidPackage string) ([]Match, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return nil, err
	}
	page, pageOK := parseTarget(pageURL)
	host := ""
	if pageOK {
		host = page.host
	}
	reg := registrable(host)
	pkg := strings.TrimSpace(androidPackage)
	if host == "" && pkg == "" {
		return []Match{}, nil
	}
	var out []Match
	walk(v.db, func(le *libEntry, gid string, trash bool) {
		if trash {
			return
		}
		e := fromLib(v.db, le, gid, false)
		if e.Kind == KindNote {
			return
		}
		best := 0
		if pkg != "" {
			for _, f := range e.Fields {
				if strings.EqualFold(f.Name, "AndroidApp") && strings.TrimSpace(f.Value) == pkg {
					best = 4
				}
			}
			if strings.TrimSpace(strings.TrimPrefix(e.URL, "androidapp://")) == pkg && strings.HasPrefix(e.URL, "androidapp://") {
				best = 4
			}
		}
		if host != "" {
			urls := []string{e.URL}
			for _, f := range e.Fields {
				if n := strings.ToLower(f.Name); strings.HasPrefix(n, "url") || strings.HasPrefix(n, "kp2a_url") {
					urls = append(urls, f.Value)
				}
			}
			for _, u := range urls {
				if strings.HasPrefix(u, "androidapp://") {
					continue
				}
				et, ok := parseTarget(u)
				if !ok || et.port != page.port || !schemeAllows(page, et) {
					continue
				}
				switch {
				case et.host == host && best < 3:
					best = 3
				case registrable(et.host) == reg && best < 2:
					best = 2
				}
			}
		}
		if best > 0 {
			out = append(out, Match{Summary: e.Summarize(), Score: best})
		}
	}, nil)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	if out == nil {
		out = []Match{}
	}
	return out, nil
}
