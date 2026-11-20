package markdown

import (
	"html"
	"strings"
)

// Rendered is the structured render tree consumed by the shells.
type Rendered struct {
	Blocks []RBlock  `json:"blocks"`
	TOC    []TOCItem `json:"toc"` // top-level headings in document order, never nil
}

// TOCItem is one entry of the table of contents.
type TOCItem struct {
	BlockID string `json:"block_id"`
	Level   int    `json:"level"`
	Text    string `json:"text"` // plain text of the heading (inline markup stripped)
}

// RBlock is a block of the render tree.
type RBlock struct {
	BlockID  string       `json:"block_id"`
	Kind     string       `json:"kind"`
	Raw      string       `json:"raw"`
	Level    int          `json:"level,omitempty"`
	Lang     string       `json:"lang,omitempty"`
	Info     string       `json:"info,omitempty"`
	Text     string       `json:"text,omitempty"`
	Inline   []Inline     `json:"inline,omitempty"`
	Children []RBlock     `json:"children,omitempty"`
	Ordered  bool         `json:"ordered,omitempty"`
	Start    *int         `json:"start,omitempty"`
	Tight    bool         `json:"tight,omitempty"`
	Items    []RItem      `json:"items,omitempty"`
	Align    []string     `json:"align,omitempty"`
	Header   [][]Inline   `json:"header,omitempty"`
	Rows     [][][]Inline `json:"rows,omitempty"`
}

// RItem is a list item.
type RItem struct {
	Checked *bool    `json:"checked,omitempty"` // nil = not a task item
	Blocks  []RBlock `json:"blocks"`
}

// Inline is an inline element of the render tree.
type Inline struct {
	T        string   `json:"t"`
	Text     string   `json:"text,omitempty"`
	Href     string   `json:"href,omitempty"`
	Title    string   `json:"title,omitempty"`
	Alt      string   `json:"alt,omitempty"`
	Children []Inline `json:"children,omitempty"`
}

// Render builds the render tree of src. It is a pure function of
// Normalize(src).
func Render(src string) Rendered {
	norm := Normalize(src)
	r := Rendered{Blocks: []RBlock{}, TOC: []TOCItem{}}
	if norm == "" {
		return r
	}
	if out, ok := renderNormalized(norm); ok {
		return out
	}
	raw := strings.TrimSuffix(norm, "\n")
	r.Blocks = append(r.Blocks, RBlock{BlockID: BlockID("unknown", raw), Kind: "unknown", Raw: raw, Text: raw})
	return r
}

func renderNormalized(norm string) (out Rendered, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	doc, blocks := normalizedBlocks(norm)
	if doc == nil || blocks == nil {
		return Rendered{}, false
	}
	out = Rendered{Blocks: make([]RBlock, 0, len(doc.kids)), TOC: []TOCItem{}}
	for _, k := range doc.kids {
		rb := renderBlock(k)
		out.Blocks = append(out.Blocks, rb)
		if k.kind == kHeading {
			out.TOC = append(out.TOC, TOCItem{BlockID: rb.BlockID, Level: k.level, Text: plainText(parseInlines(k.lines[0], false))})
		}
	}
	return out, true
}

func renderBlocks(kids []*node) []RBlock {
	joinBlocks(kids, true) // makes sure list marker alternation is settled
	out := make([]RBlock, 0, len(kids))
	for _, k := range kids {
		out = append(out, renderBlock(k))
	}
	return out
}

func renderBlock(n *node) RBlock {
	raw := n.raw()
	kind := kindName(n.kind)
	b := RBlock{BlockID: BlockID(kind, raw), Kind: kind, Raw: raw}
	switch n.kind {
	case kHeading:
		b.Level = n.level
		b.Inline = toInlines(parseInlines(n.lines[0], false))
	case kPara:
		b.Inline = toInlines(parseInlines(trimRightST(strings.Join(n.lines, "\n")), false))
	case kCode:
		b.Info = n.info
		if f := strings.Fields(n.info); len(f) > 0 {
			b.Lang = f[0]
		}
		b.Text = strings.Join(n.lines, "\n")
	case kHTML, kUnknown:
		b.Text = strings.Join(n.lines, "\n")
	case kQuote:
		b.Children = renderBlocks(n.kids)
	case kList:
		b.Ordered, b.Tight = n.ordered, n.tight
		if n.ordered {
			start := n.start
			b.Start = &start // a pointer: a list that starts at 0 must say so
		}
		b.Items = make([]RItem, 0, len(n.kids))
		for _, it := range n.kids {
			ri := RItem{Blocks: renderBlocks(it.kids)}
			if it.task != taskNone {
				stripTaskMarker(&ri.Blocks[0], it.kids[0])
			}
			switch it.task {
			case taskOpen:
				v := false
				ri.Checked = &v
			case taskDone:
				v := true
				ri.Checked = &v
			}
			b.Items = append(b.Items, ri)
		}
	case kTable:
		ncol := len(n.align)
		b.Align = append([]string(nil), n.align...)
		b.Header = renderRow(splitRow(n.header), ncol)
		b.Rows = make([][][]Inline, 0, len(n.lines))
		for _, l := range n.lines {
			b.Rows = append(b.Rows, renderRow(splitRow(l), ncol))
		}
	}
	return b
}

