package notes

import (
	"fmt"
	"strings"
	"testing"

	"nightclip/core/proto"
)

func ids(r SearchResult) []string {
	var out []string
	for _, it := range r.Items {
		out = append(out, it.NoteID)
	}
	return out
}

func TestSearchFindsTextTitleAndChinese(t *testing.T) {
	d, clk := one(t)
	a := d.mustCreate("Groceries", "milk, **eggs** and bread\n")
	clk.tick()
	b := d.mustCreate("周末计划", "去超市买菜，然后写周报\n\n```\nmake check\n```\n")
	clk.tick()
	c := d.mustCreate("Trip", "pack the bags; see [the docs](http://example.com/secret-url)\n")

	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"EGGS", []string{a}},       // case-insensitive, formatting does not hide a word
		{"bread milk", []string{a}}, // every term must occur
		{"周报", []string{b}},         // no word boundaries needed
		{"make check", []string{b}}, // code blocks are searchable
		{"docs", []string{c}},       // link text, not the URL
		{"secret-url", nil},         // the URL is not note text
		{"nothing at all", nil},
		{"周末", []string{b}}, // title
	} {
		got, err := d.svc.Search(tc.query, 10, "")
		if err != nil {
			t.Fatalf("%q: %v", tc.query, err)
		}
		if fmt.Sprint(ids(got)) != fmt.Sprint(tc.want) && !(len(tc.want) == 0 && len(got.Items) == 0) {
			t.Fatalf("Search(%q) = %v, want %v", tc.query, ids(got), tc.want)
		}
	}
	if _, err := d.svc.Search("   ", 10, ""); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("empty query: %v", err)
	}
	if _, err := d.svc.Search("x", 10, "abc"); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("bad cursor: %v", err)
	}
}

func TestSearchRanksTitlesFirstAndMarksTheMatches(t *testing.T) {
	d, clk := one(t)
	body := d.mustCreate("misc", "alpha beta gamma alpha delta alpha\n")
	clk.tick()
	title := d.mustCreate("Alpha plan", "something else\n")

	got, _ := d.svc.Search("alpha", 10, "")
	if len(got.Items) != 2 || got.Items[0].NoteID != title || got.Items[1].NoteID != body {
		t.Fatalf("ranking = %v", ids(got))
	}
	if h := got.Items[0].TitleHits; len(h) != 1 || h[0] != (Highlight{0, 5}) {
		t.Fatalf("title highlights = %+v", h)
	}
	hit := got.Items[1]
	if len(hit.Highlights) != 3 {
		t.Fatalf("snippet %q highlights = %+v", hit.Snippet, hit.Highlights)
	}
	runes := []rune(hit.Snippet)
	for _, h := range hit.Highlights {
		if strings.ToLower(string(runes[h.Start:h.End])) != "alpha" {
			t.Fatalf("highlight %+v selects %q", h, string(runes[h.Start:h.End]))
		}
	}
}

func TestSearchSnippetsAroundALateMatchAndHighlightOffsetsAreCodePoints(t *testing.T) {
	d, _ := one(t)
	text := strings.Repeat("前缀😀 ", 60) + "needle" + strings.Repeat(" 后缀", 60)
	d.mustCreate("long", text+"\n")
	got, err := d.svc.Search("needle", 10, "")
	if err != nil || len(got.Items) != 1 {
		t.Fatalf("Search = %+v, %v", got, err)
	}
	it := got.Items[0]
	if !strings.HasPrefix(it.Snippet, "…") || !strings.HasSuffix(it.Snippet, "…") || len([]rune(it.Snippet)) > snippetRunes+2 {
		t.Fatalf("snippet = %q", it.Snippet)
	}
	runes := []rune(it.Snippet)
	if len(it.Highlights) != 1 || string(runes[it.Highlights[0].Start:it.Highlights[0].End]) != "needle" {
		t.Fatalf("highlights = %+v in %q", it.Highlights, it.Snippet)
	}
}

func TestSearchFollowsEditsDeletesAndPaging(t *testing.T) {
	d, clk := one(t)
	id := d.mustCreate("x", "first word\n")
	if got, _ := d.svc.Search("first", 10, ""); len(got.Items) != 1 {
		t.Fatalf("before edit: %+v", got)
	}
	g, _ := d.svc.Get(id, "")
	clk.tick()
	_, _ = d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Body: strp("second word\n")})
	if got, _ := d.svc.Search("first", 10, ""); len(got.Items) != 0 {
		t.Fatalf("the index kept the old text: %+v", got)
	}
	if got, _ := d.svc.Search("second", 10, ""); len(got.Items) != 1 {
		t.Fatalf("the index missed the new text: %+v", got)
	}
	clk.tick()
	_, _ = d.svc.Delete(id)
	if got, _ := d.svc.Search("second", 10, ""); len(got.Items) != 0 {
		t.Fatalf("a trashed note is still found: %+v", got)
	}
	clk.tick()
	_, _ = d.svc.Restore(id)
	if got, _ := d.svc.Search("second", 10, ""); len(got.Items) != 1 {
		t.Fatalf("a restored note is not found: %+v", got)
	}

	for i := 0; i < 7; i++ {
		clk.tick()
		d.mustCreate("", fmt.Sprintf("common term %d\n", i))
	}
	var all []string
	cursor := ""
	for {
		page, err := d.svc.Search("common", 3, cursor)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, ids(page)...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(all) != 7 {
		t.Fatalf("paging returned %d hits, want 7", len(all))
	}
	seen := map[string]bool{}
	for _, id := range all {
		if seen[id] {
			t.Fatalf("a hit appeared on two pages: %s", id)
		}
		seen[id] = true
	}
}

func TestSearchSeesNotesThatArrivedFromAnotherDevice(t *testing.T) {
	a, b, clk := twoDevices(t)
	id := a.mustCreate("shared", "remote needle\n")
	clk.tick()
	if got, _ := b.svc.Search("needle", 10, ""); len(got.Items) != 0 {
		t.Fatal("found a note that is not here yet")
	}
	b.syncWith(a)
	got, _ := b.svc.Search("needle", 10, "")
	if len(got.Items) != 1 || got.Items[0].NoteID != id {
		t.Fatalf("after sync: %+v", got)
	}
}
