package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"

	"nightclip/core/proto"
)

// The notes tables (notes/docs/sync-model.md §2). Like the rest of this package the code here is
// policy-free: it persists revisions and keeps the head markers right, while core/notes decides what a
// merge is. Titles, bodies and previews are sealed with the database key.

// ErrMissingParent is returned by NotesTx.InsertRev when a parent revision is not stored (or belongs
// to another note): a revision is only ever stored after its whole ancestry.
var ErrMissingParent = errors.New("store: revision parent is missing")

// NoteRev is one immutable revision of a note.
type NoteRev struct {
	RevID         string
	NoteID        string
	Parents       []string // sorted, no duplicates
	Title         string
	Body          string // normalized Markdown
	BodyHash      string
	FormatVersion int
	OriginDevice  string
	CreatedAt     int64 // unix milliseconds
	Deleted       bool
	ConflictOf    string // note id this note is a conflict copy of; "" otherwise
}

// RevMeta is NoteRev without the sealed text, for walking the revision graph cheaply.
type RevMeta struct {
	RevID        string
	Parents      []string
	OriginDevice string
	CreatedAt    int64
	Deleted      bool
	BodyHash     string
	Title        string // only set by RevPage (history)
}

// NoteRow is the current state of one note (derived from its head revision by core/notes).
type NoteRow struct {
	NoteID     string
	CreatedAt  int64
	UpdatedAt  int64
	HeadRev    string
	Deleted    bool
	ConflictOf string
	Title      string
	Preview    string
}

// NoteHeads is one entry of the sync digest.
type NoteHeads struct {
	NoteID string
	Heads  []string
}

// NoteSyncRow is the sync bookkeeping for one peer.
type NoteSyncRow struct {
	PeerID   string
	State    string // idle | syncing | error | peer_old
	Detail   string
	LastOKAt int64
	PeerSeq  int64 // the peer's notes seq the last time its digest was fully processed (-1: never)
	OurSeq   int64 // our notes seq when we last made sure the peer had everything (-1: never)
	Behind   int
	Ahead    int
}

// NotesTx is one transaction over the notes tables. The database has a single connection, so inside
// the callback only the methods of the NotesTx may be used (any other DB call would wait forever).
type NotesTx struct {
	db *DB
	tx *sql.Tx
}

