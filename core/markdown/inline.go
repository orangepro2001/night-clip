package markdown

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type inlKind uint8

const (
	iText inlKind = iota // src: verbatim source text
	iCode                // src: verbatim span including backticks; text: content
	iHTML                // src: raw inline HTML, kept as literal text
	iAuto                // src: "<...>"; dest: target
	iEm                  // marker/n describe the delimiters as written
	iStrong
	iDel
	iLink  // tail: verbatim "(...)"; dest/title decoded lazily
	iImage // same as iLink
	iBreak // hard line break; n is 1 for a trailing backslash, 2 for trailing spaces
	iSoft  // soft line break
)

// inl is an inline AST node. While parsing, children form a linked list
// (first/last, prev/next); finish() converts them to kids slices.
type inl struct {
	kind        inlKind
	marker      byte
	multiline   bool
	n           int8
	depth       int16
	src         string
	aux         *inlAux
	prev, next  *inl
	first, last *inl
	kids        []*inl
}

// inlAux carries the fields only code spans, links and autolinks need, which
// keeps the (very numerous) text and delimiter nodes small.
type inlAux struct {
	text        string // code span content
	dest, title string // raw (still escaped) destination and title
	tail        string // verbatim "(...)" of a link or image
}

func (n *inl) codeText() string {
	if n.aux == nil {
		return ""
	}
	return n.aux.text
}

func (n *inl) destRaw() string {
	if n.aux == nil {
		return ""
	}
	return n.aux.dest
}

func (n *inl) titleRaw() string {
	if n.aux == nil {
		return ""
	}
	return n.aux.title
}

func (n *inl) tailSrc() string {
	if n.aux == nil {
		return ""
	}
	return n.aux.tail
}

type delim struct {
	cc                byte
	num, orig         int
	node              *inl
	canOpen, canClose bool
	prev, next        *delim
}

type bracket struct {
	node      *inl
	image     bool
	prevDelim *delim
	idx       int
}

type inlParser struct {
	s     string
	pos   int
	plain bool
	cell  bool

	root          *inl
	delims        *delim
	brackets      []bracket
	inactiveBelow int
	tooDeep       bool

	tickPos    map[int][]int
	tickIdx    map[int]int
	ticksBuilt bool

	noComment, noPI, noCDATA, noDecl bool

	slab      []inl
	slabSize  int
	dslab     []delim
	dslabSize int
}

var inlSpecial [256]bool

func init() {
	for _, c := range []byte("\\`*_~[]!<\n") {
		inlSpecial[c] = true
	}
}

// parseInlines parses inline Markdown. Source text of opaque spans is kept
// verbatim in the returned tree.
func parseInlines(s string, cell bool) []*inl {
	p := newInlParser(s, cell, false)
	p.run()
	if p.tooDeep {
		p = newInlParser(s, cell, true)
		p.run()
	}
	return p.finish()
}

func newInlParser(s string, cell, plain bool) *inlParser {
	return &inlParser{s: s, cell: cell, plain: plain, root: &inl{}}
}

// ---- list helpers (all live nodes belong to root's list) ----

func (p *inlParser) appendNode(n *inl) {
	n.prev = p.root.last
	n.next = nil
	if p.root.last != nil {
		p.root.last.next = n
	} else {
		p.root.first = n
	}
	p.root.last = n
}

func (p *inlParser) insertAfter(a, n *inl) {
	n.prev = a
	n.next = a.next
	if a.next != nil {
		a.next.prev = n
	} else {
		p.root.last = n
	}
	a.next = n
}

func (p *inlParser) unlink(n *inl) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		p.root.first = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		p.root.last = n.prev
	}
	n.prev, n.next = nil, nil
}

// newNode hands out nodes from slabs: documents with a node per few bytes
// would otherwise spend most of their time in the allocator and the GC.
func (p *inlParser) newNode(kind inlKind) *inl {
	if len(p.slab) == 0 {
		// Grow geometrically: most inline runs are a handful of nodes.
		if p.slabSize < 256 {
			if p.slabSize == 0 {
				p.slabSize = 4
			} else {
				p.slabSize *= 2
			}
		}
		p.slab = make([]inl, p.slabSize)
	}
	n := &p.slab[0]
	p.slab = p.slab[1:]
	n.kind = kind
	return n
}

