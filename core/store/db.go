// Package store owns the SQLite database behind the core: device identity,
// peers, links, outbox, inbox, settings and clipboard state.
// See docs/architecture.md §5.
//
// The package is policy-free. Sequence allocation, retries, TTL sweeps and
// "should this event be applied" decisions belong to peer and clip; store only
// persists rows and seals the sensitive columns with AES-256-GCM under the
// caller-supplied 32-byte database key.
package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, cross-compiles to Windows/Android

	"nightclip/core/proto"
)

const (
	// FileName is the database file created inside the configured data
	// directory.
	FileName = "clipbridge.db"

	keySize = 32
)

// DB is one open database. It is safe for concurrent use: database/sql pools
// connections and Open pins the pool to a single one, so writers serialise
// instead of racing on SQLite's file lock.
type DB struct {
	sql  *sql.DB
	key  [keySize]byte
	path string

	closeOnce sync.Once
	closeErr  error
}

// Open creates (or opens) <dir>/clipbridge.db. The key must be exactly 32
// bytes and is used for AES-256-GCM; it is never written to disk by this
// package.
func Open(dir string, key []byte) (*DB, error) {
	if len(key) != keySize {
		return nil, proto.Errorf(proto.CodeInvalid, "db key must be %d bytes, got %d", keySize, len(key))
	}
	if dir == "" {
		return nil, proto.Errorf(proto.CodeInvalid, "data_dir is required")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, proto.Errorf(proto.CodeDBUnavailable, "create data_dir: %v", err)
	}

	path := filepath.Join(dir, FileName)
	// Plain file name, not a "file:" DSN: Windows paths and spaces need no
	// URI escaping this way, and the PRAGMAs are applied explicitly below.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, proto.Errorf(proto.CodeDBUnavailable, "open database: %v", err)
	}
	raw.SetMaxOpenConns(1)
	db := &DB{sql: raw, path: path}
	copy(db.key[:], key)
	if err := db.init(); err != nil {
		raw.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) init() error {
	if err := db.sql.Ping(); err != nil {
		return proto.Errorf(proto.CodeDBUnavailable, "ping database: %v", err)
	}
	for _, pragma := range []string{
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.sql.Exec(pragma); err != nil {
			return proto.Errorf(proto.CodeDBUnavailable, "%s: %v", pragma, err)
		}
	}
	// journal_mode returns a row, so it cannot go through Exec.
	var mode string
	if err := db.sql.QueryRow("PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return proto.Errorf(proto.CodeDBUnavailable, "enable WAL: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return proto.Errorf(proto.CodeDBUnavailable, "journal_mode is %q, want wal", mode)
	}
	return migrate(db.sql)
}

// Close releases the database. It is idempotent.
func (db *DB) Close() error {
	db.closeOnce.Do(func() { db.closeErr = db.sql.Close() })
	return db.closeErr
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// seal encrypts plain with a fresh nonce. The returned blob is
// nonce(12) || ciphertext || tag.
func (db *DB) seal(plain, aad []byte) ([]byte, error) {
	gcm, err := db.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("store: nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plain, aad), nil
}

// errNotAuthentic covers both a wrong key and a tampered blob: AES-GCM cannot
// tell them apart. Callers map it to KEY_INVALID where a wrong key is the
// likely cause and to DB_UNAVAILABLE where corruption is.
var errNotAuthentic = errors.New("store: ciphertext is not authentic")

// unseal is the inverse of seal.
func (db *DB) unseal(blob, aad []byte) ([]byte, error) {
	gcm, err := db.gcm()
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(blob) < ns+gcm.Overhead() {
		return nil, errNotAuthentic
	}
	plain, err := gcm.Open(nil, blob[:ns], blob[ns:], aad)
	if err != nil {
		return nil, errNotAuthentic
	}
	return plain, nil
}

func (db *DB) gcm() (cipher.AEAD, error) {
	block, err := aes.NewCipher(db.key[:])
	if err != nil {
		return nil, fmt.Errorf("store: cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// rowAAD binds a ciphertext to one cell: moving it to another row or column
// makes it fail to open.
func rowAAD(table, column, pk string) []byte {
	return []byte(table + "\x00" + column + "\x00" + pk)
}

// dbErr maps a driver error to the stable DB_UNAVAILABLE code without leaking
// secrets: the wrapped text only carries the operation and the driver message.
func dbErr(op string, err error) error {
	return proto.Errorf(proto.CodeDBUnavailable, "%s: %v", op, err)
}
