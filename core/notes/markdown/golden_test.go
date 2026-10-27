package markdown

import (
	"strings"
	"testing"
)

type golden struct {
	name string
	in   string
	want string
}

func runGolden(t *testing.T, cases []golden) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Normalize(c.in)
			if got != c.want {
				t.Errorf("Normalize(%q)\n got:  %q\n want: %q", c.in, got, c.want)
			}
			if again := Normalize(got); again != got {
				t.Errorf("not idempotent: %q -> %q", got, again)
			}
			if once := preprocess(canonicalOnce(preprocess(c.in))); once != c.want {
				t.Errorf("a single pass already differs from the fixed point\n pass: %q\n want: %q", once, c.want)
			}
		})
	}
}

func TestGoldenBasics(t *testing.T) {
	runGolden(t, []golden{
		{"empty", "", ""},
		{"only newlines", "\n\n\n", ""},
		{"whitespace only", "  \t \n\t\n   ", ""},
		{"crlf", "a\r\nb\r\n", "a\nb\n"},
		{"cr only", "a\rb", "a\nb\n"},
		{"mixed endings", "a\r\n\r\nb\rc\n", "a\n\nb\nc\n"},
		{"no final newline", "a", "a\n"},
		{"many final newlines", "a\n\n\n\n", "a\n"},
		{"leading blank lines", "\n\n  \n# x", "# x\n"},
		{"blank run collapsed", "a\n\n\n\n\nb", "a\n\nb\n"},
		{"trailing spaces", "a   \nb   ", "a\\\nb\n"},
		{"invalid utf8", "a\xffb\xc3", "a\ufffdb\ufffd\n"},
		{"nul", "a\x00b", "a\ufffdb\n"},
		{"bom", "\ufeff# x", "# x\n"},
		{"bom inside", "a\ufeffb", "ab\n"},
		{"cjk and emphasis", "# 标题\n\n**加粗**文字，_斜体_。", "# 标题\n\n**加粗**文字，*斜体*。\n"},
		{"emoji", "😀 _x_ 👍🏽\n", "😀 *x* 👍🏽\n"},
		{"plain paragraph", "  indented para\n   second line\n", "indented para\nsecond line\n"},
		{"tab inside text kept", "a\tb\n", "a\tb\n"},
		{"two paragraphs", "a\n\nb\n", "a\n\nb\n"},
	})
}

func TestGoldenHeadings(t *testing.T) {
	runGolden(t, []golden{
		{"setext 1", "Title\n=====\n", "# Title\n"},
		{"setext 2", "Sub\n---\n", "## Sub\n"},
		{"setext multi line", "Foo\nbar\n---\n", "## Foo bar\n"},
		{"setext short underline", "Foo\n=\n", "# Foo\n"},
		{"atx spacing", "#   spaced   \n", "# spaced\n"},
		{"atx closing", "## closing ##   \n", "## closing\n"},
		{"atx closing long", "### a ##########\n", "### a\n"},
		{"atx empty", "#\n", "#\n"},
		{"atx empty spaces", "#   \n", "#\n"},
		{"atx empty closing", "## ##\n", "##\n"},
		{"seven hashes", "####### seven\n", "####### seven\n"},
		{"hash without space", "#nospace\n", "#nospace\n"},
		{"hash inside", "# a #b\n", "# a #b\n"},
		{"escaped closing hash", "# foo \\#\n", "# foo \\#\n"},
		{"heading text hash protected", "Foo #\n===\n", "# Foo \\#\n"},
		{"all levels", "# 1\n## 2\n### 3\n#### 4\n##### 5\n###### 6\n", "# 1\n\n## 2\n\n### 3\n\n#### 4\n\n##### 5\n\n###### 6\n"},
		{"heading with emphasis", "# _a_ __b__\n", "# *a* **b**\n"},
		{"heading interrupts paragraph", "text\n# h\nmore\n", "text\n\n# h\n\nmore\n"},
		{"indented heading", "   # h\n", "# h\n"},
		{"four spaces is code", "    # h\n", "```\n# h\n```\n"},
	})
}

