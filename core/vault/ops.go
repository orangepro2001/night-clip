package vault

import (
	"sort"
	"strings"

	g "github.com/tobischo/gokeepasslib/v3"
	w "github.com/tobischo/gokeepasslib/v3/wrappers"
)

type (
	libEntry = g.Entry
	libGroup = g.Group
)

// walk visits every group and entry; trash is true inside the recycle bin.
func walk(db *g.Database, onEntry func(e *libEntry, groupID string, trash bool), onGroup func(gr *libGroup, parentID string, trash bool)) {
	bin := binID(db)
	var rec func(gr *g.Group, parent string, trash bool)
	rec = func(gr *g.Group, parent string, trash bool) {
		id := uuidHex(gr.UUID)
		if bin != "" && id == bin {
			trash = true
		}
		if onGroup != nil {
			onGroup(gr, parent, trash)
		}
		for i := range gr.Entries {
			if onEntry != nil {
				onEntry(&gr.Entries[i], id, trash)
			}
		}
		for i := range gr.Groups {
			rec(&gr.Groups[i], id, trash)
		}
	}
	for i := range db.Content.Root.Groups {
		rec(&db.Content.Root.Groups[i], "", false)
	}
}

func binID(db *g.Database) string {
	m := db.Content.Meta
	if m == nil || !m.RecycleBinEnabled.Bool || m.RecycleBinUUID.IsZero() {
		return ""
	}
	return uuidHex(m.RecycleBinUUID)
}

func (v *Vault) root() *g.Group { return &v.db.Content.Root.Groups[0] }

// findGroup returns the group with the id and its parent (nil for the root).
func (v *Vault) findGroup(id string) (gr, parent *g.Group, inTrash bool) {
	bin := binID(v.db)
	var rec func(cur, par *g.Group, trash bool) bool
	rec = func(cur, par *g.Group, trash bool) bool {
		cid := uuidHex(cur.UUID)
		if bin != "" && cid == bin {
			trash = true
		}
		if cid == id {
			gr, parent, inTrash = cur, par, trash
			return true
		}
		for i := range cur.Groups {
			if rec(&cur.Groups[i], cur, trash) {
				return true
			}
		}
		return false
	}
	for i := range v.db.Content.Root.Groups {
		if rec(&v.db.Content.Root.Groups[i], nil, false) {
			return
		}
	}
	return nil, nil, false
}

// findEntry returns the entry, its group and its index there.
func (v *Vault) findEntry(id string) (e *g.Entry, gr *g.Group, idx int, inTrash bool) {
	if _, ok := parseUUID(id); !ok {
		return nil, nil, 0, false
	}
	bin := binID(v.db)
	var rec func(cur *g.Group, trash bool) bool
	rec = func(cur *g.Group, trash bool) bool {
		if bin != "" && uuidHex(cur.UUID) == bin {
			trash = true
		}
		for i := range cur.Entries {
			if uuidHex(cur.Entries[i].UUID) == id {
				e, gr, idx, inTrash = &cur.Entries[i], cur, i, trash
				return true
			}
		}
		for i := range cur.Groups {
			if rec(&cur.Groups[i], trash) {
				return true
			}
		}
		return false
	}
	for i := range v.db.Content.Root.Groups {
		if rec(&v.db.Content.Root.Groups[i], false) {
			return
		}
	}
	return nil, nil, 0, false
}

func (v *Vault) countEntries() int {
	n := 0
	walk(v.db, func(*libEntry, string, bool) { n++ }, nil)
	return n
}

// trashGroup returns the recycle bin, creating it when create is set.
func (v *Vault) trashGroup(create bool) *g.Group {
	if id := binID(v.db); id != "" {
		if gr, _, _ := v.findGroup(id); gr != nil {
			return gr
		}
	}
	if !create {
		return nil
	}
	bin := v.newGroupNamed("Recycle Bin")
	bin.UUID = binUUIDFor(v.root().UUID)
	bin.IconID = 43
	bin.EnableSearching = w.NewNullableBoolWrapper(false)
	root := v.root()
	root.Groups = append(root.Groups, bin)
	m := v.db.Content.Meta
	m.RecycleBinEnabled = w.NewBoolWrapper(true)
	m.RecycleBinUUID = bin.UUID
	now := v.nowWrap()
	m.RecycleBinChanged = &now
	return &root.Groups[len(root.Groups)-1]
}

