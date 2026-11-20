package store

import (
	"database/sql"
	"errors"
	"time"

	"moonpassword/core/proto"
)

// MaxSecretBytes bounds one sealed value.
const MaxSecretBytes = 64 << 10

// GetSecret opens a sealed value; found is false when the name is unset. A value that does not
// open under the current key answers KEY_INVALID (wrong database key, or a tampered row).
func (db *DB) GetSecret(name string) (value []byte, found bool, err error) {
	var blob []byte
	err = db.sql.QueryRow(`SELECT value_enc FROM secrets WHERE name = ?`, name).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, dbErr("read secret", err)
	}
	plain, err := db.unseal(blob, rowAAD("secrets", "value_enc", name))
	if err != nil {
		return nil, false, proto.Errorf(proto.CodeKeyInvalid, "a stored secret cannot be opened with this database key")
	}
	return plain, true, nil
}

// SetSecret seals value under the database key and stores it, replacing any earlier value.
func (db *DB) SetSecret(name string, value []byte) error {
	if name == "" {
		return proto.Errorf(proto.CodeInvalid, "secret name is required")
	}
	if len(value) > MaxSecretBytes {
		return proto.Errorf(proto.CodeTooLarge, "secret %q is larger than %d bytes", name, MaxSecretBytes)
	}
	blob, err := db.seal(value, rowAAD("secrets", "value_enc", name))
	if err != nil {
		return err
	}
	_, err = db.sql.Exec(`
		INSERT INTO secrets (name, value_enc, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET value_enc = excluded.value_enc, updated_at = excluded.updated_at`,
		name, blob, time.Now().UnixMilli())
	if err != nil {
		return dbErr("store secret", err)
	}
	return nil
}

// DeleteSecret removes a sealed value; deleting an absent name is not an error.
func (db *DB) DeleteSecret(name string) error {
	if _, err := db.sql.Exec(`DELETE FROM secrets WHERE name = ?`, name); err != nil {
		return dbErr("delete secret", err)
	}
	return nil
}
