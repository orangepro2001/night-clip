package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"nightclip/core/proto"
	"nightclip/core/update/updatetest"
)

func TestFakeServesTheSameAppAsTheClient(t *testing.T) {
	if updatetest.AppID != AppID {
		t.Fatalf("updatetest.AppID = %q, update.AppID = %q", updatetest.AppID, AppID)
	}
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func payloadBytes(n int, seed byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = seed + byte(i*7)
	}
	return b
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func code(err error) string {
	var pe *proto.Error
	if errors.As(err, &pe) {
		return string(pe.Code)
	}
	return ""
}

func serve(t *testing.T, h http.Handler) string {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s.URL
}

// fakeClient returns a client for platform that trusts the fake's key.
// Downloads retry after a millisecond so failure tests stay fast.
func fakeClient(f *updatetest.Server, base, platform string) *Client {
	return &Client{BaseURL: base, Platform: platform, Keys: []ed25519.PublicKey{f.PublicKey()}, RetryWait: time.Millisecond}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

type events struct {
	mu sync.Mutex
	l  []map[string]any
}

func (e *events) emit(ev map[string]any) { e.mu.Lock(); e.l = append(e.l, ev); e.mu.Unlock() }
func (e *events) types() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []string
	for _, ev := range e.l {
		out = append(out, ev["type"].(string))
	}
	return out
}
func (e *events) count(typ string) int {
	n := 0
	for _, t := range e.types() {
		if t == typ {
			n++
		}
	}
	return n
}
