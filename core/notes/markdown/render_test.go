package markdown

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// mustJSON marshals without HTML escaping, so that goldens stay readable.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func txt(s string) Inline { return Inline{T: "text", Text: s} }

func TestRenderOrderedListStartIsKeptEvenWhenZero(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want string // the "start" member of the list block in the JSON, "" = absent
	}{
		{"0. zero\n1. one\n", `"start":0`},
		{"1. one\n2. two\n", `"start":1`},
		{"7. seven\n", `"start":7`},
		{"- bullet\n", ""},
		{"text\n", ""},
	} {
		b, err := json.Marshal(Render(tc.src))
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(b), `"start"`); got != (tc.want != "") || tc.want != "" && !strings.Contains(string(b), tc.want) {
			t.Errorf("Render(%q) json = %s, want %q", tc.src, b, tc.want)
		}
	}
}

func TestRenderEmpty(t *testing.T) {
	for _, in := range []string{"", "  \n\t\n"} {
		got := mustJSON(t, Render(in))
		if got != `{"blocks":[],"toc":[]}` {
			t.Errorf("Render(%q) = %s", in, got)
		}
	}
}

func TestRenderHeadingsAndTOC(t *testing.T) {
	r := Render("# Title\n\nintro\n\n## Part _one_ with `code` and [link](u) ![img](i)\n\nSetext\n---\n\n> ## quoted\n")
	h := r.Blocks[0]
	if h.Kind != "heading" || h.Level != 1 || !reflect.DeepEqual(h.Inline, []Inline{txt("Title")}) || h.Raw != "# Title" {
		t.Errorf("heading = %+v", h)
	}
	// Blocks: heading, paragraph, heading, heading (setext), quote.
	if len(r.Blocks) != 5 {
		t.Fatalf("blocks = %d, want 5", len(r.Blocks))
	}
	want := []TOCItem{
		{BlockID: r.Blocks[0].BlockID, Level: 1, Text: "Title"},
		{BlockID: r.Blocks[2].BlockID, Level: 2, Text: "Part one with code and link img"},
		{BlockID: r.Blocks[3].BlockID, Level: 2, Text: "Setext"},
	}
	if !reflect.DeepEqual(r.TOC, want) {
		t.Errorf("toc = %+v\nwant  %+v", r.TOC, want)
	}
	if r.Blocks[3].Level != 2 || r.Blocks[3].Raw != "## Setext" {
		t.Errorf("setext heading = %+v", r.Blocks[3])
	}
	// Headings inside containers are not part of the (top-level) TOC.
	if q := r.Blocks[4]; q.Kind != "quote" || len(q.Children) != 1 || q.Children[0].Kind != "heading" {
		t.Errorf("quote = %+v", q)
	}
	if len(Render("text").TOC) != 0 || Render("text").TOC == nil {
		t.Error("toc must be an empty, non-nil slice")
	}
}

func TestRenderInlines(t *testing.T) {
	r := Render("a *b* **c** ~~d~~ `e` [f](http://u \"T\") ![g](i.png) <http://auto> <me@x.io> &amp; \\* h\\\ni\nj  \nk\n")
	p := r.Blocks[0]
	if p.Kind != "paragraph" {
		t.Fatalf("kind %s", p.Kind)
	}
	want := []Inline{
		txt("a "),
		{T: "em", Children: []Inline{txt("b")}},
		txt(" "),
		{T: "strong", Children: []Inline{txt("c")}},
		txt(" "),
		{T: "del", Children: []Inline{txt("d")}},
		txt(" "),
		{T: "code", Text: "e"},
		txt(" "),
		{T: "link", Href: "http://u", Title: "T", Children: []Inline{txt("f")}},
		txt(" "),
		{T: "image", Href: "i.png", Alt: "g"},
		txt(" "),
		{T: "link", Href: "http://auto", Children: []Inline{txt("http://auto")}},
		txt(" "),
		{T: "link", Href: "mailto:me@x.io", Children: []Inline{txt("me@x.io")}},
		txt(" & * h"),
		{T: "br"},
		txt("i"),
		{T: "softbr"},
		txt("j"),
		{T: "br"},
		txt("k"),
	}
	if !reflect.DeepEqual(p.Inline, want) {
		t.Errorf("inline mismatch\n got: %s\nwant: %s", mustJSON(t, p.Inline), mustJSON(t, want))
	}
}

