package agentops

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGitHubActionSendsCredentialOnlyToFixedAPIAndSelectsResultFields(t *testing.T) {
	const sample = "synthetic-credential"
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != "GET" || r.URL.String() != "https://api.github.com/repos/team/project" ||
			r.Header.Get("Authorization") != "Bearer "+sample {
			t.Fatal("wrong destination or credential binding")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(
			`{"full_name":"team/project","default_branch":"main","private":true,"description":"synthetic-credential"}`))}, nil
	})}
	repo, err := githubRepository(context.Background(), client, "team/project", sample)
	if err != nil || repo == nil || !repo.Private {
		t.Fatal("repository metadata unavailable")
	}
	data, _ := json.Marshal(repo)
	if strings.Contains(string(data), sample) {
		t.Fatal("credential in result")
	}
	for _, repo := range []string{"team/..", "team/%2f", "https://example.com/private", "team/project?extra=1"} {
		if _, err := githubRepository(context.Background(), client, repo, sample); err == nil {
			t.Fatal("bad repository target accepted")
		}
	}
	for _, u := range []string{"https://impostor.github.com", "http://github.com", "https://github.com@evil.example", "https://api.github.com:8443"} {
		if GitHubURL(u) {
			t.Fatal("accepted non-GitHub credential URL")
		}
	}
}
