package vault

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { c.t = c.t.Add(time.Second); return c.t }

// fork copies the vault's file into a new directory and opens it: a second device.
func fork(t *testing.T, v *Vault, clock *fakeClock) *Vault {
	t.Helper()
	data, err := os.ReadFile(v.path())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "fork")
	_ = os.MkdirAll(dir, 0o700)
	if err := os.WriteFile(filepath.Join(dir, FileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Open(dir, v.creds.clone())
	if err != nil {
		t.Fatal(err)
	}
	f.now = clock.Now
	t.Cleanup(f.Close)
	return f
}

// state is a canonical picture of what a vault says, ignoring file-level randomness.
func state(v *Vault) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	var lines []string
	walk(v.db, func(le *libEntry, gid string, trash bool) {
		var hist []string
		for _, h := range le.Histories {
			for i := range h.Entries {
				hist = append(hist, fmt.Sprintf("%d:%s", unix(h.Entries[i].Times.LastModificationTime), contentHash(v.db, &h.Entries[i])[:8]))
			}
		}
		sort.Strings(hist)
		lines = append(lines, fmt.Sprintf("E %s g=%s trash=%v mod=%d h=%s hist=%s", uuidHex(le.UUID), gid, trash, unix(le.Times.LastModificationTime), contentHash(v.db, le)[:8], strings.Join(hist, ",")))
	}, func(gr *libGroup, parent string, trash bool) {
		lines = append(lines, fmt.Sprintf("G %s p=%s name=%s trash=%v", uuidHex(gr.UUID), parent, gr.Name, trash))
	})
	var del []string
	for _, d := range v.db.Content.Root.DeletedObjects {
		del = append(del, uuidHex(d.UUID))
	}
	sort.Strings(del)
	lines = append(lines, "D "+strings.Join(del, ","))
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func merge(t *testing.T, into, from *Vault) MergeStats {
	t.Helper()
	data, err := os.ReadFile(from.path())
	if err != nil {
		t.Fatal(err)
	}
	st, err := into.MergeBytes(data, from.creds, "merge")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func baseVault(t *testing.T) (*Vault, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)}
	v := newTestVault(t)
	v.now = clock.Now
	return v, clock
}

func TestMergeAddsFromBothSides(t *testing.T) {
	a, clock := baseVault(t)
	_, _ = a.AddEntry(Entry{Title: "shared", Password: "s"})
	b := fork(t, a, clock)
	_, _ = a.AddEntry(Entry{Title: "only on A", Password: "a"})
	gid, _ := b.AddGroup("", "Servers")
	_, _ = b.AddEntry(Entry{Title: "only on B", Password: "b", GroupID: gid})

	if st := merge(t, a, b); st.EntriesAdded != 1 || st.GroupsAdded != 1 {
		t.Fatalf("A<-B: %+v", st)
	}
	if st := merge(t, b, a); st.EntriesAdded != 1 {
		t.Fatalf("B<-A: %+v", st)
	}
	if sa, sb := state(a), state(b); sa != sb {
		t.Fatalf("replicas differ:\nA:\n%s\nB:\n%s", sa, sb)
	}
	if l, _ := a.List(Filter{}); len(l) != 3 {
		t.Fatalf("entries: %+v", l)
	}
	if st := merge(t, a, b); st.Changed() {
		t.Fatalf("merging again must change nothing: %+v", st)
	}
}

func TestMergeNewerEditWinsAndTheOtherGoesToHistory(t *testing.T) {
	a, clock := baseVault(t)
	id, _ := a.AddEntry(Entry{Title: "site", Username: "u", Password: "v0"})
	b := fork(t, a, clock)
	_ = a.UpdateEntry(id, Entry{Title: "site", Username: "u", Password: "from-A"})
	_ = b.UpdateEntry(id, Entry{Title: "site", Username: "u", Password: "from-B"}) // later clock
	merge(t, a, b)
	merge(t, b, a)
	for name, v := range map[string]*Vault{"A": a, "B": b} {
		e, _ := v.Get(id)
		if e.Password != "from-B" {
			t.Errorf("%s: password = %q, want the newer edit", name, e.Password)
		}
		h, _ := v.History(id)
		if len(h) < 2 {
			t.Errorf("%s: history lost a version: %+v", name, h)
		}
	}
	if state(a) != state(b) {
		t.Fatal("replicas differ")
	}
}

