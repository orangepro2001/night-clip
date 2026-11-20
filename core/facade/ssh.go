package facade

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"

	"nightclip/core/proto"
	"nightclip/core/sshagent"
	"nightclip/core/sshkey"
	"nightclip/core/vault"
)

// SSH keys in the vault (docs/adr/009-ssh-agent.md, docs/vault-api.md §3.9).
// A key entry has kind "ssh", the private key as an unencrypted OpenSSH
// attachment (the vault is the encryption) and a KeeAgent.settings
// attachment, which is how KeePassXC marks the same thing. Private keys only
// leave the core through ssh.export, which asks for the master password.

// sshErr maps sshkey errors to protocol errors.
func sshErr(err error) error {
	if err == nil {
		return nil
	}
	var u *sshkey.UnsupportedError
	var pe *sshkey.PassphraseError
	switch {
	case errors.As(err, &u):
		e := proto.Errorf(proto.CodeSSHKeyUnsupported, "this key cannot be used (%s)", u.Reason)
		e.Details = map[string]any{"reason": u.Reason}
		return e
	case errors.As(err, &pe):
		e := proto.Errorf(proto.CodeSSHKeyPassphrase, "the key is encrypted")
		e.Details = map[string]any{"wrong": pe.Wrong}
		return e
	case errors.Is(err, sshkey.ErrTooExpensive):
		return proto.Errorf(proto.CodeSSHKeyTooExpensive, "the key's encryption parameters are too expensive to try")
	case errors.Is(err, sshkey.ErrTooLarge):
		return proto.Errorf(proto.CodeTooLarge, "too large")
	case errors.Is(err, sshkey.ErrCorrupt):
		return proto.Errorf(proto.CodeSSHKeyCorrupt, "the key file is damaged")
	}
	return err
}

// sshInput reads a key from data_b64 or a path (at most sshkey.MaxInput).
func sshInput(dataB64, path string) ([]byte, error) {
	if path != "" {
		if !filepath.IsAbs(path) {
			return nil, proto.Errorf(proto.CodeInvalid, "path must be absolute")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, proto.Errorf(proto.CodeInvalid, "cannot read that file")
		}
		defer f.Close()
		return readLimited(f, sshkey.MaxInput)
	}
	b, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil || len(b) == 0 {
		return nil, proto.Errorf(proto.CodeInvalid, "give data_b64 or path")
	}
	if len(b) > sshkey.MaxInput {
		return nil, proto.Errorf(proto.CodeTooLarge, "too large")
	}
	return b, nil
}

// sshEval is a vault SSH entry after its key has been looked at.
type sshEval struct {
	cand     vault.SSHCandidate
	settings *sshkey.AgentSettings
	key      *sshkey.Key
	problem  string // "" | needs_passphrase | external_file | missing_key | unsupported | corrupt | too_large | missing_settings
	reason   string // for "unsupported": the sshkey reason
}

func (e *sshEval) agentOn() bool { return e.settings != nil && e.settings.Enabled() }

// sshEvaluate parses the key of each candidate. Parsed keys are cached by
// content and passphrase so a KeePassXC entry whose key is encrypted is not
// put through bcrypt again on every save; the cache is dropped whenever the
// agent's keys are.
func (c *Core) sshEvaluate(cands []vault.SSHCandidate) []sshEval {
	out := make([]sshEval, 0, len(cands))
	for _, cand := range cands {
		e := sshEval{cand: cand}
		if cand.Settings != nil {
			if s, err := sshkey.ParseAgentSettings(cand.Settings); err == nil {
				e.settings = &s
				if s.LocationType == "file" {
					e.problem = "external_file"
					out = append(out, e)
					continue
				}
			} else {
				e.problem = "corrupt"
				out = append(out, e)
				continue
			}
		}
		switch {
		case cand.KeyName == "" || (cand.Key == nil && cand.KeySize == 0):
			e.problem = "missing_key"
		case cand.Key == nil:
			e.problem = "too_large"
		default:
			k, err := c.sshParseCached(cand.Key, cand.Password)
			var u *sshkey.UnsupportedError
			var pe *sshkey.PassphraseError
			switch {
			case err == nil:
				e.key = k
				if e.settings == nil {
					e.problem = "missing_settings"
				}
			case errors.As(err, &pe):
				e.problem = "needs_passphrase"
			case errors.As(err, &u):
				e.problem, e.reason = "unsupported", u.Reason
			default:
				e.problem = "corrupt"
			}
		}
		out = append(out, e)
	}
	return out
}

