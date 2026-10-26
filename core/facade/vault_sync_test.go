package facade

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func syncState(n *node, peerID string) map[string]any {
	for _, p := range n.call("vault.sync_status", nil)["peers"].([]any) {
		pm := p.(map[string]any)
		if pm["device_id"] == peerID {
			return pm
		}
	}
	return nil
}

func titles(n *node) map[string]bool {
	out := map[string]bool{}
	for _, e := range n.call("vault.list", nil)["entries"].([]any) {
		out[e.(map[string]any)["title"].(string)] = true
	}
	return out
}

// pairedVaultPair returns a listening "PC" with a vault holding one entry, and a dialing "phone".
func pairedVaultPair(t *testing.T) (win, pix *node, winID, pixID string) {
	t.Helper()
	win = startNode(t, "Tony-PC", "windows", true)
	pix = startNode(t, "Pixel", "android", false)
	pairNodes(t, win, pix)
	winID, pixID = win.deviceID(), pix.deviceID()
	vcreate(t, win)
	win.call("vault.add", map[string]any{"entry": map[string]any{"title": "from PC", "password": "pc-secret"}})
	return
}

func enableBoth(win, pix *node, winID, pixID string) {
	win.call("vault.sync_enable", map[string]any{"device_id": pixID, "enabled": true})
	pix.call("vault.sync_enable", map[string]any{"device_id": winID, "enabled": true})
}

func TestVaultSyncNeedsBothSidesAndJoinsANewDevice(t *testing.T) {
	win, pix, winID, pixID := pairedVaultPair(t)

	// nothing is shared by default
	if st := syncState(pix, winID); st["enabled"] != false {
		t.Fatalf("default state = %v", st)
	}
	time.Sleep(400 * time.Millisecond)
	if pix.call("vault.status", nil)["exists"] != false {
		t.Fatal("a vault appeared on a device that never asked for it")
	}

	// the phone says yes, the PC has not: the PC refuses
	pix.call("vault.sync_enable", map[string]any{"device_id": winID, "enabled": true})
	waitFor(t, "peer_off on the phone", func() bool { return syncState(pix, winID)["state"] == "peer_off" })
	if pix.call("vault.status", nil)["exists"] != false {
		t.Fatal("the PC served its vault although it had not agreed")
	}

	// both agree: the phone joins the PC's vault
	win.call("vault.sync_enable", map[string]any{"device_id": pixID, "enabled": true})
	pix.waitEvent("vault.joined", nil)
	if st := pix.call("vault.status", nil); st["exists"] != true || st["unlocked"] != false {
		t.Fatalf("after joining: %v", st)
	}
	pix.call("vault.unlock", map[string]any{"password": vpw})
	if got := titles(pix); !got["from PC"] {
		t.Fatalf("entries on the phone: %v", got)
	}
	if r := pix.call("vault.reveal", map[string]any{"id": firstID(pix), "field": "password"}); r["value"] != "pc-secret" {
		t.Fatalf("secret on the phone = %v", r)
	}
}

func firstID(n *node) string {
	return n.call("vault.list", nil)["entries"].([]any)[0].(map[string]any)["id"].(string)
}

func TestVaultSyncCarriesEditsBothWays(t *testing.T) {
	win, pix, winID, pixID := pairedVaultPair(t)
	enableBoth(win, pix, winID, pixID)
	pix.waitEvent("vault.joined", nil)
	pix.call("vault.unlock", map[string]any{"password": vpw})

	// phone -> PC (pushed as soon as the phone saves)
	pix.call("vault.add", map[string]any{"entry": map[string]any{"title": "from phone", "password": "ph-secret"}})
	waitFor(t, "the PC to get the phone's entry", func() bool { return titles(win)["from phone"] })
	win.waitEvent("vault.changed", map[string]any{"reason": "sync"})

	// PC -> phone (the phone polls)
	win.call("vault.add", map[string]any{"entry": map[string]any{"title": "second from PC", "password": "x"}})
	waitFor(t, "the phone to get the PC's second entry", func() bool { return titles(pix)["second from PC"] })

	// conflicting edits of the same entry: both end with the same current version and keep both in history
	id := firstIDNamed(win, "from PC")
	win.call("vault.update", map[string]any{"id": id, "entry": map[string]any{"title": "from PC", "password": "edited on PC"}})
	time.Sleep(1100 * time.Millisecond) // the later edit must have a later (second-resolution) timestamp
	pix.call("vault.update", map[string]any{"id": id, "entry": map[string]any{"title": "from PC", "password": "edited on phone"}})
	waitFor(t, "both devices to agree", func() bool {
		a := win.call("vault.reveal", map[string]any{"id": id, "field": "password"})["value"]
		b := pix.call("vault.reveal", map[string]any{"id": id, "field": "password"})["value"]
		return a == "edited on phone" && b == "edited on phone"
	})
	if h := win.call("vault.history", map[string]any{"id": id})["versions"].([]any); len(h) < 2 {
		t.Fatalf("the PC lost a version: %v", h)
	}

	// a deletion travels too
	win.call("vault.delete", map[string]any{"id": firstIDNamed(win, "second from PC")})
	waitFor(t, "the trash move on the phone", func() bool { return !titles(pix)["second from PC"] })
}

func firstIDNamed(n *node, title string) string {
	for _, e := range n.call("vault.list", nil)["entries"].([]any) {
		if m := e.(map[string]any); m["title"] == title {
			return m["id"].(string)
		}
	}
	return ""
}

