package xfer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// OfferSource describes what to send: a path (Windows) or a descriptor the core
// takes ownership of (Android's detachFd).
type OfferSource struct {
	Path string
	FD   int
	Name string // required with FD; optional with Path (defaults to the file name)
	Size int64  // optional hint for FD sources
}

// Offer starts sending a file to peerID. It returns at once; the file is hashed
// in the background (state "hashing") and the offer goes out when that is done.
func (s *Service) Offer(peerID string, src OfferSource) (string, error) {
	p, found, err := s.db.GetPeer(peerID)
	if err != nil {
		return "", err
	}
	if !found || p.State != "paired" {
		return "", proto.Errorf(proto.CodeNotPaired, "no paired device %s", peerID)
	}

	var (
		f     *os.File
		path  string
		name  = src.Name
		keep  bool // keep f open as the source (descriptor-backed)
		fdSrc = src.Path == ""
	)
	switch {
	case !fdSrc:
		if !filepath.IsAbs(src.Path) {
			return "", proto.Errorf(proto.CodeInvalid, "path must be absolute")
		}
		f, err = os.Open(src.Path)
		if err != nil {
			return "", proto.Errorf(proto.CodeInvalid, "cannot open the file: %v", err)
		}
		path = src.Path
		if name == "" {
			name = filepath.Base(src.Path)
		}
	case src.FD > 0:
		f = os.NewFile(uintptr(src.FD), "offer")
		if f == nil {
			return "", proto.Errorf(proto.CodeInvalid, "bad file descriptor")
		}
		if name == "" {
			f.Close()
			return "", proto.Errorf(proto.CodeInvalid, "name is required with fd")
		}
	default:
		return "", proto.Errorf(proto.CodeInvalid, "give either path or fd")
	}
	defer func() {
		if !keep && f != nil {
			f.Close()
		}
	}()

	st, err := f.Stat()
	if err != nil {
		return "", proto.Errorf(proto.CodeInvalid, "cannot read the file: %v", err)
	}
	size := st.Size()
	if fdSrc {
		// A content-provider descriptor may be a pipe: it cannot serve Range requests.
		if !st.Mode().IsRegular() {
			size = 0
		}
		if _, serr := f.Seek(0, io.SeekStart); serr != nil || !st.Mode().IsRegular() {
			copied, n, cerr := s.spoolToDisk(f)
			if cerr != nil {
				return "", proto.Errorf(proto.CodeInvalid, "cannot read the file: %v", cerr)
			}
			path, size = copied, n
			f.Close()
			f = nil
		} else {
			keep = true
		}
	} else if !st.Mode().IsRegular() {
		return "", proto.Errorf(proto.CodeInvalid, "not a regular file")
	}
	if max := s.MaxFileBytes(); size > max {
		if keep {
			f.Close()
		}
		return "", proto.Errorf(proto.CodeTooLarge, "the file is %d bytes; the limit is %d", size, max)
	}

	id := newTransferID()
	now := s.now()
	mode := ModePull
	if s.e.WeDial(p) {
		mode = ModePush
	}
	t := store.Transfer{
		ID: id, PeerID: peerID, Direction: DirOut, Name: SafeName(name), Size: size, State: StateHashing,
		Path: path, CreatedAt: now.Unix(), ExpiresAt: now.Add(offerTTL).Unix(), Mode: mode, MimeType: mimeFor(name),
	}
	if err := s.db.PutTransfer(t); err != nil {
		return "", err
	}
	if keep {
		s.mu.Lock()
		s.sources[id] = f
		s.mu.Unlock()
	}
	s.emit(map[string]any{"type": "file.state", "transfer_id": id, "state": StateHashing})
	s.startHash(t)
	return id, nil
}

// spoolToDisk copies a non-seekable source into the data directory.
func (s *Service) spoolToDisk(r io.Reader) (string, int64, error) {
	dir := filepath.Join(s.dataDir, "outgoing")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, err
	}
	f, err := os.CreateTemp(dir, "spool-*")
	if err != nil {
		return "", 0, err
	}
	n, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", 0, err
	}
	return f.Name(), n, nil
}