func TestGoldenInline(t *testing.T) {
	runGolden(t, []golden{
		{"underscore em", "_a_\n", "*a*\n"},
		{"underscore strong", "__a__\n", "**a**\n"},
		{"star untouched", "*a* **b** ***c***\n", "*a* **b** ***c***\n"},
		{"triple underscore", "___x___\n", "***x***\n"},
		{"snake case", "snake_case_word and a_b_c\n", "snake_case_word and a_b_c\n"},
		{"dunder module name is strong", "__init__.py\n", "**init**.py\n"},
		{"dunder in code", "`__init__.py`\n", "`__init__.py`\n"},
		{"intraword em", "a_b_ c\n", "a_b_ c\n"},
		{"em containing underscore", "_a_b_\n", "*a_b*\n"},
		{"nested", "_a __b__ c_\n", "*a **b** c*\n"},
		{"strike single", "~x~\n", "~~x~~\n"},
		{"strike double", "~~x~~\n", "~~x~~\n"},
		{"strike triple literal", "a ~~~x~~~ b\n", "a ~~~x~~~ b\n"},
		{"tilde fence with info looks like strike", "~~~x~~~\n", "```x~~~\n```\n"},
		{"strike mixed lengths literal", "~x~~\n", "~x~~\n"},
		{"code span untouched", "`_x_` and ``a`b_c_``\n", "`_x_` and ``a`b_c_``\n"},
		{"escapes untouched", "\\_a\\_ \\*b\\*\n", "\\_a\\_ \\*b\\*\n"},
		{"link text converted", "[_a_](u_v_w)\n", "[*a*](u_v_w)\n"},
		{"link destination untouched", "[a](<u _v_> \"t _x_\")\n", "[a](<u _v_> \"t _x_\")\n"},
		{"image", "![_alt_](u)\n", "![*alt*](u)\n"},
		{"autolink untouched", "<http://a.b/c_d_e> <a_b@c.de>\n", "<http://a.b/c_d_e> <a_b@c.de>\n"},
		{"raw html untouched", "<span _a_ x=\"*b*\">_c_</span>\n", "<span _a_ x=\"*b*\">*c*</span>\n"},
		{"entities untouched", "&amp; &copy; &#35;\n", "&amp; &copy; &#35;\n"},
		{"em nested in em keeps source markers", "*_x_*\n", "*_x_*\n"},
		{"pairing hazard keeps source markers", "_a *b_ c*\n", "_a *b_ c*\n"},
		{"hard break spaces", "a  \nb\n", "a\\\nb\n"},
		{"hard break backslash", "a\\\nb\n", "a\\\nb\n"},
		{"hard break at end dropped spaces", "a  \n", "a\n"},
		{"soft break leading spaces", "a\n    b\n", "a\nb\n"},
		{"hard break after emphasis", "**b**  \nc\n", "**b**\\\nc\n"},
		{"literal backslash before break", "a\\ \nb\n", "a\\\\\nb\n"},
		{"literal backslash then hard break", "a\\   \nb\n", "a\\\\\\\nb\n"},
		{"unmatched stay literal", "*a _b `c [d ![e\n", "*a _b `c [d ![e\n"},
		{"reference link stays literal", "[foo][bar] [baz]\n", "[foo][bar] [baz]\n"},
		{"cjk emphasis", "中文_强调_中文 和 **粗体**测试\n", "中文_强调_中文 和 **粗体**测试\n"},
		{"multiline code span trailing space", "`a  \nb`\n", "`a\nb`\n"},
	})
}

