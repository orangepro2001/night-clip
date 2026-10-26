//go:build windows

package xfer

import (
	"os"
	"testing"
)

// Descriptor-backed offers exist for Android only.
func dupFD(t *testing.T, f *os.File) int {
	t.Helper()
	t.Skip("descriptor offers are an Android feature")
	return 0
}