func TestMergeDeletionRules(t *testing.T) {
	a, clock := baseVault(t)
	gone, _ := a.AddEntry(Entry{Title: "to delete", Password: "x"})
	kept, _ := a.AddEntry(Entry{Title: "edited later", Password: "x"})
	b := fork(t, a, clock)
	// A deletes both for good (twice: bin, then purge)
	for _, id := range []string{gone, kept} {
		_, _ = a.Delete(id)
		_, _ = a.Delete(id)
	}
	// B edits one of them AFTER A deleted it
	_ = b.UpdateEntry(kept, Entry{Title: "edited later", Password: "changed on B"})
	merge(t, b, a)
	if _, err := b.Get(gone); err == nil {
		t.Error("an untouched entry deleted on the other side must disappear")
	}
	if e, err := b.Get(kept); err != nil || e.Password != "changed on B" {
		t.Errorf("a later edit must survive an older deletion: %+v %v", e, err)
	}
	merge(t, a, b)
	if e, err := a.Get(kept); err != nil || e.Password != "changed on B" {
		t.Errorf("A did not get the surviving entry back: %+v %v", e, err)
	}
	if _, err := a.Get(gone); err == nil {
		t.Error("deleted entry resurrected")
	}
	if state(a) != state(b) {
		t.Fatalf("replicas differ:\n%s\n---\n%s", state(a), state(b))
	}
}

func TestMergeTrashMoveAndEditAreIndependent(t *testing.T) {
	a, clock := baseVault(t)
	id, _ := a.AddEntry(Entry{Title: "x", Password: "p0"})
	b := fork(t, a, clock)
	_, _ = a.Delete(id)                                                  // A: into the recycle bin
	_ = b.UpdateEntry(id, Entry{Title: "x", Password: "p1 edited on B"}) // B: edits it
	merge(t, a, b)
	merge(t, b, a)
	e, err := a.Get(id)
	if err != nil || !e.InTrash || e.Password != "p1 edited on B" {
		t.Fatalf("A: %+v %v (want in trash with B's edit)", e, err)
	}
	if state(a) != state(b) {
		t.Fatal("replicas differ")
	}
	// restoring on one side moves it back everywhere
	_ = b.RestoreEntry(id)
	merge(t, a, b)
	if e, _ := a.Get(id); e.InTrash {
		t.Fatal("restore did not propagate")
	}
}

func TestMergeAttachmentsAndGroupsAreCopied(t *testing.T) {
	a, clock := baseVault(t)
	b := fork(t, a, clock)
	gid, _ := b.AddGroup("", "Keys")
	id, _ := b.AddEntry(Entry{Title: "with file", GroupID: gid})
	_ = b.AddAttachment(id, "k.pem", []byte("PEM DATA"))
	merge(t, a, b)
	got, err := a.Attachment(id, "k.pem")
	if err != nil || string(got) != "PEM DATA" {
		t.Fatalf("attachment after merge: %q %v", got, err)
	}
	if e, _ := a.Get(id); e.GroupID != gid {
		t.Fatalf("group not carried: %+v", e)
	}
}