// ---- entries -------------------------------------------------------------------------------

// applyEntry writes the editable fields of e into le.
func (v *Vault) applyEntry(le *g.Entry, e Entry) {
	entrySet(le, fTitle, e.Title, false)
	entrySet(le, fUser, e.Username, false)
	entrySet(le, fPassword, e.Password, true)
	entrySet(le, fURL, e.URL, false)
	entrySet(le, fNotes, e.Notes, false)
	if e.TOTP != "" {
		entrySet(le, fOTP, e.TOTP, true)
	} else {
		entryDel(le, fOTP)
	}
	// replace the custom fields as a set
	out := le.Values[:0]
	for _, val := range le.Values {
		switch val.Key {
		case fTitle, fUser, fPassword, fURL, fNotes, fOTP:
			out = append(out, val)
		}
	}
	le.Values = out
	for _, f := range e.Fields {
		entrySet(le, strings.TrimSpace(f.Name), f.Value, f.Protected)
	}
	le.Tags = joinTags(e.Tags)
	setCustom(le, cdKind, e.Kind)
	if e.ExpiresAt > 0 {
		le.Times.Expires = w.NewBoolWrapper(true)
		le.Times.ExpiryTime = timeWrap(e.ExpiresAt)
	} else {
		le.Times.Expires = w.NewBoolWrapper(false)
	}
}

func setCustom(le *g.Entry, key, value string) {
	for i := range le.CustomData {
		if le.CustomData[i].Key == key {
			le.CustomData[i].Value = value
			return
		}
	}
	le.CustomData = append(le.CustomData, g.CustomData{Key: key, Value: value})
}

func getCustom(le *g.Entry, key string) string {
	for _, cd := range le.CustomData {
		if cd.Key == key {
			return cd.Value
		}
	}
	return ""
}

func delCustom(le *g.Entry, key string) {
	out := le.CustomData[:0]
	for _, cd := range le.CustomData {
		if cd.Key != key {
			out = append(out, cd)
		}
	}
	le.CustomData = out
}

func (v *Vault) touch(le *g.Entry) {
	now := v.nowWrap()
	le.Times.LastModificationTime = &now
}

// AddEntry stores a new entry and returns its id. e.ID, e.CreatedAt and the like are ignored.
func (v *Vault) AddEntry(e Entry) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return "", err
	}
	if err := e.validate(); err != nil {
		return "", err
	}
	if v.countEntries() >= MaxEntries {
		return "", ErrTooLarge
	}
	gr := v.root()
	if e.GroupID != "" {
		var trash bool
		gr, _, trash = v.findGroup(e.GroupID)
		if gr == nil || trash {
			return "", ErrNotFound
		}
	}
	le := g.NewEntry()
	v.applyEntry(&le, e)
	now := v.nowWrap()
	le.Times.CreationTime, le.Times.LastModificationTime, le.Times.LastAccessTime, le.Times.LocationChanged = &now, &now, &now, &now
	gr.Entries = append(gr.Entries, le)
	if err := v.commit("save"); err != nil {
		return uuidHex(le.UUID), err
	}
	return uuidHex(le.UUID), nil
}

// Get returns the full entry including its secrets.
func (v *Vault) Get(id string) (Entry, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return Entry{}, err
	}
	le, gr, _, trash := v.findEntry(id)
	if le == nil {
		return Entry{}, ErrNotFound
	}
	return fromLib(v.db, le, uuidHex(gr.UUID), trash), nil
}

