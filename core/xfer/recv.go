package xfer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"nightclip/core/proto"
	"nightclip/core/store"
)

// ---- incoming offers ------------------------------------------------------------------

// OnOffer handles a file offer event from a peer: validate, remember, and ask the
// user. Nothing is downloaded until Accept.
func (s *Service) OnOffer(peerID string, env *proto.Envelope) {
	var o struct {
		TransferID string `json:"transfer_id"`
		Name       string `json:"name"`
		Size       int64  `json:"size"`
		SHA256     string `json:"sha256"`
		MimeType   string `json:"mime_type"`
		Mode       string `json:"mode"`
	}
	if !decodePayload(env, &o) || !transferIDRe.MatchString(o.TransferID) || !sha256HexRe.MatchString(o.SHA256) ||
		o.Size < 0 || len(o.Name) == 0 || len(o.Name) > 1024 || len(o.MimeType) > 128 || (o.Mode != ModePull && o.Mode != ModePush) {
		return // malformed: ignore (the event was already acknowledged and consumed)
	}
	if _, found, _ := s.db.GetTransfer(o.TransferID); found {
		return // an offer is processed once
	}
	p, found, err := s.db.GetPeer(peerID)
	if err != nil || !found || p.State != "paired" {
		return
	}
	reject := func(code string) {
		s.control(peerID, map[string]any{"action": "file_reject", "transfer_id": o.TransferID, "reason": code})
	}
	// Who moves the bytes must agree with who dials: a "pull" means the sender serves
	// and we are the dialer; a "push" means we serve and the sender dials.
	if (o.Mode == ModePull) != s.e.WeDial(p) {
		reject("bad_mode")
		return
	}
	if o.Size > s.MaxFileBytes() {
		reject(string(proto.CodeTooLarge))
		return
	}
	t := store.Transfer{
		ID: o.TransferID, PeerID: peerID, Direction: DirIn, Name: SafeName(o.Name), Size: o.Size, SHA256: o.SHA256,
		State: StateOffered, CreatedAt: s.now().Unix(), ExpiresAt: env.ExpiresAt.Unix(), Mode: o.Mode, MimeType: o.MimeType,
	}
	if err := s.db.PutTransfer(t); err != nil {
		return
	}
	s.emit(map[string]any{
		"type": "file.offered", "transfer_id": t.ID, "from": peerID, "from_name": p.Name, "name": t.Name, "size": t.Size,
		"sha256": t.SHA256, "mime_type": t.MimeType, "expires_at": t.ExpiresAt,
	})
}

// partPath is where a download is assembled: beside its destination, so the final
// step is an atomic rename on the same volume.
func partPath(t store.Transfer) string {
	return filepath.Join(t.SaveDir, "."+t.ID+".part")
}

func (s *Service) removePart(t store.Transfer) {
	if t.SaveDir != "" {
		_ = os.Remove(partPath(t))
	}
}

func partSize(t store.Transfer) int64 {
	if st, err := os.Stat(partPath(t)); err == nil {
		return st.Size()
	}
	return 0
}

