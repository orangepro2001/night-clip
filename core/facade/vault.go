package facade

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"nightclip/core/proto"
	"nightclip/core/vault"
)

// The password vault methods (docs/plan-vault.md §11). They live behind the same Call() as the rest,
// but never on the local named pipe: another process of the same user must not be able to talk to the
// vault (core/pipe has a test that fixes this).

// Vault settings, kept in the main settings table (not secret).
const (
	settingVaultAutoLock  = "vault_autolock_s"   // idle seconds before the vault locks itself
	settingVaultClipClear = "vault_clip_clear_s" // seconds a copied secret stays on the clipboard (0 = never clear)
	settingVaultKeyFile   = "vault_keyfile"      // on/off; internal, not a user setting
	defaultVaultAutoLock  = 300
	defaultVaultClipClear = 20
)

type vaultState struct {
	mu         sync.Mutex
	v          *vault.Vault
	timer      *time.Timer
	lastActive time.Time // wall clock (no monotonic part): a machine that slept unlocked still counts the time
	fails      int
	blockTil   time.Time
	attemptMu  sync.Mutex // password guesses and unlocks run one at a time
}

func (c *Core) vaultDir() string { return filepath.Join(c.cfg.DataDir, "vault") }

// vaultProvider gives the sync service the open vault without arming the idle timer (a background
// sync must not keep the vault unlocked).
type vaultProvider struct{ c *Core }

func (p vaultProvider) Current() *vault.Vault {
	p.c.vs.mu.Lock()
	defer p.c.vs.mu.Unlock()
	return p.c.vs.v
}
func (p vaultProvider) Dir() string { return p.c.vaultDir() }

// afterOpen wires a freshly opened vault to the sync service. Called without vs.mu held.
func (c *Core) vaultAfterOpen(v *vault.Vault) {
	n := c.net()
	if n == nil || n.sync == nil {
		return
	}
	v.SetOnSaved(n.sync.Kick)
	go n.sync.OnUnlocked()
}

func (c *Core) vaultIntSetting(key string, def int) int {
	if v, found, _ := c.db.GetSetting(key); found {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func validateVaultSetting(key, value string) error {
	n, err := strconv.Atoi(value)
	switch key {
	case settingVaultAutoLock:
		// 0 = never lock. The UI offers it explicitly; the upper bound keeps timers sane.
		if err != nil || n != 0 && (n < 30 || n > 86400) {
			return proto.Errorf(proto.CodeInvalid, "invalid value for %s (0, or 30 to 86400 seconds)", key)
		}
	case settingVaultClipClear:
		if err != nil || n != 0 && (n < 5 || n > 300) {
			return proto.Errorf(proto.CodeInvalid, "invalid value for %s (0, or 5 to 300 seconds)", key)
		}
	}
	return nil
}

func isVaultSetting(key string) bool {
	return key == settingVaultAutoLock || key == settingVaultClipClear
}

// vaultErr maps the vault package's errors to protocol errors.
func vaultErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*proto.Error); ok {
		return err
	}
	switch {
	case errors.Is(err, vault.ErrLocked):
		return proto.Errorf(proto.CodeVaultLocked, "the vault is locked")
	case errors.Is(err, vault.ErrWrongCredentials):
		return proto.Errorf(proto.CodeVaultCredentials, "wrong master password or key file, or the file is damaged")
	case errors.Is(err, vault.ErrNotFound):
		return proto.Errorf(proto.CodeVaultNotFound, "not found")
	case errors.Is(err, vault.ErrExists):
		return proto.Errorf(proto.CodeVaultExists, "already exists")
	case errors.Is(err, vault.ErrUnsupported):
		return proto.Errorf(proto.CodeVaultUnsupported, "%v", err)
	case errors.Is(err, vault.ErrReview), errors.Is(err, vault.ErrDifferentVault):
		return proto.Errorf(proto.CodeVaultReview, "this needs your approval")
	case errors.Is(err, vault.ErrNotEmpty):
		return proto.Errorf(proto.CodeVaultNotEmpty, "the group is not empty")
	case errors.Is(err, vault.ErrBroken):
		return proto.Errorf(proto.CodeVaultBroken, "the vault state is inconsistent; lock and unlock again")
	case errors.Is(err, vault.ErrTooLarge):
		return proto.Errorf(proto.CodeTooLarge, "too large")
	case errors.Is(err, vault.ErrInvalid):
		return proto.Errorf(proto.CodeInvalid, "%v", strings.TrimPrefix(err.Error(), "vault: invalid input: "))
	}
	return proto.Errorf(proto.CodeInternal, "vault error")
}

