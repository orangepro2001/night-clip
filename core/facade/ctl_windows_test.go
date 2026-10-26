//go:build windows

package facade

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
)

func controlAddress(t *testing.T) string {
	return fmt.Sprintf(`\\.\pipe\clipbridge-facade-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
}

// Wine does not enforce FILE_FLAG_FIRST_PIPE_INSTANCE (Windows does), so a name
// that is already served can still be listened on there. The assertion stays on
// real Windows.
func skipWithoutSquatProtection(t *testing.T) {
	if syscall.NewLazyDLL("kernel32.dll").NewProc("wine_get_unix_file_name").Find() == nil {
		t.Skip("running under Wine, which does not enforce FILE_FLAG_FIRST_PIPE_INSTANCE")
	}
}
