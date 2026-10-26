//go:build !windows

package pipe

import (
	"os"
	"testing"
)

func TestSocketIsPrivateToTheUser(t *testing.T) {
	addr := testAddress(t)
	l, err := Listen(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	st, err := os.Stat(addr)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, %v; want 0600", st.Mode().Perm(), err)
	}
}

func TestAStaleSocketFileIsReplaced(t *testing.T) {
	addr := testAddress(t)
	if err := os.WriteFile(addr, nil, 0o600); err != nil { // left behind by a crash
		t.Fatal(err)
	}
	l, err := Listen(addr)
	if err != nil {
		t.Fatalf("a stale file blocked Listen: %v", err)
	}
	l.Close()
}
