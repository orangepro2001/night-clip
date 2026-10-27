package notes

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"nightclip/core/notes/markdown"
	"nightclip/core/proto"
	"nightclip/core/store"
)

const (
	// MaxBodyBytes is the largest normalized body a note may have (notes/docs/architecture.md §4).
	MaxBodyBytes = 1 << 20
	// MaxTitleBytes bounds a title.
	MaxTitleBytes = 1024
	// maxInputBytes bounds the raw text a caller may hand in before it is normalized.
	maxInputBytes = 4 << 20
	// MergeOrigin is the origin_device of a revision made by merging, not by a person.
	MergeOrigin = "merge"
	maxParents  = 16
	revPrefix   = "r_"
	notePrefix  = "nt_"
)

var (
	noteIDRe = regexp.MustCompile(`^nt_[a-z2-7]{26}$`)
	revIDRe  = regexp.MustCompile(`^r_[0-9a-f]{32}$`)
	deviceRe = regexp.MustCompile(`^dev_[a-z2-7]{26}$`)

	idEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)
)

// newNoteID returns a fresh random note id ("nt_" + 26 base32 characters, like the device ids).
func newNoteID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("notes: no randomness: " + err.Error())
	}
	return notePrefix + idEncoding.EncodeToString(b[:])
}

// deriveNoteID is the id of the conflict copy of one set-aside edit: a function of what was set aside,
// so every device that sets the same edit aside makes the same note and the copies collapse into one.
func deriveNoteID(parts ...string) string {
	h := sha256.New()
	h.Write([]byte("clipbridge.notes.conflict.v1"))
	for _, p := range parts {
		fmt.Fprintf(h, "\x00%d:%s", len(p), p)
	}
	return notePrefix + idEncoding.EncodeToString(h.Sum(nil)[:16])
}

// RevID is the content-derived id of a revision (notes/docs/sync-model.md §2): every device computes
// the same id for the same revision, so receiving it twice is a no-op. It does not include the id
// itself or the body hash (derived from the body).
func RevID(r store.NoteRev) string {
	parents := append([]string{}, r.Parents...)
	sort.Strings(parents)
	canon, err := proto.Canonical(map[string]any{
		"note_id":        r.NoteID,
		"parents":        parents,
		"title":          r.Title,
		"body":           r.Body,
		"format_version": r.FormatVersion,
		"origin_device":  r.OriginDevice,
		"created_at":     r.CreatedAt,
		"deleted":        r.Deleted,
		"conflict_of":    r.ConflictOf,
	})
	if err != nil {
		panic("notes: canonical revision: " + err.Error()) // only strings, ints and bools go in
	}
	sum := sha256.Sum256(append([]byte("clipbridge.notes.rev.v1\x00"), canon...))
	return revPrefix + hex.EncodeToString(sum[:16])
}

// seal fills in the derived fields of a revision.
func seal(r store.NoteRev) store.NoteRev {
	sort.Strings(r.Parents)
	r.BodyHash = markdown.Hash(r.Body)
	r.RevID = RevID(r)
	return r
}

// normTitle is the canonical form of a title: one line, no control characters, trimmed. It is
// idempotent, which the wire check relies on.
func normTitle(s string) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// cutTitle shortens a title to max bytes without splitting a character.
func cutTitle(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut])
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// normBody normalizes a body and enforces the size limits.
func normBody(raw string) (string, error) {
	if len(raw) > maxInputBytes {
		return "", proto.Errorf(proto.CodeTooLarge, "the note is too large")
	}
	body := markdown.Normalize(raw)
	if len(body) > MaxBodyBytes {
		return "", proto.Errorf(proto.CodeTooLarge, "the note is larger than %d bytes", MaxBodyBytes)
	}
	return body, nil
}

