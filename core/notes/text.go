package notes

import (
	"strings"
	"unicode/utf8"

	"nightclip/core/notes/markdown"
)

// The one-line texts a list shows (title and preview) come from the render tree, so they are plain
// text: no Markdown punctuation, no link syntax.

const (
	maxDisplayTitle = 80  // characters of a title taken from the body
	maxPreviewRunes = 160 // characters of preview
	maxTextLines    = 12  // blocks looked at: a list row never needs more
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

// textLines returns the plain text of the first text-carrying blocks of a body, one entry per block.
func textLines(body string) []string {
	var lines []string
	var walk func(blocks []markdown.RBlock)
	walk = func(blocks []markdown.RBlock) {
		for _, b := range blocks {
			if len(lines) >= maxTextLines {
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
				for i, cell := range b.Header {
					if i > 0 {
						sb.WriteByte(' ')
					}
					inlineText(cell, &sb)
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
	lines := textLines(body)
	display = title
	if display == "" && len(lines) > 0 {
		display = cutTitle(normTitle(cutRunes(lines[0], maxDisplayTitle)), MaxTitleBytes)
		lines = lines[1:]
	}
	return display, cutRunes(strings.Join(lines, " "), maxPreviewRunes)
}
