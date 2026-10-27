package markdown

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

const oneMiB = 1 << 20

func rep(s string, size int) string {
	return strings.Repeat(s, size/len(s)+1)[:size]
}

// repEnd is rep, but the result ends on a whole repetition.
func repN(s string, size int) string {
	return strings.Repeat(s, size/len(s))
}

func pathologicalInputs() map[string]string {
	m := map[string]string{
		"100k gt":                      strings.Repeat(">", 100000) + "x\n",
		"gt space 500k":                repN("> ", 500000) + "x\n",
		"gt lines":                     repN(">\n", oneMiB),
		"50k stars":                    strings.Repeat("*", 50000),
		"1M stars":                     rep("*", oneMiB),
		"unmatched star openers":       repN("*a ", oneMiB),
		"star pairs":                   repN("*a* ", oneMiB),
		"rule of 3 a**":                repN("a**", oneMiB),
		"rule of 3 **a":                repN("**a", oneMiB),
		"mixed star underscore":        repN("*a_", oneMiB),
		"mixed star underscore b":      repN("_a*_", oneMiB),
		"underscores":                  rep("_", oneMiB),
		"underscore words":             repN("_a", oneMiB),
		"intraword underscores":        repN("a_b", oneMiB),
		"tildes":                       rep("~", oneMiB),
		"tilde words":                  repN("~a", oneMiB),
		"backticks":                    rep("`", oneMiB),
		"backtick runs growing":        growingRuns("`", oneMiB),
		"backtick open close":          repN("`a ", oneMiB),
		"brackets open":                rep("[", oneMiB),
		"brackets open close":          strings.Repeat("[", oneMiB/2) + strings.Repeat("]", oneMiB/2),
		"brackets pairs":               repN("[a]", oneMiB),
		"link tails":                   repN("[a](", oneMiB),
		"link tails with titles":       repN("[a](b \"", oneMiB),
		"nested images":                strings.Repeat("![", 100000) + "a" + strings.Repeat("](b)", 100000),
		"nested links":                 strings.Repeat("[", 100000) + "a" + strings.Repeat("](b)", 100000),
		"many links in many brackets":  strings.Repeat("[", 50000) + repN("a](b)", 500000),
		"bang brackets":                repN("![a]", oneMiB),
		"lt runs":                      rep("<", oneMiB),
		"unterminated tags":            repN("<a ", oneMiB),
		"unterminated quoted attrs":    repN("<a b=\"", oneMiB),
		"unterminated comments":        repN("<!--", oneMiB),
		"unterminated pi":              repN("<?", oneMiB),
		"unterminated cdata":           repN("<![CDATA[", oneMiB),
		"autolink starts":              repN("<http://a", oneMiB),
		"email starts":                 repN("<a@b", oneMiB),
		"list items":                   repN("- a\n", oneMiB),
		"100k list items":              repN("-\n", 200000) + repN("- a\n", 400000),
		"ordered list items":           repN("1. a\n", oneMiB),
		"nested list single line":      rep("- ", oneMiB),
		"nested list indent":           nestedIndent(2000),
		"nested quotes lists":          repN("> - ", oneMiB),
		"loose list":                   repN("- a\n\n", oneMiB),
		"huge table":                   "| a | b | c | d | e |\n|---|---|---|---|---|\n" + repN("| 1 | 2 | 3 | 4 | 5 |\n", oneMiB),
		"wide table":                   "|" + strings.Repeat("a|", 250000) + "\n|" + strings.Repeat("-|", 250000) + "\n|" + strings.Repeat("b|", 250000) + "\n",
		"pipes":                        rep("|", oneMiB),
		"table headers":                repN("a|b\n--|--\n", oneMiB),
		"pipe lines":                   repN("a|b\n", oneMiB),
		"one long line":                rep("lorem ipsum ", oneMiB),
		"many short lines":             repN("a\n", oneMiB),
		"many paragraphs":              repN("a\n\n", oneMiB),
		"hard breaks":                  repN("a  \n", oneMiB),
		"backslash breaks":             repN("a\\\n", oneMiB),
		"backslashes":                  rep("\\", oneMiB),
		"backslash lines":              repN("\\\n", oneMiB),
		"headings":                     repN("# h\n", oneMiB),
		"setext":                       repN("a\n---\n", oneMiB),
		"hrs":                          repN("***\n", oneMiB),
		"fences":                       repN("```\n", oneMiB),
		"fenced code body":             "```\n" + repN("code line\n", oneMiB) + "```\n",
		"indented code":                repN("    code\n", oneMiB),
		"html blocks":                  repN("<div>\n", oneMiB),
		"html comment body":            "<!--\n" + repN("x\n", oneMiB),
		"ref defs":                     repN("[a]: b\n", oneMiB),
		"ref defs with titles":         repN("[a]: b \"t\"\n", oneMiB),
		"label starts":                 repN("[a\n", oneMiB),
		"footnotes":                    repN("[^1]: x\n", oneMiB),
		"footnote body":                "[^1]: x\n" + repN("    y\n\n", oneMiB),
		"math":                         "$$\n" + repN("x\n", oneMiB),
		"colon containers":             repN(":::\n", oneMiB),
		"tasks":                        repN("- [ ] a\n", oneMiB),
		"entities":                     repN("&amp;", oneMiB),
		"cjk":                          repN("你好，世界。_强调_ **粗体**\n", oneMiB),
		"emoji":                        repN("😀_a_", oneMiB),
		"tabs":                         repN("\t", oneMiB),
		"tab indented list":            repN("\t- a\n", oneMiB),
		"alternating list markers":     repN("- a\n\n* b\n\n", oneMiB),
		"paragraph needing guards":     "a\n" + repN("    - b\n", oneMiB),
		"deep quote in list":           repN("- > ", 200000) + "x",
		"code spans with escapes":      repN("`a\\`", oneMiB),
		"emphasis around code":         repN("*`a`*", oneMiB),
		"emphasis around links":        repN("*[a](b)*", oneMiB),
		"strikethrough pairs":          repN("~~a~~", oneMiB),
		"conversion candidates":        repN("_a_ ", oneMiB),
		"conversion with hard breaks":  repN("*a*  \n_b_  \n", oneMiB),
		"unicode punctuation runs":     repN("“*”", oneMiB),
		"invalid utf8":                 repN("\xff", oneMiB),
		"crlf lines":                   repN("a\r\n", oneMiB),
		"nul bytes":                    repN("\x00", oneMiB),
		"bom":                          repN("\xef\xbb\xbf", oneMiB),
		"quote lazy lines":             "> a\n" + repN("b\n", oneMiB),
		"list lazy lines":              "- a\n" + repN("b\n", oneMiB),
		"item continuation paragraphs": "- a\n" + repN("\n  b\n", oneMiB),
	}
	return m
}

