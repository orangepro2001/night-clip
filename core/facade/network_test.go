package facade

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// node is a Core driven purely through its public JSON API.
type node struct {
	t    *testing.T
	c    *Core
	name string

	mu     sync.Mutex
	events []map[string]any
}

func startNode(t *testing.T, name, platform string, listen bool) *node {
	t.Helper()
	c := New()
	cfg := configFor(t, t.TempDir(), func(m map[string]any) {
		m["device_name"] = name
		m["platform"] = platform
		m["listen"] = listen
		m["db_key_b64"] = testKeyB64(byte(len(name)))
	})
	if err := c.Init(cfg); err != nil {
		t.Fatalf("Init %s: %v", name, err)
	}
	n := &node{t: t, c: c, name: name}
	go func() {
		for {
			select {
			case <-c.done:
				return
			default:
			}
			raw := c.NextEvent(50)
			if raw == "" {
				continue
			}
			var ev map[string]any
			if json.Unmarshal([]byte(raw), &ev) == nil {
				n.mu.Lock()
				n.events = append(n.events, ev)
				n.mu.Unlock()
			}
		}
	}()
	t.Cleanup(c.Shutdown)
	return n
}

// call runs a method and fails the test unless it succeeded.
func (n *node) call(method string, params any) map[string]any {
	n.t.Helper()
	r := n.try(method, params)
	if !r.OK {
		n.t.Fatalf("%s.%s failed: %s", n.name, method, r.Error.Code)
	}
	var out map[string]any
	if len(r.Result) > 0 {
		_ = json.Unmarshal(r.Result, &out)
	}
	return out
}

func (n *node) try(method string, params any) reply {
	n.t.Helper()
	b, _ := json.Marshal(params)
	if params == nil {
		b = []byte("{}")
	}
	return decode(n.t, n.c.Call(method, string(b)))
}

func (n *node) errorCode(method string, params any) string {
	n.t.Helper()
	r := n.try(method, params)
	if r.OK {
		n.t.Fatalf("%s.%s unexpectedly succeeded", n.name, method)
	}
	return r.Error.Code
}

func (n *node) find(typ string, match map[string]any) map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, ev := range n.events {
		if ev["type"] != typ {
			continue
		}
		ok := true
		for k, v := range match {
			if ev[k] != v {
				ok = false
			}
		}
		if ok {
			return ev
		}
	}
	return nil
}

func (n *node) waitEvent(typ string, match map[string]any) map[string]any {
	n.t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if ev := n.find(typ, match); ev != nil {
			return ev
		}
		time.Sleep(5 * time.Millisecond)
	}
	n.t.Fatalf("%s: no %s event %v", n.name, typ, match)
	return nil
}

