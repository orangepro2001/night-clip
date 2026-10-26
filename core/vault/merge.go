package vault

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	g "github.com/tobischo/gokeepasslib/v3"
	w "github.com/tobischo/gokeepasslib/v3/wrappers"
)

// MergeStats says what a merge changed in the receiving vault.
type MergeStats struct {
	EntriesAdded   int `json:"entries_added"`
	EntriesUpdated int `json:"entries_updated"`
	EntriesRemoved int `json:"entries_removed"`
	EntriesMoved   int `json:"entries_moved"`
	GroupsAdded    int `json:"groups_added"`
	GroupsRemoved  int `json:"groups_removed"`
}

// Changed reports whether anything happened.
func (s MergeStats) Changed() bool { return s != MergeStats{} }

// The merge follows KeePass's own rules so that a file edited in KeePassXC and in ClipBridge on
// different devices comes together without losing either side (docs/plan-vault.md §5.3):
//
//   - objects are matched by UUID;
//   - the newer LastModificationTime wins the current content, the other version goes into the
//     history, and the histories are united;
//   - the location of an entry or group (which group it is in, the recycle bin included) is decided
//     separately by LocationChanged;
//   - a deletion beats an older edit and loses against a newer one ("keep rather than lose");
//   - ties are broken by the content hash, so merge(A,B) and merge(B,A) agree.
//
// Both databases must be unlocked (plain values). src is only read, but its entries are copied by value
// into dst, so it should be discarded afterwards.
func mergeInto(dst, src *g.Database, only map[string]bool) MergeStats {
	var st MergeStats
	m := &merger{dst: dst, src: src, only: only, st: &st, binMap: map[int]int{}}
	m.run()
	return st
}

type merger struct {
	dst, src *g.Database
	only     map[string]bool // restrict to these entry ids (selective restore); nil = everything
	st       *MergeStats
	binMap   map[int]int

	groupMap  map[g.UUID]g.UUID // src group -> dst group (the roots and the recycle bins are paired)
	dstGroups map[g.UUID]*g.Group
	dstEntry  map[g.UUID]*g.Entry
	dstParent map[g.UUID]g.UUID // entry or group -> containing group
	deletedAt map[g.UUID]int64  // union of both DeletedObjects
}

func (m *merger) run() {
	m.deletedAt = map[g.UUID]int64{}
	for _, d := range m.dst.Content.Root.DeletedObjects {
		m.noteDeleted(d.UUID, unix(d.DeletionTime))
	}
	for _, d := range m.src.Content.Root.DeletedObjects {
		m.noteDeleted(d.UUID, unix(d.DeletionTime))
	}
	m.index()
	m.groupMap = map[g.UUID]g.UUID{}
	srcRoot, dstRoot := &m.src.Content.Root.Groups[0], &m.dst.Content.Root.Groups[0]
	m.groupMap[srcRoot.UUID] = dstRoot.UUID
	if m.only == nil {
		m.mergeGroups(srcRoot, dstRoot.UUID)
	} else {
		m.pairGroupsOnly(srcRoot)
	}
	srcBin := m.src.Content.Meta.RecycleBinUUID
	if !srcBin.IsZero() && m.src.Content.Meta.RecycleBinEnabled.Bool {
		m.groupMap[srcBin] = m.dstBin(true)
		m.index()
	}
	m.mergeEntries(srcRoot)
	if m.only == nil {
		m.applyDeletions()
		m.unionDeleted()
	}
}

func (m *merger) noteDeleted(u g.UUID, t int64) {
	if t >= m.deletedAt[u] {
		m.deletedAt[u] = t
	}
}

func (m *merger) index() {
	m.dstGroups = map[g.UUID]*g.Group{}
	m.dstEntry = map[g.UUID]*g.Entry{}
	m.dstParent = map[g.UUID]g.UUID{}
	var rec func(gr *g.Group, parent g.UUID)
	rec = func(gr *g.Group, parent g.UUID) {
		m.dstGroups[gr.UUID] = gr
		m.dstParent[gr.UUID] = parent
		for i := range gr.Entries {
			m.dstEntry[gr.Entries[i].UUID] = &gr.Entries[i]
			m.dstParent[gr.Entries[i].UUID] = gr.UUID
		}
		for i := range gr.Groups {
			rec(&gr.Groups[i], gr.UUID)
		}
	}
	rec(&m.dst.Content.Root.Groups[0], g.UUID{})
}

