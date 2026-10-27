package notes

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"nightclip/core/proto"
	"nightclip/core/store"
)

func twoDevices(t *testing.T) (a, b *device, clk *clock) {
	clk = &clock{ms: 1_700_000_000_000}
	a, b = newDevice(t, 0, clk), newDevice(t, 1, clk)
	pair(t, a, b)
	return
}

func sameView(t *testing.T, devs ...*device) {
	t.Helper()
	first := devs[0].snapshot()
	for _, d := range devs[1:] {
		if got := d.snapshot(); !reflect.DeepEqual(first, got) {
			t.Fatalf("devices disagree:\n%s: %+v\n%s: %+v", devs[0].id[len(devs[0].id)-1:], first, d.id[len(d.id)-1:], got)
		}
	}
}

func TestSyncCopiesNotesBothWays(t *testing.T) {
	a, b, clk := twoDevices(t)
	n1 := a.mustCreate("from a", "hello\n")
	clk.tick()
	n2 := b.mustCreate("from b", "world\n")

	a.syncWith(b)
	sameView(t, a, b)
	for _, d := range []*device{a, b} {
		if g, err := d.svc.Get(n1, ""); err != nil || g.Body != "hello\n" || g.Title != "from a" {
			t.Fatalf("n1 on %s: %+v %v", d.id, g, err)
		}
		if g, err := d.svc.Get(n2, ""); err != nil || g.Body != "world\n" {
			t.Fatalf("n2 on %s: %+v %v", d.id, g, err)
		}
	}
	if evs := b.eventsOf("notes.changed"); len(evs) != 2 { // its own note, then a's
		t.Fatalf("b's change events = %d", len(evs))
	}
	if st := a.eventsOf("notes.sync.updated"); len(st) == 0 || st[len(st)-1]["state"] != StateIdle {
		t.Fatalf("a's sync events = %v", st)
	}
	if status, _ := a.svc.Status(); len(status.Peers) != 1 || status.Peers[0].State != StateIdle || status.Peers[0].LastSyncAt == 0 || status.Peers[0].Behind != 0 {
		t.Fatalf("status = %+v", status)
	}
}

func TestSyncIsIdempotentAndQuietWhenNothingChanged(t *testing.T) {
	a, b, clk := twoDevices(t)
	a.mustCreate("x", "one\n")
	clk.tick()
	b.mustCreate("y", "two\n")
	a.syncWith(b)
	seqA, seqB := seqOf(t, a), seqOf(t, b)
	snap := a.snapshot()
	for i := 0; i < 3; i++ {
		a.syncWith(b)
	}
	if seqOf(t, a) != seqA || seqOf(t, b) != seqB || !reflect.DeepEqual(a.snapshot(), snap) {
		t.Fatal("repeating a sync changed the state")
	}
	// a normal (non-forced) round with nothing new does not even look at the digest
	a.svc.runRound(t.Context(), b.id, localRemote{b.svc}, false)
	a.svc.runRound(t.Context(), b.id, localRemote{b.svc}, false)
	if n := len(a.eventsOf("notes.changed")); n != 2 {
		t.Fatalf("a announced %d changes, want 2", n)
	}
}

