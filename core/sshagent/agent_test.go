package sshagent

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// memListener hands out the server ends of in-memory connections.
type memListener struct {
	ch     chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newMemListener() *memListener {
	return &memListener{ch: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *memListener) Accept() (io.ReadWriteCloser, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, io.EOF
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *memListener) dial(t *testing.T) agent.ExtendedAgent {
	t.Helper()
	client, server := net.Pipe()
	l.ch <- server
	t.Cleanup(func() { client.Close() })
	return agent.NewClient(client)
}

type recorder struct {
	mu     sync.Mutex
	uses   []Use
	needed int
}

func (r *recorder) hooks(allowFwd *bool) Hooks {
	return Hooks{
		AllowForwarding: func() bool { return allowFwd != nil && *allowFwd },
		OnUse: func(u Use) {
			r.mu.Lock()
			r.uses = append(r.uses, u)
			r.mu.Unlock()
		},
		OnKeysNeeded: func([]Proc) {
			r.mu.Lock()
			r.needed++
			r.mu.Unlock()
		},
		Client: func(io.ReadWriteCloser) []Proc { return []Proc{{PID: 42, Name: "ssh.exe"}} },
	}
}

func (r *recorder) last() Use {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.uses[len(r.uses)-1]
}

func testKeys(t testing.TB) []*Key {
	t.Helper()
	_, ed, _ := ed25519.GenerateKey(rand.Reader)
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	ek, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	var keys []*Key
	for i, priv := range []any{ed, rk, ek} {
		k, err := NewKey(priv, "entry"+string(rune('A'+i)), "Key "+string(rune('A'+i)), "c")
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	return keys
}

func serve(t *testing.T, ring *Keyring, h Hooks) *memListener {
	t.Helper()
	l := newMemListener()
	s := Serve(l, ring, h)
	t.Cleanup(s.Close)
	return l
}

func TestListAndSignEveryKeyType(t *testing.T) {
	ring := NewKeyring(nil)
	keys := testKeys(t)
	ring.Replace(ring.Gen(), keys)
	rec := &recorder{}
	l := serve(t, ring, rec.hooks(nil))
	a := l.dial(t)

	listed, err := a.List()
	if err != nil || len(listed) != 3 {
		t.Fatalf("list: %v %v", listed, err)
	}
	if listed[0].Comment != "Key A" {
		t.Fatalf("comment %q", listed[0].Comment)
	}
	data := []byte("challenge")
	for _, k := range keys {
		flagSets := []agent.SignatureFlags{0}
		if k.Public.Type() == ssh.KeyAlgoRSA {
			flagSets = []agent.SignatureFlags{0, agent.SignatureFlagRsaSha256, agent.SignatureFlagRsaSha512}
		}
		for _, f := range flagSets {
			sig, err := a.SignWithFlags(k.Public, data, f)
			if err != nil {
				t.Fatalf("%s flags %d: %v", k.Public.Type(), f, err)
			}
			if err := k.Public.Verify(data, sig); err != nil {
				t.Fatalf("%s: bad signature: %v", k.Public.Type(), err)
			}
			want := map[agent.SignatureFlags]string{0: ssh.KeyAlgoRSA, agent.SignatureFlagRsaSha256: ssh.KeyAlgoRSASHA256, agent.SignatureFlagRsaSha512: ssh.KeyAlgoRSASHA512}[f]
			if k.Public.Type() == ssh.KeyAlgoRSA && sig.Format != want {
				t.Fatalf("rsa flags %d gave %s", f, sig.Format)
			}
			if u := rec.last(); u.Result != ResultSigned || u.Title != k.Title || u.Client[0].Name != "ssh.exe" || u.Kind != KindOther {
				t.Fatalf("use %+v", u)
			}
		}
	}
}

func TestMutationsAreRefused(t *testing.T) {
	ring := NewKeyring(nil)
	keys := testKeys(t)
	ring.Replace(ring.Gen(), keys)
	a := serve(t, ring, Hooks{}).dial(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	if err := a.Add(agent.AddedKey{PrivateKey: priv}); err == nil {
		t.Fatal("add accepted")
	}
	if err := a.Remove(keys[0].Public); err == nil {
		t.Fatal("remove accepted")
	}
	if err := a.RemoveAll(); err == nil {
		t.Fatal("remove all accepted")
	}
	if err := a.Lock([]byte("x")); err == nil {
		t.Fatal("lock accepted")
	}
	if l, _ := a.List(); len(l) != 3 {
		t.Fatal("keys changed")
	}
}

func TestEmptyKeyringAsksForKeysAndRefusesUnknownKeys(t *testing.T) {
	ring := NewKeyring(nil)
	rec := &recorder{}
	a := serve(t, ring, rec.hooks(nil)).dial(t)
	if l, _ := a.List(); len(l) != 0 {
		t.Fatal("keys listed")
	}
	if rec.needed != 1 {
		t.Fatalf("keys needed %d", rec.needed)
	}
	other := testKeys(t)[0]
	if _, err := a.Sign(other.Public, []byte("x")); err == nil {
		t.Fatal("signed with a key that is not loaded")
	}
	if u := rec.last(); u.Result != ResultRefused || u.Reason != RefusedNoKeys {
		t.Fatalf("%+v", u)
	}
	ring.Replace(ring.Gen(), testKeys(t))
	if _, err := a.Sign(other.Public, []byte("x")); err == nil {
		t.Fatal("signed with a key that is not loaded")
	}
	if u := rec.last(); u.Reason != RefusedUnknownKey {
		t.Fatalf("%+v", u)
	}
}

func bindMsg(t *testing.T, host ssh.Signer, sid []byte, fwd bool, forge bool) []byte {
	t.Helper()
	sig, err := host.Sign(rand.Reader, sid)
	if err != nil {
		t.Fatal(err)
	}
	if forge {
		sig.Blob[0] ^= 1
	}
	return ssh.Marshal(struct {
		HostKey, SessionID, Sig []byte
		Fwd                     bool
	}{host.PublicKey().Marshal(), sid, ssh.Marshal(sig), fwd})
}

func hostSigner(t *testing.T) ssh.Signer {
	_, hk, _ := ed25519.GenerateKey(rand.Reader)
	s, err := ssh.NewSignerFromKey(hk)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSessionBindAndForwarding(t *testing.T) {
	ring := NewKeyring(nil)
	keys := testKeys(t)
	ring.Replace(ring.Gen(), keys)
	rec := &recorder{}
	allow := false
	l := serve(t, ring, rec.hooks(&allow))
	host := hostSigner(t)

	a := l.dial(t)
	if _, err := a.Extension("session-bind@openssh.com", bindMsg(t, host, []byte("session-1"), false, true)); err == nil {
		t.Fatal("forged bind accepted")
	}
	if _, err := a.Extension("session-bind@openssh.com", bindMsg(t, host, []byte("session-1"), false, false)); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := a.Extension("no-such-extension@example.com", nil); err == nil {
		t.Fatal("unknown extension accepted")
	}
	if _, err := a.Sign(keys[0].Public, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if u := rec.last(); u.HostKey == nil || string(u.HostKey.Marshal()) != string(host.PublicKey().Marshal()) || u.Forwarded {
		t.Fatalf("%+v", u)
	}
	// The key that just worked for this host is offered first next time.
	b := l.dial(t)
	_, _ = b.Extension("session-bind@openssh.com", bindMsg(t, host, []byte("session-2"), false, false))
	if _, err := b.Sign(keys[2].Public, []byte("y")); err != nil {
		t.Fatal(err)
	}
	c := l.dial(t)
	_, _ = c.Extension("session-bind@openssh.com", bindMsg(t, host, []byte("session-3"), false, false))
	if listed, _ := c.List(); listed[0].Comment != keys[2].Title {
		t.Fatalf("order: %s first", listed[0].Comment)
	}

	// A forwarded connection gets nothing unless forwarding is allowed.
	f := l.dial(t)
	_, _ = f.Extension("session-bind@openssh.com", bindMsg(t, host, []byte("s-a"), false, false))
	_, _ = f.Extension("session-bind@openssh.com", bindMsg(t, hostSigner(t), []byte("s-b"), true, false))
	if listed, _ := f.List(); len(listed) != 0 {
		t.Fatal("forwarded list not empty")
	}
	if _, err := f.Sign(keys[0].Public, []byte("x")); err == nil {
		t.Fatal("forwarded sign allowed")
	}
	if u := rec.last(); u.Reason != RefusedForwarding || !u.Forwarded {
		t.Fatalf("%+v", u)
	}
	allow = true
	if _, err := f.Sign(keys[0].Public, []byte("x")); err != nil {
		t.Fatalf("forwarding allowed: %v", err)
	}

	// At most 16 binds per connection.
	g := l.dial(t)
	for i := range maxBindsPerConn {
		if _, err := g.Extension("session-bind@openssh.com", bindMsg(t, host, []byte{byte(i + 1)}, false, false)); err != nil {
			t.Fatalf("bind %d: %v", i, err)
		}
	}
	if _, err := g.Extension("session-bind@openssh.com", bindMsg(t, host, []byte("one more"), false, false)); err == nil {
		t.Fatal("17th bind accepted")
	}
}

func TestClassify(t *testing.T) {
	user := testKeys(t)[0]
	host := hostSigner(t)
	userauth := func(method string, withHost bool) []byte {
		b := ssh.Marshal(struct {
			SID     []byte
			Msg     byte
			User    string
			Service string
			Method  string
			HasSig  bool
			Algo    string
			Pub     []byte
		}{[]byte("sid"), msgUserauthRequest, "git", "ssh-connection", method, true, user.Public.Type(), user.Public.Marshal()})
		if withHost {
			b = append(b, ssh.Marshal(struct{ H []byte }{host.PublicKey().Marshal()})...)
		}
		return b
	}
	if r := classify(userauth("publickey", false)); r.Kind != KindUserauth || r.User != "git" || r.HostKey != nil {
		t.Fatalf("%+v", r)
	}
	if r := classify(userauth("publickey-hostbound-v00@openssh.com", true)); r.Kind != KindUserauth || r.HostKey == nil {
		t.Fatalf("%+v", r)
	}
	sshsig := append([]byte("SSHSIG"), ssh.Marshal(struct{ NS, Reserved, Hash, H string }{"git", "", "sha512", "h"})...)
	if r := classify(sshsig); r.Kind != KindSSHSig || r.Namespace != "git" {
		t.Fatalf("%+v", r)
	}
	for _, junk := range [][]byte{nil, {50}, []byte("SSHSIG"), userauth("password", false)} {
		if r := classify(junk); r.Kind != KindOther {
			t.Fatalf("%q: %+v", junk, r)
		}
	}
}

func TestKeyringGenerationsAndGrace(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ring := NewKeyring(func() time.Time { return now })
	expired := 0
	ring.OnExpire = func() { expired++ }

	gen := ring.Gen()
	ring.Clear() // a lock between reading the generation and replacing
	if ring.Replace(gen, testKeys(t)) {
		t.Fatal("stale load replaced the keys")
	}
	keys := testKeys(t)
	if !ring.Replace(ring.Gen(), keys) {
		t.Fatal("fresh load refused")
	}
	if _, st, _ := ring.Snapshot(); st != StateLoaded {
		t.Fatal(st)
	}
	gen = ring.Gen()
	if !ring.Grace(now.Add(time.Hour)) {
		t.Fatal("grace refused")
	}
	if ring.Replace(gen, testKeys(t)) {
		t.Fatal("a load from before the lock came back")
	}
	if k, st, exp := ring.Snapshot(); len(k) != 3 || st != StateGrace || !exp.Equal(now.Add(time.Hour)) {
		t.Fatalf("%d %s %v", len(k), st, exp)
	}
	now = now.Add(time.Hour)
	if k, st, _ := ring.Snapshot(); len(k) != 0 || st != StateEmpty || expired != 1 {
		t.Fatalf("not expired: %d %s %d", len(k), st, expired)
	}
	seed := keys[0].seed
	for _, b := range seed {
		if b != 0 {
			t.Fatal("ed25519 seed not wiped")
		}
	}
	if ring.Grace(now.Add(time.Hour)) {
		t.Fatal("grace with no keys")
	}
}

func TestNewKeyCopiesEd25519(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	keep := append(ed25519.PrivateKey(nil), priv...)
	k, _ := NewKey(priv, "", "", "")
	k.wipe()
	if string(priv) != string(keep) {
		t.Fatal("wiping a loaded key changed the caller's key")
	}
}

func TestServerCloseDisconnectsClients(t *testing.T) {
	ring := NewKeyring(nil)
	l := newMemListener()
	s := Serve(l, ring, Hooks{})
	a := l.dial(t)
	if _, err := a.List(); err != nil {
		t.Fatal(err)
	}
	if s.Connections() != 1 {
		t.Fatalf("connections %d", s.Connections())
	}
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung with a client connected")
	}
}

func TestKnownHostsNames(t *testing.T) {
	host := hostSigner(t)
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(host.PublicKey())))
	path := filepath.Join(t.TempDir(), "known_hosts")
	content := "# comment\n" +
		"github.com,140.82.112.3 " + line + "\n" +
		"[git.example.com]:2222 " + line + "\n" +
		"|1|c2FsdA==|aGFzaA== " + line + "\n" +
		"@cert-authority *.corp " + line + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	k := NewKnownHosts(path)
	got := strings.Join(k.Names(host.PublicKey()), ",")
	if got != "github.com,140.82.112.3,[git.example.com]:2222" {
		t.Fatalf("names %q", got)
	}
	if n := k.Names(hostSigner(t).PublicKey()); len(n) != 0 {
		t.Fatalf("unknown host named %v", n)
	}
	if NewKnownHosts(filepath.Join(t.TempDir(), "missing")).Names(host.PublicKey()) != nil {
		t.Fatal("missing file gave names")
	}
}

func TestClientChainOfSelf(t *testing.T) {
	chain := ClientChain(uint32(os.Getpid()))
	if len(chain) == 0 || chain[0].PID != uint32(os.Getpid()) {
		t.Fatalf("%+v", chain)
	}
}

func FuzzAgentFrames(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 11})                             // request identities
	f.Add([]byte{0, 0, 0, 5, 27, 0, 0, 0, 0})                 // empty extension
	f.Add(append([]byte{0, 0, 0, 9, 13}, make([]byte, 8)...)) // sign with empty key
	ring := NewKeyring(nil)
	ring.Replace(ring.Gen(), testKeys(f))
	f.Fuzz(func(t *testing.T, frames []byte) {
		client, server := net.Pipe()
		s := &Server{ring: ring, hooks: Hooks{Now: time.Now}, hostKeys: map[string]map[string]uint64{}}
		done := make(chan struct{})
		go func() {
			_ = agent.ServeAgent(&connAgent{s: s, conn: server}, server)
			server.Close()
			close(done)
		}()
		go func() {
			_, _ = client.Write(frames)
			_ = client.Close()
		}()
		go func() { _, _ = io.Copy(io.Discard, client) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("agent hung")
		}
	})
}

func FuzzSessionBind(f *testing.F) {
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = parseSessionBind(b)
		_ = classify(b)
	})
}
