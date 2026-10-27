package facade

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noteBody(n *node, id string) string {
	r := n.try("notes.get", map[string]any{"note_id": id})
	if !r.OK {
		return ""
	}
	return n.call("notes.get", map[string]any{"note_id": id})["body"].(string)
}

func noteRev(n *node, id string) string {
	return n.call("notes.get", map[string]any{"note_id": id})["rev_id"].(string)
}

func TestNotesMethodsThroughTheFacade(t *testing.T) {
	n := startNode(t, "Tony-PC", "windows", true)

	created := n.call("notes.create", map[string]any{"title": "Ideas", "body": "# Ideas\r\n\r\n- one\r\n- two\r\n"})
	id := created["note_id"].(string)
	ev := n.waitEvent("notes.changed", map[string]any{"note_id": id})
	if ev["rev_id"] != created["rev_id"] {
		t.Fatalf("event = %v, create = %v", ev, created)
	}
	if _, hasBody := ev["body"]; hasBody {
		t.Fatal("events must not carry the text")
	}

	got := n.call("notes.get", map[string]any{"note_id": id})
	body := got["body"].(string)
	if strings.Contains(body, "\r") || got["title"] != "Ideas" || got["format_version"] != float64(1) || got["rev_count"] != float64(1) {
		t.Fatalf("notes.get = %v", got)
	}

	list := n.call("notes.list", nil)
	items := list["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["note_id"] != id {
		t.Fatalf("notes.list = %v", list)
	}

	up := n.call("notes.update", map[string]any{"note_id": id, "base_rev_id": created["rev_id"], "body": body + "\n- three\n"})
	if up["merged"] != false || up["rev_id"] == created["rev_id"] {
		t.Fatalf("notes.update = %v", up)
	}

	render := n.call("notes.render", map[string]any{"note_id": id})
	if render["note_id"] != id || render["rev_id"] != up["rev_id"] || len(render["blocks"].([]any)) == 0 || render["toc"] == nil {
		t.Fatalf("notes.render = %v", render)
	}

	// the live preview of text that is not saved
	live := n.call("notes.render", map[string]any{"body": "# Draft\n\nnot saved yet"})
	if _, hasNote := live["note_id"]; hasNote || len(live["blocks"].([]any)) != 2 || len(live["toc"].([]any)) != 1 {
		t.Fatalf("notes.render with a body = %v", live)
	}
	if c := n.errorCode("notes.render", map[string]any{"body": "x", "note_id": id}); c != "INVALID" {
		t.Fatalf("render with both a body and a note = %s", c)
	}
	if up["changed"] != true {
		t.Fatalf("notes.update changed = %v", up["changed"])
	}

	hist := n.call("notes.history", map[string]any{"note_id": id})["items"].([]any)
	if len(hist) != 2 || hist[0].(map[string]any)["rev_id"] != up["rev_id"] {
		t.Fatalf("notes.history = %v", hist)
	}

	norm := n.call("notes.normalize", map[string]any{"body": "a  \r\n\r\n\r\nb"})
	if norm["body_norm"] != "a\n\nb\n" || !strings.HasPrefix(norm["body_hash"].(string), "sha256:") {
		t.Fatalf("notes.normalize = %v", norm)
	}

	n.call("notes.delete", map[string]any{"note_id": id})
	if live := n.call("notes.list", nil)["items"].([]any); len(live) != 0 {
		t.Fatalf("deleted note still listed: %v", live)
	}
	if trash := n.call("notes.list", map[string]any{"deleted": true})["items"].([]any); len(trash) != 1 {
		t.Fatalf("trash = %v", trash)
	}
	n.call("notes.restore", map[string]any{"note_id": id})
	if live := n.call("notes.list", nil)["items"].([]any); len(live) != 1 {
		t.Fatalf("restored note missing: %v", live)
	}

	found := n.call("notes.search", map[string]any{"query": "three"})["items"].([]any)
	if len(found) != 1 || found[0].(map[string]any)["note_id"] != id || !strings.Contains(found[0].(map[string]any)["snippet"].(string), "three") {
		t.Fatalf("notes.search = %v", found)
	}
	if c := n.errorCode("notes.search", map[string]any{"query": " "}); c != "INVALID" {
		t.Fatalf("empty search = %s", c)
	}
	if c := n.errorCode("notes.get", map[string]any{"note_id": "nt_" + strings.Repeat("a", 26)}); c != "NOT_FOUND" {
		t.Fatalf("missing note = %s", c)
	}
	if c := n.errorCode("notes.get", map[string]any{"note_id": "x"}); c != "INVALID" {
		t.Fatalf("malformed id = %s", c)
	}
	if c := n.errorCode("notes.nothing", nil); c != "METHOD_NOT_FOUND" {
		t.Fatalf("unknown notes method = %s", c)
	}
	if st := n.call("notes.sync_status", nil); len(st["peers"].([]any)) != 0 || st["pending_push"] != float64(0) {
		t.Fatalf("notes.sync_status without peers = %v", st)
	}
}

