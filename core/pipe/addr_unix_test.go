//go:build !windows

package pipe

import (
	"path/filepath"
	"testing"
)

func testAddress(t *testing.T) string { return filepath.Join(t.TempDir(), "cb.sock") }

func skipIfNoSquatProtection(*testing.T) {}