// UpdateEntry replaces the editable fields of an entry (title, username, password, URL, notes,
// tags, custom fields, TOTP, expiry, kind). Attachments, group and history are kept; the previous
// version goes into the history.
func (v *Vault) UpdateEntry(id string, e Entry) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	if err := e.validate(); err != nil {
		return err
	}
	le, _, _, _ := v.findEntry(id)
	if le == nil {
		return ErrNotFound
	}
	before := fromLib(v.db, le, "", false)
	probe := g.NewEntry()
	v.applyEntry(&probe, e)
	after := fromLib(v.db, &probe, "", false)
	before.GroupID, before.CreatedAt, before.ModifiedAt, before.ID, before.Attachments, before.History = "", 0, 0, "", nil, 0
	after.GroupID, after.CreatedAt, after.ModifiedAt, after.ID, after.Attachments, after.History = "", 0, 0, "", nil, 0
	if entryEqual(before, after) {
		return nil // nothing changed: no new history item, no new modification time
	}
	v.pushHistory(le)
	v.applyEntry(le, e)
	v.touch(le)
	return v.commit("save")
}

func entryEqual(a, b Entry) bool {
	if a.Kind != b.Kind || a.Title != b.Title || a.Username != b.Username || a.Password != b.Password || a.URL != b.URL ||
		a.Notes != b.Notes || a.TOTP != b.TOTP || a.ExpiresAt != b.ExpiresAt || joinTags(a.Tags) != joinTags(b.Tags) || len(a.Fields) != len(b.Fields) {
		return false
	}
	m := map[string]Field{}
	for _, f := range a.Fields {
		m[f.Name] = f
	}
	for _, f := range b.Fields {
		if m[f.Name] != f {
			return false
		}
	}
	return true
}

// pushHistory records the current state of le as a history item (keeping the entry's UUID, as KeePass does).
func (v *Vault) pushHistory(le *g.Entry) {
	old := le.Clone()
	old.UUID = le.UUID
	old.Histories = nil
	if len(le.Histories) == 0 {
		le.Histories = []g.History{{}}
	}
	h := &le.Histories[0]
	h.Entries = append(h.Entries, old)
	max := int(v.db.Content.Meta.HistoryMaxItems)
	if max <= 0 {
		max = 10
	}
	if len(h.Entries) > max {
		h.Entries = h.Entries[len(h.Entries)-max:]
	}
}

// History lists the earlier versions of an entry, oldest first (secrets are not included).
func (v *Vault) History(id string) ([]Version, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return nil, err
	}
	le, _, _, _ := v.findEntry(id)
	if le == nil {
		return nil, ErrNotFound
	}
	out := []Version{}
	if len(le.Histories) > 0 {
		for i := range le.Histories[0].Entries {
			h := &le.Histories[0].Entries[i]
			out = append(out, Version{Index: i, ModifiedAt: unix(h.Times.LastModificationTime), Title: entryGet(h, fTitle), Username: entryGet(h, fUser), URL: entryGet(h, fURL)})
		}
	}
	return out, nil
}

// RestoreVersion makes an earlier version current again (the present one goes into the history).
func (v *Vault) RestoreVersion(id string, index int) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	le, _, _, _ := v.findEntry(id)
	if le == nil || len(le.Histories) == 0 || index < 0 || index >= len(le.Histories[0].Entries) {
		return ErrNotFound
	}
	past := le.Histories[0].Entries[index].Clone()
	v.pushHistory(le)
	le.Values = past.Values
	le.Tags = past.Tags
	le.Times.Expires, le.Times.ExpiryTime = past.Times.Expires, past.Times.ExpiryTime
	for _, cd := range past.CustomData {
		if cd.Key == cdKind {
			setCustom(le, cdKind, cd.Value)
		}
	}
	v.touch(le)
	return v.commit("save")
}

