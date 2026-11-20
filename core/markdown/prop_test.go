package markdown

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

var fragments = []string{
	"#", "# ", "## ", "###### ", "####### ", "> ", ">", "> > ", "- ", "* ", "+ ", "-", "1. ", "2) ", "10. ", "3. ",
	"    ", "  ", " ", "\t", "```", "~~~", "```go", "````", "|", " | ", "|---|", "| :-: | --: |", "---", "===", "***", "- - -",
	"_", "__", "___", "*", "**", "***", "~", "~~", "~~~~", "[x](y)", "[", "]", "(", ")", "![a](b \"t\")", "`", "``", "[a](<b c>)",
	"\n", "\n", "\n\n", "text", "word", "snake_case", "a_b_c", "你好", "😀", "<div>", "</div>", "<!--", "-->", "<b>", "[^1]: x", "$$", ":::", ":::note",
	"[ ] ", "[x] ", "\\", "&amp;", "<http://a.b>", "<a@b.co>", "  \n", "\\\n", "[a]: http://x", "[a]: <b> 'c'", "<script>", "</script>", "<?php", "foo", "bar",
	"| a | b |", "\\|", "`code`", "*em*", "_em_", "**s**", "~~d~~", "1.", "0.", "999999999. ", "\xc2\xa0", "\xef\xbb\xbf", "\x00", "\r\n", "\r",
}

func soup(seed int64) string {
	rng := rand.New(rand.NewSource(seed))
	n := rng.Intn(36) + 1
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(fragments[rng.Intn(len(fragments))])
		if rng.Intn(4) == 0 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// dumpDoc renders the semantic structure of a block tree for comparisons that
// must ignore canonical formatting.
func dumpDoc(n *node) string {
	var b strings.Builder
	var walk func(n *node)
	walk = func(n *node) {
		fmt.Fprintf(&b, "(%d", n.kind)
		switch n.kind {
		case kPara, kHeading:
			if n.kind == kHeading {
				fmt.Fprintf(&b, " L%d", n.level)
			}
			b.WriteString(" " + dumpInl(semNodes(parseInlines(trimRightST(strings.Join(n.lines, "\n")), false))))
		case kCode:
			fmt.Fprintf(&b, " %q %q", n.info, n.lines)
		case kHTML, kUnknown:
			fmt.Fprintf(&b, " %q", n.lines)
		case kTable:
			fmt.Fprintf(&b, " %v", n.align)
			for _, row := range append([]string{n.header}, n.lines...) {
				cells := splitRow(row)
				for len(cells) < len(n.align) {
					cells = append(cells, "")
				}
				cells = cells[:len(n.align)]
				for _, c := range cells {
					b.WriteString(" [" + dumpInl(semNodes(parseInlines(c, true))) + "]")
				}
			}
		case kList:
			fmt.Fprintf(&b, " o%v", n.ordered)
		case kItem:
			fmt.Fprintf(&b, " k%d", n.task)
		}
		for _, k := range n.kids {
			walk(k)
		}
		b.WriteString(")")
	}
	walk(n)
	return b.String()
}

func parseNoPanic(t *testing.T, name, in string) (out string, ok bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: panic %v on %q", name, r, in)
			ok = false
		}
	}()
	return preprocess(canonicalOnce(preprocess(in))), true
}

// violation checks every invariant of the package for one input and returns a
// description of the first one that fails ("" if all hold). It calls the
// unguarded canonicalOnce so that a panic is reported instead of being
// absorbed by Normalize's safety net.
func violation(in string) (bad string) {
	defer func() {
		if r := recover(); r != nil {
			bad = fmt.Sprintf("panic: %v", r)
		}
	}()
	pre := preprocess(in)
	once := preprocess(canonicalOnce(pre))
	norm := Normalize(in)
	if again := Normalize(norm); again != norm {
		return fmt.Sprintf("not idempotent: %q -> %q", norm, again)
	}
	if norm != "" && (!strings.HasSuffix(norm, "\n") || strings.HasSuffix(norm, "\n\n")) {
		return fmt.Sprintf("bad trailing newline %q", norm)
	}
	if strings.ContainsAny(norm, "\r\x00\xef\xbb\xbf") {
		return fmt.Sprintf("CR, NUL or BOM in output %q", norm)
	}
	// Strict: one pass must already reach the fixed point, so that the
	// cycle-breaking safety net in Normalize is never what makes it idempotent.
	if twice := preprocess(canonicalOnce(once)); twice != once {
		return fmt.Sprintf("one pass is not a fixed point: %q -> %q", once, twice)
	}
	if once != norm {
		return fmt.Sprintf("Normalize %q differs from one pass %q", norm, once)
	}
	blocks := Split(in)
	raws := make([]string, len(blocks))
	for k, b := range blocks {
		raws[k] = b.Raw
		if b.ID != BlockID(b.Kind, b.Raw) {
			return "block id mismatch"
		}
	}
	if JoinRaw(raws) != norm {
		return fmt.Sprintf("Join(Split) %q != Normalize %q", JoinRaw(raws), norm)
	}
	if norm != "" {
		if doc, _ := normalizedBlocks(norm); doc == nil {
			return "Split had to fall back to a single verbatim block"
		}
	}
	if !reflect.DeepEqual(blocks, Split(norm)) {
		return "Split(Normalize(x)) differs from Split(x)"
	}
	r1, r2 := Render(in), Render(norm)
	j1, err1 := json.Marshal(r1)
	j2, err2 := json.Marshal(r2)
	if err1 != nil || err2 != nil || string(j1) != string(j2) {
		return fmt.Sprintf("render not deterministic or not marshalable (%v %v)", err1, err2)
	}
	if strings.Contains(string(j1), "null") {
		return "render JSON contains null: " + string(j1)
	}
	if a, b := dumpDoc(parseBlocks(pre, 0)), dumpDoc(parseBlocks(norm, 0)); a != b {
		return fmt.Sprintf("normalization changed the structure\n   %s\n   %s", a, b)
	}
	return ""
}

