package facade

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const vpw = "a long master pass phrase for tests"

func vcreate(t *testing.T, n *node) {
	t.Helper()
	n.call("vault.create", map[string]any{"password": vpw, "kdf": "low"})
}

func TestVaultLifecycleThroughTheFacade(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	if st := n.call("vault.status", nil); st["exists"] != false || st["unlocked"] != false {
		t.Fatalf("fresh status = %v", st)
	}
	if code := n.errorCode("vault.list", nil); code != "VAULT_LOCKED" {
		t.Fatalf("list while there is no vault: %s", code)
	}
	if code := n.errorCode("vault.create", map[string]any{"password": "short"}); code != "INVALID" {
		t.Fatalf("a 5 character master password: %s", code)
	}
	vcreate(t, n)
	if code := n.errorCode("vault.create", map[string]any{"password": "another long password"}); code != "VAULT_EXISTS" {
		t.Fatalf("second create: %s", code)
	}
	st := n.call("vault.status", nil)
	if st["key_file"] != false || st["kdf"].(map[string]any)["memory_mib"].(float64) != 32 {
		t.Fatalf("key_file / kdf in status: %v", st)
	}
	if st["exists"] != true || st["unlocked"] != true || st["autolock_s"].(float64) != 300 {
		t.Fatalf("status = %v", st)
	}

	add := n.call("vault.add", map[string]any{"entry": map[string]any{
		"kind": "apikey", "title": "Stripe live", "username": "acct_1", "password": "sk_live_TOPSECRET", "url": "https://api.stripe.com",
		"fields": []map[string]any{{"name": "Webhook", "value": "whsec_TOPSECRET2", "protected": true}, {"name": "Env", "value": "prod"}},
		"totp":   "JBSWY3DPEHPK3PXP",
	}})
	id := add["id"].(string)

	// listings and details never carry secrets
	raw := n.c.Call("vault.get", `{"id":"`+id+`"}`)
	rawList := n.c.Call("vault.list", `{}`)
	for _, out := range []string{raw, rawList} {
		if strings.Contains(out, "TOPSECRET") || strings.Contains(out, "JBSWY3DP") {
			t.Fatalf("a secret leaked into: %s", out)
		}
	}
	got := n.call("vault.get", map[string]any{"id": id})
	if got["has_password"] != true || got["has_totp"] != true || got["title"] != "Stripe live" {
		t.Fatalf("get = %v", got)
	}
	var protectedSeen, plainSeen bool
	for _, f := range got["fields"].([]any) {
		fm := f.(map[string]any)
		if fm["name"] == "Webhook" && fm["value"] == "" && fm["has_value"] == true && fm["protected"] == true {
			protectedSeen = true
		}
		if fm["name"] == "Env" && fm["value"] == "prod" {
			plainSeen = true
		}
	}
	if !protectedSeen || !plainSeen {
		t.Fatalf("fields = %v", got["fields"])
	}
	if r := n.call("vault.reveal", map[string]any{"id": id, "field": "password"}); r["value"] != "sk_live_TOPSECRET" {
		t.Fatalf("reveal = %v", r)
	}
	if r := n.call("vault.reveal", map[string]any{"id": id, "field": "field:Webhook"}); r["value"] != "whsec_TOPSECRET2" {
		t.Fatalf("reveal field = %v", r)
	}
	if r := n.call("vault.totp", map[string]any{"id": id}); len(r["code"].(string)) != 6 {
		t.Fatalf("totp = %v", r)
	}
	if code := n.errorCode("vault.reveal", map[string]any{"id": id, "field": "bogus"}); code != "INVALID" {
		t.Fatalf("bogus field: %s", code)
	}

	// lock / unlock
	n.call("vault.lock", nil)
	n.waitEvent("vault.locked", map[string]any{"reason": "user"})
	for _, m := range []string{"vault.list", "vault.get", "vault.reveal", "vault.add", "vault.health", "vault.groups"} {
		if code := n.errorCode(m, map[string]any{"id": id}); code != "VAULT_LOCKED" {
			t.Fatalf("%s while locked: %s", m, code)
		}
	}
	if code := n.errorCode("vault.unlock", map[string]any{"password": "wrong"}); code != "VAULT_WRONG_CREDENTIALS" {
		t.Fatalf("wrong password: %s", code)
	}
	n.call("vault.unlock", map[string]any{"password": vpw})
	if l := n.call("vault.list", nil)["entries"].([]any); len(l) != 1 {
		t.Fatalf("entries after unlock: %v", l)
	}
	if r := n.call("vault.health", nil); r["total"].(float64) != 1 {
		t.Fatalf("health = %v", r)
	}
	// the vault file is encrypted: none of the secrets are in it
	data, _ := os.ReadFile(filepath.Join(n.c.vaultDir(), "vault.kdbx"))
	for _, s := range []string{"TOPSECRET", "Stripe", "api.stripe.com"} {
		if strings.Contains(string(data), s) {
			t.Fatalf("%q is readable in the vault file", s)
		}
	}
}

