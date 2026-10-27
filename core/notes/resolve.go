package notes

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"nightclip/core/notes/markdown"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// Head resolution (notes/docs/sync-model.md §4).
//
// The revisions of a note form a graph, and the only thing two devices ever exchange is revisions:
// the set of revisions only grows, so what a device holds after a sync does not depend on the order
// things arrived in. When a note has more than one head (two devices edited the same revision),
// reconcile turns them into ONE new merge revision whose content is a pure function of the heads. Two
// devices holding the same revisions therefore compute the same merge revision, with the same id, and
// converge without exchanging anything beyond the revisions themselves.

// stamp orders revisions for tie-breaks: newer wins, then the higher origin, then the higher id.
type stamp struct {
	createdAt int64
	origin    string
	revID     string
}

func (a stamp) after(b stamp) bool {
	if a.createdAt != b.createdAt {
		return a.createdAt > b.createdAt
	}
	if a.origin != b.origin {
		return a.origin > b.origin
	}
	return a.revID > b.revID
}

func maxStamp(a, b stamp) stamp {
	if a.after(b) {
		return a
	}
	return b
}

// content is what a merge works on: the user-visible part of a revision.
type content struct {
	title      string
	body       string
	deleted    bool
	conflictOf string
	stamp      stamp
}

func contentOf(r store.NoteRev) content {
	return content{title: r.Title, body: r.Body, deleted: r.Deleted, conflictOf: r.ConflictOf,
		stamp: stamp{createdAt: r.CreatedAt, origin: r.OriginDevice, revID: r.RevID}}
}

func blocksOf(body string) []string {
	bs := markdown.Split(body)
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = b.Raw
	}
	return out
}

// loser is an edit that could not be merged and is set aside as a conflict copy.
type loser struct {
	content
}

// folded is the result of merging all the heads of a note.
type folded struct {
	content
	losers           []loser
	deletedElsewhere bool // an edit survived because another head deleted the note
}

// mergeContents merges two heads over their common ancestor. base may be the empty content when the
// heads share no history.
func mergeContents(base, a, b content, haveBase bool) (content, []loser, bool) {
	winner, other := a, b
	if b.stamp.after(a.stamp) {
		winner, other = b, a
	}
	newest := maxStamp(a.stamp, b.stamp)

	switch {
	case a.deleted && b.deleted:
		w := winner
		w.stamp = newest
		return w, nil, false
	case a.deleted != b.deleted:
		live, gone := a, b
		if !b.deleted {
			live, gone = b, a
		}
		// "Deleting only beats an object nobody touched": a live side that still equals the common
		// ancestor was not edited, so the deletion stands. Otherwise the edit survives.
		if haveBase && live.title == base.title && live.body == base.body {
			gone.stamp = newest
			return gone, nil, false
		}
		live.stamp = newest
		return live, nil, true
	}

	conflictOf := winner.conflictOf
	if a.conflictOf == b.conflictOf {
		conflictOf = a.conflictOf
	}
	title, titleOK := merge3String(base.title, a.title, b.title)
	merged, bodyOK := mergeBodies(base.body, a.body, b.body)
	if titleOK && bodyOK {
		return content{title: title, body: merged, conflictOf: conflictOf, stamp: newest}, nil, false
	}
	// Not mergeable: the newer edit is the note, the older one is kept as a conflict copy.
	w := winner
	w.stamp = newest
	return w, []loser{{other}}, false
}

func mergeBodies(base, a, b string) (string, bool) {
	if a == b {
		return a, true
	}
	if a == base {
		return b, true
	}
	if b == base {
		return a, true
	}
	blocks, ok := merge3Blocks(blocksOf(base), blocksOf(a), blocksOf(b))
	if !ok {
		return "", false
	}
	// A block merged line by line may not be in canonical form (two list edits that are fine apart
	// can make a list loose together): the revision must be, so normalize. It is a pure function.
	return markdown.Normalize(markdown.JoinRaw(blocks)), true
}

// graph is the revision graph of one note.
type graph struct {
	parents map[string][]string
	meta    map[string]store.RevMeta
}

func loadGraph(tx *store.NotesTx, noteID string) (*graph, error) {
	metas, err := tx.Graph(noteID)
	if err != nil {
		return nil, err
	}
	g := &graph{parents: make(map[string][]string, len(metas)), meta: make(map[string]store.RevMeta, len(metas))}
	for _, m := range metas {
		g.parents[m.RevID] = m.Parents
		g.meta[m.RevID] = m
	}
	return g, nil
}