// Random operations on three replicas, then merge them in different orders: every order must
// converge to the same state, and merging again must change nothing.
func TestMergeConvergesUnderRandomOperations(t *testing.T) {
	for seed := int64(1); seed <= seedCount(12); seed++ {
		t.Run(fmt.Sprintf("seed%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			a, clock := baseVault(t)
			var ids []string
			for i := 0; i < 4; i++ {
				id, _ := a.AddEntry(Entry{Title: fmt.Sprintf("seed-%d", i), Password: fmt.Sprintf("p%d", i)})
				ids = append(ids, id)
			}
			g1, _ := a.AddGroup("", "G1")
			b, c := fork(t, a, clock), fork(t, a, clock)
			reps := []*Vault{a, b, c}
			for step := 0; step < 24; step++ {
				v := reps[rng.Intn(3)]
				switch op := rng.Intn(6); op {
				case 0:
					id, _ := v.AddEntry(Entry{Title: fmt.Sprintf("new-%d-%d", seed, step), Password: "n"})
					ids = append(ids, id)
				case 1:
					id := ids[rng.Intn(len(ids))]
					_ = v.UpdateEntry(id, Entry{Title: "edited", Password: fmt.Sprintf("pw-%d", step), Tags: []string{fmt.Sprint(step % 3)}})
				case 2:
					_, _ = v.Delete(ids[rng.Intn(len(ids))])
				case 3:
					_ = v.RestoreEntry(ids[rng.Intn(len(ids))])
				case 4:
					_ = v.MoveEntry(ids[rng.Intn(len(ids))], g1)
				case 5:
					_ = v.AddAttachment(ids[rng.Intn(len(ids))], "f", []byte(fmt.Sprint(step)))
				}
			}
			order := func(pairs [][2]int) {
				for _, p := range pairs {
					merge(t, reps[p[0]], reps[p[1]])
				}
			}
			// two different gossip orders on copies of the same replicas would need more forks;
			// instead run a full exchange twice and require all three states equal
			// Eventual consistency: a version that a replica discarded because of a deletion can come
			// back from a third replica later, so two complete all-pairs rounds are allowed.
			for round := 0; round < 2; round++ {
				order([][2]int{{0, 1}, {0, 2}, {1, 0}, {1, 2}, {2, 0}, {2, 1}})
			}
			s0, s1, s2 := state(reps[0]), state(reps[1]), state(reps[2])
			if s0 != s1 || s1 != s2 {
				t.Fatalf("replicas did not converge\nA:\n%s\nB:\n%s\nC:\n%s", s0, s1, s2)
			}
			for _, p := range [][2]int{{0, 1}, {1, 2}, {2, 0}} {
				if st := merge(t, reps[p[0]], reps[p[1]]); st.Changed() {
					t.Fatalf("merge is not idempotent: %+v", st)
				}
			}
		})
	}
}

// merge(A,B) and merge(B,A) must produce the same logical state from the same inputs.
func TestMergeIsCommutative(t *testing.T) {
	for seed := int64(1); seed <= seedCount(10); seed++ {
		rng := rand.New(rand.NewSource(seed * 77))
		a, clock := baseVault(t)
		var ids []string
		for i := 0; i < 3; i++ {
			id, _ := a.AddEntry(Entry{Title: fmt.Sprintf("e%d", i), Password: "p"})
			ids = append(ids, id)
		}
		b := fork(t, a, clock)
		for step := 0; step < 10; step++ {
			v := []*Vault{a, b}[rng.Intn(2)]
			switch rng.Intn(3) {
			case 0:
				_ = v.UpdateEntry(ids[rng.Intn(len(ids))], Entry{Title: "t", Password: fmt.Sprint(rng.Int())})
			case 1:
				_, _ = v.Delete(ids[rng.Intn(len(ids))])
			case 2:
				_, _ = v.AddEntry(Entry{Title: fmt.Sprint("n", step), Password: "q"})
			}
		}
		ab := fork(t, a, clock)
		ba := fork(t, b, clock)
		merge(t, ab, b) // A then B
		merge(t, ba, a) // B then A
		if state(ab) != state(ba) {
			t.Fatalf("seed %d: merge(A,B) != merge(B,A)\n%s\n---\n%s", seed, state(ab), state(ba))
		}
	}
}

