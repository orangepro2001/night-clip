package vault

import (
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
