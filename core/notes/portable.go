package notes

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"nightclip/core/notes/markdown"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// Import and export (notes/docs/development-plan.md §5): the way out is a folder of ordinary Markdown
// files, so the notes never depend on this app.
//
// An export is one ZIP file with one ".md" file per note. Each file starts with a small front matter
// block (title, id, created, updated) that other Markdown editors understand, followed by the note's
// canonical Markdown. Notes in the trash go into "_trash/" when asked for. An import reads a ZIP, a
// single Markdown file or a folder of them, and always creates NEW notes (the ids in a front matter are
// not reused, so importing the same export twice makes two copies, never overwrites anything).

const (
	maxImportFiles = 10_000
	maxImportTotal = 256 << 20 // bytes of Markdown read from one import
	maxFlushEvents = 50        // notes.changed events sent after an import (the shell re-lists anyway)
)

// ExportResult is returned by Export.
type ExportResult struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
	Bytes int64  `json:"bytes"`
}

// ImportSkip names a file that was not imported and why.
type ImportSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// ImportResult is returned by Import.
type ImportResult struct {
	Imported int          `json:"imported"`
	Skipped  []ImportSkip `json:"skipped"`
	NoteIDs  []string     `json:"note_ids"` // the first few, so the shell can open one
}

var windowsReserved = map[string]bool{"con": true, "prn": true, "aux": true, "nul": true, "com1": true, "com2": true, "com3": true,
	"com4": true, "com5": true, "com6": true, "com7": true, "com8": true, "com9": true, "lpt1": true, "lpt2": true, "lpt3": true,
	"lpt4": true, "lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true}

// fileStem makes a title usable as a file name on every platform.
func fileStem(title string) string {
	s := strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f || strings.ContainsRune(`/\:*?"<>|`, r):
			return '_'
		case unicode.IsControl(r):
			return '_'
		}
		return r
	}, title)
	s = strings.Trim(strings.TrimSpace(cutRunes(s, 80)), ". ")
	if s == "" {
		return "untitled"
	}
	if windowsReserved[strings.ToLower(s)] {
		s = "_" + s
	}
	return s
}

// frontMatter renders the block at the top of an exported file.
func frontMatter(id, title string, created, updated int64) string {
	t, _ := json.Marshal(title) // a JSON string is a valid YAML double-quoted scalar
	iso := func(ms int64) string { return time.UnixMilli(ms).UTC().Format(time.RFC3339) }
	return fmt.Sprintf("---\ntitle: %s\nid: %s\ncreated: %s\nupdated: %s\n---\n\n", t, id, iso(created), iso(updated))
}

// splitFrontMatter separates a leading "---" block from the text. Only the title is read from it.
func splitFrontMatter(text string) (title, body string) {
	text = strings.TrimPrefix(text, "\xef\xbb\xbf") // a UTF-8 byte order mark
	norm := strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(norm, "---\n") {
		return "", text
	}
	rest := norm[4:]
	end := -1
	for off := 0; off < len(rest) && off < 4096; {
		nl := strings.IndexByte(rest[off:], '\n')
		line := rest[off:]
		if nl >= 0 {
			line = rest[off : off+nl]
		}
		if strings.TrimRight(line, " \t") == "---" || strings.TrimRight(line, " \t") == "..." {
			end = off
			break
		}
		if nl < 0 {
			break
		}
		off += nl + 1
	}
	if end < 0 {
		return "", text
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) != "title" {
			continue
		}
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, `"`) {
			var t string
			if json.Unmarshal([]byte(v), &t) == nil {
				return t, strings.TrimLeft(afterBlock(rest, end), "\n")
			}
		}
		return strings.Trim(v, `'"`), strings.TrimLeft(afterBlock(rest, end), "\n")
	}
	return "", strings.TrimLeft(afterBlock(rest, end), "\n")
}

// afterBlock is what follows the closing line of the front matter that starts at rest[end:].
func afterBlock(rest string, end int) string {
	nl := strings.IndexByte(rest[end:], '\n')
	if nl < 0 {
		return ""
	}
	return rest[end+nl+1:]
}

