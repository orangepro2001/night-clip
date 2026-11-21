package facade

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"moonpassword/core/vault"
	"moonpassword/core/vaultsync"
	moonsso "sso.moonl.cloud/sdk/go"
)

func TestCloudSyncJoinsIndependentVaultsAndMergesOfflineEdits(t *testing.T) {
	var offline atomic.Bool
	service, err := vaultsync.NewServer(t.TempDir(), func(_ context.Context, token string) (string, error) {
		subject, ok := strings.CutPrefix(token, "signed-for-")
		if !ok || subject == "" {
			return "", errors.New("unauthenticated")
		}
		return subject, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if offline.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		service.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	pair := func(name, platform, password, subject string) *node {
		t.Helper()
		n := startNode(t, name, platform)
		n.c.cloud.close()
		n.c.cloud = newCloudSync(n.c, server.URL)
		a := n.c.account
		a.mu.Lock()
		err := a.ensureSession()
		if err == nil {
			err = a.session.Begin(a.ctx, &moonsso.Token{
				AccessToken: "signed-for-" + subject, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
			}, moonsso.Claims{"sub": subject})
		}
		a.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		n.call("vault.create", map[string]any{"password": password, "kdf": "low"})
		return n
	}
	pc := pair("PC", "windows", "pc-master-key", "owner")
	phone := pair("Phone", "android", "phone-master-key", "owner")
	other := pair("Other", "android", "other-master-key", "other")
	add := func(n *node, title string) {
		n.call("vault.add", map[string]any{"entry": map[string]any{"kind": "login", "title": title, "password": "sensitive-fixture"}})
	}
	titles := func(n *node) map[string]bool {
		values := n.call("vault.list", nil)["entries"].([]any)
		result := map[string]bool{}
		for _, raw := range values {
			item := raw.(map[string]any)
			result[item["title"].(string)] = true
		}
		return result
	}
	add(pc, "First device")
	if result := pc.call("sync.enable", map[string]any{"mode": "create"}); result["error"] != nil {
		t.Fatalf("create failed: %v", result)
	}
	key := pc.call("sync.recovery_key", nil)["key"].(string)
	if code := other.errorCode("sync.enable", map[string]any{"mode": "join", "key": key}); code != "SYNC_MISSING" {
		t.Fatalf("another account accessed the vault: %s", code)
	}
	wrong := strings.Repeat("A", 43)
	if key == wrong {
		wrong = "B" + wrong[1:]
	}
	if code := phone.errorCode("sync.enable", map[string]any{"mode": "join", "key": wrong}); code != "SYNC_KEY" {
		t.Fatalf("wrong recovery key: %s", code)
	}
	if start := phone.call("sync.pair_start", nil); start["state"] != "waiting" || start["code"] != nil {
		t.Fatalf("pair request: %v", start)
	}
	if result := other.call("sync.pair_status", nil); result["state"] != "idle" {
		t.Fatalf("cross-account pairing: %v", result)
	}
	pc.call("sync.pair_prepare", nil)
	newDevice := phone.call("sync.pair_status", nil)
	old := pc.call("sync.pair_status", nil)
	code, _ := newDevice["code"].(string)
	if len(code) != 6 || old["code"] != code || old["device"] != "Phone" {
		t.Fatalf("devices did not derive the same code: %v / %v", newDevice, old)
	}
	if failure := pc.errorCode("sync.pair_approve", map[string]any{"code": "wrong"}); failure != "PAIR_CONFIRM" {
		t.Fatalf("bad comparison code was accepted: %s", failure)
	}
	if poll := phone.call("sync.pair_status", nil); poll["state"] != "compare" {
		t.Fatalf("key released without approval: %v", poll)
	}
	pc.call("sync.pair_approve", map[string]any{"code": code})
	if result := phone.call("sync.pair_status", nil); result["state"] != "connected" {
		t.Fatalf("pairing did not join the existing vault: %v", result)
	}
	if phone.call("sync.recovery_key", nil)["key"] != key || pc.call("sync.recovery_key", nil)["key"] != key {
		t.Fatal("pairing changed the existing recovery key")
	}
	if !titles(phone)["First device"] {
		t.Fatal("the Android vault did not receive the Windows entry")
	}
	snapshot, err := vaultsync.Get(context.Background(), server.Client(), server.URL, "signed-for-owner", "")
	if err != nil || strings.Contains(string(snapshot.Data), "sensitive-fixture") || strings.Contains(string(snapshot.Data), key) {
		t.Fatalf("cloud snapshot leaked credentials or could not be read: %v", err)
	}

	offline.Store(true)
	add(pc, "Offline Windows")
	add(phone, "Offline Android")
	if code := pc.errorCode("sync.now", nil); code != "SYNC_NETWORK" {
		t.Fatalf("offline sync: %s", code)
	}
	offline.Store(false)
	pc.call("sync.now", nil)
	phone.call("sync.now", nil)
	pc.call("sync.now", nil)
	for _, n := range []*node{pc, phone} {
		got := titles(n)
		for _, title := range []string{"First device", "Offline Windows", "Offline Android"} {
			if !got[title] {
				t.Fatalf("%s lost %q: %v", n.name, title, got)
			}
		}
	}
	phone.call("vault.lock", nil)
	if code := phone.errorCode("sync.now", nil); code != "VAULT_LOCKED" {
		t.Fatalf("locked vault was synchronized: %s", code)
	}
	phone.call("vault.unlock", map[string]any{"password": "phone-master-key"})
	add(pc, "Automatic")
	waitFor(t, "automatic upload", func() bool {
		b, _ := pc.c.cloud.load()
		hash, _, ok := vault.FileHash(pc.c.vaultDir())
		return ok && b != nil && b.LocalHash == hash
	})
	phone.call("sync.now", nil)
	if !titles(phone)["Automatic"] {
		t.Fatal("the save-triggered upload did not reach the second device")
	}
}
