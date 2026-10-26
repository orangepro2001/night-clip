package vault

import (
	"fmt"
	"os"
	"sort"

	g "github.com/tobischo/gokeepasslib/v3"
)

// MergeBytes merges another KDBX database (a file from KeePassXC, a copy from another device, a
// backup) into this vault. c are the credentials of that file; a KDBX 3.x file is converted in memory.
// The current file is snapshotted first (by the save), so the merge can be undone from the backups.
func (v *Vault) MergeBytes(data []byte, c Creds, reason string) (MergeStats, error) {
	return v.MergeGuarded(data, c, reason, MergeOptions{})
}

// MergeOptions guard a merge that was not asked for by the user in person (device sync).
type MergeOptions struct {
	// PeerFile: the file comes from another device, so its key derivation cost is bounded before it runs.
	PeerFile bool
	// RequireSameVault refuses a file whose top group is not this vault's: two unrelated vaults are
	// merged only when the user says so.
	RequireSameVault bool
	// LimitRemoved / MaxRemoved: refuse a merge that would delete more than MaxRemoved entries
	// (MaxRemoved 0 with LimitRemoved set means "no deletions at all").
	LimitRemoved bool
	MaxRemoved   int
	// LimitUpdated / MaxUpdated: the same for entries that would be overwritten by the other side's version.
	LimitUpdated bool
	MaxUpdated   int
}

// MergeGuarded is MergeBytes with safety checks. c may be empty: the vault's own credentials are used.
// The returned error is ErrDifferentVault or ErrReview when a guard stopped the merge (nothing changed).
//
// The slow part, decoding (the file's own key derivation), runs without the vault lock held, so a
// large or expensive file cannot freeze the vault; the lock is taken only to merge and save.
func (v *Vault) MergeGuarded(data []byte, c Creds, reason string, o MergeOptions) (MergeStats, error) {
	v.mu.Lock()
	if err := v.usable(); err != nil {
		v.mu.Unlock()
		return MergeStats{}, err
	}
	use := c.clone()
	if len(use.Password) == 0 {
		use = v.creds.clone()
	}
	v.mu.Unlock()
	defer use.wipe()

	if o.PeerFile {
		if err := CheckPeerKDF(data); err != nil {
			return MergeStats{}, err
		}
	}
	src, err := decode(data, use)
	if err != nil {
		return MergeStats{}, err
	}

	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return MergeStats{}, err
	}
	if o.RequireSameVault && src.Content.Root.Groups[0].UUID != v.root().UUID {
		return MergeStats{}, ErrDifferentVault
	}
	if n := countNew(v.db, src); v.countEntries()+n > MaxEntries {
		return MergeStats{}, ErrTooLarge // refused before anything is touched
	}
	if o.LimitRemoved && wouldRemove(v.db, src) > o.MaxRemoved {
		return MergeStats{}, ErrReview
	}
	if o.LimitUpdated && wouldUpdate(v.db, src) > o.MaxUpdated {
		return MergeStats{}, ErrReview
	}
	st := mergeInto(v.db, src, nil)
	if !st.Changed() {
		return st, nil
	}
	return st, v.commit(reason)
}

// countNew counts the entries of src that dst does not have.
func countNew(dst, src *g.Database) int {
	mine := map[g.UUID]bool{}
	walk(dst, func(le *libEntry, _ string, _ bool) { mine[le.UUID] = true }, nil)
	n := 0
	walk(src, func(le *libEntry, _ string, _ bool) {
		if !mine[le.UUID] {
			n++
		}
	}, nil)
	return n
}

// wouldUpdate counts the entries that exist on both sides where the other side's version would replace
// ours (newer, different content).
func wouldUpdate(dst, src *g.Database) int {
	type rec struct {
		t int64
		h string
	}
	mine := map[g.UUID]rec{}
	walk(dst, func(le *libEntry, _ string, _ bool) {
		mine[le.UUID] = rec{unix(le.Times.LastModificationTime), contentHash(dst, le)}
	}, nil)
	n := 0
	walk(src, func(le *libEntry, _ string, _ bool) {
		if m, ok := mine[le.UUID]; ok {
			if t := unix(le.Times.LastModificationTime); t >= m.t && contentHash(src, le) != m.h {
				n++
			}
		}
	}, nil)
	return n
}

