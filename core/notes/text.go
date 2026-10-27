package notes

import (
	"strings"
	"unicode/utf8"

	"nightclip/core/notes/markdown"
)

// The one-line texts a list shows (title and preview) and the text the search looks through come from
// the render tree, so they are plain text: no Markdown punctuation, no link syntax.

const (
	maxDisplayTitle = 80  // characters of a title taken from the body
	maxPreviewRunes = 160 // characters of preview
	maxPreviewLines = 12  // blocks looked at for a list row: it never needs more
)

// inlineText flattens inline nodes to plain text.
func inlineText(in []markdown.Inline, out *strings.Builder) {
	for _, n := range in {
		switch n.T {
		case "text", "code":
			out.WriteString(n.Text)
		case "image":
			out.WriteString(n.Alt)
		case "br", "softbr":
			out.WriteByte(' ')
		default:
			inlineText(n.Children, out)
		}
	}
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// textLines returns the plain text of the text-carrying blocks of a body, one entry per block, at most
// max entries (max <= 0: all of them).
func textLines(body string, max int) []string {
	var lines []string
	var walk func(blocks []markdown.RBlock)
	walk = func(blocks []markdown.RBlock) {
		for _, b := range blocks {
			if max > 0 && len(lines) >= max {
				return
			}
			var sb strings.Builder
			switch b.Kind {
			case "heading", "paragraph":
				inlineText(b.Inline, &sb)
			case "code":
				sb.WriteString(b.Text)
			case "quote":
				walk(b.Children)
				continue
			case "list":
				for _, it := range b.Items {
					walk(it.Blocks)
				}
				continue
			case "table":
				for _, cell := range b.Header {
					inlineText(cell, &sb)
					sb.WriteByte(' ')
				}
				if max <= 0 { // search reads the whole table, a list row only its header
					for _, row := range b.Rows {
						for _, cell := range row {
							inlineText(cell, &sb)
							sb.WriteByte(' ')
						}
					}
				}
			default: // hr, html, unknown
				continue
			}
			if t := collapse(sb.String()); t != "" {
				lines = append(lines, t)
			}
		}
	}
	walk(markdown.Render(body).Blocks)
	return lines
}

func cutRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:max]))
}

// listTexts is the title and preview a list shows for a note: the stored title, else the first line of
// the body (which the preview then does not repeat).
func listTexts(title, body string) (display, preview string) {
	lines := textLines(body, maxPreviewLines)
	display = title
	if display == "" && len(lines) > 0 {
		display = cutTitle(normTitle(cutRunes(lines[0], maxDisplayTitle)), MaxTitleBytes)
		lines = lines[1:]
	}
	return display, cutRunes(strings.Join(lines, " "), maxPreviewRunes)
}

// plainText is the whole text of a note body, blocks separated by line breaks.
func plainText(body string) string { return strings.Join(textLines(body, 0), "\n") }
