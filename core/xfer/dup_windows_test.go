//go:build windows

package xfer

import (
	"os"
	"testing"
)

// Descriptor-backed offers exist for Android only. Tests that need one skip
// before they open anything, so there is nothing left to clean up.
func skipWithoutDescriptors(t *testing.T) { t.Skip("descriptor offers are an Android feature") }

func dupFD(t *testing.T, f *os.File) int {
	t.Helper()
	t.Fatal("dupFD is not available on Windows")
	return 0
}