// wouldRemove counts the entries the other side's deletions would take out of dst (same rule as the merge).
func wouldRemove(dst, src *g.Database) int {
	mine := map[g.UUID]int64{}
	walk(dst, func(le *libEntry, _ string, _ bool) { mine[le.UUID] = touched(&le.Times) }, nil)
	n := 0
	for _, d := range src.Content.Root.DeletedObjects {
		if t, ok := mine[d.UUID]; ok && t <= unix(d.DeletionTime) {
			n++
		}
	}
	return n
}

// ImportFile merges the KDBX file at path.
func (v *Vault) ImportFile(path string, c Creds) (MergeStats, error) {
	data, err := readFileLimited(path)
	if err != nil {
		return MergeStats{}, err
	}
	return v.MergeBytes(data, c, "import")
}

// ExportKDBX returns an encrypted copy of the whole vault under the given credentials and KDF (a zero
// KDF keeps the current one). The copy is independent: new random values, its own master key.
func (v *Vault) ExportKDBX(c Creds, kdf KDF) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return nil, err
	}
	if kdf == (KDF{}) {
		kp := v.db.Header.FileHeaders.KdfParameters
		kdf = KDF{MemoryMiB: kp.Memory >> 20, Iterations: kp.Iterations, Parallelism: kp.Parallelism}
	}
	// deep copy through the file format, then re-key the copy
	tmp, err := encode(v.db)
	if err != nil {
		return nil, err
	}
	cp, err := decode(tmp, v.creds)
	if err != nil {
		return nil, err
	}
	nd, err := newDatabase(c, kdf, v.db.Content.Meta.DatabaseName)
	if err != nil {
		return nil, err
	}
	adoptContent(nd, cp)
	return encode(nd)
}

// ExportFile writes ExportKDBX to path (mode 0600). It never overwrites and never follows a link that
// happens to be at that path: the file is created exclusively.
func (v *Vault) ExportFile(path string, c Creds, kdf KDF) error {
	out, err := v.ExportKDBX(c, kdf)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return ErrExists
		}
		return fmt.Errorf("vault: create export file: %w", err)
	}
	if _, err := f.Write(out); err != nil {
		f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("vault: write export file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(path)
		return err
	}
	return f.Close()
}

// ---- restoring from backups ---------------------------------------------------------------------------

// DiffItem is one entry that differs between a backup and the current vault.
type DiffItem struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	ModifiedAt int64  `json:"modified_at"`
}

// RestoreDiff compares a backup with the current vault (titles only, no secrets).
type RestoreDiff struct {
	OnlyInBackup  []DiffItem `json:"only_in_backup"`  // would come back
	OnlyInCurrent []DiffItem `json:"only_in_current"` // would be lost by a full replace
	Changed       []DiffItem `json:"changed"`         // different content (the backup's modified time is shown)
	Same          int        `json:"same"`
}

func (v *Vault) openBackup(name string, c Creds) (*g.Database, error) {
	p, err := backupPath(v.dir, name)
	if err != nil {
		return nil, err
	}
	data, err := readFileLimited(p)
	if err != nil {
		return nil, err
	}
	if len(c.Password) == 0 {
		c = v.creds
	}
	return decode(data, c)
}

// PreviewRestore shows what restoring a backup would do. c are the credentials of the backup (empty =
// the current ones).
func (v *Vault) PreviewRestore(name string, c Creds) (RestoreDiff, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return RestoreDiff{}, err
	}
	b, err := v.openBackup(name, c)
	if err != nil {
		return RestoreDiff{}, err
	}
	type rec struct {
		title string
		mod   int64
		hash  string
	}
	collect := func(db *g.Database) map[string]rec {
		m := map[string]rec{}
		walk(db, func(le *libEntry, _ string, _ bool) {
			m[uuidHex(le.UUID)] = rec{entryGet(le, fTitle), unix(le.Times.LastModificationTime), contentHash(db, le)}
		}, nil)
		return m
	}
	cur, old := collect(v.db), collect(b)
	var d RestoreDiff
	for id, r := range old {
		c, ok := cur[id]
		switch {
		case !ok:
			d.OnlyInBackup = append(d.OnlyInBackup, DiffItem{id, r.title, r.mod})
		case c.hash != r.hash:
			d.Changed = append(d.Changed, DiffItem{id, r.title, r.mod})
		default:
			d.Same++
		}
	}
	for id, r := range cur {
		if _, ok := old[id]; !ok {
			d.OnlyInCurrent = append(d.OnlyInCurrent, DiffItem{id, r.title, r.mod})
		}
	}
	for _, l := range [][]DiffItem{d.OnlyInBackup, d.OnlyInCurrent, d.Changed} {
		sort.Slice(l, func(i, j int) bool { return l[i].Title < l[j].Title })
	}
	return d, nil
}

