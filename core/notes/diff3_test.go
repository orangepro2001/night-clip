package notes

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, " ")
}

func TestDiffHunksRebuildTheSide(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for n := 0; n < 3000; n++ {
		base := randBlocks(rng, rng.Intn(9))
		side := mutate(rng, base)
		var rebuilt []string
		pos := 0
		prevEnd := -1
		for _, h := range diffHunks(base, side) {
			if h.start < prevEnd || h.end < h.start {
				t.Fatalf("hunks overlap or are inverted: base=%v side=%v", base, side)
			}
			rebuilt = append(rebuilt, base[pos:h.start]...)
			rebuilt = append(rebuilt, h.repl...)
			pos, prevEnd = h.end, h.end
		}
		rebuilt = append(rebuilt, base[pos:]...)
		if !sameLines(rebuilt, side) {
			t.Fatalf("hunks do not rebuild the side: base=%v side=%v got=%v", base, side, rebuilt)
		}
	}
}

func TestMerge3Scenarios(t *testing.T) {
	cases := []struct {
		name              string
		base, left, right string
		want              string
		ok                bool
	}{
		{"title edit and an append (the example of the design doc)", "T a b", "T2 a b", "T a b c", "T2 a b c", true},
		{"different blocks", "a b c d", "a B c d", "a b c D", "a B c D", true},
		{"adjacent blocks", "a b c", "a B c", "a b C", "a B C", true},
		{"same block edited differently", "a b c", "a B1 c", "a B2 c", "", false},
		{"same edit on both sides counts once", "a b c", "a B c", "a B c", "a B c", true},
		{"one side unchanged", "a b c", "a b c", "a b c d", "a b c d", true},
		{"both unchanged", "a b", "a b", "a b", "a b", true},
		{"delete on one side, edit elsewhere on the other", "a b c", "a c", "a b C", "a C", true},
		{"delete versus edit of the same block", "a b c", "a c", "a B c", "", false},
		{"both insert at the same place", "a c", "a b c", "a x c", "", false},
		{"identical insertions", "a c", "a b c", "a b c", "a b c", true},
		{"inserts at different places", "a c e", "a b c e", "a c d e", "a b c d e", true},
		{"insert before and after an edited block", "a b c", "a x b c", "a B c y", "a x B c y", true},
		{"insert next to a block the other side replaced", "a b c", "a x b c", "a B c", "a x B c", true},
		{"both start from nothing with different text", "", "a", "b", "", false},
		{"one side starts from nothing", "", "a", "", "a", true},
		{"everything deleted on one side, untouched on the other", "a b", "", "a b", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := merge3(lines(c.base), lines(c.left), lines(c.right))
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v (got %v)", ok, c.ok, got)
			}
			if ok && !sameLines(got, lines(c.want)) {
				t.Fatalf("merged = %v, want %v", got, lines(c.want))
			}
			// swapping the sides never changes the outcome
			got2, ok2 := merge3(lines(c.base), lines(c.right), lines(c.left))
			if ok2 != ok || ok && !sameLines(got, got2) {
				t.Fatalf("not symmetric: %v %v vs %v %v", got, ok, got2, ok2)
			}
		})
	}
}

func randBlocks(rng *rand.Rand, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("b%d", rng.Intn(6)) // repeats on purpose: equal blocks must not confuse the alignment
	}
	return out
}

func mutate(rng *rand.Rand, in []string) []string {
	out := append([]string(nil), in...)
	for k := rng.Intn(4); k > 0; k-- {
		switch rng.Intn(3) {
		case 0:
			i := rng.Intn(len(out) + 1)
			out = append(out[:i], append([]string{fmt.Sprintf("n%d", rng.Intn(100))}, out[i:]...)...)
		case 1:
			if len(out) > 0 {
				i := rng.Intn(len(out))
				out = append(out[:i], out[i+1:]...)
			}
		default:
			if len(out) > 0 {
				out[rng.Intn(len(out))] = fmt.Sprintf("e%d", rng.Intn(100))
			}
		}
	}
	return out
}

func TestMerge3IsSymmetricAndKeepsBothSides(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	merged := 0
	for n := 0; n < 5000; n++ {
		base := randBlocks(rng, rng.Intn(8))
		left, right := mutate(rng, base), mutate(rng, base)
		a, okA := merge3(base, left, right)
		b, okB := merge3(base, right, left)
		if okA != okB || okA && !reflect.DeepEqual(a, b) {
			t.Fatalf("not symmetric: base=%v left=%v right=%v -> %v/%v vs %v/%v", base, left, right, a, okA, b, okB)
		}
		if !okA {
			continue
		}
		merged++
		// merging a side with the base gives that side back
		if got, ok := merge3(base, left, base); !ok || !sameLines(got, left) {
			t.Fatalf("merge with the unchanged base lost an edit: %v -> %v", left, got)
		}
		// merging an edit with itself is the edit
		if got, ok := merge3(base, left, left); !ok || !sameLines(got, left) {
			t.Fatalf("merge of identical edits changed them: %v -> %v", left, got)
		}
	}
	if merged < 500 {
		t.Fatalf("only %d of 5000 random pairs merged: the generator or the merge is off", merged)
	}
}

