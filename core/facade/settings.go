package facade

import (
	"encoding/json"
	"strconv"
	"strings"

	"moonpassword/core/proto"
)

// parseParams parses method parameters; empty params mean "{}".
func parseParams(paramsJSON string, out any) error {
	if strings.TrimSpace(paramsJSON) == "" {
		paramsJSON = "{}"
	}
	if err := json.Unmarshal([]byte(paramsJSON), out); err != nil {
		return proto.Errorf(proto.CodeInvalid, "params: %v", err)
	}
	return nil
}

// settingKeys are the user settings settings.get/settings.set know. Everything else in the
// settings table is internal state.
var settingKeys = []string{settingVaultAutoLock, settingVaultClipClear,
	settingSSHAgent, settingSSHAgentPipe, settingSSHKeyTTL, settingSSHForwarding}

func knownSetting(key string) bool {
	for _, k := range settingKeys {
		if k == key {
			return true
		}
	}
	return false
}

// settingValue returns a setting with its default applied.
func (c *Core) settingValue(key string) string {
	switch key {
	case settingVaultAutoLock:
		return strconv.Itoa(c.vaultIntSetting(key, defaultVaultAutoLock))
	case settingVaultClipClear:
		return strconv.Itoa(c.vaultIntSetting(key, defaultVaultClipClear))
	}
	return c.sshSettingValue(key)
}

func validateSetting(key, value string) error {
	switch {
	case isVaultSetting(key):
		return validateVaultSetting(key, value)
	case isSSHSetting(key):
		return validateSSHSetting(key, value)
	}
	return proto.Errorf(proto.CodeInvalid, "unknown setting %q", key)
}

func (c *Core) settingsCall(method, params string) (any, error) {
	c.mu.RLock()
	started := c.started
	c.mu.RUnlock()
	if !started {
		return nil, proto.Errorf(proto.CodeNotInitialized, "call Init first")
	}
	switch method {
	case "settings.get":
		return c.settingsGet(params)
	case "settings.set":
		return c.settingsSet(params)
	default: // diagnostics.get
		return c.diagnostics(), nil
	}
}

func (c *Core) settingsGet(params string) (any, error) {
	var p struct {
		Key string `json:"key"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	if p.Key != "" {
		if !knownSetting(p.Key) {
			return nil, proto.Errorf(proto.CodeInvalid, "unknown setting %q", p.Key)
		}
		return map[string]any{"key": p.Key, "value": c.settingValue(p.Key)}, nil
	}
	all := map[string]string{}
	for _, k := range settingKeys {
		all[k] = c.settingValue(k)
	}
	return map[string]any{"settings": all}, nil
}

func (c *Core) settingsSet(params string) (any, error) {
	var p struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	if err := validateSetting(p.Key, p.Value); err != nil {
		return nil, err
	}
	if err := c.db.SetSetting(p.Key, p.Value); err != nil {
		return nil, err
	}
	c.onSettingChanged(p.Key)
	return map[string]any{"key": p.Key, "value": p.Value}, nil
}

// diagnostics is what a shell copies into a support report: versions and states, never secrets.
func (c *Core) diagnostics() any {
	return map[string]any{
		"core":           Version,
		"platform":       c.cfg.Platform,
		"events_dropped": c.dropped.Load(),
		"update":         c.upd.Status(),
		"ssh":            c.sshDiagnostics(),
	}
}
