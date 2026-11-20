package facade

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"moonpassword/core/pipe"
	"moonpassword/core/sshagent"
	"moonpassword/core/sshkey"
)

const fixturePass = "correct horse battery"

func fixtureB64(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "sshkey", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func testAgentPipe(t *testing.T) string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\cb-test-` + randomHex()
	}
	return filepath.Join(t.TempDir(), "a.sock")
}

func enableAgent(t *testing.T, n *node) string {
	t.Helper()
	name := testAgentPipe(t)
	n.call("settings.set", map[string]any{"key": "ssh_agent_pipe", "value": name})
	n.call("settings.set", map[string]any{"key": "ssh_agent", "value": "on"})
	n.waitEvent("ssh.agent", map[string]any{"state": "listening", "pipe": name})
	return name
}

func agentClient(t *testing.T, name string) agent.ExtendedAgent {
	t.Helper()
	conn, err := pipe.Dial(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	// Not a Closer: the pipelined client reads from a second goroutine, which a synchronous Windows pipe
	// handle cannot do while the first goroutine writes.
	return agent.NewClient(struct {
		io.Reader
		io.Writer
	}{conn, conn})
}

func listFPs(t *testing.T, name string) []string {
	t.Helper()
	keys, err := agentClient(t, name).List()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, k := range keys {
		pub, _ := ssh.ParsePublicKey(k.Blob)
		out = append(out, ssh.FingerprintSHA256(pub))
	}
	return out
}

func TestSSHInspectImportAndViews(t *testing.T) {
	n := startNode(t, "Desk", "windows")
	enc := fixtureB64(t, "openssh_ed25519_aes256-ctr")

	// inspect works without a vault and says what it needs
	in := n.call("ssh.inspect", map[string]any{"data_b64": enc})
	if in["needs_passphrase"] != true || in["format"] != "openssh" || in["cipher"] != "aes256-ctr" || in["fingerprint"] == nil {
		t.Fatalf("inspect: %v", in)
	}
	if in := n.call("ssh.inspect", map[string]any{"data_b64": enc, "passphrase": "nope"}); in["wrong_passphrase"] != true {
		t.Fatalf("wrong passphrase: %v", in)
	}
	full := n.call("ssh.inspect", map[string]any{"data_b64": enc, "passphrase": fixturePass})
	if full["needs_passphrase"] != false || full["algo"] != "ssh-ed25519" || full["comment"] != "fixture ed25519 aes256-ctr" {
		t.Fatalf("inspect with passphrase: %v", full)
	}
	if code := n.errorCode("ssh.inspect", map[string]any{"data_b64": fixtureB64(t, "reject_dsa_openssh")}); code != "SSH_KEY_UNSUPPORTED" {
		t.Fatalf("dsa: %s", code)
	}
	if code := n.errorCode("ssh.import", map[string]any{"data_b64": enc, "passphrase": fixturePass}); code != "VAULT_LOCKED" {
		t.Fatalf("import while locked: %s", code)
	}

	vcreate(t, n)
	r := n.try("ssh.import", map[string]any{"data_b64": enc, "passphrase": "nope"})
	if r.OK || r.Error.Code != "SSH_KEY_PASSPHRASE" || r.Error.Details["wrong"] != true {
		t.Fatalf("wrong passphrase import: %+v", r)
	}
	imp := n.call("ssh.import", map[string]any{"data_b64": enc, "passphrase": fixturePass, "title": "GitHub"})
	id := imp["id"].(string)
	if imp["fingerprint"] != full["fingerprint"] || imp["attachment_name"] != "id_ed25519" {
		t.Fatalf("import: %v", imp)
	}
	r = n.try("ssh.import", map[string]any{"data_b64": enc, "passphrase": fixturePass})
	if r.OK || r.Error.Code != "SSH_KEY_DUPLICATE" || r.Error.Details["entry_id"] != id {
		t.Fatalf("duplicate: %+v", r)
	}
	if in := n.call("ssh.inspect", map[string]any{"data_b64": enc, "passphrase": fixturePass}); len(in["in_vault"].([]any)) != 1 {
		t.Fatalf("in_vault: %v", in)
	}

	raw := n.c.Call("vault.get", `{"id":"`+id+`"}`)
	if strings.Contains(raw, "PRIVATE KEY") || strings.Contains(raw, "b3BlbnNzaC1rZXktdjE") {
		t.Fatalf("vault.get leaks the key: %s", raw)
	}
	get := n.call("vault.get", map[string]any{"id": id})
	sshv := get["ssh"].(map[string]any)
	if get["kind"] != "ssh" || sshv["fingerprint"] != full["fingerprint"] || sshv["agent"] != true || sshv["problem"] != "" || sshv["source_format"] != "openssh" {
		t.Fatalf("vault.get ssh: %v", get)
	}
	if !strings.HasPrefix(sshv["public_key"].(string), "ssh-ed25519 AAAA") {
		t.Fatalf("public key %v", sshv["public_key"])
	}

	pem := n.call("ssh.import", map[string]any{"data_b64": fixtureB64(t, "pem_rsa2048"), "path": "", "attachment_name": "dev-cloud.pem"})
	if pem["algo"] != "ssh-rsa" || pem["attachment_name"] != "dev-cloud.pem" {
		t.Fatalf("pem import: %v", pem)
	}
	gen := n.call("ssh.generate", map[string]any{"type": "ecdsa", "bits": 384, "title": "Build server"})
	if !strings.HasPrefix(gen["public_key"].(string), "ecdsa-sha2-nistp384 ") || !strings.HasSuffix(gen["public_key"].(string), " Build server") {
		t.Fatalf("generate: %v", gen)
	}
	if code := n.errorCode("ssh.generate", map[string]any{"type": "rsa", "bits": 1024}); code != "INVALID" {
		t.Fatalf("rsa 1024: %s", code)
	}
	keys := n.call("ssh.keys", nil)["keys"].([]any)
	if len(keys) != 3 {
		t.Fatalf("keys: %v", keys)
	}
	pk := n.call("ssh.public_key", map[string]any{"id": gen["id"]})
	if pk["public_key"] != gen["public_key"] || pk["title"] != "Build server" {
		t.Fatalf("public_key: %v", pk)
	}

	n.call("ssh.set_agent", map[string]any{"id": id, "enabled": false})
	if v := n.call("vault.get", map[string]any{"id": id})["ssh"].(map[string]any); v["agent"] != false {
		t.Fatalf("agent toggle: %v", v)
	}
	// An old client that edits the entry as a login keeps it an SSH key.
	n.call("vault.update", map[string]any{"id": id, "entry": map[string]any{"kind": "login", "title": "GitHub", "notes": "x"}})
	if get := n.call("vault.get", map[string]any{"id": id}); get["kind"] != "ssh" {
		t.Fatalf("kind after an update as login: %v", get["kind"])
	}
}

func TestSSHExportNeedsTheMasterPassword(t *testing.T) {
	n := startNode(t, "Desk", "windows")
	vcreate(t, n)
	gen := n.call("ssh.generate", map[string]any{"type": "ed25519", "title": "k"})
	dir := t.TempDir()
	out := filepath.Join(dir, "id_ed25519")
	if code := n.errorCode("ssh.export", map[string]any{"id": gen["id"], "path": out, "password": "wrong password!"}); code != "VAULT_WRONG_CREDENTIALS" {
		t.Fatalf("wrong master: %s", code)
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("file written without the master password")
	}
	n.call("ssh.export", map[string]any{"id": gen["id"], "path": out, "password": vpw, "passphrase": "file pass"})
	b, _ := os.ReadFile(out)
	k, err := sshkey.Parse(b, []byte("file pass"))
	if err != nil || k.Fingerprint() != gen["fingerprint"] || !k.Encrypted {
		t.Fatalf("exported key: %v %v", err, k)
	}
	if code := n.errorCode("ssh.export", map[string]any{"id": gen["id"], "path": out, "password": vpw}); code != "INVALID" {
		t.Fatalf("overwrite: %s", code)
	}
	pk8 := filepath.Join(dir, "k.p8")
	n.call("ssh.export", map[string]any{"id": gen["id"], "path": pk8, "password": vpw, "format": "pkcs8"})
	if b, _ := os.ReadFile(pk8); !strings.HasPrefix(string(b), "-----BEGIN PRIVATE KEY-----") {
		t.Fatalf("pkcs8: %s", b[:20])
	}
}

func TestSSHWritePubAndScan(t *testing.T) {
	n := startNode(t, "Desk", "windows")
	vcreate(t, n)
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "cloud.pem")
	raw, _ := base64.StdEncoding.DecodeString(fixtureB64(t, "pem_rsa2048"))
	_ = os.WriteFile(keyPath, raw, 0o600)

	scan := n.call("ssh.scan", map[string]any{"dir": dir})
	items := scan["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("scan: %v", scan)
	}
	it := items[0].(map[string]any)
	if it["pub_state"] != "missing" || len(it["in_vault"].([]any)) != 0 || it["format"] != "pem-pkcs1" {
		t.Fatalf("scan item: %v", it)
	}
	imp := n.call("ssh.import", map[string]any{"path": keyPath})
	if imp["attachment_name"] != "cloud.pem" {
		t.Fatalf("import by path: %v", imp)
	}
	if it := n.call("ssh.scan", map[string]any{"dir": dir})["items"].([]any)[0].(map[string]any); len(it["in_vault"].([]any)) != 1 {
		t.Fatalf("in_vault after import: %v", it)
	}
	pub := keyPath + ".pub"
	if w := n.call("ssh.write_pub", map[string]any{"path": pub, "id": imp["id"]}); w["state"] != "created" {
		t.Fatalf("write_pub: %v", w)
	}
	if w := n.call("ssh.write_pub", map[string]any{"path": pub, "fingerprint": imp["fingerprint"]}); w["state"] != "same" {
		t.Fatalf("write_pub again: %v", w)
	}
	other := n.call("ssh.generate", map[string]any{"title": "other"})
	if w := n.call("ssh.write_pub", map[string]any{"path": pub, "id": other["id"]}); w["state"] != "mismatch" {
		t.Fatalf("write_pub mismatch: %v", w)
	}
	if code := n.errorCode("ssh.write_pub", map[string]any{"path": keyPath, "id": imp["id"]}); code != "INVALID" {
		t.Fatalf("write_pub to a non-.pub path: %s", code)
	}
	if it := n.call("ssh.scan", map[string]any{"dir": dir})["items"].([]any)[0].(map[string]any); it["pub_state"] != "ok" {
		t.Fatalf("pub after write: %v", it)
	}
}

func TestSSHKeePassXCStyleEntry(t *testing.T) {
	n := startNode(t, "Desk", "windows")
	vcreate(t, n)
	// KeePassXC: a normal entry, the key passphrase in Password, the encrypted key and KeeAgent.settings attached.
	add := n.call("vault.add", map[string]any{"entry": map[string]any{"title": "From KeePassXC", "password": fixturePass}})
	id := add["id"].(string)
	n.call("vault.attach_add", map[string]any{"id": id, "name": "id_ed25519", "data_b64": fixtureB64(t, "openssh_ed25519_chacha20-poly1305")})
	settings := base64.StdEncoding.EncodeToString(sshkey.NewAgentSettings("id_ed25519", true))
	n.call("vault.attach_add", map[string]any{"id": id, "name": "KeeAgent.settings", "data_b64": settings})

	get := n.call("vault.get", map[string]any{"id": id})
	sshv := get["ssh"].(map[string]any)
	if get["kind"] != "ssh" || sshv["problem"] != "" || sshv["encrypted"] != true || sshv["agent"] != true {
		t.Fatalf("keepassxc entry: %v", get)
	}
	norm := n.call("ssh.normalize", map[string]any{"id": id})
	if norm["fingerprint"] != sshv["fingerprint"] {
		t.Fatalf("normalize: %v", norm)
	}
	if v := n.call("vault.get", map[string]any{"id": id})["ssh"].(map[string]any); v["encrypted"] != false || v["source_format"] != "openssh" {
		t.Fatalf("after normalize: %v", v)
	}

	// Without the passphrase in the entry the key is reported, not lost.
	add2 := n.call("vault.add", map[string]any{"entry": map[string]any{"title": "No pass"}})
	id2 := add2["id"].(string)
	n.call("vault.attach_add", map[string]any{"id": id2, "name": "k", "data_b64": fixtureB64(t, "openssh_ed25519_aes256-gcm")})
	n.call("vault.attach_add", map[string]any{"id": id2, "name": "KeeAgent.settings", "data_b64": base64.StdEncoding.EncodeToString(sshkey.NewAgentSettings("k", true))})
	if v := n.call("vault.get", map[string]any{"id": id2})["ssh"].(map[string]any); v["problem"] != "needs_passphrase" {
		t.Fatalf("needs passphrase: %v", v)
	}
	n.call("ssh.normalize", map[string]any{"id": id2, "passphrase": fixturePass})
	if v := n.call("vault.get", map[string]any{"id": id2})["ssh"].(map[string]any); v["problem"] != "" {
		t.Fatalf("after normalize with passphrase: %v", v)
	}
}

func TestSSHAgentServesVaultKeys(t *testing.T) {
	n := startNode(t, "Desk", "windows")
	if st := n.call("ssh.status", nil); st["state"] != "stopped" || st["enabled"] != false {
		t.Fatalf("status before: %v", st)
	}
	name := enableAgent(t, n)
	vcreate(t, n)
	gen := n.call("ssh.generate", map[string]any{"type": "ed25519", "title": "GitHub"})
	fp := gen["fingerprint"].(string)
	n.waitEvent("ssh.keys", map[string]any{"state": "loaded", "count": float64(1)})

	a := agentClient(t, name)
	keys, err := a.List()
	if err != nil || len(keys) != 1 || keys[0].Comment != "GitHub" {
		t.Fatalf("list: %v %v", keys, err)
	}
	pub, _ := ssh.ParsePublicKey(keys[0].Blob)
	sig, err := a.Sign(pub, []byte("data"))
	if err != nil || pub.Verify([]byte("data"), sig) != nil {
		t.Fatalf("sign: %v", err)
	}
	used := n.waitEvent("ssh.used", map[string]any{"fingerprint": fp})
	if used["title"] != "GitHub" || used["result"] != "signed" {
		t.Fatalf("ssh.used: %v", used)
	}
	if runtime.GOOS == "windows" && len(used["client"].([]any)) == 0 { // the client chain is Windows-only so far
		t.Fatalf("no client process in %v", used)
	}
	if recent := n.call("ssh.recent", nil)["uses"].([]any); len(recent) != 1 {
		t.Fatalf("recent: %v", recent)
	}
	if st := n.call("ssh.status", nil); st["state"] != "listening" || st["keys_loaded"] != float64(1) || st["keys_state"] != "loaded" {
		t.Fatalf("status: %v", st)
	}
	if v := n.call("vault.get", map[string]any{"id": gen["id"]})["ssh"].(map[string]any); v["loaded"] != true || v["last_used_at"] == nil {
		t.Fatalf("loaded flag: %v", v)
	}
	if st := n.call("ssh.selftest", nil); st["ok"] != true || st["listed"] != float64(1) {
		t.Fatalf("selftest: %v", st)
	}
	if d := n.call("diagnostics.get", nil)["ssh"].(map[string]any); d["state"] != "listening" {
		t.Fatalf("diagnostics: %v", d)
	}

	// Turning the key's agent flag off removes it without touching anything else.
	n.call("ssh.set_agent", map[string]any{"id": gen["id"], "enabled": false})
	waitFor(t, "key removed", func() bool { return len(listFPs(t, name)) == 0 })
	n.call("ssh.set_agent", map[string]any{"id": gen["id"], "enabled": true})
	waitFor(t, "key back", func() bool { return len(listFPs(t, name)) == 1 })
	// Deleting (to the trash) removes it too.
	n.call("vault.delete", map[string]any{"id": gen["id"]})
	waitFor(t, "trashed key removed", func() bool { return len(listFPs(t, name)) == 0 })
	n.call("vault.restore_entry", map[string]any{"id": gen["id"]})
	waitFor(t, "restored key back", func() bool { return len(listFPs(t, name)) == 1 })

	// A manual lock drops the keys at once.
	lock := n.call("vault.lock", nil)
	if lock["was_unlocked"] != true || lock["ssh_keys_cleared"] != float64(1) {
		t.Fatalf("lock: %v", lock)
	}
	if fps := listFPs(t, name); len(fps) != 0 {
		t.Fatalf("keys after a manual lock: %v", fps)
	}
	n.waitEvent("ssh.keys_needed", nil)
	if code := n.errorCode("vault.lock", map[string]any{"reason": "bogus"}); code != "INVALID" {
		t.Fatalf("bad reason: %s", code)
	}
}

func TestSSHKeysOutliveAnIdleLock(t *testing.T) {
	n := startNode(t, "Desk", "windows")
	name := enableAgent(t, n)
	n.call("settings.set", map[string]any{"key": "ssh_key_ttl_s", "value": "3600"})
	vcreate(t, n)
	gen := n.call("ssh.generate", map[string]any{"title": "k"})
	n.waitEvent("ssh.keys", map[string]any{"state": "loaded", "count": float64(1)})

	_ = n.c.db.SetSetting(settingVaultAutoLock, "1")
	n.call("vault.list", nil)
	n.waitEvent("vault.locked", map[string]any{"reason": "idle"})
	ev := n.waitEvent("ssh.keys", map[string]any{"state": "grace"})
	if ev["expires_at"] == nil {
		t.Fatalf("grace event: %v", ev)
	}
	if fps := listFPs(t, name); len(fps) != 1 || fps[0] != gen["fingerprint"] {
		t.Fatalf("keys during grace: %v", fps)
	}
	if pk := n.call("ssh.public_key", map[string]any{"fingerprint": gen["fingerprint"]}); pk["title"] != "k" {
		t.Fatalf("public key while locked: %v", pk)
	}
	if keys := n.call("ssh.keys", nil); keys["vault_unlocked"] != false || len(keys["keys"].([]any)) != 1 {
		t.Fatalf("ssh.keys while locked: %v", keys)
	}
	if st := n.call("ssh.status", nil); st["keys_state"] != "grace" || st["expires_at"].(float64) == 0 {
		t.Fatalf("status during grace: %v", st)
	}

	// A session lock while the vault is already locked still drops them.
	lock := n.call("vault.lock", map[string]any{"reason": "session"})
	if lock["was_unlocked"] != false || lock["ssh_keys_cleared"] != float64(1) {
		t.Fatalf("session lock: %v", lock)
	}
	if fps := listFPs(t, name); len(fps) != 0 {
		t.Fatalf("keys after session lock: %v", fps)
	}

	// The grace period ends by the wall clock.
	n.call("vault.unlock", map[string]any{"password": vpw})
	waitFor(t, "keys reloaded after unlock", func() bool { return len(listFPs(t, name)) == 1 })
	if !n.c.ssh.ring.Grace(time.Now().Add(-time.Second)) {
		t.Fatal("no keys to start a grace period with")
	}
	if fps := listFPs(t, name); len(fps) != 0 {
		t.Fatalf("keys after the grace period: %v", fps)
	}
	n.waitEvent("ssh.keys", map[string]any{"state": "expired"})
}

func TestSSHTTLZeroLocksWithTheVault(t *testing.T) {
	n := startNode(t, "Desk", "windows")
	name := enableAgent(t, n)
	n.call("settings.set", map[string]any{"key": "ssh_key_ttl_s", "value": "0"})
	vcreate(t, n)
	n.call("ssh.generate", map[string]any{"title": "k"})
	n.waitEvent("ssh.keys", map[string]any{"state": "loaded", "count": float64(1)})
	_ = n.c.db.SetSetting(settingVaultAutoLock, "1")
	n.call("vault.list", nil)
	n.waitEvent("vault.locked", map[string]any{"reason": "idle"})
	n.waitEvent("ssh.keys", map[string]any{"state": "cleared", "reason": "idle"})
	if fps := listFPs(t, name); len(fps) != 0 {
		t.Fatalf("keys after idle lock with ttl 0: %v", fps)
	}
}

func TestSSHAgentSettings(t *testing.T) {
	n := startNode(t, "Desk", "windows")
	for _, bad := range []map[string]any{
		{"key": "ssh_agent", "value": "yes"},
		{"key": "ssh_key_ttl_s", "value": "10"},
		{"key": "ssh_key_ttl_s", "value": "999999"},
		{"key": "ssh_allow_forwarding", "value": "1"},
	} {
		if code := n.errorCode("settings.set", bad); code != "INVALID" {
			t.Fatalf("%v: %s", bad, code)
		}
	}
	all := n.call("settings.get", nil)["settings"].(map[string]any)
	if all["ssh_agent"] != "off" || all["ssh_key_ttl_s"] != "14400" || all["ssh_allow_forwarding"] != "off" || all["ssh_agent_pipe"] == "" {
		t.Fatalf("defaults: %v", all)
	}
	name := enableAgent(t, n)
	n.call("settings.set", map[string]any{"key": "ssh_agent", "value": "off"})
	n.waitEvent("ssh.agent", map[string]any{"state": "stopped"})
	if _, err := pipe.Dial(name); err == nil {
		t.Fatal("pipe still open after turning the agent off")
	}

	// Someone else owns the pipe: the agent reports a conflict and does not take it over.
	taken := testAgentPipe(t)
	l, err := pipe.Listen(taken)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	n.call("settings.set", map[string]any{"key": "ssh_agent_pipe", "value": taken})
	n.call("settings.set", map[string]any{"key": "ssh_agent", "value": "on"})
	n.waitEvent("ssh.agent", map[string]any{"state": "conflict", "pipe": taken})
	if st := n.call("ssh.status", nil); st["state"] != "conflict" || st["error"] == "" {
		t.Fatalf("status: %v", st)
	}
	if st := n.call("ssh.selftest", nil); st["ok"] != false {
		t.Fatalf("selftest during conflict: %v", st)
	}
}

func TestSSHAgentUnsupportedOnAndroid(t *testing.T) {
	n := startNode(t, "Pixel", "android")
	n.call("settings.set", map[string]any{"key": "ssh_agent", "value": "on"})
	if st := n.call("ssh.status", nil); st["supported"] != false || st["state"] != "unsupported" {
		t.Fatalf("android: %v", st)
	}
}

func TestSSHShutdownWithConnectedClient(t *testing.T) {
	n := startNode(t, "Desk", "windows")
	name := enableAgent(t, n)
	a := agentClient(t, name)
	if _, err := a.List(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { n.c.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown hung with an agent client connected")
	}
}

// TestSSHAgentWithOpenSSH drives the agent with the real OpenSSH tools:
// ssh-add -l lists the key and ssh-keygen -Y sign signs through the agent
// with only the public key on disk. Set CB_SSH_E2E=1 to run it.
func TestSSHAgentWithOpenSSH(t *testing.T) {
	if os.Getenv("CB_SSH_E2E") == "" {
		t.Skip("set CB_SSH_E2E=1 to run against the installed OpenSSH")
	}
	tool := func(name string) string {
		if runtime.GOOS == "windows" {
			// The Windows build talks to named pipes; Git's MSYS copy earlier on PATH does not.
			if p := filepath.Join(os.Getenv("WINDIR"), "System32", "OpenSSH", name+".exe"); fileExists(p) {
				return p
			}
		}
		p, err := exec.LookPath(name)
		if err != nil {
			t.Skip("OpenSSH tools not found")
		}
		return p
	}
	sshAdd, keygen := tool("ssh-add"), tool("ssh-keygen")
	n := startNode(t, "Desk", "windows")
	dir := t.TempDir()
	knownHosts := filepath.Join(dir, "kh")
	n.c.ssh.known = sshagent.NewKnownHosts(knownHosts)
	name := enableAgent(t, n)
	vcreate(t, n)
	gen := n.call("ssh.generate", map[string]any{"type": "ed25519", "title": "e2e"})
	n.waitEvent("ssh.keys", map[string]any{"state": "loaded", "count": float64(1)})

	env := append(os.Environ(), "SSH_AUTH_SOCK="+name)
	out, err := (&exec.Cmd{Path: sshAdd, Args: []string{sshAdd, "-l"}, Env: env}).CombinedOutput()
	if err != nil || !strings.Contains(string(out), gen["fingerprint"].(string)) {
		t.Fatalf("ssh-add -l: %v\n%s", err, out)
	}
	pubPath := filepath.Join(dir, "id.pub")
	n.call("ssh.write_pub", map[string]any{"path": pubPath, "id": gen["id"]})
	msg := filepath.Join(dir, "msg.txt")
	_ = os.WriteFile(msg, []byte("signed through ClipBridge\n"), 0o600)
	cmd := &exec.Cmd{Path: keygen, Args: []string{keygen, "-Y", "sign", "-f", pubPath, "-n", "file", msg}, Env: env}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen -Y sign: %v\n%s", err, out)
	}
	pubLine, _ := os.ReadFile(pubPath)
	allowed := filepath.Join(dir, "allowed")
	_ = os.WriteFile(allowed, []byte("e2e@test "+string(pubLine)), 0o600)
	verify := &exec.Cmd{Path: keygen, Args: []string{keygen, "-Y", "verify", "-f", allowed, "-I", "e2e@test", "-n", "file", "-s", msg + ".sig"}}
	f, _ := os.Open(msg)
	defer f.Close()
	verify.Stdin = f
	if out, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen -Y verify: %v\n%s", err, out)
	}
	used := n.waitEvent("ssh.used", map[string]any{"kind": "sshsig"})
	if used["namespace"] != "file" {
		t.Fatalf("sshsig use: %v", used)
	}

	// Log in to a throwaway server with ssh, the private key nowhere on disk: IdentityFile names a
	// file that does not exist, next to the .pub written above, and IdentitiesOnly is on. This is how
	// an existing ~/.ssh/config keeps working after its private keys move into the vault.
	sshExe := tool("ssh")
	authPub, _, _, _, _ := ssh.ParseAuthorizedKey(pubLine)
	host, addr := startTestSSHServer(t, authPub)
	id := filepath.Join(dir, "id") // id does not exist, id.pub does
	configPath := filepath.Join(dir, "config")
	config := "Host e2e-office\n  HostName " + addr.host + "\n  Port " + addr.port +
		"\n  HostKeyAlias e2e-office-key\n  User tester\n  IdentitiesOnly yes\n  IdentityFile \"" + filepath.ToSlash(id) + "\"\n"
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	scan := n.call("ssh.scan", map[string]any{"dir": dir})
	hosts := scan["config_hosts"].([]any)
	if len(hosts) != 1 || hosts[0].(map[string]any)["aliases"].([]any)[0] != "e2e-office" || len(scan["items"].([]any)) != 0 {
		t.Fatalf("scan with only a public key on disk: %v", scan)
	}
	login := &exec.Cmd{Path: sshExe, Env: env, Args: []string{sshExe, "-F", configPath, "-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=" + knownHosts, "e2e-office", "true"}}
	if out, err := login.CombinedOutput(); err != nil {
		t.Fatalf("ssh login through the agent: %v\n%s", err, out)
	}
	auth := n.waitEvent("ssh.used", map[string]any{"kind": "userauth"})
	if auth["user"] != "tester" || auth["host_key_fp"] != ssh.FingerprintSHA256(host) {
		t.Fatalf("userauth use (session-bind expected to name the server): %v", auth)
	}

	// Host binding with the real client: bound to some other server, the key is not offered and
	// the login fails; bound using the imported alias's lookup name, it works again.
	other, _ := sshkey.Generate("ed25519", 0, "elsewhere")
	otherPub, _ := ssh.NewPublicKey(other.Signer.Public())
	n.call("ssh.set_hosts", map[string]any{"id": gen["id"], "hosts": []string{ssh.FingerprintSHA256(otherPub)}})
	time.Sleep(600 * time.Millisecond) // the agent reloads 200 ms after the save
	again := &exec.Cmd{Path: sshExe, Env: env, Args: login.Args}
	if out, err := again.CombinedOutput(); err == nil {
		t.Fatalf("login with a key bound to another host succeeded:\n%s", out)
	}
	targets := n.call("ssh.config_targets", map[string]any{"path": configPath})["targets"].([]any)
	if len(targets) != 1 || targets[0].(map[string]any)["known_host"] != "e2e-office-key" {
		t.Fatalf("config targets: %v", targets)
	}
	n.call("ssh.set_hosts", map[string]any{"id": gen["id"], "hosts": []string{"known_hosts:e2e-office-key"}})
	time.Sleep(600 * time.Millisecond)
	third := &exec.Cmd{Path: sshExe, Env: env, Args: login.Args}
	if out, err := third.CombinedOutput(); err != nil {
		t.Fatalf("login with the key bound to this host: %v\n%s", err, out)
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

type hostPort struct{ host, port string }

// startTestSSHServer accepts one public key and answers every exec with
// exit status 0.
func startTestSSHServer(t *testing.T, allowed ssh.PublicKey) (ssh.PublicKey, hostPort) {
	t.Helper()
	hostKey, err := sshkey.Generate("ed25519", 0, "test host")
	if err != nil {
		t.Fatal(err)
	}
	signer, _ := ssh.NewSignerFromKey(hostKey.Signer)
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if bytes.Equal(k.Marshal(), allowed.Marshal()) {
			return nil, nil
		}
		return nil, errors.New("no")
	}}
	cfg.AddHostKey(signer)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			nc, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer nc.Close()
				_, chans, reqs, err := ssh.NewServerConn(nc, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nch := range chans {
					ch, creqs, err := nch.Accept()
					if err != nil {
						continue
					}
					go func() {
						for r := range creqs {
							_ = r.Reply(r.Type == "exec", nil)
							if r.Type == "exec" {
								_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ S uint32 }{0}))
								_ = ch.Close()
							}
						}
					}()
				}
			}()
		}
	}()
	h, p, _ := net.SplitHostPort(l.Addr().String())
	return signer.PublicKey(), hostPort{h, p}
}

func TestSSHHostBindingThroughTheFacade(t *testing.T) {
	n := startNode(t, "Desk", "windows")
	name := enableAgent(t, n)
	vcreate(t, n)
	bound := n.call("ssh.generate", map[string]any{"title": "Bound"})
	free := n.call("ssh.generate", map[string]any{"title": "Free"})
	waitFor(t, "both keys loaded", func() bool { return len(listFPs(t, name)) == 2 })

	if code := n.errorCode("ssh.set_hosts", map[string]any{"id": bound["id"], "hosts": []string{"two words"}}); code != "INVALID" {
		t.Fatalf("bad pattern: %s", code)
	}
	host, _ := sshkey.Generate("ed25519", 0, "host")
	signer, _ := ssh.NewSignerFromKey(host.Signer)
	fp := ssh.FingerprintSHA256(signer.PublicKey())
	set := n.call("ssh.set_hosts", map[string]any{"id": bound["id"], "hosts": []string{" github.com ", fp, "github.com"}})
	if hosts := set["hosts"].([]any); len(hosts) != 2 {
		t.Fatalf("hosts not trimmed and deduplicated: %v", hosts)
	}
	if v := n.call("vault.get", map[string]any{"id": bound["id"]})["ssh"].(map[string]any); len(v["hosts"].([]any)) != 2 {
		t.Fatalf("vault.get hosts: %v", v)
	}

	bindAndList := func(s ssh.Signer) []string {
		a := agentClient(t, name)
		sig, _ := s.Sign(rand.Reader, []byte("session"))
		msg := ssh.Marshal(struct {
			HostKey, SessionID, Sig []byte
			Fwd                     bool
		}{s.PublicKey().Marshal(), []byte("session"), ssh.Marshal(sig), false})
		if _, err := a.Extension("session-bind@openssh.com", msg); err != nil {
			t.Fatal(err)
		}
		keys, err := a.List()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, k := range keys {
			out = append(out, k.Comment)
		}
		return out
	}
	waitFor(t, "binding loaded", func() bool { return strings.Join(bindAndList(signer), ",") != "" })
	if got := bindAndList(signer); len(got) != 2 {
		t.Fatalf("to the bound host: %v", got)
	}
	other, _ := sshkey.Generate("ed25519", 0, "other")
	otherSigner, _ := ssh.NewSignerFromKey(other.Signer)
	waitFor(t, "bound key hidden from other hosts", func() bool {
		got := bindAndList(otherSigner)
		return len(got) == 1 && got[0] == "Free"
	})
	if fps := listFPs(t, name); len(fps) != 2 {
		t.Fatalf("an unbound connection (ssh-add -l) lists every key: %v", fps)
	}

	n.call("ssh.set_hosts", map[string]any{"id": bound["id"], "hosts": []string{}})
	waitFor(t, "limit lifted", func() bool { return len(bindAndList(otherSigner)) == 2 })
	_ = free
}