func seqOf(t *testing.T, d *device) int64 {
	t.Helper()
	var n int64
	if err := d.db.NotesView(func(tx *store.NotesTx) (err error) { n, err = tx.Seq(); return }); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestConcurrentEditsOfDifferentBlocksMergeIdenticallyOnBothDevices(t *testing.T) {
	a, b, clk := twoDevices(t)
	id := a.mustCreate("Plan", "# Plan\n\nalpha\n\nbeta\n")
	clk.tick()
	a.syncWith(b)

	clk.tick()
	ga, _ := a.svc.Get(id, "")
	if _, err := a.svc.Update(UpdateParams{NoteID: id, BaseRevID: ga.RevID, Body: strp("# Plan v2\n\nalpha\n\nbeta\n")}); err != nil {
		t.Fatal(err)
	}
	clk.tick()
	gb, _ := b.svc.Get(id, "")
	if _, err := b.svc.Update(UpdateParams{NoteID: id, BaseRevID: gb.RevID, Body: strp("# Plan\n\nalpha\n\nbeta\n\ngamma\n")}); err != nil {
		t.Fatal(err)
	}
	// each side alone is not merged yet
	if x, _ := a.svc.Get(id, ""); strings.Contains(x.Body, "gamma") {
		t.Fatal("a saw b's edit before syncing")
	}

	a.syncWith(b)
	sameView(t, a, b)
	g, _ := a.svc.Get(id, "")
	if g.Body != "# Plan v2\n\nalpha\n\nbeta\n\ngamma\n" || len(g.Parents) != 2 {
		t.Fatalf("merged note = %+v", g)
	}
	if conf, _ := a.svc.Conflicts(10, ""); len(conf.Items) != 0 {
		t.Fatalf("clean merge produced conflicts: %+v", conf)
	}
	// both devices computed the very same merge revision: after the round there is nothing more to exchange
	before := seqOf(t, b)
	a.syncWith(b)
	if seqOf(t, b) != before {
		t.Fatal("a second round still moved revisions")
	}
}

func TestTwoPeopleEditingDifferentItemsOfOneListMergeWithoutAConflictCopy(t *testing.T) {
	a, b, clk := twoDevices(t)
	id := a.mustCreate("Shopping", "# Shopping\n\n- milk\n- eggs\n- bread\n- jam\n")
	clk.tick()
	a.syncWith(b)

	clk.tick()
	ga, _ := a.svc.Get(id, "")
	_, _ = a.svc.Update(UpdateParams{NoteID: id, BaseRevID: ga.RevID, Body: strp("# Shopping\n\n- [x] milk\n- eggs\n- bread\n- jam\n")})
	clk.tick()
	gb, _ := b.svc.Get(id, "")
	_, _ = b.svc.Update(UpdateParams{NoteID: id, BaseRevID: gb.RevID, Body: strp("# Shopping\n\n- milk\n- eggs\n- bread\n- jam\n- honey\n")})

	a.syncWith(b)
	sameView(t, a, b)
	g, _ := a.svc.Get(id, "")
	if g.Body != "# Shopping\n\n- [x] milk\n- eggs\n- bread\n- jam\n- honey\n" {
		t.Fatalf("merged list = %q", g.Body)
	}
	if conf, _ := a.svc.Conflicts(10, ""); len(conf.Items) != 0 || len(a.snapshot()) != 1 {
		t.Fatalf("a conflict copy was made for edits on different lines: %+v", conf)
	}

	// the same item edited on both sides is still a conflict, and still loses nothing
	clk.tick()
	ga, _ = a.svc.Get(id, "")
	_, _ = a.svc.Update(UpdateParams{NoteID: id, BaseRevID: ga.RevID, Body: strp("# Shopping\n\n- [x] milk\n- eggs (a dozen)\n- bread\n- jam\n- honey\n")})
	clk.tick()
	gb, _ = b.svc.Get(id, "")
	_, _ = b.svc.Update(UpdateParams{NoteID: id, BaseRevID: gb.RevID, Body: strp("# Shopping\n\n- [x] milk\n- eggs (free range)\n- bread\n- jam\n- honey\n")})
	a.syncWith(b)
	sameView(t, a, b)
	if conf, _ := a.svc.Conflicts(10, ""); len(conf.Items) != 1 {
		t.Fatalf("the same item edited twice must leave one copy: %+v", conf)
	}
}

func TestConcurrentEditsOfTheSameBlockLeaveOneConflictCopyEverywhere(t *testing.T) {
	a, b, clk := twoDevices(t)
	id := a.mustCreate("Doc", "alpha\n\nbeta\n")
	clk.tick()
	a.syncWith(b)

	clk.tick()
	ga, _ := a.svc.Get(id, "")
	_, _ = a.svc.Update(UpdateParams{NoteID: id, BaseRevID: ga.RevID, Body: strp("alpha\n\nbeta by a\n")})
	clk.tick()
	gb, _ := b.svc.Get(id, "")
	_, _ = b.svc.Update(UpdateParams{NoteID: id, BaseRevID: gb.RevID, Body: strp("alpha\n\nbeta by b\n")})

	a.syncWith(b)
	sameView(t, a, b)
	snap := a.snapshot()
	if len(snap) != 2 {
		t.Fatalf("want the note and one conflict copy, got %d notes: %+v", len(snap), snap)
	}
	g, _ := a.svc.Get(id, "")
	if g.Body != "alpha\n\nbeta by b\n" { // b edited later
		t.Fatalf("the newer edit must be the note, got %q", g.Body)
	}
	for _, d := range []*device{a, b} {
		conf, _ := d.svc.Conflicts(10, "")
		if len(conf.Items) != 1 || conf.Items[0].NoteID != id {
			t.Fatalf("conflicts on %s = %+v", d.id, conf)
		}
		cp, _ := d.svc.Get(conf.Items[0].ConflictNoteID, "")
		if !strings.Contains(cp.Body, "beta by a") {
			t.Fatalf("the older edit was lost: %q", cp.Body)
		}
	}
	// resolving on one device propagates to the other
	conf, _ := a.svc.Conflicts(10, "")
	clk.tick()
	if _, err := a.svc.Resolve(id, conf.Items[0].ConflictNoteID, "keep_main"); err != nil {
		t.Fatal(err)
	}
	a.syncWith(b)
	sameView(t, a, b)
	if conf, _ := b.svc.Conflicts(10, ""); len(conf.Items) != 0 {
		t.Fatalf("b still lists the resolved conflict: %+v", conf)
	}
}

func TestDeleteBeatsAnUntouchedNoteButNotANewerEdit(t *testing.T) {
	a, b, clk := twoDevices(t)
	keep := a.mustCreate("edited elsewhere", "v1\n")
	gone := a.mustCreate("deleted", "v1\n")
	clk.tick()
	a.syncWith(b)

	clk.tick()
	_, _ = a.svc.Delete(keep)
	_, _ = a.svc.Delete(gone)
	clk.tick()
	g, _ := b.svc.Get(keep, "")
	_, _ = b.svc.Update(UpdateParams{NoteID: keep, BaseRevID: g.RevID, Body: strp("v2 written on b\n")})

	a.syncWith(b)
	sameView(t, a, b)
	snap := a.snapshot()
	if !snap[gone].Deleted {
		t.Fatal("a deleted note that nobody touched must stay deleted on both devices")
	}
	if snap[keep].Deleted || snap[keep].Body != "v2 written on b\n" {
		t.Fatalf("an edit made elsewhere must survive the delete: %+v", snap[keep])
	}
	var elsewhere int
	for _, d := range []*device{a, b} {
		for _, ev := range d.eventsOf("notes.sync.conflict") {
			if ev["reason"] == "deleted_elsewhere" && ev["note_id"] == keep {
				elsewhere++
			}
		}
	}
	if elsewhere == 0 {
		t.Fatal("nobody was told that a note was deleted elsewhere but edited here")
	}
}

func TestOldRevisionsCannotBringADeletedNoteBack(t *testing.T) {
	a, b, clk := twoDevices(t)
	id := a.mustCreate("x", "one\n")
	clk.tick()
	a.syncWith(b)
	clk.tick()
	_, _ = a.svc.Delete(id)
	a.syncWith(b)
	// b pushes everything it has again (it still holds the old revisions)
	b.syncWith(a)
	a.syncWith(b)
	sameView(t, a, b)
	if !a.snapshot()[id].Deleted || !b.snapshot()[id].Deleted {
		t.Fatal("the deleted note came back")
	}
	// the user can still bring it back on purpose
	clk.tick()
	if _, err := b.svc.Restore(id); err != nil {
		t.Fatal(err)
	}
	a.syncWith(b)
	sameView(t, a, b)
	if a.snapshot()[id].Deleted {
		t.Fatal("restore did not propagate")
	}
}

func TestLongHistoryAndManyNotesSyncCompletely(t *testing.T) {
	a, b, clk := twoDevices(t)
	id := a.mustCreate("long", "0\n")
	for i := 1; i <= 430; i++ { // more than one pull answer holds
		clk.tick()
		g, _ := a.svc.Get(id, "")
		if _, err := a.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Body: strp(fmt.Sprintf("%d\n", i))}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 60; i++ {
		clk.tick()
		a.mustCreate(fmt.Sprintf("n%d", i), "x\n")
	}
	b.syncWith(a) // b dials and pulls everything
	sameView(t, a, b)
	if h, _ := b.svc.History(id, 500, ""); len(h.Items) != 431 {
		t.Fatalf("history on b has %d revisions, want 431", len(h.Items))
	}
	// and the other direction: a dials, b is ahead by one more edit
	clk.tick()
	g, _ := b.svc.Get(id, "")
	_, _ = b.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Body: strp("last\n")})
	a.syncWith(b)
	sameView(t, a, b)
}

