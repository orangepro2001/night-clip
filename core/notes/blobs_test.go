package notes

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nightclip/core/proto"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func idOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestAttachmentAddDescribesTheFileAndTheMarkdownToUseIt(t *testing.T) {
	d, _ := one(t)
	png := pngBytes(t)
	info, err := d.svc.AttachmentAdd(writeFile(t, "photo.png", png))
	if err != nil {
		t.Fatal(err)
	}
	if info.BlobID != idOf(png) || info.Mime != "image/png" || info.Size != int64(len(png)) || !info.Present || info.Name != "photo.png" ||
		info.Markdown != "![photo.png](blob:"+info.BlobID+")" {
		t.Fatalf("image = %+v", info)
	}
	pdf := []byte("%PDF-1.4 not really")
	doc, err := d.svc.AttachmentAdd(writeFile(t, "Q3 [final] report.pdf", pdf))
	if err != nil || doc.Mime != "application/pdf" || doc.Markdown != `[Q3 \[final\] report.pdf](blob:`+idOf(pdf)+`)` {
		t.Fatalf("document = %+v, %v", doc, err)
	}
	svg, _ := d.svc.AttachmentAdd(writeFile(t, "pic.svg", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")))
	if strings.HasPrefix(svg.Markdown, "!") {
		t.Fatalf("an SVG must be a link, not an inline image: %s", svg.Markdown)
	}
	same, err := d.svc.AttachmentAdd(writeFile(t, "copy of photo.png", png))
	if err != nil || same.BlobID != info.BlobID || same.Name != "photo.png" {
		t.Fatalf("the same bytes under another name: %+v, %v", same, err)
	}

	got, _ := d.svc.AttachmentGet(info.BlobID)
	if !got.Present || got.Mime != "image/png" || got.Markdown != "" {
		t.Fatalf("AttachmentGet = %+v", got)
	}
	absent, err := d.svc.AttachmentGet(strings.Repeat("0", 64))
	if err != nil || absent.Present || absent.Name != "" {
		t.Fatalf("absent attachment = %+v, %v", absent, err)
	}
	if _, err := d.svc.AttachmentGet("xyz"); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("malformed id: %v", err)
	}
}

func TestAttachmentAddRefusesWhatItShould(t *testing.T) {
	d, _ := one(t)
	if _, err := d.svc.AttachmentAdd("relative.png"); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("relative path: %v", err)
	}
	if _, err := d.svc.AttachmentAdd(t.TempDir()); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("a folder: %v", err)
	}
	if _, err := d.svc.AttachmentAdd(filepath.Join(t.TempDir(), "missing")); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("a missing file: %v", err)
	}
	big := filepath.Join(t.TempDir(), "big.bin")
	f, _ := os.Create(big)
	_ = f.Truncate(MaxBlobBytes + 1) // sparse: costs nothing
	_ = f.Close()
	if _, err := d.svc.AttachmentAdd(big); errCode(err) != string(proto.CodeTooLarge) {
		t.Fatalf("a file over the limit: %v", err)
	}
}

func TestAttachmentExportWritesTheBytesBack(t *testing.T) {
	d, _ := one(t)
	png := pngBytes(t)
	info, _ := d.svc.AttachmentAdd(writeFile(t, "a.png", png))
	dest := filepath.Join(t.TempDir(), "cache", "x.png")
	if _, err := d.svc.AttachmentExport(info.BlobID, dest); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("exporting into a folder that does not exist must fail with INVALID, not create folders: %v", err)
	}
	dest = filepath.Join(t.TempDir(), "x.png")
	out, err := d.svc.AttachmentExport(info.BlobID, dest)
	if err != nil || out.Name != "a.png" || out.Mime != "image/png" {
		t.Fatalf("AttachmentExport = %+v, %v", out, err)
	}
	if got, _ := os.ReadFile(dest); !bytes.Equal(got, png) {
		t.Fatal("exported bytes differ")
	}
	if _, err := d.svc.AttachmentExport(info.BlobID, dest); err != nil { // replaces an earlier export
		t.Fatalf("second export: %v", err)
	}
	if _, err := d.svc.AttachmentExport(idOf([]byte("nope")), filepath.Join(t.TempDir(), "n")); errCode(err) != string(proto.CodeNotFound) {
		t.Fatalf("absent attachment: %v", err)
	}
	if _, err := d.svc.AttachmentExport(info.BlobID, t.TempDir()); errCode(err) != string(proto.CodeInvalid) {
		t.Fatalf("exporting over a folder: %v", err)
	}
}

