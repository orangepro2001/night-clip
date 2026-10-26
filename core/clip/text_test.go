package clip

import (
	"strings"
	"testing"

	"nightclip/core/proto"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"a\r\nb":           "a\nb",
		"a\rb":             "a\nb",
		"a\r\n\r\nb\r\n":   "a\n\nb\n",
		"nul\x00byte":      "nulbyte",
		"bad\xffutf8":      "bad\uFFFDutf8",
		"plain":            "plain",
		"":                 "",
		"unicode 汉字 🎉":     "unicode 汉字 🎉",
		"mixed\r\nA\nB\rC": "mixed\nA\nB\nC",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	// the point of normalizing: the Windows spelling and the Android spelling hash alike
	if Hash(Normalize("line1\r\nline2")) != Hash(Normalize("line1\nline2")) {
		t.Fatal("CRLF and LF texts must have the same hash")
	}
	if Hash("a") == Hash("b") || !strings.HasPrefix(Hash("a"), "sha256:") {
		t.Fatal("Hash shape")
	}
}

func TestDecideTable(t *testing.T) {
	type c struct {
		mode      string
		sensitive bool
		want      string
	}
	for _, tc := range []c{
		{"notify", false, DecisionNotify}, {"notify", true, DecisionNotify},
		{"manual", false, DecisionManual}, {"manual", true, DecisionManual},
		{"auto", false, DecisionAuto}, {"auto", true, DecisionManual}, // sensitive never auto
		{"", false, DecisionNotify}, {"bogus", false, DecisionNotify}, {"AUTO", false, DecisionNotify},
	} {
		if got := Decide(tc.mode, tc.sensitive); got != tc.want {
			t.Errorf("Decide(%q, %v) = %s, want %s", tc.mode, tc.sensitive, got, tc.want)
		}
	}
}

func TestLooksSensitive(t *testing.T) {
	secrets := []string{
		"-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----",
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_" + strings.Repeat("a1B2c3", 7),
		"github_pat_" + strings.Repeat("x9", 20),
		"sk-" + strings.Repeat("Ab3", 10),
		"xoxb-123456789012-abcdefghij",
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"123456", "00482913", // one-time codes
		"Tr0ub4dor&3", "correct-Horse9", "Hello123", "P@ssw0rd!2024",
		"AIza" + strings.Repeat("Z9_-", 9) + "abc",
		"Authorization: " + "ghp_" + strings.Repeat("q8W", 12), // a token inside a line
		"x7Kp9QmZ2vL4nR8sT1wY6bE3",                             // 24+ random characters
	}
	for _, s := range secrets {
		if !LooksSensitive(s) {
			t.Errorf("%q should look sensitive", s)
		}
	}
	benign := []string{
		"hello world", "https://example.com/path?q=1", "see you at 7pm", "TODO: fix the bug in main.go",
		"192.168.1.5", "12345", "1234567890", "13800138000", // too short / too long to be a code
		"hello", "C:\\Users\\tony\\Documents\\notes.txt", "git commit -m \"fix: handle empty input\"",
		"The quick brown fox jumps over the lazy dog.", "", "   ", "2026-10-26", "kubectl get pods -n production",
		"supercalifragilisticexpialidocious", "https://github.com/orangepro2001/night-clip/blob/main/README.md",
		strings.Repeat("a", 200), "日本語のテキストです", "第一行\n第二行\n第三行",
	}
	for _, s := range benign {
		if LooksSensitive(s) {
			t.Errorf("%q should not look sensitive", s)
		}
	}
	if LooksSensitive(strings.Repeat("Ab1!", 5000)) {
		t.Error("very long texts are never guessed to be secrets (they are documents)")
	}
}

func TestCheckSize(t *testing.T) {
	if err := CheckSize(strings.Repeat("a", proto.MaxClipboardBytes)); err != nil {
		t.Fatal(err)
	}
	if err := CheckSize(strings.Repeat("a", proto.MaxClipboardBytes+1)); err == nil {
		t.Fatal("oversize accepted")
	}
}

func TestValidateSetting(t *testing.T) {
	ok := map[string]string{SettingCapture: "on", SettingSendSensitive: "off", SettingTTL: "900", SettingHistoryLimit: "200", SettingApplyMode: "auto"}
	for k, v := range ok {
		if err := ValidateSetting(k, v); err != nil {
			t.Errorf("%s=%s rejected: %v", k, v, err)
		}
	}
	bad := [][2]string{{SettingCapture, "yes"}, {SettingTTL, "5"}, {SettingTTL, "999999"}, {SettingTTL, "x"}, {SettingHistoryLimit, "1"},
		{SettingApplyMode, "yolo"}, {"unknown", "1"}, {SettingSendSensitive, ""}}
	for _, kv := range bad {
		if err := ValidateSetting(kv[0], kv[1]); err == nil {
			t.Errorf("%s=%s accepted", kv[0], kv[1])
		}
	}
}