// dstBin returns the receiving vault's recycle bin id, creating the group if asked.
func (m *merger) dstBin(create bool) g.UUID {
	meta := m.dst.Content.Meta
	if !meta.RecycleBinUUID.IsZero() {
		if _, ok := m.dstGroups[meta.RecycleBinUUID]; ok {
			return meta.RecycleBinUUID
		}
	}
	if !create {
		return g.UUID{}
	}
	bin := g.NewGroup()
	bin.UUID = binUUIDFor(m.dst.Content.Root.Groups[0].UUID)
	bin.Name = "Recycle Bin"
	bin.IconID = 43
	bin.EnableSearching = w.NewNullableBoolWrapper(false)
	root := &m.dst.Content.Root.Groups[0]
	root.Groups = append(root.Groups, bin)
	meta.RecycleBinEnabled = w.NewBoolWrapper(true)
	meta.RecycleBinUUID = bin.UUID
	m.index()
	return bin.UUID
}

// mergeGroups walks the source tree top-down and creates or updates groups in dst.
func (m *merger) mergeGroups(sg *g.Group, dstParent g.UUID) {
	for i := range sg.Groups {
		child := &sg.Groups[i]
		if child.UUID == m.src.Content.Meta.RecycleBinUUID && m.src.Content.Meta.RecycleBinEnabled.Bool {
			continue // paired with our own bin later
		}
		if existing, ok := m.dstGroups[child.UUID]; ok {
			m.groupMap[child.UUID] = existing.UUID
			if unix(child.Times.LastModificationTime) > unix(existing.Times.LastModificationTime) {
				existing.Name, existing.Notes = child.Name, child.Notes
				existing.Times.LastModificationTime = child.Times.LastModificationTime
			}
			if m.locationWins(child.Times.LocationChanged, existing.Times.LocationChanged, dstParent, m.dstParent[existing.UUID]) && !m.isAncestor(existing.UUID, dstParent) {
				m.moveGroup(existing.UUID, dstParent, child.Times.LocationChanged)
			} else if dstParent == m.dstParent[existing.UUID] && unix(child.Times.LocationChanged) > unix(existing.Times.LocationChanged) {
				existing.Times.LocationChanged = child.Times.LocationChanged
			}
		} else if t, gone := m.deletedAt[child.UUID]; gone && t >= touched(&child.Times) {
			continue
		} else {
			ng := g.NewGroup()
			ng.UUID, ng.Name, ng.Notes, ng.IconID, ng.Times = child.UUID, child.Name, child.Notes, child.IconID, child.Times
			par := m.dstGroups[dstParent]
			par.Groups = append(par.Groups, ng)
			m.st.GroupsAdded++
			m.index()
			m.groupMap[child.UUID] = child.UUID
		}
		m.mergeGroups(child, m.groupMap[child.UUID])
	}
}

// pairGroupsOnly maps source groups to existing destination groups without creating any (selective
// restore: entries whose group is gone land in the root).
func (m *merger) pairGroupsOnly(sg *g.Group) {
	for i := range sg.Groups {
		child := &sg.Groups[i]
		if _, ok := m.dstGroups[child.UUID]; ok {
			m.groupMap[child.UUID] = child.UUID
		}
		m.pairGroupsOnly(child)
	}
}

// locationWins decides whether the other side's placement (theirs, in group theirGroup) replaces ours:
// the later move wins, an exact tie goes to the group with the smaller id so both sides agree.
func (m *merger) locationWins(theirs, ours *w.TimeWrapper, theirGroup, ourGroup g.UUID) bool {
	if theirGroup == ourGroup {
		return false
	}
	t, o := unix(theirs), unix(ours)
	return t > o || t == o && uuidHex(theirGroup) < uuidHex(ourGroup)
}

