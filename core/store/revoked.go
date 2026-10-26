package store

import (
	"database/sql"
	"errors"
)

// AddRevokedFP permanently blocks a certificate fingerprint. Re-adding an
// already revoked fingerprint is a no-op.
func (db *DB) AddRevokedFP(certFP string, at int64) error {
	_, err := db.sql.Exec(`
		INSERT INTO revoked_fps (cert_fp, revoked_at) VALUES (?, ?)
		ON CONFLICT(cert_fp) DO NOTHING`, certFP, at)
	if err != nil {
		return dbErr("revoke fingerprint", err)
	}
	return nil
}

// IsRevoked reports whether certFP has been revoked.
func (db *DB) IsRevoked(certFP string) (bool, error) {
	var one int
	err := db.sql.QueryRow(`SELECT 1 FROM revoked_fps WHERE cert_fp = ?`, certFP).Scan(&one)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, dbErr("read revoked fingerprint", err)
	}
}
