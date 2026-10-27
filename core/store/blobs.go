package store

import (
	"database/sql"
	"errors"

	"nightclip/core/proto"
)

// NoteBlob is one attachment of the notes: a file, identified by the SHA-256 of its bytes.
type NoteBlob struct {
	BlobID    string // lower-case hex SHA-256 of Data
	Size      int64
	Mime      string
	Name      string
	Data      []byte // only filled by GetBlob with withData
	CreatedAt int64  // unix milliseconds
}

// PutBlob stores an attachment (no-op when it is already stored: same id, same bytes).
func (t *NotesTx) PutBlob(b NoteBlob) (inserted bool, err error) {
	var one int
	switch err := t.tx.QueryRow(`SELECT 1 FROM note_blobs WHERE blob_id = ?`, b.BlobID).Scan(&one); {
	case err == nil:
		return false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, dbErr("read note blob", err)
	}
	nameEnc, err := t.db.seal([]byte(b.Name), rowAAD("note_blobs", "name_enc", b.BlobID))
	if err != nil {
		return false, dbErr("store note blob", err)
	}
	dataEnc, err := t.db.seal(b.Data, rowAAD("note_blobs", "data_enc", b.BlobID))
	if err != nil {
		return false, dbErr("store note blob", err)
	}
	if _, err := t.tx.Exec(`INSERT INTO note_blobs (blob_id, size, mime, name_enc, data_enc, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		b.BlobID, b.Size, b.Mime, nameEnc, dataEnc, b.CreatedAt); err != nil {
		return false, dbErr("store note blob", err)
	}
	return true, nil
}

// GetBlob returns an attachment, with its bytes when withData is set.
func (t *NotesTx) GetBlob(blobID string, withData bool) (NoteBlob, bool, error) {
	var b NoteBlob
	var nameEnc, dataEnc []byte
	q := `SELECT blob_id, size, mime, name_enc, created_at, NULL FROM note_blobs WHERE blob_id = ?`
	if withData {
		q = `SELECT blob_id, size, mime, name_enc, created_at, data_enc FROM note_blobs WHERE blob_id = ?`
	}
	err := t.tx.QueryRow(q, blobID).Scan(&b.BlobID, &b.Size, &b.Mime, &nameEnc, &b.CreatedAt, &dataEnc)
	if errors.Is(err, sql.ErrNoRows) {
		return NoteBlob{}, false, nil
	}
	if err != nil {
		return NoteBlob{}, false, dbErr("read note blob", err)
	}
	name, err := t.db.unseal(nameEnc, rowAAD("note_blobs", "name_enc", blobID))
	if err != nil {
		return NoteBlob{}, false, proto.Errorf(proto.CodeKeyInvalid, "cannot open note attachment")
	}
	b.Name = string(name)
	if withData {
		if b.Data, err = t.db.unseal(dataEnc, rowAAD("note_blobs", "data_enc", blobID)); err != nil {
			return NoteBlob{}, false, proto.Errorf(proto.CodeKeyInvalid, "cannot open note attachment")
		}
	}
	return b, true, nil
}

// StoredBlobs returns which of the given blob ids are stored.
func (t *NotesTx) StoredBlobs(ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, id := range ids {
		var one int
		err := t.tx.QueryRow(`SELECT 1 FROM note_blobs WHERE blob_id = ?`, id).Scan(&one)
		if err == nil {
			out[id] = true
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, dbErr("read note blob", err)
		}
	}
	return out, nil
}

// SetBlobRefs replaces the list of attachments a note's current text refers to.
func (t *NotesTx) SetBlobRefs(noteID string, ids []string) error {
	if _, err := t.tx.Exec(`DELETE FROM note_blob_refs WHERE note_id = ?`, noteID); err != nil {
		return dbErr("store note blob refs", err)
	}
	for _, id := range ids {
		if _, err := t.tx.Exec(`INSERT OR IGNORE INTO note_blob_refs (note_id, blob_id) VALUES (?, ?)`, noteID, id); err != nil {
			return dbErr("store note blob refs", err)
		}
	}
	return nil
}

// ReferencedBlobs returns every attachment id some note's current text refers to, sorted.
func (t *NotesTx) ReferencedBlobs() ([]string, error) {
	rows, err := t.tx.Query(`SELECT DISTINCT blob_id FROM note_blob_refs ORDER BY blob_id`)
	if err != nil {
		return nil, dbErr("read note blob refs", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, dbErr("read note blob refs", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// BlobReferenced reports whether some note's current text refers to the attachment.
func (t *NotesTx) BlobReferenced(blobID string) (bool, error) {
	var one int
	err := t.tx.QueryRow(`SELECT 1 FROM note_blob_refs WHERE blob_id = ? LIMIT 1`, blobID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, dbErr("read note blob refs", err)
	}
	return true, nil
}

// BlobInfos lists the stored attachments without their bytes, oldest first.
func (t *NotesTx) BlobInfos() ([]NoteBlob, error) {
	rows, err := t.tx.Query(`SELECT blob_id, size, mime, created_at FROM note_blobs ORDER BY created_at, blob_id`)
	if err != nil {
		return nil, dbErr("list note blobs", err)
	}
	defer rows.Close()
	var out []NoteBlob
	for rows.Next() {
		var b NoteBlob
		if err := rows.Scan(&b.BlobID, &b.Size, &b.Mime, &b.CreatedAt); err != nil {
			return nil, dbErr("list note blobs", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteBlob removes an attachment.
func (t *NotesTx) DeleteBlob(blobID string) error {
	if _, err := t.tx.Exec(`DELETE FROM note_blobs WHERE blob_id = ?`, blobID); err != nil {
		return dbErr("delete note blob", err)
	}
	return nil
}

// Revisions streams the text of every stored revision to fn (oldest first), for jobs that need to look
// through all history. fn returns false to stop.
func (t *NotesTx) Revisions(fn func(NoteRev) bool) error {
	rows, err := t.tx.Query(`SELECT rev_id FROM note_revs ORDER BY arrival`)
	if err != nil {
		return dbErr("read note revisions", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return dbErr("read note revisions", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		r, found, err := t.GetRev(id)
		if err != nil {
			return err
		}
		if found && !fn(r) {
			return nil
		}
	}
	return nil
}
