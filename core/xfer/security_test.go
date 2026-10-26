package xfer

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Security review: the user cancels an incoming (push) transfer while the peer's PUT is in flight.
// The PUT is not a cancellable task and finalize() never re-checks the state, so the
// file still lands in the save dir and the row flips back to done.
func TestRegressionCancelDuringInFlightPutIsHonoured(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	data := randBytes(t, 400_000)
	src := writeFile(t, t.TempDir(), "u.bin", data)
	id, _ := r.pix.svc.Offer(r.win.ID.DeviceID, OfferSource{Path: src})
	r.win.offered(t, id)
	hc, base := rawClient(t, r.pix.ID, r.win)
	holdBack(r.pix.svc)
	saveDir := t.TempDir()
	if err := r.win.svc.Accept(id, saveDir); err != nil {
		t.Fatal(err)
	}

	pr, pw := io.Pipe()
	req, _ := http.NewRequest("PUT", base+"/v1/files/"+id, pr)
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(data)-1, len(data)))
	done := make(chan *http.Response, 1)
	go func() { resp, _ := hc.Do(req); done <- resp }()
	_, _ = pw.Write(data[:100_000])
	time.Sleep(300 * time.Millisecond)

	if err := r.win.svc.Cancel(id); err != nil { // the user presses "cancel" on the receiving PC
		t.Fatal(err)
	}
	_, _ = pw.Write(data[100_000:]) // the (revoked/cancelled) peer simply keeps uploading
	pw.Close()
	resp := <-done
	if resp != nil {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Logf("PUT after cancel answered %d %s", resp.StatusCode, b)
	}
	time.Sleep(300 * time.Millisecond)
	tr, _, _ := r.win.DB.GetTransfer(id)
	files, _ := filepath.Glob(filepath.Join(saveDir, "*"))
	if tr.State != StateCancelled || len(files) != 0 {
		t.Fatalf("state=%s files=%v: a cancelled transfer was completed anyway", tr.State, files)
	}
}

// Security review: an unsatisfiable Range makes the sender believe the file was delivered.
func TestRegressionBogusRangeDoesNotCompleteTheSend(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	data := randBytes(t, 100_000)
	src := writeFile(t, t.TempDir(), "r.bin", data)
	id, _ := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	r.pix.offered(t, id)
	hc, base := rawClient(t, r.pix.ID, r.win)
	req, _ := http.NewRequest("GET", base+"/v1/files/"+id, nil)
	req.Header.Set("Range", "bytes=999999999-")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	t.Logf("status %d", resp.StatusCode)
	time.Sleep(200 * time.Millisecond)
	tr, _, _ := r.win.DB.GetTransfer(id)
	if tr.State == StateDone {
		t.Fatalf("sender marked the transfer done after a 416 that delivered no file bytes")
	}
}

// Security review: a push whose final rename failed (part file complete, state transferring)
// can never be retried: the sender's next PUT has start == size and is refused with 400.
func TestRegressionCompletePartFileCanBeFinalisedByARetry(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	data := randBytes(t, 50_000)
	src := writeFile(t, t.TempDir(), "u.bin", data)
	id, _ := r.pix.svc.Offer(r.win.ID.DeviceID, OfferSource{Path: src})
	r.win.offered(t, id)
	holdBack(r.pix.svc)
	saveDir := t.TempDir()
	if err := r.win.svc.Accept(id, saveDir); err != nil {
		t.Fatal(err)
	}
	tr, _, _ := r.win.DB.GetTransfer(id)
	// state left behind by a PUT whose finalize() returned a non-permanent error (e.g. Rename blocked by AV)
	if err := os.WriteFile(partPath(tr), data, 0o600); err != nil {
		t.Fatal(err)
	}
	st, _, _ := r.pix.DB.GetTransfer(id)
	done, perm, err := r.pix.svc.uploadOnce(context.Background(), st)
	if !done {
		t.Fatalf("retry cannot complete: done=%v perm=%v err=%v", done, perm, err)
	}
}

// Security review: Windows reserved device names.
func TestRegressionSafeNameWindowsReserved(t *testing.T) {
	for _, n := range []string{"CON.tar.gz", "nul.txt.bak", "aux.a.b", "CON .txt", "COM¹.txt", "LPT²", "CONIN$", "con..txt", "prn. .txt"} {
		got := SafeName(n)
		base := strings.ToUpper(strings.SplitN(got, ".", 2)[0])
		base = strings.TrimRight(base, " ")
		switch base {
		case "CON", "PRN", "AUX", "NUL", "COM1", "LPT1", "COM¹", "LPT²", "CONIN$":
			t.Errorf("SafeName(%q) = %q is still a Windows device name", n, got)
		}
	}
}

// Security review: pull sender marks the transfer "done" once it has *written* the last byte,
// then refuses every further GET (410). If the connection dies while those last bytes
// are still in flight, the receiver can never finish.
func TestRegressionDroppedTailOfAPullCanStillBeResumed(t *testing.T) {
	var broke bool
	r := newRig(t, Hooks{}, Hooks{Chunk: func(id string, off int64) error {
		if off == 0 && !broke {
			time.Sleep(700 * time.Millisecond) // receiver is slow to start reading; sender fills the socket buffers
		}
		if off >= 100_000 && !broke {
			broke = true
			return io.ErrUnexpectedEOF // link dies; bytes already counted as "sent" never arrive
		}
		return nil
	}})
	data := randBytes(t, 400_000)
	src := writeFile(t, t.TempDir(), "x.bin", data)
	id, _ := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	r.pix.offered(t, id)
	saveDir := t.TempDir()
	if err := r.pix.svc.Accept(id, saveDir); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	tr, _, _ := r.pix.DB.GetTransfer(id)
	st, _, _ := r.win.DB.GetTransfer(id)
	t.Logf("receiver state=%s err=%s ; sender state=%s", tr.State, tr.Error, st.State)
	if tr.State != StateDone {
		t.Fatalf("receiver could not finish: %s %s", tr.State, tr.Error)
	}
}
