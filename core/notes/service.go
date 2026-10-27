// Package notes is the decentralized notes module: notes with a revision history that the user's own
// paired devices exchange directly (notes/docs/architecture.md). Markdown is the only stored format
// (core/notes/markdown); sync moves immutable revisions and a deterministic merge makes every device
// end up with the same notes (resolve.go, diff3.go); the transport is a few HTTP routes on the same
// mutually pinned TLS connection the other features use (sync.go).
package notes

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"nightclip/core/notes/markdown"
	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// Timing can be shortened by tests.
type Timing struct {
	Poll time.Duration // how often a dialing device asks its peers for news
	HTTP time.Duration // per sync round
}

func (t Timing) withDefaults() Timing {
	if t.Poll <= 0 {
		t.Poll = 10 * time.Second
	}
	if t.HTTP <= 0 {
		t.HTTP = 60 * time.Second
	}
	return t
}

// Config wires the service.
type Config struct {
	Engine   *peer.Engine // nil: the notes work locally and nothing syncs (unit tests)
	DB       *store.DB
	DeviceID string
	Emit     func(map[string]any)
	Now      func() time.Time
	Timing   Timing
}

// Service owns the notes of this device.
type Service struct {
	e      *peer.Engine
	db     *store.DB
	self   string
	emit   func(map[string]any)
	now    func() time.Time
	timing Timing

	mu    sync.Mutex // one local write at a time (the database serializes anyway; this keeps the events in order)
	sync  syncState
	index searchIndex
}

// New creates the service. When an engine is given it registers the sync routes on it, so it must be
// created before the engine starts.
func New(cfg Config) *Service {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Emit == nil {
		cfg.Emit = func(map[string]any) {}
	}
	s := &Service{e: cfg.Engine, db: cfg.DB, self: cfg.DeviceID, emit: cfg.Emit, now: cfg.Now, timing: cfg.Timing.withDefaults()}
	s.initSync()
	return s
}

func (s *Service) nowMs() int64 { return s.now().UnixMilli() }

// ---- events -------------------------------------------------------------------------------------

type changedNote struct{ noteID, revID string }

type conflictEvent struct{ noteID, conflictNoteID, reason string }

// changes collects what to tell the shells once a transaction has committed.
type changes struct {
	changed   []changedNote
	conflicts []conflictEvent
}

func (c *changes) add(o outcome) {
	if o.changed || o.merged {
		c.changed = append(c.changed, changedNote{o.noteID, o.headRev})
	}
	for _, cn := range o.conflictNotes {
		c.conflicts = append(c.conflicts, conflictEvent{o.noteID, cn, "diverged"})
	}
	if o.deletedElsewhere {
		c.conflicts = append(c.conflicts, conflictEvent{o.noteID, "", "deleted_elsewhere"})
	}
}

func (s *Service) flush(c changes) {
	for _, n := range c.changed {
		s.emit(map[string]any{"type": "notes.changed", "note_id": n.noteID, "rev_id": n.revID})
	}
	for _, e := range c.conflicts {
		ev := map[string]any{"type": "notes.sync.conflict", "note_id": e.noteID, "reason": e.reason}
		if e.conflictNoteID != "" {
			ev["conflict_note_id"] = e.conflictNoteID
		}
		s.emit(ev)
	}
}

// ---- results --------------------------------------------------------------------------------------

// NoteItem is one line of the note list.
type NoteItem struct {
	NoteID     string `json:"note_id"`
	Title      string `json:"title"`
	UpdatedAt  int64  `json:"updated_at"` // Unix milliseconds
	Deleted    bool   `json:"deleted"`
	Preview    string `json:"preview"`
	ConflictOf string `json:"conflict_of,omitempty"`
}

