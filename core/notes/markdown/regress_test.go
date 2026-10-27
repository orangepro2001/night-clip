package markdown

import "testing"

// regressionInputs are documents that once broke an invariant (found by the
// random sweeps or the fuzzer). Each must now satisfy all of them.
var regressionInputs = []string{
	// reference definition followed by text that would start a block on its own
	"###### \n[a]: http://xsnake_case\n$$\n你好",
	"[a]: http://x1.*[x](y)\n$$[x](y)\n````\n\n[x] ",
	"[a]: <b> 'c'\n<b>\n- > **[^1]: x- 1. ===\n",
	"  2) [a]: u\n    ```\n->    ```\n- [ ] ===\n1. - `c`\n\n",
	"* 1.     code\nword_with_under\n    code\n>- [ ] + ## head ##\n> * [a]: u\n    ```\n-10.   ```go\n2) 10. -->\n   -->\n",
	"[a]: <b> 'c'\n\\|text_-->snake_case\n`a_b_c===\n\\\n\t~~~~***\n",
	// byte order mark that only becomes the first character after blank lines are dropped
	"\n\n\n\xef\xbb\xbf | [a](<b c>)\r]\n</script>",
	"\xef\xbb\xbf\n  \n\xef\xbb\xbf|:::\n***\\\n</div>**s**<b><a@b.co>```3. \n__\n* (```* [a](<b c>)<a@b.co>",
	"\r\n\xef\xbb\xbf* \n[x](y)(```\\\n3. [a]: <b> 'c'|---|__\n*",
	// hard breaks next to emphasis delimiters
	"~1.[a]: http://x===___    \n|**\n[\\0.\n\x002) [x](y)>10. ####### \\___\n<a@b.co>&amp;\n``",
	"fooword\n__```  \n</script>bar  \nword&amp;===*    \n | `` -***``:::note</div>\n:::&amp;__\n[ ] )<a@b.co>\x00\xef\xbb\xbf2) - - -",
	"\\    \n``-->\xef\xbb\xbf\x00> \nfoo---***\n~-->~\\\n<http://a.b><?php\n> > # 0.😀bar\n-->😀\n`code`\n`code`<http://a.b>\u00a0\\\n___\n-->> \t",
	"***\\\n\t*em*\\|\\|~~~~|---|***\r\n1.| a | b |    \n~~d~~<a@b.co>\r\n\x00<a@b.co><b>*\n- - -_###### <?php\n***text\n\t<!--####### \n\\\n\n~###### ",
	"- - -&amp;\t\n)\n</script>_10. * ````\n\\***    \n``\x00\n|---| | \n| :-: | --: |_~~~<div><?php\r\n\n<?php[[a]: http://x😀[a](<b c>)\n[a]: <b> 'c'\nfoo\n",
	"3. ___\\\n:::__<div>\n`code`*em* | \n1. $$[a]: http://x> > 10. \n<b>\n)[^1]: x- - --->foo<?php",
	// tilde fence whose info string starts with tildes
	"~\n~~~    ~~~)~~~~<!--bar`code`\n- - -## ",
	"###### > #\\|> > \n&amp;<div>~~~\n\u00a0\n*em*___+ \n(\n~~~\t~~~text>~~~`code`\nword$$---- [a]: <b> 'c'~~d~~\\|",
	// delimiter row that looks like a list item, header that is a definition, task marker in the header
	"2) -| 1 | 2 |\n\t+ |---|---|\n",
	">1. | 1 | 2 |\n - [x] | 1 | 2 |\n    * |---|---|\n## head ##\n",
	"10. [a]: http://x[\n|---|\r\n## ~~d~~[[ ] \n<b>1. :::<script>[a](<b c>)  \x00\n__\n\r\n\t\n~\n(__\n\\|**snake_case",
	"2) - \n><script>+ \n-    [ ]  |  _>)\t~~d~~\n    | :-: | --: |\n| :-: | --: |word\r\n[x] ",
	// thematic break below a paragraph, and as the first block of an item
	" | &amp;# \n-->\n[2) [bar***\n*|---|\n-###### \n\n- \U0001F600]\n\\\n\t***",
	"+ _em_-->[a](<b c>)>\\|\n\t* ___\xef\xbb\xbf\n][\n\n你好\n*  **+ **\n\n<?php#\n\n<b>#> ===|---|| a | b |[^1]: x\x00\\\n_em_",
	"-- - -\n1. | 1 | 2 |\n    >`c`\n   >    \n      _em_ and *s*\n\t2) ===\n- [x] 1. - - -\n",
	// tabs in verbatim blocks nested in lists
	"2) > :::\n    <div>\n-| a | b |\n* \t[^1]: n\n\t\t<div>\n[ ] t\n   ## head ##\n>more text\n      [ ] t\n- [ ] \t- :::\n    $$\n",
	"`c`\n`c`\n- [ ] - 10. more text\n- [ ] +   - - -\n2) _em_ and *s*\n- - -\n 1. [^1]: n\n\t\t\\\n__x__\n",
	"__x__\n\t|---|---|\n + [^1]: n\n    \t10. <!--\n- [x] * 2) <!--\n- [ ] -->\n>1. + $$\n| a | b |\n<!--\n- \t   ---\n  2) + ***\n * 1. \n",
	"\n\t- [a]: u\n * [^1]: n\n\t\t>2. x\n1. -->\n- [ ] * 1. [^1]: n\n- [ ] > ## head ##\n- [ ] >     [ ] t\n|---|---|\n> 2) [a]: u\n<!--\n",
}

func TestRegressions(t *testing.T) {
	for i, in := range regressionInputs {
		if bad := violation(in); bad != "" {
			t.Errorf("regression %d: %s\n in:   %q\n norm: %q", i, bad, in, Normalize(in))
		}
	}
}

// TestRegressionSemantics pins the exact behaviour behind a few of them.
func TestRegressionSemantics(t *testing.T) {
	runGolden(t, []golden{
		{"emphasis markers kept when rewriting would change pairing", "_a *b_ c*\n", "_a *b_ c*\n"},
		{"hard break keeps meaning after a lone delimiter", "a ~~  \nb\n", "a ~~\\\nb\n"},
		{"hard break spaces kept where a backslash would change delimiter reading", "x.*  \n*y*\n", "x.*\\\n*y*\n"},
		{"thematic break below a paragraph in a tight item", "- a\n  ***\n", "- a\n  ***\n"},
		{"tab in footnote continuation", "- [^1]: n\n\t\tx\n", "- [^1]: n\n        x\n"},
		{"table header must not be a definition", "[a]: u\n|---|\n", "[a]: u\n\n|---|\n"},
		{"task marker is part of the paragraph", "- [ ] |  a\n| :-: | --: |\n", "- [ ] |  a\n      | :-: | --: |\n"},
	})
}