func (p *inlParser) breakNode(style int8) *inl {
	n := p.newNode(iBreak)
	n.n = style
	return n
}

func (p *inlParser) newDelim() *delim {
	if len(p.dslab) == 0 {
		if p.dslabSize < 128 {
			if p.dslabSize == 0 {
				p.dslabSize = 2
			} else {
				p.dslabSize *= 2
			}
		}
		p.dslab = make([]delim, p.dslabSize)
	}
	d := &p.dslab[0]
	p.dslab = p.dslab[1:]
	return d
}

func (p *inlParser) textNode(src string) *inl {
	n := p.newNode(iText)
	n.src = src
	return n
}

func (p *inlParser) addText(seg string) {
	if seg != "" {
		p.appendNode(p.textNode(seg))
	}
}

// ---- main loop ----

func (p *inlParser) run() {
	s := p.s
	textStart := 0
	flush := func(end int) {
		if end > textStart {
			p.addText(s[textStart:end])
		}
	}
	for p.pos < len(s) && !p.tooDeep {
		c := s[p.pos]
		if !inlSpecial[c] {
			p.pos++
			continue
		}
		switch c {
		case '\n':
			seg := s[textStart:p.pos]
			trimmed := trimRightST(seg)
			spaces := 0
			for k := len(seg) - 1; k >= 0 && seg[k] == ' '; k-- {
				spaces++
			}
			p.addText(trimmed)
			if spaces >= 2 {
				p.appendNode(p.breakNode(2))
			} else {
				p.appendNode(p.newNode(iSoft))
			}
			p.pos++
			p.pos = skipSpaces(s, p.pos)
			textStart = p.pos
		case '\\':
			if p.pos+1 < len(s) && s[p.pos+1] == '\n' {
				flush(p.pos)
				p.appendNode(p.breakNode(1))
				p.pos = skipSpaces(s, p.pos+2)
				textStart = p.pos
			} else if p.pos+1 < len(s) && isASCIIPunct(s[p.pos+1]) {
				p.pos += 2
			} else {
				p.pos++
			}
		case '`':
			n := 0
			for p.pos+n < len(s) && s[p.pos+n] == '`' {
				n++
			}
			closeAt := p.findTickClose(n, p.pos+n)
			if closeAt < 0 {
				p.pos += n
				continue
			}
			flush(p.pos)
			end := closeAt + n
			p.appendNode(p.codeNode(s[p.pos:end], s[p.pos+n:closeAt]))
			p.pos = end
			textStart = p.pos
		case '*', '_', '~':
			end := p.pos
			for end < len(s) && s[end] == c {
				end++
			}
			if p.plain || (c == '~' && end-p.pos > 2) {
				p.pos = end
				continue
			}
			canOpen, canClose := flanking(s, p.pos, end, c)
			if !canOpen && !canClose {
				p.pos = end
				continue
			}
			flush(p.pos)
			node := p.textNode(s[p.pos:end])
			p.appendNode(node)
			d := p.newDelim()
			*d = delim{cc: c, num: end - p.pos, orig: end - p.pos, node: node, canOpen: canOpen, canClose: canClose, prev: p.delims}
			if p.delims != nil {
				p.delims.next = d
			}
			p.delims = d
			p.pos = end
			textStart = p.pos
		case '[':
			if p.plain {
				p.pos++
				continue
			}
			flush(p.pos)
			node := p.textNode("[")
			p.appendNode(node)
			p.brackets = append(p.brackets, bracket{node: node, prevDelim: p.delims, idx: len(p.brackets)})
			p.pos++
			textStart = p.pos
		case '!':
			if !p.plain && p.pos+1 < len(s) && s[p.pos+1] == '[' {
				flush(p.pos)
				node := p.textNode("![")
				p.appendNode(node)
				p.brackets = append(p.brackets, bracket{node: node, image: true, prevDelim: p.delims, idx: len(p.brackets)})
				p.pos += 2
				textStart = p.pos
			} else {
				p.pos++
			}
		case ']':
			if p.plain {
				p.pos++
				continue
			}
			flush(p.pos)
			textStart = p.closeBracket(textStart)
		case '<':
			if end, node := p.angle(p.pos); node != nil {
				flush(p.pos)
				p.appendNode(node)
				p.pos = end
				textStart = p.pos
			} else {
				p.pos++
			}
		}
	}
	if p.tooDeep {
		return
	}
	flush(len(s))
	p.processEmphasis(nil)
}

