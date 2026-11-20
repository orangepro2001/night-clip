package markdown

import (
	"strings"
)

func kindName(k nodeKind) string {
	switch k {
	case kHeading:
		return "heading"
	case kPara:
		return "paragraph"
	case kCode:
		return "code"
	case kQuote:
		return "quote"
	case kList:
		return "list"
	case kTable:
		return "table"
	case kHR:
		return "hr"
	case kHTML:
		return "html"
	}
	return "unknown"
}

// canonicalOnce parses preprocessed text and serialises it in canonical form
// (one pass; Normalize iterates it to a fixed point).
func canonicalOnce(s string) string {
	doc := parseBlocks(s, 0)
	lines := joinBlocks(doc.kids, true)
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// joinBlocks serialises sibling blocks, with a blank line between them when
// blank is set.
func joinBlocks(kids []*node, blank bool) []string {
	var out []string
	for i, k := range kids {
		if k.kind == kList {
			k.alt = i > 0 && kids[i-1].kind == kList && kids[i-1].ordered == k.ordered && !kids[i-1].alt
		}
		if i > 0 && blank {
			out = append(out, "")
		}
		if !blank && i > 0 && k.kind == kHR && kids[i-1].kind == kPara {
			// "---" directly below a paragraph would be a setext underline.
			out = append(out, "***")
			continue
		}
		out = append(out, k.canonLines()...)
	}
	return out
}

func (n *node) raw() string { return strings.Join(n.canonLines(), "\n") }

// canonLines returns the canonical lines of n, without any container prefix.
// The result is cached and must not be modified.
func (n *node) canonLines() []string {
	if n.canonDone {
		return n.canon
	}
	var out []string
	switch n.kind {
	case kPara:
		out = paragraphLines(n)
	case kHeading:
		out = []string{headingLine(n)}
	case kHR:
		out = []string{"---"}
	case kCode:
		out = codeLines(n)
	case kHTML, kUnknown:
		out = append([]string(nil), n.lines...)
	case kQuote:
		inner := joinBlocks(n.kids, true)
		if len(inner) == 0 {
			out = []string{">"}
			break
		}
		out = make([]string, len(inner))
		for i, l := range inner {
			if l == "" {
				out[i] = ">"
			} else {
				out[i] = "> " + l
			}
		}
	case kList:
		out = listLines(n)
	case kTable:
		out = tableLines(n)
	}
	n.canon, n.canonDone = out, true
	return out
}

// ---- paragraphs and headings ----

func paragraphLines(n *node) []string {
	text := trimRightST(strings.Join(n.lines, "\n"))
	lines := strings.Split(canonInline(text, false), "\n")
	if len(lines) < 2 {
		return lines
	}
	need := false
	for _, l := range lines[1:] {
		if l != "" && maybeSpecial(l[0]) {
			need = true
			break
		}
	}
	if !need || standaloneParagraph(lines, n.depth) {
		return lines
	}
	// A continuation line that now looks like a block start: indent it so it
	// stays paragraph text (indented code cannot interrupt a paragraph).
	ind := make([]string, len(lines))
	ind[0] = lines[0]
	for i := 1; i < len(lines); i++ {
		ind[i] = "    " + lines[i]
	}
	return ind
}

func standaloneParagraph(lines []string, depth int) bool {
	doc := parseBlocks(strings.Join(lines, "\n"), depth)
	return len(doc.kids) == 1 && doc.kids[0].kind == kPara && len(doc.kids[0].lines) == len(lines)
}

func headingLine(n *node) string {
	text := canonInline(n.lines[0], false)
	// A trailing # run preceded by a space (or standing alone) would be read
	// as a closing sequence: escape its first character.
	k := len(text)
	for k > 0 && text[k-1] == '#' {
		k--
	}
	if k < len(text) && (k == 0 || isSpaceTab(text[k-1])) {
		text = text[:k] + `\` + text[k:]
	}
	prefix := strings.Repeat("#", n.level)
	if text == "" {
		return prefix
	}
	return prefix + " " + text
}

// ---- code ----

func longestRun(lines []string, ch byte) int {
	best := 0
	for _, l := range lines {
		cur := 0
		for i := 0; i < len(l); i++ {
			if l[i] == ch {
				cur++
				if cur > best {
					best = cur
				}
			} else {
				cur = 0
			}
		}
	}
	return best
}

func codeLines(n *node) []string {
	ch := byte('`')
	if strings.IndexByte(n.info, '`') >= 0 {
		ch = '~'
	}
	l := longestRun(n.lines, ch) + 1
	if l < 3 {
		l = 3
	}
	fence := strings.Repeat(string(ch), l)
	out := make([]string, 0, len(n.lines)+2)
	open := fence + n.info
	if n.info != "" && n.info[0] == ch {
		// An info string starting with the fence character would merge into
		// the fence.
		open = fence + " " + n.info
	}
	out = append(out, open)
	out = append(out, n.lines...)
	return append(out, fence)
}

// ---- lists ----

// endsWithParagraph reports whether the last leaf of n, through containers,
// is a paragraph (which a following line could lazily continue).
func endsWithParagraph(n *node) bool {
	for n != nil {
		switch n.kind {
		case kPara:
			return true
		case kQuote, kList, kItem:
			n = n.lastChild()
		default:
			return false
		}
	}
	return false
}

// interruptsParagraph reports whether a block of this kind can start right
// below a paragraph without a blank line.
func interruptsParagraph(n *node) bool {
	switch n.kind {
	case kHeading, kHR, kCode, kQuote:
		return true
	case kList:
		first := n.kids[0]
		return len(first.kids) > 0 && (!n.ordered || n.start == 1)
	case kHTML:
		return n.htmlType != 7
	}
	return false
}

// lazyHazard reports whether a tight list cannot be written without blank
// lines: a paragraph that follows a quote or list directly would be taken as a
// lazy continuation of it. Such a list is written (and so read back) loose.
func lazyHazard(n *node) bool {
	if !n.tight {
		return false
	}
	for _, item := range n.kids {
		for i := 1; i < len(item.kids); i++ {
			prev := item.kids[i-1]
			if (prev.kind == kQuote || prev.kind == kList) && endsWithParagraph(prev) && !interruptsParagraph(item.kids[i]) {
				return true
			}
		}
	}
	return false
}

func listLines(n *node) []string {
	var out []string
	loose := !n.tight || lazyHazard(n)
	for idx, item := range n.kids {
		if idx > 0 && loose {
			out = append(out, "")
		}
		marker := item.marker
		switch {
		case n.ordered && n.alt:
			marker += ")"
		case n.ordered:
			marker += "."
		case n.alt:
			marker = "*"
		default:
			marker = "-"
		}
		body := joinBlocks(item.kids, loose)
		if len(body) == 0 {
			out = append(out, marker)
			continue
		}
		indent := strings.Repeat(" ", len(marker)+1)
		first := marker + " " + body[0]
		if marker == "-" && body[0] == "---" {
			// "- ---" would read as a thematic break; "- ***" does not.
			first = "- ***"
		} else if isThematic(first) {
			// Any other lookalike: start the content on the next line.
			out = append(out, marker)
			first = indent + body[0]
		}
		out = append(out, first)
		for _, l := range body[1:] {
			if l == "" {
				out = append(out, "")
			} else {
				out = append(out, indent+l)
			}
		}
	}
	return out
}

// ---- tables ----

func delimCell(a string) string {
	switch a {
	case "left":
		return ":---"
	case "right":
		return "---:"
	case "center":
		return ":---:"
	}
	return "---"
}

func tableRow(cells []string, ncol int) string {
	var b strings.Builder
	b.WriteByte('|')
	for i := 0; i < ncol; i++ {
		c := ""
		if i < len(cells) {
			c = canonInline(cells[i], true)
		}
		b.WriteByte(' ')
		b.WriteString(c)
		b.WriteString(" |")
	}
	return b.String()
}

func tableLines(n *node) []string {
	ncol := len(n.align)
	out := make([]string, 0, len(n.lines)+2)
	out = append(out, tableRow(splitRow(n.header), ncol))
	var d strings.Builder
	d.WriteByte('|')
	for _, a := range n.align {
		d.WriteString(" " + delimCell(a) + " |")
	}
	out = append(out, d.String())
	for _, l := range n.lines {
		out = append(out, tableRow(splitRow(l), ncol))
	}
	return out
}