// ancestors returns rev and everything it descends from.
func (g *graph) ancestors(rev string) map[string]struct{} {
	seen := map[string]struct{}{}
	stack := []string{rev}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		stack = append(stack, g.parents[n]...)
	}
	return seen
}

// lowestCommon picks the nearest common ancestor of two ancestor sets: among the common revisions,
// those that are no other common revision's parent; if several remain, the newest by stamp.
func (g *graph) lowestCommon(a, b map[string]struct{}) (string, bool) {
	common := map[string]struct{}{}
	for r := range a {
		if _, ok := b[r]; ok {
			common[r] = struct{}{}
		}
	}
	if len(common) == 0 {
		return "", false
	}
	for r := range common {
		for _, p := range g.parents[r] {
			delete(common, p)
		}
	}
	best, found := "", false
	var bestStamp stamp
	for r := range common {
		m := g.meta[r]
		st := stamp{createdAt: m.CreatedAt, origin: m.OriginDevice, revID: r}
		if !found || st.after(bestStamp) {
			best, bestStamp, found = r, st, true
		}
	}
	return best, found
}

// foldHeads merges all heads (sorted by id) into one content. The result depends only on the heads
// and their ancestry.
func foldHeads(tx *store.NotesTx, g *graph, heads []string) (folded, error) {
	load := func(id string) (content, error) {
		r, found, err := tx.GetRev(id)
		if err != nil {
			return content{}, err
		}
		if !found {
			return content{}, proto.Errorf(proto.CodeInternal, "revision graph is inconsistent")
		}
		return contentOf(r), nil
	}
	cur, err := load(heads[0])
	if err != nil {
		return folded{}, err
	}
	curAnc := g.ancestors(heads[0])
	out := folded{}
	for _, h := range heads[1:] {
		next, err := load(h)
		if err != nil {
			return folded{}, err
		}
		nextAnc := g.ancestors(h)
		var base content
		haveBase := false
		if id, found := g.lowestCommon(curAnc, nextAnc); found {
			if base, err = load(id); err != nil {
				return folded{}, err
			}
			haveBase = true
		}
		var lost []loser
		var elsewhere bool
		cur, lost, elsewhere = mergeContents(base, cur, next, haveBase)
		out.losers = append(out.losers, lost...)
		out.deletedElsewhere = out.deletedElsewhere || elsewhere
		for r := range nextAnc {
			curAnc[r] = struct{}{}
		}
	}
	out.content = cur
	return out, nil
}

// conflictCopy builds the first revision of the conflict copy of an edit that lost a merge.
func conflictCopy(noteID string, l loser) store.NoteRev {
	short := l.stamp.origin
	switch {
	case short == MergeOrigin:
		short = "合并" // the set-aside edit was itself the product of an earlier merge
	case len(short) > 12:
		short = short[:12]
	}
	when := time.UnixMilli(l.stamp.createdAt).UTC().Format("2006-01-02 15:04")
	base := l.title
	if base == "" {
		base = "无标题"
	}
	suffix := fmt.Sprintf("（冲突 来自 %s %s）", short, when)
	title := cutTitle(base, MaxTitleBytes-len(suffix)) + suffix
	banner := fmt.Sprintf("> %s%s 在 %s UTC 的另一份修改与这篇笔记的其它修改无法自动合并，已作为副本保留。", ConflictBannerPrefix, short, when)
	body := banner
	if strings.TrimSpace(l.body) != "" {
		body += "\n\n" + l.body
	}
	return seal(store.NoteRev{
		NoteID:        deriveNoteID(noteID, l.title, l.body),
		Title:         title,
		Body:          markdown.Normalize(body),
		FormatVersion: markdown.FormatVersion,
		OriginDevice:  l.stamp.origin,
		CreatedAt:     l.stamp.createdAt,
		ConflictOf:    noteID,
	})
}

// ConflictBannerPrefix starts the quote that tops every conflict copy; resolving the conflict strips it.
const ConflictBannerPrefix = "冲突副本：来自设备 "

