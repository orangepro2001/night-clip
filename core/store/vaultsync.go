package store

import (
	"database/sql"
	"errors"
)

// VaultSyncRow is the sync bookkeeping for one peer (docs/plan-vault.md §5.2).
type VaultSyncRow struct {
	PeerID   string
	SeenHash string // sha256 of the peer's vault file the last time we processed it
	SentHash string // sha256 of our vault file the last time we sent it to the peer
	LastOKAt int64
	State    string // idle | pending_unlock | needs_password | review | error
	Detail   string
}

// GetVaultSync returns the row for a peer; the zero value with found=false when there is none.
func (db *DB) GetVaultSync(peerID string) (VaultSyncRow, bool, error) {
	var r VaultSyncRow
	err := db.sql.QueryRow(`SELECT peer_id, seen_hash, sent_hash, last_ok_at, state, detail FROM vault_sync WHERE peer_id = ?`, peerID).
		Scan(&r.PeerID, &r.SeenHash, &r.SentHash, &r.LastOKAt, &r.State, &r.Detail)
	if errors.Is(err, sql.ErrNoRows) {
		return VaultSyncRow{PeerID: peerID, State: "idle"}, false, nil
	}
	if err != nil {
		return VaultSyncRow{}, false, dbErr("read vault sync", err)
	}
	return r, true, nil
}

// PutVaultSync stores the row.
func (db *DB) PutVaultSync(r VaultSyncRow) error {
	if r.State == "" {
		r.State = "idle"
	}
	_, err := db.sql.Exec(`
		INSERT INTO vault_sync (peer_id, seen_hash, sent_hash, last_ok_at, state, detail) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET seen_hash = excluded.seen_hash, sent_hash = excluded.sent_hash,
			last_ok_at = excluded.last_ok_at, state = excluded.state, detail = excluded.detail`,
		r.PeerID, r.SeenHash, r.SentHash, r.LastOKAt, r.State, r.Detail)
	if err != nil {
		return dbErr("store vault sync", err)
	}
	return nil
}