func TestGoldenLists(t *testing.T) {
	runGolden(t, []golden{
		{"star bullets", "* a\n* b\n", "- a\n- b\n"},
		{"plus bullets", "+ a\n+ b\n", "- a\n- b\n"},
		{"paren ordered", "1) a\n2) b\n", "1. a\n2. b\n"},
		{"numbers kept", "1. a\n1. b\n1. c\n", "1. a\n1. b\n1. c\n"},
		{"odd numbers kept", "3. a\n7. b\n", "3. a\n7. b\n"},
		{"zero start", "0. a\n1. b\n", "0. a\n1. b\n"},
		{"wide marker nested", "10. a\n    - b\n", "10. a\n    - b\n"},
		{"wide marker item content", "9. a\n10. b\n    c\n", "9. a\n10. b\n    c\n"},
		{"loose", "- a\n\n- b\n", "- a\n\n- b\n"},
		{"loose by inner blank", "- a\n\n  b\n- c\n", "- a\n\n  b\n\n- c\n"},
		{"tight stays tight", "- a\n- b\n", "- a\n- b\n"},
		{"nested", "- a\n    - b\n        - c\n", "- a\n  - b\n    - c\n"},
		{"nested ordered in bullet", "- a\n  1. b\n  2. c\n", "- a\n  1. b\n  2. c\n"},
		{"task", "- [ ] a\n- [X] b\n- [x] c\n", "- [ ] a\n- [x] b\n- [x] c\n"},
		{"task needs text", "- [ ]\n- [x]\n", "- [ ]\n- [x]\n"},
		{"task needs space", "- [x]a\n", "- [x]a\n"},
		{"task in ordered", "1. [ ] a\n", "1. [ ] a\n"},
		{"adjacent lists stay separate", "- a\n\n* b\n", "- a\n\n* b\n"},
		{"adjacent ordered lists stay separate", "1. a\n\n1) b\n", "1. a\n\n1) b\n"},
		{"three adjacent lists", "- a\n\n* b\n\n+ c\n", "- a\n\n* b\n\n- c\n"},
		{"empty items", "-\n-\n", "-\n-\n"},
		{"empty item then text", "- \n  a\n", "- a\n"},
		{"code in item", "- a\n  ```\n  x\n  ```\n", "- a\n  ```\n  x\n  ```\n"},
		{"indented code in item", "- a\n\n      code\n", "- a\n\n  ```\n  code\n  ```\n"},
		{"hr in item", "- ***\n", "- ***\n"},
		{"hr in item via spaced stars", "- * * *\n", "- ***\n"},
		{"hr in ordered item", "1. ***\n", "1. ---\n"},
		{"hr in nested item", "- - * * *\n", "- - ***\n"},
		{"hr after paragraph in item", "- a\n  ***\n", "- a\n  ***\n"},
		{"ordered not 1 does not interrupt", "a\n2. b\n", "a\n2. b\n"},
		{"ordered 1 interrupts", "a\n1. b\n", "a\n\n1. b\n"},
		{"dash alone is a setext underline", "a\n-\n", "## a\n"},
		{"empty bullet does not interrupt", "a\n+\n", "a\n+\n"},
		{"lazy continuation", "- a\nb\n", "- a\n  b\n"},
		{"quote in item", "- > q\n  > r\n", "- > q\n  > r\n"},
		{"list ends at less indented", "- a\n b\n", "- a\n  b\n"},
		{"item blank lines and nested", "1. a\n\n   - b\n\n2. c\n", "1. a\n\n   - b\n\n2. c\n"},
		{"tabs after marker", "-\ta\n", "- a\n"},
		{"four spaces after marker", "-    a\n", "- a\n"},
		{"five spaces make code", "-     a\n", "- ```\n  a\n  ```\n"},
		{"six spaces keep one in code", "-      a\n", "- ```\n   a\n  ```\n"},
	})
}

func TestGoldenQuotes(t *testing.T) {
	runGolden(t, []golden{
		{"simple", "> a\n> b\n", "> a\n> b\n"},
		{"no space", ">a\n", "> a\n"},
		{"lazy", "> a\nb\n", "> a\n> b\n"},
		{"blank line inside", "> a\n>\n> b\n", "> a\n>\n> b\n"},
		{"nested", "> > a\n", "> > a\n"},
		{"nested compact", ">> a\n>> b\n", "> > a\n> > b\n"},
		{"nested then outer", "> > a\n> b\n", "> > a\n> > b\n"},
		{"two quotes", "> a\n\n> b\n", "> a\n\n> b\n"},
		{"leading blank in quote", ">\n> a\n", "> a\n"},
		{"empty quote", ">\n", ">\n"},
		{"list in quote", "> - a\n> - b\n", "> - a\n> - b\n"},
		{"code in quote", "> ```\n> x\n>\n> y\n> ```\n", "> ```\n> x\n>\n> y\n> ```\n"},
		{"indented code in quote", ">     code\n", "> ```\n> code\n> ```\n"},
		{"heading in quote", "> # h\n> text\n", "> # h\n>\n> text\n"},
		{"quote interrupts paragraph", "a\n> b\n", "a\n\n> b\n"},
		{"fenced code trailing spaces kept", "> ```\n> x  \n> ```\n", "> ```\n> x  \n> ```\n"},
		{"table in quote", "> | a |\n> |---|\n> | b |\n", "> | a |\n> | --- |\n> | b |\n"},
	})
}