// ---- state helpers ------------------------------------------------------------------------------

// open returns the unlocked vault and restarts the idle timer.
func (c *Core) vaultOpen() (*vault.Vault, error) { return c.vaultOpenFor("") }

// vaultOpenFor is vaultOpen, except that background polling (the live one-time code of an entry that
// is open on screen) must not keep an unattended vault unlocked: vault.totp does not count as activity.
// A shell that sees the user typing sends vault.touch to say "still here".
func (c *Core) vaultOpenFor(method string) (*vault.Vault, error) {
	c.vs.mu.Lock()
	defer c.vs.mu.Unlock()
	if c.vs.v == nil {
		return nil, proto.Errorf(proto.CodeVaultLocked, "the vault is locked")
	}
	if method == "vault.totp" || method == "vault.status" {
		// passive: neither polling the code nor the status keeps an unattended vault open
		return c.vs.v, nil
	}
	// The idle timer does not tick while a laptop sleeps; the wall clock does.
	idle := time.Duration(c.vaultIntSetting(settingVaultAutoLock, defaultVaultAutoLock)) * time.Second
	if idle > 0 && !c.vs.lastActive.IsZero() && time.Now().Round(0).Sub(c.vs.lastActive) > idle {
		c.lockLocked("idle")
		return nil, proto.Errorf(proto.CodeVaultLocked, "the vault is locked")
	}
	c.armIdleLocked()
	return c.vs.v, nil
}

func (c *Core) armIdleLocked() {
	c.vs.lastActive = time.Now().Round(0)
	d := time.Duration(c.vaultIntSetting(settingVaultAutoLock, defaultVaultAutoLock)) * time.Second
	if c.vs.timer != nil {
		c.vs.timer.Stop()
		c.vs.timer = nil
	}
	if d <= 0 {
		return // the user chose "never"
	}
	v := c.vs.v
	c.vs.timer = time.AfterFunc(d, func() { c.lockVaultIf(v, "idle") })
}

// lockVaultIf locks the vault if v is still the open one.
func (c *Core) lockVaultIf(v *vault.Vault, reason string) {
	c.vs.mu.Lock()
	if c.vs.v != v || v == nil {
		c.vs.mu.Unlock()
		return
	}
	c.lockLocked(reason)
	c.vs.mu.Unlock()
}

func (c *Core) lockLocked(reason string) bool {
	if c.vs.v == nil {
		return false
	}
	if c.vs.timer != nil {
		c.vs.timer.Stop()
		c.vs.timer = nil
	}
	// Close waits for whatever the vault is doing right now (a merge can take a while); the lock must not
	// wait with vs.mu held, or every vault call would hang behind it. The vault is unreachable from here on.
	v := c.vs.v
	c.vs.v = nil
	go v.Close()
	c.emit(map[string]any{"type": "vault.locked", "reason": reason})
	return true
}

// closeVault is called at shutdown.
func (c *Core) closeVault() {
	c.vs.mu.Lock()
	defer c.vs.mu.Unlock()
	if c.vs.timer != nil {
		c.vs.timer.Stop()
	}
	if c.vs.v != nil {
		c.vs.v.Close()
		c.vs.v = nil
	}
}

// guard slows down password guessing through the API: after three wrong credentials in a row each
// further attempt waits twice as long, up to five minutes. It is per process, so it stops a script
// talking to a running app; against a stolen file only the key derivation cost helps.
func (c *Core) vaultGuard() error {
	c.vs.mu.Lock()
	defer c.vs.mu.Unlock()
	if wait := time.Until(c.vs.blockTil); wait > 0 {
		e := proto.Errorf(proto.CodeRateLimited, "too many wrong attempts, try again in %d seconds", int(wait.Seconds())+1)
		e.Retryable = true
		e.Details = map[string]any{"retry_after_s": int(wait.Seconds()) + 1}
		return e
	}
	return nil
}

func (c *Core) vaultAttempt(ok bool) {
	c.vs.mu.Lock()
	defer c.vs.mu.Unlock()
	if ok {
		c.vs.fails, c.vs.blockTil = 0, time.Time{}
		return
	}
	c.vs.fails++
	if c.vs.fails >= 3 {
		d := time.Duration(1<<uint(min(c.vs.fails-3, 8))) * time.Second
		if d > 5*time.Minute {
			d = 5 * time.Minute
		}
		c.vs.blockTil = time.Now().Add(d)
	}
}

