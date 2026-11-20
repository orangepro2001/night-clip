package facade

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"moonpassword/core/proto"
	"moonpassword/core/vault"
	"moonpassword/core/vaultsync"
	moonsso "sso.moonl.cloud/sdk/go"
)

const (
	syncBase   = "https://release.moonl.cloud/vault-sync"
	syncSecret = "vault.cloud-sync.v1"
)

type syncBinding struct {
	Subject   string `json:"subject"`
	Key       string `json:"key"`
	Revision  string `json:"revision"`
	LocalHash string `json:"local_hash"`
	LastSync  int64  `json:"last_sync"`
	Create    bool   `json:"create"`
}

type syncView struct {
	State    string `json:"state"`
	Enabled  bool   `json:"enabled"`
	LastSync int64  `json:"last_sync,omitempty"`
	Error    string `json:"error,omitempty"`
}

type cloudSync struct {
	core *Core
	base string
	http *http.Client
	ctx  context.Context
	stop context.CancelFunc
	done chan struct{}
	wake chan struct{}

	mu      sync.Mutex // serializes sync against enrollment and disconnect
	failure string
}

func newCloudSync(core *Core, base string) *cloudSync {
	ctx, stop := context.WithCancel(context.Background())
	s := &cloudSync{core: core, base: base, ctx: ctx, stop: stop,
		http: &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		wake: make(chan struct{}, 1), done: make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *cloudSync) run() {
	defer close(s.done)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
		if s.core.currentVault() == nil {
			continue
		}
		s.mu.Lock()
		if s.ctx.Err() == nil {
			if binding, err := s.load(); binding != nil && err == nil {
				s.trySync(binding)
			}
		}
		s.mu.Unlock()
	}
}

func (s *cloudSync) close() {
	s.stop()
	<-s.done
	s.mu.Lock()
	s.mu.Unlock()
}

func (s *cloudSync) schedule() {
	if s == nil {
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *cloudSync) notify() { s.core.emit(map[string]any{"type": "sync.changed"}) }

func (s *cloudSync) load() (*syncBinding, error) {
	data, ok, err := s.core.db.GetSecret(syncSecret)
	if !ok || err != nil {
		return nil, err
	}
	defer clear(data)
	var b syncBinding
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *cloudSync) persist(b *syncBinding) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	defer clear(data)
	return s.core.db.SetSecret(syncSecret, data)
}

// credentials returns tokens only to the Go transport, and only for the
// account this local encrypted vault is paired with.
func (a *accountState) syncCredentials(ctx context.Context, subject string, refresh bool) (string, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ctx.Err() != nil || a.ensureSession() != nil {
		return "", "", syncError("SYNC_SIGN_IN", "sign into Moon SSO first")
	}
	profile := a.session.Profile(ctx)
	if profile == nil || profile.Sub == "" {
		return "", "", syncError("SYNC_SIGN_IN", "sign into Moon SSO first")
	}
	if subject != "" && subject != profile.Sub {
		return "", "", syncError("SYNC_ACCOUNT", "this vault is paired to a different Moon SSO account")
	}
	var err error
	if refresh {
		err = a.session.Refresh(ctx)
	}
	var access string
	if err == nil {
		access, err = a.session.AccessToken(ctx)
	}
	if errors.Is(err, moonsso.ErrSessionExpired) || errors.Is(err, moonsso.ErrNotSignedIn) {
		return "", "", syncError("SYNC_SIGN_IN", "sign into Moon SSO again")
	}
	if err != nil {
		return "", "", vaultsync.ErrUnavailable
	}
	return profile.Sub, access, nil
}

func (s *cloudSync) view(b *syncBinding) syncView {
	if b == nil {
		return syncView{State: "disabled"}
	}
	state := "ready"
	if s.core.currentVault() == nil {
		state = "locked"
	}
	return syncView{State: state, Enabled: true, LastSync: b.LastSync, Error: s.failure}
}

func syncError(code, message string) *proto.Error {
	return proto.Errorf(proto.Code(code), "%s", message)
}

func publicSyncError(err error) *proto.Error {
	var p *proto.Error
	if errors.As(err, &p) {
		return p
	}
	switch {
	case errors.Is(err, vault.ErrWrongCredentials):
		return syncError("SYNC_KEY", "the encrypted cloud vault does not open with this recovery key")
	case errors.Is(err, vault.ErrLocked):
		return syncError("VAULT_LOCKED", "unlock the vault to sync")
	case errors.Is(err, vaultsync.ErrConflict):
		return syncError("SYNC_CONFLICT", "another device updated the vault")
	case errors.Is(err, vaultsync.ErrAuth):
		return syncError("SYNC_SIGN_IN", "the sync service rejected the Moon SSO session")
	case errors.Is(err, vaultsync.ErrInvalid), errors.Is(err, vault.ErrUnsupported), errors.Is(err, vault.ErrTooLarge):
		return syncError("SYNC_DATA", "the encrypted snapshot exceeds supported limits or is damaged")
	default:
		return syncError("SYNC_NETWORK", "unable to synchronize the vault")
	}
}

func (s *cloudSync) trySync(b *syncBinding) error {
	var err error
	refresh := false
	oldTime := b.LastSync
	oldFailure := s.failure
	for range 3 {
		err = s.syncOnce(b, refresh)
		if errors.Is(err, vaultsync.ErrAuth) && !refresh {
			refresh = true
			continue
		}
		if !errors.Is(err, vaultsync.ErrConflict) {
			break
		}
	}
	if err != nil {
		s.failure = string(publicSyncError(err).Code)
	} else {
		s.failure = ""
	}
	if oldTime != b.LastSync || oldFailure != s.failure {
		s.notify()
	}
	return err
}

