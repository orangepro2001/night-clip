package markdown

import (
	"strings"
)

// finalize closes block b and runs its type-specific completion.
func (p *parser) finalize(b *node) {
	above := b.parent
	b.open = false
	switch b.kind {
	case kPara:
		p.finalizePara(b)
	case kCode:
		if b.fenced {
			if len(b.lines) > 0 {
				b.info = trimST(b.lines[0])
				b.lines = b.lines[1:]
			}
		} else {
			b.lines = trimTrailingBlank(b.lines)
		}
	case kHTML, kUnknown:
		b.lines = trimTrailingBlank(b.lines)
		for i, l := range b.lines {
			b.lines[i] = trimRightST(l)
		}
	case kList:
		finalizeList(b)
	case kItem:
		finalizeItem(b)
	}
	p.tip = above
}

func endsWithBlankLine(b *node) bool {
	for b != nil {
		if b.lastLineBlank {
			return true
		}
		if b.kind == kList || b.kind == kItem {
			b = b.lastChild()
		} else {
			break
		}
	}
	return false
}

func finalizeList(b *node) {
	for i, item := range b.kids {
		hasNext := i+1 < len(b.kids)
		if endsWithBlankLine(item) && hasNext {
			b.tight = false
			return
		}
		for j, sub := range item.kids {
			if endsWithBlankLine(sub) && (hasNext || j+1 < len(item.kids)) {
				b.tight = false
				return
			}
		}
	}
}

// finalizeItem detects a GFM task marker at the start of the first paragraph.
// The marker stays in the paragraph's first line (in canonical spelling), so
// that every check made on the paragraph sees the text that will actually be
// written; the item only remembers the state.
func finalizeItem(b *node) {
	if len(b.kids) == 0 || b.kids[0].kind != kPara || len(b.kids[0].lines) == 0 {
		return
	}
	l := b.kids[0].lines[0]
	if len(l) < 5 || l[0] != '[' || l[2] != ']' || !isSpaceTab(l[3]) {
		return
	}
	box := "[ ] "
	switch l[1] {
	case ' ':
		b.task = taskOpen
	case 'x', 'X':
		b.task = taskDone
		box = "[x] "
	default:
		return
	}
	rest := trimLeftST(l[4:])
	if rest == "" {
		b.task = taskNone
		return
	}
	b.kids[0].lines[0] = box + rest
}

// finalizePara pulls leading link reference definitions out of a paragraph
// (they become verbatim unknown blocks) and drops the paragraph if nothing
// remains.
func (p *parser) finalizePara(b *node) {
	defs, rest := extractRefDefs(b.lines)
	if len(defs) == 0 && len(rest) > 0 {
		return
	}
	if len(defs) > 0 && len(rest) > 0 && !startsAsParagraph(canonFirstLine(rest), b.depth) {
		// What follows the definitions only continued the paragraph because
		// block starts cannot interrupt it ("$$", ":::", "2. x", a fence that
		// pairs into a code span, ...). On its own it would be read as a
		// block, so keep the whole paragraph as it is.
		return
	}
	parent := b.parent
	idx := len(parent.kids) - 1
	for idx >= 0 && parent.kids[idx] != b {
		idx--
	}
	if idx < 0 {
		return
	}
	repl := append([]*node(nil), defs...)
	if len(rest) > 0 {
		b.lines = rest
		repl = append(repl, b)
	}
	for _, d := range repl {
		d.parent = parent
		d.depth = parent.depth
	}
	tail := append([]*node(nil), parent.kids[idx+1:]...)
	parent.kids = append(append(parent.kids[:idx], repl...), tail...)
}

// canonFirstLine returns the first line of the canonical form of the given
// paragraph lines, which is the text that will actually be written (trailing
// spaces trimmed, a hard break turned into a backslash, ...).
func canonFirstLine(lines []string) string {
	text := canonInline(trimRightST(strings.Join(lines, "\n")), false)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i]
	}
	return text
}

// startsAsParagraph reports whether line, parsed on its own at the given
// container depth, is a paragraph.
func startsAsParagraph(line string, depth int) bool {
	doc := parseBlocks(line, depth)
	return len(doc.kids) == 1 && doc.kids[0].kind == kPara
}

