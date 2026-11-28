package agentrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"moonpassword/core/agentops"
)

type tokenTransport string

func (t tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(t))
	return http.DefaultTransport.RoundTrip(r)
}

func TestTwoMCPClientsAndDeviceApproval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store := t.TempDir()
	verify := func(_ context.Context, tok string) (string, error) {
		switch tok {
		case "alice-session":
			return "alice", nil
		case "bob-session":
			return "bob", nil
		default:
			return "", errors.New("invalid token")
		}
	}
	service, err := New(store, verify)
	if err != nil {
		t.Fatal(err)
	}
	var now atomic.Int64
	now.Store(time.Now().Unix())
	service.now = func() time.Time { return time.Unix(now.Load(), 0) }
	server := httptest.NewServer(service)
	defer server.Close()
	device := strings.Repeat("A", 43)
	other := strings.Repeat("B", 43)
	post := func(tok, device string, p agentops.DeviceCall, expected int) agentops.DeviceReply {
		t.Helper()
		b, _ := json.Marshal(p)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/device", bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("X-MoonPassword-Device", device)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		if res.StatusCode != expected {
			t.Fatalf("%s returned %d, want %d", p.Action, res.StatusCode, expected)
		}
		var reply agentops.DeviceReply
		if expected == 200 && json.NewDecoder(res.Body).Decode(&reply) != nil {
			t.Fatal("invalid device reply")
		}
		return reply
	}
	post("invalid", device, agentops.DeviceCall{Action: "connect", Client: "codex"}, 401)
	codex := post("alice-session", device, agentops.DeviceCall{Action: "connect", Client: "codex"}, 200).Token
	grok := post("alice-session", device, agentops.DeviceCall{Action: "connect", Client: "grok"}, 200).Token
	if codex == "" || grok == "" || codex == grok {
		t.Fatal("each client needs its own token")
	}
	connect := func(name, token string) *mcp.ClientSession {
		t.Helper()
		c := mcp.NewClient(&mcp.Implementation{Name: name, Version: "1"}, nil)
		s, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp",
			HTTPClient: &http.Client{Transport: tokenTransport(token)}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	c, g := connect("fixture Codex", codex), connect("fixture Grok", grok)
	call := func(session *mcp.ClientSession, name string, args any) agentops.Result {
		t.Helper()
		r, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil || r.IsError {
			t.Fatalf("%s failed: %v", name, err)
		}
		b, _ := json.Marshal(r.StructuredContent)
		var result agentops.Result
		if json.Unmarshal(b, &result) != nil {
			t.Fatal("invalid tool response")
		}
		return result
	}
	job := call(c, "request_github_repository", repositoryArgs{Repository: "team/project"})
	if job.State != "pending" || job.ID == "" || job.Repository != nil {
		t.Fatal("missing pending state")
	}
	if got := call(g, "check_request", statusArgs{job.ID}); got.State != "expired_or_unknown" {
		t.Fatal("cross-client result visibility")
	}
	for _, identity := range [][2]string{{"bob-session", device}, {"alice-session", other}} {
		if reply := post(identity[0], identity[1], agentops.DeviceCall{Action: "next"}, 200); reply.Request != nil {
			t.Fatal("cross-device or account request visibility")
		}
	}
	req := post("alice-session", device, agentops.DeviceCall{Action: "next"}, 200).Request
	if req == nil || req.ID != job.ID || req.Client != "codex" {
		t.Fatal("request not routed to creator")
	}
	tampered := *req
	tampered.Repository = "team/wrong"
	post("alice-session", device, agentops.DeviceCall{Action: "claim", Request: tampered}, 409)
	post("alice-session", device, agentops.DeviceCall{Action: "complete", Request: *req}, 409)
	post("alice-session", device, agentops.DeviceCall{Action: "claim", Request: *req}, 200)
	post("alice-session", device, agentops.DeviceCall{Action: "claim", Request: *req}, 409)
	result := agentops.Result{ID: req.ID, State: "done", Repository: &agentops.Repository{FullName: "team/project", DefaultBranch: "main", Private: true}}
	post("alice-session", device, agentops.DeviceCall{Action: "complete", Request: *req, Result: result}, 200)
	if got := call(c, "check_request", statusArgs{req.ID}); got.State != "done" || got.Repository == nil || !got.Repository.Private {
		t.Fatal("approval result unavailable")
	}
	gJob := call(g, "request_github_repository", repositoryArgs{Repository: "team/other"})
	post("alice-session", device, agentops.DeviceCall{Action: "revoke", Client: "codex"}, 200)
	if got := call(g, "check_request", statusArgs{gJob.ID}); got.State != "pending" {
		t.Fatal("revoking Codex interrupted Grok")
	}
	now.Add(180)
	if got := call(g, "check_request", statusArgs{gJob.ID}); got.State != "expired_or_unknown" {
		t.Fatal("request retained after expiry")
	}

	restarted, e := New(store, verify)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := restarted.connections[digest(grok)]; !ok {
		t.Fatal("Grok token hash did not survive restart")
	}
	if _, ok := restarted.connections[digest(codex)]; ok {
		t.Fatal("Codex revocation not persisted")
	}
}