func TestNotesExportAndImportThroughTheFacade(t *testing.T) {
	n := startNode(t, "Tony-PC", "windows", true)
	n.call("notes.create", map[string]any{"title": "Export me", "body": "some text\n"})
	n.call("notes.create", map[string]any{"title": "And me", "body": "more text\n"})

	zipPath := filepath.Join(t.TempDir(), "backup.zip")
	exp := n.call("notes.export", map[string]any{"path": zipPath})
	if exp["count"] != float64(2) || exp["path"] != zipPath || exp["bytes"].(float64) <= 0 {
		t.Fatalf("notes.export = %v", exp)
	}
	if c := n.errorCode("notes.export", map[string]any{"path": zipPath}); c != "INVALID" {
		t.Fatalf("exporting over a file = %s", c)
	}
	imp := n.call("notes.import", map[string]any{"path": zipPath})
	if imp["imported"] != float64(2) || len(imp["skipped"].([]any)) != 0 || len(imp["note_ids"].([]any)) != 2 {
		t.Fatalf("notes.import = %v", imp)
	}
	if items := n.call("notes.list", nil)["items"].([]any); len(items) != 4 {
		t.Fatalf("notes after importing the export = %d, want 4", len(items))
	}
	if c := n.errorCode("notes.import", map[string]any{"path": "relative.zip"}); c != "INVALID" {
		t.Fatalf("relative import path = %s", c)
	}
}

func TestNotesAttachmentsTravelBetweenTwoDevices(t *testing.T) {
	win, pix := pairedNotesPair(t)
	file := filepath.Join(t.TempDir(), "plan.pdf")
	content := []byte("%PDF-1.4 the plan")
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}

	att := win.call("notes.attachment_add", map[string]any{"path": file})
	blob := att["blob_id"].(string)
	if att["mime"] != "application/pdf" || att["present"] != true || att["name"] != "plan.pdf" || !strings.HasSuffix(att["markdown"].(string), "(blob:"+blob+")") {
		t.Fatalf("notes.attachment_add = %v", att)
	}
	id := win.call("notes.create", map[string]any{"title": "Plan", "body": "read " + att["markdown"].(string) + "\n"})["note_id"].(string)
	waitFor(t, "the note on the phone", func() bool { return noteBody(pix, id) != "" })

	waitFor(t, "the attachment on the phone", func() bool {
		return pix.call("notes.attachment_get", map[string]any{"blob_id": blob})["present"] == true
	})
	pix.waitEvent("notes.attachment", map[string]any{"blob_id": blob})
	out := filepath.Join(t.TempDir(), "copy.pdf")
	pix.call("notes.attachment_export", map[string]any{"blob_id": blob, "path": out})
	if got, _ := os.ReadFile(out); !bytes.Equal(got, content) {
		t.Fatalf("exported file = %q", got)
	}

	// and from the phone (the dialing side) to the PC
	file2 := filepath.Join(t.TempDir(), "photo.png")
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	_ = os.WriteFile(file2, png, 0o600)
	att2 := pix.call("notes.attachment_add", map[string]any{"path": file2})
	pix.call("notes.update", map[string]any{"note_id": id, "base_rev_id": noteRev(pix, id), "body": noteBody(pix, id) + "\n" + att2["markdown"].(string) + "\n"})
	waitFor(t, "the phone's picture on the PC", func() bool {
		return win.call("notes.attachment_get", map[string]any{"blob_id": att2["blob_id"]})["present"] == true
	})

	if c := win.errorCode("notes.attachment_export", map[string]any{"blob_id": strings.Repeat("0", 64), "path": out}); c != "NOT_FOUND" {
		t.Fatalf("absent attachment = %s", c)
	}
	if c := win.errorCode("notes.attachment_add", map[string]any{"path": "plan.pdf"}); c != "INVALID" {
		t.Fatalf("relative path = %s", c)
	}
	if r := win.call("notes.attachment_cleanup", nil); r["removed"] != float64(0) {
		t.Fatalf("cleanup = %v", r)
	}
}

