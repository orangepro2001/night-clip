// Package markdown is the single source of truth for note bodies.
//
// Markdown text is the only authoritative storage format of a note. This
// package normalizes any input to a canonical form (byte-identical on every
// device, because its SHA-256 goes into content-addressed revision ids),
// splits it into top-level blocks (the unit of block-level three-way merge)
// and produces a structured render tree for the Windows and Android shells,
// which therefore never parse Markdown themselves.
//
// Supported syntax is CommonMark 0.31 plus a GFM subset (strikethrough, task
// lists, tables). Raw HTML blocks, footnote definitions, link reference
// definitions, "$$" math blocks and ":::" containers are kept verbatim as
// "html"/"unknown" blocks; inline raw HTML and anything the inline scanner
// cannot make sense of stays literal text. Nothing is ever executed or
// dropped.
//
// Deliberate differences from CommonMark/GFM, all part of the canonical form
// (so every device agrees on them):
//   - Link reference definitions are kept verbatim and never resolved, so
//     reference-style links ("[text][id]", "[id]") stay literal text. Bare
//     URLs are not autolinked; only "<...>" autolinks are.
//   - Container nesting (quotes, lists, items) is capped at 16 levels and
//     inline nesting at 16 levels; anything deeper is literal text.
//   - A multi-line setext heading becomes a one-line ATX heading (lines joined
//     with a space); a '#' that would read as a closing sequence is escaped.
//   - Two adjacent lists of the same kind that CommonMark keeps apart (for
//     example "- a" and "* b" separated by a blank line) are written with
//     different markers ("-" then "*", "1." then "1)") so they stay apart.
//   - A tight list that cannot be written without blank lines (a paragraph
//     directly below a quote or list would become a lazy continuation of it)
//     is written loose.
//   - U+FEFF is removed wherever it occurs; NUL becomes U+FFFD.
//   - Emphasis classification uses the Unicode tables of the Go toolchain, so
//     all devices must be built with the same Go release.
package markdown

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// FormatVersion is the version of the canonical form and the render tree.
//
// It is stored in every note revision and peers refuse revisions of a newer format. Any change that
// alters what Normalize returns for some input (a new rule, a bug fix in the output, a different Go
// release's Unicode tables) changes the bytes a revision id is computed from, so two apps that
// disagree would reject each other's revisions as "not in canonical form". Such a change therefore
// needs a new FormatVersion AND a way for the new version to check older revisions against the old
// rules (core/notes validateWire only knows the current form); until that exists, treat the golden
// tests in this package as the pin of the canonical form and do not change them lightly.
const FormatVersion = 1

// Normalize returns the canonical form of src. It is total (never panics,
// never errors) and idempotent: Normalize(Normalize(x)) == Normalize(x).
// Invalid UTF-8 becomes U+FFFD, CRLF/CR become LF, whitespace-only input
// becomes "", and non-empty output always ends with exactly one "\n".
func Normalize(src string) string {
	s := preprocess(src)
	if isBlank(strings.ReplaceAll(s, "\n", "")) {
		return ""
	}
	return fixedPoint(s)
}

// fixedPoint applies canonicalOnce until the text stops changing. If the
// sequence enters a cycle (which would be a canonicalisation bug), the
// lexicographically smallest member of the cycle is returned, which keeps
// Normalize idempotent and deterministic regardless.
func fixedPoint(s string) string {
	seen := make([]string, 0, maxNormalizeIters)
	cur := s
	for i := 0; i < maxNormalizeIters; i++ {
		next := canonStep(cur)
		if next == cur {
			return cur
		}
		seen = append(seen, cur)
		for j, prev := range seen {
			if next == prev {
				best := next
				for _, c := range seen[j:] {
					if c < best {
						best = c
					}
				}
				return best
			}
		}
		cur = next
	}
	return cur
}

// canonStep is one pass of the fixed-point iteration. The byte-level rules are
// re-applied because dropping leading blank lines can expose a byte order
// mark that was not at the start of the input.
func canonStep(s string) string { return preprocess(safeCanonical(s)) }

// safeCanonical is canonicalOnce with a last-resort panic guard: a bug in the
// parser must never take the process down, and untrusted peers supply
// bodies. On panic the text is returned unchanged apart from trailing
// whitespace and the final newline.
func safeCanonical(s string) (out string) {
	defer func() {
		if recover() != nil {
			out = fallbackText(s)
		}
	}()
	return canonicalOnce(s)
}

func fallbackText(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = trimRightST(l)
	}
	t := strings.Trim(strings.Join(lines, "\n"), "\n")
	if t == "" {
		return ""
	}
	return t + "\n"
}

// Hash returns "sha256:" + lowercase hex of SHA-256 over the bytes of norm
// (norm is assumed to be already normalized).
func Hash(norm string) string {
	sum := sha256.Sum256([]byte(norm))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Block is a top-level block of a note.
type Block struct {
	ID   string `json:"block_id"` // "b_" + first 16 hex chars of sha256(Kind + "\x00" + Raw)
	Kind string `json:"kind"`     // heading, paragraph, code, quote, list, table, hr, html, unknown
	Raw  string `json:"raw"`      // canonical markdown of this block, no leading/trailing newline
}

// BlockID computes the ID of a block of the given kind and raw text.
func BlockID(kind, raw string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + raw))
	return "b_" + hex.EncodeToString(sum[:8])
}

// JoinRaw joins canonical block texts: a blank line between blocks and one
// trailing newline; an empty slice gives "".
func JoinRaw(raws []string) string {
	if len(raws) == 0 {
		return ""
	}
	return strings.Join(raws, "\n\n") + "\n"
}

// normalizedBlocks parses already-normalized text into top-level blocks.
func normalizedBlocks(norm string) (doc *node, blocks []Block) {
	defer func() {
		if recover() != nil {
			doc, blocks = nil, nil
		}
	}()
	doc = parseBlocks(norm, 0)
	joinBlocks(doc.kids, true) // computes canonical lines (and list marker alternation)
	raws := make([]string, len(doc.kids))
	blocks = make([]Block, len(doc.kids))
	for i, k := range doc.kids {
		raws[i] = k.raw()
		kind := kindName(k.kind)
		blocks[i] = Block{ID: BlockID(kind, raws[i]), Kind: kind, Raw: raws[i]}
	}
	if JoinRaw(raws) != norm {
		return nil, nil
	}
	return doc, blocks
}

// Split normalizes src and returns its top-level blocks.
// Invariant: JoinRaw(raws of Split(x)) == Normalize(x).
func Split(src string) []Block {
	norm := Normalize(src)
	if norm == "" {
		return []Block{}
	}
	if _, blocks := normalizedBlocks(norm); blocks != nil {
		return blocks
	}
	// Defensive: the parse of the normal form did not reproduce it. Keep the
	// text intact as one verbatim block rather than breaking the invariant.
	raw := strings.TrimSuffix(norm, "\n")
	return []Block{{ID: BlockID("unknown", raw), Kind: "unknown", Raw: raw}}
}
