package facade

import (
	"strings"

	"nightclip/core/notes"
	"nightclip/core/proto"
)

// The notes methods (notes/api-spec.md). Like the vault they sit behind Call() but never on the
// local named pipe: another process of the same user must not be able to read the notes (core/pipe has
// a test that fixes this).

func isNotesMethod(method string) bool { return strings.HasPrefix(method, "notes.") }

func (c *Core) notesCall(method, params string) (any, error) {
	c.mu.RLock()
	started := c.started
	c.mu.RUnlock()
	n := c.net()
	if !started || n == nil || n.notes == nil {
		return nil, proto.Errorf(proto.CodeNotInitialized, "call Init first")
	}
	s := n.notes
	switch method {
	case "notes.list":
		var p struct {
			Limit   int    `json:"limit"`
			Cursor  string `json:"cursor"`
			Deleted bool   `json:"deleted"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return s.List(p.Limit, p.Cursor, p.Deleted)
	case "notes.get":
		var p struct {
			NoteID string `json:"note_id"`
			RevID  string `json:"rev_id"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return s.Get(p.NoteID, p.RevID)
	case "notes.create":
		var p struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return s.Create(p.Title, p.Body)
	case "notes.update":
		var p notes.UpdateParams
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return s.Update(p)
	case "notes.delete", "notes.restore":
		var p struct {
			NoteID string `json:"note_id"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		var rev string
		var err error
		if method == "notes.delete" {
			rev, err = s.Delete(p.NoteID)
		} else {
			rev, err = s.Restore(p.NoteID)
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"rev_id": rev}, nil
	case "notes.history":
		var p struct {
			NoteID string `json:"note_id"`
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return s.History(p.NoteID, p.Limit, p.Cursor)
	case "notes.render":
		var p struct {
			NoteID string  `json:"note_id"`
			RevID  string  `json:"rev_id"`
			Body   *string `json:"body"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		if p.Body != nil { // the live preview of unsaved text
			if p.NoteID != "" || p.RevID != "" {
				return nil, proto.Errorf(proto.CodeInvalid, "give either body or note_id, not both")
			}
			return s.RenderText(*p.Body)
		}
		return s.Render(p.NoteID, p.RevID)
	case "notes.normalize":
		var p struct {
			Body string `json:"body"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		norm, hash, err := s.Normalize(p.Body)
		if err != nil {
			return nil, err
		}
		return map[string]any{"body_norm": norm, "body_hash": hash}, nil
	case "notes.search":
		var p struct {
			Query  string `json:"query"`
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return s.Search(p.Query, p.Limit, p.Cursor)
	case "notes.export":
		var p struct {
			Path         string `json:"path"`
			IncludeTrash bool   `json:"include_trash"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return s.Export(p.Path, p.IncludeTrash)
	case "notes.import":
		var p struct {
			Path string `json:"path"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return s.Import(p.Path)
	case "notes.attachment_add":
		var p struct {
			Path string `json:"path"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return s.AttachmentAdd(p.Path)
	case "notes.attachment_get":
		var p struct {
			BlobID string `json:"blob_id"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return s.AttachmentGet(p.BlobID)
	case "notes.attachment_export":
		var p struct {
			BlobID string `json:"blob_id"`
			Path   string `json:"path"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return s.AttachmentExport(p.BlobID, p.Path)
	case "notes.attachment_cleanup":
		return s.AttachmentCleanup()
	case "notes.sync_status":
		return s.Status()
	case "notes.sync_now":
		var p struct {
			PeerID string `json:"peer_id"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		if err := s.SyncNow(p.PeerID); err != nil {
			return nil, err
		}
		return map[string]any{"started": true}, nil
	case "notes.sync_conflicts":
		var p struct {
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		return s.Conflicts(p.Limit, p.Cursor)
	case "notes.sync_resolve":
		var p struct {
			NoteID         string `json:"note_id"`
			ConflictNoteID string `json:"conflict_note_id"`
			Action         string `json:"action"`
		}
		if err := parseParams(params, &p); err != nil {
			return nil, err
		}
		rev, err := s.Resolve(p.NoteID, p.ConflictNoteID, p.Action)
		if err != nil {
			return nil, err
		}
		return map[string]any{"rev_id": rev}, nil
	}
	return nil, proto.Errorf(proto.CodeMethodNotFound, "unknown method %q", method)
}
