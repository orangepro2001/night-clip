package vault

import (
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

type BrowserLogin struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
}

func browserTarget(raw string) (target, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || !u.IsAbs() {
		return target{}, false
	}
	t, ok := parseTarget(raw)
	if !ok || (t.scheme == "http" && t.host != "localhost" && !net.ParseIP(t.host).IsLoopback()) {
		return target{}, false
	}
	t.host, ok = browserHost(t.host)
	return t, ok
}

func browserHost(host string) (string, bool) {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), true
	}
	ascii, err := idna.Lookup.ToASCII(host)
	return strings.ToLower(ascii), err == nil
}

func browserMatches(page target, e Entry) bool {
	if e.Kind != KindLogin || e.Password == "" {
		return false
	}
	same := func(raw string) bool {
		stored, ok := parseTarget(raw)
		if !ok {
			return false
		}
		stored.host, ok = browserHost(stored.host)
		return ok && page == stored
	}
	if same(e.URL) {
		return true
	}
	for _, f := range e.Fields {
		if isAutofillURLField(strings.ToLower(f.Name)) && same(f.Value) {
			return true
		}
	}
	return false
}

// BrowserLogins lists exact-origin matches. An approved, selected entry is checked again
// under the vault lock before the password is handed to the browser bridge.
func (v *Vault) BrowserLogins(origin, selectedID string) ([]BrowserLogin, error) {
	page, ok := browserTarget(origin)
	if !ok {
		return nil, invalid("expected an HTTPS browser origin or loopback HTTP origin")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return nil, err
	}
	out := []BrowserLogin{}
	walk(v.db, func(le *libEntry, gid string, trash bool) {
		if trash || len(out) >= 100 || (selectedID != "" && uuidHex(le.UUID) != selectedID) {
			return
		}
		e := fromLib(v.db, le, gid, false)
		if !browserMatches(page, e) {
			return
		}
		match := BrowserLogin{ID: e.ID, Title: e.Title, Username: e.Username}
		if selectedID != "" {
			match.Password = e.Password
		}
		out = append(out, match)
	}, nil)
	return out, nil
}