func TestGoldenCode(t *testing.T) {
	runGolden(t, []golden{
		{"indented", "    code\n    more\n", "```\ncode\nmore\n```\n"},
		{"indented with blank", "    a\n\n    b\n", "```\na\n\nb\n```\n"},
		{"indented trailing blanks dropped", "    a\n\n\n", "```\na\n```\n"},
		{"indented keeps extra indent", "      a\n", "```\n  a\n```\n"},
		{"tab indented", "\tcode\n", "```\ncode\n```\n"},
		{"two tabs", "\t\tcode\n", "```\n\tcode\n```\n"},
		{"tilde fence", "~~~go\nx\n~~~\n", "```go\nx\n```\n"},
		{"backtick fence", "```go\nx\n```\n", "```go\nx\n```\n"},
		{"long fence", "`````\nx\n`````\n", "```\nx\n```\n"},
		{"info string", "```  ruby startline=3 \ncode\n```\n", "```ruby startline=3\ncode\n```\n"},
		{"empty fence", "```\n```\n", "```\n```\n"},
		{"unterminated", "```\nx\n", "```\nx\n```\n"},
		{"unterminated eof no newline", "~~~\nx", "```\nx\n```\n"},
		{"content with fence", "~~~\n```\nx\n```\n~~~\n", "````\n```\nx\n```\n````\n"},
		{"trailing spaces preserved", "```\nx  \n  \n```\n", "```\nx  \n  \n```\n"},
		{"crlf content", "```\r\nx\r\n```\r\n", "```\nx\n```\n"},
		{"tilde fence with backtick info", "~~~ a`b\nx\n~~~\n", "~~~a`b\nx\n~~~\n"},
		{"info starting with tilde", "~~~ ~~~x`\ny\n~~~\n", "~~~ ~~~x`\ny\n~~~\n"},
		{"fence interrupts paragraph", "a\n```\nx\n```\nb\n", "a\n\n```\nx\n```\n\nb\n"},
		{"fence indented by two", "  ```\n  x\n   y\n  ```\n", "```\nx\n y\n```\n"},
		{"markdown inside code untouched", "```\n# h\n_x_\n| a |\n```\n", "```\n# h\n_x_\n| a |\n```\n"},
		{"blank lines in fence", "```\na\n\n\nb\n```\n", "```\na\n\n\nb\n```\n"},
	})
}

func TestGoldenHRs(t *testing.T) {
	runGolden(t, []golden{
		{"stars", "***\n", "---\n"},
		{"underscores", "___\n", "---\n"},
		{"spaced", "- - -\n", "---\n"},
		{"long", "----------\n", "---\n"},
		{"indented", "   ***\n", "---\n"},
		{"after paragraph is setext", "a\n---\n", "## a\n"},
		{"after paragraph stars", "a\n***\n", "a\n\n---\n"},
		{"between blocks", "a\n\n***\n\nb\n", "a\n\n---\n\nb\n"},
		{"two dashes is text", "--\n", "--\n"},
	})
}

func TestGoldenTables(t *testing.T) {
	runGolden(t, []golden{
		{"basic", "| a | b |\n|---|---|\n| 1 | 2 |\n", "| a | b |\n| --- | --- |\n| 1 | 2 |\n"},
		{"no outer pipes", "a | b\n--|--\n1 | 2\n", "| a | b |\n| --- | --- |\n| 1 | 2 |\n"},
		{"alignment", "a|b|c|d\n:-|-:|:-:|---\n1|2|3|4\n", "| a | b | c | d |\n| :--- | ---: | :---: | --- |\n| 1 | 2 | 3 | 4 |\n"},
		{"padding removed", "|   a   |    b |\n| ----- | --- |\n|  1  |  2  |\n", "| a | b |\n| --- | --- |\n| 1 | 2 |\n"},
		{"short row padded", "| a | b |\n|---|---|\n| 1 |\n", "| a | b |\n| --- | --- |\n| 1 |  |\n"},
		{"long row truncated", "| a | b |\n|---|---|\n| 1 | 2 | 3 |\n", "| a | b |\n| --- | --- |\n| 1 | 2 |\n"},
		{"empty cells", "| a | b |\n|---|---|\n|  | x |\n", "| a | b |\n| --- | --- |\n|  | x |\n"},
		{"escaped pipe", "| a\\|b | c |\n|---|---|\n| `x\\|y` | z |\n", "| a\\|b | c |\n| --- | --- |\n| `x\\|y` | z |\n"},
		{"inline normalized", "| _a_ | ~b~ |\n|---|---|\n| __c__ | [d](e_f_g) |\n", "| *a* | ~~b~~ |\n| --- | --- |\n| **c** | [d](e_f_g) |\n"},
		{"single column", "| a |\n|---|\n| b |\n", "| a |\n| --- |\n| b |\n"},
		{"row without pipes", "| a |\n|---|\nb\n", "| a |\n| --- |\n| b |\n"},
		{"ends at blank line", "| a |\n|---|\n| b |\n\ntext\n", "| a |\n| --- |\n| b |\n\ntext\n"},
		{"header only", "| a | b |\n|---|---|\n", "| a | b |\n| --- | --- |\n"},
		{"after paragraph", "text\n| a |\n|---|\n| b |\n", "text\n\n| a |\n| --- |\n| b |\n"},
		{"mismatched columns is text", "| a | b |\n|---|\n", "| a | b |\n|---|\n"},
		{"no pipe in delimiter is not a table", "a\n:-\n", "a\n:-\n"},
		{"heading ends table", "| a |\n|---|\n| b |\n# h\n", "| a |\n| --- |\n| b |\n\n# h\n"},
		{"quote ends table", "| a |\n|---|\n| b |\n> q\n", "| a |\n| --- |\n| b |\n\n> q\n"},
		{"cjk cells", "| 名称 | 数量 |\n|:--|--:|\n| 苹果 | 3 |\n", "| 名称 | 数量 |\n| :--- | ---: |\n| 苹果 | 3 |\n"},
		{"table in list item", "- | a |\n  |---|\n  | b |\n", "- | a |\n  | --- |\n  | b |\n"},
	})
}