func (s *cloudSync) syncOnce(b *syncBinding, refresh bool) error {
	open := s.core.currentVault()
	if open == nil {
		return vault.ErrLocked
	}
	_, token, err := s.core.account.syncCredentials(s.ctx, b.Subject, refresh)
	if err != nil {
		return err
	}
	remote, err := vaultsync.Get(s.ctx, s.http, s.base, token, b.Revision)
	if err != nil {
		return err
	}
	if remote.Revision == "" && !b.Create {
		return syncError("SYNC_MISSING", "the existing cloud vault is missing")
	}
	return s.apply(b, open, token, remote)
}

func (s *cloudSync) apply(b *syncBinding, open *vault.Vault, token string, remote vaultsync.Snapshot) error {
	key := []byte(b.Key)
	defer clear(key)
	result, err := open.PrepareSync(remote.Data, key, b.LocalHash, true)
	if result.Merged {
		s.core.emit(map[string]any{"type": "vault.changed", "reason": "sync"})
	}
	if err != nil {
		return err
	}
	revision := remote.Revision
	if len(result.Upload) > 0 {
		if s.core.currentVault() != open {
			return vault.ErrLocked
		}
		revision, err = vaultsync.Put(s.ctx, s.http, s.base, token, b.Subject, remote.Revision, b.Key, result.Upload)
		if err != nil {
			return err
		}
	}
	if b.Revision == revision && b.LocalHash == result.FileHash {
		return nil
	}
	next := *b
	next.Revision, next.LocalHash, next.Create = revision, result.FileHash, false
	next.LastSync = time.Now().Unix()
	if err := s.persist(&next); err != nil {
		return err
	}
	*b = next
	return nil
}

func (s *cloudSync) enable(mode, key string) (*syncBinding, error) {
	if mode != "create" && mode != "join" {
		return nil, syncError("INVALID", "choose create or join")
	}
	open := s.core.currentVault()
	if open == nil {
		return nil, vault.ErrLocked
	}
	subject, token, err := s.core.account.syncCredentials(s.ctx, "", false)
	if err != nil {
		return nil, err
	}
	remote, err := vaultsync.Get(s.ctx, s.http, s.base, token, "")
	if err != nil {
		return nil, err
	}
	b := &syncBinding{Subject: subject, Create: mode == "create"}
	if b.Create {
		if remote.Revision != "" {
			return nil, syncError("SYNC_EXISTS", "this account already has a cloud vault; join it with its recovery key")
		}
		random := make([]byte, 32)
		_, _ = rand.Read(random)
		b.Key = base64.RawURLEncoding.EncodeToString(random)
		clear(random)
	} else {
		key = strings.TrimSpace(key)
		raw, err := base64.RawURLEncoding.Strict().DecodeString(key)
		defer clear(raw)
		if err != nil || len(raw) != 32 || len(key) != 43 {
			return nil, syncError("SYNC_KEY", "invalid sync recovery key")
		}
		if remote.Revision == "" {
			return nil, syncError("SYNC_MISSING", "this account has no cloud vault")
		}
		secret := []byte(key)
		check, err := open.PrepareSync(remote.Data, secret, "", false)
		clear(secret)
		if check.Merged {
			s.core.emit(map[string]any{"type": "vault.changed", "reason": "sync"})
		}
		if err != nil {
			return nil, err
		}
		b.Key = key
	}
	// Persist the key before contacting the server. Losing a successful PUT
	// response must never leave an encrypted cloud vault without its key.
	if err := s.persist(b); err != nil {
		return nil, err
	}
	s.failure = ""
	if err := s.apply(b, open, token, remote); err != nil {
		s.failure = string(publicSyncError(err).Code)
		s.schedule()
	}
	s.notify()
	return b, nil
}

func (c *Core) cloudCall(method, params string) (any, error) {
	c.mu.RLock()
	s := c.cloud
	c.mu.RUnlock()
	if s == nil {
		return nil, proto.Errorf(proto.CodeNotInitialized, "call Init first")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return nil, proto.Errorf(proto.CodeNotInitialized, "core has been shut down")
	}
	if method == "sync.disconnect" {
		if err := c.db.DeleteSecret(syncSecret); err != nil {
			return nil, publicSyncError(err)
		}
		s.failure = ""
		s.notify()
		return s.view(nil), nil
	}
	b, err := s.load()
	if err != nil {
		return nil, publicSyncError(err)
	}
	switch method {
	case "sync.status":
		return s.view(b), nil
	case "sync.enable":
		if b != nil {
			return nil, syncError("SYNC_EXISTS", "this device is already paired")
		}
		var p struct {
			Mode string `json:"mode"`
			Key  string `json:"key"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		b, err = s.enable(p.Mode, p.Key)
	case "sync.now":
		if b == nil {
			return nil, syncError("SYNC_DISABLED", "connect the vault to sync first")
		}
		err = s.trySync(b)
	case "sync.recovery_key":
		if b == nil {
			return nil, syncError("SYNC_DISABLED", "connect the vault to sync first")
		}
		if c.currentVault() == nil {
			return nil, vaultErr(vault.ErrLocked)
		}
		return map[string]string{"key": b.Key}, nil
	}
	if err != nil {
		return nil, publicSyncError(err)
	}
	return s.view(b), nil
}