func TestNotesNeedAnInitialisedCore(t *testing.T) {
	c := New()
	r := decode(t, c.Call("notes.list", "{}"))
	if r.OK || r.Error.Code != "NOT_INITIALIZED" {
		t.Fatalf("notes.list before Init = %+v", r)
	}
}

// pairedNotesPair returns a listening "PC" and a dialing "phone", paired.
func pairedNotesPair(t *testing.T) (win, pix *node) {
	t.Helper()
	win = startNode(t, "Tony-PC", "windows", true)
	pix = startNode(t, "Pixel", "android", false)
	pairNodes(t, win, pix)
	return
}

func TestNotesSyncBetweenTwoDevices(t *testing.T) {
	win, pix := pairedNotesPair(t)

	// PC -> phone: the phone polls and finds the note
	id := win.call("notes.create", map[string]any{"title": "Trip", "body": "pack bags\n"})["note_id"].(string)
	waitFor(t, "the note on the phone", func() bool { return noteBody(pix, id) == "pack bags\n" })
	pix.waitEvent("notes.changed", map[string]any{"note_id": id})

	// phone -> PC: pushed as soon as the phone saves
	base := noteRev(pix, id)
	pix.call("notes.update", map[string]any{"note_id": id, "base_rev_id": base, "body": "pack bags\n\nbook train\n"})
	waitFor(t, "the phone's edit on the PC", func() bool { return noteBody(win, id) == "pack bags\n\nbook train\n" })

	// PC -> phone again, an edit made on the listening side reaches the dialer on its next poll
	win.call("notes.update", map[string]any{"note_id": id, "base_rev_id": noteRev(win, id), "body": "pack bags\n\nbook train\n\nbuy tickets\n"})
	waitFor(t, "the PC's edit on the phone", func() bool { return strings.Contains(noteBody(pix, id), "buy tickets") })

	// deletes travel too
	win.call("notes.delete", map[string]any{"note_id": id})
	waitFor(t, "the delete on the phone", func() bool {
		return len(pix.call("notes.list", nil)["items"].([]any)) == 0
	})

	// status reflects a finished round
	waitFor(t, "an idle sync state on the phone", func() bool {
		for _, p := range pix.call("notes.sync_status", nil)["peers"].([]any) {
			pm := p.(map[string]any)
			if pm["state"] == "idle" && pm["last_sync_at"].(float64) > 0 && pm["behind"] == float64(0) {
				return true
			}
		}
		return false
	})
	if st := pix.find("notes.sync.updated", map[string]any{"state": "idle"}); st == nil {
		t.Fatal("the phone never announced a finished sync")
	}
}