// MoveEntry moves an entry into another (non-trash) group.
func (v *Vault) MoveEntry(id, groupID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	le, from, idx, trash := v.findEntry(id)
	if le == nil || trash {
		return ErrNotFound
	}
	to := v.root()
	if groupID != "" {
		var t bool
		to, _, t = v.findGroup(groupID)
		if to == nil || t {
			return ErrNotFound
		}
	}
	if to == from {
		return nil
	}
	moved := *le
	from.Entries = append(from.Entries[:idx], from.Entries[idx+1:]...)
	now := v.nowWrap()
	moved.Times.LocationChanged = &now
	to.Entries = append(to.Entries, moved)
	return v.commit("save")
}

// Delete moves an entry to the recycle bin; an entry already there is removed for good (and its
// removal recorded so a sync does not bring it back). It reports whether the removal was permanent.
func (v *Vault) Delete(id string) (permanent bool, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return false, err
	}
	le, from, idx, trash := v.findEntry(id)
	if le == nil {
		return false, ErrNotFound
	}
	if trash || binID(v.db) == "" && !v.db.Content.Meta.RecycleBinEnabled.Bool {
		v.removeForGood(from, idx)
		return true, v.commit("delete")
	}
	moved := *le
	setCustom(&moved, cdPrevGroup, uuidHex(from.UUID))
	now := v.nowWrap()
	moved.Times.LocationChanged = &now
	from.Entries = append(from.Entries[:idx], from.Entries[idx+1:]...)
	bin := v.trashGroup(true) // may append to root.Groups: re-resolve nothing else afterwards
	bin.Entries = append(bin.Entries, moved)
	return false, v.commit("delete")
}

func (v *Vault) removeForGood(from *g.Group, idx int) {
	u := from.Entries[idx].UUID
	from.Entries = append(from.Entries[:idx], from.Entries[idx+1:]...)
	now := v.nowWrap()
	v.db.Content.Root.DeletedObjects = append(v.db.Content.Root.DeletedObjects, g.DeletedObjectData{UUID: u, DeletionTime: &now})
}

// RestoreEntry takes an entry out of the recycle bin, back to the group it was deleted from (the
// root if that is gone).
func (v *Vault) RestoreEntry(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	le, from, idx, trash := v.findEntry(id)
	if le == nil || !trash {
		return ErrNotFound
	}
	to := v.root()
	if gid := getCustom(le, cdPrevGroup); gid != "" {
		if gr, _, t := v.findGroup(gid); gr != nil && !t {
			to = gr
		}
	}
	moved := *le
	delCustom(&moved, cdPrevGroup)
	now := v.nowWrap()
	moved.Times.LocationChanged = &now
	from.Entries = append(from.Entries[:idx], from.Entries[idx+1:]...)
	to.Entries = append(to.Entries, moved)
	return v.commit("save")
}

// EmptyTrash removes everything in the recycle bin for good and returns how many entries went.
func (v *Vault) EmptyTrash() (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return 0, err
	}
	bin := v.trashGroup(false)
	if bin == nil {
		return 0, nil
	}
	n := 0
	now := v.nowWrap()
	var drop func(gr *g.Group)
	drop = func(gr *g.Group) {
		for _, e := range gr.Entries {
			v.db.Content.Root.DeletedObjects = append(v.db.Content.Root.DeletedObjects, g.DeletedObjectData{UUID: e.UUID, DeletionTime: &now})
			n++
		}
		gr.Entries = nil
		for i := range gr.Groups {
			drop(&gr.Groups[i])
			v.db.Content.Root.DeletedObjects = append(v.db.Content.Root.DeletedObjects, g.DeletedObjectData{UUID: gr.Groups[i].UUID, DeletionTime: &now})
		}
		gr.Groups = nil
	}
	drop(bin)
	if n == 0 {
		return 0, nil
	}
	return n, v.commit("delete")
}

// ---- listing and search ----------------------------------------------------------------------

// Filter narrows List. Query matches title, username, URL, tags, notes and custom field *names*
// (never secrets), case-insensitively.
type Filter struct {
	GroupID      string
	Kind         string
	Tag          string
	Query        string
	IncludeTrash bool
	OnlyTrash    bool
}