// startHash computes the SHA-256 in the background and then sends the offer.
func (s *Service) startHash(t store.Transfer) {
	s.runTask(t.ID, func(ctx context.Context) {
		r, closeFn, err := s.sourceFor(t)
		if err != nil {
			s.fail(t, "SOURCE_LOST", err.Error())
			return
		}
		defer closeFn()
		sum, n, err := hashReader(ctx, io.NewSectionReader(r, 0, 1<<62))
		if err != nil {
			if ctx.Err() == nil {
				s.fail(t, "SOURCE_LOST", err.Error())
			}
			return
		}
		cur, gerr := s.get(t.ID)
		if gerr != nil || terminal(cur.State) {
			return
		}
		cur.SHA256, cur.Size = sum, n
		if n > s.MaxFileBytes() {
			s.fail(cur, string(proto.CodeTooLarge), "the file is larger than the limit")
			return
		}
		cur.State = StateOffered
		if err := s.db.PutTransfer(cur); err != nil {
			s.fail(cur, string(proto.CodeDBUnavailable), err.Error())
			return
		}
		payload := map[string]any{
			"transfer_id": cur.ID, "name": cur.Name, "size": cur.Size, "sha256": cur.SHA256,
			"mime_type": cur.MimeType, "mode": cur.Mode,
		}
		if _, err := s.e.Send(cur.PeerID, proto.KindFile, payload, peer.SendOptions{TTL: time.Until(time.Unix(cur.ExpiresAt, 0))}); err != nil {
			s.fail(cur, "OFFER_FAILED", err.Error())
			return
		}
		s.emit(map[string]any{"type": "file.state", "transfer_id": cur.ID, "state": StateOffered})
	})
}

// sourceFor opens the bytes of an outgoing transfer. The returned func releases
// the handle (a no-op for descriptor-backed sources, which stay open until the
// transfer ends).
func (s *Service) sourceFor(t store.Transfer) (*os.File, func(), error) {
	if t.Path != "" {
		f, err := os.Open(t.Path)
		if err != nil {
			return nil, func() {}, fmt.Errorf("the file is no longer readable: %v", err)
		}
		return f, func() { _ = f.Close() }, nil
	}
	s.mu.Lock()
	f := s.sources[t.ID]
	s.mu.Unlock()
	if f == nil {
		return nil, func() {}, fmt.Errorf("the file handle was lost")
	}
	return f, func() {}, nil
}

// dropSource releases an outgoing transfer's descriptor or spooled copy.
func (s *Service) dropSource(id string) {
	s.mu.Lock()
	f := s.sources[id]
	delete(s.sources, id)
	s.mu.Unlock()
	if f != nil {
		_ = f.Close()
	}
	if t, found, _ := s.db.GetTransfer(id); found && t.Direction == DirOut && strings.HasPrefix(t.Path, filepath.Join(s.dataDir, "outgoing")) {
		_ = os.Remove(t.Path)
	}
}

// runTask runs fn as the transfer's single background goroutine.
func (s *Service) runTask(id string, fn func(ctx context.Context)) {
	s.mu.Lock()
	if s.closed || s.tasks[id] != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	s.tasks[id] = cancel
	s.mu.Unlock()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.mu.Lock()
			delete(s.tasks, id)
			s.mu.Unlock()
			cancel()
			s.afterTask(id)
		}()
		fn(ctx)
	}()
}

// hasTask reports whether a goroutine or an upload request still works on id (and
// so may hold the part file open).
func (s *Service) hasTask(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tasks[id] != nil || s.tasks["put:"+id] != nil
}

// afterTask removes the part file of an incoming transfer that ended without
// success. It runs once the working goroutine has released the file: Windows
// refuses to delete a file that is still open.
func (s *Service) afterTask(id string) {
	if t, found, _ := s.db.GetTransfer(id); found && t.Direction == DirIn && terminal(t.State) && t.State != StateDone {
		s.removePart(t)
	}
}