func TestMerge3HugeDocumentsDoNotBlowUp(t *testing.T) {
	base := make([]string, 6000)
	for i := range base {
		base[i] = fmt.Sprintf("block %d", i)
	}
	left := append([]string(nil), base...)
	right := append([]string(nil), base...)
	for i := 0; i < len(left); i += 2 { // every other block changed: far more cells than the diff budget
		left[i] = "L" + left[i]
	}
	right[len(right)-1] = "R"
	if _, ok := merge3(base, left, right); ok {
		// either answer is fine as long as it is quick and symmetric; the budget path treats the
		// middle as one change, which conflicts with the other side's edit inside it
		t.Log("merged")
	}
}

func TestMerge3BlocksCombinesEditsToDifferentLinesOfOneBlock(t *testing.T) {
	list := "- milk\n- eggs\n- bread\n- jam"
	cases := []struct {
		name              string
		base, left, right []string
		want              []string
		ok                bool
	}{
		{"different items of one list", []string{"# T", list}, []string{"# T", "- MILK\n- eggs\n- bread\n- jam"}, []string{"# T", "- milk\n- eggs\n- bread\n- JAM"},
			[]string{"# T", "- MILK\n- eggs\n- bread\n- JAM"}, true},
		{"an item added on each side", []string{list}, []string{"- milk\n- eggs\n- tea\n- bread\n- jam"}, []string{list + "\n- honey"},
			[]string{"- milk\n- eggs\n- tea\n- bread\n- jam\n- honey"}, true},
		{"the same item edited twice", []string{list}, []string{"- milk\n- EGGS\n- bread\n- jam"}, []string{"- milk\n- eggs!\n- bread\n- jam"}, nil, false},
		{"one item deleted, a neighbour edited", []string{list}, []string{"- milk\n- bread\n- jam"}, []string{"- milk\n- eggs\n- bread\n- JAM"},
			[]string{"- milk\n- bread\n- JAM"}, true},
		{"one item deleted, the same item edited", []string{list}, []string{"- milk\n- bread\n- jam"}, []string{"- milk\n- EGGS\n- bread\n- jam"}, nil, false},
		{"two lines of a paragraph", []string{"first line\nsecond line\nthird line"}, []string{"FIRST line\nsecond line\nthird line"},
			[]string{"first line\nsecond line\nTHIRD line"}, []string{"FIRST line\nsecond line\nTHIRD line"}, true},
		{"one line paragraphs still conflict", []string{"one line"}, []string{"one LINE"}, []string{"ONE line"}, nil, false},
		{"a block split in two by one side cannot be refined", []string{list}, []string{"- milk\n- eggs", "- bread\n- jam"}, []string{"- MILK\n- eggs\n- bread\n- jam"}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := merge3Blocks(c.base, c.left, c.right)
			got2, ok2 := merge3Blocks(c.base, c.right, c.left)
			if ok != ok2 || ok && !sameLines(got, got2) {
				t.Fatalf("not symmetric: %v/%v vs %v/%v", got, ok, got2, ok2)
			}
			if ok != c.ok || ok && !sameLines(got, c.want) {
				t.Fatalf("merge = %q ok=%v, want %q ok=%v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestMerge3BlocksIsSymmetricOnRandomMultiLineBlocks(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	block := func() string {
		var ls []string
		for i, n := 0, 1+rng.Intn(4); i < n; i++ {
			ls = append(ls, fmt.Sprintf("l%d", rng.Intn(5)))
		}
		return strings.Join(ls, "\n")
	}
	refined := 0
	for n := 0; n < 4000; n++ {
		var base []string
		for i, k := 0, 1+rng.Intn(3); i < k; i++ {
			base = append(base, block())
		}
		edit := func() []string {
			out := append([]string(nil), base...)
			i := rng.Intn(len(out))
			ls := strings.Split(out[i], "\n")
			switch rng.Intn(3) {
			case 0:
				ls = append(ls, fmt.Sprintf("n%d", rng.Intn(9)))
			case 1:
				ls[rng.Intn(len(ls))] = fmt.Sprintf("e%d", rng.Intn(9))
			default:
				if len(ls) > 1 {
					j := rng.Intn(len(ls))
					ls = append(ls[:j], ls[j+1:]...)
				}
			}
			out[i] = strings.Join(ls, "\n")
			return out
		}
		l, r := edit(), edit()
		a, okA := merge3Blocks(base, l, r)
		b, okB := merge3Blocks(base, r, l)
		if okA != okB || okA && !sameLines(a, b) {
			t.Fatalf("not symmetric: base=%q left=%q right=%q -> %q/%v vs %q/%v", base, l, r, a, okA, b, okB)
		}
		if _, plain := merge3(base, l, r); !plain && okA {
			refined++
		}
	}
	if refined < 100 {
		t.Fatalf("only %d pairs needed (and got) the line-level merge: the generator is off", refined)
	}
}

func TestMerge3String(t *testing.T) {
	for _, c := range []struct {
		base, l, r, want string
		ok               bool
	}{
		{"a", "a", "a", "a", true},
		{"a", "b", "a", "b", true},
		{"a", "a", "c", "c", true},
		{"a", "b", "b", "b", true},
		{"a", "b", "c", "", false},
	} {
		got, ok := merge3String(c.base, c.l, c.r)
		if got != c.want || ok != c.ok {
			t.Errorf("merge3String(%q,%q,%q) = %q,%v", c.base, c.l, c.r, got, ok)
		}
	}
}