func TestExportImportAndRestore(t *testing.T) {
	a, _ := baseVault(t)
	id, _ := a.AddEntry(Entry{Title: "one", Password: "p1", Notes: "n"})
	_ = a.AddAttachment(id, "a.bin", []byte("data"))
	id2, _ := a.AddEntry(Entry{Title: "two", Password: "p2"})

	// export under a different password, import into a fresh vault
	out, err := a.ExportKDBX(Creds{Password: []byte("export pw")}, KDFTest)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := Create(filepath.Join(t.TempDir(), "f"), creds("other master"), KDFTest, "Fresh")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	st, err := fresh.MergeBytes(out, Creds{Password: []byte("export pw")}, "import")
	if err != nil || st.EntriesAdded != 2 {
		t.Fatalf("import = %+v %v", st, err)
	}
	if att, err := fresh.Attachment(id, "a.bin"); err != nil || string(att) != "data" {
		t.Fatalf("attachment through export/import: %q %v", att, err)
	}
	if _, err := fresh.MergeBytes(out, creds("wrong"), "import"); err == nil {
		t.Fatal("import with the wrong password worked")
	}

	// restore: take a snapshot, break things, then bring them back from it
	snap, err := a.BackupNow()
	if err != nil {
		t.Fatal(err)
	}
	_ = a.UpdateEntry(id, Entry{Title: "one", Password: "CHANGED", Notes: "n"})
	_, _ = a.Delete(id2)
	_, _ = a.Delete(id2) // purge
	good := snap.Name
	d, err := a.PreviewRestore(good, Creds{})
	if err != nil || len(d.OnlyInBackup) != 1 || d.OnlyInBackup[0].Title != "two" || len(d.Changed) != 1 || d.Changed[0].Title != "one" {
		t.Fatalf("preview = %+v %v", d, err)
	}
	if _, err := a.Restore(good, Creds{}, RestoreEntries, []string{id2}); err != nil {
		t.Fatal(err)
	}
	if e, err := a.Get(id2); err != nil || e.Password != "p2" {
		t.Fatalf("restored entry: %+v %v", e, err)
	}
	if e, _ := a.Get(id); e.Password != "CHANGED" {
		t.Fatal("a selective restore touched an entry it was not asked to")
	}
	if _, err := a.Restore(good, Creds{}, RestoreEntries, []string{id}); err != nil {
		t.Fatal(err)
	}
	e, _ := a.Get(id)
	if e.Password == "CHANGED" || e.History < 1 {
		t.Fatalf("restoring one entry: %+v (the replaced version must be in the history)", e)
	}
	// full replace
	if _, err := a.Restore(good, Creds{}, RestoreReplace, nil); err != nil {
		t.Fatal(err)
	}
	if l, _ := a.List(Filter{}); len(l) != 2 {
		t.Fatalf("after replace: %+v", l)
	}
	if att, err := a.Attachment(id, "a.bin"); err != nil || string(att) != "data" {
		t.Fatalf("attachment after replace: %q %v", att, err)
	}
	// a restore snapshot exists, so the restore itself can be undone
	found := false
	for _, b := range ListBackups(a.Dir()) {
		if b.Reason == "restore" {
			found = true
		}
	}
	if !found {
		t.Fatal("no pre-restore snapshot")
	}
}

// seedCount lets a long soak run use many more random seeds: VAULT_SEEDS=500 go test -run Merge.
func seedCount(def int64) int64 {
	if n, err := strconv.ParseInt(os.Getenv("VAULT_SEEDS"), 10, 64); err == nil && n > 0 {
		return n
	}
	return def
}

// A restored entry must not be taken away again by the deletion record it came back from, not even
// when the vault is merged with an export of itself.
func TestRestoreDropsTheTombstone(t *testing.T) {
	a, _ := baseVault(t)
	id, _ := a.AddEntry(Entry{Title: "once deleted", Password: "p"})
	snap, _ := a.BackupNow()
	_, _ = a.Delete(id)
	_, _ = a.Delete(id)
	export, err := a.ExportKDBX(Creds{Password: []byte("x")}, KDFTest) // carries the tombstone
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Restore(snap.Name, Creds{}, RestoreEntries, []string{id}); err != nil {
		t.Fatal(err)
	}
	if len(a.db.Content.Root.DeletedObjects) != 0 {
		t.Fatalf("tombstone survived the restore: %+v", a.db.Content.Root.DeletedObjects)
	}
	if st, err := a.MergeBytes(export, Creds{Password: []byte("x")}, "import"); err != nil || st.EntriesRemoved != 0 {
		t.Fatalf("merging an older export removed the restored entry: %+v %v", st, err)
	}
	if _, err := a.Get(id); err != nil {
		t.Fatalf("restored entry is gone: %v", err)
	}
}

