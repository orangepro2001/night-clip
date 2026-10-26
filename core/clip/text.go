// Package clip is the clipboard policy layer: what is worth sending, what is
// an echo of something we just wrote ourselves, how a received text should be
// presented (notify / manual / auto) and what the application did with it.
// It owns no I/O: the shells read and write the real clipboard.
// See docs/architecture.md §6.
package clip

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"nightclip/core/proto"
)

// Normalize makes clipboard text comparable across platforms: invalid UTF-8 is
// replaced, NUL bytes dropped, and every line ending becomes "\n" (Windows
// reports "\r\n", Android "\n"; without this a text applied on one side would
// not be recognised as its own echo when read back).
func Normalize(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	s = strings.ReplaceAll(s, "\x00", "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// Hash identifies a normalized text for echo detection.
func Hash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Decision is how the shell should treat an incoming text.
const (
	DecisionNotify = "notify" // tell the user, never touch the clipboard
	DecisionManual = "manual" // tell the user; the clipboard changes only on their action
	DecisionAuto   = "auto"   // write it (after the shell's own safety check)
)

// Decide is the policy table of docs/architecture.md §6.3. Sensitive content
// never reaches DecisionAuto, whatever the peer setting says.
func Decide(applyMode string, sensitive bool) string {
	switch applyMode {
	case "auto":
		if sensitive {
			return DecisionManual
		}
		return DecisionAuto
	case "manual":
		return DecisionManual
	default: // "notify", "", anything unknown: the safe choice
		return DecisionNotify
	}
}

var (
	jwtRe      = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*$`)
	otpRe      = regexp.MustCompile(`^\d{6,8}$`)
	tokenPatRe = regexp.MustCompile(`(?:\bAKIA[0-9A-Z]{16}\b|\bgh[pousr]_[A-Za-z0-9]{30,}|\bgithub_pat_[A-Za-z0-9_]{30,}|\bsk-[A-Za-z0-9_-]{20,}|\bxox[abprs]-[A-Za-z0-9-]{10,}|\bAIza[0-9A-Za-z_-]{35}\b|\bglpat-[A-Za-z0-9_-]{20,})`)
)

// LooksSensitive guesses whether a text is a secret: private keys, well-known
// token formats, JWTs, one-time codes, and single "words" that have the shape
// of a password or a random token. A wrong guess only costs convenience (no
// auto-apply, no preview), never data.
func LooksSensitive(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" || len(t) > 8192 {
		return false
	}
	if strings.Contains(t, "-----BEGIN ") && strings.Contains(t, "PRIVATE KEY") {
		return true
	}
	if tokenPatRe.MatchString(t) {
		return true
	}
	if strings.ContainsAny(t, " \t\n") {
		return false // prose, commands, code: not a lone secret
	}
	if otpRe.MatchString(t) || jwtRe.MatchString(t) {
		return true
	}
	if strings.Contains(t, "://") || strings.ContainsAny(t, `/\`) || len(t) < 8 || len(t) > 128 {
		return false // URLs, file paths and very short/long words are not password-shaped
	}
	// A single word mixing at least three character classes, or a long run of
	// high-entropy characters, behaves like a password or an API key.
	var upper, lower, digit, symbol int
	for _, r := range t {
		switch {
		case unicode.IsUpper(r):
			upper++
		case unicode.IsLower(r):
			lower++
		case unicode.IsDigit(r):
			digit++
		default:
			symbol++
		}
	}
	classes := 0
	for _, n := range []int{upper, lower, digit, symbol} {
		if n > 0 {
			classes++
		}
	}
	if classes >= 3 && len(t) >= 8 && digit > 0 {
		return true
	}
	return len(t) >= 24 && classes >= 2 && entropy(t) > 3.6
}

// entropy is the Shannon entropy of s in bits per character.
func entropy(s string) float64 {
	counts := map[rune]float64{}
	n := 0.0
	for _, r := range s {
		counts[r]++
		n++
	}
	h := 0.0
	for _, c := range counts {
		p := c / n
		h -= p * math.Log2(p)
	}
	return h
}

// CheckSize enforces the clipboard payload limit on normalized text.
func CheckSize(normalized string) error {
	if len(normalized) > proto.MaxClipboardBytes {
		return proto.Errorf(proto.CodeTooLarge, "clipboard text is %d bytes; the limit is %d (send it as a file)", len(normalized), proto.MaxClipboardBytes)
	}
	return nil
}
