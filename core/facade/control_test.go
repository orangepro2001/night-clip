package facade

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWithoutKeysDropsOnlyTheNamedParams(t *testing.T) {
	got := withoutKeys(`{"to":"dev_x","path":"/tmp/a","fd":7}`, "fd")
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatal(err)
	}
	if _, has := m["fd"]; has || m["to"] != "dev_x" || m["path"] != "/tmp/a" {
		t.Fatalf("params = %v", m)
	}
	if got := withoutKeys(`not json`, "fd"); got != "not json" {
		t.Fatalf("malformed params must pass through unchanged, got %q", got)
	}
}

func TestRedactSensitiveWithholdsTheTextOfSensitiveItems(t *testing.T) {
	list := redactSensitive(`{"ok":true,"result":{"items":[
		{"event_id":"e1","text":"hello","sensitive":false},
		{"event_id":"e2","text":"P@ssw0rd!2024","sensitive":true}]}}`)
	if strings.Contains(list, "P@ssw0rd") || !strings.Contains(list, "hello") || !strings.Contains(list, `"redacted":true`) {
		t.Fatalf("list = %s", list)
	}
	one := redactSensitive(`{"ok":true,"result":{"event_id":"e2","text":"P@ssw0rd!2024","sensitive":true}}`)
	if strings.Contains(one, "P@ssw0rd") || !strings.Contains(one, `"redacted":true`) {
		t.Fatalf("get = %s", one)
	}
	plain := redactSensitive(`{"ok":true,"result":{"event_id":"e1","text":"hello","sensitive":false}}`)
	if !strings.Contains(plain, "hello") || strings.Contains(plain, "redacted") {
		t.Fatalf("plain = %s", plain)
	}
	fail := `{"ok":false,"error":{"code":"INVALID"}}`
	if redactSensitive(fail) != fail {
		t.Fatal("errors must pass through unchanged")
	}
}
