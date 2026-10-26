package store

import (
	"database/sql"
	"errors"
)

// ClipState is the echo-suppression state shared by the clipboard layer
// (docs/architecture.md §6).
type ClipState struct {
	LastAppliedEventID string
	LastAppliedHash    string
	LastLocalSentHash  string
	SuppressUntil      int64
}

// GetClipState returns the single clip_state row; a fresh database yields the
// zero value.
func (db *DB) GetClipState() (ClipState, error) {
	var s ClipState
	err := db.sql.QueryRow(`
		SELECT last_applied_event_id, last_applied_hash, last_local_sent_hash, suppress_until
		FROM clip_state WHERE id = 1`).
		Scan(&s.LastAppliedEventID, &s.LastAppliedHash, &s.LastLocalSentHash, &s.SuppressUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return ClipState{}, nil
	}
	if err != nil {
		return ClipState{}, dbErr("read clip state", err)
	}
	return s, nil
}

// SetClipState writes the single clip_state row.
func (db *DB) SetClipState(s ClipState) error {
	_, err := db.sql.Exec(`
		INSERT INTO clip_state (id, last_applied_event_id, last_applied_hash, last_local_sent_hash, suppress_until)
		VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			last_applied_event_id = excluded.last_applied_event_id,
			last_applied_hash = excluded.last_applied_hash,
			last_local_sent_hash = excluded.last_local_sent_hash,
			suppress_until = excluded.suppress_until`,
		s.LastAppliedEventID, s.LastAppliedHash, s.LastLocalSentHash, s.SuppressUntil)
	if err != nil {
		return dbErr("store clip state", err)
	}
	return nil
}
