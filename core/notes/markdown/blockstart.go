package markdown

import (
	"regexp"
	"strconv"
	"strings"
)

type blockStart func(p *parser, container *node) int

var blockStarts []blockStart

func init() {
	// Order matters (it is the reference implementation's order, with the
	// table and the verbatim-block starts slotted in where they cannot
	// shadow anything).
	blockStarts = []blockStart{
		startQuote, startATX, startFence, startHTML, startSetext, startTable,
		startHR, startUnknown, startList, startIndentedCode,
	}
}

const htmlTagName = `address|article|aside|base|basefont|blockquote|body|caption|center|col|colgroup|dd|details|dialog|dir|div|dl|dt|fieldset|figcaption|figure|footer|form|frame|frameset|h1|h2|h3|h4|h5|h6|head|header|hr|html|iframe|legend|li|link|main|menu|menuitem|nav|noframes|ol|optgroup|option|p|param|search|section|summary|table|tbody|td|tfoot|th|thead|title|tr|track|ul`

const (
	reOpenTag  = `<[A-Za-z][A-Za-z0-9-]*(?:\s+[a-zA-Z_:][a-zA-Z0-9:._-]*(?:\s*=\s*(?:[^"'=<>` + "`" + `\x00-\x20]+|'[^']*'|"[^"]*"))?)*\s*/?>`
	reCloseTag = `</[A-Za-z][A-Za-z0-9-]*\s*>`
)

var reHTMLOpen = [8]*regexp.Regexp{
	nil,
	regexp.MustCompile(`(?i)^<(?:script|pre|textarea|style)(?:\s|>|$)`),
	regexp.MustCompile(`^<!--`),
	regexp.MustCompile(`^<\?`),
	regexp.MustCompile(`^<![A-Za-z]`),
	regexp.MustCompile(`^<!\[CDATA\[`),
	regexp.MustCompile(`(?i)^</?(?:` + htmlTagName + `)(?:\s|/?>|$)`),
	regexp.MustCompile(`^(?:` + reOpenTag + `|` + reCloseTag + `)\s*$`),
}

var reHTMLClose = [6]*regexp.Regexp{
	nil,
	regexp.MustCompile(`(?i)</(?:script|pre|style|textarea)>`),
	regexp.MustCompile(`-->`),
	regexp.MustCompile(`\?>`),
	regexp.MustCompile(`>`),
	regexp.MustCompile(`\]\]>`),
}

// nonInterrupting reports whether a block that may not interrupt a
// paragraph can start here.
func (p *parser) nonInterrupting(c *node) bool {
	return c.kind != kPara && !(!p.allClosed && !p.blank && p.tip.kind == kPara)
}

func startQuote(p *parser, c *node) int {
	if p.indented || p.blank || p.ln[p.nextNonspace] != '>' {
		return 0
	}
	if parentFor(c, kQuote).depth+1 > maxBlockDepth {
		return 0
	}
	p.advanceNextNonspace()
	p.advanceOffset(1, false)
	if p.offset < len(p.ln) && isSpaceTab(p.ln[p.offset]) {
		p.advanceOffset(1, true)
	}
	p.closeUnmatchedBlocks()
	p.addChild(kQuote)
	return 1
}

// atxContent extracts heading text from the text after the opening #s,
// dropping the optional closing sequence.
func atxContent(s string) string {
	s = trimST(s)
	t := strings.TrimRight(s, "#")
	if len(t) == 0 {
		return ""
	}
	if len(t) < len(s) && isSpaceTab(t[len(t)-1]) {
		return trimST(t)
	}
	return s
}

func startATX(p *parser, c *node) int {
	if p.indented || p.blank || p.ln[p.nextNonspace] != '#' {
		return 0
	}
	rest := p.ln[p.nextNonspace:]
	n := 0
	for n < len(rest) && rest[n] == '#' {
		n++
	}
	if n > 6 || (n < len(rest) && !isSpaceTab(rest[n])) {
		return 0
	}
	p.advanceNextNonspace()
	p.advanceOffset(n, false)
	p.closeUnmatchedBlocks()
	h := p.addChild(kHeading)
	h.level = n
	h.lines = []string{atxContent(p.ln[p.offset:])}
	p.advanceOffset(len(p.ln)-p.offset, false)
	p.lineConsumed = true
	return 2
}

func startFence(p *parser, c *node) int {
	if p.indented || p.blank {
		return 0
	}
	rest := p.ln[p.nextNonspace:]
	ch := rest[0]
	if ch != '`' && ch != '~' {
		return 0
	}
	n := 0
	for n < len(rest) && rest[n] == ch {
		n++
	}
	if n < 3 || (ch == '`' && strings.IndexByte(rest[n:], '`') >= 0) {
		return 0
	}
	p.closeUnmatchedBlocks()
	b := p.addChild(kCode)
	b.fenced, b.fenceChar, b.fenceLen, b.fenceOffset = true, ch, n, p.indent
	p.advanceNextNonspace()
	p.advanceOffset(n, false)
	return 2
}

func startHTML(p *parser, c *node) int {
	if p.indented || p.blank || p.ln[p.nextNonspace] != '<' {
		return 0
	}
	s := p.ln[p.nextNonspace:]
	for t := 1; t <= 7; t++ {
		if reHTMLOpen[t].MatchString(s) && (t < 7 || p.nonInterrupting(c)) {
			p.closeUnmatchedBlocks()
			b := p.addChild(kHTML)
			b.htmlType = t
			p.advanceNextNonspace()
			return 2
		}
	}
	return 0
}

