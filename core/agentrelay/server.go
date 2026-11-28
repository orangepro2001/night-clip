// Package agentrelay routes authenticated, short-lived requests to an approved
// MoonPassword device. It does not receive a vault, unlock key, or API credential.
package agentrelay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"moonpassword/core/agentops"
)

const (
	requestLife    = 2 * time.Minute
	connectionLife = 30 * 24 * time.Hour
	maxConnections = 4096
)

type connection struct {
	Subject string
	Device  string
	Client  string
	Expires int64
}

type job struct {
	agentops.Request
	owner  string
	result agentops.Result
}

type contextKey struct{}

type Server struct {
	mux          *http.ServeMux
	authenticate func(context.Context, string) (string, error)
	path         string
	now          func() time.Time
	mu           sync.Mutex
	connections  map[string]connection
	jobs         map[string]*job
}

func New(dir string, authenticate func(context.Context, string) (string, error)) (*Server, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s := &Server{mux: http.NewServeMux(), authenticate: authenticate, path: filepath.Join(dir, "connections.json"),
		now: time.Now, connections: map[string]connection{}, jobs: map[string]*job{}}
	data, err := os.ReadFile(s.path)
	if err == nil {
		if err = json.Unmarshal(data, &s.connections); err != nil || s.connections == nil || len(s.connections) > maxConnections {
			return nil, errors.New("invalid agent connection store")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "MoonPassword", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "request_github_repository",
		Description: "Request read-only GitHub repository metadata from the MoonPassword device. The user selects a credential and approves the request in Windows. Save the returned request ID and check it after the user has approved.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false)}}, s.enqueue)
	mcp.AddTool(server, &mcp.Tool{Name: "check_request",
		Description: "Check your MoonPassword request by ID after the user has approved it. If still pending, stop and ask the user to approve it in the MoonPassword Windows app.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: ptr(false)}}, s.check)
	mcpHTTP := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	s.mux.Handle("/mcp", http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		hash := digest(token)
		s.mu.Lock()
		c, ok := s.connections[hash]
		s.mu.Unlock()
		if !strings.HasPrefix(token, "mpm_") || !ok || c.Expires <= s.now().Unix() {
			w.Header().Set("WWW-Authenticate", `Bearer realm="MoonPassword"`)
			http.Error(w, "MoonPassword connector token required", http.StatusUnauthorized)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		mcpHTTP.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, hash)))
	})))
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	s.mux.HandleFunc("POST /device", s.device)
	return s, nil
}
func ptr(b bool) *bool { return &b }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.mux.ServeHTTP(w, r)
}

