package vault

import (
	"os"
	"sort"

	g "github.com/tobischo/gokeepasslib/v3"
)

// MergeBytes merges another KDBX database (a file from KeePassXC, a copy from another device, a
// backup) into this vault. c are the credentials of that file; a KDBX 3.x file is converted in memory.
// The current file is snapshotted first (by the save), so the merge can be undone from the backups.
func (v *Vault) MergeBytes(data []byte, c Creds, reason string) (MergeStats, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return MergeStats{}, err
	}
	src, err := decode(data, c)
	if err != nil {
		return MergeStats{}, err
	}
	st := mergeInto(v.db, src, nil)
	if !st.Changed() {
		return st, nil
	}
	if v.countEntries() > MaxEntries {
		return st, ErrTooLarge
	}
	return st, v.commit(reason)
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

// ExportFile writes ExportKDBX to path (mode 0600, refuses to overwrite).
func (v *Vault) ExportFile(path string, c Creds, kdf KDF) error {
	out, err := v.ExportKDBX(c, kdf)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return ErrExists
	}
	return writeFileSync(path, out)
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