// List returns summaries sorted by title.
func (v *Vault) List(f Filter) ([]Summary, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(f.Query))
	tag := strings.ToLower(f.Tag)
	out := []Summary{}
	walk(v.db, func(le *libEntry, gid string, trash bool) {
		if trash && !f.IncludeTrash && !f.OnlyTrash || !trash && f.OnlyTrash {
			return
		}
		if f.GroupID != "" && gid != f.GroupID {
			return
		}
		e := fromLib(v.db, le, gid, trash)
		if f.Kind != "" && e.Kind != f.Kind {
			return
		}
		if tag != "" {
			found := false
			for _, t := range e.Tags {
				if strings.ToLower(t) == tag {
					found = true
				}
			}
			if !found {
				return
			}
		}
		if q != "" && !matches(e, q) {
			return
		}
		out = append(out, e.Summarize())
	}, nil)
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out, nil
}

func matches(e Entry, q string) bool {
	hay := []string{e.Title, e.Username, e.URL, e.Notes, strings.Join(e.Tags, " ")}
	for _, f := range e.Fields {
		hay = append(hay, f.Name)
		if !f.Protected {
			hay = append(hay, f.Value)
		}
	}
	for _, h := range hay {
		if strings.Contains(strings.ToLower(h), q) {
			return true
		}
	}
	return false
}

// ---- groups --------------------------------------------------------------------------------------

// Groups lists every group with its path ("Work / Servers") and entry count.
func (v *Vault) Groups() ([]Group, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return nil, err
	}
	paths := map[string]string{}
	out := []Group{}
	walk(v.db, nil, func(gr *libGroup, parent string, trash bool) {
		id := uuidHex(gr.UUID)
		p := gr.Name
		if parent != "" {
			p = paths[parent] + " / " + gr.Name
		}
		paths[id] = p
		out = append(out, Group{ID: id, ParentID: parent, Name: gr.Name, Path: p, Entries: len(gr.Entries), IsTrash: id == binID(v.db)})
	})
	return out, nil
}

// RootID is the id of the top group.
func (v *Vault) RootID() (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return "", err
	}
	return uuidHex(v.root().UUID), nil
}

func cleanName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || len(n) > 128 || strings.ContainsAny(n, "\x00\r\n") {
		return "", invalid("invalid group name")
	}
	return n, nil
}

// AddGroup creates a group under parentID (the root when empty).
func (v *Vault) AddGroup(parentID, name string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return "", err
	}
	name, err := cleanName(name)
	if err != nil {
		return "", err
	}
	par := v.root()
	if parentID != "" {
		var trash bool
		par, _, trash = v.findGroup(parentID)
		if par == nil || trash {
			return "", ErrNotFound
		}
	}
	if v.groupDepth(uuidHex(par.UUID)) >= maxGroupDepth {
		return "", invalid("groups can be nested at most %d deep", maxGroupDepth)
	}
	if v.countGroups() >= maxGroups {
		return "", ErrTooLarge
	}
	ng := v.newGroupNamed(name)
	par.Groups = append(par.Groups, ng)
	return uuidHex(ng.UUID), v.commit("save")
}

// RenameGroup renames a group (not the recycle bin).
func (v *Vault) RenameGroup(id, name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	name, err := cleanName(name)
	if err != nil {
		return err
	}
	gr, _, trash := v.findGroup(id)
	if gr == nil || trash {
		return ErrNotFound
	}
	gr.Name = name
	now := v.nowWrap()
	gr.Times.LastModificationTime = &now
	return v.commit("save")
}

// DeleteGroup removes an empty group (no entries, no subgroups). Delete the entries first: they go
// to the recycle bin and can be restored.
func (v *Vault) DeleteGroup(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	gr, par, trash := v.findGroup(id)
	if gr == nil || trash {
		return ErrNotFound
	}
	if par == nil {
		return invalid("the top group cannot be deleted")
	}
	if len(gr.Entries) > 0 || len(gr.Groups) > 0 {
		return ErrNotEmpty
	}
	for i := range par.Groups {
		if par.Groups[i].UUID == gr.UUID {
			now := v.nowWrap()
			v.db.Content.Root.DeletedObjects = append(v.db.Content.Root.DeletedObjects, g.DeletedObjectData{UUID: gr.UUID, DeletionTime: &now})
			par.Groups = append(par.Groups[:i], par.Groups[i+1:]...)
			break
		}
	}
	return v.commit("delete")
}