func (c *Core) sshParseCached(data []byte, password string) (*sshkey.Key, error) {
	h := sha256.New()
	h.Write(data)
	h.Write([]byte{0})
	h.Write([]byte(password))
	var id [32]byte
	copy(id[:], h.Sum(nil))
	c.ssh.mu.Lock()
	if k, ok := c.ssh.parsed[id]; ok {
		c.ssh.mu.Unlock()
		return k, nil
	}
	c.ssh.mu.Unlock()
	k, err := sshkey.Parse(data, nil)
	var pe *sshkey.PassphraseError
	if errors.As(err, &pe) && !pe.Wrong && password != "" {
		// KeePassXC keeps an encrypted key's passphrase in the entry password.
		k, err = sshkey.Parse(data, []byte(password))
	}
	if err != nil {
		return nil, err
	}
	c.ssh.mu.Lock()
	if c.ssh.parsed == nil || len(c.ssh.parsed) >= 256 {
		c.ssh.parsed = map[[32]byte]*sshkey.Key{}
	}
	c.ssh.parsed[id] = k
	c.ssh.mu.Unlock()
	return k, nil
}

// sshEntryView is the "ssh" block of vault.get and an item of ssh.keys.
func (c *Core) sshEntryView(e sshEval, groupPaths map[string]string) map[string]any {
	v := map[string]any{
		"entry_id": e.cand.ID, "title": e.cand.Title, "group_id": e.cand.GroupID,
		"key_attachment": e.cand.KeyName, "agent": e.agentOn(), "loaded": false,
		"problem": e.problem, "in_trash": e.cand.InTrash, "expires_at": e.cand.ExpiresAt,
		"hosts": nonNil(e.cand.Hosts),
	}
	if e.reason != "" {
		v["reason"] = e.reason
	}
	if p, ok := groupPaths[e.cand.GroupID]; ok {
		v["group_path"] = p
	}
	if k := e.key; k != nil {
		fp := k.Fingerprint()
		v["algo"], v["bits"], v["fingerprint"] = k.Algo(), k.Bits(), fp
		v["comment"], v["public_key"] = k.Comment, k.AuthorizedKey()
		v["source_format"], v["encrypted"] = string(k.Format), k.Encrypted
		v["loaded"] = c.sshLoaded(fp)
		if t := c.sshLastUsed(fp); t != 0 {
			v["last_used_at"] = t
		}
	}
	return v
}

func groupPaths(v *vault.Vault) map[string]string {
	out := map[string]string{}
	if gs, err := v.Groups(); err == nil {
		for _, g := range gs {
			out[g.ID] = g.Path
		}
	}
	return out
}

// sshCandidate evaluates one entry, refusing entries that are not SSH keys.
func (c *Core) sshCandidate(v *vault.Vault, id string) (sshEval, error) {
	if id == "" {
		return sshEval{}, proto.Errorf(proto.CodeInvalid, "id is required")
	}
	cands, err := v.SSHCandidates(id)
	if err != nil {
		return sshEval{}, err
	}
	if len(cands) == 0 {
		return sshEval{}, vault.ErrNotFound
	}
	return c.sshEvaluate(cands)[0], nil
}