func TestDeleteGroupNotEmptyHasItsOwnError(t *testing.T) {
	v := newTestVault(t)
	gid, _ := v.AddGroup("", "G")
	_, _ = v.AddEntry(Entry{Title: "x", GroupID: gid})
	if err := v.DeleteGroup(gid); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("DeleteGroup = %v", err)
	}
}

// Review finding 1: moving a group into a LATER sibling lost the group and its entries.
func TestMergeMoveGroupIntoALaterSiblingLosesNothing(t *testing.T) {
	for _, nested := range []bool{false, true} {
		a, clock := baseVault(t)
		ga, _ := a.AddGroup("", "A")
		gb, _ := a.AddGroup("", "B")
		gc, _ := a.AddGroup("", "C")
		target := gb
		if nested {
			gd, _ := a.AddGroup(gc, "D") // a later sibling's child
			target = gd
		}
		e1, _ := a.AddEntry(Entry{Title: "in A (shared)", GroupID: ga, Password: "p"})
		b := fork(t, a, clock)
		// b drags folder A into the later sibling; a keeps working in A meanwhile
		bGroup, _, _ := b.findGroup(ga)
		moveGroupForTest(t, b, ga, target, clock)
		_ = bGroup
		if gs, _ := b.Groups(); true {
			for _, g := range gs {
				if g.ID == ga && g.ParentID != target {
					t.Fatalf("test helper did not move the group: parent %s want %s", g.ParentID, target)
				}
			}
		}
		e2, _ := a.AddEntry(Entry{Title: "added later on a", GroupID: ga, Password: "p"})
		merge(t, a, b)
		merge(t, b, a)
		for name, v := range map[string]*Vault{"a": a, "b": b} {
			for _, id := range []string{e1, e2} {
				if _, err := v.Get(id); err != nil {
					t.Fatalf("nested=%v %s lost an entry: %v", nested, name, err)
				}
			}
			gs, _ := v.Groups()
			found := false
			for _, g := range gs {
				if g.ID == ga {
					found = true
					if g.ParentID != target {
						t.Errorf("nested=%v %s: group A is under %q, want %q", nested, name, g.ParentID, target)
					}
					if g.Entries != 2 {
						t.Errorf("nested=%v %s: group A holds %d entries, want 2", nested, name, g.Entries)
					}
				}
			}
			if !found {
				t.Fatalf("nested=%v %s: group A vanished", nested, name)
			}
		}
		if state(a) != state(b) {
			t.Fatalf("nested=%v: replicas differ", nested)
		}
	}
}

// moveGroupForTest does what KeePassXC does when a folder is dragged: the group changes parent and
// its location time moves.
func moveGroupForTest(t *testing.T, v *Vault, id, newParent string, clock *fakeClock) {
	t.Helper()
	v.mu.Lock()
	defer v.mu.Unlock()
	gr, par, _ := v.findGroup(id)
	np, _, _ := v.findGroup(newParent)
	if gr == nil || par == nil || np == nil {
		t.Fatal("groups not found")
	}
	moved := *gr
	now := v.nowWrap()
	moved.Times.LocationChanged = &now
	for i := range par.Groups {
		if par.Groups[i].UUID == gr.UUID {
			par.Groups = append(par.Groups[:i], par.Groups[i+1:]...)
			break
		}
	}
	np, _, _ = v.findGroup(newParent) // pointers moved with the slice
	np.Groups = append(np.Groups, moved)
	if err := v.commit("save"); err != nil {
		t.Fatal(err)
	}
}

