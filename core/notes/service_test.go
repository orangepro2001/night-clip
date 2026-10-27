package notes

import (
	"strings"
	"testing"

	"nightclip/core/proto"
)

func errCode(err error) string {
	if pe, ok := err.(*proto.Error); ok {
		return string(pe.Code)
	}
	return ""
}

func one(t *testing.T) (*device, *clock) {
	clk := &clock{ms: 1_700_000_000_000}
	return newDevice(t, 0, clk), clk
}

func TestNoteLifecycle(t *testing.T) {
	d, clk := one(t)
	id := d.mustCreate("Shopping", "milk\n\neggs\n")
	g, err := d.svc.Get(id, "")
	if err != nil || g.Title != "Shopping" || g.Body != "milk\n\neggs\n" || g.RevCount != 1 || !g.IsHead || g.Deleted || len(g.Parents) != 0 {
		t.Fatalf("Get after Create = %+v, %v", g, err)
	}
	rev1 := g.RevID

	clk.tick()
	up, err := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: rev1, Body: strp("milk\n\neggs\n\nbread\n")})
	if err != nil || up.Merged || up.RevID == rev1 || up.ConflictNoteID != "" {
		t.Fatalf("Update = %+v, %v", up, err)
	}
	g, _ = d.svc.Get(id, "")
	if g.Body != "milk\n\neggs\n\nbread\n" || g.RevCount != 2 || g.Parents[0] != rev1 || g.UpdatedAt <= g.CreatedAt {
		t.Fatalf("after Update: %+v", g)
	}
	old, err := d.svc.Get(id, rev1)
	if err != nil || old.Body != "milk\n\neggs\n" || old.IsHead {
		t.Fatalf("Get(old rev) = %+v, %v", old, err)
	}

	clk.tick()
	if _, err := d.svc.Delete(id); err != nil {
		t.Fatal(err)
	}
	live, _ := d.svc.List(10, "", false)
	trash, _ := d.svc.List(10, "", true)
	if len(live.Items) != 0 || len(trash.Items) != 1 || !trash.Items[0].Deleted {
		t.Fatalf("after Delete: live=%+v trash=%+v", live, trash)
	}
	if _, err := d.svc.Update(UpdateParams{NoteID: id, Body: strp("x")}); errCode(err) != string(proto.CodeConflict) {
		t.Fatalf("editing a trashed note: %v", err)
	}
	if again, err := d.svc.Delete(id); err != nil || again == "" {
		t.Fatalf("deleting twice must be a quiet no-op: %q %v", again, err)
	}

	clk.tick()
	if _, err := d.svc.Restore(id); err != nil {
		t.Fatal(err)
	}
	g, _ = d.svc.Get(id, "")
	if g.Deleted || g.Body != "milk\n\neggs\n\nbread\n" {
		t.Fatalf("after Restore: %+v", g)
	}

	h, err := d.svc.History(id, 10, "")
	if err != nil || len(h.Items) != 4 || h.Items[0].RevID != g.RevID || h.NextCursor != "" {
		t.Fatalf("History = %+v, %v", h, err)
	}
	h1, _ := d.svc.History(id, 3, "")
	if len(h1.Items) != 3 || h1.NextCursor == "" {
		t.Fatalf("first history page = %+v", h1)
	}
	h2, _ := d.svc.History(id, 3, h1.NextCursor)
	if len(h2.Items) != 1 || h2.Items[0].RevID != rev1 {
		t.Fatalf("second history page = %+v", h2)
	}

	changed := d.eventsOf("notes.changed")
	if len(changed) != 4 { // create, update, delete, restore
		t.Fatalf("notes.changed events = %d, want 4", len(changed))
	}
	for _, ev := range changed {
		if ev["note_id"] != id || ev["rev_id"] == "" {
			t.Fatalf("event = %v", ev)
		}
		if _, hasBody := ev["body"]; hasBody {
			t.Fatal("events must not carry the text")
		}
	}
}

func TestUpdateWithoutChangeMakesNoRevision(t *testing.T) {
	d, clk := one(t)
	id := d.mustCreate("T", "one\n")
	g, _ := d.svc.Get(id, "")
	clk.tick()
	up, err := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Title: strp("T"), Body: strp("one\n\n\n")})
	if err != nil || up.RevID != g.RevID {
		t.Fatalf("no-op update = %+v, %v", up, err)
	}
	if g2, _ := d.svc.Get(id, ""); g2.RevCount != 1 {
		t.Fatalf("a no-op edit made a revision: %+v", g2)
	}
	if n := len(d.eventsOf("notes.changed")); n != 1 {
		t.Fatalf("events = %d, want only the creation", n)
	}
}

