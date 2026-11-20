package sshagent

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func hashedHost(t *testing.T, name string) string {
	t.Helper()
	salt := make([]byte, 20)
	_, _ = rand.Read(salt)
	m := hmac.New(sha1.New, salt)
	m.Write([]byte(name))
	return "|1|" + base64.StdEncoding.EncodeToString(salt) + "|" + base64.StdEncoding.EncodeToString(m.Sum(nil))
}

func writeKnownHosts(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHostPatternMatch(t *testing.T) {
	cases := []struct {
		p, s string
		ok   bool
	}{
		{"github.com", "github.com", true},
		{"GitHub.com", "github.COM", true},
		{"*.corp.example", "build.corp.example", true},
		{"*.corp.example", "corp.example", false},
		{"10.0.0.?", "10.0.0.7", true},
		{"10.0.0.?", "10.0.0.17", false},
		{"*", "anything", true},
		{"a*b*c", "aXXbYYc", true},
		{"a*b*c", "aXXbYY", false},
		{"[git.example.com]:2222", "[git.example.com]:2222", true},
	}
	for _, c := range cases {
		if got := hostPatternMatch(c.p, c.s); got != c.ok {
			t.Errorf("%q vs %q: %v", c.p, c.s, got)
		}
	}
}

func TestValidHostPattern(t *testing.T) {
	fp := ssh.FingerprintSHA256(hostSigner(t).PublicKey())
	for _, ok := range []string{"github.com", "*.corp.example", "10.0.0.*", "[host]:2222", "my_host-1", fp} {
		if !ValidHostPattern(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "two words", "a,b", "SHA256:short", "host\n", strings.Repeat("a", 256), "!neg"} {
		if ValidHostPattern(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestKnownHostsMatches(t *testing.T) {
	gh := hostSigner(t)
	box := hostSigner(t)
	hidden := hostSigner(t)
	line := func(s ssh.Signer) string { return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(s.PublicKey()))) }
	k := NewKnownHosts(writeKnownHosts(t,
		"github.com,140.82.112.3 "+line(gh),
		"[box.lan]:2222 "+line(box),
		hashedHost(t, "secret.lan")+" "+line(hidden),
	), filepath.Join(t.TempDir(), "missing"))

	if !k.Matches(gh.PublicKey(), []string{"github.com"}) || !k.Matches(gh.PublicKey(), []string{"*.82.112.3"}) {
		t.Fatal("plain names")
	}
	if k.Matches(gh.PublicKey(), []string{"gitlab.com"}) {
		t.Fatal("wrong name matched")
	}
	if !k.Matches(box.PublicKey(), []string{"box.lan"}) || !k.Matches(box.PublicKey(), []string{"[box.lan]:2222"}) {
		t.Fatal("[host]:port")
	}
	if !k.Matches(hidden.PublicKey(), []string{"secret.lan"}) || k.Matches(hidden.PublicKey(), []string{"secret.*"}) {
		t.Fatal("hashed entries match literal names only")
	}
	if k.Matches(gh.PublicKey(), []string{"secret.lan"}) {
		t.Fatal("hashed name matched another host key")
	}
	if !k.Matches(box.PublicKey(), []string{"nothing", ssh.FingerprintSHA256(box.PublicKey())}) {
		t.Fatal("fingerprint")
	}
	if (*KnownHosts)(nil).Matches(gh.PublicKey(), []string{"github.com"}) || !(*KnownHosts)(nil).Matches(gh.PublicKey(), []string{ssh.FingerprintSHA256(gh.PublicKey())}) {
		t.Fatal("without known_hosts only fingerprints can match")
	}
	if got := strings.Join(k.Names(gh.PublicKey()), ","); got != "github.com,140.82.112.3" {
		t.Fatalf("names %q", got)
	}
}

func TestBoundKeysAreOfferedOnlyToTheirHosts(t *testing.T) {
	gh := hostSigner(t)
	other := hostSigner(t)
	known := NewKnownHosts(writeKnownHosts(t, "github.com "+strings.TrimSpace(string(ssh.MarshalAuthorizedKey(gh.PublicKey())))))
	keys := testKeys(t)
	keys[0].Hosts = []string{"github.com"}                             // by name, through known_hosts
	keys[1].Hosts = []string{ssh.FingerprintSHA256(other.PublicKey())} // by host key
	// keys[2] is not bound: offered everywhere
	ring := NewKeyring(nil)
	ring.Replace(ring.Gen(), keys)
	rec := &recorder{}
	h := rec.hooks(nil)
	h.KnownHosts = known
	l := serve(t, ring, h)

	listed := func(bind ssh.Signer) []string {
		a := l.dial(t)
		if bind != nil {
			if _, err := a.Extension("session-bind@openssh.com", bindMsg(t, bind, []byte("sid"), false, false)); err != nil {
				t.Fatal(err)
			}
		}
		ks, err := a.List()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, k := range ks {
			out = append(out, k.Comment)
		}
		return out
	}
	if got := strings.Join(listed(nil), ","); got != "Key A,Key B,Key C" {
		t.Fatalf("unbound connection (ssh-add -l, signing): %s", got)
	}
	if got := strings.Join(listed(gh), ","); got != "Key A,Key C" {
		t.Fatalf("to github.com: %s", got)
	}
	if got := strings.Join(listed(other), ","); got != "Key B,Key C" {
		t.Fatalf("to the other host: %s", got)
	}
	if got := strings.Join(listed(hostSigner(t)), ","); got != "Key C" {
		t.Fatalf("to an unknown host: %s", got)
	}

	// Signing a login for another host with a bound key is refused even if a client asks.
	a := l.dial(t)
	_, _ = a.Extension("session-bind@openssh.com", bindMsg(t, other, []byte("sid2"), false, false))
	login := ssh.Marshal(struct {
		SID     []byte
		Msg     byte
		User    string
		Service string
		Method  string
		HasSig  bool
		Algo    string
		Pub     []byte
	}{[]byte("sid2"), msgUserauthRequest, "git", "ssh-connection", "publickey", true, keys[0].Public.Type(), keys[0].Public.Marshal()})
	if _, err := a.Sign(keys[0].Public, login); err == nil {
		t.Fatal("bound key signed a login for another host")
	}
	if u := rec.last(); u.Reason != RefusedHost {
		t.Fatalf("%+v", u)
	}
	// Signatures that are not logins (git commit signing) are not host-bound.
	sshsig := append([]byte("SSHSIG"), ssh.Marshal(struct{ NS, R, H, D string }{"git", "", "sha512", "x"})...)
	if _, err := a.Sign(keys[0].Public, sshsig); err != nil {
		t.Fatalf("sshsig with a bound key: %v", err)
	}
	// The right host signs.
	b := l.dial(t)
	_, _ = b.Extension("session-bind@openssh.com", bindMsg(t, gh, []byte("sid3"), false, false))
	if _, err := b.Sign(keys[0].Public, login); err != nil {
		t.Fatalf("login to github.com: %v", err)
	}
}
