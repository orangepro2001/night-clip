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

// InsertOutbox queues an event. Sequence allocation is the caller's job, and
// (peer_id, sequence) is unique so a bug cannot silently duplicate a sequence.
func (db *DB) InsertOutbox(r OutboxRow) error {
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
	_, err = db.sql.Exec(`
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

// MarkOutboxSent records a delivery attempt.
func (db *DB) MarkOutboxSent(eventID string) error {
	_, err := db.sql.Exec(`
		UPDATE outbox SET state = 'sent', attempts = attempts + 1
		WHERE event_id = ? AND state = 'queued'`, eventID)
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