// ---- attachments ---------------------------------------------------------------------------------

func cleanAttachmentName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || len(n) > 255 || strings.ContainsAny(n, "/\\\x00") || n == "." || n == ".." {
		return "", invalid("invalid attachment name")
	}
	return n, nil
}

// AddAttachment stores a file in an entry (same name replaces).
func (v *Vault) AddAttachment(id, name string, data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	name, err := cleanAttachmentName(name)
	if err != nil {
		return err
	}
	if len(data) > MaxAttachmentBytes {
		return ErrTooLarge
	}
	le, _, _, _ := v.findEntry(id)
	if le == nil {
		return ErrNotFound
	}
	if len(le.Binaries) >= 32 {
		return ErrTooLarge
	}
	v.pushHistory(le)
	out := le.Binaries[:0]
	for _, b := range le.Binaries {
		if b.Name != name {
			out = append(out, b)
		}
	}
	le.Binaries = out
	bin := v.db.AddBinary(append([]byte(nil), data...))
	le.Binaries = append(le.Binaries, bin.CreateReference(name))
	v.touch(le)
	return v.commit("save")
}

// Attachment returns the content of an attachment.
func (v *Vault) Attachment(id, name string) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return nil, err
	}
	le, _, _, _ := v.findEntry(id)
	if le == nil {
		return nil, ErrNotFound
	}
	for _, br := range le.Binaries {
		if br.Name == name {
			b := v.db.FindBinary(br.Value.ID)
			if b == nil {
				return nil, ErrNotFound
			}
			c, err := b.GetContentBytes()
			if err != nil {
				return nil, ErrNotFound
			}
			return c, nil
		}
	}
	return nil, ErrNotFound
}

// RemoveAttachment deletes an attachment.
func (v *Vault) RemoveAttachment(id, name string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.usable(); err != nil {
		return err
	}
	le, _, _, _ := v.findEntry(id)
	if le == nil {
		return ErrNotFound
	}
	found := false
	out := le.Binaries[:0]
	for _, b := range le.Binaries {
		if b.Name == name {
			found = true
			continue
		}
		out = append(out, b)
	}
	if !found {
		return ErrNotFound
	}
	v.pushHistory(le)
	le.Binaries = out
	v.touch(le)
	return v.commit("save")
}

// newGroupNamed makes a group whose time stamps come from the vault's clock (the library would use the
// real one, which makes "which move is later" depend on two different clocks).
func (v *Vault) newGroupNamed(name string) g.Group {
	ng := g.NewGroup()
	ng.Name = name
	now := v.nowWrap()
	t := now
	u := now
	l := now
	ng.Times.CreationTime, ng.Times.LastModificationTime, ng.Times.LastAccessTime, ng.Times.LocationChanged = &t, &u, &now, &l
	return ng
}

func newEntry() g.Entry { return g.NewEntry() }

// groupDepth is the depth of a group (the top group is 1).
func (v *Vault) groupDepth(id string) int {
	depth, found := 0, false
	var rec func(gr *g.Group, d int)
	rec = func(gr *g.Group, d int) {
		if found {
			return
		}
		if uuidHex(gr.UUID) == id {
			depth, found = d, true
			return
		}
		for i := range gr.Groups {
			rec(&gr.Groups[i], d+1)
		}
	}
	for i := range v.db.Content.Root.Groups {
		rec(&v.db.Content.Root.Groups[i], 1)
	}
	return depth
}

func (v *Vault) countGroups() int {
	n := 0
	walk(v.db, nil, func(*libGroup, string, bool) { n++ })
	return n
}
