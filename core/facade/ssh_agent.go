package facade

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"moonpassword/core/pipe"
	"moonpassword/core/proto"
	"moonpassword/core/sshagent"
	"moonpassword/core/sshkey"
)

// The SSH agent (docs/adr/009-ssh-agent.md). It listens on a pipe only the
// current user can open, offers the keys of vault entries whose
// KeeAgent.settings say "add at database open", signs with them and refuses
// every request to add, remove or lock keys. Signing never touches the vault:
// the agent has its own key set, loaded when the vault unlocks and after each
// save, kept for ssh_key_ttl_s after an idle lock, and dropped on a manual
// lock, a session lock, suspend and shutdown.

const (
	settingSSHAgent      = "ssh_agent"            // on/off
	settingSSHAgentPipe  = "ssh_agent_pipe"       // where the agent listens
	settingSSHKeyTTL     = "ssh_key_ttl_s"        // seconds keys outlive an idle vault lock; 0 = lock with the vault
	settingSSHForwarding = "ssh_allow_forwarding" // on/off: serve requests that arrive through ssh -A
	defaultSSHKeyTTL     = 4 * 3600
	sshRecentMax         = 100
	sshEventRate         = 10 // events per second, with a burst of twice that
	sshKeysNeededEvery   = time.Minute
)

// Agent states reported by ssh.status.
const (
	sshStateListening   = "listening"
	sshStateStopped     = "stopped"
	sshStateConflict    = "conflict" // the pipe name is taken (another agent runs)
	sshStateError       = "error"
	sshStateUnsupported = "unsupported"
)

type sshState struct {
	mu     sync.Mutex
	ring   *sshagent.Keyring
	srv    *sshagent.Server
	pipe   string
	state  string
	err    string
	known  *sshagent.KnownHosts
	parsed map[[32]byte]*sshkey.Key

	recent   []map[string]any
	lastUsed map[string]int64

	loadMu    sync.Mutex
	loadTimer *time.Timer
	grace     *time.Timer

	lastNeeded time.Time
	selfTest   atomic.Int32 // a self-test is connected: its empty listing must not ask the user to unlock
	tokens     float64
	tokensAt   time.Time
	suppressed int
}

func isSSHSetting(key string) bool {
	switch key {
	case settingSSHAgent, settingSSHAgentPipe, settingSSHKeyTTL, settingSSHForwarding:
		return true
	}
	return false
}

