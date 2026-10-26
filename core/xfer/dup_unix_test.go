//go:build !windows

package xfer

import (
	"os"
	"syscall"
	"testing"
)

// dupFD returns a new descriptor for f: the core takes ownership of what it is
// given (Android's detachFd), so the test hands over a duplicate.
func dupFD(t *testing.T, f *os.File) int {
	t.Helper()
	fd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	return fd
}
