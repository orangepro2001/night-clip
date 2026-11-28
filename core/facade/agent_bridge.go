package facade

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"moonpassword/core/agentops"
	"moonpassword/core/proto"
	"moonpassword/core/vault"
)

const agentBase = "https://release.moonl.cloud/vault-agent"
const agentDeviceSecret = "agent.device.v1"

var agentHTTP = &http.Client{
	Timeout:       10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (c *Core) agentProof(create bool) (string, error) {
	c.agentMu.Lock()
	defer c.agentMu.Unlock()
	p, found, err := c.db.GetSecret(agentDeviceSecret)
	if err != nil {
		return "", err
	}
	if found {
		defer clear(p)
		return string(p), nil
	}
	if !create {
		return "", nil
	}
	var random [32]byte
	rand.Read(random[:])
	proof := base64.RawURLEncoding.EncodeToString(random[:])
	return proof, c.db.SetSecret(agentDeviceSecret, []byte(proof))
}

func (c *Core) callAgentDevice(ctx context.Context, proof string, p agentops.DeviceCall) (agentops.DeviceReply, error) {
	var reply agentops.DeviceReply
	for _, refresh := range []bool{false, true} {
		_, ssoToken, err := c.account.syncCredentials(ctx, "", refresh)
		if err != nil {
			return reply, err
		}
		b, err := json.Marshal(p)
		if err != nil {
			return reply, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, agentBase+"/device", bytes.NewReader(b))
		if err != nil {
			return reply, err
		}
		req.Header.Set("Authorization", "Bearer "+ssoToken)
		req.Header.Set("X-MoonPassword-Device", proof)
		req.Header.Set("Content-Type", "application/json")
		res, err := agentHTTP.Do(req)
		if err != nil {
			return reply, proto.Errorf("AGENT_NETWORK", "MoonPassword connector is unavailable")
		}
		if res.StatusCode == http.StatusUnauthorized {
			_ = res.Body.Close()
			continue
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusConflict {
			return reply, proto.Errorf("AGENT_EXPIRED", "request expired, changed or was already handled")
		}
		if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&reply) != nil {
			return agentops.DeviceReply{}, proto.Errorf("AGENT_NETWORK", "MoonPassword connector rejected the request")
		}
		return reply, nil
	}
	return reply, proto.Errorf("SYNC_SIGN_IN", "sign into Moon SSO again")
}

func (c *Core) agentCall(method, params string) (any, error) {
	if c.cfg.Platform != "windows" {
		return nil, proto.Errorf(proto.CodeInvalid, "agent approvals require Windows")
	}
	current := c.currentVault()
	switch method {
	case "agent.credentials":
		if current == nil {
			return nil, vaultErr(vault.ErrLocked)
		}
		entries, err := current.List(vault.Filter{Kind: vault.KindAPIKey})
		if err != nil {
			return nil, vaultErr(err)
		}
		suggestions := []map[string]string{}
		for _, item := range entries {
			if agentops.GitHubURL(item.URL) && item.HasPassword && (item.ExpiresAt == 0 || item.ExpiresAt > time.Now().Unix()) {
				suggestions = append(suggestions, map[string]string{"id": item.ID, "title": item.Title})
			}
		}
		return map[string]any{"entries": suggestions}, nil
	case "agent.next":
		if current == nil {
			return agentops.DeviceReply{}, nil
		}
	case "agent.connect", "agent.approve":
		if current == nil {
			return nil, vaultErr(vault.ErrLocked)
		}
	}
	var p struct {
		Client  string           `json:"client"`
		Request agentops.Request `json:"request"`
		EntryID string           `json:"entry_id"`
	}
	if err := parseParams(params, &p); err != nil {
		return nil, err
	}
	proof, err := c.agentProof(method == "agent.connect")
	if err != nil {
		return nil, err
	}
	if proof == "" {
		if method == "agent.approve" || method == "agent.deny" {
			return nil, proto.Errorf("AGENT_EXPIRED", "no connector on this device")
		}
		return agentops.DeviceReply{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Second)
	defer cancel()
	switch method {
	case "agent.connect", "agent.revoke":
		return c.callAgentDevice(ctx, proof, agentops.DeviceCall{Action: method[len("agent."):], Client: p.Client})
	case "agent.next":
		return c.callAgentDevice(ctx, proof, agentops.DeviceCall{Action: "next"})
	case "agent.deny":
		return c.callAgentDevice(ctx, proof, agentops.DeviceCall{Action: "deny", Request: p.Request})
	case "agent.approve":
		if !agentops.ValidRepository(p.Request.Repository) || p.Request.Expires <= time.Now().Unix() || p.EntryID == "" {
			return nil, proto.Errorf("AGENT_EXPIRED", "request expired or invalid")
		}
		_, err := c.callAgentDevice(ctx, proof, agentops.DeviceCall{Action: "claim", Request: p.Request})
		if err != nil {
			return nil, err
		}
		result := agentops.Result{ID: p.Request.ID, State: "failed"}
		result.Repository, err = executeGitHub(ctx, current, p.EntryID, p.Request.Repository)
		if err == nil {
			result.State = "done"
		}
		_, completeErr := c.callAgentDevice(ctx, proof, agentops.DeviceCall{Action: "complete", Request: p.Request, Result: result})
		return result, completeErr
	default:
		return nil, proto.Errorf(proto.CodeInvalid, "unknown agent method")
	}
}

func executeGitHub(ctx context.Context, v *vault.Vault, id, repository string) (*agentops.Repository, error) {
	entry, err := v.Get(id)
	if err != nil || entry.InTrash || entry.Kind != vault.KindAPIKey || !agentops.GitHubURL(entry.URL) ||
		(entry.ExpiresAt > 0 && entry.ExpiresAt <= time.Now().Unix()) {
		return nil, errors.New("a current GitHub API credential is required")
	}
	return agentops.GitHubRepository(ctx, repository, entry.Password)
}