// ---- code spans ----

func (p *inlParser) buildTicks() {
	p.ticksBuilt = true
	p.tickPos = map[int][]int{}
	p.tickIdx = map[int]int{}
	s := p.s
	for i := 0; i < len(s); {
		k := strings.IndexByte(s[i:], '`')
		if k < 0 {
			break
		}
		i += k
		j := i
		for j < len(s) && s[j] == '`' {
			j++
		}
		p.tickPos[j-i] = append(p.tickPos[j-i], i)
		i = j
	}
}

// findTickClose returns the start of the next backtick run of exactly n
// backticks at or after from, or -1. Calls must have non-decreasing from per
// n, which the left-to-right scan guarantees (amortised linear).
func (p *inlParser) findTickClose(n, from int) int {
	if !p.ticksBuilt {
		p.buildTicks()
	}
	list := p.tickPos[n]
	idx := p.tickIdx[n]
	for idx < len(list) && list[idx] < from {
		idx++
	}
	p.tickIdx[n] = idx
	if idx < len(list) {
		return list[idx]
	}
	return -1
}

func (p *inlParser) codeNode(src, content string) *inl {
	ml := strings.IndexByte(content, '\n') >= 0
	if ml {
		content = strings.ReplaceAll(content, "\n", " ")
	}
	if len(content) >= 2 && content[0] == ' ' && content[len(content)-1] == ' ' && strings.Trim(content, " ") != "" {
		content = content[1 : len(content)-1]
	}
	if p.cell {
		content = strings.ReplaceAll(content, `\|`, "|")
	}
	n := p.newNode(iCode)
	n.src, n.aux, n.multiline = src, &inlAux{text: content}, ml
	return n
}

// ---- delimiters ----

func isUniSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\f', '\r':
		return true
	}
	return r > 127 && unicode.Is(unicode.Zs, r)
}

