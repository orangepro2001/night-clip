package notes

import "sort"

// Block-level three-way merge (notes/docs/sync-model.md §4.1). A document is a list of canonical
// Markdown blocks; two edited copies of a common base merge when their changes touch different
// blocks. Everything here is a pure function of its inputs, and merge3 is symmetric in its two sides,
// so two devices that merge the same pair of revisions get the same text.

// hunk replaces base[start:end] by repl (start == end is a pure insertion).
type hunk struct {
	start, end int
	repl       []string
}

// maxDiffCells bounds the work of the longest-common-subsequence table. A pair of documents that
// differ in more blocks than that is merged as one big change instead (which then conflicts unless
// only one side changed).
const maxDiffCells = 4_000_000

// diffHunks returns the changes that turn base into side, in base order, never overlapping.
func diffHunks(base, side []string) []hunk {
	pre := 0
	for pre < len(base) && pre < len(side) && base[pre] == side[pre] {
		pre++
	}
	suf := 0
	for suf < len(base)-pre && suf < len(side)-pre && base[len(base)-1-suf] == side[len(side)-1-suf] {
		suf++
	}
	b, s := base[pre:len(base)-suf], side[pre:len(side)-suf]
	if len(b) == 0 && len(s) == 0 {
		return nil
	}
	if len(b) == 0 || len(s) == 0 || (len(b)+1)*(len(s)+1) > maxDiffCells {
		return []hunk{{start: pre, end: pre + len(b), repl: append([]string(nil), s...)}}
	}

	// lcs[i][j] = length of the longest common subsequence of b[i:] and s[j:]
	w := len(s) + 1
	lcs := make([]int32, (len(b)+1)*w)
	for i := len(b) - 1; i >= 0; i-- {
		for j := len(s) - 1; j >= 0; j-- {
			if b[i] == s[j] {
				lcs[i*w+j] = lcs[(i+1)*w+j+1] + 1
			} else if lcs[(i+1)*w+j] >= lcs[i*w+j+1] {
				lcs[i*w+j] = lcs[(i+1)*w+j]
			} else {
				lcs[i*w+j] = lcs[i*w+j+1]
			}
		}
	}

	var out []hunk
	i, j := 0, 0
	for i < len(b) || j < len(s) {
		if i < len(b) && j < len(s) && b[i] == s[j] {
			i++
			j++
			continue
		}
		si, sj := i, j
		for i < len(b) || j < len(s) {
			if i < len(b) && j < len(s) && b[i] == s[j] {
				break
			}
			if j >= len(s) || i < len(b) && lcs[(i+1)*w+j] >= lcs[i*w+j+1] {
				i++
			} else {
				j++
			}
		}
		out = append(out, hunk{start: pre + si, end: pre + i, repl: append([]string(nil), s[sj:j]...)})
	}
	return out
}

func sameLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// overlap reports whether two changes touch the same blocks. Changes that merely sit next to each
// other (one ends where the other starts) do not overlap; two insertions at the same point do.
func overlap(a, b hunk) bool {
	if a.start == a.end && b.start == b.end {
		return a.start == b.start
	}
	return a.start < b.end && b.start < a.end
}

// merge3 merges two edits of base. ok is false when both sides changed the same blocks differently;
// the caller then keeps one side and sets the other aside as a conflict copy.
func merge3(base, left, right []string) (merged []string, ok bool) {
	lh, rh := diffHunks(base, left), diffHunks(base, right)
	if len(lh) == 0 {
		return append([]string(nil), right...), true
	}
	if len(rh) == 0 {
		return append([]string(nil), left...), true
	}

	// An identical change on both sides counts once.
	var all []hunk
	used := make([]bool, len(rh))
	for _, l := range lh {
		all = append(all, l)
		for i, r := range rh {
			if !used[i] && l.start == r.start && l.end == r.end && sameLines(l.repl, r.repl) {
				used[i] = true
			}
		}
	}
	for i, r := range rh {
		if !used[i] {
			all = append(all, r)
		}
	}
	// Different changes must not touch the same blocks.
	for i := range all {
		for j := i + 1; j < len(all); j++ {
			if overlap(all[i], all[j]) {
				return nil, false
			}
		}
	}
	// Insertions sort before the replacement that starts at the same block.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].start != all[j].start {
			return all[i].start < all[j].start
		}
		return all[i].end < all[j].end
	})

	pos := 0
	for _, h := range all {
		merged = append(merged, base[pos:h.start]...)
		merged = append(merged, h.repl...)
		pos = h.end
	}
	merged = append(merged, base[pos:]...)
	return merged, true
}

// merge3String is the three-way merge of one value (the title): a side that did not change yields
// to the side that did.
func merge3String(base, left, right string) (string, bool) {
	switch {
	case left == right:
		return left, true
	case left == base:
		return right, true
	case right == base:
		return left, true
	}
	return "", false
}
