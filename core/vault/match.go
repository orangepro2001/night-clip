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

// matchHost turns "https://User@Login.Example.com:8443/path" or "login.example.com" into a lower-case
// host without port or userinfo; "" when there is none.
func matchHost(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	h := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return h
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
	host := matchHost(pageURL)
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
				h := matchHost(u)
				switch {
				case h == "":
				case h == host && best < 3:
					best = 3
				case registrable(h) == reg && best < 2:
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
