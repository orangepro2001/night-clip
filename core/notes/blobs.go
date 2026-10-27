package notes

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"nightclip/core/proto"
	"nightclip/core/store"
)

// Attachments (notes/docs/architecture.md §3). A note refers to a file with an ordinary Markdown link or
// image whose destination is "blob:<sha256 of the file>"; the bytes live next to the notes, sealed with
// the database key. Equal files are stored once. Sync moves the files that the current text of some note
// refers to (syncclient.go), never anything else.

const (
	// MaxBlobBytes is the largest attachment (architecture.md §9).
	MaxBlobBytes = 32 << 20
	maxBlobName  = 200
	// blobKeepFor protects fresh attachments from the cleanup: a file added a moment ago may not be
	// linked from a note yet.
	blobKeepFor = 7 * 24 * 3600 * 1000
)

var blobRefRe = regexp.MustCompile(`blob:([0-9a-f]{64})`)
var blobIDRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// blobRefs lists the attachments a text refers to (sorted, without duplicates). Occurrences inside code
// are included on purpose: this is a superset used to decide what to keep and what to sync.
func blobRefs(body string) []string {
	var out []string
	for _, m := range blobRefRe.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return sortedUnique(out)
}

// AttachmentInfo describes an attachment. Present is false for one that notes refer to but whose file
// has not reached this device yet (the other fields are then empty).
type AttachmentInfo struct {
	BlobID   string `json:"blob_id"`
	Name     string `json:"name"`
	Mime     string `json:"mime"`
	Size     int64  `json:"size"`
	Present  bool   `json:"present"`
	Markdown string `json:"markdown,omitempty"` // what to put in the note to show or link it (AttachmentAdd only)
}

func (s *Service) checkBlobID(id string) error {
	if !blobIDRe.MatchString(id) {
		return proto.Errorf(proto.CodeInvalid, "blob_id is malformed")
	}
	return nil
}

// imageMimes are shown inline by the shells. SVG is deliberately not among them: it can carry scripts.
var imageMimes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true, "image/bmp": true}

func blobName(path string) string {
	n := normTitle(filepath.Base(path))
	if len(n) > maxBlobName {
		n = cutTitle(n, maxBlobName)
	}
	if n == "" || n == "." || n == string(filepath.Separator) {
		return "file"
	}
	return n
}

func blobMime(name string, data []byte) string {
	if m := mime.TypeByExtension(strings.ToLower(filepath.Ext(name))); m != "" {
		if mt, _, err := mime.ParseMediaType(m); err == nil {
			return mt
		}
	}
	sniff := data
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	mt, _, _ := mime.ParseMediaType(http.DetectContentType(sniff))
	if mt == "" {
		return "application/octet-stream"
	}
	return mt
}

