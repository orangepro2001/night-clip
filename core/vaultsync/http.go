// Package vaultsync transports encrypted KDBX snapshots with conditional writes.
package vaultsync

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"moonpassword/core/vault"
)

const VaultPath = "/v1/vault"
const protocolHeader = "X-MoonPassword-Sync"
const writeKeyHeader = "X-MoonPassword-Sync-Key"
const signatureHeader = "X-MoonPassword-Sync-Signature"

var (
	ErrConflict    = errors.New("vault sync: revision changed")
	ErrAuth        = errors.New("vault sync: authorization rejected")
	ErrInvalid     = errors.New("vault sync: invalid encrypted snapshot")
	ErrUnavailable = errors.New("vault sync: service unavailable")
)

var kdbx4Magic = []byte{0x03, 0xd9, 0xa2, 0x9a, 0x67, 0xfb, 0x4b, 0xb5, 0, 0, 4, 0}

func validSnapshot(data []byte) bool {
	return len(data) > 64 && len(data) <= vault.MaxFileBytes &&
		bytes.Equal(data[:8], kdbx4Magic[:8]) && bytes.Equal(data[10:12], kdbx4Magic[10:12])
}

func version(data []byte) string {
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

type Snapshot struct {
	Data      []byte
	Revision  string
	Unchanged bool
}

// Get obtains the latest remote revision, unless it is already known.
func Get(ctx context.Context, hc *http.Client, base, token, previous string) (Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+VaultPath, nil)
	if err != nil {
		return Snapshot{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if previous != "" {
		req.Header.Set("If-None-Match", previous)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Snapshot{}, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.Header.Get(protocolHeader) != "1" {
		return Snapshot{}, ErrUnavailable
	}
	switch resp.StatusCode {
	case http.StatusNotFound:
		return Snapshot{}, nil
	case http.StatusNotModified:
		if previous == "" || resp.Header.Get("ETag") != previous {
			return Snapshot{}, ErrInvalid
		}
		return Snapshot{Revision: previous, Unchanged: true}, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return Snapshot{}, ErrAuth
	case http.StatusOK:
		data, err := io.ReadAll(io.LimitReader(resp.Body, vault.MaxFileBytes+1))
		if err != nil {
			return Snapshot{}, ErrUnavailable
		}
		rev := resp.Header.Get("ETag")
		if !validSnapshot(data) || rev != version(data) {
			return Snapshot{}, ErrInvalid
		}
		return Snapshot{Data: data, Revision: rev}, nil
	default:
		return Snapshot{}, ErrUnavailable
	}
}

// Put stores the snapshot only if expected still matches. An empty expected
// value is creation only. A conflicting caller must pull and merge.
func Put(ctx context.Context, hc *http.Client, base, token, subject, expected, recoveryKey string, data []byte) (string, error) {
	if !validSnapshot(data) {
		return "", ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+VaultPath, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/octet-stream")
	seed := sha256.Sum256([]byte("MoonPassword vault sync write key v1:" + recoveryKey))
	key := ed25519.NewKeyFromSeed(seed[:])
	defer clear(key)
	clear(seed[:])
	req.Header.Set(writeKeyHeader, base64.RawURLEncoding.EncodeToString(key[32:]))
	req.Header.Set(signatureHeader, base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, writeMessage(subject, expected, version(data)))))
	if expected == "" {
		req.Header.Set("If-None-Match", "*")
	} else {
		req.Header.Set("If-Match", expected)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.Header.Get(protocolHeader) != "1" {
		return "", ErrUnavailable
	}
	switch resp.StatusCode {
	case http.StatusPreconditionFailed:
		return "", ErrConflict
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", ErrAuth
	case http.StatusUnprocessableEntity:
		return "", ErrInvalid
	case http.StatusNoContent:
		rev := resp.Header.Get("ETag")
		if rev != version(data) {
			return "", ErrInvalid
		}
		return rev, nil
	default:
		return "", ErrUnavailable
	}
}

func writeMessage(subject, expected, revision string) []byte {
	return []byte("MoonPassword vault sync v1\n" + subject + "\n" + expected + "\n" + revision)
}

// Server stores at most one encrypted snapshot per authenticated subject.
// The directory belongs exclusively to this server process.
type Server struct {
	dir    string
	verify func(context.Context, string) (string, error)
	mu     sync.Mutex
	slots  chan struct{}
}

func NewServer(dir string, verify func(context.Context, string) (string, error)) (*Server, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Server{dir: dir, verify: verify, slots: make(chan struct{}, 4)}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path != VaultPath {
		http.NotFound(w, r)
		return
	}
	w.Header().Set(protocolHeader, "1")
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" || len(token) > 8192 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	sub, err := s.verify(r.Context(), token)
	if err != nil || sub == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	account := sha256.Sum256([]byte(sub))
	path := filepath.Join(s.dir, hex.EncodeToString(account[:])+".kdbx")
	if r.Method == http.MethodGet {
		s.get(w, r, path)
		return
	}
	s.put(w, r, path, sub)
}

func (s *Server) get(w http.ResponseWriter, r *http.Request, path string) {
	_, data, err := readRecord(path)
	if errors.Is(err, os.ErrNotExist) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if err != nil || !validSnapshot(data) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	rev := version(data)
	w.Header().Set("ETag", rev)
	if r.Header.Get("If-None-Match") == rev {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(data)
}

func (s *Server) put(w http.ResponseWriter, r *http.Request, path, subject string) {
	match, create := r.Header.Get("If-Match"), r.Header.Get("If-None-Match")
	if (match == "" && create != "*") || (match != "" && create != "") {
		w.WriteHeader(http.StatusPreconditionRequired)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, vault.MaxFileBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
		} else {
			w.WriteHeader(http.StatusBadRequest)
		}
		return
	}
	if !validSnapshot(data) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	pub, keyErr := base64.RawURLEncoding.DecodeString(r.Header.Get(writeKeyHeader))
	signature, sigErr := base64.RawURLEncoding.DecodeString(r.Header.Get(signatureHeader))
	revision := version(data)
	if keyErr != nil || sigErr != nil || len(pub) != ed25519.PublicKeySize ||
		!ed25519.Verify(pub, writeMessage(subject, match, revision), signature) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	currentKey, current, err := readRecord(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if (err == nil && (match == "" || match != version(current))) || (err != nil && create != "*") {
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}
	if currentKey != nil && !bytes.Equal(pub, currentKey) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	if err := s.write(path, pub, data); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("ETag", revision)
	w.WriteHeader(http.StatusNoContent)
}

func readRecord(path string) ([]byte, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	record, err := io.ReadAll(io.LimitReader(f, ed25519.PublicKeySize+vault.MaxFileBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(record) <= ed25519.PublicKeySize || !validSnapshot(record[ed25519.PublicKeySize:]) {
		return nil, nil, ErrInvalid
	}
	return record[:ed25519.PublicKeySize], record[ed25519.PublicKeySize:], nil
}

func (s *Server) write(path string, key, data []byte) error {
	f, err := os.CreateTemp(s.dir, ".snapshot-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(key); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	if dir, err := os.Open(s.dir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
