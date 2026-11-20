package proto

import (
	"errors"
	"testing"
)

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

func TestErrorfKeepsTheCode(t *testing.T) {
	var err error = Errorf(CodeVaultLocked, "locked %d", 1)
	var pe *Error
	if !errors.As(err, &pe) || pe.Code != CodeVaultLocked || pe.Message != "locked 1" {
		t.Fatalf("got %#v", err)
	}
	if err.Error() != "VAULT_LOCKED: locked 1" {
		t.Fatalf("Error() = %q", err.Error())
	}
}
