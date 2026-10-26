package store

import (
	"database/sql"
	"errors"
	"time"
)

// Link is the per-peer sequence state (docs/architecture.md §4.4).
type Link struct {
	PeerID     string
	NextSeqOut int64 // next sequence to assign for events sent to this peer
	RecvCursor int64 // highest contiguous sequence received from this peer
	UpdatedAt  int64
}

// GetLink returns the link state for peerID; found is false when no event has
// ever been exchanged. Callers start from {NextSeqOut: 1, RecvCursor: 0}.
func (db *DB) GetLink(peerID string) (Link, bool, error) {
	var l Link
	err := db.sql.QueryRow(
		`SELECT peer_id, next_seq_out, recv_cursor, updated_at FROM links WHERE peer_id = ?`,
		peerID).Scan(&l.PeerID, &l.NextSeqOut, &l.RecvCursor, &l.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Link{PeerID: peerID, NextSeqOut: 1}, false, nil
	}
	if err != nil {
		return Link{}, false, dbErr("read link", err)
	}
	return l, true, nil
}

// SetLink upserts the link state; UpdatedAt defaults to now.
func (db *DB) SetLink(l Link) error {
	if l.UpdatedAt == 0 {
		l.UpdatedAt = time.Now().Unix()
	}
	_, err := db.sql.Exec(`
		INSERT INTO links (peer_id, next_seq_out, recv_cursor, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			next_seq_out = excluded.next_seq_out,
			recv_cursor = excluded.recv_cursor,
			updated_at = excluded.updated_at`,
		l.PeerID, l.NextSeqOut, l.RecvCursor, l.UpdatedAt)
	if err != nil {
		return dbErr("store link", err)
	}
	return nil
}