func (s *Service) stopTask(id string) {
	s.mu.Lock()
	cancel := s.tasks[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ---- control events from the peer ------------------------------------------------

// OnControl handles file_accept, file_reject and file_cancel.
func (s *Service) OnControl(peerID string, env *proto.Envelope) {
	var c struct {
		Action     string `json:"action"`
		TransferID string `json:"transfer_id"`
		Offset     int64  `json:"offset"`
		Reason     string `json:"reason"`
	}
	if !decodePayload(env, &c) || !strings.HasPrefix(c.Action, "file_") {
		return
	}
	t, found, err := s.db.GetTransfer(c.TransferID)
	if err != nil || !found || t.PeerID != peerID {
		return // not ours
	}
	// The receiver verifies only after the last byte left; its verdict can arrive
	// after we already counted the transfer as delivered, and it wins.
	if c.Action == "file_cancel" && c.Reason == string(proto.CodeHashMismatch) && t.Direction == DirOut && t.State == StateDone {
		_ = s.db.SetTransferState(t.ID, StateFailed, c.Reason)
		s.emit(map[string]any{"type": "file.failed", "transfer_id": t.ID, "code": c.Reason, "direction": DirOut,
			"message": "the other device could not verify the file"})
		s.emit(map[string]any{"type": "file.state", "transfer_id": t.ID, "state": StateFailed})
		return
	}
	if terminal(t.State) {
		return
	}
	switch c.Action {
	case "file_accept":
		// push: the receiver gives the start offset. pull: only a zero-byte file sends
		// this (there is no download to learn from), so the sender can finish.
		if t.Direction != DirOut || t.State != StateOffered || (t.Mode == ModePull && t.Size != 0) {
			return
		}
		t.Offset = max(0, min(c.Offset, t.Size))
		_ = s.db.SetTransferOffset(t.ID, t.Offset)
		t = s.setState(t, StateTransferring)
		if t.Size == 0 {
			s.finishSent(t)
			return
		}
		s.startUpload(t.ID)
	case "file_reject":
		if t.Direction != DirOut {
			return
		}
		s.stopTask(t.ID)
		s.end(t, StateRejected, "REJECTED", "the other device declined the file")
	case "file_cancel":
		s.stopTask(t.ID)
		code := "CANCELLED"
		if c.Reason == string(proto.CodeHashMismatch) {
			code = c.Reason
			s.end(t, StateFailed, code, "the other device could not verify the file")
			return
		}
		s.end(t, StateCancelled, code, "the other device cancelled the transfer")
	}
}

// end moves a transfer to a terminal state and tells the shells why.
func (s *Service) end(t store.Transfer, state, code, msg string) {
	_ = s.db.SetTransferState(t.ID, state, code)
	s.dropSource(t.ID)
	if t.Direction == DirIn && !s.hasTask(t.ID) {
		s.removePart(t) // otherwise afterTask does it when the file is released
	}
	s.forgetMeter(t.ID)
	s.emit(map[string]any{"type": "file.failed", "transfer_id": t.ID, "code": code, "message": msg, "direction": t.Direction})
	s.emit(map[string]any{"type": "file.state", "transfer_id": t.ID, "state": state})
}

// finishSent marks an outgoing transfer delivered.
func (s *Service) finishSent(t store.Transfer) {
	_ = s.db.SetTransferOffset(t.ID, t.Size)
	_ = s.db.SetTransferState(t.ID, StateDone, "")
	s.dropSource(t.ID)
	s.emit(map[string]any{"type": "file.progress", "transfer_id": t.ID, "offset": t.Size, "size": t.Size, "rate_bps": s.rate(t.ID)})
	s.forgetMeter(t.ID)
	s.emit(map[string]any{"type": "file.done", "transfer_id": t.ID, "direction": DirOut, "name": t.Name, "size": t.Size, "sha256": t.SHA256})
	s.emit(map[string]any{"type": "file.state", "transfer_id": t.ID, "state": StateDone})
}

// ---- push upload (we are the dialer; the receiver is the listener) ----------------

func (s *Service) startUpload(id string) {
	s.runTask(id, func(ctx context.Context) { s.uploadLoop(ctx, id) })
}

func (s *Service) uploadLoop(ctx context.Context, id string) {
	wait := s.timing.RetryBase
	for ctx.Err() == nil {
		t, err := s.get(id)
		if err != nil || terminal(t.State) {
			return
		}
		if time.Now().Unix() >= t.ExpiresAt {
			s.end(t, StateExpired, string(proto.CodeExpired), "the offer expired")
			return
		}
		done, perm, err := s.uploadOnce(ctx, t)
		switch {
		case done:
			cur, _ := s.get(id)
			s.finishSent(cur)
			return
		case perm != nil:
			s.end(t, StateFailed, perm.code, perm.msg)
			return
		case ctx.Err() != nil:
			return
		}
		_ = err // transient: back off and ask the receiver where it stands
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, s.timing.RetryMax)
	}
}

type permanent struct{ code, msg string }

// uploadOnce asks the receiver for its offset, then sends the rest.
func (s *Service) uploadOnce(ctx context.Context, t store.Transfer) (done bool, perm *permanent, err error) {
	p, found, gerr := s.db.GetPeer(t.PeerID)
	if gerr != nil || !found || p.State != "paired" {
		return false, &permanent{string(proto.CodeNotPaired), "the device is no longer paired"}, nil
	}
	ep, err := s.e.Endpoint(p)
	if err != nil {
		return false, nil, err
	}
	f, closeFn, serr := s.sourceFor(t)
	if serr != nil {
		return false, &permanent{"SOURCE_LOST", serr.Error()}, nil
	}
	defer closeFn()
	if st, e := f.Stat(); e != nil || st.Size() != t.Size {
		return false, &permanent{"SOURCE_CHANGED", "the file changed after it was offered"}, nil
	}

	var lastErr error
	for _, base := range ep.Bases {
		url := base + "/v1/files/" + t.ID

		// Where does the receiver stand?
		hreq, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
		hresp, herr := ep.Client.Do(hreq)
		if herr != nil {
			lastErr = herr
			continue
		}
		hresp.Body.Close()
		switch hresp.StatusCode {
		case http.StatusOK:
		case http.StatusGone, http.StatusNotFound, http.StatusForbidden:
			return false, &permanent{"CANCELLED", "the other device no longer wants this file"}, nil
		default:
			lastErr = fmt.Errorf("HEAD answered %d", hresp.StatusCode)
			continue
		}
		offset, _ := strconv.ParseInt(hresp.Header.Get("X-Offset"), 10, 64)
		if hresp.Header.Get("X-State") == StateDone {
			return true, nil, nil
		}
		offset = max(0, min(offset, t.Size))

		body := &progressReader{r: io.NewSectionReader(f, offset, t.Size-offset), s: s, id: t.ID, base: offset, size: t.Size, ctx: ctx}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, url, body)
		req.ContentLength = t.Size - offset
		if req.ContentLength == 0 {
			req.Body = http.NoBody
		}
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, max(t.Size-1, 0), t.Size))
		if t.Size == 0 {
			req.Header.Set("Content-Range", "bytes */0")
		}
		resp, derr := ep.Client.Do(req)
		if derr != nil {
			lastErr = derr
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusOK:
			return true, nil, nil
		case http.StatusConflict: // the receiver moved: loop again with the offset it reports
			var c struct {
				Offset int64 `json:"offset"`
			}
			_ = json.Unmarshal(data, &c)
			lastErr = fmt.Errorf("receiver is at %d", c.Offset)
		case http.StatusUnprocessableEntity:
			return false, &permanent{string(proto.CodeHashMismatch), "the other device could not verify the file"}, nil
		case http.StatusGone, http.StatusNotFound, http.StatusForbidden:
			return false, &permanent{"CANCELLED", "the other device no longer wants this file"}, nil
		case http.StatusInsufficientStorage:
			return false, &permanent{string(proto.CodeNoSpace), "the other device ran out of space"}, nil
		default:
			lastErr = fmt.Errorf("PUT answered %d", resp.StatusCode)
		}
	}
	if lastErr == nil {
		lastErr = errTransient
	}
	return false, nil, lastErr
}

// progressReader reports bytes read from the file as upload progress and stops
// when the transfer is cancelled.
type progressReader struct {
	r    io.Reader
	s    *Service
	id   string
	base int64
	n    int64
	size int64
	ctx  context.Context
}

func (p *progressReader) Read(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	if err := p.s.chunkHook(p.id, p.base+p.n); err != nil {
		return 0, err
	}
	n, err := p.r.Read(b)
	p.n += int64(n)
	if n > 0 {
		p.s.progress(p.id, p.base+p.n, p.size)
	}
	return n, err
}