// extractRefDefs splits leading reference definitions off the paragraph
// lines.
func extractRefDefs(lines []string) ([]*node, []string) {
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "[") || !strings.Contains(lines[0], "]:") {
		return nil, lines
	}
	s := strings.Join(lines, "\n")
	var defs []*node
	pos, used := 0, 0
	for pos < len(s) && s[pos] == '[' {
		end, ok := parseRefDef(s, pos)
		if !ok {
			break
		}
		span := s[pos:end]
		n := strings.Count(span, "\n")
		if !strings.HasSuffix(span, "\n") {
			n++
		}
		d := &node{kind: kUnknown, ukind: uRefDef}
		for _, l := range lines[used : used+n] {
			d.lines = append(d.lines, trimRightST(trimLeftST(l)))
		}
		defs = append(defs, d)
		used += n
		pos = end
	}
	return defs, lines[used:]
}

func skipSpaces(s string, i int) int {
	for i < len(s) && isSpaceTab(s[i]) {
		i++
	}
	return i
}

// skipSpNl skips spaces and at most one line ending.
func skipSpNl(s string, i int) int {
	i = skipSpaces(s, i)
	if i < len(s) && s[i] == '\n' {
		i = skipSpaces(s, i+1)
	}
	return i
}

func eolOrEnd(s string, i int) (int, bool) {
	j := skipSpaces(s, i)
	if j >= len(s) {
		return j, true
	}
	if s[j] == '\n' {
		return j + 1, true
	}
	return j, false
}

// parseLinkDest scans a link destination at s[i:] and returns its end.
func parseLinkDest(s string, i int) (end int, ok bool) {
	if i < len(s) && s[i] == '<' {
		j := i + 1
		for j < len(s) {
			switch s[j] {
			case '\\':
				if j+1 < len(s) && isASCIIPunct(s[j+1]) {
					j++
				}
			case '\n', '<':
				return 0, false
			case '>':
				return j + 1, true
			}
			j++
		}
		return 0, false
	}
	depth := 0
	j := i
	for j < len(s) {
		c := s[j]
		if c == '\\' && j+1 < len(s) && isASCIIPunct(s[j+1]) {
			j += 2
			continue
		}
		if c <= ' ' || c == 0x7f {
			break
		}
		if c == '(' {
			depth++
			if depth > 32 {
				return 0, false
			}
		} else if c == ')' {
			if depth == 0 {
				break
			}
			depth--
		}
		j++
	}
	if depth != 0 {
		return 0, false
	}
	return j, true
}

// parseLinkTitle scans a quoted title at s[i:].
func parseLinkTitle(s string, i int) (end int, ok bool) {
	if i >= len(s) {
		return 0, false
	}
	closer := byte(0)
	switch s[i] {
	case '"':
		closer = '"'
	case '\'':
		closer = '\''
	case '(':
		closer = ')'
	default:
		return 0, false
	}
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		switch {
		case c == '\\' && j+1 < len(s) && isASCIIPunct(s[j+1]):
			j++
		case c == closer:
			return j + 1, true
		case c == '(' && closer == ')':
			return 0, false
		case c == '\n':
			k := skipSpaces(s, j+1)
			if k < len(s) && s[k] == '\n' {
				return 0, false
			}
		}
	}
	return 0, false
}

// parseRefDef recognises one link reference definition starting at s[pos:]
// and returns the position after its last line.
func parseRefDef(s string, pos int) (int, bool) {
	n := len(s)
	i := pos + 1
	start := i
	for i < n && i-start <= 999 {
		c := s[i]
		if c == '\\' && i+1 < n && isASCIIPunct(s[i+1]) {
			i += 2
			continue
		}
		if c == '[' {
			return 0, false
		}
		if c == ']' {
			break
		}
		i++
	}
	if i >= n || s[i] != ']' || strings.TrimSpace(s[start:i]) == "" || i+1 >= n || s[i+1] != ':' {
		return 0, false
	}
	i = skipSpNl(s, i+2)
	if i >= n || s[i] == '\n' {
		return 0, false
	}
	destEnd, ok := parseLinkDest(s, i)
	if !ok || (destEnd == i && s[i] != '<') {
		return 0, false
	}
	if destEnd == i {
		return 0, false
	}
	// Optional title; it must be separated from the destination.
	k := skipSpNl(s, destEnd)
	if k > destEnd && k < n {
		if tEnd, ok := parseLinkTitle(s, k); ok {
			if end, ok := eolOrEnd(s, tEnd); ok {
				return end, true
			}
		}
	}
	if end, ok := eolOrEnd(s, destEnd); ok {
		return end, true
	}
	return 0, false
}

// ---- verbatim "unknown" blocks ----