func growingRuns(unit string, size int) string {
	var b strings.Builder
	for n := 1; b.Len() < size; n++ {
		b.WriteString(strings.Repeat(unit, n))
		b.WriteString(" ")
	}
	return b.String()[:size]
}

func nestedIndent(depth int) string {
	var b strings.Builder
	for i := 0; i < depth; i++ {
		b.WriteString(strings.Repeat(" ", 2*i))
		b.WriteString("- x\n")
	}
	return b.String()
}

// quickPerf is the subset of pathologicalInputs run by default.
var quickPerf = map[string]bool{
	"100k gt": true, "50k stars": true, "1M stars": true, "unmatched star openers": true, "underscores": true,
	"backticks": true, "backtick runs growing": true, "brackets open close": true, "link tails": true, "nested images": true,
	"emphasis around links": true, "100k list items": true, "huge table": true, "wide table": true,
	"conversion candidates": true, "unterminated tags": true,
}

// TestPerformanceGuard feeds ~1 MiB pathological documents through every
// public entry point; each must finish within a small, generous budget.
func TestPerformanceGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	budget := 3 * time.Second
	inputs := pathologicalInputs()
	if os.Getenv("MDPERF_FULL") == "" {
		// The default run covers one input per pathology; MDPERF_FULL=1 runs
		// every shape (about a minute).
		for k := range inputs {
			if !quickPerf[k] {
				delete(inputs, k)
			}
		}
	}
	if raceEnabled {
		// The race detector slows everything down by an order of magnitude;
		// keep the shapes (they are what matters there) but shrink them.
		for k, v := range inputs {
			if len(v) > 64<<10 {
				inputs[k] = v[:64<<10]
			}
		}
	}
	names := make([]string, 0, len(inputs))
	for k := range inputs {
		names = append(names, k)
	}
	sortStrings(names)
	for _, name := range names {
		in := inputs[name]
		t.Run(name, func(t *testing.T) {
			type result struct {
				norm              string
				blocks            int
				tNorm, tSplit, tR time.Duration
			}
			done := make(chan result, 1)
			go func() {
				var r result
				start := time.Now()
				r.norm = Normalize(in)
				r.tNorm = time.Since(start)
				start = time.Now()
				r.blocks = len(Split(in))
				r.tSplit = time.Since(start)
				start = time.Now()
				Render(in)
				r.tR = time.Since(start)
				done <- r
			}()
			select {
			case r := <-done:
				if testing.Verbose() {
					fmt.Printf("%-30s %8d B  normalize %9v  split %9v  render %9v  blocks %6d  out %8d B\n", name, len(in),
						r.tNorm.Round(time.Millisecond), r.tSplit.Round(time.Millisecond), r.tR.Round(time.Millisecond), r.blocks, len(r.norm))
				}
				if r.tNorm > budget || r.tSplit > 2*budget || r.tR > 2*budget {
					t.Errorf("too slow: normalize %v, split %v, render %v (budget %v)", r.tNorm, r.tSplit, r.tR, budget)
				}
				if Normalize(r.norm) != r.norm {
					t.Error("not idempotent")
				}
			case <-time.After(20 * budget):
				t.Fatalf("timed out after %v", 20*budget)
			}
		})
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// BenchmarkNormalize profiles one pathological input at a time:
//
//	go test -run '^$' -bench 'Normalize/tilde_words' -cpuprofile cpu.out ./notes/markdown
func BenchmarkNormalize(b *testing.B) {
	for name, in := range pathologicalInputs() {
		in := in
		b.Run(strings.ReplaceAll(name, " ", "_"), func(b *testing.B) {
			b.SetBytes(int64(len(in)))
			for i := 0; i < b.N; i++ {
				Normalize(in)
			}
		})
	}
}