// Export writes every live note (and, with includeTrash, the trash) as Markdown files in a ZIP at path.
// An existing file is never overwritten.
func (s *Service) Export(dest string, includeTrash bool) (ExportResult, error) {
	if dest == "" {
		return ExportResult{}, proto.Errorf(proto.CodeInvalid, "path is required")
	}
	if !filepath.IsAbs(dest) {
		return ExportResult{}, proto.Errorf(proto.CodeInvalid, "path must be absolute")
	}
	type entry struct {
		row   store.NoteRow
		title string // as stored; the row's title may be one derived from the text
		body  string
		subd  string
	}
	var entries []entry
	err := s.db.NotesView(func(tx *store.NotesTx) error {
		for _, deleted := range []bool{false, true} {
			if deleted && !includeTrash {
				continue
			}
			var cursor *store.NoteCursor
			for {
				rows, next, err := tx.ListNotes(deleted, 500, cursor)
				if err != nil {
					return err
				}
				for _, r := range rows {
					rev, found, err := tx.GetRev(r.HeadRev)
					if err != nil {
						return err
					}
					if !found {
						continue
					}
					sub := ""
					if deleted {
						sub = "_trash/"
					}
					entries = append(entries, entry{row: r, title: rev.Title, body: rev.Body, subd: sub})
				}
				if next == nil {
					break
				}
				cursor = next
			}
		}
		return nil
	})
	if err != nil {
		return ExportResult{}, err
	}

	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ExportResult{}, proto.Errorf(proto.CodeInvalid, "a file already exists at that path")
		}
		return ExportResult{}, proto.Errorf(proto.CodeInvalid, "cannot create the export file: %v", err)
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(dest)
		}
	}()
	zw := zip.NewWriter(f)
	used := map[string]bool{}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].row.UpdatedAt > entries[j].row.UpdatedAt })
	for _, e := range entries {
		stem := fileStem(e.row.Title)
		name := e.subd + stem + ".md"
		for n := 2; used[strings.ToLower(name)]; n++ {
			name = fmt.Sprintf("%s%s (%d).md", e.subd, stem, n)
		}
		used[strings.ToLower(name)] = true
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.UnixMilli(e.row.UpdatedAt).UTC()}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return ExportResult{}, proto.Errorf(proto.CodeInternal, "export failed: %v", err)
		}
		if _, err := io.WriteString(w, frontMatter(e.row.NoteID, e.title, e.row.CreatedAt, e.row.UpdatedAt)+e.body); err != nil {
			return ExportResult{}, proto.Errorf(proto.CodeNoSpace, "export failed: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		return ExportResult{}, proto.Errorf(proto.CodeNoSpace, "export failed: %v", err)
	}
	if err := f.Sync(); err != nil {
		return ExportResult{}, proto.Errorf(proto.CodeNoSpace, "export failed: %v", err)
	}
	st, _ := f.Stat()
	ok = true
	res := ExportResult{Path: dest, Count: len(entries)}
	if st != nil {
		res.Bytes = st.Size()
	}
	return res, nil
}

type importFile struct {
	name string
	read func() ([]byte, error)
	size int64
}

func isMarkdownName(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".txt":
		return true
	}
	return false
}

// collectImportFiles lists the Markdown files behind a path: a ZIP, a single file or a folder.
func collectImportFiles(src string) (files []importFile, closer func(), skipped []ImportSkip, err error) {
	closer = func() {}
	st, statErr := os.Stat(src)
	if statErr != nil {
		return nil, closer, nil, proto.Errorf(proto.CodeInvalid, "cannot read that path: %v", statErr)
	}
	switch {
	case st.IsDir():
		walkErr := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // unreadable entries are skipped, not fatal
			}
			rel, _ := filepath.Rel(src, p)
			if d.IsDir() {
				if strings.HasPrefix(d.Name(), ".") && p != src {
					return fs.SkipDir // .git, .obsidian, ...
				}
				return nil
			}
			if !isMarkdownName(d.Name()) {
				return nil
			}
			if !d.Type().IsRegular() { // symlinks and devices are not followed
				skipped = append(skipped, ImportSkip{Name: filepath.ToSlash(rel), Reason: "not a regular file"})
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			p2 := p
			files = append(files, importFile{name: filepath.ToSlash(rel), size: info.Size(), read: func() ([]byte, error) {
				return readLimited(func() (io.ReadCloser, error) { return os.Open(p2) })
			}})
			if len(files) > maxImportFiles {
				return io.EOF
			}
			return nil
		})
		if walkErr != nil && !errors.Is(walkErr, io.EOF) {
			return nil, closer, nil, proto.Errorf(proto.CodeInvalid, "cannot read that folder: %v", walkErr)
		}
	case strings.EqualFold(filepath.Ext(src), ".zip"):
		zr, zerr := zip.OpenReader(src)
		if zerr != nil {
			return nil, closer, nil, proto.Errorf(proto.CodeInvalid, "that is not a readable ZIP file")
		}
		closer = func() { _ = zr.Close() }
		for _, zf := range zr.File {
			if zf.FileInfo().IsDir() || !isMarkdownName(zf.Name) || strings.HasPrefix(path.Base(zf.Name), ".") || strings.HasPrefix(zf.Name, "__MACOSX/") {
				continue
			}
			if zf.Mode()&fs.ModeSymlink != 0 {
				skipped = append(skipped, ImportSkip{Name: zf.Name, Reason: "not a regular file"})
				continue
			}
			zf := zf
			files = append(files, importFile{name: zf.Name, size: int64(zf.UncompressedSize64), read: func() ([]byte, error) {
				return readLimited(func() (io.ReadCloser, error) { return zf.Open() })
			}})
			if len(files) > maxImportFiles {
				break
			}
		}
	default:
		if !isMarkdownName(src) {
			return nil, closer, nil, proto.Errorf(proto.CodeInvalid, "give a .zip, a Markdown file or a folder")
		}
		files = append(files, importFile{name: filepath.Base(src), size: st.Size(), read: func() ([]byte, error) {
			return readLimited(func() (io.ReadCloser, error) { return os.Open(src) })
		}})
	}
	return files, closer, skipped, nil
}