// credAttempt runs fn, which checks credentials, as one guessing attempt. Attempts are serialized, so
// 40 parallel calls cannot all start before the first failure is counted; a success only clears the
// counter when resetOnSuccess is set (opening the vault or re-confirming the master password, not
// opening a backup with an old password).
func (c *Core) credAttempt(resetOnSuccess bool, fn func() error) error {
	c.vs.attemptMu.Lock()
	defer c.vs.attemptMu.Unlock()
	if err := c.vaultGuard(); err != nil {
		return err
	}
	err := fn()
	switch {
	case errors.Is(err, vault.ErrWrongCredentials):
		c.vaultAttempt(false)
	case err == nil && resetOnSuccess:
		c.vaultAttempt(true)
	}
	return err
}

type credParams struct {
	Password string `json:"password"`
	KeyFile  string `json:"key_file_b64"`
}

func (p credParams) creds() (vault.Creds, error) {
	c := vault.Creds{Password: []byte(p.Password)}
	if p.KeyFile != "" {
		b, err := base64.StdEncoding.DecodeString(p.KeyFile)
		if err != nil || len(b) == 0 || len(b) > 1<<20 {
			return c, proto.Errorf(proto.CodeInvalid, "key_file_b64 must be base64 of a file up to 1 MiB")
		}
		c.KeyFile = b
	}
	if len(c.Password) == 0 {
		return c, proto.Errorf(proto.CodeInvalid, "the master password must not be empty")
	}
	return c, nil
}

func kdfPreset(name string) (vault.KDF, error) {
	switch name {
	case "", "standard":
		return vault.KDFStandard, nil
	case "low":
		return vault.KDFLow, nil
	case "high":
		return vault.KDFHigh, nil
	}
	return vault.KDF{}, proto.Errorf(proto.CodeInvalid, "kdf must be low, standard or high")
}

// ---- views -----------------------------------------------------------------------------------------

type fieldView struct {
	Name      string `json:"name"`
	Value     string `json:"value"` // empty for protected fields: ask vault.reveal
	Protected bool   `json:"protected"`
	HasValue  bool   `json:"has_value"`
}

// entryView is an entry as listings and the detail screen get it: secrets are replaced by flags.
func entryView(e vault.Entry) map[string]any {
	fields := make([]fieldView, 0, len(e.Fields))
	for _, f := range e.Fields {
		fv := fieldView{Name: f.Name, Protected: f.Protected, HasValue: f.Value != ""}
		if !f.Protected {
			fv.Value = f.Value
		}
		fields = append(fields, fv)
	}
	return map[string]any{
		"id": e.ID, "group_id": e.GroupID, "kind": e.Kind, "title": e.Title, "username": e.Username,
		"url": e.URL, "notes": e.Notes, "tags": e.Tags, "fields": fields, "has_password": e.Password != "",
		"has_totp": e.TOTP != "", "expires_at": e.ExpiresAt, "created_at": e.CreatedAt, "modified_at": e.ModifiedAt,
		"attachments": e.Attachments, "history": e.History, "in_trash": e.InTrash,
		"password_strength": vault.Strength(e.Password),
	}
}

// ---- dispatch ---------------------------------------------------------------------------------------

func (c *Core) vaultCall(method, params string) (any, error) {
	r, err := c.vaultDispatch(method, params)
	return r, vaultErr(err)
}