func (n *node) waitPeerConnected(other *node) {
	n.t.Helper()
	id := other.deviceID()
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range n.call("peers.list", nil)["peers"].([]any) {
			pm := p.(map[string]any)
			if pm["device_id"] == id && pm["state"] == "connected" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	n.t.Fatalf("%s never connected to %s", n.name, other.name)
}

func (n *node) deviceID() string { return n.call("identity.get", nil)["device_id"].(string) }

// pairNodes performs the complete pairing through the API.
func pairNodes(t *testing.T, win, pix *node) {
	t.Helper()
	b := win.call("pair.begin", nil)
	join := pix.call("pair.join", map[string]any{"link": b["link"]})
	req := win.waitEvent("pair.request", nil)
	sas := pix.waitEvent("pair.sas", map[string]any{"join_id": join["join_id"]})
	if req["sas"] != sas["sas"] || len(req["sas"].(string)) != 6 {
		t.Fatalf("codes differ: %v vs %v", req["sas"], sas["sas"])
	}
	win.call("pair.confirm", map[string]any{"pair_id": req["pair_id"], "accept": true})
	pix.waitEvent("pair.result", map[string]any{"join_id": join["join_id"], "state": "accepted"})
	win.waitPeerConnected(pix)
	pix.waitPeerConnected(win)
}

func TestPairAndSyncThroughTheFacade(t *testing.T) {
	win := startNode(t, "Tony-PC", "windows", true)
	pix := startNode(t, "Pixel", "android", false)

	b := win.call("pair.begin", nil)
	qr := b["qr"].(map[string]any)
	size := int(qr["size"].(float64))
	rows := qr["rows"].([]any)
	if size < 21 || len(rows) != size {
		t.Fatalf("qr size %d, %d rows", size, len(rows))
	}
	dark := 0
	for _, r := range rows {
		row := r.(string)
		if len(row) != size || strings.Trim(row, "01") != "" {
			t.Fatalf("qr row %q is not a %d-wide 0/1 string", row, size)
		}
		dark += strings.Count(row, "1")
	}
	if dark == 0 || dark == size*size {
		t.Fatalf("qr matrix is blank or solid (%d dark modules)", dark)
	}
	if !strings.HasPrefix(b["link"].(string), "clipbridge://pair?") || b["host"] != "127.0.0.1" {
		t.Fatalf("pair.begin = %v", b)
	}
	win.call("pair.cancel", map[string]any{"session_id": b["session_id"]})

	pairNodes(t, win, pix)

	// capture is opt-in; turn it on on the PC and copy something there
	if got := win.call("clip.local_changed", map[string]any{"text": "ignored"}); got["suppressed"] != "capture_off" {
		t.Fatalf("capture should start off: %v", got)
	}
	win.call("settings.set", map[string]any{"key": "capture", "value": "on"})
	got := win.call("clip.local_changed", map[string]any{"text": "hello from the pc"})
	if len(got["sent"].([]any)) != 1 {
		t.Fatalf("local_changed = %v", got)
	}
	ev := pix.waitEvent("clip.received", map[string]any{"text": "hello from the pc"})
	if ev["decision"] != "notify" || ev["from_name"] != "Tony-PC" || ev["sensitive"] != false {
		t.Fatalf("clip.received = %v", ev)
	}

	// the phone applies it; the PC is told, and the history shows it
	pix.call("clip.mark_applied", map[string]any{"event_id": ev["event_id"]})
	win.waitEvent("clip.ack", map[string]any{"event_id": ev["event_id"], "result": "applied"})
	items := pix.call("inbox.list", nil)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["state"] != "applied" {
		t.Fatalf("inbox = %v", items)
	}
	if one := pix.call("inbox.get", map[string]any{"event_id": ev["event_id"]}); one["text"] != "hello from the pc" {
		t.Fatalf("inbox.get = %v", one)
	}

	// and the other direction, by explicit send (the phone never enabled capture)
	sent := pix.call("clip.send", map[string]any{"text": "reply from the phone"})
	if len(sent["sent"].([]any)) != 1 {
		t.Fatalf("clip.send = %v", sent)
	}
	win.waitEvent("clip.received", map[string]any{"text": "reply from the phone"})

	// status and diagnostics describe the link without leaking secrets
	st := win.call("status", nil)
	peers := st["peers"].([]any)
	if len(peers) != 1 || peers[0].(map[string]any)["state"] != "connected" || st["capture"] != "on" {
		t.Fatalf("status = %v", st)
	}
	diag, _ := json.Marshal(win.call("diagnostics.get", nil))
	for _, secret := range []string{"psk", "private", "db_key", testKeyB64(byte(len("Tony-PC")))} {
		if strings.Contains(string(diag), secret) {
			t.Fatalf("diagnostics leaks %q: %s", secret, diag)
		}
	}
}

func TestManualPairingWithHostPortAndSecret(t *testing.T) {
	win := startNode(t, "Desk", "windows", true)
	pix := startNode(t, "Phone", "android", false)
	b := win.call("pair.begin", nil)
	link := strings.SplitN(b["link"].(string), "?", 2)[1]
	q := map[string]string{}
	for _, kv := range strings.Split(link, "&") {
		p := strings.SplitN(kv, "=", 2)
		q[p[0]] = p[1]
	}
	// typed by hand: host, port and the secret, no fingerprint
	join := pix.call("pair.join", map[string]any{"host": b["host"], "port": b["port"], "psk": q["psk"]})
	req := win.waitEvent("pair.request", nil)
	win.call("pair.confirm", map[string]any{"pair_id": req["pair_id"], "accept": true})
	pix.waitEvent("pair.result", map[string]any{"join_id": join["join_id"], "state": "accepted"})
	pix.waitPeerConnected(win)
}

func TestPairingFailuresAreReportedAsEvents(t *testing.T) {
	win := startNode(t, "Desk", "windows", true)
	pix := startNode(t, "Phone", "android", false)

	// a malformed link fails the call itself
	if code := pix.errorCode("pair.join", map[string]any{"link": "https://example.com"}); code != "INVALID" {
		t.Fatalf("bad link = %s", code)
	}
	if code := pix.errorCode("pair.join", map[string]any{}); code != "INVALID" {
		t.Fatalf("no link = %s", code)
	}

	// a well-formed link with the wrong secret fails asynchronously
	b := win.call("pair.begin", nil)
	good := b["link"].(string)
	psk := func(l string) string { return strings.Split(strings.Split(l, "psk=")[1], "&")[0] }
	forged := strings.Replace(good, psk(good), "AAAAAAAAAAAAAAAAAAAAAA", 1) // 16 bytes, but not the secret
	join := pix.call("pair.join", map[string]any{"link": forged})
	res := pix.waitEvent("pair.result", map[string]any{"join_id": join["join_id"], "state": "failed"})
	if res["code"] != "PAIRING_FAILED" {
		t.Fatalf("pair.result = %v", res)
	}
	if win.find("pair.request", nil) != nil {
		t.Fatal("the PC user was asked about a request with the wrong secret")
	}

	// the user declining is a failure with a clear code on the other side
	join = pix.call("pair.join", map[string]any{"link": good})
	req := win.waitEvent("pair.request", nil)
	win.call("pair.confirm", map[string]any{"pair_id": req["pair_id"], "accept": false})
	res = pix.waitEvent("pair.result", map[string]any{"join_id": join["join_id"], "state": "failed", "code": "PAIRING_FAILED"})
	if !strings.Contains(res["message"].(string), "declined") {
		t.Fatalf("pair.result = %v", res)
	}
	if code := win.errorCode("pair.confirm", map[string]any{"pair_id": req["pair_id"], "accept": true}); code != "INVALID" && code != "PAIRING_EXPIRED" {
		t.Fatalf("second confirm = %s", code)
	}
	if code := win.errorCode("pair.confirm", map[string]any{"pair_id": "nope", "accept": true}); code != "PAIRING_EXPIRED" {
		t.Fatalf("unknown pair id = %s", code)
	}
	if got := win.call("peers.list", nil)["peers"].([]any); len(got) != 0 {
		t.Fatalf("a refused pairing left a peer behind: %v", got)
	}
}

func TestPeerManagement(t *testing.T) {
	win := startNode(t, "Desk", "windows", true)
	pix := startNode(t, "Phone", "android", false)
	pairNodes(t, win, pix)
	pixID := pix.deviceID()

	win.call("peers.update", map[string]any{"device_id": pixID, "name": "My Phone", "apply_mode": "auto"})
	p := win.call("peers.list", nil)["peers"].([]any)[0].(map[string]any)
	if p["name"] != "My Phone" || p["apply_mode"] != "auto" {
		t.Fatalf("peer after update = %v", p)
	}
	for _, bad := range []map[string]any{
		{"device_id": pixID, "apply_mode": "yolo"}, {"device_id": pixID, "name": " "}, {"device_id": pixID, "role": "boss"},
	} {
		if code := win.errorCode("peers.update", bad); code != "INVALID" {
			t.Fatalf("update %v = %s", bad, code)
		}
	}
	if code := win.errorCode("peers.update", map[string]any{"device_id": "dev_" + strings.Repeat("a", 26), "name": "x"}); code != "NOT_PAIRED" {
		t.Fatalf("unknown device = %s", code)
	}

	// the new apply mode governs how the PC treats events from the phone
	pix.call("clip.send", map[string]any{"text": "plain words"})
	if ev := win.waitEvent("clip.received", map[string]any{"text": "plain words"}); ev["decision"] != "auto" {
		t.Fatalf("decision = %v", ev["decision"])
	}

	// removing revokes: the phone learns of it and both sides stop talking
	win.call("peers.remove", map[string]any{"device_id": pixID})
	pix.waitEvent("peer.revoked", nil)
	if code := win.errorCode("clip.send", map[string]any{"text": "x"}); code != "NOT_PAIRED" {
		t.Fatalf("send after revoke = %s", code)
	}
	win.call("peers.remove", map[string]any{"device_id": pixID, "forget": true})
	if got := win.call("peers.list", nil)["peers"].([]any); len(got) != 0 {
		t.Fatalf("forgotten peer still listed: %v", got)
	}
}

func TestSettingsAPI(t *testing.T) {
	n := startNode(t, "Desk", "windows", true)
	all := n.call("settings.get", nil)["settings"].(map[string]any)
	if all["capture"] != "off" || all["clip_ttl_s"] != "900" || all["default_apply_mode"] != "notify" {
		t.Fatalf("defaults = %v", all)
	}
	n.call("settings.set", map[string]any{"key": "clip_ttl_s", "value": "120"})
	if got := n.call("settings.get", map[string]any{"key": "clip_ttl_s"}); got["value"] != "120" {
		t.Fatalf("get = %v", got)
	}
	for _, bad := range []map[string]any{
		{"key": "capture", "value": "maybe"}, {"key": "clip_ttl_s", "value": "1"}, {"key": "nonsense", "value": "x"}, {"key": "", "value": ""},
	} {
		if code := n.errorCode("settings.set", bad); code != "INVALID" {
			t.Fatalf("settings.set %v = %s", bad, code)
		}
	}
	if code := n.errorCode("settings.get", map[string]any{"key": "nonsense"}); code != "INVALID" {
		t.Fatalf("settings.get unknown key = %s", code)
	}
}

func TestNetworkMethodsNeedInitAndValidInput(t *testing.T) {
	cold := New()
	for _, m := range []string{"pair.begin", "peers.list", "clip.send", "inbox.list", "settings.get", "diagnostics.get"} {
		if r := decode(t, cold.Call(m, "{}")); r.OK || r.Error.Code != "NOT_INITIALIZED" {
			t.Errorf("%s before Init = %+v", m, r)
		}
	}
	n := startNode(t, "Desk", "windows", true)
	if code := n.errorCode("clip.send", map[string]any{"text": "x"}); code != "NOT_PAIRED" {
		t.Fatalf("send with nobody paired = %s", code)
	}
	if code := n.errorCode("inbox.get", map[string]any{"event_id": "5b8f0e5c-3f6e-4a8b-9f32-3d4c7f2a1e90"}); code != "INVALID" {
		t.Fatalf("unknown inbox event = %s", code)
	}
	if r := decode(t, n.c.Call("clip.send", "not json")); r.OK || r.Error.Code != "INVALID" {
		t.Fatalf("garbage params = %+v", r)
	}
	if code := n.errorCode("pair.nonsense", nil); code != "METHOD_NOT_FOUND" {
		t.Fatalf("unknown pair method = %s", code)
	}
	// the phone cannot hand out a pairing code: it does not listen
	pix := startNode(t, "Phone", "android", false)
	if code := pix.errorCode("pair.begin", nil); code != "NO_TAILNET" {
		t.Fatalf("pair.begin on a non-listening core = %s", code)
	}
}

func TestPairedDevicesReconnectAfterARestart(t *testing.T) {
	winDir, pixDir := t.TempDir(), t.TempDir()
	boot := func(name, platform string, listen bool, dir string) *node {
		c := New()
		cfg := configFor(t, dir, func(m map[string]any) {
			m["device_name"], m["platform"], m["listen"] = name, platform, listen
		})
		if err := c.Init(cfg); err != nil {
			t.Fatal(err)
		}
		n := &node{t: t, c: c, name: name}
		go func() {
			for {
				select {
				case <-c.done:
					return
				default:
				}
				if raw := c.NextEvent(50); raw != "" {
					var ev map[string]any
					if json.Unmarshal([]byte(raw), &ev) == nil {
						n.mu.Lock()
						n.events = append(n.events, ev)
						n.mu.Unlock()
					}
				}
			}
		}()
		return n
	}
	win := boot("Desk", "windows", true, winDir)
	pix := boot("Phone", "android", false, pixDir)
	pairNodes(t, win, pix)
	pixID := pix.deviceID()
	win.call("settings.set", map[string]any{"key": "capture", "value": "on"})

	// both apps are closed and opened again
	win.c.Shutdown()
	pix.c.Shutdown()
	win2 := boot("Desk", "windows", true, winDir)
	pix2 := boot("Phone", "android", false, pixDir)
	t.Cleanup(win2.c.Shutdown)
	t.Cleanup(pix2.c.Shutdown)

	if pix2.deviceID() != pixID {
		t.Fatal("the phone's identity changed across the restart")
	}
	// the phone's stored address for the PC has a stale port; the PC now listens elsewhere.
	// Real deployments use a fixed port; this test only checks that pairing survived, so
	// re-point the phone at the new port the way a fresh hello would.
	port := int(win2.call("status", nil)["listening_port"].(float64))
	pix2.call("peers.update", map[string]any{"device_id": win2.deviceID(), "role": "dialer"})
	rec, _, _ := pix2.c.db.GetPeer(win2.deviceID())
	rec.Port = port
	if err := pix2.c.db.PutPeer(rec); err != nil {
		t.Fatal(err)
	}
	_ = pix2.c.n.eng.Reconnect(win2.deviceID())
	pix2.waitPeerConnected(win2)
	win2.waitPeerConnected(pix2)

	got := win2.call("clip.local_changed", map[string]any{"text": "after the restart"})
	if len(got["sent"].([]any)) != 1 {
		t.Fatalf("local_changed = %v", got)
	}
	pix2.waitEvent("clip.received", map[string]any{"text": "after the restart"})
}

func TestFileTransferThroughTheFacade(t *testing.T) {
	win := startNode(t, "Desk", "windows", true)
	pix := startNode(t, "Phone", "android", false)
	pairNodes(t, win, pix)
	pixID, winID := pix.deviceID(), win.deviceID()

	data := make([]byte, 1<<20+17)
	for i := range data {
		data[i] = byte(i * 31)
	}
	src := filepath.Join(t.TempDir(), "notes.bin")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}

	// PC -> phone (the phone pulls)
	off := win.call("file.offer", map[string]any{"to": pixID, "path": src})
	id := off["transfer_id"].(string)
	offered := pix.waitEvent("file.offered", map[string]any{"transfer_id": id})
	if offered["name"] != "notes.bin" || offered["from_name"] != "Desk" {
		t.Fatalf("file.offered = %v", offered)
	}
	saveDir := t.TempDir()
	pix.call("file.accept", map[string]any{"transfer_id": id, "save_dir": saveDir})
	done := pix.waitEvent("file.done", map[string]any{"transfer_id": id})
	got, err := os.ReadFile(done["path"].(string))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("received file differs (%v)", err)
	}
	win.waitEvent("file.done", map[string]any{"transfer_id": id, "direction": "out"})

	// phone -> PC (the phone pushes), declined this time
	off2 := pix.call("file.offer", map[string]any{"to": winID, "path": src})
	id2 := off2["transfer_id"].(string)
	win.waitEvent("file.offered", map[string]any{"transfer_id": id2})
	win.call("file.reject", map[string]any{"transfer_id": id2})
	pix.waitEvent("file.failed", map[string]any{"transfer_id": id2, "code": "REJECTED"})

	// and accepted
	id3 := pix.call("file.offer", map[string]any{"to": winID, "path": src})["transfer_id"].(string)
	win.waitEvent("file.offered", map[string]any{"transfer_id": id3})
	win.call("file.accept", map[string]any{"transfer_id": id3, "save_dir": saveDir})
	win.waitEvent("file.done", map[string]any{"transfer_id": id3})

	list := pix.call("transfers.list", nil)["transfers"].([]any)
	if len(list) != 3 {
		t.Fatalf("transfers.list = %d entries", len(list))
	}
	states := map[string]string{}
	for _, e := range list {
		m := e.(map[string]any)
		states[m["transfer_id"].(string)] = m["state"].(string)
	}
	if states[id] != "done" || states[id2] != "rejected" || states[id3] != "done" {
		t.Fatalf("states = %v", states)
	}

	// errors and settings
	if code := pix.errorCode("file.accept", map[string]any{"transfer_id": "tr_0000000000000000"}); code != "INVALID" {
		t.Fatalf("accept unknown = %s", code)
	}
	if code := pix.errorCode("file.offer", map[string]any{"to": winID}); code != "INVALID" {
		t.Fatalf("offer without a source = %s", code)
	}
	win.call("settings.set", map[string]any{"key": "download_dir", "value": saveDir})
	if got := win.call("settings.get", map[string]any{"key": "download_dir"}); got["value"] != saveDir {
		t.Fatalf("download_dir = %v", got)
	}
	if code := win.errorCode("settings.set", map[string]any{"key": "download_dir", "value": "relative"}); code != "INVALID" {
		t.Fatalf("relative download_dir = %s", code)
	}
	if v := win.call("settings.get", map[string]any{"key": "max_file_bytes"})["value"]; v != "4294967296" {
		t.Fatalf("max_file_bytes default = %v", v)
	}
}
