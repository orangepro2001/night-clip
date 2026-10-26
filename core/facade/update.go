package facade

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nightclip/core/proto"
	"nightclip/core/store"
	"nightclip/core/update"
)

// updateKeys supplies the trusted release keys; tests replace it.
var updateKeys = update.TrustedKeys

const (
	settingUpdateAuto = "update.auto" // "0" | "1"; absent means on
	settingUpdateURL  = "update.url"  // user override of the build-time server
	settingUpdateHigh = "update.high" // highest versionCode the server ever announced
	checkTimeout      = 30 * time.Second
)

// newUpdateManager builds the update manager for this core. It never fails
// because of a missing server: an unconfigured build simply has updates off.
func (c *Core) newUpdateManager(cfg Config, db *store.DB) (*update.Manager, error) {
	auto := true
	if v, found, err := db.GetSetting(settingUpdateAuto); err != nil {
		return nil, err
	} else if found {
		auto = v != "0"
	}
	raw := cfg.UpdateURL
	if v, found, err := db.GetSetting(settingUpdateURL); err != nil {
		return nil, err
	} else if found && v != "" {
		raw = v
	}
	return update.NewManager(update.Options{
		Client:      newUpdateClient(cfg.Platform, raw),
		Dir:         filepath.Join(cfg.DataDir, "updates"),
		CurrentCode: cfg.AppVersionCode,
		CurrentName: cfg.AppVersionName,
		Auto:        auto,
		Emit:        c.emit,
		LoadHighWater: func() int64 {
			v, _, _ := db.GetSetting(settingUpdateHigh)
			n, _ := strconv.ParseInt(v, 10, 64)
			return n
		},
		StoreHighWater: func(code int64) { _ = db.SetSetting(settingUpdateHigh, strconv.FormatInt(code, 10)) },
	}), nil
}

// newUpdateClient returns nil (updates off) for an empty or unusable URL.
func newUpdateClient(platform, rawURL string) *update.Client {
	if strings.TrimSpace(rawURL) == "" {
		return nil
	}
	base, err := update.ValidateBaseURL(rawURL)
	if err != nil {
		return nil
	}
	return &update.Client{BaseURL: base, Platform: platform, Keys: updateKeys()}
}

func (c *Core) updateCall(method, paramsJSON string) (any, error) {
	c.mu.RLock()
	upd, db, cfg, started := c.upd, c.db, c.cfg, c.started
	c.mu.RUnlock()
	if !started {
		return nil, proto.Errorf(proto.CodeNotInitialized, "call Init first")
	}
	switch method {
	case "update.status":
		return upd.Status(), nil
	case "update.check":
		ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
		defer cancel()
		st, err := upd.Check(ctx)
		if err != nil {
			return nil, err
		}
		return st, nil
	case "update.download":
		st, err := upd.Download()
		if err != nil {
			return nil, err
		}
		return st, nil
	default:
		return c.updateConfig(upd, db, cfg, paramsJSON)
	}
}

// updateConfig reads or changes the update settings. Params (all optional):
// {"auto": bool, "url": string}; an empty url restores the build-time default.
func (c *Core) updateConfig(upd *update.Manager, db *store.DB, cfg Config, paramsJSON string) (any, error) {
	var p struct {
		Auto *bool   `json:"auto"`
		URL  *string `json:"url"`
	}
	if strings.TrimSpace(paramsJSON) != "" {
		if err := json.Unmarshal([]byte(paramsJSON), &p); err != nil {
			return nil, proto.Errorf(proto.CodeInvalid, "params: %v", err)
		}
	}
	if p.URL != nil {
		want := strings.TrimSpace(*p.URL)
		effective := want
		if want == "" {
			effective = cfg.UpdateURL
		} else if _, err := update.ValidateBaseURL(want); err != nil {
			return nil, err
		}
		if want == "" {
			if err := db.DeleteSetting(settingUpdateURL); err != nil {
				return nil, err
			}
		} else if err := db.SetSetting(settingUpdateURL, want); err != nil {
			return nil, err
		}
		upd.SetClient(newUpdateClient(cfg.Platform, effective))
	}
	if p.Auto != nil {
		v := "0"
		if *p.Auto {
			v = "1"
		}
		if err := db.SetSetting(settingUpdateAuto, v); err != nil {
			return nil, err
		}
		upd.SetAuto(*p.Auto)
	}
	st := upd.Status()
	url := cfg.UpdateURL
	if v, found, err := db.GetSetting(settingUpdateURL); err == nil && found && v != "" {
		url = v
	}
	return map[string]any{"auto": st.Auto, "enabled": st.Enabled, "url": url, "default_url": cfg.UpdateURL}, nil
}
