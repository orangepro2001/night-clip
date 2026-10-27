package notes

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"nightclip/core/proto"
	"nightclip/core/store"
)

// Full-text search. Notes are sealed in the database, so a persistent full-text index (FTS5) would
// put their text on disk in clear. The index here lives in memory only: it is built from the notes
// the first time a search runs and brought up to date on every later search by looking at which notes
// have a different head revision than the one that was indexed, so it costs one pass over the note
// list plus the notes that changed.

const (
	maxQueryTerms  = 8
	maxTermRunes   = 64
	snippetRunes   = 120
	maxIndexedText = 512 << 10 // bytes of one note's text that are searchable
	defaultHits    = 30
	maxHits        = 100
)

type indexedDoc struct {
	headRev    string
	conflictOf string
	updatedAt  int64
	title      string // as listed
	titleLow   string
	plain      string
	plainLow   string
}

type searchIndex struct {
	mu   sync.Mutex
	docs map[string]*indexedDoc
}

// Highlight is a matched range inside a snippet or title, in Unicode code points.
type Highlight struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// SearchItem is one hit.
type SearchItem struct {
	NoteID     string      `json:"note_id"`
	Title      string      `json:"title"`
	UpdatedAt  int64       `json:"updated_at"`
	Snippet    string      `json:"snippet"`
	Highlights []Highlight `json:"highlights"`       // in Snippet
	TitleHits  []Highlight `json:"title_highlights"` // in Title
	ConflictOf string      `json:"conflict_of,omitempty"`
}

