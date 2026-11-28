package facade

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentCredentialsOnlyReturnsValidGitHubAPIEntries(t *testing.T) {
	n := startNode(t, "Desk", "windows")
	if code := n.errorCode("agent.credentials", nil); code != "VAULT_LOCKED" {
		t.Fatalf("locked credentials returned %s", code)
	}
	vcreate(t, n)
	add := func(kind, title, url, password string) {
		t.Helper()
		n.call("vault.add", map[string]any{"entry": map[string]any{
			"kind": kind, "title": title, "url": url, "password": password,
		}})
	}
	add("apikey", "Opt-in GitHub token", "https://api.github.com/", "synthetic-test-key")
	add("login", "Account password", "https://github.com/", "synthetic-login-password")
	add("apikey", "Other provider", "https://github.com.other.example/", "synthetic-other-key")
	result := n.call("agent.credentials", nil)
	entries := result["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["title"] != "Opt-in GitHub token" {
		t.Fatal("eligible credentials were not filtered correctly")
	}
	safe, _ := json.Marshal(result)
	if strings.Contains(string(safe), "synthetic") || strings.Contains(string(safe), "password") {
		t.Fatal("the credentials picker exposed secret fields")
	}
	n.call("vault.lock", nil)
	if code := n.errorCode("agent.approve", nil); code != "VAULT_LOCKED" {
		t.Fatalf("locked approval returned %s", code)
	}
}