func TestPeerSendingBadRevisionsIsRefusedOnlyForThoseRevisions(t *testing.T) {
	_, b, clk := twoDevices(t)
	good := seal(rev("nt_"+strings.Repeat("a", 26), nil, "good\n", clk))

	forged := toWire(good)
	forged.Body = "tampered\n" // id no longer matches
	notNormal := toWire(seal(rev("nt_"+strings.Repeat("b", 26), nil, "text\n\n\n\nmore\n", clk)))
	notNormal.Body = "text\n\n\n\nmore\n"
	tooNew := toWire(good)
	tooNew.FormatVersion = 99
	badOrigin := toWire(seal(withOrigin(good, "evil")))
	orphan := toWire(seal(rev("nt_"+strings.Repeat("c", 26), []string{"r_" + strings.Repeat("0", 32)}, "x\n", clk)))

	rep, err := b.svc.applyWire([]WireRev{forged, notNormal, tooNew, badOrigin, orphan, toWire(good)})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Accepted != 1 || rep.Inserted != 1 || len(rep.Rejected) != 5 {
		t.Fatalf("reply = %+v", rep)
	}
	codes := map[string]bool{}
	for _, r := range rep.Rejected {
		codes[r.Code] = true
	}
	if !codes[string(proto.CodeUnsupportedFormat)] || !codes[string(proto.CodeInvalid)] || !codes["MISSING_PARENT"] {
		t.Fatalf("rejection codes = %v", codes)
	}
	if page, _ := b.svc.List(10, "", false); len(page.Items) != 1 {
		t.Fatalf("only the valid revision may be stored, got %+v", page)
	}
	if firstUnsupported(rep) == nil {
		t.Fatal("a too-new format must be reported as such")
	}
}