func TestGoldenVerbatim(t *testing.T) {
	runGolden(t, []golden{
		{"html block", "<div>\n  *x*\n</div>\n", "<div>\n  *x*\n</div>\n"},
		{"html block trailing spaces", "<div>  \nx\t\n</div>\n", "<div>\nx\n</div>\n"},
		{"html block ends at blank", "<div>\nx\n\n_y_\n", "<div>\nx\n\n*y*\n"},
		{"html comment spans blanks", "<!--\n\nx\n-->\n\ny\n", "<!--\n\nx\n-->\n\ny\n"},
		{"html comment one line", "<!-- _x_ -->\n_y_\n", "<!-- _x_ -->\n\n*y*\n"},
		{"script block", "<script>\nif (a < b) {}\n\nx\n</script>\ntext\n", "<script>\nif (a < b) {}\n\nx\n</script>\n\ntext\n"},
		{"pre block", "<pre>\n_a_\n</pre>\n", "<pre>\n_a_\n</pre>\n"},
		{"html indented first line", "  <div>\n", "<div>\n"},
		{"inline-looking tag alone is html block", "<b>\n", "<b>\n"},
		{"inline tag in text is text", "a <b>_x_</b>\n", "a <b>*x*</b>\n"},
		{"footnote", "[^1]: note\n", "[^1]: note\n"},
		{"footnote continuation", "[^1]: note\n    more\n\n    para\n\ntext\n", "[^1]: note\n    more\n\n    para\n\ntext\n"},
		{"footnote trailing spaces", "[^a]: x   \n", "[^a]: x\n"},
		{"two footnotes", "[^1]: a\n[^2]: b\n", "[^1]: a\n\n[^2]: b\n"},
		{"reference definition", "[a]: http://x \"t\"  \n", "[a]: http://x \"t\"\n"},
		{"reference definitions", "[a]: http://x\n[b]: <y z>\n", "[a]: http://x\n\n[b]: <y z>\n"},
		{"reference definition title next line", "[a]: http://x\n  'title'\n", "[a]: http://x\n'title'\n"},
		{"reference then paragraph", "[a]: u\ntext\n", "[a]: u\n\ntext\n"},
		{"reference then block-like text stays one paragraph", "[a]: u\n$$\n", "[a]: u\n$$\n"},
		{"reference then ordered item text stays one paragraph", "[a]: u\n2. x\n", "[a]: u\n2. x\n"},
		{"math", "$$\nx_1 + y_2\n$$\n", "$$\nx_1 + y_2\n$$\n"},
		{"math one line", "$$ a_b $$\ntext\n", "$$ a_b $$\n\ntext\n"},
		{"math keeps blank lines", "$$\na\n\nb\n$$\n", "$$\na\n\nb\n$$\n"},
		{"math unterminated swallows rest", "$$\na\n\nb\n", "$$\na\n\nb\n"},
		{"math does not interrupt paragraph", "a\n$$\nb\n", "a\n$$\nb\n"},
		{"container", ":::note\n**hi**\n\nx\n:::\ntext\n", ":::note\n**hi**\n\nx\n:::\n\ntext\n"},
		{"container unterminated", ":::\nx\n", ":::\nx\n"},
		{"unknown in quote", "> [^1]: x\n> $$\n> y\n> $$\n", "> [^1]: x\n>\n> $$\n> y\n> $$\n"},
		{"unknown in list", "- $$\n  y\n  $$\n- b\n", "- $$\n  y\n  $$\n- b\n"},
		{"html in list", "- <div>\n  x\n", "- <div>\n  x\n"},
	})
}