var mdEscape = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`)

func attachmentMarkdown(name, mimeType, id string) string {
	if imageMimes[mimeType] {
		return "![" + mdEscape.Replace(name) + "](blob:" + id + ")"
	}
	return "[" + mdEscape.Replace(name) + "](blob:" + id + ")"
}

// AttachmentAdd stores the file at path as an attachment and returns the Markdown that refers to it.
func (s *Service) AttachmentAdd(path string) (AttachmentInfo, error) {
	if path == "" || !filepath.IsAbs(path) {
		return AttachmentInfo{}, proto.Errorf(proto.CodeInvalid, "path must be an absolute path")
	}
	f, err := os.Open(path)
	if err != nil {
		return AttachmentInfo{}, proto.Errorf(proto.CodeInvalid, "cannot read that file")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return AttachmentInfo{}, proto.Errorf(proto.CodeInvalid, "that is not a file")
	}
	if st.Size() > MaxBlobBytes {
		return AttachmentInfo{}, proto.Errorf(proto.CodeTooLarge, "the file is larger than %d MiB", MaxBlobBytes>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBlobBytes+1))
	if err != nil {
		return AttachmentInfo{}, proto.Errorf(proto.CodeInvalid, "cannot read that file")
	}
	if len(data) > MaxBlobBytes {
		return AttachmentInfo{}, proto.Errorf(proto.CodeTooLarge, "the file is larger than %d MiB", MaxBlobBytes>>20)
	}
	sum := sha256.Sum256(data)
	b := store.NoteBlob{BlobID: hex.EncodeToString(sum[:]), Size: int64(len(data)), Name: blobName(path), Data: data, CreatedAt: s.nowMs()}
	b.Mime = blobMime(b.Name, data)

	s.mu.Lock()
	defer s.mu.Unlock()
	var info AttachmentInfo
	err = s.db.NotesTx(func(tx *store.NotesTx) error {
		if _, err := tx.PutBlob(b); err != nil {
			return err
		}
		// an equal file may be stored under another name: keep that one, the link text is the caller's
		stored, _, err := tx.GetBlob(b.BlobID, false)
		if err != nil {
			return err
		}
		info = AttachmentInfo{BlobID: stored.BlobID, Name: stored.Name, Mime: stored.Mime, Size: stored.Size, Present: true,
			Markdown: attachmentMarkdown(b.Name, stored.Mime, stored.BlobID)}
		return nil
	})
	return info, err
}

// AttachmentGet describes an attachment.
func (s *Service) AttachmentGet(blobID string) (AttachmentInfo, error) {
	if err := s.checkBlobID(blobID); err != nil {
		return AttachmentInfo{}, err
	}
	info := AttachmentInfo{BlobID: blobID}
	err := s.db.NotesView(func(tx *store.NotesTx) error {
		b, found, err := tx.GetBlob(blobID, false)
		if err != nil || !found {
			return err
		}
		info = AttachmentInfo{BlobID: blobID, Name: b.Name, Mime: b.Mime, Size: b.Size, Present: true}
		return nil
	})
	return info, err
}

// AttachmentExport writes an attachment to a file (replacing a file that is already there), so a shell
// can show or open it.
func (s *Service) AttachmentExport(blobID, dest string) (AttachmentInfo, error) {
	if err := s.checkBlobID(blobID); err != nil {
		return AttachmentInfo{}, err
	}
	if dest == "" || !filepath.IsAbs(dest) {
		return AttachmentInfo{}, proto.Errorf(proto.CodeInvalid, "path must be an absolute path")
	}
	var blob store.NoteBlob
	err := s.db.NotesView(func(tx *store.NotesTx) error {
		b, found, err := tx.GetBlob(blobID, true)
		if err != nil {
			return err
		}
		if !found {
			return proto.Errorf(proto.CodeNotFound, "that attachment has not reached this device yet")
		}
		blob = b
		return nil
	})
	if err != nil {
		return AttachmentInfo{}, err
	}
	if st, err := os.Stat(dest); err == nil && !st.Mode().IsRegular() {
		return AttachmentInfo{}, proto.Errorf(proto.CodeInvalid, "path is not a file")
	}
	if st, err := os.Stat(filepath.Dir(dest)); err != nil || !st.IsDir() {
		return AttachmentInfo{}, proto.Errorf(proto.CodeInvalid, "the folder for that file does not exist")
	}
	tmp := dest + ".part"
	if err := os.WriteFile(tmp, blob.Data, 0o600); err != nil {
		return AttachmentInfo{}, proto.Errorf(proto.CodeNoSpace, "cannot write the file: %v", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return AttachmentInfo{}, proto.Errorf(proto.CodeNoSpace, "cannot write the file: %v", err)
	}
	return AttachmentInfo{BlobID: blobID, Name: blob.Name, Mime: blob.Mime, Size: blob.Size, Present: true}, nil
}

// CleanupResult is returned by AttachmentCleanup.
type CleanupResult struct {
	Removed    int   `json:"removed"`
	FreedBytes int64 `json:"freed_bytes"`
	Kept       int   `json:"kept"`
}

// AttachmentCleanup deletes attachments that no revision of any note refers to (history included, so
// that an old version can still be shown) and that are older than a week.
func (s *Service) AttachmentCleanup() (CleanupResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res CleanupResult
	err := s.db.NotesTx(func(tx *store.NotesTx) error {
		infos, err := tx.BlobInfos()
		if err != nil {
			return err
		}
		used := map[string]bool{}
		if err := tx.Revisions(func(r store.NoteRev) bool {
			for _, id := range blobRefs(r.Body) {
				used[id] = true
			}
			return true
		}); err != nil {
			return err
		}
		cutoff := s.nowMs() - blobKeepFor
		for _, b := range infos {
			if used[b.BlobID] || b.CreatedAt > cutoff {
				res.Kept++
				continue
			}
			if err := tx.DeleteBlob(b.BlobID); err != nil {
				return err
			}
			res.Removed++
			res.FreedBytes += b.Size
		}
		return nil
	})
	return res, err
}

// ---- receiving attachments from a peer ----------------------------------------------------------------

type blobData struct {
	Name string
	Mime string
	Data []byte
}

var errBlobRefused = errors.New("notes: attachment refused")

// storeReceivedBlob checks and stores an attachment a peer sent (or served). The bytes must hash to the
// id, fit the limit, and be wanted: some note's current text must refer to them.
func (s *Service) storeReceivedBlob(blobID string, d blobData) error {
	if !blobIDRe.MatchString(blobID) || len(d.Data) > MaxBlobBytes {
		return errBlobRefused
	}
	sum := sha256.Sum256(d.Data)
	if hex.EncodeToString(sum[:]) != blobID {
		return errBlobRefused
	}
	name := normTitle(d.Name)
	if len(name) > maxBlobName {
		name = cutTitle(name, maxBlobName)
	}
	mt, _, err := mime.ParseMediaType(d.Mime)
	if err != nil || mt == "" {
		mt = "application/octet-stream"
	}
	inserted := false
	err = s.db.NotesTx(func(tx *store.NotesTx) error {
		wanted, err := tx.BlobReferenced(blobID)
		if err != nil {
			return err
		}
		if !wanted {
			return errBlobRefused
		}
		inserted, err = tx.PutBlob(store.NoteBlob{BlobID: blobID, Size: int64(len(d.Data)), Mime: mt, Name: name, Data: d.Data, CreatedAt: s.nowMs()})
		return err
	})
	if err != nil {
		return err
	}
	if inserted {
		s.emit(map[string]any{"type": "notes.attachment", "blob_id": blobID})
	}
	return nil
}

// missingAndHeld splits the attachments that notes refer to into those not stored here and those stored.
func (s *Service) missingAndHeld() (missing, held []string, err error) {
	err = s.db.NotesView(func(tx *store.NotesTx) error {
		refs, err := tx.ReferencedBlobs()
		if err != nil {
			return err
		}
		stored, err := tx.StoredBlobs(refs)
		if err != nil {
			return err
		}
		for _, id := range refs {
			if stored[id] {
				held = append(held, id)
			} else {
				missing = append(missing, id)
			}
		}
		return nil
	})
	return
}

// heldBlob reads a stored attachment for serving.
func (s *Service) heldBlob(blobID string) (blobData, bool, error) {
	var out blobData
	var found bool
	err := s.db.NotesView(func(tx *store.NotesTx) error {
		b, ok, err := tx.GetBlob(blobID, true)
		if err != nil || !ok {
			return err
		}
		out, found = blobData{Name: b.Name, Mime: b.Mime, Data: b.Data}, true
		return nil
	})
	return out, found, err
}

// storedAmong returns which of the ids are stored here.
func (s *Service) storedAmong(ids []string) ([]string, error) {
	var out []string
	err := s.db.NotesView(func(tx *store.NotesTx) error {
		m, err := tx.StoredBlobs(ids)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if m[id] {
				out = append(out, id)
			}
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}
