package notes

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nightclip/core/proto"
)

func zipEntries(t *testing.T, path string) map[string]string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := rc.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		rc.Close()
		out[f.Name] = sb.String()
	}
	return out
}

func TestExportWritesOneMarkdownFilePerNote(t *testing.T) {
	d, clk := one(t)
	a := d.mustCreate("Plan: A/B?", "# Plan\n\n- one\n- two\n")
	clk.tick()
	d.mustCreate("Plan: A/B?", "same title\n") // file names must not collide
	clk.tick()
	d.mustCreate("CON", "reserved on Windows\n")
	clk.tick()
	d.mustCreate("", "no title at all\n")
	clk.tick()
	gone := d.mustCreate("In the trash", "bye\n")
	_, _ = d.svc.Delete(gone)

	dest := filepath.Join(t.TempDir(), "notes.zip")
	res, err := d.svc.Export(dest, false)
	if err != nil || res.Count != 4 || res.Bytes == 0 || res.Path != dest {
		t.Fatalf("Export = %+v, %v", res, err)
	}
	files := zipEntries(t, dest)
	if len(files) != 4 {
		t.Fatalf("files = %v", files)
	}
	var plan string
	for name, text := range files {
		if strings.ContainsAny(name, `\:*?"<>|`) || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
			t.Fatalf("unsafe file name %q", name)
		}
		if strings.HasPrefix(text, "---\ntitle: \"Plan: A/B?\"\nid: "+a) {
			plan = text
		}
	}
	if plan == "" || !strings.HasSuffix(plan, "---\n\n# Plan\n\n- one\n- two\n") || !strings.Contains(plan, "created: 20") {
		t.Fatalf("front matter or body missing in %q", plan)
	}
	for _, want := range []string{"Plan_ A_B_.md", "Plan_ A_B_ (2).md", "_CON.md", "no title at all.md"} {
		if _, ok := files[want]; !ok {
			t.Fatalf("no %q in %v", want, keys(files))
		}
	}

	again, err := d.svc.Export(dest, false)
	if err == nil || errCode(err) != string(proto.CodeInvalid) || again.Count != 0 {
		t.Fatalf("exporting over an existing file: %+v, %v", again, err)
	}
	if st, _ := os.Stat(dest); st == nil || st.Size() != res.Bytes {
		t.Fatal("the existing file was touched")
	}

	withTrash := filepath.Join(t.TempDir(), "all.zip")
	if r, err := d.svc.Export(withTrash, true); err != nil || r.Count != 5 {
		t.Fatalf("Export with trash = %+v, %v", r, err)
	}
	if _, ok := zipEntries(t, withTrash)["_trash/In the trash.md"]; !ok {
		t.Fatal("the trash is not under _trash/")
	}
	if _, err := d.svc.Export("relative.zip", false); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("relative path: %v", err)
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestExportThenImportOnAnotherDeviceKeepsTitlesAndText(t *testing.T) {
	a, b, clk := twoDevices(t)
	a.mustCreate("日本語のメモ", "# 見出し\n\n本文 😀\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n")
	clk.tick()
	a.mustCreate("", "untitled body\n")
	dest := filepath.Join(t.TempDir(), "out.zip")
	if _, err := a.svc.Export(dest, false); err != nil {
		t.Fatal(err)
	}

	res, err := b.svc.Import(dest)
	if err != nil || res.Imported != 2 || len(res.Skipped) != 0 || len(res.NoteIDs) != 2 {
		t.Fatalf("Import = %+v, %v", res, err)
	}
	got := b.snapshot()
	var titles []string
	for _, v := range got {
		titles = append(titles, v.Title)
	}
	for _, id := range res.NoteIDs {
		g, _ := b.svc.Get(id, "")
		if g.Title == "日本語のメモ" && g.Body != "# 見出し\n\n本文 😀\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n" {
			t.Fatalf("imported body = %q", g.Body)
		}
	}
	if !contains(titles, "日本語のメモ") || !contains(titles, "untitled body") { // an untitled note takes its file name, which comes from its text
		t.Fatalf("titles = %v", titles)
	}
	// importing again makes copies, never overwrites
	res2, err := b.svc.Import(dest)
	if err != nil || res2.Imported != 2 || len(b.snapshot()) != 4 {
		t.Fatalf("second import = %+v, %v (%d notes)", res2, err, len(b.snapshot()))
	}
	// the imported notes are ordinary notes: they sync
	a.syncWith(b)
	sameView(t, a, b)
}