func TestRenderInlineEdgeCases(t *testing.T) {
	cases := []struct {
		in   string
		want []Inline
	}{
		{"snake_case_word", []Inline{txt("snake_case_word")}},
		{"__init__.py", []Inline{{T: "strong", Children: []Inline{txt("init")}}, txt(".py")}},
		{"<span>x</span>", []Inline{txt("<span>x</span>")}},
		{"*a _b_ c*", []Inline{{T: "em", Children: []Inline{txt("a "), {T: "em", Children: []Inline{txt("b")}}, txt(" c")}}}},
		{"[a [b](u2) c](u1)", []Inline{txt("[a "), {T: "link", Href: "u2", Children: []Inline{txt("b")}}, txt(" c](u1)")}},
		{"![a [b](u2) c](u1)", []Inline{{T: "image", Href: "u1", Alt: "a b c"}}},
		{"[x](<a b> 'T')", []Inline{{T: "link", Href: "a b", Title: "T", Children: []Inline{txt("x")}}}},
		{"[x](a\\)b)", []Inline{{T: "link", Href: "a)b", Children: []Inline{txt("x")}}}},
		{"&copy; &#35; &#x23; &bogus;", []Inline{txt("© # # &bogus;")}},
		{"`` a`b ``", []Inline{{T: "code", Text: "a`b"}}},
		{"[ref][id]", []Inline{txt("[ref][id]")}},
		{"\\*", []Inline{txt("*")}},
	}
	for _, c := range cases {
		r := Render(c.in)
		if len(r.Blocks) != 1 {
			t.Errorf("%q: %d blocks", c.in, len(r.Blocks))
			continue
		}
		if !reflect.DeepEqual(r.Blocks[0].Inline, c.want) {
			t.Errorf("%q\n got:  %s\n want: %s", c.in, mustJSON(t, r.Blocks[0].Inline), mustJSON(t, c.want))
		}
	}
}

func TestRenderLists(t *testing.T) {
	r := Render("- [ ] todo\n- [x] done\n- plain\n  - nested\n\n3. three\n4. four\n\n- a\n\n- b\n")
	if len(r.Blocks) != 3 {
		t.Fatalf("blocks = %d: %s", len(r.Blocks), mustJSON(t, r))
	}
	l := r.Blocks[0]
	if l.Kind != "list" || l.Ordered || !l.Tight || len(l.Items) != 3 {
		t.Fatalf("list = %+v", l)
	}
	if l.Items[0].Checked == nil || *l.Items[0].Checked || l.Items[1].Checked == nil || !*l.Items[1].Checked || l.Items[2].Checked != nil {
		t.Errorf("task states wrong: %s", mustJSON(t, l.Items))
	}
	if got := l.Items[0].Blocks[0].Inline; !reflect.DeepEqual(got, []Inline{txt("todo")}) {
		t.Errorf("task text = %+v", got)
	}
	if l.Items[0].Blocks[0].Raw != "todo" {
		t.Errorf("task paragraph raw = %q", l.Items[0].Blocks[0].Raw)
	}
	item := l.Items[2]
	if len(item.Blocks) != 2 || item.Blocks[0].Kind != "paragraph" || item.Blocks[1].Kind != "list" ||
		item.Blocks[1].Items[0].Blocks[0].Inline[0].Text != "nested" {
		t.Errorf("nested item = %s", mustJSON(t, item))
	}
	o := r.Blocks[1]
	if !o.Ordered || o.Start == nil || *o.Start != 3 || !o.Tight || len(o.Items) != 2 {
		t.Errorf("ordered list = %+v", o)
	}
	loose := r.Blocks[2]
	if loose.Tight || len(loose.Items) != 2 {
		t.Errorf("loose list = %+v", loose)
	}
	// An empty item still has a (non-null) blocks array.
	if got := mustJSON(t, Render("-\n").Blocks[0].Items); got != `[{"blocks":[]}]` {
		t.Errorf("empty item json = %s", got)
	}
}