func TestRenderShowsAnAttachmentAsAnImageWithABlobAddress(t *testing.T) {
	d, _ := one(t)
	info, _ := d.svc.AttachmentAdd(writeFile(t, "a.png", pngBytes(t)))
	id := d.mustCreate("pic", "look:\n\n"+info.Markdown+"\n")
	r, err := d.svc.Render(id, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range r.Blocks {
		for _, in := range b.Inline {
			if in.T == "image" && in.Href == "blob:"+info.BlobID && in.Alt == "a.png" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no image inline with the blob address in %+v", r.Blocks)
	}
}

func TestAttachmentsFollowTheNotesThatUseThem(t *testing.T) {
	a, b, clk := twoDevices(t)
	png := pngBytes(t)
	info, _ := a.svc.AttachmentAdd(writeFile(t, "a.png", png))
	id := a.mustCreate("with picture", info.Markdown+"\n")
	clk.tick()

	b.syncWith(a) // b dials and fetches
	got, err := b.svc.AttachmentGet(info.BlobID)
	if err != nil || !got.Present || got.Name != "a.png" || got.Size != int64(len(png)) || got.Mime != "image/png" {
		t.Fatalf("attachment on b = %+v, %v", got, err)
	}
	if evs := b.eventsOf("notes.attachment"); len(evs) != 1 || evs[0]["blob_id"] != info.BlobID {
		t.Fatalf("attachment events = %v", evs)
	}
	dest := filepath.Join(t.TempDir(), "out.png")
	if _, err := b.svc.AttachmentExport(info.BlobID, dest); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dest); !bytes.Equal(data, png) {
		t.Fatal("the bytes changed on the way")
	}

	// the other direction: b makes a file and a note, b dials and sends it
	pdf := []byte("%PDF-1.4 hello")
	doc, _ := b.svc.AttachmentAdd(writeFile(t, "doc.pdf", pdf))
	g, _ := b.svc.Get(id, "")
	clk.tick()
	_, _ = b.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Body: strp(g.Body + "\n" + doc.Markdown + "\n")})
	b.syncWith(a)
	if got, _ := a.svc.AttachmentGet(doc.BlobID); !got.Present || got.Mime != "application/pdf" {
		t.Fatalf("attachment on a = %+v", got)
	}
	sameView(t, a, b)
}

func TestAttachmentOfATrashedNoteStillTravelsSoRestoringWorks(t *testing.T) {
	a, b, clk := twoDevices(t)
	info, _ := a.svc.AttachmentAdd(writeFile(t, "a.png", pngBytes(t)))
	id := a.mustCreate("x", info.Markdown+"\n")
	clk.tick()
	_, _ = a.svc.Delete(id)
	b.syncWith(a)
	if got, _ := b.svc.AttachmentGet(info.BlobID); !got.Present {
		t.Fatal("the picture of a trashed note did not arrive: restoring there would show a hole")
	}
}

func TestAPeerCannotStoreUnwantedOrForgedAttachments(t *testing.T) {
	d, _ := one(t)
	data := []byte("some bytes")
	id := idOf(data)
	if err := d.svc.storeReceivedBlob(id, blobData{Name: "x", Mime: "text/plain", Data: data}); err == nil {
		t.Fatal("an attachment no note refers to was stored")
	}
	d.mustCreate("uses it", "[x](blob:"+id+")\n")
	if err := d.svc.storeReceivedBlob(id, blobData{Name: "x", Mime: "text/plain", Data: []byte("other bytes")}); err == nil {
		t.Fatal("bytes that do not hash to the id were stored")
	}
	if err := d.svc.storeReceivedBlob(id, blobData{Name: "evil\n\x00name", Mime: "not a mime", Data: data}); err != nil {
		t.Fatalf("the genuine bytes were refused: %v", err)
	}
	got, _ := d.svc.AttachmentGet(id)
	if !got.Present || strings.ContainsAny(got.Name, "\n\x00") || got.Mime != "application/octet-stream" {
		t.Fatalf("stored attachment = %+v: names and types from a peer must be cleaned", got)
	}
	if err := d.svc.storeReceivedBlob(id, blobData{Data: make([]byte, MaxBlobBytes+1)}); err == nil {
		t.Fatal("an oversized attachment was stored")
	}
}

func TestCleanupRemovesOnlyOldFilesNothingRefersToAnymore(t *testing.T) {
	d, clk := one(t)
	keep, _ := d.svc.AttachmentAdd(writeFile(t, "kept.png", pngBytes(t)))
	old, _ := d.svc.AttachmentAdd(writeFile(t, "old.bin", []byte("old unused")))
	inHistory, _ := d.svc.AttachmentAdd(writeFile(t, "hist.bin", []byte("only an old revision uses this")))
	id := d.mustCreate("n", keep.Markdown+"\n"+inHistory.Markdown+"\n")
	g, _ := d.svc.Get(id, "")
	clk.tick()
	_, _ = d.svc.Update(UpdateParams{NoteID: id, BaseRevID: g.RevID, Body: strp(keep.Markdown + "\n")}) // the link is gone from the text, not from history

	if res, _ := d.svc.AttachmentCleanup(); res.Removed != 0 {
		t.Fatalf("fresh attachments must survive: %+v", res)
	}
	clk.set(clk.Now().UnixMilli() + 8*24*3600*1000)
	res, err := d.svc.AttachmentCleanup()
	if err != nil || res.Removed != 1 || res.FreedBytes != int64(len("old unused")) || res.Kept != 2 {
		t.Fatalf("cleanup = %+v, %v", res, err)
	}
	if g, _ := d.svc.AttachmentGet(old.BlobID); g.Present {
		t.Fatal("the unused attachment is still there")
	}
	for _, id := range []string{keep.BlobID, inHistory.BlobID} {
		if g, _ := d.svc.AttachmentGet(id); !g.Present {
			t.Fatalf("a used attachment was removed: %s", id)
		}
	}
}