// NotesTx runs fn in a read-write transaction: it commits when fn returns nil.
func (db *DB) NotesTx(fn func(*NotesTx) error) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return dbErr("begin notes transaction", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit
	if err := fn(&NotesTx{db: db, tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return dbErr("commit notes transaction", err)
	}
	return nil
}

// NotesView runs fn in a transaction that is always rolled back: reads only.
func (db *DB) NotesView(fn func(*NotesTx) error) error {
	tx, err := db.sql.Begin()
	if err != nil {
		return dbErr("begin notes transaction", err)
	}
	defer tx.Rollback() //nolint:errcheck
	return fn(&NotesTx{db: db, tx: tx})
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Seq is a counter that grows with every stored revision (0 = none). It only has meaning on this
// device: sync compares it with the value it saw earlier to learn "nothing new since".
func (t *NotesTx) Seq() (int64, error) {
	var n sql.NullInt64
	if err := t.tx.QueryRow(`SELECT MAX(arrival) FROM note_revs`).Scan(&n); err != nil {
		return 0, dbErr("read notes seq", err)
	}
	return n.Int64, nil
}

// HasRev reports whether the revision is stored.
func (t *NotesTx) HasRev(revID string) (bool, error) {
	var one int
	err := t.tx.QueryRow(`SELECT 1 FROM note_revs WHERE rev_id = ?`, revID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, dbErr("read note revision", err)
	}
	return true, nil
}

// GetRev returns one revision with its text.
func (t *NotesTx) GetRev(revID string) (NoteRev, bool, error) {
	var (
		r                 NoteRev
		parents           string
		titleEnc, bodyEnc []byte
		deleted, format   int
	)
	err := t.tx.QueryRow(`SELECT rev_id, note_id, parents_json, title_enc, body_enc, body_hash, format_version,
			origin_device, created_at, deleted, conflict_of FROM note_revs WHERE rev_id = ?`, revID).
		Scan(&r.RevID, &r.NoteID, &parents, &titleEnc, &bodyEnc, &r.BodyHash, &format, &r.OriginDevice, &r.CreatedAt, &deleted, &r.ConflictOf)
	if errors.Is(err, sql.ErrNoRows) {
		return NoteRev{}, false, nil
	}
	if err != nil {
		return NoteRev{}, false, dbErr("read note revision", err)
	}
	if err := json.Unmarshal([]byte(parents), &r.Parents); err != nil {
		return NoteRev{}, false, dbErr("read note revision", err)
	}
	title, err := t.db.unseal(titleEnc, rowAAD("note_revs", "title_enc", revID))
	if err != nil {
		return NoteRev{}, false, proto.Errorf(proto.CodeKeyInvalid, "cannot open note revision")
	}
	body, err := t.db.unseal(bodyEnc, rowAAD("note_revs", "body_enc", revID))
	if err != nil {
		return NoteRev{}, false, proto.Errorf(proto.CodeKeyInvalid, "cannot open note revision")
	}
	r.Title, r.Body, r.FormatVersion, r.Deleted = string(title), string(body), format, deleted != 0
	return r, true, nil
}

// InsertRev stores a revision (no-op when it is already stored). Every parent must already be stored
// for the same note. It keeps the head markers right: the new revision is a head, its parents are not.
func (t *NotesTx) InsertRev(r NoteRev) (inserted bool, err error) {
	has, err := t.HasRev(r.RevID)
	if err != nil || has {
		return false, err
	}
	parents := append([]string(nil), r.Parents...)
	sort.Strings(parents)
	for _, p := range parents {
		var note string
		err := t.tx.QueryRow(`SELECT note_id FROM note_revs WHERE rev_id = ?`, p).Scan(&note)
		if errors.Is(err, sql.ErrNoRows) || err == nil && note != r.NoteID {
			return false, ErrMissingParent
		}
		if err != nil {
			return false, dbErr("read note revision", err)
		}
	}
	raw, err := json.Marshal(append([]string{}, parents...))
	if err != nil {
		return false, dbErr("store note revision", err)
	}
	titleEnc, err := t.db.seal([]byte(r.Title), rowAAD("note_revs", "title_enc", r.RevID))
	if err != nil {
		return false, dbErr("store note revision", err)
	}
	bodyEnc, err := t.db.seal([]byte(r.Body), rowAAD("note_revs", "body_enc", r.RevID))
	if err != nil {
		return false, dbErr("store note revision", err)
	}
	seq, err := t.Seq()
	if err != nil {
		return false, err
	}
	if _, err := t.tx.Exec(`INSERT INTO note_revs (rev_id, note_id, parents_json, title_enc, body_enc, body_hash, format_version,
			origin_device, created_at, deleted, conflict_of, is_head, arrival) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		r.RevID, r.NoteID, string(raw), titleEnc, bodyEnc, r.BodyHash, r.FormatVersion, r.OriginDevice, r.CreatedAt, b2i(r.Deleted), r.ConflictOf, seq+1); err != nil {
		return false, dbErr("store note revision", err)
	}
	for _, p := range parents {
		if _, err := t.tx.Exec(`UPDATE note_revs SET is_head = 0 WHERE rev_id = ?`, p); err != nil {
			return false, dbErr("store note revision", err)
		}
	}
	return true, nil
}

// Heads returns the head revisions of a note, sorted by id.
func (t *NotesTx) Heads(noteID string) ([]string, error) {
	rows, err := t.tx.Query(`SELECT rev_id FROM note_revs WHERE note_id = ? AND is_head = 1 ORDER BY rev_id`, noteID)
	if err != nil {
		return nil, dbErr("read note heads", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, dbErr("read note heads", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Graph returns the metadata of every revision of a note (no text), oldest arrival first.
func (t *NotesTx) Graph(noteID string) ([]RevMeta, error) {
	rows, err := t.tx.Query(`SELECT rev_id, parents_json, origin_device, created_at, deleted, body_hash
		FROM note_revs WHERE note_id = ? ORDER BY arrival`, noteID)
	if err != nil {
		return nil, dbErr("read note graph", err)
	}
	defer rows.Close()
	var out []RevMeta
	for rows.Next() {
		var m RevMeta
		var parents string
		var deleted int
		if err := rows.Scan(&m.RevID, &parents, &m.OriginDevice, &m.CreatedAt, &deleted, &m.BodyHash); err != nil {
			return nil, dbErr("read note graph", err)
		}
		if err := json.Unmarshal([]byte(parents), &m.Parents); err != nil {
			return nil, dbErr("read note graph", err)
		}
		m.Deleted = deleted != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

// RevPage lists a note's revisions newest first (history). before is the cursor from a previous page
// (0 = from the newest); the returned cursor is 0 when there is no more.
func (t *NotesTx) RevPage(noteID string, limit int, before int64) ([]RevMeta, int64, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if before <= 0 {
		before = 1<<62 - 1
	}
	rows, err := t.tx.Query(`SELECT rev_id, parents_json, title_enc, origin_device, created_at, deleted, body_hash, arrival
		FROM note_revs WHERE note_id = ? AND arrival < ? ORDER BY arrival DESC LIMIT ?`, noteID, before, limit+1)
	if err != nil {
		return nil, 0, dbErr("read note history", err)
	}
	defer rows.Close()
	var out []RevMeta
	var arrivals []int64
	for rows.Next() {
		var m RevMeta
		var parents string
		var titleEnc []byte
		var deleted int
		var arrival int64
		if err := rows.Scan(&m.RevID, &parents, &titleEnc, &m.OriginDevice, &m.CreatedAt, &deleted, &m.BodyHash, &arrival); err != nil {
			return nil, 0, dbErr("read note history", err)
		}
		if err := json.Unmarshal([]byte(parents), &m.Parents); err != nil {
			return nil, 0, dbErr("read note history", err)
		}
		title, err := t.db.unseal(titleEnc, rowAAD("note_revs", "title_enc", m.RevID))
		if err != nil {
			return nil, 0, proto.Errorf(proto.CodeKeyInvalid, "cannot open note revision")
		}
		m.Title, m.Deleted = string(title), deleted != 0
		out = append(out, m)
		arrivals = append(arrivals, arrival)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, dbErr("read note history", err)
	}
	var next int64
	if len(out) > limit {
		out, next = out[:limit], arrivals[limit-1]
	}
	return out, next, nil
}

// GetNote returns the note's current state.
func (t *NotesTx) GetNote(noteID string) (NoteRow, bool, error) {
	row := t.tx.QueryRow(`SELECT note_id, created_at, updated_at, head_rev, deleted, conflict_of, title_enc, preview_enc
		FROM notes WHERE note_id = ?`, noteID)
	n, err := t.scanNote(row)
	if errors.Is(err, sql.ErrNoRows) {
		return NoteRow{}, false, nil
	}
	if err != nil {
		return NoteRow{}, false, err
	}
	return n, true, nil
}

func (t *NotesTx) scanNote(r rowScanner) (NoteRow, error) {
	var n NoteRow
	var deleted int
	var titleEnc, previewEnc []byte
	if err := r.Scan(&n.NoteID, &n.CreatedAt, &n.UpdatedAt, &n.HeadRev, &deleted, &n.ConflictOf, &titleEnc, &previewEnc); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NoteRow{}, err
		}
		return NoteRow{}, dbErr("read note", err)
	}
	title, err := t.db.unseal(titleEnc, rowAAD("notes", "title_enc", n.NoteID))
	if err != nil {
		return NoteRow{}, proto.Errorf(proto.CodeKeyInvalid, "cannot open note")
	}
	preview, err := t.db.unseal(previewEnc, rowAAD("notes", "preview_enc", n.NoteID))
	if err != nil {
		return NoteRow{}, proto.Errorf(proto.CodeKeyInvalid, "cannot open note")
	}
	n.Title, n.Preview, n.Deleted = string(title), string(preview), deleted != 0
	return n, nil
}

// PutNote stores the current state of a note (insert or replace).
func (t *NotesTx) PutNote(n NoteRow) error {
	titleEnc, err := t.db.seal([]byte(n.Title), rowAAD("notes", "title_enc", n.NoteID))
	if err != nil {
		return dbErr("store note", err)
	}
	previewEnc, err := t.db.seal([]byte(n.Preview), rowAAD("notes", "preview_enc", n.NoteID))
	if err != nil {
		return dbErr("store note", err)
	}
	_, err = t.tx.Exec(`INSERT INTO notes (note_id, created_at, updated_at, head_rev, deleted, conflict_of, title_enc, preview_enc)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(note_id) DO UPDATE SET created_at = excluded.created_at, updated_at = excluded.updated_at,
			head_rev = excluded.head_rev, deleted = excluded.deleted, conflict_of = excluded.conflict_of,
			title_enc = excluded.title_enc, preview_enc = excluded.preview_enc`,
		n.NoteID, n.CreatedAt, n.UpdatedAt, n.HeadRev, b2i(n.Deleted), n.ConflictOf, titleEnc, previewEnc)
	if err != nil {
		return dbErr("store note", err)
	}
	return nil
}

// NoteCursor positions a ListNotes page: notes are ordered by (updated_at desc, note_id desc).
type NoteCursor struct {
	UpdatedAt int64
	NoteID    string
}

// ListNotes returns one page of notes, deleted or not, newest first. A non-nil next continues the list.
func (t *NotesTx) ListNotes(deleted bool, limit int, after *NoteCursor) ([]NoteRow, *NoteCursor, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT note_id, created_at, updated_at, head_rev, deleted, conflict_of, title_enc, preview_enc FROM notes WHERE deleted = ?`
	args := []any{b2i(deleted)}
	if after != nil {
		q += ` AND (updated_at < ? OR (updated_at = ? AND note_id < ?))`
		args = append(args, after.UpdatedAt, after.UpdatedAt, after.NoteID)
	}
	q += ` ORDER BY updated_at DESC, note_id DESC LIMIT ?`
	args = append(args, limit+1)
	return t.queryNotes(q, args, limit)
}

// ListConflictNotes returns the live conflict copies, newest first.
func (t *NotesTx) ListConflictNotes(limit int, after *NoteCursor) ([]NoteRow, *NoteCursor, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT note_id, created_at, updated_at, head_rev, deleted, conflict_of, title_enc, preview_enc
		FROM notes WHERE deleted = 0 AND conflict_of != ''`
	var args []any
	if after != nil {
		q += ` AND (updated_at < ? OR (updated_at = ? AND note_id < ?))`
		args = append(args, after.UpdatedAt, after.UpdatedAt, after.NoteID)
	}
	q += ` ORDER BY updated_at DESC, note_id DESC LIMIT ?`
	args = append(args, limit+1)
	return t.queryNotes(q, args, limit)
}

// CountConflictNotes is the number of live conflict copies.
func (t *NotesTx) CountConflictNotes() (int, error) {
	var n int
	if err := t.tx.QueryRow(`SELECT COUNT(*) FROM notes WHERE deleted = 0 AND conflict_of != ''`).Scan(&n); err != nil {
		return 0, dbErr("count note conflicts", err)
	}
	return n, nil
}

func (t *NotesTx) queryNotes(q string, args []any, limit int) ([]NoteRow, *NoteCursor, error) {
	rows, err := t.tx.Query(q, args...)
	if err != nil {
		return nil, nil, dbErr("list notes", err)
	}
	defer rows.Close()
	var out []NoteRow
	for rows.Next() {
		n, err := t.scanNote(rows)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, dbErr("list notes", err)
	}
	var next *NoteCursor
	if len(out) > limit {
		out = out[:limit]
		next = &NoteCursor{UpdatedAt: out[limit-1].UpdatedAt, NoteID: out[limit-1].NoteID}
	}
	return out, next, nil
}

// Digest returns up to limit notes after the given id, ordered by id, each with its head revisions.
func (t *NotesTx) Digest(after string, limit int) ([]NoteHeads, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := t.tx.Query(`SELECT n.note_id, r.rev_id FROM
			(SELECT note_id FROM notes WHERE note_id > ? ORDER BY note_id LIMIT ?) n
			JOIN note_revs r ON r.note_id = n.note_id AND r.is_head = 1
			ORDER BY n.note_id, r.rev_id`, after, limit)
	if err != nil {
		return nil, dbErr("read notes digest", err)
	}
	defer rows.Close()
	var out []NoteHeads
	for rows.Next() {
		var note, rev string
		if err := rows.Scan(&note, &rev); err != nil {
			return nil, dbErr("read notes digest", err)
		}
		if len(out) == 0 || out[len(out)-1].NoteID != note {
			out = append(out, NoteHeads{NoteID: note})
		}
		out[len(out)-1].Heads = append(out[len(out)-1].Heads, rev)
	}
	return out, rows.Err()
}

// GetNoteSync returns the sync row for a peer; the zero state (never synced) with found=false when none.
func (db *DB) GetNoteSync(peerID string) (NoteSyncRow, bool, error) {
	r := NoteSyncRow{PeerID: peerID, State: "idle", PeerSeq: -1, OurSeq: -1}
	err := db.sql.QueryRow(`SELECT state, detail, last_ok_at, peer_seq, our_seq, behind, ahead FROM note_peer_state WHERE peer_id = ?`, peerID).
		Scan(&r.State, &r.Detail, &r.LastOKAt, &r.PeerSeq, &r.OurSeq, &r.Behind, &r.Ahead)
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	if err != nil {
		return NoteSyncRow{}, false, dbErr("read notes sync", err)
	}
	return r, true, nil
}

// PutNoteSync stores the row. A peer that is no longer stored has no row to hold it (foreign key):
// that is not an error, the bookkeeping simply has nothing to attach to.
func (db *DB) PutNoteSync(r NoteSyncRow) error {
	if r.State == "" {
		r.State = "idle"
	}
	_, err := db.sql.Exec(`
		INSERT INTO note_peer_state (peer_id, state, detail, last_ok_at, peer_seq, our_seq, behind, ahead) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET state = excluded.state, detail = excluded.detail, last_ok_at = excluded.last_ok_at,
			peer_seq = excluded.peer_seq, our_seq = excluded.our_seq, behind = excluded.behind, ahead = excluded.ahead`,
		r.PeerID, r.State, r.Detail, r.LastOKAt, r.PeerSeq, r.OurSeq, r.Behind, r.Ahead)
	if err != nil {
		if _, found, _ := db.GetPeer(r.PeerID); !found {
			return nil
		}
		return dbErr("store notes sync", err)
	}
	return nil
}

// DeleteNoteSync drops the bookkeeping for a peer (it was unpaired).
func (db *DB) DeleteNoteSync(peerID string) error {
	if _, err := db.sql.Exec(`DELETE FROM note_peer_state WHERE peer_id = ?`, peerID); err != nil {
		return dbErr("delete notes sync", err)
	}
	return nil
}
