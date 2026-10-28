package proto

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCanonicalSortsKeysAndStripsWhitespace(t *testing.T) {
	in := []byte(`{ "b": 1, "a": [ true, null, "x" ], "c": { "z": 2, "y": "<&>" } }`)
	got, err := Canonical(json.RawMessage(in))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":[true,null,"x"],"b":1,"c":{"y":"<&>","z":2}}`
	if string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestCanonicalRejectsFloatsAndTrailingData(t *testing.T) {
	if _, err := Canonical(json.RawMessage(`{"a":1.5}`)); err == nil {
		t.Fatal("expected float rejection")
	}
	if _, err := Canonical(json.RawMessage(`{"a":1} {"b":2}`)); err == nil {
		t.Fatal("expected trailing data rejection")
	}
}

func TestContentHashIsKeyOrderIndependent(t *testing.T) {
	a, _ := ContentHash(json.RawMessage(`{"x":1,"y":"é"}`))
	b, _ := ContentHash(json.RawMessage(`{"y":"é","x":1}`))
	if a != b || !strings.HasPrefix(a, "sha256:") || len(a) != len("sha256:")+64 {
		t.Fatalf("hashes differ or malformed: %s %s", a, b)
	}
}

const (
	devA = "dev_abcdefghijklmnopqrstuvwxyz"
	devB = "dev_zyxwvutsrqponmlkjihgfedcba"
)

func validClipboard(t *testing.T, now time.Time) *Envelope {
	t.Helper()
	payload := ClipboardPayload{MimeType: MimeText, Content: "hello", Size: 5}
	pb, _ := json.Marshal(payload)
	h, err := ContentHash(json.RawMessage(pb))
	if err != nil {
		t.Fatal(err)
	}
	return &Envelope{
		Version: 1, EventID: "5b8f0e5c-3f6e-4a8b-9f32-3d4c7f2a1e90", Kind: KindClipboard,
		SourceDeviceID: devA, TargetDeviceID: devB, Sequence: 1,
		CreatedAt: now, ExpiresAt: now.Add(15 * time.Minute),
		ContentHash: h, Payload: pb,
	}
}

func TestEnvelopeValidate(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	if err := validClipboard(t, now).Validate(now); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}

	cases := map[string]struct {
		mutate func(*Envelope)
		code   Code
	}{
		"bad version":   {func(e *Envelope) { e.Version = 2 }, CodeInvalid},
		"bad event id":  {func(e *Envelope) { e.EventID = "nope" }, CodeInvalid},
		"bad kind":      {func(e *Envelope) { e.Kind = "x" }, CodeInvalid},
		"self target":   {func(e *Envelope) { e.TargetDeviceID = e.SourceDeviceID }, CodeInvalid},
		"zero sequence": {func(e *Envelope) { e.Sequence = 0 }, CodeInvalid},
		"expired": {func(e *Envelope) {
			e.CreatedAt, e.ExpiresAt = now.Add(-time.Hour), now.Add(-time.Minute)
		}, CodeExpired},
		"expires before created": {func(e *Envelope) { e.ExpiresAt = e.CreatedAt }, CodeInvalid},
		"hash mismatch": {func(e *Envelope) {
			e.Payload = json.RawMessage(`{"mime_type":"text/plain; charset=utf-8","content":"HELLO","size":5}`)
		}, CodeHashMismatch},
		"bad mime": {func(e *Envelope) {
			e.Payload = json.RawMessage(`{"mime_type":"text/html","content":"hello","size":5}`)
			e.ContentHash, _ = ContentHash(e.Payload)
		}, CodeInvalid},
		"size mismatch": {func(e *Envelope) {
			e.Payload = json.RawMessage(`{"mime_type":"text/plain; charset=utf-8","content":"hello","size":9}`)
			e.ContentHash, _ = ContentHash(e.Payload)
		}, CodeInvalid},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := validClipboard(t, now)
			tc.mutate(e)
			err := e.Validate(now)
			pe, ok := err.(*Error)
			if !ok || pe.Code != tc.code {
				t.Fatalf("got %v, want code %s", err, tc.code)
			}
		})
	}
}

func TestClipboardTooLarge(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	e := validClipboard(t, now)
	big := strings.Repeat("a", MaxClipboardBytes+1)
	pb, _ := json.Marshal(ClipboardPayload{MimeType: MimeText, Content: big, Size: len(big)})
	e.Payload = pb
	e.ContentHash, _ = ContentHash(json.RawMessage(pb))
	if pe, ok := e.Validate(now).(*Error); !ok || pe.Code != CodeTooLarge {
		t.Fatalf("want TOO_LARGE, got %v", pe)
	}
}

func TestPlatforms(t *testing.T) {
	for _, c := range []struct {
		p              string
		known, desktop bool
	}{{"windows", true, true}, {"macos", true, true}, {"android", true, false}, {"linux", false, false}, {"", false, false}, {"MacOS", false, false}} {
		if KnownPlatform(c.p) != c.known || DesktopPlatform(c.p) != c.desktop {
			t.Errorf("%q: known=%v desktop=%v", c.p, KnownPlatform(c.p), DesktopPlatform(c.p))
		}
	}
}