func TestVaultSyncWaitsForUnlockAndForAPasswordChange(t *testing.T) {
	win, pix, winID, pixID := pairedVaultPair(t)
	enableBoth(win, pix, winID, pixID)
	pix.waitEvent("vault.joined", nil)
	pix.call("vault.unlock", map[string]any{"password": vpw})
	n := win.c.net()
	_ = n

	// the PC is locked while the phone adds an entry: it waits aside
	win.call("vault.lock", nil)
	pix.call("vault.add", map[string]any{"entry": map[string]any{"title": "while PC locked", "password": "z"}})
	waitFor(t, "pending_unlock on the PC", func() bool {
		st := syncState(win, pixID)
		return st["pending_incoming"] == true && st["state"] == "pending_unlock"
	})
	win.call("vault.unlock", map[string]any{"password": vpw})
	waitFor(t, "the PC to merge after unlock", func() bool { return titles(win)["while PC locked"] })
	if st := syncState(win, pixID); st["pending_incoming"] != false || st["state"] != "idle" {
		t.Fatalf("after the merge: %v", st)
	}

	// the phone changes its master password: the PC cannot read the new file until it is told the password
	pix.call("vault.change_master", map[string]any{"password": vpw, "new": map[string]any{"password": "a brand new master"}})
	waitFor(t, "needs_password on the PC", func() bool { return syncState(win, pixID)["state"] == "needs_password" })
	if code := win.errorCode("vault.sync_resolve", map[string]any{"device_id": pixID, "password": "still wrong"}); code != "VAULT_WRONG_CREDENTIALS" {
		t.Fatalf("resolve with a wrong password: %s", code)
	}
	win.call("vault.sync_resolve", map[string]any{"device_id": pixID, "password": "a brand new master"})
	waitFor(t, "the state to clear", func() bool { return syncState(win, pixID)["state"] == "idle" })
}

func TestVaultSyncStopsAMassDeletionUntilApproved(t *testing.T) {
	win, pix, winID, pixID := pairedVaultPair(t)
	for i := 0; i < 8; i++ {
		win.call("vault.add", map[string]any{"entry": map[string]any{"title": "bulk" + string(rune('A'+i)), "password": "p"}})
	}
	enableBoth(win, pix, winID, pixID)
	pix.waitEvent("vault.joined", nil)
	pix.call("vault.unlock", map[string]any{"password": vpw})
	waitFor(t, "the phone to have 9 entries", func() bool { return len(titles(pix)) == 9 })

	// the phone is offline from the PC's point of view (sync switched off) while it deletes seven
	// entries for good (bin, then purge); the whole batch then arrives in one file
	pix.call("vault.sync_enable", map[string]any{"device_id": winID, "enabled": false})
	for _, e := range pix.call("vault.list", nil)["entries"].([]any)[:7] {
		id := e.(map[string]any)["id"].(string)
		pix.call("vault.delete", map[string]any{"id": id})
		pix.call("vault.delete", map[string]any{"id": id})
	}
	pix.call("vault.sync_enable", map[string]any{"device_id": winID, "enabled": true})
	waitFor(t, "review on the PC", func() bool { return syncState(win, pixID)["state"] == "review" })

	if len(titles(win)) != 9 {
		t.Fatalf("the PC applied a mass deletion on its own: %v", titles(win))
	}
	win.call("vault.sync_resolve", map[string]any{"device_id": pixID, "approve": true})
	waitFor(t, "the approved deletions", func() bool { return len(titles(win)) == 2 })
}

func TestVaultSyncRejectsGarbageAndSwitchedOffPeers(t *testing.T) {
	win, pix, winID, pixID := pairedVaultPair(t)
	enableBoth(win, pix, winID, pixID)
	pix.waitEvent("vault.joined", nil)

	// what the PC serves: a paired, enabled device may fetch; anything else gets a clear refusal
	p, _, _ := pix.c.db.GetPeer(winID)
	ep, err := pix.c.net().eng.Endpoint(p)
	if err != nil {
		t.Fatal(err)
	}
	put := func(body []byte) int {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, ep.Bases[0]+"/v1/vault/file", bytes.NewReader(body))
		resp, err := ep.Client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := put([]byte("this is not a vault")); code != http.StatusBadRequest {
		t.Fatalf("garbage upload = %d", code)
	}
	time.Sleep(50 * time.Millisecond)
	if code := put(append([]byte{0x03, 0xd9, 0xa2, 0x9a, 0x67, 0xfb, 0x4b, 0xb5}, bytes.Repeat([]byte{7}, 200)...)); code != http.StatusAccepted {
		t.Fatalf("a file with the right signature is kept aside for the merge to judge, got %d", code)
	}
	waitFor(t, "the PC to judge the fake file", func() bool {
		st := syncState(win, pixID)
		return st["state"] == "needs_password" || st["state"] == "error"
	})
	if titles(win)["from PC"] != true || len(titles(win)) != 1 {
		t.Fatalf("a junk upload changed the PC's vault: %v", titles(win))
	}

	// the PC switches sync off for the phone: nothing is served any more
	win.call("vault.sync_enable", map[string]any{"device_id": pixID, "enabled": false})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ep.Bases[0]+"/v1/vault/file", nil)
	resp, err := ep.Client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a device that was switched off got status %d", resp.StatusCode)
	}
	if st := syncState(win, pixID); st["pending_incoming"] != false {
		t.Fatalf("what the switched-off device sent must be discarded: %v", st)
	}
}
