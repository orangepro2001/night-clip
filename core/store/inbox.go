package store

import (
	"database/sql"
	"errors"
	"time"

	"nightclip/core/proto"
)

// InboxRow is one received event. Payload is the event payload in clear;
// store seals it with AAD bound to event_id.
type InboxRow struct {
	EventID    string
	PeerID     string
	Sequence   int64
	Kind       string
	Payload    []byte
	ReceivedAt int64
	ExpiresAt  int64
	State      string // received | applied | ignored | expired | error
	AppliedAt  int64
	Sensitive  bool
}

// InsertInbox stores a received event and reports whether it was new. A repeat
// of an already stored event_id returns inserted=false, which is the dedupe
// point for the receive path. A duplicate (peer_id, sequence) with a different
// event_id is a protocol error and surfaces as an ordinary error.
func (db *DB) InsertInbox(r InboxRow) (inserted bool, err error) {
	if r.State == "" {
		r.State = "received"
	}
	if r.ReceivedAt == 0 {
		r.ReceivedAt = time.Now().Unix()
	}
	sealed, err := db.seal(r.Payload, rowAAD("inbox", "payload_enc", r.EventID))
	if err != nil {
		return false, proto.Errorf(proto.CodeInternal, "seal inbox payload: %v", err)
	}
	res, err := db.sql.Exec(`
		INSERT INTO inbox (event_id, peer_id, sequence, kind, payload_enc,
		                   received_at, expires_at, state, applied_at, sensitive)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_id) DO NOTHING`,
		r.EventID, r.PeerID, r.Sequence, r.Kind, sealed,
		r.ReceivedAt, r.ExpiresAt, r.State, nullableTime(r.AppliedAt), r.Sensitive)
	if err != nil {
		return false, dbErr("store inbox event", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, dbErr("store inbox event", err)
	}
	return n == 1, nil
}

// HasInbox reports whether eventID has already been received.
func (db *DB) HasInbox(eventID string) (bool, error) {
	var one int
	err := db.sql.QueryRow(`SELECT 1 FROM inbox WHERE event_id = ?`, eventID).Scan(&one)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, dbErr("read inbox", err)
	}
}

// GetInbox returns one received event; found is false when it is unknown.
func (db *DB) GetInbox(eventID string) (InboxRow, bool, error) {
	var (
		r         InboxRow
		sealed    []byte
		appliedAt sql.NullInt64
	)
	err := db.sql.QueryRow(`
		SELECT event_id, peer_id, sequence, kind, payload_enc, received_at, expires_at, state, applied_at, sensitive
		FROM inbox WHERE event_id = ?`, eventID).
		Scan(&r.EventID, &r.PeerID, &r.Sequence, &r.Kind, &sealed,
			&r.ReceivedAt, &r.ExpiresAt, &r.State, &appliedAt, &r.Sensitive)
	if errors.Is(err, sql.ErrNoRows) {
		return InboxRow{}, false, nil
	}
	if err != nil {
		return InboxRow{}, false, dbErr("read inbox", err)
	}
	r.Payload, err = db.unseal(sealed, rowAAD("inbox", "payload_enc", r.EventID))
	if err != nil {
		return InboxRow{}, false, proto.Errorf(proto.CodeDBUnavailable,
			"cannot decrypt inbox event %s (wrong key or corruption)", r.EventID)
	}
	r.AppliedAt = appliedAt.Int64
	return r, true, nil
}

// ListInbox returns the newest received events first. A limit <= 0 means no
// limit.
func (db *DB) ListInbox(limit int) ([]InboxRow, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := db.sql.Query(`
		SELECT event_id, peer_id, sequence, kind, payload_enc, received_at, expires_at, state, applied_at, sensitive
		FROM inbox ORDER BY received_at DESC, rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, dbErr("list inbox", err)
	}
	defer rows.Close()

	var out []InboxRow
	for rows.Next() {
		var (
			r         InboxRow
			sealed    []byte
			appliedAt sql.NullInt64
		)
		if err := rows.Scan(&r.EventID, &r.PeerID, &r.Sequence, &r.Kind, &sealed,
			&r.ReceivedAt, &r.ExpiresAt, &r.State, &appliedAt, &r.Sensitive); err != nil {
			return nil, dbErr("list inbox", err)
		}
		r.Payload, err = db.unseal(sealed, rowAAD("inbox", "payload_enc", r.EventID))
		if err != nil {
			return nil, proto.Errorf(proto.CodeDBUnavailable,
				"cannot decrypt inbox event %s (wrong key or corruption)", r.EventID)
		}
		r.AppliedAt = appliedAt.Int64
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr("list inbox", err)
	}
	return out, nil
}

// SetInboxState moves a received event through its lifecycle. appliedAt is
// stored when non-zero.
func (db *DB) SetInboxState(eventID, state string, appliedAt int64) error {
	_, err := db.sql.Exec(`
		UPDATE inbox SET state = ?, applied_at = COALESCE(?, applied_at)
		WHERE event_id = ?`, state, nullableTime(appliedAt), eventID)
	if err != nil {
		return dbErr("update inbox state", err)
	}
	return nil
}

// ExpireInbox marks unapplied rows past their expiry, returning how many rows
// changed.
func (db *DB) ExpireInbox(now int64) (int64, error) {
	res, err := db.sql.Exec(`
		UPDATE inbox SET state = 'expired'
		WHERE expires_at <= ? AND state = 'received'`, now)
	if err != nil {
		return 0, dbErr("expire inbox", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, dbErr("expire inbox", err)
	}
	return n, nil
}

// PruneInbox keeps the newest `keep` rows (by received_at) and deletes the
// rest, returning how many were removed. keep <= 0 deletes nothing.
func (db *DB) PruneInbox(keep int) (int64, error) {
	if keep <= 0 {
		return 0, nil
	}
	res, err := db.sql.Exec(`
		DELETE FROM inbox WHERE event_id IN (
			SELECT event_id FROM inbox ORDER BY received_at DESC, rowid DESC LIMIT -1 OFFSET ?)`, keep)
	if err != nil {
		return 0, dbErr("prune inbox", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, dbErr("prune inbox", err)
	}
	return n, nil
}

// ClearInbox deletes every received event.
func (db *DB) ClearInbox() error {
	if _, err := db.sql.Exec(`DELETE FROM inbox`); err != nil {
		return dbErr("clear inbox", err)
	}
	return nil
}