func (m *merger) isAncestor(anc, of g.UUID) bool {
	for cur := of; !cur.IsZero(); cur = m.dstParent[cur] {
		if cur == anc {
			return true
		}
	}
	return false
}

func (m *merger) moveGroup(id, newParent g.UUID, at *w.TimeWrapper) {
	gr := m.dstGroups[id]
	old := m.dstGroups[m.dstParent[id]]
	if gr == nil || old == nil || m.dstGroups[newParent] == nil {
		return
	}
	moved := *gr
	moved.Times.LocationChanged = at
	for i := range old.Groups {
		if old.Groups[i].UUID == id {
			old.Groups = append(old.Groups[:i], old.Groups[i+1:]...)
			break
		}
	}
	np := m.dstGroups[newParent]
	np.Groups = append(np.Groups, moved)
	m.index()
}

func (m *merger) walkSrc(sg *g.Group, fn func(e *g.Entry, groupID g.UUID)) {
	for i := range sg.Entries {
		fn(&sg.Entries[i], sg.UUID)
	}
	for i := range sg.Groups {
		m.walkSrc(&sg.Groups[i], fn)
	}
}

func (m *merger) mergeEntries(srcRoot *g.Group) {
	m.walkSrc(srcRoot, func(se *g.Entry, sgid g.UUID) {
		if m.only != nil && !m.only[uuidHex(se.UUID)] {
			return
		}
		target, ok := m.groupMap[sgid]
		if !ok {
			target = m.dst.Content.Root.Groups[0].UUID
		}
		de, exists := m.dstEntry[se.UUID]
		if !exists {
			if t, gone := m.deletedAt[se.UUID]; gone && t >= touched(&se.Times) && m.only == nil {
				return // deleted on our side after this version was written
			}
			m.addEntry(se, target)
			return
		}
		m.mergeEntry(de, se)
		if m.only == nil {
			if m.locationWins(se.Times.LocationChanged, de.Times.LocationChanged, target, m.dstParent[de.UUID]) {
				m.moveEntry(de.UUID, target, se.Times.LocationChanged)
			} else if target == m.dstParent[de.UUID] && unix(se.Times.LocationChanged) > unix(de.Times.LocationChanged) {
				// the same place, decided by a later move: remember when, or an older move elsewhere
				// would look newer than it is when it arrives later
				de.Times.LocationChanged = se.Times.LocationChanged
			}
		}
	})
}

func (m *merger) addEntry(se *g.Entry, target g.UUID) {
	ne := se.Clone()
	ne.UUID = se.UUID
	m.remapBinaries(&ne)
	par := m.dstGroups[target]
	if par == nil {
		par = &m.dst.Content.Root.Groups[0]
	}
	par.Entries = append(par.Entries, ne)
	m.st.EntriesAdded++
	m.index()
}

func (m *merger) moveEntry(id, target g.UUID, at *w.TimeWrapper) {
	e := m.dstEntry[id]
	from := m.dstGroups[m.dstParent[id]]
	to := m.dstGroups[target]
	if e == nil || from == nil || to == nil {
		return
	}
	moved := *e
	moved.Times.LocationChanged = at
	for i := range from.Entries {
		if from.Entries[i].UUID == id {
			from.Entries = append(from.Entries[:i], from.Entries[i+1:]...)
			break
		}
	}
	to.Entries = append(to.Entries, moved)
	m.st.EntriesMoved++
	m.index()
}

// remapBinaries copies the attachments an entry (and its history) references from src into dst.
func (m *merger) remapBinaries(e *g.Entry) {
	fix := func(es *g.Entry) {
		refs := es.Binaries[:0]
		for _, r := range es.Binaries {
			n, ok := m.binMap[r.Value.ID]
			if !ok {
				b := m.src.FindBinary(r.Value.ID)
				if b == nil {
					continue
				}
				c, err := b.GetContentBytes()
				if err != nil || len(c) > MaxAttachmentBytes {
					continue
				}
				n = m.dst.AddBinary(c).ID
				m.binMap[r.Value.ID] = n
			}
			r.Value.ID = n
			refs = append(refs, r)
		}
		es.Binaries = refs
	}
	fix(e)
	for h := range e.Histories {
		for i := range e.Histories[h].Entries {
			fix(&e.Histories[h].Entries[i])
		}
	}
}

