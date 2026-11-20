// Package migrate reads what an older ClipBridge installation leaves behind so the migration
// wizard can bring it into a MoonPassword vault: the notes export (a ZIP of Markdown files)
// and, later, the old vault file.
package migrate

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"moonpassword/core/proto"
)

// Limits for one notes import.
const (
	MaxNoteFiles     = 10_000
	MaxNoteBodyBytes = 1 << 20   // one note's Markdown (the limit ClipBridge had)
	MaxNotesTotal    = 256 << 20 // all Markdown read from one import
	MaxTitleRunes    = 200
)

// Note is one note read from an export.
type Note struct {
	Title   string // from the front matter, else the file name
	Body    string // Markdown, with the front matter removed
	Trashed bool   // it was in the export's "_trash/" folder
	Source  string // file name inside the export, for messages
}

// Skip names a file that was not read and why.
type Skip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// ReadNotes reads every Markdown file behind src: a ZIP made by ClipBridge's "export notes", a
// folder of Markdown files or a single file. Notes come back sorted by file name.
//
// The format: one ".md" file per note, starting with a front matter block
// (`---`, `title: "…"`, `id:`, `created:`, `updated:`, `---`) followed by the Markdown body; notes
// that were in the trash sit under "_trash/".
func ReadNotes(src string) ([]Note, []Skip, error) {
	if src == "" || !filepath.IsAbs(src) {
		return nil, nil, proto.Errorf(proto.CodeInvalid, "path must be an absolute path")
	}
	files, closeFn, skipped, err := collectNoteFiles(src)
	defer closeFn()
	if err != nil {
		return nil, nil, err
	}
	if len(files) > MaxNoteFiles {
		return nil, nil, proto.Errorf(proto.CodeTooLarge, "more than %d files", MaxNoteFiles)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })

	var notes []Note
	var total int64
	for _, f := range files {
		if f.size > MaxNoteBodyBytes+8192 {
			skipped = append(skipped, Skip{Name: f.name, Reason: "larger than 1 MiB"})
			continue
		}
		if total += f.size; total > MaxNotesTotal {
			return nil, nil, proto.Errorf(proto.CodeTooLarge, "more than %d MiB of Markdown", MaxNotesTotal>>20)
		}
		raw, err := f.read()
		if err != nil {
			skipped = append(skipped, Skip{Name: f.name, Reason: "cannot be read"})
			continue
		}
		if bytes.IndexByte(raw, 0) >= 0 || !utf8.Valid(raw) { // binary, or UTF-16 text
			skipped = append(skipped, Skip{Name: f.name, Reason: "not a UTF-8 text file"})
			continue
		}
		if len(raw) > MaxNoteBodyBytes+8192 {
			skipped = append(skipped, Skip{Name: f.name, Reason: "larger than 1 MiB"})
			continue
		}
		title, body := splitFrontMatter(string(raw))
		if title = cleanTitle(title); title == "" {
			title = cleanTitle(strings.TrimSuffix(path.Base(f.name), path.Ext(f.name)))
		}
		if title == "" {
			title = "Untitled"
		}
		body = strings.ReplaceAll(body, "\r\n", "\n")
		if len(body) > MaxNoteBodyBytes {
			skipped = append(skipped, Skip{Name: f.name, Reason: "larger than 1 MiB"})
			continue
		}
		notes = append(notes, Note{Title: title, Body: body, Trashed: strings.HasPrefix(f.name, "_trash/"), Source: f.name})
	}
	if skipped == nil {
		skipped = []Skip{}
	}
	return notes, skipped, nil
}

// cleanTitle makes a one-line title of at most MaxTitleRunes characters.
func cleanTitle(s string) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)), " ")
	if utf8.RuneCountInString(s) > MaxTitleRunes {
		s = string([]rune(s)[:MaxTitleRunes])
	}
	return s
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
		if t := strings.TrimRight(line, " \t"); t == "---" || t == "..." {
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
	after := strings.TrimLeft(afterBlock(rest, end), "\n")
	for _, line := range strings.Split(rest[:end], "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) != "title" {
			continue
		}
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, `"`) {
			var t string
			if json.Unmarshal([]byte(v), &t) == nil {
				return t, after
			}
		}
		return strings.Trim(v, `'"`), after
	}
	return "", after
}

// afterBlock is what follows the closing line of the front matter that starts at rest[end:].
func afterBlock(rest string, end int) string {
	nl := strings.IndexByte(rest[end:], '\n')
	if nl < 0 {
		return ""
	}
	return rest[end+nl+1:]
}

type noteFile struct {
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

// collectNoteFiles lists the Markdown files behind a path: a ZIP, a single file or a folder.
func collectNoteFiles(src string) (files []noteFile, closer func(), skipped []Skip, err error) {
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
				skipped = append(skipped, Skip{Name: filepath.ToSlash(rel), Reason: "not a regular file"})
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			p2 := p
			files = append(files, noteFile{name: filepath.ToSlash(rel), size: info.Size(), read: func() ([]byte, error) {
				return readLimited(func() (io.ReadCloser, error) { return os.Open(p2) })
			}})
			if len(files) > MaxNoteFiles {
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
				skipped = append(skipped, Skip{Name: zf.Name, Reason: "not a regular file"})
				continue
			}
			zf := zf
			files = append(files, noteFile{name: zf.Name, size: int64(zf.UncompressedSize64), read: func() ([]byte, error) {
				return readLimited(func() (io.ReadCloser, error) { return zf.Open() })
			}})
			if len(files) > MaxNoteFiles {
				break
			}
		}
	default:
		if !isMarkdownName(src) {
			return nil, closer, nil, proto.Errorf(proto.CodeInvalid, "give a .zip, a Markdown file or a folder")
		}
		files = append(files, noteFile{name: filepath.Base(src), size: st.Size(), read: func() ([]byte, error) {
			return readLimited(func() (io.ReadCloser, error) { return os.Open(src) })
		}})
	}
	return files, closer, skipped, nil
}

// readLimited reads one file but never more than a note may hold (plus slack for the front matter
// and one byte to notice that it is too long).
func readLimited(open func() (io.ReadCloser, error)) ([]byte, error) {
	rc, err := open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(io.LimitReader(rc, MaxNoteBodyBytes+8192+1))
}