// SearchResult is one page of hits, best first.
type SearchResult struct {
	Items      []SearchItem `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

// searchTerms splits a query into lower-cased terms; all of them must occur in a note.
func searchTerms(query string) ([]string, error) {
	fields := strings.Fields(strings.ToLower(query))
	if len(fields) == 0 {
		return nil, proto.Errorf(proto.CodeInvalid, "query is empty")
	}
	if len(fields) > maxQueryTerms {
		fields = fields[:maxQueryTerms]
	}
	for i, f := range fields {
		if utf8.RuneCountInString(f) > maxTermRunes {
			fields[i] = cutRunes(f, maxTermRunes)
		}
	}
	return fields, nil
}

// refresh brings the index in line with the notes. The caller holds idx.mu.
func (s *Service) refreshIndex(tx *store.NotesTx) error {
	idx := &s.index
	if idx.docs == nil {
		idx.docs = map[string]*indexedDoc{}
	}
	seen := map[string]bool{}
	var cursor *store.NoteCursor
	for {
		rows, next, err := tx.ListNotes(false, 500, cursor)
		if err != nil {
			return err
		}
		for _, r := range rows {
			seen[r.NoteID] = true
			if d := idx.docs[r.NoteID]; d != nil && d.headRev == r.HeadRev {
				continue
			}
			rev, found, err := tx.GetRev(r.HeadRev)
			if err != nil {
				return err
			}
			if !found {
				continue
			}
			text := plainText(rev.Body)
			if len(text) > maxIndexedText {
				text = text[:maxIndexedText]
				for !utf8.ValidString(text) {
					text = text[:len(text)-1]
				}
			}
			idx.docs[r.NoteID] = &indexedDoc{headRev: r.HeadRev, conflictOf: r.ConflictOf, updatedAt: r.UpdatedAt, title: r.Title,
				titleLow: strings.ToLower(r.Title), plain: text, plainLow: strings.ToLower(text)}
		}
		if next == nil {
			break
		}
		cursor = next
	}
	for id := range idx.docs { // deleted or gone since the last search
		if !seen[id] {
			delete(idx.docs, id)
		}
	}
	return nil
}

type scored struct {
	id    string
	doc   *indexedDoc
	score int
}

// Search finds live notes that contain every term of the query (case-insensitive substrings, so it
// works for Chinese and other scripts without word boundaries). Title hits rank above text hits, then
// more occurrences, then newer notes.
func (s *Service) Search(query string, limit int, cursor string) (SearchResult, error) {
	terms, err := searchTerms(query)
	if err != nil {
		return SearchResult{}, err
	}
	if limit <= 0 || limit > maxHits {
		limit = defaultHits
	}
	offset := 0
	if cursor != "" {
		if offset, err = strconv.Atoi(cursor); err != nil || offset < 0 {
			return SearchResult{}, proto.Errorf(proto.CodeInvalid, "cursor is malformed")
		}
	}

	s.index.mu.Lock()
	defer s.index.mu.Unlock()
	if err := s.db.NotesView(s.refreshIndex); err != nil {
		return SearchResult{}, err
	}

	var hits []scored
	for id, d := range s.index.docs {
		score, ok := 0, true
		for _, t := range terms {
			inTitle := strings.Contains(d.titleLow, t)
			n := strings.Count(d.plainLow, t)
			if !inTitle && n == 0 {
				ok = false
				break
			}
			if inTitle {
				score += 1000
			}
			if n > 20 {
				n = 20
			}
			score += n
		}
		if ok {
			hits = append(hits, scored{id, d, score})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.doc.updatedAt != b.doc.updatedAt {
			return a.doc.updatedAt > b.doc.updatedAt
		}
		return a.id > b.id
	})

	res := SearchResult{Items: []SearchItem{}}
	if offset >= len(hits) {
		return res, nil
	}
	end := offset + limit
	if end < len(hits) {
		res.NextCursor = strconv.Itoa(end)
	} else {
		end = len(hits)
	}
	for _, h := range hits[offset:end] {
		snippet, marks := snippetFor(h.doc, terms)
		res.Items = append(res.Items, SearchItem{NoteID: h.id, Title: h.doc.title, UpdatedAt: h.doc.updatedAt,
			Snippet: snippet, Highlights: marks, TitleHits: marksIn(h.doc.title, h.doc.titleLow, terms), ConflictOf: h.doc.conflictOf})
	}
	return res, nil
}

// marksIn returns the ranges of text (in code points) where a term occurs. low must be
// strings.ToLower(text); when lower-casing changed the length the ranges cannot be mapped back and none are returned.
func marksIn(text, low string, terms []string) []Highlight {
	out := []Highlight{}
	if len(text) != len(low) {
		return out
	}
	for _, t := range terms {
		from := 0
		for {
			i := strings.Index(low[from:], t)
			if i < 0 {
				break
			}
			start := from + i
			out = append(out, Highlight{Start: utf8.RuneCountInString(text[:start]), End: utf8.RuneCountInString(text[:start+len(t)])})
			from = start + len(t)
			if len(out) >= 50 {
				return mergeMarks(out)
			}
		}
	}
	return mergeMarks(out)
}

func mergeMarks(in []Highlight) []Highlight {
	if len(in) < 2 {
		return in
	}
	sort.Slice(in, func(i, j int) bool { return in[i].Start < in[j].Start })
	out := []Highlight{in[0]}
	for _, h := range in[1:] {
		last := &out[len(out)-1]
		if h.Start <= last.End {
			if h.End > last.End {
				last.End = h.End
			}
			continue
		}
		out = append(out, h)
	}
	return out
}

// snippetFor cuts a window of the note's text around the first match and marks the terms in it.
func snippetFor(d *indexedDoc, terms []string) (string, []Highlight) {
	text := d.plain
	first := -1
	if len(d.plainLow) == len(text) {
		for _, t := range terms {
			if i := strings.Index(d.plainLow, t); i >= 0 && (first < 0 || i < first) {
				first = i
			}
		}
	}
	runes := []rune(text)
	startRune := 0
	if first > 0 {
		startRune = utf8.RuneCountInString(text[:first]) - snippetRunes/4
		if startRune < 0 {
			startRune = 0
		}
	}
	endRune := startRune + snippetRunes
	if endRune > len(runes) {
		endRune = len(runes)
	}
	window := string(runes[startRune:endRune])
	window = strings.Map(func(r rune) rune {
		if r == '\n' {
			return ' ' // one rune for one rune: the offsets stay valid
		}
		return r
	}, window)
	prefix, suffix := "", ""
	if startRune > 0 {
		prefix = "…"
	}
	if endRune < len(runes) {
		suffix = "…"
	}
	marks := marksIn(window, strings.ToLower(window), terms)
	if prefix != "" {
		for i := range marks {
			marks[i].Start++
			marks[i].End++
		}
	}
	return prefix + window + suffix, marks
}