func TestGoldenParagraphGuards(t *testing.T) {
	runGolden(t, []golden{
		// Indented lines continue a paragraph; after stripping the indent they
		// would start a block, so the canonical form keeps them indented.
		{"indented list-like continuation", "foo\n    - bar\n", "foo\n    - bar\n"},
		{"indented heading-like continuation", "foo\n    # bar\n", "foo\n    # bar\n"},
		{"indented fence-like continuation", "foo\n    ```\n", "foo\n    ```\n"},
		{"indented underline-like continuation", "foo\n    ---\n", "foo\n    ---\n"},
		{"indented quote-like continuation", "foo\n    > bar\n", "foo\n    > bar\n"},
		{"indented table-delimiter continuation", "| a |\n    |---|\n", "| a |\n    |---|\n"},
		{"plain continuation", "foo\nbar\n", "foo\nbar\n"},
		{"digit start continuation", "foo\n2024 was good\n", "foo\n2024 was good\n"},
	})
}

func TestGoldenDocument(t *testing.T) {
	in := "Meeting notes\r\n=============\r\n\r\n*  Attendees:  Alice,  Bob\r\n*  Agenda\r\n    1)  Budget  _review_\r\n    2)  Hiring\r\n\r\n- [ ] send __minutes__\r\n- [X] book room\r\n\r\n> **Note**\r\n> keep it short\r\n\r\n| Item | Cost |\r\n|:--|--:|\r\n| Pens | 3 |\r\n| Paper | 12 | extra |\r\n\r\n```py\r\nprint('hi')   \r\n```\r\n\r\nSee [docs](https://x.y/a_b_c) or <https://x.y>.  \r\nLine two with `code_span_` and snake_case.\r\n\r\n***\r\n"
	want := "# Meeting notes\n\n- Attendees:  Alice,  Bob\n- Agenda\n  1. Budget  *review*\n  2. Hiring\n\n* [ ] send **minutes**\n* [x] book room\n\n> **Note**\n> keep it short\n\n| Item | Cost |\n| :--- | ---: |\n| Pens | 3 |\n| Paper | 12 |\n\n```py\nprint('hi')   \n```\n\nSee [docs](https://x.y/a_b_c) or <https://x.y>.\\\nLine two with `code_span_` and snake_case.\n\n---\n"
	runGolden(t, []golden{{"meeting notes", in, want}})
	kinds := []string{}
	for _, b := range Split(in) {
		kinds = append(kinds, b.Kind)
	}
	if got, want := strings.Join(kinds, ","), "heading,list,list,quote,table,code,paragraph,hr"; got != want {
		t.Errorf("kinds = %s, want %s", got, want)
	}
}

func TestGoldenDepthCap(t *testing.T) {
	deep := strings.Repeat("> ", 40) + "x\n"
	got := Normalize(deep)
	want := strings.Repeat("> ", maxBlockDepth) + strings.Repeat("> ", 40-maxBlockDepth) + "x\n"
	if got != want {
		t.Errorf("deep quote\n got:  %q\n want: %q", got, want)
	}
	if again := Normalize(got); again != got {
		t.Errorf("deep quote not idempotent")
	}
	r := Render(deep)
	depth := 0
	b := r.Blocks[0]
	for b.Kind == "quote" {
		depth++
		if len(b.Children) == 0 {
			break
		}
		b = b.Children[0]
	}
	if depth != maxBlockDepth || b.Kind != "paragraph" {
		t.Errorf("render depth = %d, last kind %q", depth, b.Kind)
	}
	if len(b.Inline) == 0 || !strings.HasPrefix(b.Inline[0].Text, ">") {
		t.Errorf("beyond the cap the markers must be literal text: %+v", b.Inline)
	}
	// Lists nest quote-like.
	list := ""
	for i := 0; i < 30; i++ {
		list += strings.Repeat("  ", i) + "- x\n"
	}
	if n := Normalize(list); Normalize(n) != n {
		t.Errorf("deep list not idempotent")
	}
}
