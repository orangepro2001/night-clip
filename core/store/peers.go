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
	// SendDisabled stops broadcasts from reaching this peer (it still receives
	// nothing from us, but what it sends us is handled as usual). Zero value =
	// enabled, so records built without the field keep working.
	SendDisabled bool
	// VaultSync lets this peer exchange the password vault file with us (docs/plan-vault.md §5).
	// On by default for every paired device (schema v6); it is an advanced per-device opt-out,
	// not a user onboarding step.
	VaultSync bool
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
		                   role, state, apply_mode, paired_at, last_seen_at, send_disabled, vault_sync)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
			last_seen_at = excluded.last_seen_at,
			send_disabled = excluded.send_disabled,
			vault_sync = excluded.vault_sync`,
		p.DeviceID, p.Name, p.Platform, p.CertFP, string(raw), p.Port,
		p.Role, p.State, p.ApplyMode, p.PairedAt, p.LastSeenAt, boolInt(p.SendDisabled), boolInt(p.VaultSync))
	if err != nil {
		return dbErr("store peer", err)
	}
	return nil
}

// GetPeer returns one peer; found is false when it is unknown.
func (db *DB) GetPeer(deviceID string) (Peer, bool, error) {
	row := db.sql.QueryRow(`
		SELECT device_id, name, platform, cert_fp, tailnet_addrs_json, port,
		       role, state, apply_mode, paired_at, last_seen_at, send_disabled, vault_sync
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
		       role, state, apply_mode, paired_at, last_seen_at, send_disabled, vault_sync
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
		       role, state, apply_mode, paired_at, last_seen_at, send_disabled, vault_sync
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
		sendOff int
		vsync   int
	)
	if err := row.Scan(&p.DeviceID, &p.Name, &p.Platform, &p.CertFP, &addrsJS, &p.Port,
		&p.Role, &p.State, &p.ApplyMode, &p.PairedAt, &p.LastSeenAt, &sendOff, &vsync); err != nil {
		return Peer{}, err
	}
	p.SendDisabled = sendOff != 0
	p.VaultSync = vsync != 0
	if addrsJS != "" {
		if err := json.Unmarshal([]byte(addrsJS), &p.TailnetAddrs); err != nil {
			return Peer{}, err
		}
	}
	return p, nil
}

// SetPeerPort records the port the peer was last reached on (it moves between
// the default and the fallback port when the default cannot be bound there).
func (db *DB) SetPeerPort(deviceID string, port int) error {
	if _, err := db.sql.Exec(`UPDATE peers SET port = ? WHERE device_id = ?`, port, deviceID); err != nil {
		return dbErr("set peer port", err)
	}
	return nil
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

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SetPeerVaultSync switches vault sync for one peer without rewriting the rest of its row (a
// read-modify-write of the whole record could undo a concurrent change such as an unpairing).
func (db *DB) SetPeerVaultSync(deviceID string, on bool) error {
	res, err := db.sql.Exec(`UPDATE peers SET vault_sync = ? WHERE device_id = ? AND state = 'paired'`, boolInt(on), deviceID)
	if err != nil {
		return dbErr("set vault sync", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return proto.Errorf(proto.CodeNotPaired, "no paired device %s", deviceID)
	}
	return nil
}