// readLimited reads one file but never more than a note may hold (plus slack for the front matter).
func readLimited(open func() (io.ReadCloser, error)) ([]byte, error) {
	rc, err := open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, MaxBodyBytes+8192+1))
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Import creates a note for every Markdown file behind src.
func (s *Service) Import(src string) (ImportResult, error) {
	if src == "" || !filepath.IsAbs(src) {
		return ImportResult{}, proto.Errorf(proto.CodeInvalid, "path must be an absolute path")
	}
	files, closeFn, skipped, err := collectImportFiles(src)
	defer closeFn()
	if err != nil {
		return ImportResult{}, err
	}
	if len(files) > maxImportFiles {
		return ImportResult{}, proto.Errorf(proto.CodeTooLarge, "more than %d files", maxImportFiles)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	type prepared struct {
		title, body string
		trashed     bool // it was in the trash of the export it comes from: it goes back there
	}
	var notes []prepared
	var total int64
	res := ImportResult{Skipped: []ImportSkip{}, NoteIDs: []string{}}
	res.Skipped = append(res.Skipped, skipped...)
	for _, f := range files {
		if f.size > MaxBodyBytes+8192 {
			res.Skipped = append(res.Skipped, ImportSkip{Name: f.name, Reason: "larger than the 1 MiB limit of a note"})
			continue
		}
		if total += f.size; total > maxImportTotal {
			return ImportResult{}, proto.Errorf(proto.CodeTooLarge, "more than %d MiB of Markdown", maxImportTotal>>20)
		}
		raw, err := f.read()
		if err != nil {
			res.Skipped = append(res.Skipped, ImportSkip{Name: f.name, Reason: "cannot be read"})
			continue
		}
		if bytes.IndexByte(raw, 0) >= 0 { // binary, or UTF-16 text (not supported)
			res.Skipped = append(res.Skipped, ImportSkip{Name: f.name, Reason: "not a text file"})
			continue
		}
		title, body := splitFrontMatter(string(raw))
		if title == "" {
			title = strings.TrimSuffix(path.Base(f.name), path.Ext(f.name))
		}
		t, err := checkTitle(title)
		if err != nil {
			t = cutTitle(normTitle(title), MaxTitleBytes)
		}
		b, err := normBody(body)
		if err != nil {
			res.Skipped = append(res.Skipped, ImportSkip{Name: f.name, Reason: "larger than the 1 MiB limit of a note"})
			continue
		}
		notes = append(notes, prepared{title: t, body: b, trashed: strings.HasPrefix(f.name, "_trash/")})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	var ch changes
	err = s.db.NotesTx(func(tx *store.NotesTx) error {
		base := s.nowMs()
		for i, n := range notes {
			rev := seal(store.NoteRev{NoteID: newNoteID(), Title: n.title, Body: n.body, FormatVersion: markdown.FormatVersion,
				OriginDevice: s.self, CreatedAt: base + int64(i)})
			if _, err := tx.InsertRev(rev); err != nil {
				return err
			}
			if n.trashed {
				if _, err := tx.InsertRev(s.child(rev, rev.Title, rev.Body, true)); err != nil {
					return err
				}
			}
			o, err := reconcile(tx, rev.NoteID)
			if err != nil {
				return err
			}
			if len(ch.changed) < maxFlushEvents {
				ch.add(o)
			}
			if !n.trashed && len(res.NoteIDs) < 5 {
				res.NoteIDs = append(res.NoteIDs, rev.NoteID)
			}
		}
		return nil
	})
	if err != nil {
		return ImportResult{}, err
	}
	res.Imported = len(notes)
	s.flush(ch)
	if res.Imported > 0 {
		s.Kick()
	}
	return res, nil
}