func TestVaultUnlockIsRateLimitedAfterThreeWrongAttempts(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	vcreate(t, n)
	n.call("vault.lock", nil)
	for i := 0; i < 3; i++ {
		if code := n.errorCode("vault.unlock", map[string]any{"password": "nope"}); code != "VAULT_WRONG_CREDENTIALS" {
			t.Fatalf("attempt %d: %s", i, code)
		}
	}
	r := n.try("vault.unlock", map[string]any{"password": vpw}) // the right one, but too soon
	if r.OK || r.Error.Code != "RATE_LIMITED" {
		t.Fatalf("expected a wait, got %+v", r)
	}
	if st := n.call("vault.status", nil); st["retry_after_s"].(float64) < 1 {
		t.Fatalf("status shows no wait: %v", st)
	}
	n.c.vs.mu.Lock()
	n.c.vs.blockTil = time.Time{}
	n.c.vs.mu.Unlock()
	n.call("vault.unlock", map[string]any{"password": vpw})
	if st := n.call("vault.status", nil); st["retry_after_s"].(float64) != 0 {
		t.Fatalf("success must reset the counter: %v", st)
	}
}

func TestVaultLocksItselfWhenIdle(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	vcreate(t, n)
	if code := n.errorCode("settings.set", map[string]any{"key": "vault_autolock_s", "value": "5"}); code != "INVALID" {
		t.Fatalf("autolock below the minimum accepted: %s", code)
	}
	if code := n.errorCode("settings.set", map[string]any{"key": "vault_clip_clear_s", "value": "2"}); code != "INVALID" {
		t.Fatalf("clip clear below the minimum accepted: %s", code)
	}
	// 0 means "never": the core must not arm a timer and status must expose it.
	n.call("settings.set", map[string]any{"key": "vault_autolock_s", "value": "0"})
	if v := n.call("vault.status", nil)["autolock_s"].(float64); v != 0 {
		t.Fatalf("never autolock status = %v", v)
	}
	n.call("vault.list", nil) // must not be locked by a zero timer
	n.call("settings.set", map[string]any{"key": "vault_autolock_s", "value": "30"})
	if v := n.call("settings.get", map[string]any{"key": "vault_autolock_s"})["value"]; v != "30" {
		t.Fatalf("setting = %v", v)
	}
	// the tests cannot wait 30 s: shorten the stored value directly
	_ = n.c.db.SetSetting(settingVaultAutoLock, "1")
	n.call("vault.list", nil) // activity re-arms the timer with the new value
	n.waitEvent("vault.locked", map[string]any{"reason": "idle"})
	if code := n.errorCode("vault.list", nil); code != "VAULT_LOCKED" {
		t.Fatalf("after idle lock: %s", code)
	}
}

