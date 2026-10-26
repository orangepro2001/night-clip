package xfer

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
)

func jsonError(w http.ResponseWriter, status int, code string, extra map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body := map[string]any{"error": map[string]any{"code": code}}
	for k, v := range extra {
		body[k] = v
	}
	_ = json.NewEncoder(w).Encode(body)
}

// transferFor finds the transfer a request is about and checks that the caller
// is the device it was exchanged with. It writes the error response itself.
func (s *Service) transferFor(w http.ResponseWriter, r *http.Request) (store.Transfer, bool) {
	caller := peer.PeerFromContext(r.Context())
	if caller.Peer == nil {
		jsonError(w, http.StatusForbidden, string(proto.CodeNotPaired), nil)
		return store.Transfer{}, false
	}
	t, found, err := s.db.GetTransfer(r.PathValue("id"))
	if err != nil {
		jsonError(w, http.StatusInternalServerError, string(proto.CodeInternal), nil)
		return store.Transfer{}, false
	}
	if !found {
		jsonError(w, http.StatusNotFound, "NOT_FOUND", nil)
		return store.Transfer{}, false
	}
	if t.PeerID != caller.Peer.DeviceID {
		// Another paired device has no business with this transfer; do not reveal it exists.
		jsonError(w, http.StatusNotFound, "NOT_FOUND", nil)
		return store.Transfer{}, false
	}
	return t, true
}

// live reports whether a transfer may still move bytes; if not it writes 410.
func live(w http.ResponseWriter, t store.Transfer, allowDone bool) bool {
	if t.State == StateDone && allowDone {
		return true
	}
	if terminal(t.State) || time.Now().Unix() >= t.ExpiresAt {
		code := "GONE"
		switch t.State {
		case StateCancelled:
			code = "CANCELLED"
		case StateExpired:
			code = string(proto.CodeExpired)
		}
		if time.Now().Unix() >= t.ExpiresAt {
			code = string(proto.CodeExpired)
		}
		jsonError(w, http.StatusGone, code, nil)
		return false
	}
	return true
}

// serveHead tells the counterparty where the transfer stands, so an interrupted
// upload can resume at the right byte.
func (s *Service) serveHead(w http.ResponseWriter, r *http.Request) {
	t, ok := s.transferFor(w, r)
	if !ok || !live(w, t, true) {
		return
	}
	offset := t.Offset
	if t.Direction == DirIn {
		offset = partSize(t)
		if t.State == StateDone {
			offset = t.Size
		}
	}
	w.Header().Set("X-Offset", strconv.FormatInt(offset, 10))
	w.Header().Set("X-State", t.State)
	w.Header().Set("X-Size", strconv.FormatInt(t.Size, 10))
	w.WriteHeader(http.StatusOK)
}

// progressWriter counts the bytes of a response for progress reporting.
type progressWriter struct {
	http.ResponseWriter
	s    *Service
	id   string
	base int64
	n    int64
	size int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	if err := p.s.chunkHook(p.id, p.base+p.n); err != nil {
		return 0, err
	}
	n, err := p.ResponseWriter.Write(b)
	p.n += int64(n)
	if n > 0 {
		p.s.progress(p.id, p.base+p.n, p.size)
	}
	return n, err
}