func sweepSeeds(t *testing.T, name string, gen func(int64) string, defaultCases int) {
	cases := defaultCases
	if testing.Short() {
		cases = defaultCases / 10
	}
	if raceEnabled {
		cases = defaultCases / 4 // the code is single-threaded; the race detector only costs time
	}
	// MDSOUP_BASE / MDSOUP_N let a developer sweep other seed ranges.
	base := int64(0)
	if v, err := strconv.Atoi(os.Getenv("MDSOUP_BASE")); err == nil {
		base = int64(v)
	}
	if v, err := strconv.Atoi(os.Getenv("MDSOUP_N")); err == nil && v > 0 {
		cases = v
	}
	reported := 0
	for i := 0; i < cases; i++ {
		seed := int64(i) + base
		in := gen(seed)
		if bad := violation(in); bad != "" {
			reported++
			if reported <= 10 {
				t.Errorf("%s seed %d: %s\n in:   %q\n norm: %q", name, seed, bad, in, Normalize(in))
			}
		}
	}
	if reported > 0 {
		t.Fatalf("%d violations in %d cases", reported, cases)
	}
}

// TestPropertyRandomSoups is the main safety net: tens of thousands of
// deterministic random documents must normalize without panics and with all
// invariants holding.
func TestPropertyRandomSoups(t *testing.T) {
	sweepSeeds(t, "soup", soup, 20000)
}

var (
	prefixes = []string{"> ", "- ", "* ", "+ ", "1. ", "2) ", "10. ", "- [ ] ", "- [x] ", ">", "-", "   ", "  ", "    ", "\t", " "}
	contents = []string{
		"text", "more text", "_em_ and *s*", "# head", "## head ##", "```", "```go", "~~~", "    code", "---", "===", "***",
		"| a | b |", "|---|---|", "| 1 | 2 |", "<div>", "</div>", "$$", ":::", "[^1]: n", "[a]: u", "<!--", "-->", "", "", "",
		"a  ", "b\\", "`c`", "你好", "- - -", "2. x", "[ ] t", "\\", "word_with_under", "__x__",
	}
)

func structuredSoup(seed int64) string {
	rng := rand.New(rand.NewSource(seed))
	n := rng.Intn(14) + 1
	var b strings.Builder
	for i := 0; i < n; i++ {
		for k := rng.Intn(4); k > 0; k-- {
			b.WriteString(prefixes[rng.Intn(len(prefixes))])
		}
		b.WriteString(contents[rng.Intn(len(contents))])
		b.WriteByte('\n')
	}
	return b.String()
}

// TestPropertyStructuredSoups stresses container nesting with line-oriented
// documents (container prefixes in front of block-looking content).
func TestPropertyStructuredSoups(t *testing.T) {
	sweepSeeds(t, "structured", structuredSoup, 10000)
}

func FuzzNormalize(f *testing.F) {
	seeds := []string{
		"", "\n", "# h\n", "- a\n- b\n", "1. a\n   - b\n", "> q\n> > r\n", "| a | b |\n|---|---|\n| 1 | 2 |\n",
		"```go\ncode\n```\n", "    code\n", "~~~\nx\n~~~\n", "a  \nb\\\nc\n", "_a_ __b__ ~c~ snake_case\n", "<div>\nx\n</div>\n",
		"[^1]: n\n", "[a]: http://x\n", "$$\nx\n$$\n", ":::\nx\n:::\n", "- [ ] t\n- [x] d\n", "***\n---\n___\n", "Title\n=====\n",
		"[x](y \"z\") ![i](j) <http://a.b> `c` **s** *e*\n", "a\r\nb\rc\n", "\xff\xfe", "\xef\xbb\xbf# x", "- - -\n", "- ***\n", "*_x_* _a *b_ c*",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	for i := 0; i < 40; i++ {
		f.Add(soup(int64(1000 + i)))
	}
	f.Fuzz(func(t *testing.T, in string) {
		if bad := violation(in); bad != "" {
			t.Fatalf("%s\n in: %q", bad, in)
		}
	})
}

// semNodes copies an inline tree with escapes resolved and line-end
// whitespace dropped, so that two serialisations of the same meaning dump
// identically.
func semNodes(ns []*inl) []*inl {
	out := make([]*inl, len(ns))
	for i, n := range ns {
		c := *n
		switch n.kind {
		case iText:
			c.src = unescapeText(n.src)
		case iHTML:
			// raw inline HTML is displayed as literal text
			c.kind, c.src = iText, rightTrimLines(n.src)
		}
		c.kids = semNodes(n.kids)
		out[i] = &c
	}
	return out
}

// deepSoup builds documents whose containers nest well past the depth cap.
func deepSoup(seed int64) string {
	rng := rand.New(rand.NewSource(seed))
	marks := []string{"> ", "- ", "1. ", "* ", ">", "  ", "    "}
	var b strings.Builder
	for i := rng.Intn(6) + 1; i > 0; i-- {
		for d := rng.Intn(24) + 8; d > 0; d-- {
			b.WriteString(marks[rng.Intn(len(marks))])
		}
		b.WriteString(contents[rng.Intn(len(contents))])
		b.WriteByte('\n')
	}
	return b.String()
}

// TestPropertyDeepNesting covers the container depth cap.
func TestPropertyDeepNesting(t *testing.T) {
	sweepSeeds(t, "deep", deepSoup, 3000)
}