func keyProblemErr(e sshEval) error {
	switch e.problem {
	case "", "missing_settings":
		return nil
	case "needs_passphrase":
		pe := proto.Errorf(proto.CodeSSHKeyPassphrase, "the stored key is encrypted")
		pe.Details = map[string]any{"wrong": false}
		return pe
	case "unsupported":
		pe := proto.Errorf(proto.CodeSSHKeyUnsupported, "this key cannot be used (%s)", e.reason)
		pe.Details = map[string]any{"reason": e.reason}
		return pe
	}
	pe := proto.Errorf(proto.CodeSSHKeyCorrupt, "the entry holds no usable key (%s)", e.problem)
	pe.Details = map[string]any{"problem": e.problem}
	return pe
}

// ---- dispatch -----------------------------------------------------------------------------------

func isSSHMethod(m string) bool { return strings.HasPrefix(m, "ssh.") }

func (c *Core) sshCall(method, params string) (any, error) {
	c.mu.RLock()
	started := c.started
	c.mu.RUnlock()
	if !started {
		return nil, proto.Errorf(proto.CodeNotInitialized, "call Init first")
	}
	r, err := c.sshDispatch(method, params)
	if err != nil {
		if _, ok := err.(*proto.Error); !ok {
			if e := sshErr(err); e != err {
				return nil, e
			}
			return nil, vaultErr(err)
		}
	}
	return r, err
}