// contentHash identifies what an entry says (not when). Attachments count by name and content.
func contentHash(db *g.Database, e *g.Entry) string {
	h := sha256.New()
	vals := make([]string, 0, len(e.Values))
	for _, v := range e.Values {
		vals = append(vals, v.Key+"\x00"+v.Value.Content+"\x00"+strconv.FormatBool(v.Value.Protected.Bool))
	}
	sort.Strings(vals)
	h.Write([]byte(strings.Join(vals, "\x01")))
	h.Write([]byte("\x02" + e.Tags + "\x02" + strconv.FormatBool(e.Times.Expires.Bool) + strconv.FormatInt(unix(e.Times.ExpiryTime), 10)))
	cds := make([]string, 0)
	for _, cd := range e.CustomData {
		if cd.Key == cdPrevGroup {
			continue
		}
		cds = append(cds, cd.Key+"\x00"+cd.Value)
	}
	sort.Strings(cds)
	h.Write([]byte("\x03" + strings.Join(cds, "\x01")))
	var bins []string
	for _, br := range e.Binaries {
		d := ""
		if b := db.FindBinary(br.Value.ID); b != nil {
			if c, err := b.GetContentBytes(); err == nil {
				sum := sha256.Sum256(c)
				d = hex.EncodeToString(sum[:])
			}
		}
		bins = append(bins, br.Name+"\x00"+d)
	}
	sort.Strings(bins)
	h.Write([]byte("\x04" + strings.Join(bins, "\x01")))
	return hex.EncodeToString(h.Sum(nil))
}

// mergeEntry reconciles two versions of the same entry.
func (m *merger) mergeEntry(de, se *g.Entry) {
	dt, st := unix(de.Times.LastModificationTime), unix(se.Times.LastModificationTime)
	dh, sh := contentHash(m.dst, de), contentHash(m.src, se)
	changed := false

	srcWins := st > dt || st == dt && dh != sh && sh > dh
	if dh == sh {
		// the same content on both sides: nothing to preserve, only the later modification time
		// and the histories matter (the same on both sides of a merge, whichever way it runs)
		if st > dt {
			de.Times.LastModificationTime = se.Times.LastModificationTime
			changed = true
		}
	} else if srcWins {
		m.addHistory(de, m.snapshotOf(m.dst, de), true)
		keepLoc, keepHist, keepUUID := de.Times.LocationChanged, de.Histories, de.UUID
		prevGroup := getCustom(de, cdPrevGroup)
		ne := se.Clone()
		ne.UUID = keepUUID
		m.remapBinaries(&ne)
		ne.Times.LocationChanged = keepLoc
		ne.Histories = keepHist
		// the trash bookkeeping belongs to the location, which is decided separately
		delCustom(&ne, cdPrevGroup)
		if prevGroup != "" {
			setCustom(&ne, cdPrevGroup, prevGroup)
		}
		*de = ne
		changed = true
	} else {
		// our version stays; remember theirs, it is a different state
		s := m.snapshotOf(m.src, se)
		m.remapBinaries(&s)
		if m.addHistory(de, s, false) {
			changed = true
		}
	}
	// the other side's history is united with ours
	for _, h := range se.Histories {
		for i := range h.Entries {
			s := h.Entries[i].Clone()
			s.UUID = de.UUID
			m.remapBinaries(&s)
			if m.addHistory(de, s, false) {
				changed = true
			}
		}
	}
	if changed {
		m.st.EntriesUpdated++
	}
}

func (m *merger) snapshotOf(db *g.Database, e *g.Entry) g.Entry {
	s := e.Clone()
	s.UUID = e.UUID
	s.Histories = nil
	return s
}