// Review finding 3: big merges must be cheap, and a refused merge must leave the vault untouched.
func TestMergeOfThousandsOfEntriesIsFastAndTheLimitIsCheckedFirst(t *testing.T) {
	a, clock := baseVault(t)
	b := fork(t, a, clock)
	b.mu.Lock()
	for i := 0; i < 6000; i++ {
		le := newEntry()
		le.Values = nil
		entrySet(&le, fTitle, fmt.Sprintf("bulk %d", i), false)
		entrySet(&le, fPassword, "p", true)
		b.root().Entries = append(b.root().Entries, le)
	}
	b.mu.Unlock()
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	st := merge(t, a, b)
	if st.EntriesAdded != 6000 {
		t.Fatalf("stats = %+v", st)
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Fatalf("merging 6000 entries took %s (it used to be quadratic)", d)
	}
	// 6000 existing + the file with MaxEntries more must be refused BEFORE anything changes
	c, _ := baseVault(t)
	c.mu.Lock()
	for i := 0; i < MaxEntries-10; i++ {
		le := newEntry()
		entrySet(&le, fTitle, "c", false)
		c.root().Entries = append(c.root().Entries, le)
	}
	c.mu.Unlock()
	data, _ := os.ReadFile(b.path())
	before := c.countEntries()
	if _, err := c.MergeBytes(data, b.creds, "import"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the entry limit: %v", err)
	}
	if after := c.countEntries(); after != before {
		t.Fatalf("a refused merge changed the vault: %d -> %d entries", before, after)
	}
}