func bearer(r *http.Request) string {
	name, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(name, "Bearer") || len(token) > 8192 {
		return ""
	}
	return token
}
func digest(v string) string {
	b := sha256.Sum256([]byte(v))
	return hex.EncodeToString(b[:])
}
func randomToken() string {
	var b [32]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

var devicePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type repositoryArgs struct {
	Repository string `json:"repository" jsonschema:"GitHub repository in owner/name form"`
}
type statusArgs struct {
	ID string `json:"id" jsonschema:"Request ID returned by request_github_repository"`
}

func (s *Server) enqueue(ctx context.Context, _ *mcp.CallToolRequest, a repositoryArgs) (*mcp.CallToolResult, agentops.Result, error) {
	if !agentops.ValidRepository(a.Repository) {
		return nil, agentops.Result{}, errors.New("use a GitHub repository in owner/name form")
	}
	owner, _ := ctx.Value(contextKey{}).(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.prune(now.Unix())
	c, ok := s.connections[owner]
	if !ok || c.Expires <= now.Unix() {
		return nil, agentops.Result{}, errors.New("connection expired or revoked")
	}
	for _, j := range s.jobs {
		if j.owner == owner && (j.result.State == "pending" || j.result.State == "running") {
			if strings.EqualFold(j.Repository, a.Repository) {
				return nil, j.result, nil
			}
			return nil, agentops.Result{}, errors.New("another repository request is already awaiting a decision")
		}
	}
	if len(s.jobs) >= maxConnections {
		return nil, agentops.Result{}, errors.New("request limit reached")
	}
	id := rand.Text()
	j := &job{Request: agentops.Request{ID: id, Client: c.Client, Repository: a.Repository, Expires: now.Add(requestLife).Unix()},
		owner: owner, result: agentops.Result{ID: id, State: "pending"}}
	s.jobs[id] = j
	return nil, j.result, nil
}

func (s *Server) check(ctx context.Context, _ *mcp.CallToolRequest, a statusArgs) (*mcp.CallToolResult, agentops.Result, error) {
	owner, _ := ctx.Value(contextKey{}).(string)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prune(s.now().Unix())
	j := s.jobs[a.ID]
	if c, ok := s.connections[owner]; !ok || c.Expires <= s.now().Unix() || j == nil || j.owner != owner {
		return nil, agentops.Result{ID: a.ID, State: "expired_or_unknown"}, nil
	}
	return nil, j.result, nil
}

func (s *Server) prune(now int64) {
	for k, j := range s.jobs {
		if j.Expires <= now {
			delete(s.jobs, k)
		}
	}
}

func (s *Server) device(w http.ResponseWriter, r *http.Request) {
	token, device := bearer(r), r.Header.Get("X-MoonPassword-Device")
	if token == "" || !devicePattern.MatchString(device) {
		http.Error(w, "device sign-in required", http.StatusUnauthorized)
		return
	}
	sub, err := s.authenticate(r.Context(), token)
	if err != nil || sub == "" {
		http.Error(w, "device sign-in required", http.StatusUnauthorized)
		return
	}
	var p agentops.DeviceCall
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil || dec.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid device request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reply, status := s.handle(sub, digest(device), p)
	if status != http.StatusOK {
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}

func (s *Server) handle(subject, device string, p agentops.DeviceCall) (agentops.DeviceReply, int) {
	now := s.now()
	s.prune(now.Unix())
	var result agentops.DeviceReply
	belongs := func(k string) bool {
		c, ok := s.connections[k]
		return ok && c.Expires > now.Unix() && c.Subject == subject && c.Device == device
	}
	switch p.Action {
	case "connect", "revoke":
		if p.Client != "codex" && p.Client != "grok" {
			return result, http.StatusBadRequest
		}
		next := make(map[string]connection, len(s.connections)+1)
		for k, c := range s.connections {
			if c.Expires > now.Unix() && !(c.Subject == subject && c.Device == device && c.Client == p.Client) {
				next[k] = c
			}
		}
		if p.Action == "connect" {
			if len(next) >= maxConnections {
				return result, http.StatusTooManyRequests
			}
			result.Token = "mpm_" + randomToken()
			result.Expires = now.Add(connectionLife).Unix()
			next[digest(result.Token)] = connection{Subject: subject, Device: device, Client: p.Client, Expires: result.Expires}
		}
		if s.save(next) != nil {
			return agentops.DeviceReply{}, http.StatusServiceUnavailable
		}
		s.connections = next
		for id, j := range s.jobs {
			if _, ok := next[j.owner]; !ok {
				delete(s.jobs, id)
			}
		}
	case "next":
		for _, j := range s.jobs {
			if !belongs(j.owner) || j.result.State != "pending" {
				continue
			}
			if result.Request == nil || j.Expires < result.Request.Expires {
				copy := j.Request
				result.Request = &copy
			}
		}
	case "claim", "deny", "complete":
		j := s.jobs[p.Request.ID]
		if j == nil || !belongs(j.owner) || j.Request != p.Request {
			return result, http.StatusConflict
		}
		switch p.Action {
		case "claim", "deny":
			if j.result.State != "pending" {
				return result, http.StatusConflict
			}
			if p.Action == "claim" {
				j.result.State = "running"
			} else {
				j.result.State = "denied"
			}
		case "complete":
			if j.result.State != "running" || p.Result.ID != j.ID {
				return result, http.StatusConflict
			}
			switch p.Result.State {
			case "done":
				repo := p.Result.Repository
				if repo == nil || !strings.EqualFold(repo.FullName, j.Request.Repository) || len(repo.DefaultBranch) > 256 {
					return result, http.StatusBadRequest
				}
				j.result = p.Result
			case "failed":
				j.result = agentops.Result{ID: j.ID, State: "failed"}
			default:
				return result, http.StatusBadRequest
			}
		}
	default:
		return result, http.StatusBadRequest
	}
	return result, http.StatusOK
}

func (s *Server) save(next map[string]connection) error {
	f, err := os.CreateTemp(filepath.Dir(s.path), "connections-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = json.NewEncoder(f).Encode(next); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), s.path)
}