// ListResult is one page of the note list.
type ListResult struct {
	Items      []NoteItem `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// GetResult is one revision of a note with its text.
type GetResult struct {
	NoteID        string   `json:"note_id"`
	Title         string   `json:"title"`
	Body          string   `json:"body"`
	FormatVersion int      `json:"format_version"`
	RevID         string   `json:"rev_id"`
	Parents       []string `json:"parents"`
	CreatedAt     int64    `json:"created_at"` // when the note was created
	UpdatedAt     int64    `json:"updated_at"` // when this revision was made
	Deleted       bool     `json:"deleted"`
	OriginDevice  string   `json:"origin_device"`
	RevCount      int      `json:"rev_count"`
	IsHead        bool     `json:"is_head"`
	ConflictOf    string   `json:"conflict_of,omitempty"`
}

// CreateResult is returned by Create.
type CreateResult struct {
	NoteID string `json:"note_id"`
	RevID  string `json:"rev_id"`
}

// BlockPatch replaces one top-level block of the base revision by new Markdown (empty: remove it).
type BlockPatch struct {
	BlockID string `json:"block_id"`
	Body    string `json:"body"`
}

// UpdateParams describes an edit. BaseRevID is the revision the edit was made on; when it is no longer
// the newest, the edit is merged into the newest instead of overwriting it.
type UpdateParams struct {
	NoteID    string       `json:"note_id"`
	BaseRevID string       `json:"base_rev_id"`
	Title     *string      `json:"title"`
	Body      *string      `json:"body"`
	Patch     []BlockPatch `json:"patch"`
}

// UpdateResult is returned by Update.
type UpdateResult struct {
	RevID string `json:"rev_id"`
	// Changed is false when the edit changed nothing (relative to the revision it was made on), so no
	// revision was made and RevID is simply the note's current revision.
	Changed        bool   `json:"changed"`
	Merged         bool   `json:"merged"`
	ConflictNoteID string `json:"conflict_note_id,omitempty"`
}

// HistoryItem is one revision in a note's history.
type HistoryItem struct {
	RevID        string   `json:"rev_id"`
	Title        string   `json:"title"`
	BodyHash     string   `json:"body_hash"`
	OriginDevice string   `json:"origin_device"`
	CreatedAt    int64    `json:"created_at"`
	Parents      []string `json:"parents"`
	Deleted      bool     `json:"deleted"`
}

// HistoryResult is one page of history, newest first.
type HistoryResult struct {
	Items      []HistoryItem `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// ConflictItem names a conflict copy and the note it was set aside from.
type ConflictItem struct {
	NoteID         string `json:"note_id"`
	ConflictNoteID string `json:"conflict_note_id"`
	Title          string `json:"title"`
	CreatedAt      int64  `json:"created_at"`
}

// ConflictsResult is one page of the conflict list.
type ConflictsResult struct {
	Items      []ConflictItem `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// ---- helpers --------------------------------------------------------------------------------------

func notFound() error { return proto.Errorf(proto.CodeNotFound, "no such note") }

func (s *Service) checkNoteID(id string) error {
	if !noteIDRe.MatchString(id) {
		return proto.Errorf(proto.CodeInvalid, "note_id is malformed")
	}
	return nil
}

func encodeCursor(c *store.NoteCursor) string {
	if c == nil {
		return ""
	}
	return fmt.Sprintf("%d:%s", c.UpdatedAt, c.NoteID)
}

func decodeCursor(s string) (*store.NoteCursor, error) {
	if s == "" {
		return nil, nil
	}
	at, id, ok := strings.Cut(s, ":")
	n, err := strconv.ParseInt(at, 10, 64)
	if !ok || err != nil || !noteIDRe.MatchString(id) {
		return nil, proto.Errorf(proto.CodeInvalid, "cursor is malformed")
	}
	return &store.NoteCursor{UpdatedAt: n, NoteID: id}, nil
}

func items(rows []store.NoteRow) []NoteItem {
	out := make([]NoteItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, NoteItem{NoteID: r.NoteID, Title: r.Title, UpdatedAt: r.UpdatedAt, Deleted: r.Deleted, Preview: r.Preview, ConflictOf: r.ConflictOf})
	}
	return out
}

// child makes the revision that follows parent with the given content. Its time is never earlier than
// its parent's, so a note's updated_at only moves forward even if the clock does not.
func (s *Service) child(parent store.NoteRev, title, body string, deleted bool) store.NoteRev {
	at := s.nowMs()
	if at <= parent.CreatedAt {
		at = parent.CreatedAt + 1
	}
	return seal(store.NoteRev{NoteID: parent.NoteID, Parents: []string{parent.RevID}, Title: title, Body: body,
		FormatVersion: markdown.FormatVersion, OriginDevice: s.self, CreatedAt: at, Deleted: deleted, ConflictOf: parent.ConflictOf})
}

// head returns the single current revision of a note, merging first in the (unexpected) case that the
// note is found with several heads.
func head(tx *store.NotesTx, noteID string, ch *changes) (store.NoteRow, store.NoteRev, error) {
	note, found, err := tx.GetNote(noteID)
	if err != nil {
		return store.NoteRow{}, store.NoteRev{}, err
	}
	if !found {
		return store.NoteRow{}, store.NoteRev{}, notFound()
	}
	heads, err := tx.Heads(noteID)
	if err != nil {
		return store.NoteRow{}, store.NoteRev{}, err
	}
	if len(heads) != 1 || heads[0] != note.HeadRev {
		o, err := reconcile(tx, noteID)
		if err != nil {
			return store.NoteRow{}, store.NoteRev{}, err
		}
		ch.add(o)
		if note, _, err = tx.GetNote(noteID); err != nil {
			return store.NoteRow{}, store.NoteRev{}, err
		}
	}
	rev, found, err := tx.GetRev(note.HeadRev)
	if err != nil {
		return store.NoteRow{}, store.NoteRev{}, err
	}
	if !found {
		return store.NoteRow{}, store.NoteRev{}, proto.Errorf(proto.CodeInternal, "note is inconsistent")
	}
	return note, rev, nil
}

// applyPatch replaces blocks of body (found by id) with the patched Markdown.
func applyPatch(body string, patch []BlockPatch) (string, error) {
	blocks := markdown.Split(body)
	byID := map[string]string{}
	for _, p := range patch {
		if _, dup := byID[p.BlockID]; dup {
			return "", proto.Errorf(proto.CodeInvalid, "block %s is patched twice", p.BlockID)
		}
		byID[p.BlockID] = p.Body
	}
	raws := make([]string, 0, len(blocks)+len(patch))
	used := map[string]bool{}
	for _, b := range blocks {
		if text, ok := byID[b.ID]; ok && !used[b.ID] {
			used[b.ID] = true
			if strings.TrimSpace(text) != "" {
				raws = append(raws, text)
			}
			continue
		}
		raws = append(raws, b.Raw)
	}
	for id := range byID {
		if !used[id] {
			return "", proto.Errorf(proto.CodeInvalid, "the revision has no block %s", id)
		}
	}
	return normBody(markdown.JoinRaw(raws))
}

// ---- operations -----------------------------------------------------------------------------------

// Create makes a new note.
func (s *Service) Create(title, body string) (CreateResult, error) {
	t, err := checkTitle(title)
	if err != nil {
		return CreateResult{}, err
	}
	b, err := normBody(body)
	if err != nil {
		return CreateResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rev := seal(store.NoteRev{NoteID: newNoteID(), Title: t, Body: b, FormatVersion: markdown.FormatVersion, OriginDevice: s.self, CreatedAt: s.nowMs()})
	var ch changes
	err = s.db.NotesTx(func(tx *store.NotesTx) error {
		if _, err := tx.InsertRev(rev); err != nil {
			return err
		}
		o, err := reconcile(tx, rev.NoteID)
		ch.add(o)
		return err
	})
	if err != nil {
		return CreateResult{}, err
	}
	s.flush(ch)
	s.Kick()
	return CreateResult{NoteID: rev.NoteID, RevID: rev.RevID}, nil
}

// Update edits a note. An edit made on an older revision than the newest is merged into the newest
// (blocks that only one side touched combine; otherwise the older edit becomes a conflict copy).
func (s *Service) Update(p UpdateParams) (UpdateResult, error) {
	if err := s.checkNoteID(p.NoteID); err != nil {
		return UpdateResult{}, err
	}
	if p.Body != nil && p.Patch != nil {
		return UpdateResult{}, proto.Errorf(proto.CodeInvalid, "give either body or patch, not both")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var res UpdateResult
	var ch changes
	err := s.db.NotesTx(func(tx *store.NotesTx) error {
		note, cur, err := head(tx, p.NoteID, &ch)
		if err != nil {
			return err
		}
		if note.Deleted {
			return proto.Errorf(proto.CodeConflict, "the note is in the trash: restore it first")
		}
		base := cur
		if p.BaseRevID != "" && p.BaseRevID != cur.RevID {
			var found bool
			if base, found, err = tx.GetRev(p.BaseRevID); err != nil {
				return err
			}
			if !found || base.NoteID != p.NoteID {
				return proto.Errorf(proto.CodeNotFound, "unknown base revision")
			}
		}
		title, body := base.Title, base.Body
		if p.Title != nil {
			if title, err = checkTitle(*p.Title); err != nil {
				return err
			}
		}
		switch {
		case p.Body != nil:
			body, err = normBody(*p.Body)
		case p.Patch != nil:
			body, err = applyPatch(base.Body, p.Patch)
		}
		if err != nil {
			return err
		}
		if title == base.Title && body == base.Body {
			res.RevID = cur.RevID // nothing changed relative to the revision the edit was made on
			return nil
		}
		rev := s.child(base, title, body, false)
		if _, err := tx.InsertRev(rev); err != nil {
			return err
		}
		o, err := reconcile(tx, p.NoteID)
		if err != nil {
			return err
		}
		ch.add(o)
		res = UpdateResult{RevID: o.headRev, Changed: true, Merged: o.merged}
		if len(o.conflictNotes) > 0 {
			res.ConflictNoteID = o.conflictNotes[0]
		}
		return nil
	})
	if err != nil {
		return UpdateResult{}, err
	}
	s.flush(ch)
	s.Kick()
	return res, nil
}

// setDeleted appends a tombstone (or its reversal) to a note.
func (s *Service) setDeleted(tx *store.NotesTx, noteID string, deleted bool, ch *changes) (string, error) {
	note, cur, err := head(tx, noteID, ch)
	if err != nil {
		return "", err
	}
	if note.Deleted == deleted {
		return cur.RevID, nil
	}
	rev := s.child(cur, cur.Title, cur.Body, deleted)
	if _, err := tx.InsertRev(rev); err != nil {
		return "", err
	}
	o, err := reconcile(tx, noteID)
	if err != nil {
		return "", err
	}
	ch.add(o)
	return o.headRev, nil
}

// Delete moves a note to the trash (a tombstone revision; nothing is erased).
func (s *Service) Delete(noteID string) (string, error) { return s.toggle(noteID, true) }

// Restore takes a note out of the trash.
func (s *Service) Restore(noteID string) (string, error) { return s.toggle(noteID, false) }

func (s *Service) toggle(noteID string, deleted bool) (string, error) {
	if err := s.checkNoteID(noteID); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var rev string
	var ch changes
	err := s.db.NotesTx(func(tx *store.NotesTx) error {
		var err error
		rev, err = s.setDeleted(tx, noteID, deleted, &ch)
		return err
	})
	if err != nil {
		return "", err
	}
	s.flush(ch)
	s.Kick()
	return rev, nil
}

// List returns a page of notes, newest first: the live ones, or the trash.
func (s *Service) List(limit int, cursor string, deleted bool) (ListResult, error) {
	cur, err := decodeCursor(cursor)
	if err != nil {
		return ListResult{}, err
	}
	var res ListResult
	err = s.db.NotesView(func(tx *store.NotesTx) error {
		rows, next, err := tx.ListNotes(deleted, limit, cur)
		res = ListResult{Items: items(rows), NextCursor: encodeCursor(next)}
		return err
	})
	return res, err
}

// Get returns a note: its newest revision, or the one named.
func (s *Service) Get(noteID, revID string) (GetResult, error) {
	if err := s.checkNoteID(noteID); err != nil {
		return GetResult{}, err
	}
	var res GetResult
	err := s.db.NotesView(func(tx *store.NotesTx) error {
		note, found, err := tx.GetNote(noteID)
		if err != nil {
			return err
		}
		if !found {
			return notFound()
		}
		if revID == "" {
			revID = note.HeadRev
		}
		rev, found, err := tx.GetRev(revID)
		if err != nil {
			return err
		}
		if !found || rev.NoteID != noteID {
			return proto.Errorf(proto.CodeNotFound, "no such revision")
		}
		graph, err := tx.Graph(noteID)
		if err != nil {
			return err
		}
		parents := rev.Parents
		if parents == nil {
			parents = []string{}
		}
		res = GetResult{NoteID: noteID, Title: rev.Title, Body: rev.Body, FormatVersion: rev.FormatVersion, RevID: rev.RevID, Parents: parents,
			CreatedAt: note.CreatedAt, UpdatedAt: rev.CreatedAt, Deleted: rev.Deleted, OriginDevice: rev.OriginDevice,
			RevCount: len(graph), IsHead: rev.RevID == note.HeadRev, ConflictOf: rev.ConflictOf}
		return nil
	})
	return res, err
}

// RenderResult is a note ready for the shells to draw.
type RenderResult struct {
	NoteID string `json:"note_id,omitempty"`
	RevID  string `json:"rev_id,omitempty"`
	markdown.Rendered
}

// RenderText renders a text that is not saved (yet): the live preview of what is being typed.
func (s *Service) RenderText(body string) (RenderResult, error) {
	norm, err := normBody(body)
	if err != nil {
		return RenderResult{}, err
	}
	return RenderResult{Rendered: markdown.Render(norm)}, nil
}

// Render returns the structured render tree of a note (newest revision, or the one named).
func (s *Service) Render(noteID, revID string) (RenderResult, error) {
	g, err := s.Get(noteID, revID)
	if err != nil {
		return RenderResult{}, err
	}
	return RenderResult{NoteID: noteID, RevID: g.RevID, Rendered: markdown.Render(g.Body)}, nil
}

// Normalize previews the canonical form of a text, so a shell can show what will be stored.
func (s *Service) Normalize(body string) (norm, hash string, err error) {
	norm, err = normBody(body)
	if err != nil {
		return "", "", err
	}
	return norm, markdown.Hash(norm), nil
}

// History lists the revisions of a note, newest first.
func (s *Service) History(noteID string, limit int, cursor string) (HistoryResult, error) {
	if err := s.checkNoteID(noteID); err != nil {
		return HistoryResult{}, err
	}
	var before int64
	if cursor != "" {
		n, err := strconv.ParseInt(cursor, 10, 64)
		if err != nil || n <= 0 {
			return HistoryResult{}, proto.Errorf(proto.CodeInvalid, "cursor is malformed")
		}
		before = n
	}
	var res HistoryResult
	err := s.db.NotesView(func(tx *store.NotesTx) error {
		if _, found, err := tx.GetNote(noteID); err != nil || !found {
			if err != nil {
				return err
			}
			return notFound()
		}
		page, next, err := tx.RevPage(noteID, limit, before)
		if err != nil {
			return err
		}
		res.Items = make([]HistoryItem, 0, len(page))
		for _, m := range page {
			parents := m.Parents
			if parents == nil {
				parents = []string{}
			}
			res.Items = append(res.Items, HistoryItem{RevID: m.RevID, Title: m.Title, BodyHash: m.BodyHash, OriginDevice: m.OriginDevice,
				CreatedAt: m.CreatedAt, Parents: parents, Deleted: m.Deleted})
		}
		if next > 0 {
			res.NextCursor = strconv.FormatInt(next, 10)
		}
		return nil
	})
	return res, err
}

// Conflicts lists the conflict copies waiting for a decision.
func (s *Service) Conflicts(limit int, cursor string) (ConflictsResult, error) {
	cur, err := decodeCursor(cursor)
	if err != nil {
		return ConflictsResult{}, err
	}
	res := ConflictsResult{Items: []ConflictItem{}}
	err = s.db.NotesView(func(tx *store.NotesTx) error {
		rows, next, err := tx.ListConflictNotes(limit, cur)
		if err != nil {
			return err
		}
		for _, r := range rows {
			res.Items = append(res.Items, ConflictItem{NoteID: r.ConflictOf, ConflictNoteID: r.NoteID, Title: r.Title, CreatedAt: r.CreatedAt})
		}
		res.NextCursor = encodeCursor(next)
		return nil
	})
	return res, err
}

// Resolve settles a conflict copy: keep_main drops the copy; keep_conflict puts the copy's text into
// the note and drops the copy; merge appends the copy's text to the note and drops the copy. The copy
// goes to the trash like any deleted note.
func (s *Service) Resolve(noteID, conflictNoteID, action string) (string, error) {
	if err := s.checkNoteID(noteID); err != nil {
		return "", err
	}
	if err := s.checkNoteID(conflictNoteID); err != nil {
		return "", err
	}
	if action != "keep_main" && action != "keep_conflict" && action != "merge" {
		return "", proto.Errorf(proto.CodeInvalid, "action must be keep_main, keep_conflict or merge")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var result string
	var ch changes
	err := s.db.NotesTx(func(tx *store.NotesTx) error {
		cnote, cur, err := head(tx, conflictNoteID, &ch)
		if err != nil {
			return err
		}
		if cnote.Deleted || cnote.ConflictOf != noteID {
			return notFound()
		}
		mainNote, main, err := head(tx, noteID, &ch)
		if err != nil {
			return err
		}
		result = main.RevID
		if action != "keep_main" {
			if mainNote.Deleted {
				return proto.Errorf(proto.CodeConflict, "the note is in the trash: restore it first")
			}
			title, body := stripConflict(cur.Title, cur.Body)
			newTitle, newBody := main.Title, body
			if action == "keep_conflict" && title != "" {
				newTitle = title
			}
			if action == "merge" {
				newBody = main.Body
				if strings.TrimSpace(main.Body) != "" && strings.TrimSpace(body) != "" {
					newBody = main.Body + "\n---\n\n" + body
				} else if strings.TrimSpace(main.Body) == "" {
					newBody = body
				}
				if newBody, err = normBody(newBody); err != nil {
					return err
				}
			}
			if newTitle != main.Title || newBody != main.Body {
				rev := s.child(main, newTitle, newBody, false)
				if _, err := tx.InsertRev(rev); err != nil {
					return err
				}
				o, err := reconcile(tx, noteID)
				if err != nil {
					return err
				}
				ch.add(o)
				result = o.headRev
			}
		}
		_, err = s.setDeleted(tx, conflictNoteID, true, &ch)
		return err
	})
	if err != nil {
		return "", err
	}
	s.flush(ch)
	s.Kick()
	return result, nil
}