func defaultSSHPipe(dataDir string) string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\openssh-ssh-agent`
	}
	return filepath.Join(dataDir, "ssh-agent.sock")
}

func validateSSHSetting(key, value string) error {
	switch key {
	case settingSSHAgent, settingSSHForwarding:
		if value != "on" && value != "off" {
			return proto.Errorf(proto.CodeInvalid, "%s must be on or off", key)
		}
	case settingSSHKeyTTL:
		n, err := strconv.Atoi(value)
		if err != nil || n != 0 && (n < 300 || n > 86400) {
			return proto.Errorf(proto.CodeInvalid, "invalid value for %s (0, or 300 to 86400 seconds)", key)
		}
	case settingSSHAgentPipe:
		if value == "" {
			return nil // back to the default
		}
		if runtime.GOOS == "windows" {
			if !strings.HasPrefix(value, `\\.\pipe\`) || len(value) <= len(`\\.\pipe\`) || len(value) > 256 || strings.ContainsAny(value[len(`\\.\pipe\`):], `\/`) {
				return proto.Errorf(proto.CodeInvalid, `ssh_agent_pipe must look like \\.\pipe\name`)
			}
		} else if !filepath.IsAbs(value) {
			return proto.Errorf(proto.CodeInvalid, "ssh_agent_pipe must be an absolute path")
		}
	}
	return nil
}

func (c *Core) sshSettingValue(key string) string {
	v, found, _ := c.db.GetSetting(key)
	switch key {
	case settingSSHAgent, settingSSHForwarding:
		if !found {
			return "off"
		}
	case settingSSHKeyTTL:
		if !found {
			return strconv.Itoa(defaultSSHKeyTTL)
		}
	case settingSSHAgentPipe:
		if !found || v == "" {
			return defaultSSHPipe(c.cfg.DataDir)
		}
	}
	return v
}

func (c *Core) sshEnabled() bool { return c.sshSettingValue(settingSSHAgent) == "on" }

func (c *Core) sshTTL() time.Duration {
	n, _ := strconv.Atoi(c.sshSettingValue(settingSSHKeyTTL))
	return time.Duration(n) * time.Second
}

func (c *Core) sshSupported() bool { return c.cfg.Platform != "android" }

// sshInit runs at the end of Init (c.mu held, so no c.net()).
func (c *Core) sshInit() {
	c.ssh.ring = sshagent.NewKeyring(nil)
	c.ssh.ring.OnExpire = func() { c.sshKeysEvent("expired", "grace period over") }
	c.ssh.state = sshStateStopped
	if !c.sshSupported() {
		c.ssh.state = sshStateUnsupported
	}
	c.ssh.known = sshagent.NewKnownHosts(knownHostsFiles()...)
	if c.sshSupported() && c.sshEnabled() {
		c.sshStart()
	}
}

// sshStart opens the pipe and serves it. Failures are reported in
// ssh.status and as an ssh.agent event, never fatal.
func (c *Core) sshStart() {
	c.ssh.mu.Lock()
	defer c.ssh.mu.Unlock()
	if c.ssh.srv != nil || !c.sshSupported() {
		return
	}
	name := c.sshSettingValue(settingSSHAgentPipe)
	c.ssh.pipe = name
	l, err := pipe.Listen(name)
	if err != nil {
		c.ssh.state, c.ssh.err = sshStateError, err.Error()
		if pipeTaken(err) {
			c.ssh.state = sshStateConflict
		}
		c.emit(map[string]any{"type": "ssh.agent", "state": c.ssh.state, "pipe": name, "error": c.ssh.err})
		return
	}
	c.ssh.srv = sshagent.Serve(l, c.ssh.ring, sshagent.Hooks{
		AllowForwarding: func() bool { return c.sshSettingValue(settingSSHForwarding) == "on" },
		OnUse:           c.sshOnUse,
		OnKeysNeeded: func(client []sshagent.Proc) {
			if c.ssh.selfTest.Load() > 0 {
				return
			}
			c.sshKeysNeeded(client)
		},
		Client: func(conn io.ReadWriteCloser) []sshagent.Proc {
			pid, ok := pipe.ClientPID(conn)
			if !ok {
				return []sshagent.Proc{}
			}
			return sshagent.ClientChain(pid)
		},
		KnownHosts: c.ssh.known,
	})
	c.ssh.state, c.ssh.err = sshStateListening, ""
	c.emit(map[string]any{"type": "ssh.agent", "state": c.ssh.state, "pipe": name, "error": ""})
	go c.sshLoad()
}

// knownHostsFiles are the files OpenSSH reads by default: the user's and the
// system-wide one.
func knownHostsFiles() []string {
	var files []string
	if home, err := os.UserHomeDir(); err == nil {
		files = append(files, filepath.Join(home, ".ssh", "known_hosts"), filepath.Join(home, ".ssh", "known_hosts2"))
	}
	if runtime.GOOS == "windows" {
		if pd := os.Getenv("ProgramData"); pd != "" {
			files = append(files, filepath.Join(pd, "ssh", "ssh_known_hosts"))
		}
	} else {
		files = append(files, "/etc/ssh/ssh_known_hosts")
	}
	return files
}

// pipeTaken reports whether a listen error means another process owns the name.
func pipeTaken(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) && (errno == 5 || errno == 231) { // ACCESS_DENIED (first-instance flag), PIPE_BUSY
		return true
	}
	return errors.Is(err, os.ErrExist)
}

// sshStop closes the server; with clear it also drops the loaded keys.
func (c *Core) sshStop(clear bool) {
	c.ssh.mu.Lock()
	srv := c.ssh.srv
	c.ssh.srv = nil
	if c.ssh.state == sshStateListening || c.ssh.state == sshStateConflict || c.ssh.state == sshStateError {
		c.ssh.state, c.ssh.err = sshStateStopped, ""
	}
	if c.ssh.loadTimer != nil {
		c.ssh.loadTimer.Stop()
	}
	c.ssh.mu.Unlock()
	if srv != nil {
		srv.Close()
	}
	if clear {
		c.sshClearKeys("agent_off")
	}
}

// sshRestart applies a changed ssh_agent or ssh_agent_pipe setting.
func (c *Core) sshRestart() {
	wasRunning := func() bool { c.ssh.mu.Lock(); defer c.ssh.mu.Unlock(); return c.ssh.srv != nil }()
	on := c.sshSupported() && c.sshEnabled()
	c.sshStop(!on)
	if on {
		c.sshStart()
	} else if wasRunning {
		c.emit(map[string]any{"type": "ssh.agent", "state": sshStateStopped, "pipe": c.sshSettingValue(settingSSHAgentPipe), "error": ""})
	}
}

// onSettingChanged applies settings that need more than being stored.
func (c *Core) onSettingChanged(key string) {
	switch key {
	case settingSSHAgent, settingSSHAgentPipe:
		c.sshRestart()
	}
}

// sshShutdown stops the agent at core shutdown.
func (c *Core) sshShutdown() {
	c.sshStop(false)
	c.ssh.mu.Lock()
	if c.ssh.grace != nil {
		c.ssh.grace.Stop()
	}
	c.ssh.mu.Unlock()
	if c.ssh.ring != nil {
		c.ssh.ring.Clear()
	}
	c.sshDropParsed()
}

func (c *Core) sshDropParsed() {
	c.ssh.mu.Lock()
	c.ssh.parsed = nil
	c.ssh.mu.Unlock()
}

// ---- loading ------------------------------------------------------------------------------------

// sshScheduleLoad reloads the keys shortly; repeated saves collapse into one load.
func (c *Core) sshScheduleLoad() {
	c.ssh.mu.Lock()
	defer c.ssh.mu.Unlock()
	if c.ssh.srv == nil {
		return
	}
	if c.ssh.loadTimer != nil {
		c.ssh.loadTimer.Stop()
	}
	c.ssh.loadTimer = time.AfterFunc(200*time.Millisecond, c.sshLoad)
}

// sshLoad replaces the agent's keys with those of the open vault. Loads run
// one at a time; a lock that happens meanwhile wins (Keyring generation).
func (c *Core) sshLoad() {
	c.ssh.loadMu.Lock()
	defer c.ssh.loadMu.Unlock()
	c.ssh.mu.Lock()
	running := c.ssh.srv != nil
	c.ssh.mu.Unlock()
	if !running || c.ssh.ring == nil {
		return
	}
	gen := c.ssh.ring.Gen()
	v := c.currentVault()
	if v == nil {
		return
	}
	cands, err := v.SSHCandidates("")
	if err != nil {
		return
	}
	now := time.Now().Unix()
	var keys []*sshagent.Key
	seen := map[string]bool{}
	for _, e := range c.sshEvaluate(cands) {
		if e.key == nil || e.cand.InTrash || !e.agentOn() || (e.cand.ExpiresAt > 0 && e.cand.ExpiresAt < now) {
			continue
		}
		fp := e.key.Fingerprint()
		if seen[fp] {
			continue // the same key in two entries is offered once
		}
		seen[fp] = true
		k, err := sshagent.NewKey(e.key.Signer, e.cand.ID, e.cand.Title, e.key.Comment)
		if err != nil {
			continue
		}
		k.Hosts = append([]string(nil), e.cand.Hosts...)
		keys = append(keys, k)
	}
	c.vs.mu.Lock()
	still := c.vs.v == v
	c.vs.mu.Unlock()
	if !still {
		return
	}
	if c.ssh.ring.Replace(gen, keys) {
		c.sshKeysEvent("loaded", "")
	}
}

// sshOnLock is called by lockLocked with vs.mu held; it must not take vs.mu.
// An idle lock with a TTL keeps the keys until lastActive+idle+ttl, so a
// machine that slept past the lock does not get a fresh grace period.
func (c *Core) sshOnLock(reason string, lastActive time.Time, idle time.Duration) {
	if c.ssh.ring == nil {
		return
	}
	ttl := c.sshTTL()
	if reason == "idle" && ttl > 0 {
		base := lastActive
		if base.IsZero() {
			base = time.Now()
		} else {
			base = base.Add(idle)
		}
		until := base.Add(ttl)
		if until.After(time.Now()) && c.ssh.ring.Grace(until) {
			c.ssh.mu.Lock()
			if c.ssh.grace != nil {
				c.ssh.grace.Stop()
			}
			c.ssh.grace = time.AfterFunc(time.Until(until)+time.Second, func() { c.ssh.ring.Snapshot() })
			c.ssh.mu.Unlock()
			go c.sshKeysEvent("grace", reason)
			return
		}
	}
	go c.sshClearKeys(reason)
}

// sshClearKeys drops the agent's keys and reports how many there were.
func (c *Core) sshClearKeys(reason string) int {
	if c.ssh.ring == nil {
		return 0
	}
	n := c.ssh.ring.Clear()
	c.ssh.mu.Lock()
	if c.ssh.grace != nil {
		c.ssh.grace.Stop()
		c.ssh.grace = nil
	}
	c.ssh.mu.Unlock()
	c.sshDropParsed()
	if n > 0 {
		c.sshKeysEvent("cleared", reason)
	}
	return n
}

func (c *Core) sshLoadedKeys() []*sshagent.Key {
	if c.ssh.ring == nil {
		return nil
	}
	keys, _, _ := c.ssh.ring.Snapshot()
	return keys
}

func (c *Core) sshLoaded(fingerprint string) bool {
	for _, k := range c.sshLoadedKeys() {
		if k.Fingerprint == fingerprint {
			return true
		}
	}
	return false
}

func (c *Core) sshLastUsed(fingerprint string) int64 {
	c.ssh.mu.Lock()
	defer c.ssh.mu.Unlock()
	return c.ssh.lastUsed[fingerprint]
}

// ---- events -------------------------------------------------------------------------------------

func (c *Core) sshKeysEvent(state, reason string) {
	keys, st, exp := []*sshagent.Key(nil), sshagent.StateEmpty, time.Time{}
	if c.ssh.ring != nil {
		keys, st, exp = c.ssh.ring.Snapshot()
	}
	ev := map[string]any{"type": "ssh.keys", "state": state, "count": len(keys), "keys_state": st, "reason": reason}
	if !exp.IsZero() {
		ev["expires_at"] = exp.Unix()
	}
	c.emit(ev)
}

// sshEmit rate-limits the per-request events so a local process hammering
// the agent cannot crowd other events out of the queue.
func (c *Core) sshEmit(ev map[string]any) {
	c.ssh.mu.Lock()
	now := time.Now()
	if c.ssh.tokensAt.IsZero() {
		c.ssh.tokens = 2 * sshEventRate
	} else {
		c.ssh.tokens = min(2*sshEventRate, c.ssh.tokens+now.Sub(c.ssh.tokensAt).Seconds()*sshEventRate)
	}
	c.ssh.tokensAt = now
	if c.ssh.tokens < 1 {
		c.ssh.suppressed++
		c.ssh.mu.Unlock()
		return
	}
	c.ssh.tokens--
	if c.ssh.suppressed > 0 {
		ev["suppressed"] = c.ssh.suppressed
		c.ssh.suppressed = 0
	}
	c.ssh.mu.Unlock()
	c.emit(ev)
}

func (c *Core) sshOnUse(u sshagent.Use) {
	view := map[string]any{
		"at": u.At.Unix(), "fingerprint": u.Fingerprint, "title": u.Title, "entry_id": u.EntryID,
		"client": nonNil(u.Client), "user": u.User, "kind": u.Kind, "namespace": u.Namespace,
		"forwarded": u.Forwarded, "result": u.Result, "reason": u.Reason, "host": "", "host_key_fp": "",
	}
	if u.HostKey != nil {
		view["host_key_fp"] = ssh.FingerprintSHA256(u.HostKey)
		if names := c.ssh.known.Names(u.HostKey); len(names) > 0 {
			view["host"] = names[0]
		}
	}
	c.ssh.mu.Lock()
	c.ssh.recent = append(c.ssh.recent, view)
	if len(c.ssh.recent) > sshRecentMax {
		c.ssh.recent = c.ssh.recent[len(c.ssh.recent)-sshRecentMax:]
	}
	if u.Result == sshagent.ResultSigned {
		if c.ssh.lastUsed == nil {
			c.ssh.lastUsed = map[string]int64{}
		}
		c.ssh.lastUsed[u.Fingerprint] = u.At.Unix()
	}
	c.ssh.mu.Unlock()
	ev := map[string]any{"type": "ssh.used"}
	if u.Result != sshagent.ResultSigned {
		ev["type"] = "ssh.refused"
	}
	for k, v := range view {
		ev[k] = v
	}
	c.sshEmit(ev)
}

func (c *Core) sshKeysNeeded(client []sshagent.Proc) {
	c.ssh.mu.Lock()
	if time.Since(c.ssh.lastNeeded) < sshKeysNeededEvery {
		c.ssh.mu.Unlock()
		return
	}
	c.ssh.lastNeeded = time.Now()
	c.ssh.mu.Unlock()
	c.sshEmit(map[string]any{"type": "ssh.keys_needed", "client": nonNil(client)})
}

// ---- status methods -----------------------------------------------------------------------------

func (c *Core) sshRecent(limit int) []map[string]any {
	c.ssh.mu.Lock()
	defer c.ssh.mu.Unlock()
	if limit <= 0 || limit > sshRecentMax {
		limit = sshRecentMax
	}
	out := []map[string]any{}
	for i := len(c.ssh.recent) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, c.ssh.recent[i])
	}
	return out
}

func (c *Core) sshStatus() map[string]any {
	keys, st, exp := []*sshagent.Key(nil), sshagent.StateEmpty, time.Time{}
	if c.ssh.ring != nil {
		keys, st, exp = c.ssh.ring.Snapshot()
	}
	c.ssh.mu.Lock()
	state, errText, pipeName := c.ssh.state, c.ssh.err, c.ssh.pipe
	conns := 0
	if c.ssh.srv != nil {
		conns = c.ssh.srv.Connections()
	}
	c.ssh.mu.Unlock()
	if pipeName == "" {
		pipeName = c.sshSettingValue(settingSSHAgentPipe)
	}
	out := map[string]any{
		"supported": c.sshSupported(), "enabled": c.sshEnabled(), "state": state, "pipe": pipeName, "error": errText,
		"keys_loaded": len(keys), "keys_state": st, "expires_at": int64(0), "vault_unlocked": c.currentVault() != nil,
		"ttl_s": int(c.sshTTL() / time.Second), "allow_forwarding": c.sshSettingValue(settingSSHForwarding) == "on",
		"connections": conns,
	}
	if !exp.IsZero() {
		out["expires_at"] = exp.Unix()
	}
	return out
}

// sshSelfTest connects to our own pipe as a client and lists the keys. It
// never signs.
func (c *Core) sshSelfTest() map[string]any {
	c.ssh.mu.Lock()
	running, name := c.ssh.srv != nil, c.ssh.pipe
	c.ssh.mu.Unlock()
	out := map[string]any{"ok": false, "listed": 0, "fingerprints": []string{}, "error": ""}
	if !running {
		out["error"] = "the agent is not running"
		return out
	}
	c.ssh.selfTest.Add(1)
	defer c.ssh.selfTest.Add(-1)
	conn, err := pipe.Dial(name)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	defer conn.Close()
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = conn.Close()
		}
	}()
	// Only Read and Write: given a Closer the client reads from a second goroutine, and a synchronous
	// Windows pipe handle blocks a write while a read is pending.
	keys, err := agent.NewClient(struct {
		io.Reader
		io.Writer
	}{conn, conn}).List()
	close(done)
	if err != nil {
		out["error"] = err.Error()
		return out
	}
	fps := []string{}
	for _, k := range keys {
		if pub, err := ssh.ParsePublicKey(k.Blob); err == nil {
			fps = append(fps, ssh.FingerprintSHA256(pub))
		}
	}
	out["ok"], out["listed"], out["fingerprints"] = true, len(keys), fps
	return out
}

// sshDiagnostics is the "ssh" block of diagnostics.get (no key material, no titles).
func (c *Core) sshDiagnostics() map[string]any {
	s := c.sshStatus()
	return map[string]any{
		"state": s["state"], "pipe": s["pipe"], "error": s["error"], "keys_loaded": s["keys_loaded"],
		"keys_state": s["keys_state"], "expires_at": s["expires_at"], "enabled": s["enabled"],
	}
}