// Accept agrees to receive an offered file. saveDir overrides the default
// download directory.
func (s *Service) Accept(id, saveDir string) error {
	t, err := s.get(id)
	if err != nil {
		return err
	}
	if t.Direction != DirIn || t.State != StateOffered {
		return proto.Errorf(proto.CodeInvalid, "this transfer is not waiting for an answer")
	}
	if time.Now().Unix() >= t.ExpiresAt {
		s.end(t, StateExpired, string(proto.CodeExpired), "the offer expired")
		return proto.Errorf(proto.CodeExpired, "the offer has expired")
	}
	dir := saveDir
	if dir == "" {
		dir = s.DownloadDir()
	}
	if !filepath.IsAbs(dir) {
		return proto.Errorf(proto.CodeInvalid, "save_dir must be an absolute path")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return proto.Errorf(proto.CodeInvalid, "cannot use that folder: %v", err)
	}
	free, err := freeSpace(dir)
	if err != nil {
		return proto.Errorf(proto.CodeInvalid, "cannot use that folder: %v", err)
	}
	if need := uint64(t.Size) + uint64(spaceHeadroom); free < need {
		s.control(t.PeerID, map[string]any{"action": "file_reject", "transfer_id": t.ID, "reason": string(proto.CodeNoSpace)})
		s.end(t, StateFailed, string(proto.CodeNoSpace), "not enough free space")
		return proto.Errorf(proto.CodeNoSpace, "not enough free space in %s", dir)
	}
	t.SaveDir = dir
	if err := s.db.PutTransfer(t); err != nil {
		return err
	}
	t = s.setState(t, StateAccepted)

	if t.Size == 0 { // nothing to move: create the empty file now
		return s.finishEmpty(t)
	}
	if t.Mode == ModePull {
		s.startDownload(t.ID)
		return nil
	}
	// push: tell the sender where to start; its upload arrives on our server
	s.control(t.PeerID, map[string]any{"action": "file_accept", "transfer_id": t.ID, "offset": partSize(t)})
	return nil
}

// finishEmpty completes a zero-byte transfer.
func (s *Service) finishEmpty(t store.Transfer) error {
	if _, err := s.finalize(t); err != nil {
		return err
	}
	// Nothing will be fetched or uploaded, so say explicitly that it arrived.
	s.control(t.PeerID, map[string]any{"action": "file_accept", "transfer_id": t.ID, "offset": 0})
	return nil
}

// Reject declines an offer.
func (s *Service) Reject(id string) error {
	t, err := s.get(id)
	if err != nil {
		return err
	}
	if t.Direction != DirIn || t.State != StateOffered {
		return proto.Errorf(proto.CodeInvalid, "this transfer is not waiting for an answer")
	}
	s.control(t.PeerID, map[string]any{"action": "file_reject", "transfer_id": t.ID, "reason": "declined"})
	_ = s.db.SetTransferState(t.ID, StateRejected, "")
	s.emit(map[string]any{"type": "file.state", "transfer_id": t.ID, "state": StateRejected})
	return nil
}

// Cancel stops a transfer that is not finished, on either side.
func (s *Service) Cancel(id string) error {
	t, err := s.get(id)
	if err != nil {
		return err
	}
	if terminal(t.State) {
		return proto.Errorf(proto.CodeInvalid, "this transfer has already finished")
	}
	s.stopTask(id)
	s.control(t.PeerID, map[string]any{"action": "file_cancel", "transfer_id": t.ID, "reason": "user"})
	s.end(t, StateCancelled, "CANCELLED", "cancelled")
	return nil
}

// Release is called by the Android shell after it moved a finished file into
// MediaStore: the private copy is deleted and the row points nowhere.
func (s *Service) Release(id string) error {
	t, err := s.get(id)
	if err != nil {
		return err
	}
	if t.Direction != DirIn || t.State != StateDone {
		return proto.Errorf(proto.CodeInvalid, "only a finished incoming file can be released")
	}
	if t.Path != "" {
		_ = os.Remove(t.Path)
	}
	return s.db.SetTransferPath(id, "")
}

// ---- pull download (we are the dialer; the sender is the listener) -----------------

func (s *Service) startDownload(id string) {
	s.runTask(id, func(ctx context.Context) { s.downloadLoop(ctx, id) })
}

func (s *Service) downloadLoop(ctx context.Context, id string) {
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
		done, perm, _ := s.pullOnce(ctx, t)
		switch {
		case done:
			return
		case perm != nil:
			if perm.code == string(proto.CodeHashMismatch) {
				s.control(t.PeerID, map[string]any{"action": "file_cancel", "transfer_id": t.ID, "reason": perm.code})
			}
			s.end(t, StateFailed, perm.code, perm.msg)
			return
		case ctx.Err() != nil:
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, s.timing.RetryMax)
	}
}