func TestRenderTable(t *testing.T) {
	r := Render("| 名称 | _n_ | r |\n|:--|:-:|--:|\n| a\\|b | `c` |\n| 1 | 2 | 3 | 4 |\n")
	tb := r.Blocks[0]
	if tb.Kind != "table" {
		t.Fatalf("kind = %s", tb.Kind)
	}
	if !reflect.DeepEqual(tb.Align, []string{"left", "center", "right"}) {
		t.Errorf("align = %v", tb.Align)
	}
	wantHeader := [][]Inline{{txt("名称")}, {{T: "em", Children: []Inline{txt("n")}}}, {txt("r")}}
	if !reflect.DeepEqual(tb.Header, wantHeader) {
		t.Errorf("header = %s", mustJSON(t, tb.Header))
	}
	wantRows := [][][]Inline{
		{{txt("a|b")}, {{T: "code", Text: "c"}}, {}},
		{{txt("1")}, {txt("2")}, {txt("3")}},
	}
	if !reflect.DeepEqual(tb.Rows, wantRows) {
		t.Errorf("rows = %s\nwant   %s", mustJSON(t, tb.Rows), mustJSON(t, wantRows))
	}
	if strings.Contains(mustJSON(t, r), "null") {
		t.Errorf("table json has null: %s", mustJSON(t, r))
	}
	if tb.Raw != "| 名称 | *n* | r |\n| :--- | :---: | ---: |\n| a\\|b | `c` |  |\n| 1 | 2 | 3 |" {
		t.Errorf("raw = %q", tb.Raw)
	}
}

func TestRenderCodeQuoteAndVerbatim(t *testing.T) {
	r := Render("```go title=x\nfmt.Println(1)\n\n```\n\n~~~\nplain\n~~~\n\n    indented\n\n> a\n>\n> > b\n\n---\n\n<div>\n_x_\n</div>\n\n[^1]: note\n\n[ref]: http://x\n\n$$\nx\n$$\n\n:::\nc\n:::\n")
	kinds := []string{}
	for _, b := range r.Blocks {
		kinds = append(kinds, b.Kind)
	}
	if want := []string{"code", "code", "code", "quote", "hr", "html", "unknown", "unknown", "unknown", "unknown"}; !reflect.DeepEqual(kinds, want) {
		t.Fatalf("kinds = %v\nwant    %v", kinds, want)
	}
	c := r.Blocks[0]
	if c.Lang != "go" || c.Info != "go title=x" || c.Text != "fmt.Println(1)\n" {
		t.Errorf("code = %+v", c)
	}
	if c := r.Blocks[1]; c.Lang != "" || c.Info != "" || c.Text != "plain" {
		t.Errorf("plain code = %+v", c)
	}
	if c := r.Blocks[2]; c.Text != "indented" || c.Raw != "```\nindented\n```" {
		t.Errorf("indented code = %+v", c)
	}
	q := r.Blocks[3]
	if len(q.Children) != 2 || q.Children[0].Kind != "paragraph" || q.Children[1].Kind != "quote" ||
		q.Children[1].Children[0].Inline[0].Text != "b" || q.Children[1].Raw != "> b" {
		t.Errorf("quote = %s", mustJSON(t, q))
	}
	if r.Blocks[4].Raw != "---" {
		t.Errorf("hr = %+v", r.Blocks[4])
	}
	if h := r.Blocks[5]; h.Text != "<div>\n_x_\n</div>" || len(h.Inline) != 0 {
		t.Errorf("html = %+v", h)
	}
	for i, want := range []string{"[^1]: note", "[ref]: http://x", "$$\nx\n$$", ":::\nc\n:::"} {
		if b := r.Blocks[6+i]; b.Text != want || b.Raw != want {
			t.Errorf("unknown %d = %+v, want %q", i, b, want)
		}
	}
}

func TestRenderIsPureFunctionOfNormalize(t *testing.T) {
	in := "Title\n=====\r\n\r\n* _a_\n* b  \r\n\r\n    code\n"
	a, b := Render(in), Render(Normalize(in))
	if mustJSON(t, a) != mustJSON(t, b) {
		t.Errorf("render(x) != render(normalize(x))")
	}
	if !reflect.DeepEqual(a, Render(in)) {
		t.Error("render not deterministic")
	}
}

