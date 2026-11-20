package markdown

import "strings"

type nodeKind uint8

const (
	kDoc nodeKind = iota
	kQuote
	kList
	kItem
	kPara
	kHeading
	kCode
	kHR
	kHTML
	kUnknown
	kTable
)

// Unknown block flavours.
const (
	uFootnote uint8 = iota + 1
	uRefDef
	uMath
	uContainer
)

// task states of a list item.
const (
	taskNone int8 = iota
	taskOpen
	taskDone
)

// node is a block-level AST node. Leaf nodes keep their container-stripped
// source lines; container nodes keep children.
type node struct {
	kind   nodeKind
	open   bool
	parent *node
	kids   []*node
	lines  []string
	depth  int

	lastLineBlank bool
	startLine     int

	// heading
	level int
	// list
	ordered bool
	delim   byte // bullet char, or '.' / ')' for ordered lists
	start   int
	tight   bool
	alt     bool // emit with the alternate marker (keeps adjacent lists apart)
	// item
	marker       string // typed digits of an ordered item
	markerOffset int
	padding      int
	task         int8
	// code
	fenced      bool
	fenceChar   byte
	fenceLen    int
	fenceOffset int
	info        string
	// html / unknown
	htmlType int
	ukind    uint8
	ucount   int
	// table
	align  []string
	header string

	canon     []string
	canonDone bool
}

func (n *node) lastChild() *node {
	if len(n.kids) == 0 {
		return nil
	}
	return n.kids[len(n.kids)-1]
}

func isContainer(k nodeKind) bool { return k == kQuote || k == kList || k == kItem }

func acceptsLines(k nodeKind) bool {
	switch k {
	case kPara, kCode, kHTML, kUnknown, kTable:
		return true
	}
	return false
}

func canContain(parent, child nodeKind) bool {
	switch parent {
	case kDoc, kQuote, kItem:
		return child != kItem
	case kList:
		return child == kItem
	}
	return false
}

// parser is a line-oriented CommonMark block parser (the container/leaf
// algorithm of the reference implementation) extended with GFM tables and
// verbatim "unknown" blocks.
type parser struct {
	doc, tip, oldtip, lastMatched *node
	ln                            string
	offset, column                int
	nextNonspace                  int
	nextNonspaceColumn            int
	indent                        int
	indented, blank               bool
	partialTab                    bool
	allClosed                     bool
	lineConsumed                  bool
	lineNo                        int
}

// parseBlocks parses src (already preprocessed) into a block tree whose root
// sits at container depth baseDepth.
func parseBlocks(src string, baseDepth int) *node {
	p := &parser{}
	p.doc = &node{kind: kDoc, open: true, depth: baseDepth}
	p.tip = p.doc
	for _, ln := range splitLines(src) {
		p.incorporateLine(ln)
	}
	for p.tip != nil {
		p.finalize(p.tip)
	}
	return p.doc
}

func (p *parser) findNextNonspace() {
	i, cols := p.offset, p.column
	for i < len(p.ln) {
		c := p.ln[i]
		if c == ' ' {
			i++
			cols++
		} else if c == '\t' {
			i++
			cols += 4 - cols%4
		} else {
			break
		}
	}
	p.blank = i >= len(p.ln)
	p.nextNonspace = i
	p.nextNonspaceColumn = cols
	p.indent = cols - p.column
	p.indented = p.indent >= 4
}

func (p *parser) advanceNextNonspace() {
	p.offset = p.nextNonspace
	p.column = p.nextNonspaceColumn
	p.partialTab = false
}

func (p *parser) advanceOffset(count int, columns bool) {
	for count > 0 && p.offset < len(p.ln) {
		c := p.ln[p.offset]
		if c == '\t' {
			charsToTab := 4 - p.column%4
			if columns {
				p.partialTab = charsToTab > count
				adv := charsToTab
				if count < adv {
					adv = count
				}
				p.column += adv
				if !p.partialTab {
					p.offset++
				}
				count -= adv
			} else {
				p.partialTab = false
				p.column += charsToTab
				p.offset++
				count--
			}
		} else {
			p.partialTab = false
			p.offset++
			p.column++
			count--
		}
	}
}

func (p *parser) addLine() {
	t := p.tip
	var s string
	if p.partialTab {
		p.offset++
		charsToTab := 4 - p.column%4
		s = strings.Repeat(" ", charsToTab) + p.ln[p.offset:]
	} else {
		s = p.ln[p.offset:]
	}
	switch t.kind {
	case kPara:
		s = trimLeftST(s)
	case kHTML, kUnknown:
		s = expandLeadingTabs(s, p.column)
	}
	t.lines = append(t.lines, s)
}

// expandLeadingTabs turns tabs in the leading whitespace of s into the spaces
// they stand for when s starts at column col. Verbatim blocks are re-indented
// by the serialiser, which would otherwise change what a tab means.
func expandLeadingTabs(s string, col int) string {
	i := 0
	for i < len(s) && isSpaceTab(s[i]) {
		i++
	}
	if strings.IndexByte(s[:i], '\t') < 0 {
		return s
	}
	var b strings.Builder
	for j := 0; j < i; j++ {
		if s[j] == '\t' {
			w := 4 - col%4
			b.WriteString(strings.Repeat(" ", w))
			col += w
		} else {
			b.WriteByte(' ')
			col++
		}
	}
	b.WriteString(s[i:])
	return b.String()
}

func (p *parser) addChild(kind nodeKind) *node {
	for !canContain(p.tip.kind, kind) {
		p.finalize(p.tip)
	}
	n := &node{kind: kind, open: true, parent: p.tip, startLine: p.lineNo, depth: p.tip.depth}
	if isContainer(kind) {
		n.depth++
	}
	p.tip.kids = append(p.tip.kids, n)
	p.tip = n
	return n
}

