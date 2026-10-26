//go:build !windows

package facade

import (
	"path/filepath"
	"testing"
)

func controlAddress(t *testing.T) string { return filepath.Join(t.TempDir(), "ctl.sock") }

func skipWithoutSquatProtection(*testing.T) {}
