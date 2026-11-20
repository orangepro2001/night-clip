package markdown

import (
	"strings"
	"unicode/utf8"
)

// Resource limits. They are part of the canonical form: two devices must
// agree on them, otherwise the same input would normalize differently.
const (
	// maxBlockDepth caps container nesting (quotes, lists, list items).
	// Anything deeper is parsed as literal paragraph text.
	maxBlockDepth = 16
	// maxInlineDepth caps inline nesting (emphasis, links, images). A
	// paragraph that exceeds it is parsed in "plain" mode, where only
	// opaque spans (code, escapes, autolinks, raw HTML) are recognised.
	maxInlineDepth = 16
	// maxNormalizeIters bounds the fixed-point search in Normalize.
	maxNormalizeIters = 8
)

// preprocess applies the byte-level rules that precede parsing: invalid
// UTF-8 becomes U+FFFD, NUL becomes U+FFFD, CRLF and CR become LF, and any
// byte order marks (U+FEFF) are dropped wherever they occur. It is idempotent.
func preprocess(src string) string {
	s := src
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\ufffd")
	}
	if strings.IndexByte(s, '\r') >= 0 {
		s = strings.ReplaceAll(s, "\r\n", "\n")
		s = strings.ReplaceAll(s, "\r", "\n")
	}
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "\ufffd")
	}
	if strings.Contains(s, "\ufeff") {
		s = strings.ReplaceAll(s, "\ufeff", "")
	}
	return s
}

// splitLines splits s on "\n". A trailing newline does not produce a final
// empty line.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func isSpaceTab(c byte) bool { return c == ' ' || c == '\t' }

func isBlank(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isSpaceTab(s[i]) {
			return false
		}
	}
	return true
}

func trimST(s string) string { return strings.Trim(s, " \t") }

func trimRightST(s string) string { return strings.TrimRight(s, " \t") }

func trimLeftST(s string) string { return strings.TrimLeft(s, " \t") }

// trimTrailingBlank drops trailing blank lines from lines.
func trimTrailingBlank(lines []string) []string {
	n := len(lines)
	for n > 0 && isBlank(lines[n-1]) {
		n--
	}
	return lines[:n]
}

// rightTrimLines removes trailing spaces and tabs from every line of s.
func rightTrimLines(s string) string {
	if !strings.ContainsAny(s, " \t") {
		return s
	}
	lines := strings.Split(s, "\n")
	changed := false
	for i, l := range lines {
		t := trimRightST(l)
		if len(t) != len(l) {
			lines[i] = t
			changed = true
		}
	}
	if !changed {
		return s
	}
	return strings.Join(lines, "\n")
}

func isASCIIPunct(c byte) bool {
	return (c >= '!' && c <= '/') || (c >= ':' && c <= '@') || (c >= '[' && c <= '`') || (c >= '{' && c <= '~')
}