func TestVaultCopyIsNeverBroadcast(t *testing.T) {
	win := startNode(t, "Tony-PC", "windows", true)
	pix := startNode(t, "Pixel", "android", false)
	pairNodes(t, win, pix)
	vcreate(t, win)
	id := win.call("vault.add", map[string]any{"entry": map[string]any{"title": "bank", "username": "me", "password": "Tr0ub4dor&3-bank-secret"}})["id"].(string)

	cp := win.call("vault.copy", map[string]any{"id": id, "field": "password"})
	if cp["value"] != "Tr0ub4dor&3-bank-secret" || cp["clear_after_s"].(float64) != 20 {
		t.Fatalf("copy = %v", cp)
	}
	// the shell now writes the clipboard; the change notification must not send it anywhere
	res := win.call("clip.local_changed", map[string]any{"text": "Tr0ub4dor&3-bank-secret"})
	if res["suppressed"] != "vault_origin" || len(res["sent"].([]any)) != 0 {
		t.Fatalf("a secret copied from the vault was broadcast: %v", res)
	}
	time.Sleep(150 * time.Millisecond)
	if ev := pix.find("clip.received", nil); ev != nil {
		t.Fatalf("the other device received it: %v", ev)
	}
	// the username was copied by hand: also not broadcast; ordinary text is
	win.call("vault.copy", map[string]any{"id": id, "field": "username"})
	if res := win.call("clip.local_changed", map[string]any{"text": "me"}); res["suppressed"] != "vault_origin" {
		t.Fatalf("username copied from the vault: %v", res)
	}
	if res := win.call("clip.local_changed", map[string]any{"text": "just some other text"}); len(res["sent"].([]any)) != 1 {
		t.Fatalf("ordinary copies must still be broadcast: %v", res)
	}
	// reveal (display on screen, no clipboard) registers nothing
	win.call("vault.reveal", map[string]any{"id": id, "field": "notes"})
}

func TestVaultExportNeedsTheMasterPasswordAndNeverOverwrites(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	vcreate(t, n)
	id := n.call("vault.add", map[string]any{"entry": map[string]any{"title": "t", "password": "p1"}})["id"].(string)
	dir := t.TempDir()
	kdbx := filepath.Join(dir, "out.kdbx")
	if code := n.errorCode("vault.export_file", map[string]any{"path": kdbx, "password": "export pw", "confirm_password": "wrong"}); code != "VAULT_WRONG_CREDENTIALS" {
		t.Fatalf("export with a wrong confirmation: %s", code)
	}
	if _, err := os.Stat(kdbx); err == nil {
		t.Fatal("a file was written despite the wrong confirmation")
	}
	n.call("vault.export_file", map[string]any{"path": kdbx, "password": "export pw", "confirm_password": vpw, "kdf": "low"})
	if code := n.errorCode("vault.export_file", map[string]any{"path": kdbx, "password": "export pw", "confirm_password": vpw}); code != "VAULT_EXISTS" {
		t.Fatalf("overwrite: %s", code)
	}
	csv := filepath.Join(dir, "out.csv")
	n.call("vault.export_csv", map[string]any{"path": csv, "confirm_password": vpw})
	if b, _ := os.ReadFile(csv); !strings.Contains(string(b), "p1") {
		t.Fatalf("csv = %q", b)
	}
	// import the KDBX back into a second vault
	m := startNode(t, "Other", "windows", false)
	m.call("vault.create", map[string]any{"password": "other master", "kdf": "low"})
	r := m.call("vault.import_file", map[string]any{"path": kdbx, "password": "export pw"})
	if r["stats"].(map[string]any)["entries_added"].(float64) != 1 {
		t.Fatalf("import = %v", r)
	}
	if got := m.call("vault.reveal", map[string]any{"id": id, "field": "password"}); got["value"] != "p1" {
		t.Fatalf("imported password = %v", got)
	}
	m.call("vault.import_csv", map[string]any{"path": csv})
}

func TestVaultBackupsAndRestoreThroughTheFacade(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	vcreate(t, n)
	id := n.call("vault.add", map[string]any{"entry": map[string]any{"title": "keep me", "password": "orig"}})["id"].(string)
	snap := n.call("vault.backup_now", nil)
	name := snap["name"].(string)
	n.call("vault.update", map[string]any{"id": id, "entry": map[string]any{"title": "keep me", "password": "changed"}})
	if chk := n.call("vault.backup_verify", map[string]any{"name": name, "password": vpw}); chk["entries"].(float64) != 1 {
		t.Fatalf("verify = %v", chk)
	}
	if code := n.errorCode("vault.backup_verify", map[string]any{"name": name, "password": "wrong"}); code != "VAULT_WRONG_CREDENTIALS" {
		t.Fatalf("verify with a wrong password: %s", code)
	}
	if code := n.errorCode("vault.backup_verify", map[string]any{"name": "../../etc/passwd", "password": vpw}); code != "INVALID" {
		t.Fatalf("path traversal: %s", code)
	}
	prev := n.call("vault.restore_preview", map[string]any{"name": name})
	if len(prev["changed"].([]any)) != 1 {
		t.Fatalf("preview = %v", prev)
	}
	n.call("vault.restore", map[string]any{"name": name, "mode": "entries", "ids": []string{id}})
	if r := n.call("vault.reveal", map[string]any{"id": id, "field": "password"}); r["value"] != "orig" {
		t.Fatalf("after restore: %v", r)
	}
	if l := n.call("vault.backup_list", nil)["backups"].([]any); len(l) < 2 {
		t.Fatalf("backups = %v", l)
	}
}

