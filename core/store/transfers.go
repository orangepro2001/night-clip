package store

import (
	"database/sql"
	"errors"
)

// Transfer is one file moving between this device and a peer
// (docs/architecture.md §7). Direction is "in" or "out"; Mode is "pull" (the
// receiver fetches from the sender's server) or "push" (the sender uploads to
// the receiver's server).
type Transfer struct {
	ID        string
	PeerID    string
	Direction string
	Name      string
	Size      int64
	SHA256    string
	Path      string // out: the source file ("" when the core holds an open fd); in: the final file once done
	Offset    int64
	State     string
	Error     string
	CreatedAt int64
	ExpiresAt int64
	Mode      string
	MimeType  string
	SaveDir   string
}

const transferCols = `transfer_id, peer_id, direction, name, size, sha256, path, "offset", state, error,
	created_at, expires_at, mode, mime_type, save_dir`

func scanTransfer(row rowScanner) (Transfer, error) {
	var t Transfer
	err := row.Scan(&t.ID, &t.PeerID, &t.Direction, &t.Name, &t.Size, &t.SHA256, &t.Path, &t.Offset, &t.State,
		&t.Error, &t.CreatedAt, &t.ExpiresAt, &t.Mode, &t.MimeType, &t.SaveDir)
	return t, err
}

// PutTransfer inserts or replaces a transfer row.
func (db *DB) PutTransfer(t Transfer) error {
	_, err := db.sql.Exec(`
		INSERT INTO transfers (`+transferCols+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(transfer_id) DO UPDATE SET
			peer_id = excluded.peer_id, direction = excluded.direction, name = excluded.name,
			size = excluded.size, sha256 = excluded.sha256, path = excluded.path, "offset" = excluded."offset",
			state = excluded.state, error = excluded.error, created_at = excluded.created_at,
			expires_at = excluded.expires_at, mode = excluded.mode, mime_type = excluded.mime_type,
			save_dir = excluded.save_dir`,
		t.ID, t.PeerID, t.Direction, t.Name, t.Size, t.SHA256, t.Path, t.Offset, t.State, t.Error,
		t.CreatedAt, t.ExpiresAt, t.Mode, t.MimeType, t.SaveDir)
	if err != nil {
		return dbErr("store transfer", err)
	}
	return nil
}

// GetTransfer returns one transfer; found is false when unknown.
func (db *DB) GetTransfer(id string) (Transfer, bool, error) {
	t, err := scanTransfer(db.sql.QueryRow(`SELECT `+transferCols+` FROM transfers WHERE transfer_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Transfer{}, false, nil
	}
	if err != nil {
		return Transfer{}, false, dbErr("read transfer", err)
	}
	return t, true, nil
}

// ListTransfers returns transfers newest first. limit <= 0 means all.
func (db *DB) ListTransfers(limit int) ([]Transfer, error) {
	if limit <= 0 {
		limit = -1
	}
	return db.queryTransfers(`SELECT `+transferCols+` FROM transfers ORDER BY created_at DESC, transfer_id LIMIT ?`, limit)
}

// ListTransfersInStates returns transfers whose state is one of states.
func (db *DB) ListTransfersInStates(states ...string) ([]Transfer, error) {
	if len(states) == 0 {
		return nil, nil
	}
	q := `SELECT ` + transferCols + ` FROM transfers WHERE state IN (?` + repeatComma(len(states)-1) + `) ORDER BY created_at`
	args := make([]any, len(states))
	for i, s := range states {
		args[i] = s
	}
	return db.queryTransfers(q, args...)
}

func repeatComma(n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += ", ?"
	}
	return out
}

func (db *DB) queryTransfers(q string, args ...any) ([]Transfer, error) {
	rows, err := db.sql.Query(q, args...)
	if err != nil {
		return nil, dbErr("list transfers", err)
	}
	defer rows.Close()
	var out []Transfer
	for rows.Next() {
		t, err := scanTransfer(rows)
		if err != nil {
			return nil, dbErr("list transfers", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr("list transfers", err)
	}
	return out, nil
}

// SetTransferOffset records progress.
func (db *DB) SetTransferOffset(id string, offset int64) error {
	if _, err := db.sql.Exec(`UPDATE transfers SET "offset" = ? WHERE transfer_id = ?`, offset, id); err != nil {
		return dbErr("update transfer offset", err)
	}
	return nil
}

// SetTransferState moves a transfer to a new state, with an error text for
// failed ones.
func (db *DB) SetTransferState(id, state, errText string) error {
	if _, err := db.sql.Exec(`UPDATE transfers SET state = ?, error = ? WHERE transfer_id = ?`, state, errText, id); err != nil {
		return dbErr("update transfer state", err)
	}
	return nil
}

// SetTransferPath records the final location (in) or source (out).
func (db *DB) SetTransferPath(id, path string) error {
	if _, err := db.sql.Exec(`UPDATE transfers SET path = ? WHERE transfer_id = ?`, path, id); err != nil {
		return dbErr("update transfer path", err)
	}
	return nil
}

// ExpireTransfers marks transfers still in flight (offered, accepted,
// transferring) past their expiry, returning them so the caller can clean up.
func (db *DB) ExpireTransfers(now int64) ([]Transfer, error) {
	rows, err := db.queryTransfers(`SELECT `+transferCols+` FROM transfers
		WHERE state IN ('hashing', 'offered', 'accepted', 'transferring') AND expires_at <= ?`, now)
	if err != nil {
		return nil, err
	}
	for _, t := range rows {
		if err := db.SetTransferState(t.ID, "expired", ""); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

// DeleteTransfer removes a row.
func (db *DB) DeleteTransfer(id string) error {
	if _, err := db.sql.Exec(`DELETE FROM transfers WHERE transfer_id = ?`, id); err != nil {
		return dbErr("delete transfer", err)
	}
	return nil
}
