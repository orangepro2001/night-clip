//go:build windows

package pipe

import (
	"fmt"
	"os"
	"syscall"
	"testing"
	"time"
)

func testAddress(t *testing.T) string {
	return fmt.Sprintf(`\\.\pipe\clipbridge-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
}

// Wine does not implement FILE_FLAG_FIRST_PIPE_INSTANCE, which Windows documents as
// failing the creation of any further instance of the name with ERROR_ACCESS_DENIED.
// The assertion stays in force on real Windows.
func skipIfNoSquatProtection(t *testing.T) {
	if syscall.NewLazyDLL("kernel32.dll").NewProc("wine_get_unix_file_name").Find() == nil {
		t.Skip("running under Wine, which does not enforce FILE_FLAG_FIRST_PIPE_INSTANCE")
	}
}