func TestVaultChangeMasterAndGenerators(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	vcreate(t, n)
	n.call("vault.add", map[string]any{"entry": map[string]any{"title": "t", "password": "p"}})
	if code := n.errorCode("vault.change_master", map[string]any{"password": "wrong", "new": map[string]any{"password": "n"}}); code != "VAULT_WRONG_CREDENTIALS" {
		t.Fatalf("change with a wrong current password: %s", code)
	}
	key := base64.StdEncoding.EncodeToString([]byte("a key file's bytes"))
	n.call("vault.change_master", map[string]any{"password": vpw, "new": map[string]any{"password": "brand new master", "key_file_b64": key}})
	n.call("vault.lock", nil)
	if code := n.errorCode("vault.unlock", map[string]any{"password": "brand new master"}); code != "VAULT_WRONG_CREDENTIALS" {
		t.Fatalf("the key file is part of the credentials: %s", code)
	}
	n.c.vs.mu.Lock()
	n.c.vs.blockTil = time.Time{}
	n.c.vs.mu.Unlock()
	n.call("vault.unlock", map[string]any{"password": "brand new master", "key_file_b64": key})

	g := n.call("vault.generate", map[string]any{"type": "password", "length": 24, "exclude_ambiguous": true})
	if len(g["value"].(string)) != 24 || g["strength"].(map[string]any)["label"] != "strong" {
		t.Fatalf("generate = %v", g)
	}
	pp := n.call("vault.generate", map[string]any{"type": "passphrase", "words": 5, "separator": " "})
	if len(strings.Fields(pp["value"].(string))) != 5 {
		t.Fatalf("passphrase = %v", pp)
	}
	if code := n.errorCode("vault.generate", map[string]any{"type": "nope"}); code != "INVALID" {
		t.Fatalf("generate type: %s", code)
	}
	if code := n.errorCode("vault.generate", map[string]any{"length": 2}); code != "INVALID" {
		t.Fatalf("short password: %s", code)
	}
}

// Whatever happens, no response may repeat a password or master key that was sent in.
func TestVaultResponsesNeverEchoSecretsOrParams(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	vcreate(t, n)
	probe := "ZZ-never-echo-ZZ"
	for _, c := range []struct {
		m string
		p map[string]any
	}{
		{"vault.unlock", map[string]any{"password": probe}},
		{"vault.verify_master", map[string]any{"password": probe}},
		{"vault.change_master", map[string]any{"password": probe, "new": map[string]any{"password": probe}}},
		{"vault.add", map[string]any{"entry": map[string]any{"title": "", "password": probe}}},
		{"vault.import_file", map[string]any{"path": "/nonexistent", "password": probe}},
		{"vault.restore", map[string]any{"name": "vault.kdbx.prev", "mode": "bogus", "password": probe}},
	} {
		b, _ := json.Marshal(c.p)
		if out := n.c.Call(c.m, string(b)); strings.Contains(out, probe) {
			t.Errorf("%s echoed a secret: %s", c.m, out)
		}
	}
}

func TestVaultMatchForAutofill(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	vcreate(t, n)
	n.call("vault.add", map[string]any{"entry": map[string]any{"title": "GitHub", "url": "https://github.com/login", "username": "me", "password": "gh-secret"}})
	n.call("vault.add", map[string]any{"entry": map[string]any{"title": "Phish bait", "url": "https://github.com.evil.example", "password": "x"}})
	raw := n.c.Call("vault.match", `{"url":"https://gist.github.com/x"}`)
	if strings.Contains(raw, "gh-secret") {
		t.Fatalf("a secret leaked through match: %s", raw)
	}
	m := n.call("vault.match", map[string]any{"url": "https://gist.github.com/x"})["matches"].([]any)
	if len(m) != 1 || m[0].(map[string]any)["title"] != "GitHub" || m[0].(map[string]any)["score"].(float64) != 2 {
		t.Fatalf("matches = %v", m)
	}
	n.call("vault.lock", nil)
	if code := n.errorCode("vault.match", map[string]any{"url": "https://github.com"}); code != "VAULT_LOCKED" {
		t.Fatalf("match while locked: %s", code)
	}
}