// RestoreMode selects what Restore does.
type RestoreMode string

const (
	// RestoreReplace makes the vault's contents equal to the backup's (re-encrypted under the current
	// credentials); entries only in the current vault are lost (they stay in the pre-restore snapshot).
	RestoreReplace RestoreMode = "replace"
	// RestoreEntries brings back the listed entries as they were in the backup. A current version is
	// kept in the entry's history; an entry that no longer exists is recreated.
	RestoreEntries RestoreMode = "entries"
	// RestoreMerge merges the backup in with the usual rules (newer edits win, nothing is deleted).
	RestoreMerge RestoreMode = "merge"
)

// Restore applies a backup. The file as it is now is snapshotted first (reason "restore"), so a
// restore can always be undone.
func (v *Vault) Restore(name string, c Creds, mode RestoreMode, ids []string) (MergeStats, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return MergeStats{}, err
	}
	b, err := v.openBackup(name, c)
	if err != nil {
		return MergeStats{}, err
	}
	// Make sure a snapshot of the present state exists even if the last routine one was moments ago.
	if cur, err := os.ReadFile(v.path()); err == nil {
		v.snapshotLocked(cur, "restore")
	}
	var st MergeStats
	switch mode {
	case RestoreReplace:
		kp := v.db.Header.FileHeaders.KdfParameters
		nd, err := newDatabase(v.creds, KDF{MemoryMiB: kp.Memory >> 20, Iterations: kp.Iterations, Parallelism: kp.Parallelism}, b.Content.Meta.DatabaseName)
		if err != nil {
			return st, err
		}
		adoptContent(nd, b)
		st.EntriesUpdated = 1
		v.db = nd
	case RestoreMerge:
		st = mergeInto(v.db, b, nil)
	case RestoreEntries:
		if len(ids) == 0 {
			return st, invalid("name the entries to restore")
		}
		only := map[string]bool{}
		for _, id := range ids {
			if _, ok := parseUUID(id); !ok {
				return st, invalid("bad entry id")
			}
			only[id] = true
		}
		st = v.restoreEntries(b, only)
	default:
		return st, invalid("unknown restore mode")
	}
	return st, v.commit("restore")
}

func (v *Vault) dropTombstone(u g.UUID) {
	out := v.db.Content.Root.DeletedObjects[:0]
	for _, d := range v.db.Content.Root.DeletedObjects {
		if d.UUID != u {
			out = append(out, d)
		}
	}
	v.db.Content.Root.DeletedObjects = out
}

// restoreEntries forces the backup's version of the given entries to become current.
func (v *Vault) restoreEntries(b *g.Database, only map[string]bool) MergeStats {
	var st MergeStats
	m := &merger{dst: v.db, src: b, binMap: map[int]int{}, st: &st}
	m.index()
	walk(b, func(se *libEntry, _ string, _ bool) {
		id := uuidHex(se.UUID)
		if !only[id] {
			return
		}
		ne := se.Clone()
		ne.UUID = se.UUID
		m.remapBinaries(&ne)
		v.dropTombstone(se.UUID) // it exists again: a later merge must not delete it by an old record
		if de, ok := m.dstEntry[se.UUID]; ok {
			v.pushHistory(de)
			ne.Histories = de.Histories
			ne.Times.LocationChanged = de.Times.LocationChanged
			if pg := getCustom(de, cdPrevGroup); pg != "" {
				setCustom(&ne, cdPrevGroup, pg)
			}
			v.touch(&ne)
			*de = ne
			st.EntriesUpdated++
			return
		}
		now := v.nowWrap()
		ne.Times.LastModificationTime, ne.Times.LocationChanged = &now, &now
		delCustom(&ne, cdPrevGroup)
		v.root().Entries = append(v.root().Entries, ne)
		st.EntriesAdded++
		m.index()
	}, nil)
	return st
}
