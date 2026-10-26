package store

import (
	"database/sql"
	"errors"
	"time"

	"nightclip/core/proto"
)

// OutboxRow is one queued event for one peer. Envelope is the canonical
// envelope JSON in clear; store seals it with AAD bound to event_id.
type OutboxRow struct {
	EventID   string
	PeerID    string
	Sequence  int64
	Kind      string
	Envelope  []byte
	CreatedAt int64
	ExpiresAt int64
	Attempts  int
	State     string // queued | sent | acked | expired
	AckedAt   int64
	AckResult string
}

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// InsertOutbox queues an event. Sequence allocation is the caller's job, and
// (peer_id, sequence) is unique so a bug cannot silently duplicate a sequence.
// Prefer EnqueueOutbox, which allocates the sequence atomically.
func (db *DB) InsertOutbox(r OutboxRow) error { return db.insertOutbox(db.sql, r) }

func (db *DB) insertOutbox(ex execer, r OutboxRow) error {
	if r.State == "" {
		r.State = "queued"
	}
	if r.CreatedAt == 0 {
		r.CreatedAt = time.Now().Unix()
	}
	sealed, err := db.seal(r.Envelope, rowAAD("outbox", "envelope_enc", r.EventID))
	if err != nil {
		return proto.Errorf(proto.CodeInternal, "seal outbox envelope: %v", err)
	}
	_, err = ex.Exec(`
		INSERT INTO outbox (event_id, peer_id, sequence, kind, envelope_enc,
		                    created_at, expires_at, state, attempts, acked_at, ack_result)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.EventID, r.PeerID, r.Sequence, r.Kind, sealed,
		r.CreatedAt, r.ExpiresAt, r.State, r.Attempts, nullableTime(r.AckedAt), nullableString(r.AckResult))
	if err != nil {
		return dbErr("queue outbox event", err)
	}
	return nil
}

// EnqueueOutbox allocates the next sequence of the link to peerID, lets build
// produce the row for it (the envelope usually embeds the sequence) and stores
// the row, all in one transaction: concurrent callers never share a sequence,
// and a failing build or insert consumes none.
func (db *DB) EnqueueOutbox(peerID string, build func(seq int64) (OutboxRow, error)) (OutboxRow, error) {
	tx, err := db.sql.Begin()
	if err != nil {
		return OutboxRow{}, dbErr("begin enqueue", err)
	}
	defer func() { _ = tx.Rollback() }()

	seq := int64(1)
	err = tx.QueryRow(`SELECT next_seq_out FROM links WHERE peer_id = ?`, peerID).Scan(&seq)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return OutboxRow{}, dbErr("read link", err)
	}
	row, err := build(seq)
	if err != nil {
		return OutboxRow{}, err
	}
	row.PeerID, row.Sequence = peerID, seq
	if row.CreatedAt == 0 {
		row.CreatedAt = time.Now().Unix()
	}
	if err := db.insertOutbox(tx, row); err != nil {
		return OutboxRow{}, err
	}
	if _, err := tx.Exec(`
		INSERT INTO links (peer_id, next_seq_out, recv_cursor, updated_at) VALUES (?, ?, 0, ?)
		ON CONFLICT(peer_id) DO UPDATE SET next_seq_out = excluded.next_seq_out, updated_at = excluded.updated_at`,
		peerID, seq+1, row.CreatedAt); err != nil {
		return OutboxRow{}, dbErr("advance link", err)
	}
	if err := tx.Commit(); err != nil {
		return OutboxRow{}, dbErr("commit enqueue", err)
	}
	if row.State == "" {
		row.State = "queued"
	}
	return row, nil
}

// CountPendingOutbox returns how many events for peerID are queued or sent but
// not yet acknowledged.
func (db *DB) CountPendingOutbox(peerID string) (int, error) {
	var n int
	err := db.sql.QueryRow(
		`SELECT count(*) FROM outbox WHERE peer_id = ? AND state IN ('queued', 'sent')`, peerID).Scan(&n)
	if err != nil {
		return 0, dbErr("count outbox", err)
	}
	return n, nil
}

// PurgeOutbox deletes acknowledged and expired rows that finished before
// olderThan (unix seconds), returning how many were removed. The sequence
// gaps this leaves are covered by skip frames.
func (db *DB) PurgeOutbox(olderThan int64) (int64, error) {
	res, err := db.sql.Exec(`
		DELETE FROM outbox
		WHERE state IN ('acked', 'expired') AND COALESCE(acked_at, expires_at) < ?`, olderThan)
	if err != nil {
		return 0, dbErr("purge outbox", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, dbErr("purge outbox", err)
	}
	return n, nil
}

// ListOutbox returns this peer's queued rows with sequence > afterSeq, oldest
// first. A limit <= 0 means no limit.
func (db *DB) ListOutbox(peerID string, afterSeq int64, limit int) ([]OutboxRow, error) {
	if limit <= 0 {
		limit = -1
	}
	rows, err := db.sql.Query(`
		SELECT event_id, peer_id, sequence, kind, envelope_enc,
		       created_at, expires_at, state, attempts, acked_at, ack_result
		FROM outbox
		WHERE peer_id = ? AND sequence > ?
		ORDER BY sequence
		LIMIT ?`, peerID, afterSeq, limit)
	if err != nil {
		return nil, dbErr("list outbox", err)
	}
	defer rows.Close()

	var out []OutboxRow
	for rows.Next() {
		var (
			r         OutboxRow
			sealed    []byte
			ackedAt   sql.NullInt64
			ackResult sql.NullString
		)
		if err := rows.Scan(&r.EventID, &r.PeerID, &r.Sequence, &r.Kind, &sealed,
			&r.CreatedAt, &r.ExpiresAt, &r.State, &r.Attempts, &ackedAt, &ackResult); err != nil {
			return nil, dbErr("list outbox", err)
		}
		r.Envelope, err = db.unseal(sealed, rowAAD("outbox", "envelope_enc", r.EventID))
		if err != nil {
			return nil, proto.Errorf(proto.CodeDBUnavailable,
				"cannot decrypt outbox event %s (wrong key or corruption)", r.EventID)
		}
		r.AckedAt, r.AckResult = ackedAt.Int64, ackResult.String
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr("list outbox", err)
	}
	return out, nil
}

// MarkOutboxSent records a delivery attempt. A row that was already sent is
// counted again (a resend after reconnect); acked and expired rows never move
// back to sent.
func (db *DB) MarkOutboxSent(eventID string) error {
	_, err := db.sql.Exec(`
		UPDATE outbox SET state = 'sent', attempts = attempts + 1
		WHERE event_id = ? AND state IN ('queued', 'sent')`, eventID)
	if err != nil {
		return dbErr("mark outbox sent", err)
	}
	return nil
}

// MarkOutboxAcked records the peer's ack.
func (db *DB) MarkOutboxAcked(eventID, result string, at int64) error {
	_, err := db.sql.Exec(`
		UPDATE outbox SET state = 'acked', acked_at = ?, ack_result = ?
		WHERE event_id = ?`, at, result, eventID)
	if err != nil {
		return dbErr("mark outbox acked", err)
	}
	return nil
}

// ExpireOutbox marks every undelivered row past its expiry, returning how many
// rows changed. The sequence gap left behind is what the skip frame covers.
func (db *DB) ExpireOutbox(now int64) (int64, error) {
	res, err := db.sql.Exec(`
		UPDATE outbox SET state = 'expired'
		WHERE expires_at <= ? AND state IN ('queued', 'sent')`, now)
	if err != nil {
		return 0, dbErr("expire outbox", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, dbErr("expire outbox", err)
	}
	return n, nil
}

// GetOutbox returns one queued row; found is false when it is unknown.
func (db *DB) GetOutbox(eventID string) (OutboxRow, bool, error) {
	var (
		r         OutboxRow
		sealed    []byte
		ackedAt   sql.NullInt64
		ackResult sql.NullString
	)
	err := db.sql.QueryRow(`
		SELECT event_id, peer_id, sequence, kind, envelope_enc,
		       created_at, expires_at, state, attempts, acked_at, ack_result
		FROM outbox WHERE event_id = ?`, eventID).
		Scan(&r.EventID, &r.PeerID, &r.Sequence, &r.Kind, &sealed,
			&r.CreatedAt, &r.ExpiresAt, &r.State, &r.Attempts, &ackedAt, &ackResult)
	if errors.Is(err, sql.ErrNoRows) {
		return OutboxRow{}, false, nil
	}
	if err != nil {
		return OutboxRow{}, false, dbErr("read outbox", err)
	}
	r.Envelope, err = db.unseal(sealed, rowAAD("outbox", "envelope_enc", r.EventID))
	if err != nil {
		return OutboxRow{}, false, proto.Errorf(proto.CodeDBUnavailable,
			"cannot decrypt outbox event %s (wrong key or corruption)", r.EventID)
	}
	r.AckedAt, r.AckResult = ackedAt.Int64, ackResult.String
	return r, true, nil
}

func nullableTime(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
