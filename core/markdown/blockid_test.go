package markdown

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

func TestHash(t *testing.T) {
	if got := Hash(""); got != "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Errorf("Hash(\"\") = %s", got)
	}
	sum := sha256.Sum256([]byte("# a\n"))
	if got, want := Hash("# a\n"), "sha256:"+hex.EncodeToString(sum[:]); got != want {
		t.Errorf("Hash = %s, want %s", got, want)
	}
	if Hash("a\n") == Hash("b\n") {
		t.Error("distinct bodies must hash differently")
	}
	// Equivalent spellings share one revision hash.
	if Hash(Normalize("* _a_\r\n")) != Hash(Normalize("- *a*\n")) {
		t.Error("equivalent spellings must hash alike")
	}
}

func TestBlockIDFormula(t *testing.T) {
	sum := sha256.Sum256([]byte("paragraph\x00hello"))
	want := "b_" + hex.EncodeToString(sum[:])[:16]
	if got := BlockID("paragraph", "hello"); got != want {
		t.Errorf("BlockID = %s, want %s", got, want)
	}
	if len(want) != 18 {
		t.Errorf("id length = %d", len(want))
	}
	if BlockID("paragraph", "x") == BlockID("heading", "x") {
		t.Error("kind must be part of the id")
	}
	// The NUL separator keeps (kind, raw) pairs from colliding.
	if BlockID("ab", "c") == BlockID("a", "bc") {
		t.Error("kind/raw boundary must matter")
	}
}

func TestJoinRaw(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"a"}, "a\n"},
		{[]string{"a", "b"}, "a\n\nb\n"},
		{[]string{"# h", "- x\n- y", "```\nc\n```"}, "# h\n\n- x\n- y\n\n```\nc\n```\n"},
	}
	for _, c := range cases {
		if got := JoinRaw(c.in); got != c.want {
			t.Errorf("JoinRaw(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSplitBasics(t *testing.T) {
	in := "Title\n=====\n\npara one\nline two\n\n* a\n* b\n\ntext\n\n    code\n\n> q\n\n***\n\n<div>x</div>\n\n[^1]: n\n\n| a |\n|---|\n| b |\n"
	blocks := Split(in)
	var kinds []string
	for _, b := range blocks {
		kinds = append(kinds, b.Kind)
		if strings.HasPrefix(b.Raw, "\n") || strings.HasSuffix(b.Raw, "\n") {
			t.Errorf("raw has surrounding newline: %q", b.Raw)
		}
		if b.ID != BlockID(b.Kind, b.Raw) {
			t.Errorf("id mismatch for %+v", b)
		}
	}
	want := []string{"heading", "paragraph", "list", "paragraph", "code", "quote", "hr", "html", "unknown", "table"}
	if !reflect.DeepEqual(kinds, want) {
		t.Errorf("kinds = %v, want %v", kinds, want)
	}
	if blocks[0].Raw != "# Title" || blocks[2].Raw != "- a\n- b" || blocks[4].Raw != "```\ncode\n```" {
		t.Errorf("raws = %q", []string{blocks[0].Raw, blocks[2].Raw, blocks[3].Raw})
	}
	raws := make([]string, len(blocks))
	for i, b := range blocks {
		raws[i] = b.Raw
	}
	if JoinRaw(raws) != Normalize(in) {
		t.Errorf("JoinRaw(Split) != Normalize")
	}
	if got := Split(""); got == nil || len(got) != 0 {
		t.Errorf("Split(\"\") = %#v, want empty non-nil", got)
	}
}

func ids(bs []Block) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.ID
	}
	return out
}

func TestBlockIDStability(t *testing.T) {
	base := "# Title\n\nfirst paragraph\n\nsecond paragraph\n\n- a\n- b\n\n```\ncode\n```\n"
	a := Split(base)
	b := Split(base)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("same content must give the same blocks")
	}
	// Spelling differences that normalize away do not change ids.
	variant := "Title\r\n=====\r\n\r\nfirst paragraph\r\n\r\nsecond paragraph\r\n\r\n* a\r\n* b\r\n\r\n~~~\r\ncode\r\n~~~\r\n"
	if !reflect.DeepEqual(ids(a), ids(Split(variant))) {
		t.Errorf("equivalent spellings must keep ids:\n%v\n%v", ids(a), ids(Split(variant)))
	}

	// Editing one block changes only that block's id.
	edited := strings.Replace(base, "second paragraph", "second paragraph, edited", 1)
	e := Split(edited)
	if len(e) != len(a) {
		t.Fatalf("block count changed: %d vs %d", len(e), len(a))
	}
	for i := range a {
		same := a[i].ID == e[i].ID
		if i == 2 && same {
			t.Errorf("edited block kept its id")
		}
		if i != 2 && !same {
			t.Errorf("untouched block %d changed id", i)
		}
	}

	// Inserting a block in the middle keeps all other ids.
	inserted := Split(strings.Replace(base, "second paragraph", "new block\n\nsecond paragraph", 1))
	got := ids(inserted)
	want := append(append(append([]string{}, ids(a)[:2]...), inserted[2].ID), ids(a)[2:]...)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("insert changed other ids:\n got  %v\n want %v", got, want)
	}

	// Reordering blocks keeps the ids (and so can be merged as moves).
	order := []string{"# Title", "first paragraph", "second paragraph", "- a\n- b", "```\ncode\n```"}
	reordered := Split(strings.Join([]string{order[0], order[2], order[1], order[4], order[3]}, "\n\n"))
	idOf := map[string]string{}
	for _, blk := range a {
		idOf[blk.Raw] = blk.ID
	}
	for _, blk := range reordered {
		if idOf[blk.Raw] != blk.ID {
			t.Errorf("reordered block %q has id %s, want %s", blk.Raw, blk.ID, idOf[blk.Raw])
		}
	}
	if reflect.DeepEqual(ids(reordered), ids(a)) {
		t.Error("reordering must change the order of ids")
	}

	// Identical blocks share an id (ids are content addressed).
	dup := Split("same\n\nsame\n")
	if len(dup) != 2 || dup[0].ID != dup[1].ID {
		t.Errorf("duplicate blocks: %+v", dup)
	}

	// A block's id depends on its kind as well as its text.
	if Split("# x\n")[0].ID == Split("x\n")[0].ID {
		t.Error("heading and paragraph with similar text must differ")
	}
}