// addHistory appends s to e's history unless an equal state (same time and content) is there,
// keeps the history ordered by time and bounded; it reports whether anything was added.
//
// force skips the "is this the current state" check: the caller is about to replace the current state
// and wants it kept.
func (m *merger) addHistory(e *g.Entry, s g.Entry, force bool) bool {
	st, sh := unix(s.Times.LastModificationTime), contentHash(m.dst, &s)
	if !force && unix(e.Times.LastModificationTime) == st && contentHash(m.dst, e) == sh {
		return false // it is the current state
	}
	if len(e.Histories) == 0 {
		e.Histories = []g.History{{}}
	}
	h := &e.Histories[0]
	for i := range h.Entries {
		if unix(h.Entries[i].Times.LastModificationTime) == st && contentHash(m.dst, &h.Entries[i]) == sh {
			return false
		}
	}
	s.Histories = nil
	h.Entries = append(h.Entries, s)
	sort.SliceStable(h.Entries, func(i, j int) bool {
		return unix(h.Entries[i].Times.LastModificationTime) < unix(h.Entries[j].Times.LastModificationTime)
	})
	max := int(m.dst.Content.Meta.HistoryMaxItems)
	if max <= 0 {
		max = 10
	}
	if len(h.Entries) > max {
		h.Entries = h.Entries[len(h.Entries)-max:]
	}
	return true
}

// applyDeletions removes what the other side deleted, unless we changed it afterwards.
func (m *merger) applyDeletions() {
	for _, d := range m.src.Content.Root.DeletedObjects {
		t := unix(d.DeletionTime)
		if e, ok := m.dstEntry[d.UUID]; ok && touched(&e.Times) <= t {
			if gr := m.dstGroups[m.dstParent[d.UUID]]; gr != nil {
				for i := range gr.Entries {
					if gr.Entries[i].UUID == d.UUID {
						gr.Entries = append(gr.Entries[:i], gr.Entries[i+1:]...)
						m.st.EntriesRemoved++
						break
					}
				}
				m.index()
			}
		}
	}
	// empty groups the other side deleted (deepest first, so nested ones go together)
	for pass := 0; pass < 8; pass++ {
		removed := false
		for _, d := range m.src.Content.Root.DeletedObjects {
			gr, ok := m.dstGroups[d.UUID]
			if !ok || len(gr.Entries) > 0 || len(gr.Groups) > 0 || d.UUID == m.dst.Content.Root.Groups[0].UUID || d.UUID == m.dst.Content.Meta.RecycleBinUUID {
				continue
			}
			if touched(&gr.Times) > unix(d.DeletionTime) {
				continue
			}
			if par := m.dstGroups[m.dstParent[d.UUID]]; par != nil {
				for i := range par.Groups {
					if par.Groups[i].UUID == d.UUID {
						par.Groups = append(par.Groups[:i], par.Groups[i+1:]...)
						m.st.GroupsRemoved++
						removed = true
						break
					}
				}
				m.index()
			}
		}
		if !removed {
			break
		}
	}
}

// unionDeleted rewrites dst's DeletedObjects as the union of both sides, minus anything that exists again.
func (m *merger) unionDeleted() {
	out := make([]g.DeletedObjectData, 0, len(m.deletedAt))
	for u, t := range m.deletedAt {
		if e, ok := m.dstEntry[u]; ok && touched(&e.Times) > t {
			continue
		}
		if _, ok := m.dstGroups[u]; ok {
			continue
		}
		out = append(out, g.DeletedObjectData{UUID: u, DeletionTime: timeWrap(t)})
	}
	sort.Slice(out, func(i, j int) bool { return uuidHex(out[i].UUID) < uuidHex(out[j].UUID) })
	m.dst.Content.Root.DeletedObjects = out
}

// touched is the latest time anything happened to an object: an edit or a move. A deletion only wins
// over objects that were left alone since.
func touched(t *g.TimeData) int64 {
	if a, b := unix(t.LastModificationTime), unix(t.LocationChanged); b > a {
		return b
	}
	return unix(t.LastModificationTime)
}