// stripTaskMarker removes the "[ ] "/"[x] " prefix from the first paragraph of
// a task item: the state travels in RItem.Checked instead.
func stripTaskMarker(rb *RBlock, n *node) {
	lines := append([]string{n.lines[0][4:]}, n.lines[1:]...)
	rb.Inline = toInlines(parseInlines(trimRightST(strings.Join(lines, "\n")), false))
	rb.Raw = strings.TrimPrefix(rb.Raw, n.lines[0][:4])
	rb.BlockID = BlockID(rb.Kind, rb.Raw)
}

func renderRow(cells []string, ncol int) [][]Inline {
	row := make([][]Inline, ncol)
	for i := range row {
		if i < len(cells) {
			row[i] = toInlines(parseInlines(cells[i], true))
		} else {
			row[i] = []Inline{}
		}
	}
	return row
}

// ---- inline conversion ----

func toInlines(nodes []*inl) []Inline {
	out := make([]Inline, 0, len(nodes))
	// Adjacent text pieces (literal delimiters, brackets, raw HTML) merge into
	// one text element; a builder keeps that linear.
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = append(out, Inline{T: "text", Text: text.String()})
			text.Reset()
		}
	}
	add := func(in Inline) {
		flush()
		out = append(out, in)
	}
	for _, n := range nodes {
		switch n.kind {
		case iText:
			text.WriteString(unescapeText(n.src))
		case iHTML:
			text.WriteString(n.src)
		case iCode:
			add(Inline{T: "code", Text: n.codeText()})
		case iAuto:
			shown := n.src[1 : len(n.src)-1]
			add(Inline{T: "link", Href: n.destRaw(), Children: []Inline{{T: "text", Text: shown}}})
		case iEm:
			add(Inline{T: "em", Children: toInlines(n.kids)})
		case iStrong:
			add(Inline{T: "strong", Children: toInlines(n.kids)})
		case iDel:
			add(Inline{T: "del", Children: toInlines(n.kids)})
		case iLink:
			add(Inline{T: "link", Href: linkDest(n.destRaw()), Title: unescapeText(n.titleRaw()), Children: toInlines(n.kids)})
		case iImage:
			add(Inline{T: "image", Href: linkDest(n.destRaw()), Title: unescapeText(n.titleRaw()), Alt: plainText(n.kids)})
		case iBreak:
			add(Inline{T: "br"})
		case iSoft:
			add(Inline{T: "softbr"})
		}
	}
	flush()
	return out
}

func linkDest(raw string) string {
	if len(raw) >= 2 && raw[0] == '<' && raw[len(raw)-1] == '>' {
		raw = raw[1 : len(raw)-1]
	}
	return unescapeText(raw)
}

// plainText returns the text of inline nodes with all markup stripped.
func plainText(nodes []*inl) string {
	var b strings.Builder
	var walk func(ns []*inl)
	walk = func(ns []*inl) {
		for _, n := range ns {
			switch n.kind {
			case iText:
				b.WriteString(unescapeText(n.src))
			case iHTML:
				b.WriteString(n.src)
			case iCode:
				b.WriteString(n.codeText())
			case iAuto:
				b.WriteString(n.src[1 : len(n.src)-1])
			case iBreak, iSoft:
				b.WriteByte(' ')
			default:
				walk(n.kids)
			}
		}
	}
	walk(nodes)
	return b.String()
}

// unescapeText resolves backslash escapes and character references.
func unescapeText(s string) string {
	if strings.IndexByte(s, '\\') < 0 && strings.IndexByte(s, '&') < 0 {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c == '\\' && i+1 < len(s) && isASCIIPunct(s[i+1]) {
			b.WriteByte(s[i+1])
			i += 2
			continue
		}
		if c == '&' {
			if tok := entityAt(s, i); tok != "" {
				b.WriteString(html.UnescapeString(tok))
				i += len(tok)
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

// entityAt returns the character reference starting at s[i] ("&amp;",
// "&#35;", "&#x23;") or "" if there is none.
func entityAt(s string, i int) string {
	j := i + 1
	if j >= len(s) {
		return ""
	}
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	switch {
	case s[j] == '#':
		j++
		hex := j < len(s) && (s[j] == 'x' || s[j] == 'X')
		max := 7
		if hex {
			j++
			max = 6
		}
		start := j
		for j < len(s) && j-start < max && (isDigit(s[j]) || hex && (s[j] >= 'a' && s[j] <= 'f' || s[j] >= 'A' && s[j] <= 'F')) {
			j++
		}
		if j == start {
			return ""
		}
	case s[j] >= 'A' && s[j] <= 'Z' || s[j] >= 'a' && s[j] <= 'z':
		start := j
		for j < len(s) && j-start < 32 && (isDigit(s[j]) || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= 'a' && s[j] <= 'z') {
			j++
		}
	default:
		return ""
	}
	if j >= len(s) || s[j] != ';' {
		return ""
	}
	tok := s[i : j+1]
	if html.UnescapeString(tok) == tok {
		return ""
	}
	return tok
}