func TestInputIsValidated(t *testing.T) {
	d, _ := one(t)
	if _, err := d.svc.Create(strings.Repeat("x", MaxTitleBytes+1), ""); errCode(err) != string(proto.CodeTooLarge) {
		t.Fatalf("long title: %v", err)
	}
	if _, err := d.svc.Create("", strings.Repeat("x\n\n", MaxBodyBytes)); errCode(err) != string(proto.CodeTooLarge) {
		t.Fatalf("long body: %v", err)
	}
	id := d.mustCreate("a\tb\nc", "")
	if g, _ := d.svc.Get(id, ""); g.Title != "a b c" {
		t.Fatalf("title = %q: control characters must become spaces", g.Title)
	}
	for _, bad := range []string{"", "nt_short", "../etc", strings.ToUpper(id)} {
		if _, err := d.svc.Get(bad, ""); errCode(err) != string(proto.CodeInvalid) {
			t.Fatalf("Get(%q) = %v", bad, err)
		}
	}
	missing := newNoteID()
	if _, err := d.svc.Get(missing, ""); errCode(err) != string(proto.CodeNotFound) {
		t.Fatalf("missing note: %v", err)
	}
	if _, err := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: "r_00000000000000000000000000000000", Body: strp("x")}); errCode(err) != string(proto.CodeNotFound) {
		t.Fatalf("unknown base revision: %v", err)
	}
	if _, err := d.svc.Update(UpdateParams{NoteID: id, Body: strp("x"), Patch: []BlockPatch{{BlockID: "b_x", Body: "y"}}}); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("body and patch together: %v", err)
	}
	other := d.mustCreate("other", "text\n")
	g, _ := d.svc.Get(other, "")
	if _, err := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Body: strp("x")}); errCode(err) != string(proto.CodeNotFound) {
		t.Fatalf("a revision of another note as the base: %v", err)
	}
	if _, err := d.svc.List(10, "garbage", false); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestPatchReplacesOneBlock(t *testing.T) {
	d, _ := one(t)
	id := d.mustCreate("", "first\n\nsecond\n\nthird\n")
	rd, err := d.svc.Render(id, "")
	if err != nil || len(rd.Blocks) != 3 {
		t.Fatalf("Render = %+v, %v", rd, err)
	}
	g, _ := d.svc.Get(id, "")
	up, err := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Patch: []BlockPatch{{BlockID: rd.Blocks[1].BlockID, Body: "SECOND"}}})
	if err != nil {
		t.Fatal(err)
	}
	if g2, _ := d.svc.Get(id, up.RevID); g2.Body != "first\n\nSECOND\n\nthird\n" {
		t.Fatalf("patched body = %q", g2.Body)
	}
	// removing a block
	g, _ = d.svc.Get(id, "")
	up, err = d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Patch: []BlockPatch{{BlockID: rd.Blocks[0].BlockID, Body: ""}}})
	if err != nil {
		t.Fatal(err)
	}
	if g2, _ := d.svc.Get(id, up.RevID); g2.Body != "SECOND\n\nthird\n" {
		t.Fatalf("body after removing a block = %q", g2.Body)
	}
	if _, err := d.svc.Update(UpdateParams{NoteID: id, Patch: []BlockPatch{{BlockID: "b_0000000000000000", Body: "x"}}}); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("patching an unknown block: %v", err)
	}
	if _, err := d.svc.Update(UpdateParams{NoteID: id, Patch: []BlockPatch{{BlockID: rd.Blocks[2].BlockID, Body: "a"}, {BlockID: rd.Blocks[2].BlockID, Body: "b"}}}); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("patching a block twice: %v", err)
	}
}

func TestStaleBaseEditMergesWhenDifferentBlocksChanged(t *testing.T) {
	d, clk := one(t)
	id := d.mustCreate("Plan", "# Plan\n\nalpha\n\nbeta\n")
	g, _ := d.svc.Get(id, "")
	base := g.RevID

	clk.tick()
	if _, err := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: base, Body: strp("# Plan v2\n\nalpha\n\nbeta\n")}); err != nil {
		t.Fatal(err)
	}
	clk.tick()
	// a second editor still looking at the first revision appends a paragraph
	up, err := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: base, Body: strp("# Plan\n\nalpha\n\nbeta\n\ngamma\n")})
	if err != nil || !up.Merged || up.ConflictNoteID != "" {
		t.Fatalf("stale update = %+v, %v", up, err)
	}
	g, _ = d.svc.Get(id, "")
	if g.Body != "# Plan v2\n\nalpha\n\nbeta\n\ngamma\n" || len(g.Parents) != 2 || g.OriginDevice != MergeOrigin {
		t.Fatalf("merged note = %+v", g)
	}
	conf, _ := d.svc.Conflicts(10, "")
	if len(conf.Items) != 0 {
		t.Fatalf("a clean merge made conflicts: %+v", conf)
	}
}