func TestVaultTotpDoesNotKeepAnUnattendedVaultUnlocked(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	vcreate(t, n)
	id := n.call("vault.add", map[string]any{"entry": map[string]any{"title": "t", "totp": "JBSWY3DPEHPK3PXP"}})["id"].(string)
	_ = n.c.db.SetSetting(settingVaultAutoLock, "1")
	n.call("vault.list", nil) // re-arm with 1 s
	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) { // polling the one-time code must not postpone the lock
		n.call("vault.totp", map[string]any{"id": id})
		time.Sleep(100 * time.Millisecond)
	}
	n.waitEvent("vault.locked", map[string]any{"reason": "idle"})

	n.call("vault.unlock", map[string]any{"password": vpw})
	n.call("vault.list", nil)
	for i := 0; i < 8; i++ { // "still here" does
		time.Sleep(250 * time.Millisecond)
		n.call("vault.touch", nil)
	}
	if st := n.call("vault.status", nil); st["unlocked"] != true {
		t.Fatalf("vault.touch must keep the vault open: %v", st)
	}
}

func TestVaultGroupDeleteNotEmptyCode(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	vcreate(t, n)
	gid := n.call("vault.group_add", map[string]any{"name": "G"})["id"].(string)
	n.call("vault.add", map[string]any{"entry": map[string]any{"title": "x", "group_id": gid}})
	if code := n.errorCode("vault.group_delete", map[string]any{"id": gid}); code != "VAULT_NOT_EMPTY" {
		t.Fatalf("code = %s", code)
	}
}

func TestVaultPatchChangesOnlyWhatIsGiven(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	vcreate(t, n)
	id := n.call("vault.add", map[string]any{"entry": map[string]any{
		"title": "site", "username": "me", "password": "old", "totp": "JBSWY3DPEHPK3PXP",
		"fields": []map[string]any{{"name": "Key", "value": "keep-me", "protected": true}},
	}})["id"].(string)
	n.call("vault.patch", map[string]any{"id": id, "password": "new"})
	if r := n.call("vault.reveal", map[string]any{"id": id, "field": "password"}); r["value"] != "new" {
		t.Fatalf("password = %v", r)
	}
	if r := n.call("vault.reveal", map[string]any{"id": id, "field": "field:Key"}); r["value"] != "keep-me" {
		t.Fatalf("a patch wiped a protected field: %v", r)
	}
	if r := n.call("vault.reveal", map[string]any{"id": id, "field": "totp_secret"}); r["value"] != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("a patch wiped the totp secret: %v", r)
	}
	got := n.call("vault.get", map[string]any{"id": id})
	if got["username"] != "me" || got["history"].(float64) != 1 {
		t.Fatalf("get = %v", got)
	}
	n.call("vault.patch", map[string]any{"id": id, "tags": []string{"a"}, "title": "renamed"})
	if code := n.errorCode("vault.patch", map[string]any{"id": id, "title": ""}); code != "INVALID" {
		t.Fatalf("empty title: %s", code)
	}
}

// ---- regression tests for the adversarial review ---------------------------------------------

func TestVaultStatusDoesNotKeepTheVaultOpenAndAWallClockGapLocks(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	vcreate(t, n)
	_ = n.c.db.SetSetting(settingVaultAutoLock, "1")
	n.call("vault.list", nil)
	deadline := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(deadline) {
		n.call("vault.status", nil) // polling the status is not activity
		time.Sleep(100 * time.Millisecond)
	}
	n.waitEvent("vault.locked", map[string]any{"reason": "idle"})

	// a laptop that slept: the timer did not fire, the wall clock says the time is up
	_ = n.c.db.SetSetting(settingVaultAutoLock, "30")
	n.call("vault.unlock", map[string]any{"password": vpw})
	n.c.vs.mu.Lock()
	n.c.vs.lastActive = time.Now().Add(-time.Hour).Round(0)
	n.c.vs.mu.Unlock()
	if code := n.errorCode("vault.list", nil); code != "VAULT_LOCKED" {
		t.Fatalf("after an hour without activity: %s", code)
	}
}