func rev(noteID string, parents []string, body string, clk *clock) store.NoteRev {
	return store.NoteRev{NoteID: noteID, Parents: parents, Title: "t", Body: normForTest(body), FormatVersion: 1,
		OriginDevice: deviceID(0), CreatedAt: clk.Now().UnixMilli()}
}

func withOrigin(r store.NoteRev, origin string) store.NoteRev {
	r.OriginDevice = origin
	return r
}

func normForTest(s string) string {
	n, err := normBody(s)
	if err != nil {
		panic(err)
	}
	return n
}

// ---- the property: any order of syncs ends in the same notes ------------------------------------------

func TestDevicesConvergeWhateverTheOrderOfEditsAndSyncs(t *testing.T) {
	seeds := 30
	if testing.Short() {
		seeds = 8
	}
	var merges, diverged, elsewhere int
	defer func() {
		t.Logf("exercised: %d notes ending on a merge revision, %d conflict copies, %d delete-vs-edit cases", merges, diverged, elsewhere)
		if !testing.Short() && (merges == 0 || diverged == 0 || elsewhere == 0) {
			t.Errorf("the generator no longer reaches every merge path (merges=%d conflict copies=%d delete-vs-edit=%d)", merges, diverged, elsewhere)
		}
	}()
	for seed := 0; seed < seeds; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(seed)))
			clk := &clock{ms: 1_700_000_000_000}
			devs := []*device{newDevice(t, 0, clk), newDevice(t, 1, clk), newDevice(t, 2, clk)}
			pair(t, devs...)
			var ids []string

			randomEdit := func(d *device) {
				clk.tick()
				if len(ids) == 0 || rng.Intn(6) == 0 {
					ids = append(ids, d.mustCreate(fmt.Sprintf("t%d", rng.Intn(3)), randomBody(rng)))
					return
				}
				id := ids[rng.Intn(len(ids))]
				g, err := d.svc.Get(id, "")
				if err != nil {
					return // this device has not seen the note yet
				}
				switch r := rng.Intn(10); {
				case r < 6 && !g.Deleted:
					_, _ = d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Body: strp(mutateBody(rng, g.Body))})
				case r < 7 && !g.Deleted:
					_, _ = d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Title: strp(fmt.Sprintf("t%d", rng.Intn(3)))})
				case r < 9:
					_, _ = d.svc.Delete(id)
				default:
					_, _ = d.svc.Restore(id)
				}
			}
			for step := 0; step < 40; step++ {
				switch rng.Intn(3) {
				case 0, 1:
					randomEdit(devs[rng.Intn(3)])
				default:
					i, j := rng.Intn(3), rng.Intn(3)
					if i != j {
						devs[i].syncWith(devs[j])
					}
				}
			}
			// settle: sync every pair, both directions, twice
			for round := 0; round < 2; round++ {
				for i := range devs {
					for j := range devs {
						if i != j {
							devs[i].syncWith(devs[j])
						}
					}
				}
			}
			sameView(t, devs...)
			for _, d := range devs {
				for _, ev := range d.eventsOf("notes.sync.conflict") {
					if ev["reason"] == "diverged" {
						diverged++
					} else {
						elsewhere++
					}
				}
			}
			// how many notes ended on a merge revision (counted on one device: they all agree)
			for id := range devs[0].snapshot() {
				if g, err := devs[0].svc.Get(id, ""); err == nil && g.OriginDevice == MergeOrigin {
					merges++
				}
			}
			// no device is left with an unresolved fork
			for _, d := range devs {
				err := d.db.NotesView(func(tx *store.NotesTx) error {
					for _, id := range ids {
						if _, found, _ := tx.GetNote(id); !found {
							continue
						}
						if heads, _ := tx.Heads(id); len(heads) != 1 {
							t.Fatalf("note %s has %d heads on %s", id, len(heads), d.id)
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func randomBody(rng *rand.Rand) string {
	var parts []string
	for i := 0; i < 1+rng.Intn(4); i++ {
		if rng.Intn(3) == 0 { // a list is one block: its items merge line by line
			parts = append(parts, fmt.Sprintf("- item %d\n- item %d\n- item %d", rng.Intn(5), rng.Intn(5), rng.Intn(5)))
			continue
		}
		parts = append(parts, fmt.Sprintf("para %d", rng.Intn(5)))
	}
	return strings.Join(parts, "\n\n") + "\n"
}

func mutateBody(rng *rand.Rand, body string) string {
	parts := strings.Split(strings.TrimSuffix(body, "\n"), "\n\n")
	if i := rng.Intn(len(parts)); strings.HasPrefix(parts[i], "- ") && rng.Intn(2) == 0 {
		lines := strings.Split(parts[i], "\n")
		switch rng.Intn(3) {
		case 0:
			lines = append(lines, fmt.Sprintf("- added %d", rng.Intn(50)))
		case 1:
			lines[rng.Intn(len(lines))] = fmt.Sprintf("- edited %d", rng.Intn(50))
		default:
			if len(lines) > 1 {
				j := rng.Intn(len(lines))
				lines = append(lines[:j], lines[j+1:]...)
			}
		}
		parts[i] = strings.Join(lines, "\n")
		return strings.Join(parts, "\n\n") + "\n"
	}
	switch rng.Intn(3) {
	case 0:
		parts = append(parts, fmt.Sprintf("added %d", rng.Intn(50)))
	case 1:
		if len(parts) > 1 {
			i := rng.Intn(len(parts))
			parts = append(parts[:i], parts[i+1:]...)
		}
	default:
		parts[rng.Intn(len(parts))] = fmt.Sprintf("edited %d", rng.Intn(50))
	}
	return strings.Join(parts, "\n\n") + "\n"
}

func TestSettingTheRevisionGraphIsAGrowOnlySet(t *testing.T) {
	// Syncing a then b equals syncing b then a: the revision sets only grow and the merge is a pure
	// function of them, so the order cannot matter. Checked on a fixed fork with three heads.
	for _, order := range [][3]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}} {
		clk := &clock{ms: 1_700_000_000_000}
		devs := []*device{newDevice(t, 0, clk), newDevice(t, 1, clk), newDevice(t, 2, clk)}
		pair(t, devs...)
		id := devs[0].mustCreate("T", "p1\n\np2\n\np3\n")
		clk.tick()
		devs[1].syncWith(devs[0])
		devs[2].syncWith(devs[0])
		bodies := []string{"P1\n\np2\n\np3\n", "p1\n\nP2\n\np3\n", "p1\n\np2\n\nP3\n"}
		for i, d := range devs {
			clk.tick()
			g, _ := d.svc.Get(id, "")
			if _, err := d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Body: strp(bodies[i])}); err != nil {
				t.Fatal(err)
			}
		}
		devs[order[0]].syncWith(devs[order[1]])
		devs[order[1]].syncWith(devs[order[2]])
		devs[order[2]].syncWith(devs[order[0]])
		devs[order[0]].syncWith(devs[order[1]])
		sameView(t, devs...)
		if g, _ := devs[0].svc.Get(id, ""); g.Body != "P1\n\nP2\n\nP3\n" {
			t.Fatalf("order %v: merged body = %q", order, g.Body)
		}
	}
}