func TestStaleBaseEditOnTheSameBlockKeepsBothAsConflictCopy(t *testing.T) {
	d, clk := one(t)
	id := d.mustCreate("Notes", "alpha\n\nbeta\n")
	g, _ := d.svc.Get(id, "")
	base := g.RevID

	clk.tick()
	if _, err := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: base, Body: strp("alpha\n\nbeta from phone\n")}); err != nil {
		t.Fatal(err)
	}
	clk.tick()
	up, err := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: base, Body: strp("alpha\n\nbeta from pc\n")})
	if err != nil || !up.Merged || up.ConflictNoteID == "" {
		t.Fatalf("conflicting stale update = %+v, %v", up, err)
	}
	// the newer edit is the note; the older one is not lost
	main, _ := d.svc.Get(id, "")
	if main.Body != "alpha\n\nbeta from pc\n" {
		t.Fatalf("note body = %q", main.Body)
	}
	cp, err := d.svc.Get(up.ConflictNoteID, "")
	if err != nil || cp.ConflictOf != id || !strings.Contains(cp.Body, "beta from phone") || !strings.HasPrefix(cp.Body, "> "+ConflictBannerPrefix) || !strings.Contains(cp.Title, "（冲突 来自 ") {
		t.Fatalf("conflict copy = %+v, %v", cp, err)
	}
	conf, _ := d.svc.Conflicts(10, "")
	if len(conf.Items) != 1 || conf.Items[0].NoteID != id || conf.Items[0].ConflictNoteID != up.ConflictNoteID {
		t.Fatalf("Conflicts = %+v", conf)
	}
	if evs := d.eventsOf("notes.sync.conflict"); len(evs) != 1 || evs[0]["conflict_note_id"] != up.ConflictNoteID || evs[0]["reason"] != "diverged" {
		t.Fatalf("conflict events = %v", evs)
	}
}

func conflictFixture(t *testing.T) (d *device, clk *clock, id, copyID string) {
	d, clk = one(t)
	id = d.mustCreate("Notes", "alpha\n\nbeta\n")
	g, _ := d.svc.Get(id, "")
	clk.tick()
	if _, err := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Title: strp("Notes A"), Body: strp("alpha\n\nbeta A\n")}); err != nil {
		t.Fatal(err)
	}
	clk.tick()
	up, err := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Body: strp("alpha\n\nbeta B\n")})
	if err != nil || up.ConflictNoteID == "" {
		t.Fatalf("fixture: %+v %v", up, err)
	}
	return d, clk, id, up.ConflictNoteID
}

func TestResolveKeepMainDropsTheCopy(t *testing.T) {
	d, _, id, cp := conflictFixture(t)
	main, _ := d.svc.Get(id, "")
	if _, err := d.svc.Resolve(id, cp, "keep_main"); err != nil {
		t.Fatal(err)
	}
	if g, _ := d.svc.Get(id, ""); g.RevID != main.RevID {
		t.Fatal("keep_main changed the note")
	}
	if conf, _ := d.svc.Conflicts(10, ""); len(conf.Items) != 0 {
		t.Fatalf("copy still listed: %+v", conf)
	}
	if trash, _ := d.svc.List(10, "", true); len(trash.Items) != 1 || trash.Items[0].NoteID != cp {
		t.Fatalf("the copy should be in the trash: %+v", trash)
	}
	if _, err := d.svc.Resolve(id, cp, "keep_main"); errCode(err) != string(proto.CodeNotFound) {
		t.Fatalf("resolving twice: %v", err)
	}
}

func TestResolveKeepConflictPutsTheCopyTextBack(t *testing.T) {
	d, _, id, cp := conflictFixture(t)
	if _, err := d.svc.Resolve(id, cp, "keep_conflict"); err != nil {
		t.Fatal(err)
	}
	g, _ := d.svc.Get(id, "")
	if g.Body != "alpha\n\nbeta A\n" || g.Title != "Notes A" {
		t.Fatalf("note after keep_conflict = title %q body %q (the banner and title suffix must be gone)", g.Title, g.Body)
	}
	if conf, _ := d.svc.Conflicts(10, ""); len(conf.Items) != 0 {
		t.Fatalf("copy still listed: %+v", conf)
	}
}