func TestImportOfAnExportWithTrashPutsTheTrashedNotesBackInTheTrash(t *testing.T) {
	a, b, clk := twoDevices(t)
	a.mustCreate("alive", "stays\n")
	clk.tick()
	gone := a.mustCreate("gone", "was deleted\n")
	_, _ = a.svc.Delete(gone)
	dest := filepath.Join(t.TempDir(), "all.zip")
	if _, err := a.svc.Export(dest, true); err != nil {
		t.Fatal(err)
	}

	res, err := b.svc.Import(dest)
	if err != nil || res.Imported != 2 || len(res.NoteIDs) != 1 {
		t.Fatalf("Import = %+v, %v", res, err)
	}
	live, _ := b.svc.List(10, "", false)
	trash, _ := b.svc.List(10, "", true)
	if len(live.Items) != 1 || live.Items[0].Title != "alive" || len(trash.Items) != 1 || trash.Items[0].Title != "gone" {
		t.Fatalf("live = %+v, trash = %+v", live, trash)
	}
	if res.NoteIDs[0] != live.Items[0].NoteID {
		t.Fatal("the note ids offered to open must be live ones")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestImportReadsFoldersAndSingleFiles(t *testing.T) {
	d, _ := one(t)
	dir := t.TempDir()
	write := func(rel, text string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("top.md", "---\ntitle: From front matter\ntags: [a]\n---\n\nbody one\r\n")
	write("sub/deep.markdown", "# Heading\r\n\r\ntext")
	write("sub/notes.txt", "plain text file")
	write("image.png", "not markdown")
	write(".hidden/secret.md", "must not be imported")
	write("binary.md", "abc\x00def")
	write("huge.md", strings.Repeat("x", MaxBodyBytes+20000))
	if err := os.Symlink(filepath.Join(dir, "top.md"), filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}

	res, err := d.svc.Import(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 3 {
		t.Fatalf("imported %d, skipped %+v", res.Imported, res.Skipped)
	}
	reasons := map[string]string{}
	for _, s := range res.Skipped {
		reasons[s.Name] = s.Reason
	}
	if reasons["binary.md"] == "" || reasons["huge.md"] == "" || reasons["link.md"] == "" || len(reasons) != 3 {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
	byTitle := map[string]string{}
	for _, v := range d.snapshot() {
		byTitle[v.Title] = v.Body
	}
	if byTitle["From front matter"] != "body one\n" || byTitle["deep"] != "# Heading\n\ntext\n" || byTitle["notes"] != "plain text file\n" {
		t.Fatalf("notes = %v", byTitle)
	}

	single := filepath.Join(t.TempDir(), "one.md")
	_ = os.WriteFile(single, []byte("---\ntitle: \"Quoted \\\"title\\\"\"\n---\nx"), 0o600)
	if r, err := d.svc.Import(single); err != nil || r.Imported != 1 {
		t.Fatalf("single file = %+v, %v", r, err)
	}
	found := false
	for _, v := range d.snapshot() {
		found = found || v.Title == `Quoted "title"`
	}
	if !found {
		t.Fatal("a quoted front matter title was not read")
	}
}

func TestImportRefusesWhatItCannotRead(t *testing.T) {
	d, _ := one(t)
	if _, err := d.svc.Import("relative.zip"); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("relative path: %v", err)
	}
	if _, err := d.svc.Import(filepath.Join(t.TempDir(), "missing.zip")); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("missing file: %v", err)
	}
	bad := filepath.Join(t.TempDir(), "bad.zip")
	_ = os.WriteFile(bad, []byte("this is not a zip"), 0o600)
	if _, err := d.svc.Import(bad); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("bad zip: %v", err)
	}
	other := filepath.Join(t.TempDir(), "photo.jpg")
	_ = os.WriteFile(other, []byte("x"), 0o600)
	if _, err := d.svc.Import(other); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("unsupported file type: %v", err)
	}
	if page, _ := d.svc.List(10, "", false); len(page.Items) != 0 {
		t.Fatal("a refused import created notes")
	}
}

func TestImportOfAZipWithOddEntriesOnlyTakesMarkdown(t *testing.T) {
	d, _ := one(t)
	p := filepath.Join(t.TempDir(), "odd.zip")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	for name, text := range map[string]string{
		"../../evil.md":     "path tricks are only names here",
		"__MACOSX/._a.md":   "resource fork",
		"folder/ok.md":      "fine",
		"readme.pdf":        "no",
		"a/b/c/d/e/deep.md": "deep",
	} {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
		_, _ = w.Write([]byte(text))
	}
	_ = zw.Close()
	_ = f.Close()
	res, err := d.svc.Import(p)
	if err != nil || res.Imported != 3 {
		t.Fatalf("Import = %+v, %v", res, err)
	}
	// nothing was written anywhere: names are only used as titles
	for _, v := range d.snapshot() {
		if strings.Contains(v.Title, "/") || strings.Contains(v.Title, "..") {
			t.Fatalf("title %q carries a path", v.Title)
		}
	}
}