func TestVaultGuessingCannotBeParallelisedOrResetByOtherCalls(t *testing.T) {
	n := startNode(t, "Desk", "windows", false)
	vcreate(t, n)
	n.call("vault.backup_now", nil)
	name := n.call("vault.backup_list", nil)["backups"].([]any)[0].(map[string]any)["name"].(string)
	n.call("vault.lock", nil)
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[string]int{}
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := n.try("vault.unlock", map[string]any{"password": "wrong password " + time.Now().String()})
			mu.Lock()
			codes[r.Error.Code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes["VAULT_WRONG_CREDENTIALS"] > 3 || codes["RATE_LIMITED"] < 20 {
		t.Fatalf("24 parallel guesses got through: %v", codes)
	}
	// a known-good call on the side must not reset the counter
	if chk := n.try("vault.backup_verify", map[string]any{"name": name, "password": vpw}); chk.OK || chk.Error.Code != "RATE_LIMITED" {
		t.Fatalf("backup_verify while blocked: %+v", chk)
	}
	n.c.vs.mu.Lock()
	n.c.vs.blockTil = time.Time{}
	n.c.vs.mu.Unlock()
	n.call("vault.backup_verify", map[string]any{"name": name, "password": vpw}) // succeeds, but ...
	n.c.vs.mu.Lock()
	fails := n.c.vs.fails
	n.c.vs.mu.Unlock()
	if fails < 3 {
		t.Fatalf("a successful backup_verify reset the failure counter (%d)", fails)
	}
}

func TestVaultSyncResolveRefusesInventedDeviceIDs(t *testing.T) {
	n := startNode(t, "Desk", "windows", true)
	n2 := startNode(t, "Other", "android", false)
	pairNodes(t, n, n2)
	vcreate(t, n)
	file := filepath.Join(n.c.vaultDir(), "vault.kdbx")
	for _, id := range []string{"../vault", "..\\vault", "dev_x", "", "dev_aaaaaaaaaaaaaaaaaaaaaaaaaa/../../vault"} {
		r := n.try("vault.sync_resolve", map[string]any{"device_id": id, "approve": true, "sha256": "x"})
		if r.OK {
			t.Fatalf("device_id %q was accepted", id)
		}
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("the vault file is gone: %v", err)
	}
}

func TestEverythingTheVaultHandsOutIsKeptOffTheClipboardSync(t *testing.T) {
	win := startNode(t, "Tony-PC", "windows", true)
	pix := startNode(t, "Pixel", "android", false)
	pairNodes(t, win, pix)
	vcreate(t, win)
	id := win.call("vault.add", map[string]any{"entry": map[string]any{"title": "x", "username": "admin-user", "password": "Rev3aled-pass-word", "notes": "secret note text", "totp": "JBSWY3DPEHPK3PXP"}})["id"].(string)
	suppressed := func(text string) bool {
		return win.call("clip.local_changed", map[string]any{"text": text})["suppressed"] == "vault_origin"
	}
	win.call("vault.reveal", map[string]any{"id": id, "field": "password"})
	if !suppressed("Rev3aled-pass-word") {
		t.Fatal("a revealed password that the user copies by hand was broadcast")
	}
	gen := win.call("vault.generate", map[string]any{"type": "password", "length": 20})["value"].(string)
	if !suppressed(gen) {
		t.Fatal("a generated password was broadcast")
	}
	code := win.call("vault.totp", map[string]any{"id": id})["code"].(string)
	if !suppressed(code) {
		t.Fatal("a one-time code was broadcast")
	}
	win.call("vault.get", map[string]any{"id": id})
	if !suppressed("secret note text") {
		t.Fatal("the notes of an open entry were broadcast")
	}
	time.Sleep(150 * time.Millisecond)
	if pix.find("clip.received", nil) != nil {
		t.Fatal("the other device received something from the vault")
	}
}