func TestResolveMergeAppendsTheCopyText(t *testing.T) {
	d, _, id, cp := conflictFixture(t)
	if _, err := d.svc.Resolve(id, cp, "merge"); err != nil {
		t.Fatal(err)
	}
	g, _ := d.svc.Get(id, "")
	if !strings.Contains(g.Body, "beta B") || !strings.Contains(g.Body, "beta A") || strings.Contains(g.Body, ConflictBannerPrefix) {
		t.Fatalf("merged body = %q", g.Body)
	}
	if _, err := d.svc.Resolve(id, cp, "nonsense"); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("unknown action: %v", err)
	}
}

func TestNormalizePreview(t *testing.T) {
	d, _ := one(t)
	norm, hash, err := d.svc.Normalize("a  \r\n\r\n\r\nb")
	if err != nil || norm != "a\n\nb\n" || !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("Normalize = %q %q %v", norm, hash, err)
	}
	id := d.mustCreate("", "# Heading line\n\nlots of body text here\n")
	page, _ := d.svc.List(10, "", false)
	if page.Items[0].NoteID != id || page.Items[0].Title != "Heading line" || !strings.Contains(page.Items[0].Preview, "lots of body") || strings.Contains(page.Items[0].Preview, "Heading line") {
		t.Fatalf("list item without a title = %+v", page.Items[0])
	}
}

func TestListTextsArePlainText(t *testing.T) {
	d, _ := one(t)
	id := d.mustCreate("", "# Title *with* emphasis\n\nSee [the docs](http://example.com/x) and `code` and **bold**\n\n- item one\n- item two\n")
	page, _ := d.svc.List(10, "", false)
	got := page.Items[0]
	if got.NoteID != id || got.Title != "Title with emphasis" || got.Preview != "See the docs and code and bold item one item two" {
		t.Fatalf("list item = %+v", got)
	}
	// a stored title is kept, and the first line is then part of the preview
	id2 := d.mustCreate("Mine", "# Heading\n\nbody\n")
	page, _ = d.svc.List(10, "", false)
	for _, it := range page.Items {
		if it.NoteID == id2 && (it.Title != "Mine" || it.Preview != "Heading body") {
			t.Fatalf("titled item = %+v", it)
		}
	}
	long := d.mustCreate("", strings.Repeat("word ", 100)+"\n\n"+strings.Repeat("more ", 100)+"\n")
	page, _ = d.svc.List(10, "", false)
	for _, it := range page.Items {
		if it.NoteID == long && (len([]rune(it.Title)) > maxDisplayTitle || len([]rune(it.Preview)) > maxPreviewRunes) {
			t.Fatalf("list texts not cut: %d / %d runes", len([]rune(it.Title)), len([]rune(it.Preview)))
		}
	}
}

func TestRenderTextPreviewsUnsavedText(t *testing.T) {
	d, _ := one(t)
	r, err := d.svc.RenderText("# Hi\r\n\r\ntext  \r\n")
	if err != nil || r.NoteID != "" || r.RevID != "" || len(r.Blocks) != 2 || r.Blocks[0].Kind != "heading" || len(r.TOC) != 1 {
		t.Fatalf("RenderText = %+v, %v", r, err)
	}
	if _, err := d.svc.RenderText(strings.Repeat("x\n\n", MaxBodyBytes)); errCode(err) != string(proto.CodeTooLarge) {
		t.Fatalf("huge text: %v", err)
	}
}

func TestUpdateReportsWhetherAnythingChanged(t *testing.T) {
	d, clk := one(t)
	id := d.mustCreate("T", "one\n")
	g, _ := d.svc.Get(id, "")
	clk.tick()
	same, _ := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Body: strp("one")})
	if same.Changed || same.RevID != g.RevID {
		t.Fatalf("no-op update = %+v", same)
	}
	changed, _ := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Body: strp("two")})
	if !changed.Changed || changed.RevID == g.RevID {
		t.Fatalf("real update = %+v", changed)
	}
	// a stale base whose edit changes nothing must not look like a new revision of that edit
	stale, _ := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Body: strp("one")})
	if stale.Changed || stale.RevID != changed.RevID {
		t.Fatalf("stale no-op update = %+v", stale)
	}
}