func isUniPunct(r rune) bool {
	if r < 128 {
		return isASCIIPunct(byte(r))
	}
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

func flanking(s string, start, end int, c byte) (canOpen, canClose bool) {
	before, after := ' ', ' '
	if start > 0 {
		before, _ = utf8.DecodeLastRuneInString(s[:start])
	}
	if end < len(s) {
		after, _ = utf8.DecodeRuneInString(s[end:])
	}
	afterWS, beforeWS := isUniSpace(after), isUniSpace(before)
	afterP, beforeP := isUniPunct(after), isUniPunct(before)
	left := !afterWS && (!afterP || beforeWS || beforeP)
	right := !beforeWS && (!beforeP || afterWS || afterP)
	if c == '_' {
		return left && (!right || beforeP), right && (!left || afterP)
	}
	return left, right
}

func (p *inlParser) removeDelim(d *delim) {
	if d.prev != nil {
		d.prev.next = d.next
	}
	if d.next == nil {
		p.delims = d.prev
	} else {
		d.next.prev = d.prev
	}
}

func bottomIndex(d *delim) int {
	k := 0
	switch d.cc {
	case '_':
		k = 1
	case '~':
		k = 2
	}
	i := k*6 + d.orig%3
	if d.canOpen {
		i += 3
	}
	return i
}

func (p *inlParser) processEmphasis(bottom *delim) {
	var openersBottom [18]*delim
	for i := range openersBottom {
		openersBottom[i] = bottom
	}
	if p.delims == bottom {
		return // nothing above the bottom; also avoids walking the whole stack
	}
	closer := p.delims
	for closer != nil && closer.prev != bottom {
		closer = closer.prev
	}
	for closer != nil && !p.tooDeep {
		if !closer.canClose {
			closer = closer.next
			continue
		}
		idx := bottomIndex(closer)
		opener := closer.prev
		found := false
		for opener != nil && opener != bottom && opener != openersBottom[idx] {
			if opener.cc == closer.cc && opener.canOpen {
				if closer.cc == '~' {
					if opener.num == closer.num {
						found = true
						break
					}
				} else {
					odd := (closer.canOpen || opener.canClose) && closer.orig%3 != 0 && (opener.orig+closer.orig)%3 == 0
					if !odd {
						found = true
						break
					}
				}
			}
			opener = opener.prev
		}
		old := closer
		if !found {
			closer = closer.next
			openersBottom[idx] = old.prev
			if !old.canOpen {
				p.removeDelim(old)
			}
			continue
		}
		use := 1
		if closer.cc == '~' {
			use = closer.num
		} else if closer.num >= 2 && opener.num >= 2 {
			use = 2
		}
		oi, ci := opener.node, closer.node
		opener.num -= use
		closer.num -= use
		oi.src = oi.src[:len(oi.src)-use]
		ci.src = ci.src[use:]
		em := p.newNode(iEm)
		em.marker, em.n = closer.cc, int8(use)
		switch {
		case closer.cc == '~':
			em.kind = iDel
		case use == 1:
			em.kind = iEm
		default:
			em.kind = iStrong
		}
		if first := oi.next; first != ci {
			last := ci.prev
			em.first, em.last = first, last
			first.prev, last.next = nil, nil
			md := int16(0)
			for c := first; c != nil; c = c.next {
				if c.depth > md {
					md = c.depth
				}
			}
			em.depth = md + 1
		} else {
			em.depth = 1
		}
		if int(em.depth) > maxInlineDepth {
			p.tooDeep = true
		}
		em.prev, em.next = oi, ci
		oi.next, ci.prev = em, em
		if opener.next != closer {
			opener.next = closer
			closer.prev = opener
		}
		if opener.num == 0 {
			p.unlink(oi)
			p.removeDelim(opener)
		}
		if closer.num == 0 {
			p.unlink(ci)
			next := closer.next
			p.removeDelim(closer)
			closer = next
		}
	}
	for p.delims != nil && p.delims != bottom {
		p.removeDelim(p.delims)
	}
}

// ---- links and images ----

// closeBracket handles ']' at p.pos and returns the new start of pending
// text.
func (p *inlParser) closeBracket(textStart int) int {
	s := p.s
	pos := p.pos
	n := len(p.brackets)
	if n == 0 {
		p.pos++
		return pos
	}
	b := p.brackets[n-1]
	if !b.image && b.idx < p.inactiveBelow {
		p.brackets = p.brackets[:n-1]
		p.pos++
		return pos
	}
	if pos+1 < len(s) && s[pos+1] == '(' {
		if end, dest, title, ok := parseLinkTail(s, pos+1); ok {
			p.brackets = p.brackets[:n-1]
			p.processEmphasis(b.prevDelim)
			node := p.newNode(iLink)
			node.aux = &inlAux{tail: s[pos+1 : end], dest: dest, title: title}
			if b.image {
				node.kind = iImage
			}
			opener := b.node
			if first := opener.next; first != nil {
				last := p.root.last
				node.first, node.last = first, last
				first.prev, last.next = nil, nil
				md := int16(0)
				for c := first; c != nil; c = c.next {
					if c.depth > md {
						md = c.depth
					}
				}
				node.depth = md + 1
			} else {
				node.depth = 1
			}
			if int(node.depth) > maxInlineDepth {
				p.tooDeep = true
			}
			opener.next = nil
			p.root.last = opener
			p.insertAfter(opener, node)
			p.unlink(opener)
			if !b.image {
				p.inactiveBelow = len(p.brackets)
			}
			p.pos = end
			return end
		}
	}
	p.brackets = p.brackets[:n-1]
	p.pos++
	return pos
}

// parseLinkTail parses "(dest "title")" starting at s[i] == '('. It returns
// the end (after ')') and the raw destination/title.
func parseLinkTail(s string, i int) (end int, dest, title string, ok bool) {
	j := skipSpNl(s, i+1)
	if j >= len(s) {
		return 0, "", "", false
	}
	if s[j] == ')' {
		return j + 1, "", "", true
	}
	de, ok := parseLinkDest(s, j)
	if !ok || de == j {
		return 0, "", "", false
	}
	dest = s[j:de]
	k := skipSpNl(s, de)
	if k > de && k < len(s) {
		if te, tok := parseLinkTitle(s, k); tok {
			title = s[k+1 : te-1]
			k = skipSpNl(s, te)
		}
	}
	if k < len(s) && s[k] == ')' {
		return k + 1, dest, title, true
	}
	return 0, "", "", false
}

// ---- autolinks and raw HTML ----

var (
	reAutoURI   = regexp.MustCompile(`^<[A-Za-z][A-Za-z0-9.+-]{1,31}:[^\x00-\x20<>]*>`)
	reAutoEmail = regexp.MustCompile(`^<[a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*>`)
	reInlOpen   = regexp.MustCompile(`^` + reOpenTag)
	reInlClose  = regexp.MustCompile(`^` + reCloseTag)
)

// angle tries to recognise an autolink or raw HTML at s[pos] == '<'.
func (p *inlParser) angle(pos int) (int, *inl) {
	s := p.s
	if pos+1 >= len(s) {
		return 0, nil
	}
	rest := s[pos:]
	c := rest[1]
	switch {
	case c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z':
		if m := reAutoURI.FindStringIndex(rest); m != nil {
			n := p.newNode(iAuto)
			n.src, n.aux = rest[:m[1]], &inlAux{dest: rest[1 : m[1]-1]}
			return pos + m[1], n
		}
		if m := reAutoEmail.FindStringIndex(rest); m != nil {
			n := p.newNode(iAuto)
			n.src, n.aux = rest[:m[1]], &inlAux{dest: "mailto:" + rest[1:m[1]-1]}
			return pos + m[1], n
		}
		if m := reInlOpen.FindStringIndex(rest); m != nil {
			return pos + m[1], &inl{kind: iHTML, src: rest[:m[1]]}
		}
	case c == '/':
		if m := reInlClose.FindStringIndex(rest); m != nil {
			return pos + m[1], &inl{kind: iHTML, src: rest[:m[1]]}
		}
	case c == '!':
		if strings.HasPrefix(rest, "<!-->") {
			return pos + 5, &inl{kind: iHTML, src: rest[:5]}
		}
		if strings.HasPrefix(rest, "<!--->") {
			return pos + 6, &inl{kind: iHTML, src: rest[:6]}
		}
		if strings.HasPrefix(rest, "<!--") {
			if p.noComment {
				return 0, nil
			}
			k := strings.Index(rest[4:], "-->")
			if k < 0 {
				p.noComment = true
				return 0, nil
			}
			end := 4 + k + 3
			return pos + end, &inl{kind: iHTML, src: rest[:end]}
		}
		if strings.HasPrefix(rest, "<![CDATA[") {
			if p.noCDATA {
				return 0, nil
			}
			k := strings.Index(rest[9:], "]]>")
			if k < 0 {
				p.noCDATA = true
				return 0, nil
			}
			end := 9 + k + 3
			return pos + end, &inl{kind: iHTML, src: rest[:end]}
		}
		if len(rest) > 2 && (rest[2] >= 'A' && rest[2] <= 'Z' || rest[2] >= 'a' && rest[2] <= 'z') {
			if p.noDecl {
				return 0, nil
			}
			k := strings.IndexByte(rest, '>')
			if k < 0 {
				p.noDecl = true
				return 0, nil
			}
			return pos + k + 1, &inl{kind: iHTML, src: rest[:k+1]}
		}
	case c == '?':
		if p.noPI {
			return 0, nil
		}
		k := strings.Index(rest[2:], "?>")
		if k < 0 {
			p.noPI = true
			return 0, nil
		}
		end := 2 + k + 2
		return pos + end, &inl{kind: iHTML, src: rest[:end]}
	}
	return 0, nil
}

// finish converts the linked tree into kids slices.
func (p *inlParser) finish() []*inl {
	finishKids(p.root)
	return p.root.kids
}

func finishKids(n *inl) {
	count := 0
	for c := n.first; c != nil; c = c.next {
		count++
	}
	if count > 0 {
		n.kids = make([]*inl, 0, count)
	}
	for c := n.first; c != nil; c = c.next {
		n.kids = append(n.kids, c)
		if c.first != nil {
			finishKids(c)
		}
	}
	n.first, n.last = nil, nil
}