func isFootnoteDef(s string) bool {
	if !strings.HasPrefix(s, "[^") {
		return false
	}
	for i := 2; i < len(s); i++ {
		switch c := s[i]; {
		case c == ']':
			return i > 2 && i+1 < len(s) && s[i+1] == ':'
		case c == ' ' || c == '\t' || c == '[':
			return false
		}
	}
	return false
}

func startUnknown(p *parser, c *node) int {
	if p.indented || p.blank || !p.nonInterrupting(c) {
		return 0
	}
	rest := p.ln[p.nextNonspace:]
	var kind uint8
	count := 0
	switch {
	case isFootnoteDef(rest):
		kind = uFootnote
	case strings.HasPrefix(rest, "$$"):
		kind = uMath
	case strings.HasPrefix(rest, ":::"):
		kind = uContainer
		for count < len(rest) && rest[count] == ':' {
			count++
		}
	default:
		return 0
	}
	p.closeUnmatchedBlocks()
	b := p.addChild(kUnknown)
	b.ukind, b.ucount = kind, count
	p.advanceNextNonspace()
	return 2
}

func (p *parser) contUnknown(n *node) int {
	if n.ukind == uFootnote && !p.blank && p.indent < 4 {
		return 1
	}
	return 0
}

func (p *parser) afterUnknownLine(n *node) {
	line := trimST(p.ln[p.offset:])
	switch n.ukind {
	case uMath:
		if len(n.lines) == 1 {
			if strings.Contains(line[2:], "$$") {
				p.finalize(n)
			}
		} else if strings.HasSuffix(line, "$$") {
			p.finalize(n)
		}
	case uContainer:
		if len(n.lines) > 1 && len(line) >= n.ucount && strings.Trim(line, ":") == "" {
			p.finalize(n)
		}
	}
}

// ---- tables ----

// splitRow splits a table row into trimmed cell sources. Pipes split cells
// unless backslash-escaped.
func splitRow(row string) []string {
	s := trimST(row)
	if strings.HasPrefix(s, "|") {
		s = s[1:]
	}
	var cells []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) && isASCIIPunct(s[i+1]) {
				i++
			}
		case '|':
			cells = append(cells, trimST(s[start:i]))
			start = i + 1
		}
	}
	if tail := s[start:]; start < len(s) && !isBlank(tail) || len(cells) == 0 {
		cells = append(cells, trimST(tail))
	}
	return cells
}

// parseDelimRow validates a table delimiter row and returns the alignment of
// each column.
func parseDelimRow(row string) ([]string, bool) {
	if strings.IndexByte(row, '|') < 0 {
		return nil, false
	}
	cells := splitRow(row)
	align := make([]string, len(cells))
	for i, c := range cells {
		if c == "" {
			return nil, false
		}
		left := c[0] == ':'
		right := len(c) > 1 && c[len(c)-1] == ':'
		body := strings.Trim(c, ":")
		if body == "" || strings.Trim(body, "-") != "" {
			return nil, false
		}
		switch {
		case left && right:
			align[i] = "center"
		case left:
			align[i] = "left"
		case right:
			align[i] = "right"
		default:
			align[i] = "none"
		}
	}
	return align, true
}

func startTable(p *parser, c *node) int {
	if p.indented || p.blank || c.kind != kPara || len(c.lines) == 0 {
		return 0
	}
	rest := p.ln[p.nextNonspace:]
	if len(rest) > 1 && rest[0] == '-' && isSpaceTab(rest[1]) {
		return 0 // "- |---|" is a bullet list item, not a delimiter row
	}
	align, ok := parseDelimRow(rest)
	if !ok {
		return 0
	}
	header := c.lines[len(c.lines)-1]
	if len(splitRow(header)) != len(align) {
		return 0
	}
	if _, rest := extractRefDefs(c.lines); len(rest) == 0 {
		return 0 // the would-be header line is a reference definition
	}
	p.closeUnmatchedBlocks()
	c.lines = c.lines[:len(c.lines)-1]
	parent := c.parent
	tbl := &node{kind: kTable, open: true, parent: parent, depth: parent.depth, startLine: p.lineNo,
		align: align, header: header}
	if len(c.lines) == 0 {
		parent.kids[len(parent.kids)-1] = tbl
	} else {
		p.finalize(c)
		parent.kids = append(parent.kids, tbl)
	}
	p.tip = tbl
	p.advanceOffset(len(p.ln)-p.offset, false)
	p.lineConsumed = true
	return 2
}