var titleSuffixCut = func(s string) string {
	// "…（冲突 来自 dev_x 2026-10-27 12:30）" -> "…"
	i := strings.LastIndex(s, "（冲突 来自 ")
	if i < 0 || !strings.HasSuffix(s, "）") {
		return s
	}
	return strings.TrimSpace(s[:i])
}

// stripConflict removes the banner and title suffix a conflict copy was given.
func stripConflict(title, body string) (string, string) {
	blocks := markdown.Split(body)
	if len(blocks) > 0 && blocks[0].Kind == "quote" && strings.HasPrefix(blocks[0].Raw, "> "+ConflictBannerPrefix) {
		raws := make([]string, 0, len(blocks)-1)
		for _, b := range blocks[1:] {
			raws = append(raws, b.Raw)
		}
		body = markdown.JoinRaw(raws)
	}
	t := titleSuffixCut(title)
	if t == "无标题" {
		t = ""
	}
	return t, body
}

// outcome reports what reconcile did to one note.
type outcome struct {
	noteID           string
	headRev          string
	changed          bool     // the visible head is a different revision than before
	merged           bool     // a merge revision was created
	conflictNotes    []string // conflict copies created by this call
	deletedElsewhere bool
}

// reconcile brings the notes row of a note up to date with its revisions, merging heads first when
// there is more than one. It is deterministic in the stored revisions.
func reconcile(tx *store.NotesTx, noteID string) (outcome, error) {
	out := outcome{noteID: noteID}
	prev, hadRow, err := tx.GetNote(noteID)
	if err != nil {
		return out, err
	}
	heads, err := tx.Heads(noteID)
	if err != nil {
		return out, err
	}
	if len(heads) == 0 {
		return out, proto.Errorf(proto.CodeNotFound, "no such note")
	}
	var g *graph
	if len(heads) > 1 {
		if g, err = loadGraph(tx, noteID); err != nil {
			return out, err
		}
		res, err := foldHeads(tx, g, heads)
		if err != nil {
			return out, err
		}
		newest := int64(0)
		for _, h := range heads {
			if c := g.meta[h].CreatedAt; c > newest {
				newest = c
			}
		}
		merge := seal(store.NoteRev{
			NoteID: noteID, Parents: append([]string(nil), heads...), Title: res.title, Body: res.body,
			FormatVersion: markdown.FormatVersion, OriginDevice: MergeOrigin, CreatedAt: newest,
			Deleted: res.deleted, ConflictOf: res.conflictOf,
		})
		if _, err := tx.InsertRev(merge); err != nil {
			return out, err
		}
		out.merged, out.deletedElsewhere = true, res.deletedElsewhere
		for _, l := range res.losers {
			cc := conflictCopy(noteID, l)
			inserted, err := tx.InsertRev(cc)
			if err != nil {
				return out, err
			}
			if _, err := reconcile(tx, cc.NoteID); err != nil {
				return out, err
			}
			if inserted {
				out.conflictNotes = append(out.conflictNotes, cc.NoteID)
			}
		}
		heads = []string{merge.RevID}
	}
	head, found, err := tx.GetRev(heads[0])
	if err != nil {
		return out, err
	}
	if !found {
		return out, proto.Errorf(proto.CodeInternal, "revision graph is inconsistent")
	}
	created := prev.CreatedAt
	if !hadRow {
		if g == nil {
			if g, err = loadGraph(tx, noteID); err != nil {
				return out, err
			}
		}
		created = head.CreatedAt
		for _, m := range g.meta {
			if m.CreatedAt < created {
				created = m.CreatedAt
			}
		}
	}
	title, preview := listTexts(head.Title, head.Body)
	row := store.NoteRow{NoteID: noteID, CreatedAt: created, UpdatedAt: head.CreatedAt, HeadRev: head.RevID,
		Deleted: head.Deleted, ConflictOf: head.ConflictOf, Title: title, Preview: preview}
	if err := tx.PutNote(row); err != nil {
		return out, err
	}
	if err := tx.SetBlobRefs(noteID, blobRefs(head.Body)); err != nil {
		return out, err
	}
	out.headRev = head.RevID
	out.changed = !hadRow || prev.HeadRev != head.RevID
	return out, nil
}

// sortedUnique returns the distinct strings in order.
func sortedUnique(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	k := 0
	for i, s := range out {
		if i == 0 || s != out[i-1] {
			out[k] = s
			k++
		}
	}
	return out[:k]
}