// Review findings 3/11/13: hostile shapes and names are refused or neutralised when a file is read.
func TestHostileShapesAreRefusedAndNamesNeutralised(t *testing.T) {
	deep := newTestVault(t)
	deep.mu.Lock()
	cur := deep.root()
	for i := 0; i < maxGroupDepth+3; i++ {
		cur.Groups = append(cur.Groups, deep.newGroupNamed(fmt.Sprint("d", i)))
		cur = &cur.Groups[0]
	}
	deep.mu.Unlock()
	if err := deep.Save(); err == nil {
		t.Fatal("a vault nested deeper than the limit was saved (it must not read back)")
	}

	v := newTestVault(t)
	id, _ := v.AddEntry(Entry{Title: "with file"})
	if err := v.AddAttachment(id, "ok.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}
	v.mu.Lock()
	le, _, _, _ := v.findEntry(id)
	le.Binaries[0].Name = `..\..\AppData\Roaming\Startup\evil.bat`
	v.mu.Unlock()
	if err := v.Save(); err != nil {
		t.Fatal(err)
	}
	w, err := Open(v.Dir(), creds("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	e, _ := w.Get(id)
	if len(e.Attachments) != 1 || strings.ContainsAny(e.Attachments[0].Name, `/\:`) || strings.HasPrefix(e.Attachments[0].Name, ".") {
		t.Fatalf("attachment name from a file is not neutralised: %q", e.Attachments[0].Name)
	}
}

func TestCSVDeepGroupPathsAreCapped(t *testing.T) {
	v := newTestVault(t)
	path := strings.Repeat("g/", 3000) + "leaf"
	start := time.Now()
	added, _, err := v.ImportCSV(strings.NewReader("Group,Title,Password\n" + path + ",deep,p\n"))
	if err != nil || added != 1 {
		t.Fatalf("import = %d %v", added, err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("took %s", time.Since(start))
	}
	if err := v.Save(); err != nil {
		t.Fatalf("the vault can no longer be saved after a deep CSV: %v", err)
	}
	gs, _ := v.Groups()
	for _, g := range gs {
		if strings.Count(g.Path, " / ") >= maxGroupDepth {
			t.Fatalf("group deeper than the cap: %s", g.Path)
		}
	}
}

func TestCSVExportDefusesFormulasButNotPasswords(t *testing.T) {
	v := newTestVault(t)
	_, _ = v.AddEntry(Entry{Title: "=HYPERLINK(\"http://evil\",\"x\")", Username: "+cmd|' /C calc'!A0", Password: "=keep-this-password", Notes: "@SUM(1)"})
	var buf bytes.Buffer
	_ = v.ExportCSV(&buf)
	out := buf.String()
	if strings.Contains(out, ",=HYPERLINK") || strings.Contains(out, ",+cmd") || strings.Contains(out, ",@SUM") {
		t.Fatalf("a spreadsheet would run this:\n%s", out)
	}
	if !strings.Contains(out, ",=keep-this-password,") {
		t.Fatalf("the password must be exported as it is:\n%s", out)
	}
	w := newTestVault(t)
	if _, _, err := w.ImportCSV(&buf); err != nil {
		t.Fatal(err)
	}
	l, _ := w.List(Filter{})
	e, _ := w.Get(l[0].ID)
	if e.Title != "=HYPERLINK(\"http://evil\",\"x\")" || e.Password != "=keep-this-password" {
		t.Fatalf("round trip: %+v", e)
	}
}

func TestExportDoesNotFollowASymlink(t *testing.T) {
	v := newTestVault(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "victim")
	link := filepath.Join(dir, "export.kdbx")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("no symlinks here")
	}
	if err := v.ExportFile(link, Creds{Password: []byte("x")}, KDFTest); err == nil {
		t.Fatal("the export went through a symlink")
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("the symlink target was created")
	}
}

func TestCheckPeerKDFRejectsExpensiveHeadersBeforeAnyDerivation(t *testing.T) {
	v := newTestVault(t)
	data, _ := os.ReadFile(v.path())
	if err := CheckPeerKDF(data); err != nil {
		t.Fatalf("a normal file was refused: %v", err)
	}
	big, err := Create(filepath.Join(t.TempDir(), "big"), creds("x"), KDF{MemoryMiB: 200, Iterations: 2, Parallelism: 2}, "Big")
	if err != nil {
		t.Fatal(err)
	}
	defer big.Close()
	bigData, _ := os.ReadFile(big.path())
	if err := CheckPeerKDF(bigData); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("a file asking for 200 MiB from a peer was accepted: %v", err)
	}
	if err := CheckPeerKDF([]byte("garbage")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestWeakKeyDerivationIsRaisedOnOpen(t *testing.T) {
	v := newTestVault(t) // created with KDFTest: 1 MiB, 1 iteration
	dir := v.Dir()
	v.Close()
	kdfFloorOn = true
	defer func() { kdfFloorOn = false }()
	w, err := Open(dir, creds("correct horse battery staple"))
	if err != nil {
		t.Fatal(err)
	}
	k, _ := w.KDFInfo()
	if k.MemoryMiB < 16 || k.Iterations < 2 {
		t.Fatalf("the weak parameters were kept: %+v", k)
	}
	w.Close()
	data, _ := os.ReadFile(filepath.Join(dir, FileName))
	if c, ok := peekKDF(data); !ok || !c.argon2 || c.memoryBytes < 16<<20 {
		t.Fatalf("the file on disk still has weak parameters: %+v", c)
	}
	if _, err := Open(dir, creds("correct horse battery staple")); err != nil {
		t.Fatal(err)
	}
}

func TestBackupsTheUserAskedForSurviveRoutineChurn(t *testing.T) {
	v := newTestVault(t)
	clock := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	v.now = func() time.Time { return clock }
	_, _ = v.AddEntry(Entry{Title: "one", Password: "p"})
	manual, err := v.BackupNow()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ { // many routine saves, each far enough apart to snapshot
		clock = clock.Add(11 * time.Second)
		_, _ = v.AddEntry(Entry{Title: fmt.Sprint("n", i), Password: "p"})
	}
	found := false
	for _, b := range ListBackups(v.Dir()) {
		if b.Name == manual.Name {
			found = true
		}
	}
	if !found {
		t.Fatal("the manual backup was pushed out by routine snapshots")
	}
	// sync snapshots are rate limited
	before := len(ListBackups(v.Dir()))
	b, _ := os.ReadFile(v.path())
	for i := 0; i < 5; i++ {
		clock = clock.Add(2 * time.Second)
		v.mu.Lock()
		v.snapshotLocked(b, "sync")
		v.mu.Unlock()
	}
	if after := len(ListBackups(v.Dir())); after > before+1 {
		t.Fatalf("sync snapshots are not rate limited: %d -> %d", before, after)
	}
	n, err := v.PurgeBackups()
	if err != nil || n == 0 || len(ListBackups(v.Dir())) != 0 {
		t.Fatalf("PurgeBackups = %d %v, left %d", n, err, len(ListBackups(v.Dir())))
	}
}