// parentFor returns the node that would become the parent of a new child of
// the given kind when the last matched container is c.
func parentFor(c *node, kind nodeKind) *node {
	for !canContain(c.kind, kind) {
		c = c.parent
	}
	return c
}

func (p *parser) closeUnmatchedBlocks() {
	if p.allClosed {
		return
	}
	for p.oldtip != p.lastMatched {
		parent := p.oldtip.parent
		p.finalize(p.oldtip)
		p.oldtip = parent
	}
	p.allClosed = true
}

// cont reports whether an open container matches the current line:
// 0 matched, 1 not matched, 2 line fully consumed.
func (p *parser) cont(n *node) int {
	ln := p.ln
	switch n.kind {
	case kDoc, kList:
		return 0
	case kQuote:
		if !p.indented && p.nextNonspace < len(ln) && ln[p.nextNonspace] == '>' {
			p.advanceNextNonspace()
			p.advanceOffset(1, false)
			if p.offset < len(ln) && isSpaceTab(ln[p.offset]) {
				p.advanceOffset(1, true)
			}
			return 0
		}
		return 1
	case kItem:
		if p.blank {
			if len(n.kids) == 0 {
				return 1
			}
			p.advanceNextNonspace()
		} else if p.indent >= n.markerOffset+n.padding {
			p.advanceOffset(n.markerOffset+n.padding, true)
		} else {
			return 1
		}
		return 0
	case kHeading, kHR:
		return 1
	case kCode:
		if n.fenced {
			if p.indent <= 3 && p.nextNonspace < len(ln) && ln[p.nextNonspace] == n.fenceChar &&
				isClosingFence(ln[p.nextNonspace:], n.fenceChar, n.fenceLen) {
				p.finalize(n)
				return 2
			}
			i := n.fenceOffset
			for i > 0 && p.offset < len(ln) && isSpaceTab(ln[p.offset]) {
				p.advanceOffset(1, true)
				i--
			}
			return 0
		}
		if p.indent >= 4 {
			p.advanceOffset(4, true)
		} else if p.blank {
			p.advanceNextNonspace()
		} else {
			return 1
		}
		return 0
	case kHTML:
		if p.blank && (n.htmlType == 6 || n.htmlType == 7) {
			return 1
		}
		return 0
	case kPara:
		if p.blank {
			return 1
		}
		return 0
	case kUnknown:
		return p.contUnknown(n)
	case kTable:
		if p.blank {
			return 1
		}
		return 0
	}
	return 1
}

func (p *parser) incorporateLine(ln string) {
	allMatched := true
	container := p.doc
	p.oldtip = p.tip
	p.offset, p.column = 0, 0
	p.blank, p.partialTab, p.lineConsumed = false, false, false
	p.lineNo++
	p.ln = ln

	for {
		last := container.lastChild()
		if last == nil || !last.open {
			break
		}
		container = last
		p.findNextNonspace()
		switch p.cont(container) {
		case 1:
			container = container.parent
			allMatched = false
		case 2:
			return
		}
		if !allMatched {
			break
		}
	}

	p.allClosed = container == p.oldtip
	p.lastMatched = container

	matchedLeaf := container.kind != kPara && container.kind != kTable && acceptsLines(container.kind)
	for !matchedLeaf {
		p.findNextNonspace()
		if !p.indented && (p.blank || !maybeSpecial(ln[p.nextNonspace])) {
			p.advanceNextNonspace()
			break
		}
		started := false
		for _, st := range blockStarts {
			res := st(p, container)
			if res == 1 {
				container = p.tip
				started = true
				break
			}
			if res == 2 {
				container = p.tip
				matchedLeaf = true
				started = true
				break
			}
		}
		if !started {
			p.advanceNextNonspace()
			break
		}
	}

	if !p.allClosed && !p.blank && p.tip.kind == kPara {
		p.addLine() // lazy paragraph continuation
		return
	}
	p.closeUnmatchedBlocks()
	if p.blank && container.lastChild() != nil {
		container.lastChild().lastLineBlank = true
	}
	t := container.kind
	lastBlank := p.blank && !(t == kQuote || (t == kCode && container.fenced) ||
		(t == kItem && len(container.kids) == 0 && container.startLine == p.lineNo))
	for c := container; c != nil; c = c.parent {
		c.lastLineBlank = lastBlank
	}
	if p.lineConsumed {
		return
	}
	if acceptsLines(t) {
		p.addLine()
		p.afterAddLine(container)
	} else if p.offset < len(ln) && !p.blank {
		p.addChild(kPara)
		p.advanceNextNonspace()
		p.addLine()
	}
}

// afterAddLine checks end conditions of blocks that close on a marker line.
func (p *parser) afterAddLine(n *node) {
	switch n.kind {
	case kHTML:
		if n.htmlType >= 1 && n.htmlType <= 5 && reHTMLClose[n.htmlType].MatchString(p.ln[p.offset:]) {
			p.finalize(n)
		}
	case kUnknown:
		p.afterUnknownLine(n)
	}
}

func maybeSpecial(c byte) bool {
	switch c {
	case '#', '`', '~', '<', '>', '*', '+', '-', '_', '=', '|', '[', '$', ':':
		return true
	}
	return c >= '0' && c <= '9'
}

func isClosingFence(s string, ch byte, minLen int) bool {
	i := 0
	for i < len(s) && s[i] == ch {
		i++
	}
	return i >= minLen && isBlank(s[i:])
}