// pullOnce fetches the missing bytes from the sender, resuming at the size of the
// part file. A transient problem returns (false, nil, err); a final one a
// *permanent.
func (s *Service) pullOnce(ctx context.Context, t store.Transfer) (done bool, perm *permanent, err error) {
	p, found, gerr := s.db.GetPeer(t.PeerID)
	if gerr != nil || !found || p.State != "paired" {
		return false, &permanent{string(proto.CodeNotPaired), "the device is no longer paired"}, nil
	}
	ep, err := s.e.Endpoint(p)
	if err != nil {
		return false, nil, err
	}
	var lastErr error
	for _, base := range ep.Bases {
		off := partSize(t)
		if off > t.Size {
			_ = os.Remove(partPath(t))
			off = 0
		}
		if off == t.Size { // everything is already here (a crash between download and verify)
			_, ferr := s.finalize(t)
			return ferr == nil, nil, ferr
		}
		actx, cancel := context.WithCancel(ctx)
		stall := time.AfterFunc(s.timing.Stall, cancel)
		req, _ := http.NewRequestWithContext(actx, http.MethodGet, base+"/v1/files/"+t.ID, nil)
		if off > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", off))
		}
		resp, derr := ep.Client.Do(req)
		if derr != nil {
			stall.Stop()
			cancel()
			lastErr = derr
			continue
		}
		finish := func() { stall.Stop(); cancel(); resp.Body.Close() }

		flags := os.O_WRONLY | os.O_CREATE
		switch resp.StatusCode {
		case http.StatusOK:
			off = 0 // the sender ignored the range: take everything again
			flags |= os.O_TRUNC
		case http.StatusPartialContent:
			var start, end, total int64
			if _, serr := fmt.Sscanf(resp.Header.Get("Content-Range"), "bytes %d-%d/%d", &start, &end, &total); serr != nil || start != off || total != t.Size {
				finish()
				_ = os.Remove(partPath(t))
				lastErr = fmt.Errorf("unexpected Content-Range")
				continue
			}
			flags |= os.O_APPEND
		case http.StatusRequestedRangeNotSatisfiable:
			finish()
			_ = os.Remove(partPath(t))
			lastErr = fmt.Errorf("range refused")
			continue
		case http.StatusGone, http.StatusNotFound, http.StatusForbidden:
			finish()
			return false, &permanent{"CANCELLED", "the other device no longer offers this file"}, nil
		default:
			finish()
			lastErr = fmt.Errorf("GET answered %d", resp.StatusCode)
			continue
		}

		f, ferr := os.OpenFile(partPath(t), flags, 0o600)
		if ferr != nil {
			finish()
			return false, &permanent{string(proto.CodeNoSpace), ferr.Error()}, nil
		}
		if t.State != StateTransferring {
			t = s.setState(t, StateTransferring)
		}
		n, cerr := s.copyBody(actx, f, resp.Body, t, off, stall)
		_ = f.Close()
		finish()
		if cerr != nil {
			var pe *permanent
			if asPermanent(cerr, &pe) {
				return false, pe, nil
			}
			lastErr = cerr
			if ctx.Err() != nil {
				return false, nil, ctx.Err()
			}
			continue
		}
		if off+n != t.Size {
			lastErr = fmt.Errorf("the transfer ended early (%d of %d bytes)", off+n, t.Size)
			continue
		}
		path, ferr2 := s.finalize(t)
		if ferr2 != nil {
			var pe *permanent
			if asPermanent(ferr2, &pe) {
				return false, pe, nil
			}
			return false, nil, ferr2
		}
		_ = path
		return true, nil, nil
	}
	if lastErr == nil {
		lastErr = errTransient
	}
	return false, nil, lastErr
}

func asPermanent(err error, out **permanent) bool {
	if pe, ok := err.(*permanentError); ok {
		*out = pe.p
		return true
	}
	return false
}