func TestNotesEditedOnBothDevicesWhileApartConverge(t *testing.T) {
	win, pix := pairedNotesPair(t)
	id := win.call("notes.create", map[string]any{"title": "Doc", "body": "alpha\n\nbeta\n"})["note_id"].(string)
	waitFor(t, "the note on the phone", func() bool { return noteBody(pix, id) != "" })

	// both sides edit different blocks of the same revision
	base := noteRev(win, id)
	win.call("notes.update", map[string]any{"note_id": id, "base_rev_id": base, "body": "ALPHA\n\nbeta\n"})
	pix.call("notes.update", map[string]any{"note_id": id, "base_rev_id": base, "body": "alpha\n\nbeta\n\ngamma\n"})

	waitFor(t, "both devices to hold the merged note", func() bool {
		return noteBody(win, id) == "ALPHA\n\nbeta\n\ngamma\n" && noteBody(pix, id) == "ALPHA\n\nbeta\n\ngamma\n"
	})
	waitFor(t, "the same head revision", func() bool { return noteRev(win, id) == noteRev(pix, id) })
}

func TestNotesConflictCopyReachesBothDevicesAndCanBeResolved(t *testing.T) {
	win, pix := pairedNotesPair(t)
	id := win.call("notes.create", map[string]any{"title": "Doc", "body": "alpha\n"})["note_id"].(string)
	waitFor(t, "the note on the phone", func() bool { return noteBody(pix, id) != "" })

	base := noteRev(win, id)
	// the same block edited twice from the same base: the second edit on the same device makes the fork
	pix.call("notes.update", map[string]any{"note_id": id, "base_rev_id": base, "body": "alpha by phone\n"})
	win.call("notes.update", map[string]any{"note_id": id, "base_rev_id": base, "body": "alpha by pc\n"})

	waitFor(t, "a conflict copy on both devices", func() bool {
		return len(win.call("notes.sync_conflicts", nil)["items"].([]any)) == 1 && len(pix.call("notes.sync_conflicts", nil)["items"].([]any)) == 1
	})
	waitFor(t, "the same head revision", func() bool { return noteRev(win, id) == noteRev(pix, id) })
	if win.find("notes.sync.conflict", nil) == nil && pix.find("notes.sync.conflict", nil) == nil {
		t.Fatal("nobody was told about the conflict")
	}

	item := pix.call("notes.sync_conflicts", nil)["items"].([]any)[0].(map[string]any)
	if item["note_id"] != id {
		t.Fatalf("conflict item = %v", item)
	}
	copyID := item["conflict_note_id"].(string)
	if body := noteBody(pix, copyID); !strings.Contains(body, "alpha by") {
		t.Fatalf("conflict copy = %q", body)
	}
	pix.call("notes.sync_resolve", map[string]any{"note_id": id, "conflict_note_id": copyID, "action": "keep_main"})
	waitFor(t, "the resolution on the PC", func() bool { return len(win.call("notes.sync_conflicts", nil)["items"].([]any)) == 0 })
}

func TestNotesSyncNowAndRemovedPeers(t *testing.T) {
	win, pix := pairedNotesPair(t)
	if r := pix.try("notes.sync_now", map[string]any{"peer_id": "dev_" + strings.Repeat("a", 26)}); r.OK || r.Error.Code != "NOT_PAIRED" {
		t.Fatalf("sync_now with an unknown peer = %+v", r)
	}
	if r := pix.try("notes.sync_now", map[string]any{"peer_id": win.deviceID()}); !r.OK {
		t.Fatalf("sync_now = %+v", r)
	}
	id := win.call("notes.create", map[string]any{"body": "before unpairing\n"})["note_id"].(string)
	waitFor(t, "the note on the phone", func() bool { return noteBody(pix, id) != "" })

	// after the phone removes the PC, nothing flows any more
	pix.call("peers.remove", map[string]any{"device_id": win.deviceID()})
	win.call("notes.create", map[string]any{"body": "after unpairing\n"})
	if st := pix.call("notes.sync_status", nil); len(st["peers"].([]any)) != 0 {
		t.Fatalf("status still lists the removed peer: %v", st)
	}
	if n := len(pix.call("notes.list", nil)["items"].([]any)); n != 1 {
		t.Fatalf("the phone has %d notes, want 1", n)
	}
}