func (c *Core) vaultDispatch(method, params string) (any, error) {
	c.mu.RLock()
	started := c.started
	c.mu.RUnlock()
	if !started {
		return nil, proto.Errorf(proto.CodeNotInitialized, "call Init first")
	}
	switch method {
	case "vault.status":
		return c.vaultStatus()
	case "vault.create":
		return c.vaultCreate(params)
	case "vault.unlock":
		return c.vaultUnlock(params)
	case "vault.lock":
		c.vs.mu.Lock()
		was := c.lockLocked("user")
		c.vs.mu.Unlock()
		return map[string]any{"was_unlocked": was}, nil
	case "vault.verify_master":
		return c.vaultVerifyMaster(params)
	case "vault.change_master":
		return c.vaultChangeMaster(params)
	case "vault.generate":
		return c.vaultGenerate(params)
	case "vault.strength":
		var p struct {
			Password string `json:"password"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return vault.Strength(p.Password), nil
	case "vault.backup_list":
		return map[string]any{"backups": nonNil(vault.ListBackups(c.vaultDir()))}, nil
	case "vault.backup_verify":
		return c.vaultBackupVerify(params)
	case "vault.sync_status", "vault.sync_enable", "vault.sync_resolve", "vault.sync_now":
		return c.vaultSyncCall(method, params)
	}
	v, err := c.vaultOpenFor(method)
	if err != nil {
		return nil, err
	}
	switch method {
	case "vault.touch":
		return map[string]any{}, nil // the call itself restarted the idle timer
	case "vault.list":
		var p struct {
			Query, GroupID, Kind, Tag string
			Trash                     bool
			IncludeTrash              bool `json:"include_trash"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		l, err := v.List(vault.Filter{Query: p.Query, GroupID: p.GroupID, Kind: p.Kind, Tag: p.Tag, OnlyTrash: p.Trash, IncludeTrash: p.IncludeTrash})
		return map[string]any{"entries": l}, err
	case "vault.get":
		var p struct {
			ID string `json:"id"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		e, err := v.Get(p.ID)
		if err != nil {
			return nil, err
		}
		if len(e.Notes) <= 8192 { // notes often hold secrets and are shown on screen
			c.markSecret(e.Notes)
		}
		return entryView(e), nil
	case "vault.match":
		var p struct {
			URL     string `json:"url"`
			Package string `json:"package"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		m, err := v.FindMatches(p.URL, p.Package)
		return map[string]any{"matches": m}, err
	case "vault.reveal", "vault.copy":
		return c.vaultReveal(v, method == "vault.copy", params)
	case "vault.totp":
		var p struct {
			ID string `json:"id"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		code, left, err := v.TOTP(p.ID)
		if err == nil {
			c.markSecret(code)
		}
		return map[string]any{"code": code, "seconds_left": left}, err
	case "vault.add":
		var p struct {
			Entry vault.Entry `json:"entry"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		id, err := v.AddEntry(p.Entry)
		return map[string]any{"id": id}, err
	case "vault.update":
		var p struct {
			ID    string      `json:"id"`
			Entry vault.Entry `json:"entry"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return map[string]any{}, v.UpdateEntry(p.ID, p.Entry)
	case "vault.patch":
		var p struct {
			ID        string         `json:"id"`
			Kind      *string        `json:"kind"`
			Title     *string        `json:"title"`
			Username  *string        `json:"username"`
			Password  *string        `json:"password"`
			URL       *string        `json:"url"`
			Notes     *string        `json:"notes"`
			TOTP      *string        `json:"totp"`
			Tags      *[]string      `json:"tags"`
			Fields    *[]vault.Field `json:"fields"`
			ExpiresAt *int64         `json:"expires_at"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		e, err := v.Get(p.ID)
		if err != nil {
			return nil, err
		}
		set := func(dst *string, src *string) {
			if src != nil {
				*dst = *src
			}
		}
		set(&e.Kind, p.Kind)
		set(&e.Title, p.Title)
		set(&e.Username, p.Username)
		set(&e.Password, p.Password)
		set(&e.URL, p.URL)
		set(&e.Notes, p.Notes)
		set(&e.TOTP, p.TOTP)
		if p.Tags != nil {
			e.Tags = *p.Tags
		}
		if p.Fields != nil {
			e.Fields = *p.Fields
		}
		if p.ExpiresAt != nil {
			e.ExpiresAt = *p.ExpiresAt
		}
		return map[string]any{}, v.UpdateEntry(p.ID, e)
	case "vault.delete":
		var p struct {
			ID string `json:"id"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		perm, err := v.Delete(p.ID)
		return map[string]any{"permanent": perm}, err
	case "vault.restore_entry":
		var p struct {
			ID string `json:"id"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return map[string]any{}, v.RestoreEntry(p.ID)
	case "vault.empty_trash":
		n, err := v.EmptyTrash()
		return map[string]any{"removed": n}, err
	case "vault.move":
		var p struct {
			ID      string `json:"id"`
			GroupID string `json:"group_id"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return map[string]any{}, v.MoveEntry(p.ID, p.GroupID)
	case "vault.groups":
		g, err := v.Groups()
		return map[string]any{"groups": g}, err
	case "vault.group_add":
		var p struct {
			ParentID string `json:"parent_id"`
			Name     string `json:"name"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		id, err := v.AddGroup(p.ParentID, p.Name)
		return map[string]any{"id": id}, err
	case "vault.group_rename":
		var p struct{ ID, Name string }
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return map[string]any{}, v.RenameGroup(p.ID, p.Name)
	case "vault.group_delete":
		var p struct {
			ID string `json:"id"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return map[string]any{}, v.DeleteGroup(p.ID)
	case "vault.history":
		var p struct {
			ID string `json:"id"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		h, err := v.History(p.ID)
		return map[string]any{"versions": h}, err
	case "vault.history_restore":
		var p struct {
			ID    string `json:"id"`
			Index int    `json:"index"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return map[string]any{}, v.RestoreVersion(p.ID, p.Index)
	case "vault.attach_add":
		var p struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Data string `json:"data_b64"`
			Path string `json:"path"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		var data []byte
		if p.Path != "" {
			f, err := os.Open(p.Path)
			if err != nil {
				return nil, proto.Errorf(proto.CodeInvalid, "cannot read that file")
			}
			defer f.Close()
			if data, err = readLimited(f, vault.MaxAttachmentBytes); err != nil {
				return nil, err
			}
			if p.Name == "" {
				p.Name = filepath.Base(p.Path)
			}
		} else {
			var err error
			if data, err = base64.StdEncoding.DecodeString(p.Data); err != nil {
				return nil, proto.Errorf(proto.CodeInvalid, "data_b64 is not base64")
			}
		}
		return map[string]any{}, v.AddAttachment(p.ID, p.Name, data)
	case "vault.attach_get":
		var p struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Path string `json:"path"` // when set the core writes the file there (never overwrites)
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		data, err := v.Attachment(p.ID, p.Name)
		if err != nil {
			return nil, err
		}
		if p.Path != "" {
			f, err := os.OpenFile(p.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return nil, proto.Errorf(proto.CodeInvalid, "cannot create that file (it may exist)")
			}
			defer f.Close()
			if _, err := f.Write(data); err != nil {
				return nil, proto.Errorf(proto.CodeInternal, "cannot write that file")
			}
			return map[string]any{"size": len(data)}, nil
		}
		return map[string]any{"data_b64": base64.StdEncoding.EncodeToString(data), "size": len(data)}, nil
	case "vault.attach_remove":
		var p struct{ ID, Name string }
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return map[string]any{}, v.RemoveAttachment(p.ID, p.Name)
	case "vault.health":
		return v.Health()
	case "vault.backup_now":
		b, err := v.BackupNow()
		return b, err
	case "vault.restore_preview":
		var p struct {
			Name string `json:"name"`
			credParams
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		cr, err := optionalCreds(p.credParams)
		if err != nil {
			return nil, err
		}
		var diff vault.RestoreDiff
		err = c.credAttempt(false, func() error {
			var rerr error
			diff, rerr = v.PreviewRestore(p.Name, cr)
			return rerr
		})
		return diff, err
	case "vault.restore":
		var p struct {
			Name string   `json:"name"`
			Mode string   `json:"mode"`
			IDs  []string `json:"ids"`
			credParams
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		cr, err := optionalCreds(p.credParams)
		if err != nil {
			return nil, err
		}
		var st vault.MergeStats
		err = c.credAttempt(false, func() error {
			var rerr error
			st, rerr = v.Restore(p.Name, cr, vault.RestoreMode(p.Mode), p.IDs)
			return rerr
		})
		return map[string]any{"stats": st}, err
	case "vault.import_file":
		var p struct {
			Path string `json:"path"`
			credParams
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		cr, err := p.creds()
		if err != nil {
			return nil, err
		}
		var st vault.MergeStats
		err = c.credAttempt(false, func() error {
			var rerr error
			st, rerr = v.ImportFile(p.Path, cr)
			return rerr
		})
		return map[string]any{"stats": st}, err
	case "vault.import_csv":
		var p struct {
			Path string `json:"path"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		f, err := os.Open(p.Path)
		if err != nil {
			return nil, proto.Errorf(proto.CodeInvalid, "cannot read that file")
		}
		defer f.Close()
		added, skipped, err := v.ImportCSV(f)
		return map[string]any{"added": added, "skipped": skipped}, err
	case "vault.export_file", "vault.export_csv":
		return c.vaultExport(v, method, params)
	}
	return nil, proto.Errorf(proto.CodeMethodNotFound, "unknown method %q", method)
}

func nonNil[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}

func readLimited(f *os.File, max int64) ([]byte, error) {
	var buf bytes.Buffer
	n, err := buf.ReadFrom(&limitedReader{f, max + 1})
	if err != nil {
		return nil, proto.Errorf(proto.CodeInvalid, "cannot read that file")
	}
	if n > max {
		return nil, proto.Errorf(proto.CodeTooLarge, "too large")
	}
	return buf.Bytes(), nil
}

type limitedReader struct {
	f *os.File
	n int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, errEOF
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.f.Read(p)
	l.n -= int64(n)
	return n, err
}

var errEOF = errors.New("EOF")

func optionalCreds(p credParams) (vault.Creds, error) {
	if p.Password == "" {
		return vault.Creds{}, nil // the current credentials
	}
	return p.creds()
}

// ---- lifecycle methods ------------------------------------------------------------------------------

func (c *Core) vaultStatus() (any, error) {
	dir := c.vaultDir()
	c.vs.mu.Lock()
	unlocked := c.vs.v != nil
	wait := time.Until(c.vs.blockTil)
	c.vs.mu.Unlock()
	st := map[string]any{
		"exists": vault.Exists(dir), "unlocked": unlocked,
		"autolock_s":    c.vaultIntSetting(settingVaultAutoLock, defaultVaultAutoLock),
		"clip_clear_s":  c.vaultIntSetting(settingVaultClipClear, defaultVaultClipClear),
		"retry_after_s": 0,
	}
	if wait > 0 {
		st["retry_after_s"] = int(wait.Seconds()) + 1
	}
	if kf, found, _ := c.db.GetSetting(settingVaultKeyFile); found {
		st["key_file"] = kf == "on" // absent when this device never saw the credentials (a vault joined from another device)
	}
	if bs := vault.ListBackups(dir); len(bs) > 0 {
		last := int64(0)
		for _, b := range bs {
			if b.Reason == "manual" || b.Reason == "export" {
				if b.Time > last {
					last = b.Time
				}
			}
		}
		st["last_manual_backup_at"] = last
		st["backups"] = len(bs)
	}
	if unlocked {
		if v, err := c.vaultOpenFor("vault.status"); err == nil {
			l, _ := v.List(vault.Filter{})
			st["entries"] = len(l)
			if k, err := v.KDFInfo(); err == nil {
				st["kdf"] = k
			}
		}
	}
	return st, nil
}

func (c *Core) vaultCreate(params string) (any, error) {
	var p struct {
		credParams
		KDF  string `json:"kdf"`
		Name string `json:"name"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	cr, err := p.creds()
	if err != nil {
		return nil, err
	}
	if err := checkNewMaster(cr); err != nil {
		return nil, err
	}
	kdf, err := kdfPreset(p.KDF)
	if err != nil {
		return nil, err
	}
	c.vs.mu.Lock()
	defer c.vs.mu.Unlock()
	if c.vs.v != nil || vault.Exists(c.vaultDir()) {
		return nil, vault.ErrExists
	}
	start := time.Now()
	v, err := vault.Create(c.vaultDir(), cr, kdf, p.Name)
	if err != nil {
		return nil, err
	}
	c.vs.v = v
	c.armIdleLocked()
	c.noteKeyFile(cr)
	go c.vaultAfterOpen(v)
	return map[string]any{"unlocked": true, "create_ms": time.Since(start).Milliseconds()}, nil
}

func (c *Core) vaultUnlock(params string) (any, error) {
	var p credParams
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	cr, err := p.creds()
	if err != nil {
		return nil, err
	}
	var unlockMS int64
	err = c.credAttempt(true, func() error {
		c.vs.mu.Lock()
		already := c.vs.v != nil
		c.vs.mu.Unlock()
		start := time.Now()
		v, err := vault.Open(c.vaultDir(), cr)
		if err != nil {
			return err
		}
		unlockMS = time.Since(start).Milliseconds()
		c.noteKeyFile(cr)
		c.vs.mu.Lock()
		defer c.vs.mu.Unlock()
		if already && c.vs.v != nil { // a second unlock: keep the first, drop this copy
			v.Close()
		} else {
			c.vs.v = v
			go c.vaultAfterOpen(v)
		}
		c.armIdleLocked()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"unlocked": true, "unlock_ms": unlockMS}, nil
}

func (c *Core) vaultVerifyMaster(params string) (any, error) {
	var p credParams
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	cr, err := p.creds()
	if err != nil {
		return nil, err
	}
	v, err := c.vaultOpen()
	if err != nil {
		return nil, err
	}
	if err := c.credAttempt(true, func() error {
		if !v.VerifyCredentials(cr) {
			return vault.ErrWrongCredentials
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// confirm re-checks the master password for actions that hand secrets out wholesale.
func (c *Core) vaultConfirm(v *vault.Vault, p credParams) error {
	cr, err := p.creds()
	if err != nil {
		return err
	}
	return c.credAttempt(true, func() error {
		if !v.VerifyCredentials(cr) {
			return vault.ErrWrongCredentials
		}
		return nil
	})
}

func (c *Core) vaultChangeMaster(params string) (any, error) {
	var p struct {
		credParams
		New struct {
			Password string `json:"password"`
			KeyFile  string `json:"key_file_b64"`
		} `json:"new"`
		KDF          string `json:"kdf"`
		PurgeBackups bool   `json:"purge_backups"` // delete every snapshot and the previous file (they still open with the old password)
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	v, err := c.vaultOpen()
	if err != nil {
		return nil, err
	}
	if err := c.vaultConfirm(v, p.credParams); err != nil {
		return nil, err
	}
	nc, err := credParams{Password: p.New.Password, KeyFile: p.New.KeyFile}.creds()
	if err != nil {
		return nil, err
	}
	if err := checkNewMaster(nc); err != nil {
		return nil, err
	}
	var kdf vault.KDF
	if p.KDF != "" {
		if kdf, err = kdfPreset(p.KDF); err != nil {
			return nil, err
		}
	}
	if err := v.ChangeCredentials(nc, kdf); err != nil {
		return nil, err
	}
	c.noteKeyFile(nc)
	purged := 0
	if p.PurgeBackups {
		purged, _ = v.PurgeBackups()
	}
	return map[string]any{"purged": purged}, nil
}

// checkNewMaster: a master password chosen now must be at least 8 characters (the UI also shows its
// strength; this is the floor the core insists on).
func checkNewMaster(cr vault.Creds) error {
	if utf8.RuneCount(cr.Password) < 8 {
		return proto.Errorf(proto.CodeInvalid, "the master password must be at least 8 characters")
	}
	return nil
}

// noteKeyFile remembers (in the settings table, not secret) whether the credentials in use include a
// key file, so the unlock screen can show or hide the key-file picker. A KDBX file does not say.
func (c *Core) noteKeyFile(cr vault.Creds) {
	val := "off"
	if len(cr.KeyFile) > 0 {
		val = "on"
	}
	_ = c.db.SetSetting(settingVaultKeyFile, val)
}

func (c *Core) vaultBackupVerify(params string) (any, error) {
	var p struct {
		Name string `json:"name"`
		credParams
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	cr, err := p.creds()
	if err != nil {
		return nil, err
	}
	var chk vault.BackupCheck
	err = c.credAttempt(false, func() error {
		var verr error
		chk, verr = vault.VerifyBackup(c.vaultDir(), p.Name, cr)
		return verr
	})
	return chk, err
}

func (c *Core) vaultGenerate(params string) (any, error) {
	var p struct {
		Type string `json:"type"`
		vault.PasswordOpts
		Words      int    `json:"words"`
		Separator  string `json:"separator"`
		Capitalize bool   `json:"capitalize"`
		Number     bool   `json:"number"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	var s string
	var err error
	switch p.Type {
	case "", "password":
		if p.Length == 0 {
			p.Length = 20
		}
		s, err = vault.GeneratePassword(p.PasswordOpts)
	case "passphrase":
		if p.Words == 0 {
			p.Words = 6
		}
		if p.Separator == "" {
			p.Separator = "-"
		}
		s, err = vault.GeneratePassphrase(vault.PassphraseOpts{Words: p.Words, Separator: p.Separator, Capitalize: p.Capitalize, Number: p.Number})
	default:
		return nil, proto.Errorf(proto.CodeInvalid, "type must be password or passphrase")
	}
	if err != nil {
		return nil, err
	}
	c.markSecret(s) // a generated password the user copies by hand is not broadcast either
	return map[string]any{"value": s, "strength": vault.Strength(s)}, nil
}

// ---- reveal / copy / export -----------------------------------------------------------------------------

// vaultReveal hands one secret to the shell. With copy set the text is also registered with the
// clipboard service, so the clipboard change it is about to cause is never broadcast, and the answer
// says when to clear it again.
func (c *Core) markSecret(value string) {
	if value == "" {
		return
	}
	if n := c.net(); n != nil {
		n.clip.MarkSecret(value)
	}
}

func (c *Core) vaultReveal(v *vault.Vault, copyIt bool, params string) (any, error) {
	var p struct {
		ID    string `json:"id"`
		Field string `json:"field"` // password, username, url, notes, totp (the current code), totp_secret, field:<name>
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	e, err := v.Get(p.ID)
	if err != nil {
		return nil, err
	}
	var value string
	switch {
	case p.Field == "password":
		value = e.Password
	case p.Field == "username":
		value = e.Username
	case p.Field == "url":
		value = e.URL
	case p.Field == "notes":
		value = e.Notes
	case p.Field == "totp_secret":
		value = e.TOTP
	case p.Field == "totp":
		code, _, err := v.TOTP(p.ID)
		if err != nil {
			return nil, err
		}
		value = code
	case strings.HasPrefix(p.Field, "field:"):
		name := strings.TrimPrefix(p.Field, "field:")
		found := false
		for _, f := range e.Fields {
			if f.Name == name {
				value, found = f.Value, true
			}
		}
		if !found {
			return nil, vault.ErrNotFound
		}
	default:
		return nil, proto.Errorf(proto.CodeInvalid, "unknown field %q", p.Field)
	}
	out := map[string]any{"value": value}
	// Whatever leaves the vault may land on the clipboard by hand (select, Ctrl+C): never broadcast it.
	c.markSecret(value)
	if copyIt {
		out["clear_after_s"] = c.vaultIntSetting(settingVaultClipClear, defaultVaultClipClear)
	}
	return out, nil
}

func (c *Core) vaultExport(v *vault.Vault, method, params string) (any, error) {
	var p struct {
		Path           string `json:"path"`
		Confirm        string `json:"confirm_password"`
		ConfirmKeyFile string `json:"confirm_key_file_b64"`
		credParams            // the credentials of the exported KDBX file (export_file only)
		KDF            string `json:"kdf"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	if p.Path == "" {
		return nil, proto.Errorf(proto.CodeInvalid, "path is required")
	}
	if err := c.vaultConfirm(v, credParams{Password: p.Confirm, KeyFile: p.ConfirmKeyFile}); err != nil {
		return nil, err
	}
	if _, err := os.Stat(p.Path); err == nil {
		return nil, vault.ErrExists
	}
	if method == "vault.export_csv" {
		f, err := os.OpenFile(p.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, proto.Errorf(proto.CodeInvalid, "cannot create that file")
		}
		defer f.Close()
		return map[string]any{}, v.ExportCSV(f)
	}
	cr, err := p.creds()
	if err != nil {
		return nil, err
	}
	var kdf vault.KDF
	if p.KDF != "" {
		if kdf, err = kdfPreset(p.KDF); err != nil {
			return nil, err
		}
	}
	return map[string]any{}, v.ExportFile(p.Path, cr, kdf)
}

// ---- device sync -----------------------------------------------------------------------------------------

func (c *Core) vaultSyncCall(method, params string) (any, error) {
	n := c.net()
	if n == nil || n.sync == nil {
		return nil, proto.Errorf(proto.CodeNotInitialized, "the network is not running")
	}
	switch method {
	case "vault.sync_status":
		return map[string]any{"peers": n.sync.Status(), "vault_exists": vault.Exists(c.vaultDir())}, nil
	case "vault.sync_now":
		n.sync.Kick()
		return map[string]any{}, nil
	case "vault.sync_enable":
		var p struct {
			DeviceID string `json:"device_id"`
			Enabled  bool   `json:"enabled"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		if err := c.db.SetPeerVaultSync(p.DeviceID, p.Enabled); err != nil {
			return nil, err
		}
		if p.Enabled {
			n.sync.Kick()
		} else {
			n.sync.Forget(p.DeviceID)
		}
		return map[string]any{}, nil
	default: // vault.sync_resolve
		var p struct {
			DeviceID string `json:"device_id"`
			Approve  bool   `json:"approve"`
			SHA256   string `json:"sha256"` // required with approve: the file the user reviewed (vault.sync_status incoming_sha256)
			credParams
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		var cr vault.Creds
		if p.Password != "" {
			var err error
			if cr, err = p.creds(); err != nil {
				return nil, err
			}
		}
		var rerr error
		if len(cr.Password) > 0 {
			rerr = c.credAttempt(false, func() error { return n.sync.Resolve(p.DeviceID, cr, p.Approve, p.SHA256) })
		} else {
			rerr = n.sync.Resolve(p.DeviceID, cr, p.Approve, p.SHA256)
		}
		return map[string]any{}, rerr
	}
}
