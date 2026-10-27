package markdown

import (
	"strconv"
	"strings"
)

// canonInline returns the canonical source of an inline run (paragraph text,
// heading text or table cell).
//
// Two rewrites are applied only if re-parsing shows that they keep the
// meaning of the run:
//   - emphasis markers (_ to *, ~ to ~~), which can interact with other
//     delimiters in odd cases such as "*_x_*" or "_a *b_ c*";
//   - a hard break written as a trailing backslash instead of two trailing
//     spaces, which changes what follows an emphasis delimiter and so can
//     change how that delimiter is read. Only in that rare case the two
//     spaces are kept.
//
// Candidates are tried from the most canonical down to the verbatim source
// form, which by definition has the reference meaning.
func canonInline(text string, cell bool) string {
	nodes := parseInlines(text, cell)
	conv := hasConvertible(nodes)
	cand, delimBreak := genInline(nodes, conv, breakCanon)
	if !conv && !delimBreak {
		return finishInline(cand)
	}
	// Fast path: the candidate reads back as exactly the tree it came from.
	if dumpInl(parseInlines(spaceBreaks(cand), cell)) == dumpInl(nodes) {
		return finishInline(cand)
	}
	// Slow path: judge candidates against the verbatim form, so that benign
	// differences (an escape added in front of a line ending) do not veto them.
	verbatim, _ := genInline(nodes, false, breakVerbatim)
	truth := dumpInl(parseInlines(spaceBreaks(verbatim), cell))
	same := func(c string) bool { return dumpInl(parseInlines(spaceBreaks(c), cell)) == truth }
	tried := cand
	if same(cand) {
		return finishInline(cand)
	}
	options := []struct {
		conv bool
		mode breakMode
	}{{conv, breakAfterDelim}, {false, breakCanon}, {false, breakAfterDelim}}
	for _, o := range options {
		c, _ := genInline(nodes, o.conv, o.mode)
		if c == tried {
			continue
		}
		tried = c
		if same(c) {
			return finishInline(c)
		}
	}
	return finishInline(verbatim)
}

// finishInline drops trailing whitespace. The NUL-prefixed line ending marker
// stands for the two spaces of a hard break that has to stay (NUL cannot
// occur in preprocessed input).
func finishInline(s string) string {
	return spaceBreaks(rightTrimLines(s))
}

func spaceBreaks(s string) string { return strings.ReplaceAll(s, "\x00\n", "  \n") }

type breakMode uint8

const (
	breakCanon      breakMode = iota // every hard break is a trailing backslash
	breakAfterDelim                  // space-style breaks right after * _ ~ keep their spaces
	breakVerbatim                    // every hard break keeps the style it was written in
)

func genInline(nodes []*inl, convert bool, mode breakMode) (string, bool) {
	w := inlWriter{convert: convert, mode: mode}
	w.write(nodes)
	return w.b.String(), w.delimBreak
}

func hasConvertible(nodes []*inl) bool {
	for _, n := range nodes {
		switch n.kind {
		case iEm, iStrong:
			if n.marker == '_' {
				return true
			}
		case iDel:
			if n.n == 1 {
				return true
			}
		}
		if len(n.kids) > 0 && hasConvertible(n.kids) {
			return true
		}
	}
	return false
}

func trailingBackslashes(s string) int {
	k := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		k++
	}
	return k
}

type inlWriter struct {
	b          strings.Builder
	convert    bool
	mode       breakMode
	delimBreak bool // a space-style hard break directly follows an emphasis delimiter character
}

func (w *inlWriter) write(nodes []*inl) {
	b := &w.b
	for _, n := range nodes {
		switch n.kind {
		case iText, iCode, iHTML, iAuto:
			b.WriteString(n.src)
		case iEm, iStrong, iDel:
			m, cnt := n.marker, int(n.n)
			if w.convert {
				if n.kind == iDel {
					m, cnt = '~', 2
				} else {
					m = '*'
				}
			}
			for i := 0; i < cnt; i++ {
				b.WriteByte(m)
			}
			w.write(n.kids)
			for i := 0; i < cnt; i++ {
				b.WriteByte(m)
			}
		case iLink, iImage:
			if n.kind == iImage {
				b.WriteByte('!')
			}
			b.WriteByte('[')
			w.write(n.kids)
			b.WriteByte(']')
			b.WriteString(n.tailSrc())
		case iBreak, iSoft:
			cur := b.String()
			// A literal backslash right before the line ending must be
			// escaped, or the line ending would turn into a hard break.
			if trailingBackslashes(cur)%2 == 1 {
				b.WriteByte('\\')
				cur = b.String()
			}
			if n.kind == iBreak {
				last := byte(0)
				if len(cur) > 0 {
					last = cur[len(cur)-1]
				}
				afterDelim := last == '*' || last == '_' || last == '~'
				if n.n == 2 && afterDelim {
					w.delimBreak = true
				}
				if n.n == 2 && (w.mode == breakVerbatim || w.mode == breakAfterDelim && afterDelim) {
					b.WriteString("\x00\n")
					continue
				}
				b.WriteByte('\\')
			}
			b.WriteByte('\n')
		}
	}
}

// dumpInl renders the structure of a tree (ignoring the delimiter characters
// used for emphasis) for equality checks.
func dumpInl(nodes []*inl) string {
	var b strings.Builder
	dumpTo(&b, nodes)
	return b.String()
}

func dumpTo(b *strings.Builder, nodes []*inl) {
	var pend strings.Builder
	flush := func() {
		if pend.Len() > 0 {
			b.WriteString("t")
			b.WriteString(strconv.Quote(pend.String()))
			pend.Reset()
		}
	}
	for _, n := range nodes {
		if n.kind == iText {
			pend.WriteString(n.src)
			continue
		}
		flush()
		switch n.kind {
		case iCode:
			b.WriteString("c")
			if n.multiline {
				b.WriteString("()")
			} else {
				b.WriteString(strconv.Quote(n.codeText()))
			}
		case iHTML:
			b.WriteString("h" + strconv.Quote(n.src))
		case iAuto:
			b.WriteString("a" + strconv.Quote(n.destRaw()))
		case iEm:
			b.WriteString("<e>")
			dumpTo(b, n.kids)
			b.WriteString("</e>")
		case iStrong:
			b.WriteString("<s>")
			dumpTo(b, n.kids)
			b.WriteString("</s>")
		case iDel:
			b.WriteString("<d>")
			dumpTo(b, n.kids)
			b.WriteString("</d>")
		case iLink, iImage:
			b.WriteString("L")
			if n.kind == iImage {
				b.WriteString("I")
			}
			b.WriteString(strconv.Quote(n.destRaw()) + strconv.Quote(n.titleRaw()) + "[")
			dumpTo(b, n.kids)
			b.WriteString("]")
		case iBreak:
			b.WriteString("<br>")
		case iSoft:
			b.WriteString("<sb>")
		}
	}
	flush()
}
