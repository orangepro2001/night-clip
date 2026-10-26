package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"nightclip/core/proto"
)

// Peer is one paired device (docs/architecture.md §5).
type Peer struct {
	DeviceID     string
	Name         string
	Platform     string
	CertFP       string
	TailnetAddrs []string
	Port         int
	Role         string // "auto" | "dialer" | "listener" (dial-direction override; peer layer owns the rule)
	State        string // "paired" | "revoked"
	ApplyMode    string // "notify" | "manual" | "auto"
	PairedAt     int64
	LastSeenAt   int64
}

// PutPeer inserts or replaces a peer row keyed by device_id.
func (db *DB) PutPeer(p Peer) error {
	addrs := p.TailnetAddrs
	if addrs == nil {
		addrs = []string{}
	}
	raw, err := json.Marshal(addrs)
	if err != nil {
		return proto.Errorf(proto.CodeInvalid, "tailnet addresses: %v", err)
	}
	_, err = db.sql.Exec(`
		INSERT INTO peers (device_id, name, platform, cert_fp, tailnet_addrs_json, port,
		                   role, state, apply_mode, paired_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(device_id) DO UPDATE SET
			name = excluded.name,
			platform = excluded.platform,
			cert_fp = excluded.cert_fp,
			tailnet_addrs_json = excluded.tailnet_addrs_json,
			port = excluded.port,
			role = excluded.role,
			state = excluded.state,
			apply_mode = excluded.apply_mode,
			paired_at = excluded.paired_at,
			last_seen_at = excluded.last_seen_at`,
		p.DeviceID, p.Name, p.Platform, p.CertFP, string(raw), p.Port,
		p.Role, p.State, p.ApplyMode, p.PairedAt, p.LastSeenAt)
	if err != nil {
		return dbErr("store peer", err)
	}
	return nil
}

// GetPeer returns one peer; found is false when it is unknown.
func (db *DB) GetPeer(deviceID string) (Peer, bool, error) {
	row := db.sql.QueryRow(`
		SELECT device_id, name, platform, cert_fp, tailnet_addrs_json, port,
		       role, state, apply_mode, paired_at, last_seen_at
		FROM peers WHERE device_id = ?`, deviceID)
	p, err := scanPeer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Peer{}, false, nil
	}
	if err != nil {
		return Peer{}, false, dbErr("read peer", err)
	}
	return p, true, nil
}

// FindPeerByFingerprint returns the peer pinned to certFP. The TLS layer uses
// it to decide whether an incoming certificate is paired.
func (db *DB) FindPeerByFingerprint(certFP string) (Peer, bool, error) {
	row := db.sql.QueryRow(`
		SELECT device_id, name, platform, cert_fp, tailnet_addrs_json, port,
		       role, state, apply_mode, paired_at, last_seen_at
		FROM peers WHERE cert_fp = ?`, certFP)
	p, err := scanPeer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Peer{}, false, nil
	}
	if err != nil {
		return Peer{}, false, dbErr("read peer by fingerprint", err)
	}
	return p, true, nil
}

// ListPeers returns every peer, paired or revoked, ordered by name.
func (db *DB) ListPeers() ([]Peer, error) {
	rows, err := db.sql.Query(`
		SELECT device_id, name, platform, cert_fp, tailnet_addrs_json, port,
		       role, state, apply_mode, paired_at, last_seen_at
		FROM peers ORDER BY name, device_id`)
	if err != nil {
		return nil, dbErr("list peers", err)
	}
	defer rows.Close()
	var out []Peer
	for rows.Next() {
		p, err := scanPeer(rows)
		if err != nil {
			return nil, dbErr("list peers", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, dbErr("list peers", err)
	}
	return out, nil
}

// DeletePeer removes a peer and, by cascade, its link and outbox rows.
func (db *DB) DeletePeer(deviceID string) error {
	if _, err := db.sql.Exec(`DELETE FROM peers WHERE device_id = ?`, deviceID); err != nil {
		return dbErr("delete peer", err)
	}
	return nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPeer(row rowScanner) (Peer, error) {
	var (
		p       Peer
		addrsJS string
	)
	if err := row.Scan(&p.DeviceID, &p.Name, &p.Platform, &p.CertFP, &addrsJS, &p.Port,
		&p.Role, &p.State, &p.ApplyMode, &p.PairedAt, &p.LastSeenAt); err != nil {
		return Peer{}, err
	}
	if addrsJS != "" {
		if err := json.Unmarshal([]byte(addrsJS), &p.TailnetAddrs); err != nil {
			return Peer{}, err
		}
	}
	return p, nil
}

// TouchPeer records that the peer was just seen at addrs. An empty addrs keeps
// the stored addresses.
func (db *DB) TouchPeer(deviceID string, addrs []string, seenAt int64) error {
	if len(addrs) == 0 {
		_, err := db.sql.Exec(`UPDATE peers SET last_seen_at = ? WHERE device_id = ?`, seenAt, deviceID)
		if err != nil {
			return dbErr("touch peer", err)
		}
		return nil
	}
	raw, err := json.Marshal(addrs)
	if err != nil {
		return proto.Errorf(proto.CodeInvalid, "tailnet addresses: %v", err)
	}
	if _, err := db.sql.Exec(
		`UPDATE peers SET tailnet_addrs_json = ?, last_seen_at = ? WHERE device_id = ?`,
		string(raw), seenAt, deviceID); err != nil {
		return dbErr("touch peer", err)
	}
	return nil
}

// SetPeerState sets a peer's state ("paired" or "revoked").
func (db *DB) SetPeerState(deviceID, state string) error {
	if _, err := db.sql.Exec(`UPDATE peers SET state = ? WHERE device_id = ?`, state, deviceID); err != nil {
		return dbErr("set peer state", err)
	}
	return nil
}