func (c *Core) sshDispatch(method, params string) (any, error) {
	// Methods that work while the vault is locked.
	switch method {
	case "ssh.status":
		return c.sshStatus(), nil
	case "ssh.recent":
		var p struct {
			Limit int `json:"limit"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return map[string]any{"uses": c.sshRecent(p.Limit)}, nil
	case "ssh.selftest":
		return c.sshSelfTest(), nil
	case "ssh.inspect":
		return c.sshInspect(params)
	case "ssh.scan":
		return c.sshScan(params)
	case "ssh.keys":
		return c.sshKeys()
	case "ssh.public_key":
		return c.sshPublicKey(params)
	case "ssh.write_pub":
		return c.sshWritePub(params)
	}
	v, err := c.vaultOpen()
	if err != nil {
		return nil, err
	}
	switch method {
	case "ssh.import":
		return c.sshImport(v, params)
	case "ssh.generate":
		return c.sshGenerate(v, params)
	case "ssh.export":
		return c.sshExport(v, params)
	case "ssh.set_agent":
		return c.sshSetAgent(v, params)
	case "ssh.normalize":
		return c.sshNormalize(v, params)
	case "ssh.set_hosts":
		return c.sshSetHosts(v, params)
	}
	return nil, proto.Errorf(proto.CodeMethodNotFound, "unknown method %q", method)
}

// currentVault returns the open vault without counting as user activity.
func (c *Core) currentVault() *vault.Vault { c.vs.mu.Lock(); defer c.vs.mu.Unlock(); return c.vs.v }

// ---- methods ------------------------------------------------------------------------------------

func (c *Core) sshInspect(params string) (any, error) {
	var p struct {
		Data       string `json:"data_b64"`
		Path       string `json:"path"`
		Passphrase string `json:"passphrase"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	data, err := sshInput(p.Data, p.Path)
	if err != nil {
		return nil, err
	}
	k, err := sshkey.Parse(data, []byte(p.Passphrase))
	var pe *sshkey.PassphraseError
	out := map[string]any{"needs_passphrase": false, "wrong_passphrase": false, "warnings": []string{}}
	var info sshkey.Info
	switch {
	case err == nil:
		info = k.Info
		out["algo"], out["bits"], out["fingerprint"] = k.Algo(), k.Bits(), k.Fingerprint()
		out["comment"], out["public_key"] = k.Comment, k.AuthorizedKey()
	case errors.As(err, &pe):
		info = pe.Info
		out["needs_passphrase"], out["wrong_passphrase"] = true, pe.Wrong
		if info.Public != nil {
			out["algo"], out["fingerprint"] = info.Public.Type(), ssh.FingerprintSHA256(info.Public)
			out["public_key"] = sshkey.AuthorizedLine(info.Public, info.Comment)
		}
		if info.Comment != "" {
			out["comment"] = info.Comment
		}
	default:
		return nil, sshErr(err)
	}
	out["format"], out["encrypted"], out["cipher"], out["kdf"] = string(info.Format), info.Encrypted, info.Cipher, info.KDF
	if v := c.currentVault(); v != nil {
		if fp, ok := out["fingerprint"].(string); ok {
			out["in_vault"] = c.sshInVault(v, fp)
		}
	}
	return out, nil
}

// sshInVault lists the vault entries (outside the trash) holding the key.
func (c *Core) sshInVault(v *vault.Vault, fingerprint string) []map[string]any {
	found := []map[string]any{}
	cands, err := v.SSHCandidates("")
	if err != nil {
		return found
	}
	for _, e := range c.sshEvaluate(cands) {
		if e.key != nil && !e.cand.InTrash && e.key.Fingerprint() == fingerprint {
			found = append(found, map[string]any{"entry_id": e.cand.ID, "title": e.cand.Title})
		}
	}
	return found
}

type sshStoreParams struct {
	Title          string `json:"title"`
	GroupID        string `json:"group_id"`
	Agent          *bool  `json:"agent"`
	AttachmentName string `json:"attachment_name"`
}

// sshStore writes a key into a new entry.
func (c *Core) sshStore(v *vault.Vault, k *sshkey.Key, p sshStoreParams, fallbackName string) (any, error) {
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = strings.TrimSpace(k.Comment)
	}
	if title == "" {
		title = fallbackName
	}
	if title == "" {
		title = "SSH " + shortAlgo(k.Algo())
	}
	if k.Comment == "" {
		k.Comment = title
	}
	name := strings.TrimSpace(p.AttachmentName)
	if name == "" {
		name = fallbackName
	}
	if name == "" || name == sshkey.SettingsAttachment {
		name = "id_" + shortAlgo(k.Algo())
	}
	agentOn := p.Agent == nil || *p.Agent
	pem, err := sshkey.MarshalOpenSSH(k)
	if err != nil {
		return nil, sshErr(err)
	}
	id, err := v.AddEntryWithAttachments(vault.Entry{Kind: vault.KindSSH, Title: title, GroupID: p.GroupID}, []vault.NamedData{
		{Name: name, Data: pem},
		{Name: sshkey.SettingsAttachment, Data: sshkey.NewAgentSettings(name, agentOn)},
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id": id, "fingerprint": k.Fingerprint(), "public_key": k.AuthorizedKey(),
		"algo": k.Algo(), "bits": k.Bits(), "attachment_name": name,
	}, nil
}

func shortAlgo(algo string) string {
	switch {
	case algo == ssh.KeyAlgoED25519:
		return "ed25519"
	case algo == ssh.KeyAlgoRSA:
		return "rsa"
	case strings.HasPrefix(algo, "ecdsa-"):
		return "ecdsa"
	}
	return "key"
}

func (c *Core) sshImport(v *vault.Vault, params string) (any, error) {
	var p struct {
		sshStoreParams
		Data           string `json:"data_b64"`
		Path           string `json:"path"`
		Passphrase     string `json:"passphrase"`
		AllowDuplicate bool   `json:"allow_duplicate"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	data, err := sshInput(p.Data, p.Path)
	if err != nil {
		return nil, err
	}
	k, err := sshkey.Parse(data, []byte(p.Passphrase))
	if err != nil {
		return nil, sshErr(err)
	}
	if !p.AllowDuplicate {
		if in := c.sshInVault(v, k.Fingerprint()); len(in) > 0 {
			e := proto.Errorf(proto.CodeSSHKeyDuplicate, "this key is already in the vault")
			e.Details = map[string]any{"entry_id": in[0]["entry_id"], "title": in[0]["title"]}
			return nil, e
		}
	}
	fallback := ""
	if p.Path != "" {
		fallback = filepath.Base(p.Path)
	}
	return c.sshStore(v, k, p.sshStoreParams, fallback)
}

func (c *Core) sshGenerate(v *vault.Vault, params string) (any, error) {
	var p struct {
		sshStoreParams
		Type    string `json:"type"`
		Bits    int    `json:"bits"`
		Comment string `json:"comment"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	comment := strings.TrimSpace(p.Comment)
	if comment == "" {
		comment = strings.TrimSpace(p.Title)
	}
	k, err := sshkey.Generate(p.Type, p.Bits, comment)
	if err != nil {
		return nil, proto.Errorf(proto.CodeInvalid, "%v", strings.TrimPrefix(err.Error(), "sshkey: "))
	}
	return c.sshStore(v, k, p.sshStoreParams, "")
}

func (c *Core) sshExport(v *vault.Vault, params string) (any, error) {
	var p struct {
		credParams
		ID         string `json:"id"`
		Format     string `json:"format"`
		Passphrase string `json:"passphrase"`
		Path       string `json:"path"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	if p.Path == "" || !filepath.IsAbs(p.Path) {
		return nil, proto.Errorf(proto.CodeInvalid, "path must be absolute")
	}
	if err := c.vaultConfirm(v, p.credParams); err != nil {
		return nil, err
	}
	e, err := c.sshCandidate(v, p.ID)
	if err != nil {
		return nil, err
	}
	if err := keyProblemErr(e); err != nil {
		return nil, err
	}
	if p.Format == "" {
		p.Format = sshkey.ExportOpenSSH
	}
	out, err := sshkey.Export(e.key, p.Format, []byte(p.Passphrase))
	if err != nil {
		return nil, proto.Errorf(proto.CodeInvalid, "%v", strings.TrimPrefix(err.Error(), "sshkey: "))
	}
	f, err := os.OpenFile(p.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, proto.Errorf(proto.CodeInvalid, "cannot create that file (it may exist)")
	}
	defer f.Close()
	if _, err := f.Write(out); err != nil {
		return nil, proto.Errorf(proto.CodeInternal, "cannot write that file")
	}
	return map[string]any{"size": len(out)}, nil
}

func (c *Core) sshSetAgent(v *vault.Vault, params string) (any, error) {
	var p struct {
		ID      string `json:"id"`
		Enabled bool   `json:"enabled"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	e, err := c.sshCandidate(v, p.ID)
	if err != nil {
		return nil, err
	}
	var settings []byte
	if e.cand.Settings != nil && e.problem != "corrupt" {
		if settings, err = sshkey.SetAgentEnabled(e.cand.Settings, p.Enabled); err != nil {
			return nil, sshErr(err)
		}
	} else if e.cand.KeyName != "" {
		settings = sshkey.NewAgentSettings(e.cand.KeyName, p.Enabled)
	} else {
		return nil, keyProblemErr(e)
	}
	if err := v.ReplaceAttachments(p.ID, []vault.NamedData{{Name: sshkey.SettingsAttachment, Data: settings}}); err != nil {
		return nil, err
	}
	return map[string]any{"agent": p.Enabled}, nil
}

func (c *Core) sshNormalize(v *vault.Vault, params string) (any, error) {
	var p struct {
		ID         string `json:"id"`
		Passphrase string `json:"passphrase"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	e, err := c.sshCandidate(v, p.ID)
	if err != nil {
		return nil, err
	}
	if e.cand.Key == nil {
		return nil, keyProblemErr(e)
	}
	pass := p.Passphrase
	if pass == "" {
		pass = e.cand.Password
	}
	k, err := sshkey.Parse(e.cand.Key, []byte(pass))
	if err != nil {
		return nil, sshErr(err)
	}
	pem, err := sshkey.MarshalOpenSSH(k)
	if err != nil {
		return nil, sshErr(err)
	}
	atts := []vault.NamedData{{Name: e.cand.KeyName, Data: pem}}
	if e.cand.Settings == nil {
		atts = append(atts, vault.NamedData{Name: sshkey.SettingsAttachment, Data: sshkey.NewAgentSettings(e.cand.KeyName, true)})
	}
	if err := v.ReplaceAttachments(p.ID, atts); err != nil {
		return nil, err
	}
	return map[string]any{"fingerprint": k.Fingerprint()}, nil
}

// sshSetHosts limits a key to some servers (an empty list lifts the limit).
// Patterns are host names with optional * ? wildcards, "[host]:port", or a
// host key fingerprint "SHA256:…"; the agent matches names through the
// user's known_hosts files.
func (c *Core) sshSetHosts(v *vault.Vault, params string) (any, error) {
	var p struct {
		ID    string   `json:"id"`
		Hosts []string `json:"hosts"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	if _, err := c.sshCandidate(v, p.ID); err != nil {
		return nil, err
	}
	hosts := []string{}
	for _, h := range p.Hosts {
		h = strings.TrimSpace(h)
		if h == "" || containsString(hosts, h) {
			continue
		}
		if !sshagent.ValidHostPattern(h) {
			e := proto.Errorf(proto.CodeInvalid, "not a host name, [host]:port or SHA256 fingerprint: %q", h)
			e.Details = map[string]any{"host": h}
			return nil, e
		}
		hosts = append(hosts, h)
	}
	if len(hosts) > 32 {
		return nil, proto.Errorf(proto.CodeInvalid, "at most 32 hosts")
	}
	if err := v.SetSSHHosts(p.ID, hosts); err != nil {
		return nil, err
	}
	return map[string]any{"hosts": hosts}, nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// sshKeys lists the vault's SSH entries, or only the loaded keys when the
// vault is locked.
func (c *Core) sshKeys() (any, error) {
	v := c.currentVault()
	keys := []map[string]any{}
	if v == nil {
		for _, k := range c.sshLoadedKeys() {
			keys = append(keys, map[string]any{
				"entry_id": k.EntryID, "title": k.Title, "algo": k.Public.Type(), "fingerprint": k.Fingerprint,
				"comment": k.Comment, "agent": true, "loaded": true, "problem": "", "last_used_at": c.sshLastUsed(k.Fingerprint),
				"hosts": nonNil(k.Hosts),
			})
		}
		return map[string]any{"vault_unlocked": false, "keys": keys}, nil
	}
	cands, err := v.SSHCandidates("")
	if err != nil {
		return nil, err
	}
	paths := groupPaths(v)
	for _, e := range c.sshEvaluate(cands) {
		if !e.cand.InTrash {
			keys = append(keys, c.sshEntryView(e, paths))
		}
	}
	return map[string]any{"vault_unlocked": true, "keys": keys}, nil
}

// sshPublicKeyFor finds a public key by entry id or fingerprint, from the
// vault when it is open and from the agent's loaded keys otherwise.
func (c *Core) sshPublicKeyFor(id, fingerprint string) (ssh.PublicKey, string, string, error) {
	if v := c.currentVault(); v != nil {
		cands, err := v.SSHCandidates(id)
		if err != nil {
			return nil, "", "", err
		}
		for _, e := range c.sshEvaluate(cands) {
			if e.key == nil || e.cand.InTrash || (fingerprint != "" && e.key.Fingerprint() != fingerprint) {
				continue
			}
			return e.key.Public, e.key.Comment, e.cand.Title, nil
		}
		return nil, "", "", vault.ErrNotFound
	}
	for _, k := range c.sshLoadedKeys() {
		if (id == "" || k.EntryID == id) && (fingerprint == "" || k.Fingerprint == fingerprint) {
			return k.Public, k.Comment, k.Title, nil
		}
	}
	return nil, "", "", proto.Errorf(proto.CodeVaultLocked, "the vault is locked")
}

func (c *Core) sshPublicKey(params string) (any, error) {
	var p struct {
		ID          string `json:"id"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	if p.ID == "" && p.Fingerprint == "" {
		return nil, proto.Errorf(proto.CodeInvalid, "give id or fingerprint")
	}
	pub, comment, title, err := c.sshPublicKeyFor(p.ID, p.Fingerprint)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"public_key": sshkey.AuthorizedLine(pub, comment), "fingerprint": ssh.FingerprintSHA256(pub),
		"algo": pub.Type(), "title": title,
	}, nil
}

func (c *Core) sshScan(params string) (any, error) {
	var p struct {
		Dir string `json:"dir"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	if p.Dir == "" {
		if home == "" {
			return nil, proto.Errorf(proto.CodeInvalid, "no home directory")
		}
		p.Dir = filepath.Join(home, ".ssh")
	}
	if !filepath.IsAbs(p.Dir) {
		return nil, proto.Errorf(proto.CodeInvalid, "dir must be absolute")
	}
	res, err := sshkey.Scan(p.Dir, home)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{"dir": p.Dir, "items": []any{}, "skipped": 0, "vault_unlocked": c.currentVault() != nil}, nil
		}
		return nil, proto.Errorf(proto.CodeInvalid, "cannot read that folder")
	}
	inVault := map[string][]map[string]any{}
	v := c.currentVault()
	if v != nil {
		if cands, err := v.SSHCandidates(""); err == nil {
			for _, e := range c.sshEvaluate(cands) {
				if e.key != nil && !e.cand.InTrash {
					fp := e.key.Fingerprint()
					inVault[fp] = append(inVault[fp], map[string]any{"entry_id": e.cand.ID, "title": e.cand.Title})
				}
			}
		}
	}
	items := make([]map[string]any, 0, len(res.Items))
	for _, it := range res.Items {
		m := map[string]any{
			"path": it.Path, "format": string(it.Format), "encrypted": it.Encrypted, "algo": it.Algo, "bits": it.Bits,
			"fingerprint": it.Fingerprint, "comment": it.Comment, "pub_path": it.PubPath, "pub_state": it.PubState,
			"config_hosts": it.ConfigHosts, "referenced_by": it.ReferencedBy, "problem": it.Problem,
			"in_vault": nonNil(inVault[it.Fingerprint]),
		}
		items = append(items, m)
	}
	return map[string]any{"dir": res.Dir, "items": items, "skipped": res.Skipped, "vault_unlocked": v != nil}, nil
}

func (c *Core) sshWritePub(params string) (any, error) {
	var p struct {
		Path        string `json:"path"`
		ID          string `json:"id"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	if p.Path == "" || !filepath.IsAbs(p.Path) || !strings.EqualFold(filepath.Ext(p.Path), ".pub") {
		return nil, proto.Errorf(proto.CodeInvalid, "path must be an absolute path ending in .pub")
	}
	if p.ID == "" && p.Fingerprint == "" {
		return nil, proto.Errorf(proto.CodeInvalid, "give id or fingerprint")
	}
	pub, comment, _, err := c.sshPublicKeyFor(p.ID, p.Fingerprint)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"pub_path": p.Path}
	if old, err := os.ReadFile(p.Path); err == nil {
		if len(old) <= 64<<10 {
			if have, _, _, _, perr := ssh.ParseAuthorizedKey(old); perr == nil && bytes.Equal(have.Marshal(), pub.Marshal()) {
				out["state"] = "same"
				return out, nil
			}
		}
		out["state"] = "mismatch"
		return out, nil
	}
	f, err := os.OpenFile(p.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, proto.Errorf(proto.CodeInvalid, "cannot create that file")
	}
	defer f.Close()
	if _, err := f.WriteString(sshkey.AuthorizedLine(pub, comment) + "\n"); err != nil {
		return nil, proto.Errorf(proto.CodeInternal, "cannot write that file")
	}
	out["state"] = "created"
	return out, nil
}