func startSetext(p *parser, c *node) int {
	if p.indented || p.blank || c.kind != kPara {
		return 0
	}
	rest := p.ln[p.nextNonspace:]
	ch := rest[0]
	if ch != '=' && ch != '-' {
		return 0
	}
	i := 0
	for i < len(rest) && rest[i] == ch {
		i++
	}
	if !isBlank(rest[i:]) {
		return 0
	}
	p.closeUnmatchedBlocks()
	defs, lines := extractRefDefs(c.lines)
	if len(lines) == 0 {
		return 0
	}
	parent := c.parent
	parent.kids = parent.kids[:len(parent.kids)-1]
	for _, d := range defs {
		d.parent, d.depth = parent, parent.depth
	}
	parent.kids = append(parent.kids, defs...)
	parts := make([]string, len(lines))
	for k, l := range lines {
		parts[k] = trimST(l)
	}
	h := &node{kind: kHeading, open: true, parent: parent, depth: parent.depth, startLine: p.lineNo,
		lines: []string{strings.Join(parts, " ")}, level: 2}
	if ch == '=' {
		h.level = 1
	}
	parent.kids = append(parent.kids, h)
	p.tip = h
	p.advanceOffset(len(p.ln)-p.offset, false)
	p.lineConsumed = true
	return 2
}

func isThematic(s string) bool {
	if s == "" {
		return false
	}
	ch := s[0]
	if ch != '*' && ch != '_' && ch != '-' {
		return false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == ch:
			n++
		case isSpaceTab(s[i]):
		default:
			return false
		}
	}
	return n >= 3
}

func startHR(p *parser, c *node) int {
	if p.indented || p.blank || !isThematic(p.ln[p.nextNonspace:]) {
		return 0
	}
	p.closeUnmatchedBlocks()
	p.addChild(kHR)
	p.advanceOffset(len(p.ln)-p.offset, false)
	p.lineConsumed = true
	return 2
}

func startIndentedCode(p *parser, c *node) int {
	if p.indented && p.tip.kind != kPara && !p.blank {
		p.advanceOffset(4, true)
		p.closeUnmatchedBlocks()
		p.addChild(kCode)
		return 2
	}
	return 0
}

type listData struct {
	ordered      bool
	delim        byte
	start        int
	digits       string
	padding      int
	markerOffset int
}

// parseListMarker is the reference algorithm. It mutates the parser only on
// success.
func parseListMarker(p *parser, c *node) *listData {
	if p.indent >= 4 || p.blank {
		return nil
	}
	rest := p.ln[p.nextNonspace:]
	d := &listData{markerOffset: p.indent}
	mlen := 0
	switch ch := rest[0]; {
	case ch == '*' || ch == '+' || ch == '-':
		d.delim = ch
		mlen = 1
	case ch >= '0' && ch <= '9':
		k := 0
		for k < len(rest) && k < 10 && rest[k] >= '0' && rest[k] <= '9' {
			k++
		}
		if k > 9 || k >= len(rest) || (rest[k] != '.' && rest[k] != ')') {
			return nil
		}
		d.digits = rest[:k]
		d.ordered = true
		d.delim = rest[k]
		d.start, _ = strconv.Atoi(d.digits)
		if c.kind == kPara && d.start != 1 {
			return nil
		}
		mlen = k + 1
	default:
		return nil
	}
	if mlen < len(rest) && !isSpaceTab(rest[mlen]) {
		return nil
	}
	if c.kind == kPara && isBlank(rest[mlen:]) {
		return nil
	}
	p.advanceNextNonspace()
	p.advanceOffset(mlen, true)
	spacesStartCol, spacesStartOffset := p.column, p.offset
	for {
		p.advanceOffset(1, true)
		if !(p.column-spacesStartCol < 5 && p.offset < len(p.ln) && isSpaceTab(p.ln[p.offset])) {
			break
		}
	}
	blankItem := p.offset >= len(p.ln)
	spaces := p.column - spacesStartCol
	if spaces >= 5 || spaces < 1 || blankItem {
		d.padding = mlen + 1
		p.column, p.offset = spacesStartCol, spacesStartOffset
		if p.offset < len(p.ln) && isSpaceTab(p.ln[p.offset]) {
			p.advanceOffset(1, true)
		}
	} else {
		d.padding = mlen + spaces
	}
	return d
}

func startList(p *parser, c *node) int {
	if p.indented {
		return 0
	}
	off, col, pt := p.offset, p.column, p.partialTab
	d := parseListMarker(p, c)
	if d == nil {
		return 0
	}
	reuse := c.kind == kList && c.ordered == d.ordered && c.delim == d.delim
	over := false
	if reuse {
		over = c.depth+1 > maxBlockDepth
	} else {
		over = parentFor(c, kList).depth+2 > maxBlockDepth
	}
	if over {
		p.offset, p.column, p.partialTab = off, col, pt
		return 0
	}
	p.closeUnmatchedBlocks()
	if !reuse {
		l := p.addChild(kList)
		l.ordered, l.delim, l.start, l.tight = d.ordered, d.delim, d.start, true
	}
	it := p.addChild(kItem)
	it.markerOffset, it.padding, it.marker = d.markerOffset, d.padding, d.digits
	return 1
}
