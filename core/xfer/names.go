// Package xfer moves files between paired devices: offers, accept/reject,
// resumable downloads (pull) and uploads (push) over the mutual-TLS HTTPS
// server, SHA-256 verification and disk-space checks (docs/architecture.md §7).
package xfer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxNameBytes = 200

var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// SafeName turns a name chosen by another device into something safe to create
// on any of our file systems: no directory parts, no control characters or the
// characters Windows forbids, no reserved device names, no trailing dots or
// spaces, bounded length. The extension is kept. It never returns "".
func SafeName(name string) string {
	// Only the last element counts, whichever separator the sender's OS used.
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	if !utf8.ValidString(name) {
		name = strings.ToValidUTF8(name, "_")
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r):
			b.WriteByte('_') // control and bidi-override characters can disguise an extension
		case strings.ContainsRune(`<>:"|?*`, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimRight(strings.TrimLeft(b.String(), " "), " .")
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	stem, ext := name, ""
	if i := strings.LastIndex(name, "."); i > 0 {
		stem, ext = name[:i], name[i:]
	}
	if isReserved(name) {
		stem = "_" + stem
	}
	// keep the extension intact when shortening
	if len(ext) > 32 {
		ext = ext[:32]
	}
	for len(stem)+len(ext) > maxNameBytes {
		_, size := utf8.DecodeLastRuneInString(stem)
		stem = stem[:len(stem)-size]
	}
	if stem == "" {
		stem = "file"
	}
	return stem + ext
}

// isReserved reports whether Windows would treat name as a device. The device is
// whatever comes before the FIRST dot (so "CON.tar.gz" and "nul.txt.bak" are devices
// too), ignoring trailing spaces; the superscript digits of COM¹ / LPT² count as digits.
func isReserved(name string) bool {
	base := name
	if i := strings.Index(base, "."); i >= 0 {
		base = base[:i]
	}
	base = strings.ToUpper(strings.TrimRight(base, " "))
	if reservedNames[base] || base == "CONIN$" || base == "CONOUT$" {
		return true
	}
	if len(base) > 3 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		switch strings.TrimPrefix(strings.TrimPrefix(base, "COM"), "LPT") {
		case "¹", "²", "³":
			return true
		}
	}
	return false
}

// UniquePath returns dir/name, or dir/stem (1).ext, (2) ... when that exists.
// It reserves the name by creating an empty file, so two concurrent callers
// cannot choose the same path.
func UniquePath(dir, name string) (string, error) {
	stem, ext := name, ""
	if i := strings.LastIndex(name, "."); i > 0 {
		stem, ext = name[:i], name[i:]
	}
	for n := 0; n < 10000; n++ {
		candidate := name
		if n > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		p := filepath.Join(dir, candidate)
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_ = f.Close()
			return p, nil
		}
		if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("no free file name for %q", name)
}