type permanentError struct{ p *permanent }

func (e *permanentError) Error() string { return e.p.msg }

// copyBody streams the response into the part file, reporting progress, and
// resets the stall timer whenever bytes arrive.
func (s *Service) copyBody(ctx context.Context, f *os.File, body io.Reader, t store.Transfer, base int64, stall *time.Timer) (int64, error) {
	buf := make([]byte, copyBuffer)
	var n int64
	for {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		if err := s.chunkHook(t.ID, base+n); err != nil {
			return n, err
		}
		m, rerr := body.Read(buf)
		if m > 0 {
			stall.Reset(s.timing.Stall)
			if base+n+int64(m) > t.Size {
				return n, &permanentError{&permanent{string(proto.CodeHashMismatch), "the sender sent more than it announced"}}
			}
			if _, werr := f.Write(buf[:m]); werr != nil {
				return n, &permanentError{&permanent{string(proto.CodeNoSpace), werr.Error()}}
			}
			n += int64(m)
			s.progress(t.ID, base+n, t.Size)
		}
		if rerr == io.EOF {
			return n, nil
		}
		if rerr != nil {
			return n, rerr
		}
	}
}

// finalize verifies the assembled file against the announced SHA-256 and moves it
// to its final name. A mismatch deletes the part file.
func (s *Service) finalize(t store.Transfer) (string, error) {
	part := partPath(t)
	if t.Size == 0 {
		f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return "", &permanentError{&permanent{string(proto.CodeNoSpace), err.Error()}}
		}
		_ = f.Close()
	}
	f, err := os.Open(part)
	if err != nil {
		return "", err
	}
	sum, n, herr := hashReader(s.ctx, f)
	_ = f.Close()
	if herr != nil {
		return "", herr
	}
	if n != t.Size || sum != t.SHA256 {
		_ = os.Remove(part)
		return "", &permanentError{&permanent{string(proto.CodeHashMismatch), "the file does not match its checksum"}}
	}
	final, err := UniquePath(t.SaveDir, t.Name)
	if err != nil {
		return "", &permanentError{&permanent{string(proto.CodeNoSpace), err.Error()}}
	}
	if err := os.Rename(part, final); err != nil {
		_ = os.Remove(final)
		return "", err
	}
	t.Path, t.Offset = final, t.Size
	_ = s.db.SetTransferPath(t.ID, final)
	_ = s.db.SetTransferOffset(t.ID, t.Size)
	_ = s.db.SetTransferState(t.ID, StateDone, "")
	s.emit(map[string]any{"type": "file.progress", "transfer_id": t.ID, "offset": t.Size, "size": t.Size, "rate_bps": s.rate(t.ID)})
	s.forgetMeter(t.ID)
	s.emit(map[string]any{"type": "file.done", "transfer_id": t.ID, "direction": DirIn, "path": final, "name": filepath.Base(final),
		"size": t.Size, "sha256": t.SHA256})
	s.emit(map[string]any{"type": "file.state", "transfer_id": t.ID, "state": StateDone})
	return final, nil
}

// ---- expiry ------------------------------------------------------------------

func (s *Service) sweepLoop() {
	defer s.wg.Done()
	tick := time.NewTicker(s.timing.Sweep)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
			s.sweep()
		}
	}
}

func (s *Service) sweep() {
	expired, err := s.db.ExpireTransfers(time.Now().Unix())
	if err != nil {
		return
	}
	for _, t := range expired {
		s.stopTask(t.ID)
		s.dropSource(t.ID)
		if t.Direction == DirIn && !s.hasTask(t.ID) {
			s.removePart(t)
		}
		s.emit(map[string]any{"type": "file.failed", "transfer_id": t.ID, "code": string(proto.CodeExpired), "message": "the offer expired", "direction": t.Direction})
		s.emit(map[string]any{"type": "file.state", "transfer_id": t.ID, "state": StateExpired})
	}
}
