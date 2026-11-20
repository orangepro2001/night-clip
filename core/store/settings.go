package store

import "database/sql"

// GetSetting returns a settings value; found is false when the key is unset.
func (db *DB) GetSetting(key string) (string, bool, error) {
	var value string
	err := db.sql.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, dbErr("read setting", err)
	}
	return value, true, nil
}

// SetSetting inserts or replaces a settings value.
func (db *DB) SetSetting(key, value string) error {
	_, err := db.sql.Exec(`
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	if err != nil {
		return dbErr("store setting", err)
	}
	return nil
}

// DeleteSetting removes a settings value; deleting an absent key is not an
// error.
func (db *DB) DeleteSetting(key string) error {
	if _, err := db.sql.Exec(`DELETE FROM settings WHERE key = ?`, key); err != nil {
		return dbErr("delete setting", err)
	}
	return nil
}