func TestRenderTopLevelIDsMatchSplit(t *testing.T) {
	in := "# a\n\ntext\n\n- x\n- y\n\n```\nc\n```\n\n| a |\n|---|\n| b |\n"
	blocks := Split(in)
	r := Render(in)
	if len(blocks) != len(r.Blocks) {
		t.Fatalf("%d vs %d blocks", len(blocks), len(r.Blocks))
	}
	for i := range blocks {
		if blocks[i].ID != r.Blocks[i].BlockID || blocks[i].Kind != r.Blocks[i].Kind || blocks[i].Raw != r.Blocks[i].Raw {
			t.Errorf("block %d: split %+v vs render %+v", i, blocks[i], r.Blocks[i])
		}
	}
	// Nested blocks are identified by their own kind and raw text.
	q := Render("> a\n>\n> b\n").Blocks[0]
	for _, c := range q.Children {
		if c.BlockID != BlockID(c.Kind, c.Raw) {
			t.Errorf("nested id %q != BlockID(%q, %q)", c.BlockID, c.Kind, c.Raw)
		}
	}
}

func TestRenderJSONGolden(t *testing.T) {
	in := "# Notes _2026_\n\nSome **bold** text with a [link](https://x.y \"t\").\n\n- [x] done\n- [ ] todo\n  1. nested\n\n| a | b |\n|:-:|--:|\n| 1 | `2` |\n\n```go\nx := 1\n```\n\n> quote\n\n$$\nE=mc^2\n$$\n"
	got := mustJSON(t, Render(in))
	want := `{"blocks":[` +
		`{"block_id":"` + BlockID("heading", "# Notes *2026*") + `","kind":"heading","raw":"# Notes *2026*","level":1,"inline":[{"t":"text","text":"Notes "},{"t":"em","children":[{"t":"text","text":"2026"}]}]},` +
		`{"block_id":"` + BlockID("paragraph", "Some **bold** text with a [link](https://x.y \"t\").") + `","kind":"paragraph","raw":"Some **bold** text with a [link](https://x.y \"t\").","inline":[{"t":"text","text":"Some "},{"t":"strong","children":[{"t":"text","text":"bold"}]},{"t":"text","text":" text with a "},{"t":"link","href":"https://x.y","title":"t","children":[{"t":"text","text":"link"}]},{"t":"text","text":"."}]},` +
		`{"block_id":"` + BlockID("list", "- [x] done\n- [ ] todo\n  1. nested") + `","kind":"list","raw":"- [x] done\n- [ ] todo\n  1. nested","tight":true,"items":[` +
		`{"checked":true,"blocks":[{"block_id":"` + BlockID("paragraph", "done") + `","kind":"paragraph","raw":"done","inline":[{"t":"text","text":"done"}]}]},` +
		`{"checked":false,"blocks":[{"block_id":"` + BlockID("paragraph", "todo") + `","kind":"paragraph","raw":"todo","inline":[{"t":"text","text":"todo"}]},` +
		`{"block_id":"` + BlockID("list", "1. nested") + `","kind":"list","raw":"1. nested","ordered":true,"start":1,"tight":true,"items":[{"blocks":[{"block_id":"` + BlockID("paragraph", "nested") + `","kind":"paragraph","raw":"nested","inline":[{"t":"text","text":"nested"}]}]}]}]}]},` +
		`{"block_id":"` + BlockID("table", "| a | b |\n| :---: | ---: |\n| 1 | `2` |") + `","kind":"table","raw":"| a | b |\n| :---: | ---: |\n| 1 | ` + "`2`" + ` |","align":["center","right"],"header":[[{"t":"text","text":"a"}],[{"t":"text","text":"b"}]],"rows":[[[{"t":"text","text":"1"}],[{"t":"code","text":"2"}]]]},` +
		`{"block_id":"` + BlockID("code", "```go\nx := 1\n```") + `","kind":"code","raw":"` + "```go\\nx := 1\\n```" + `","lang":"go","info":"go","text":"x := 1"},` +
		`{"block_id":"` + BlockID("quote", "> quote") + `","kind":"quote","raw":"> quote","children":[{"block_id":"` + BlockID("paragraph", "quote") + `","kind":"paragraph","raw":"quote","inline":[{"t":"text","text":"quote"}]}]},` +
		`{"block_id":"` + BlockID("unknown", "$$\nE=mc^2\n$$") + `","kind":"unknown","raw":"$$\nE=mc^2\n$$","text":"$$\nE=mc^2\n$$"}` +
		`],"toc":[{"block_id":"` + BlockID("heading", "# Notes *2026*") + `","level":1,"text":"Notes 2026"}]}`
	if got != want {
		t.Errorf("render json mismatch\n got:  %s\n want: %s", got, want)
	}
}