func (p *progressWriter) Flush() {
	if f, ok := p.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// rangeStart returns N of "Range: bytes=N-" (0 when absent or unusual).
func rangeStart(h string) int64 {
	h = strings.TrimSpace(h)
	if !strings.HasPrefix(h, "bytes=") {
		return 0
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(h, "bytes="), "-")
	n, err := strconv.ParseInt(first, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// serveGet serves an offered file to the device it was offered to (mode pull).
// Range requests make interrupted downloads resumable.
func (s *Service) serveGet(w http.ResponseWriter, r *http.Request) {
	t, ok := s.transferFor(w, r)
	if !ok {
		return
	}
	if t.Direction != DirOut || t.Mode != ModePull {
		jsonError(w, http.StatusForbidden, "WRONG_MODE", nil)
		return
	}
	if !live(w, t, false) {
		return
	}
	if t.State == StateHashing {
		jsonError(w, http.StatusConflict, "NOT_READY", nil)
		return
	}
	f, closeFn, err := s.sourceFor(t)
	if err != nil {
		jsonError(w, http.StatusGone, "SOURCE_LOST", nil)
		return
	}
	defer closeFn()
	if st, e := f.Stat(); e != nil || st.Size() != t.Size {
		jsonError(w, http.StatusConflict, "SOURCE_CHANGED", nil)
		return
	}
	if t.State == StateOffered || t.State == StateAccepted {
		t = s.setState(t, StateTransferring)
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", `"`+t.SHA256+`"`)
	pw := &progressWriter{ResponseWriter: w, s: s, id: t.ID, base: rangeStart(r.Header.Get("Range")), size: t.Size}
	http.ServeContent(pw, r, "", time.Time{}, io.NewSectionReader(f, 0, t.Size))
	if pw.base+pw.n >= t.Size && r.Context().Err() == nil {
		if cur, e := s.get(t.ID); e == nil && !terminal(cur.State) {
			s.finishSent(cur)
		}
	}
}

// servePut receives an upload (mode push). The sender asks HEAD for the offset,
// then sends the rest; a wrong start gets 409 with the right one.
func (s *Service) servePut(w http.ResponseWriter, r *http.Request) {
	t, ok := s.transferFor(w, r)
	if !ok {
		return
	}
	if t.Direction != DirIn || t.Mode != ModePush {
		jsonError(w, http.StatusForbidden, "WRONG_MODE", nil)
		return
	}
	if !live(w, t, false) {
		return
	}
	if t.State != StateAccepted && t.State != StateTransferring {
		jsonError(w, http.StatusConflict, "NOT_ACCEPTED", nil)
		return
	}

	var start, end, total int64
	cr := r.Header.Get("Content-Range")
	switch {
	case cr == "bytes */0":
		start, end, total = 0, -1, 0
	default:
		if _, err := fmt.Sscanf(cr, "bytes %d-%d/%d", &start, &end, &total); err != nil || start < 0 || end < start || total != t.Size || end >= total {
			jsonError(w, http.StatusBadRequest, string(proto.CodeInvalid), nil)
			return
		}
	}
	if r.ContentLength != end-start+1 {
		jsonError(w, http.StatusBadRequest, string(proto.CodeInvalid), nil)
		return
	}
	cur := partSize(t)
	if t.Size > 0 && start != cur {
		jsonError(w, http.StatusConflict, "OFFSET_MISMATCH", map[string]any{"offset": cur})
		return
	}
	if !s.claim(t.ID) {
		jsonError(w, http.StatusConflict, "BUSY", map[string]any{"offset": cur})
		return
	}
	defer func() { s.release(t.ID); s.afterTask(t.ID) }()

	if t.State != StateTransferring {
		t = s.setState(t, StateTransferring)
	}
	if t.Size > 0 {
		f, err := os.OpenFile(partPath(t), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			jsonError(w, http.StatusInsufficientStorage, string(proto.CodeNoSpace), nil)
			return
		}
		idle := noStall()
		defer idle.Stop()
		n, cerr := s.copyBody(r.Context(), f, io.LimitReader(r.Body, end-start+1), t, cur, idle)
		_ = f.Close()
		if cerr != nil {
			var pe *permanent
			if asPermanent(cerr, &pe) {
				s.control(t.PeerID, map[string]any{"action": "file_cancel", "transfer_id": t.ID, "reason": pe.code})
				s.end(t, StateFailed, pe.code, pe.msg)
				status := http.StatusInsufficientStorage
				if pe.code == string(proto.CodeHashMismatch) {
					status = http.StatusUnprocessableEntity
				}
				jsonError(w, status, pe.code, nil)
			}
			return // the connection broke: the sender will ask HEAD and resume
		}
		if cur+n < t.Size {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "partial", "offset": cur + n})
			return
		}
	}
	if _, err := s.finalize(t); err != nil {
		var pe *permanent
		if asPermanent(err, &pe) {
			s.control(t.PeerID, map[string]any{"action": "file_cancel", "transfer_id": t.ID, "reason": pe.code})
			s.end(t, StateFailed, pe.code, pe.msg)
			status := http.StatusInsufficientStorage
			if pe.code == string(proto.CodeHashMismatch) {
				status = http.StatusUnprocessableEntity
			}
			jsonError(w, status, pe.code, nil)
			return
		}
		jsonError(w, http.StatusInternalServerError, string(proto.CodeInternal), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"state": "complete"})
}

// claim marks a transfer as being written by one request; two concurrent PUTs
// for the same transfer would corrupt the part file.
func (s *Service) claim(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := "put:" + id
	if _, busy := s.tasks[key]; busy {
		return false
	}
	s.tasks[key] = func() {}
	return true
}

func (s *Service) release(id string) {
	s.mu.Lock()
	delete(s.tasks, "put:"+id)
	s.mu.Unlock()
}

// noStall is a timer that never fires: server-side uploads are bounded by the
// HTTP server's own connection handling.
func noStall() *time.Timer {
	t := time.NewTimer(time.Hour * 24 * 365)
	return t
}
