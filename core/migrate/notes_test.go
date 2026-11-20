package migrate

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeZip writes the files in the shape ClipBridge's "export notes" produced.
func writeZip(t *testing.T, files map[string]string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "clipbridge-notes-20261120.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadNotesFromAClipBridgeExport(t *testing.T) {
	src := writeZip(t, map[string]string{
		"Shopping.md":        "---\ntitle: \"Shopping: \\\"weekly\\\"\"\nid: 01HZX\ncreated: 2026-01-02T03:04:05Z\nupdated: 2026-01-02T03:04:05Z\n---\n\n- milk\n- eggs\n",
		"_trash/Old idea.md": "---\ntitle: \"Old idea\"\nid: 01HZY\n---\n\nnot needed\n",
		"No front matter.md": "# Heading\r\n\r\ntext\r\n",
		"picture.png":        "\x89PNG",
		"__MACOSX/._x.md":    "junk",
		".hidden.md":         "hidden",
	})
	notes, skipped, err := ReadNotes(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped %v", skipped)
	}
	if len(notes) != 3 {
		t.Fatalf("got %d notes: %+v", len(notes), notes)
	}
	byTitle := map[string]Note{}
	for _, n := range notes {
		byTitle[n.Title] = n
	}
	shop, ok := byTitle[`Shopping: "weekly"`]
	if !ok || shop.Body != "- milk\n- eggs\n" || shop.Trashed {
		t.Fatalf("shopping = %+v", shop)
	}
	old, ok := byTitle["Old idea"]
	if !ok || !old.Trashed || old.Body != "not needed\n" {
		t.Fatalf("trashed note = %+v", old)
	}
	plain, ok := byTitle["No front matter"]
	if !ok || plain.Body != "# Heading\n\ntext\n" {
		t.Fatalf("note without front matter = %+v", plain)
	}
}

func TestReadNotesSkipsWhatIsNotText(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "utf16.md"), []byte{0xff, 0xfe, 'a', 0, 'b', 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "big.md"), []byte(strings.Repeat("x", MaxNoteBodyBytes+9000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.markdown"), []byte("fine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "x.md"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	notes, skipped, err := ReadNotes(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0].Title != "ok" || notes[0].Body != "fine" {
		t.Fatalf("notes = %+v", notes)
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped = %+v", skipped)
	}
}

func TestReadNotesRejectsBadPaths(t *testing.T) {
	if _, _, err := ReadNotes("relative/path.zip"); err == nil {
		t.Fatal("relative path accepted")
	}
	p := filepath.Join(t.TempDir(), "x.pdf")
	if err := os.WriteFile(p, []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadNotes(p); err == nil {
		t.Fatal("a PDF was accepted")
	}
	bad := filepath.Join(t.TempDir(), "broken.zip")
	if err := os.WriteFile(bad, []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadNotes(bad); err == nil {
		t.Fatal("a broken ZIP was accepted")
	}
}

func TestLongTitlesAreCut(t *testing.T) {
	title := cleanTitle(strings.Repeat("长", MaxTitleRunes+50) + "\n\tend")
	if n := len([]rune(title)); n != MaxTitleRunes {
		t.Fatalf("title has %d runes", n)
	}
	if cleanTitle("  a \t b\n") != "a b" {
		t.Fatalf("whitespace not collapsed: %q", cleanTitle("  a \t b\n"))
	}
}
