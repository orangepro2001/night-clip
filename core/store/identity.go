package store

import (
	"database/sql"
	"errors"
	"time"

	"nightclip/core/identity"
	"nightclip/core/proto"
)

// LoadOrCreateIdentity returns this device's long-term identity, creating and
// persisting one on first run. The Ed25519 private key is sealed with the
// database key; the certificate is stored as DER (public material).
//
// name and platform come from the shell config. A different platform means the
// data directory is being reused by the wrong app, which is a configuration
// error, not a silent rebrand.
func (db *DB) LoadOrCreateIdentity(name, platform string) (*identity.Identity, error) {
	const q = `SELECT device_id, name, platform, key_blob_enc, cert_der FROM identity LIMIT 1`

	var (
		deviceID, storedName, storedPlatform string
		blob, certDER                        []byte
	)
	err := db.sql.QueryRow(q).Scan(&deviceID, &storedName, &storedPlatform, &blob, &certDER)
	switch {
	case err == nil:
		return db.openIdentity(deviceID, storedName, storedPlatform, name, platform, blob, certDER)
	case !errors.Is(err, sql.ErrNoRows):
		return nil, dbErr("read identity", err)
	}

	id, err := identity.Generate(name)
	if err != nil {
		return nil, proto.Errorf(proto.CodeInternal, "generate identity: %v", err)
	}
	priv, err := id.PrivateKeyPKCS8()
	if err != nil {
		return nil, proto.Errorf(proto.CodeInternal, "encode identity key: %v", err)
	}
	blob, err = db.seal(priv, rowAAD("identity", "key_blob_enc", id.DeviceID))
	if err != nil {
		return nil, proto.Errorf(proto.CodeInternal, "seal identity key: %v", err)
	}
	_, err = db.sql.Exec(
		`INSERT INTO identity (device_id, name, platform, key_blob_enc, cert_der, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id.DeviceID, name, platform, blob, id.CertDER, time.Now().Unix())
	if err != nil {
		return nil, dbErr("store identity", err)
	}
	return id, nil
}

func (db *DB) openIdentity(deviceID, storedName, storedPlatform, name, platform string, blob, certDER []byte) (*identity.Identity, error) {
	privDER, err := db.unseal(blob, rowAAD("identity", "key_blob_enc", deviceID))
	if err != nil {
		return nil, proto.Errorf(proto.CodeKeyInvalid, "cannot decrypt the stored identity (wrong db key?)")
	}
	priv, err := identity.ParsePrivateKeyPKCS8(privDER)
	if err != nil {
		return nil, proto.Errorf(proto.CodeDBUnavailable, "stored identity key is corrupt: %v", err)
	}
	id, err := identity.FromParts(name, certDER, priv)
	if err != nil {
		return nil, proto.Errorf(proto.CodeDBUnavailable, "stored identity is corrupt: %v", err)
	}
	if id.DeviceID != deviceID {
		return nil, proto.Errorf(proto.CodeDBUnavailable,
			"stored identity device_id does not match its certificate")
	}
	if storedPlatform != platform {
		return nil, proto.Errorf(proto.CodeInvalid,
			"data_dir belongs to platform %q, not %q", storedPlatform, platform)
	}
	if storedName != name {
		if _, err := db.sql.Exec(`UPDATE identity SET name = ? WHERE device_id = ?`, name, deviceID); err != nil {
			return nil, dbErr("update identity name", err)
		}
	}
	return id, nil
}