func checkTitle(raw string) (string, error) {
	if len(raw) > 4*MaxTitleBytes {
		return "", proto.Errorf(proto.CodeTooLarge, "the title is too long")
	}
	t := normTitle(raw)
	if len(t) > MaxTitleBytes {
		return "", proto.Errorf(proto.CodeTooLarge, "the title is longer than %d bytes", MaxTitleBytes)
	}
	return t, nil
}

// WireRev is a revision as it travels between devices.
type WireRev struct {
	RevID         string   `json:"rev_id"`
	NoteID        string   `json:"note_id"`
	Parents       []string `json:"parents"`
	Title         string   `json:"title"`
	Body          string   `json:"body"`
	FormatVersion int      `json:"format_version"`
	OriginDevice  string   `json:"origin_device"`
	CreatedAt     int64    `json:"created_at"`
	Deleted       bool     `json:"deleted"`
	ConflictOf    string   `json:"conflict_of,omitempty"`
}

func toWire(r store.NoteRev) WireRev {
	parents := r.Parents
	if parents == nil {
		parents = []string{}
	}
	return WireRev{RevID: r.RevID, NoteID: r.NoteID, Parents: parents, Title: r.Title, Body: r.Body, FormatVersion: r.FormatVersion,
		OriginDevice: r.OriginDevice, CreatedAt: r.CreatedAt, Deleted: r.Deleted, ConflictOf: r.ConflictOf}
}

// validateWire checks a revision received from another device and returns it in storage form.
// Nothing a peer sends is trusted: the id must be the hash of the content, the text must already be in
// canonical form (so no device can slip in text that normalizes differently elsewhere), and the sizes
// must be within the limits. A format newer than ours is reported as such, not as garbage.
func validateWire(w WireRev) (store.NoteRev, error) {
	bad := func(format string, args ...any) (store.NoteRev, error) {
		return store.NoteRev{}, proto.Errorf(proto.CodeInvalid, "revision "+format, args...)
	}
	if !revIDRe.MatchString(w.RevID) {
		return bad("id is malformed")
	}
	if !noteIDRe.MatchString(w.NoteID) {
		return bad("note id is malformed")
	}
	if w.FormatVersion > markdown.FormatVersion {
		return store.NoteRev{}, proto.Errorf(proto.CodeUnsupportedFormat, "revision format %d is newer than this app understands (%d)", w.FormatVersion, markdown.FormatVersion)
	}
	if w.FormatVersion < 1 {
		return bad("format is malformed")
	}
	if w.OriginDevice != MergeOrigin && !deviceRe.MatchString(w.OriginDevice) {
		return bad("origin is malformed")
	}
	if w.CreatedAt < 0 {
		return bad("time is malformed")
	}
	if w.ConflictOf != "" && (!noteIDRe.MatchString(w.ConflictOf) || w.ConflictOf == w.NoteID) {
		return bad("conflict marker is malformed")
	}
	if len(w.Parents) > maxParents {
		return bad("has too many parents")
	}
	for i, p := range w.Parents {
		if !revIDRe.MatchString(p) || p == w.RevID || i > 0 && p <= w.Parents[i-1] {
			return bad("parents are malformed")
		}
	}
	if len(w.Title) > MaxTitleBytes || len(w.Body) > MaxBodyBytes {
		return store.NoteRev{}, proto.Errorf(proto.CodeTooLarge, "revision is too large")
	}
	if normTitle(w.Title) != w.Title {
		return bad("title is not in canonical form")
	}
	if markdown.Normalize(w.Body) != w.Body {
		return bad("body is not in canonical form")
	}
	r := store.NoteRev{RevID: w.RevID, NoteID: w.NoteID, Parents: append([]string(nil), w.Parents...), Title: w.Title, Body: w.Body,
		BodyHash: markdown.Hash(w.Body), FormatVersion: w.FormatVersion, OriginDevice: w.OriginDevice, CreatedAt: w.CreatedAt,
		Deleted: w.Deleted, ConflictOf: w.ConflictOf}
	if RevID(r) != w.RevID {
		return bad("id does not match its content")
	}
	return r, nil
}
