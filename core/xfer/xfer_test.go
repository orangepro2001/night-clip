package xfer

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"nightclip/core/identity"
	"nightclip/core/internal/testnet"
	"nightclip/core/peer"
	"nightclip/core/proto"
)

var fastTiming = Timing{ProgressEvery: time.Millisecond, RetryBase: 20 * time.Millisecond, RetryMax: 100 * time.Millisecond, Stall: 5 * time.Second, Sweep: time.Hour}

// handler routes engine callbacks to the xfer service.
type handler struct{ s *Service }

func (h handler) OnEvent(p string, e *proto.Envelope) {
	switch e.Kind {
	case proto.KindFile:
		h.s.OnOffer(p, e)
	case proto.KindControl:
		h.s.OnControl(p, e)
	}
}
func (handler) OnAck(string, string, string, string) {}
func (handler) OnPeerState(string, string)           {}

type side struct {
	*testnet.Device
	svc *Service
	dir string // data dir
}

type rig struct {
	win, pix *side // win listens; pix dials
}

func newSide(t *testing.T, name, platform string, listen bool, clock *testnet.Clock, hooks Hooks) *side {
	t.Helper()
	d := testnet.New(t, name, platform, listen, clock, nil)
	dir := t.TempDir()
	svc := New(Config{Engine: d.Engine, DB: d.DB, DataDir: dir, Platform: "android", Emit: d.Emit, Timing: fastTiming, Hooks: hooks})
	d.Proxy.Set(handler{svc})
	t.Cleanup(svc.Close)
	return &side{Device: d, svc: svc, dir: dir}
}

func newRig(t *testing.T, winHooks, pixHooks Hooks) *rig {
	t.Helper()
	r := &rig{win: newSide(t, "win", "windows", true, nil, winHooks), pix: newSide(t, "pixel", "android", false, nil, pixHooks)}
	testnet.Connect(t, r.win.Device, r.pix.Device, "notify", "notify")
	r.win.svc.Start()
	r.pix.svc.Start()
	return r
}

func randBytes(t *testing.T, n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func (s *side) offered(t *testing.T, id string) map[string]any {
	t.Helper()
	var ev map[string]any
	testnet.WaitUntil(t, s.Name+" sees the offer", func() bool {
		ev = s.Find("file.offered", map[string]any{"transfer_id": id})
		return ev != nil
	})
	return ev
}

func (s *side) waitState(t *testing.T, id, want string) {
	t.Helper()
	testnet.WaitUntil(t, fmt.Sprintf("%s: %s is %s", s.Name, id, want), func() bool {
		tr, found, _ := s.DB.GetTransfer(id)
		return found && tr.State == want
	})
}

func (s *side) transfer(t *testing.T, id string) Info {
	t.Helper()
	list, err := s.svc.List(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range list {
		if i.ID == id {
			return i
		}
	}
	t.Fatalf("%s has no transfer %s", s.Name, id)
	return Info{}
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ---- happy paths ------------------------------------------------------------------

func TestPullWindowsToPhone(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	data := randBytes(t, 3<<20+123)
	src := writeFile(t, t.TempDir(), "holiday photo.jpg", data)

	id, err := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	if err != nil {
		t.Fatal(err)
	}
	ev := r.pix.offered(t, id)
	if ev["name"] != "holiday photo.jpg" || fmt.Sprint(ev["size"]) != strconv.Itoa(len(data)) || ev["sha256"] != sum(data) ||
		ev["from_name"] != "win" || ev["mime_type"] != "image/jpeg" {
		t.Fatalf("file.offered = %v", ev)
	}
	// the offer is not accepted by itself: nothing moves
	time.Sleep(100 * time.Millisecond)
	if got := r.pix.transfer(t, id); got.State != StateOffered || got.Offset != 0 {
		t.Fatalf("before Accept = %+v", got)
	}
	if tr, _, _ := r.pix.DB.GetTransfer(id); tr.Mode != ModePull {
		t.Fatalf("the PC listens, so the phone must pull; mode = %s", tr.Mode)
	}

	saveDir := t.TempDir()
	if err := r.pix.svc.Accept(id, saveDir); err != nil {
		t.Fatal(err)
	}
	r.pix.waitState(t, id, StateDone)
	r.win.waitState(t, id, StateDone)

	done := r.pix.Find("file.done", map[string]any{"transfer_id": id})
	final := filepath.Join(saveDir, "holiday photo.jpg")
	if done == nil || done["path"] != final || done["direction"] != "in" {
		t.Fatalf("file.done = %v", done)
	}
	if !bytes.Equal(readFile(t, final), data) {
		t.Fatal("received bytes differ")
	}
	if _, err := os.Stat(filepath.Join(saveDir, "."+id+".part")); !os.IsNotExist(err) {
		t.Fatal("the part file was left behind")
	}
	if r.win.Find("file.done", map[string]any{"transfer_id": id, "direction": "out"}) == nil {
		t.Fatal("the sender was not told it was delivered")
	}
	if r.pix.Count("file.progress") == 0 {
		t.Fatal("no progress events")
	}
	if got := r.pix.transfer(t, id); got.Path != final || got.Offset != int64(len(data)) {
		t.Fatalf("info = %+v", got)
	}
}

func TestPushPhoneToWindowsFromAFileDescriptor(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	data := randBytes(t, 2<<20)
	f, err := os.Open(writeFile(t, t.TempDir(), "src.bin", data))
	if err != nil {
		t.Fatal(err)
	}
	fd := dupFD(t, f) // the core takes ownership of what it is given
	f.Close()

	id, err := r.pix.svc.Offer(r.win.ID.DeviceID, OfferSource{FD: fd, Name: "from the phone.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if tr, _, _ := r.pix.DB.GetTransfer(id); tr.Mode != ModePush || tr.Path != "" {
		t.Fatalf("the phone dials, so it pushes; transfer = %+v", tr)
	}
	r.win.offered(t, id)
	saveDir := t.TempDir()
	if err := r.win.svc.Accept(id, saveDir); err != nil {
		t.Fatal(err)
	}
	r.win.waitState(t, id, StateDone)
	r.pix.waitState(t, id, StateDone)
	if !bytes.Equal(readFile(t, filepath.Join(saveDir, "from the phone.bin")), data) {
		t.Fatal("received bytes differ")
	}
	// the descriptor is released once the transfer is over
	r.pix.svc.mu.Lock()
	n := len(r.pix.svc.sources)
	r.pix.svc.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d descriptors still held", n)
	}
}

func TestANonSeekableSourceIsSpooledToDisk(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	data := randBytes(t, 700_000)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = pw.Write(data); pw.Close() }()
	id, err := r.pix.svc.Offer(r.win.ID.DeviceID, OfferSource{FD: dupFD(t, pr), Name: "streamed.bin"})
	pr.Close()
	if err != nil {
		t.Fatal(err)
	}
	tr, _, _ := r.pix.DB.GetTransfer(id)
	if tr.Path == "" || !strings.HasPrefix(tr.Path, filepath.Join(r.pix.dir, "outgoing")) || tr.Size != int64(len(data)) {
		t.Fatalf("spooled transfer = %+v", tr)
	}
	r.win.offered(t, id)
	saveDir := t.TempDir()
	if err := r.win.svc.Accept(id, saveDir); err != nil {
		t.Fatal(err)
	}
	r.win.waitState(t, id, StateDone)
	if !bytes.Equal(readFile(t, filepath.Join(saveDir, "streamed.bin")), data) {
		t.Fatal("received bytes differ")
	}
	r.pix.waitState(t, id, StateDone)
	if _, err := os.Stat(tr.Path); !os.IsNotExist(err) {
		t.Fatal("the spooled copy was not removed after the transfer")
	}
}

func TestEmptyFilesMoveInBothModes(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	for _, c := range []struct {
		from, to *side
	}{{r.win, r.pix}, {r.pix, r.win}} {
		src := writeFile(t, t.TempDir(), "empty.txt", nil)
		id, err := c.from.svc.Offer(c.to.ID.DeviceID, OfferSource{Path: src})
		if err != nil {
			t.Fatal(err)
		}
		c.to.offered(t, id)
		saveDir := t.TempDir()
		if err := c.to.svc.Accept(id, saveDir); err != nil {
			t.Fatal(err)
		}
		c.to.waitState(t, id, StateDone)
		c.from.waitState(t, id, StateDone)
		if st, err := os.Stat(filepath.Join(saveDir, "empty.txt")); err != nil || st.Size() != 0 {
			t.Fatalf("%s -> %s: %v %v", c.from.Name, c.to.Name, st, err)
		}
	}
}

func TestNamesFromThePeerAreCleanedAndNeverOverwrite(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	saveDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(saveDir, "evil.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := writeFile(t, t.TempDir(), "x.bin", []byte("payload"))
	id, err := r.pix.svc.Offer(r.win.ID.DeviceID, OfferSource{Path: src, Name: `..\..\evil.txt`})
	if err != nil {
		t.Fatal(err)
	}
	ev := r.win.offered(t, id)
	if ev["name"] != "evil.txt" {
		t.Fatalf("offered name = %v", ev["name"])
	}
	if err := r.win.svc.Accept(id, saveDir); err != nil {
		t.Fatal(err)
	}
	r.win.waitState(t, id, StateDone)
	if string(readFile(t, filepath.Join(saveDir, "evil.txt"))) != "mine" {
		t.Fatal("an existing file was overwritten")
	}
	if string(readFile(t, filepath.Join(saveDir, "evil (1).txt"))) != "payload" {
		t.Fatal("the received file is not where it should be")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(saveDir), "evil.txt")); err == nil {
		t.Fatal("the file escaped the save directory")
	}
}

// ---- refusals and cancellation -----------------------------------------------------

func TestRejectingAnOfferTellsTheSender(t *testing.T) {
	for _, dir := range []string{"pull", "push"} {
		t.Run(dir, func(t *testing.T) {
			r := newRig(t, Hooks{}, Hooks{})
			from, to := r.win, r.pix
			if dir == "push" {
				from, to = r.pix, r.win
			}
			src := writeFile(t, t.TempDir(), "a.bin", randBytes(t, 1000))
			id, _ := from.svc.Offer(to.ID.DeviceID, OfferSource{Path: src})
			to.offered(t, id)
			if err := to.svc.Reject(id); err != nil {
				t.Fatal(err)
			}
			from.waitState(t, id, StateRejected)
			if from.Find("file.failed", map[string]any{"transfer_id": id, "code": "REJECTED"}) == nil {
				t.Fatal("the sender's shell was not told")
			}
			if err := to.svc.Accept(id, ""); err == nil {
				t.Fatal("accepting a rejected transfer succeeded")
			}
		})
	}
}

func TestOffersAboveTheLimitAreRefusedAutomatically(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	if err := r.pix.DB.SetSetting(SettingMaxFileBytes, strconv.Itoa(1<<20)); err != nil {
		t.Fatal(err)
	}
	src := writeFile(t, t.TempDir(), "big.bin", randBytes(t, 2<<20))
	id, _ := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	r.win.waitState(t, id, StateRejected)
	if _, found, _ := r.pix.DB.GetTransfer(id); found {
		t.Fatal("an oversized offer was stored")
	}
	// the sender enforces its own limit before even hashing
	if err := r.win.DB.SetSetting(SettingMaxFileBytes, strconv.Itoa(1<<20)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src}); err == nil {
		t.Fatal("offered a file above the sender's own limit")
	}
}

func TestCancellingMidTransferStopsBothSides(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	reached := make(chan struct{})
	r := newRig(t, Hooks{}, Hooks{Chunk: func(id string, off int64) error {
		if off >= 512<<10 {
			once.Do(func() { close(reached) })
			<-gate
		}
		return nil
	}})
	src := writeFile(t, t.TempDir(), "big.bin", randBytes(t, 8<<20))
	id, _ := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	r.pix.offered(t, id)
	saveDir := t.TempDir()
	if err := r.pix.svc.Accept(id, saveDir); err != nil {
		t.Fatal(err)
	}
	<-reached
	if err := r.pix.svc.Cancel(id); err != nil {
		t.Fatal(err)
	}
	close(gate)
	r.pix.waitState(t, id, StateCancelled)
	r.win.waitState(t, id, StateCancelled)
	time.Sleep(100 * time.Millisecond)
	if matches, _ := filepath.Glob(filepath.Join(saveDir, "*")); len(matches) != 0 {
		t.Fatalf("cancelled download left files behind: %v", matches)
	}
	if err := r.pix.svc.Cancel(id); err == nil {
		t.Fatal("cancelling a finished transfer succeeded")
	}
	if got := r.win.transfer(t, id); got.Error != "CANCELLED" {
		t.Fatalf("sender info = %+v", got)
	}
}

func TestTheSenderCanCancelToo(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	reached := make(chan struct{})
	r := newRig(t, Hooks{Chunk: func(id string, off int64) error {
		if off >= 512<<10 {
			once.Do(func() { close(reached) })
			<-gate
		}
		return nil
	}}, Hooks{})
	src := writeFile(t, t.TempDir(), "big.bin", randBytes(t, 8<<20))
	id, _ := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	r.pix.offered(t, id)
	if err := r.pix.svc.Accept(id, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	<-reached
	if err := r.win.svc.Cancel(id); err != nil {
		t.Fatal(err)
	}
	close(gate)
	r.win.waitState(t, id, StateCancelled)
	r.pix.waitState(t, id, StateCancelled)
}

// ---- integrity and resilience --------------------------------------------------------

func TestACorruptedFileIsDetectedDeletedAndReported(t *testing.T) {
	for _, dir := range []string{"pull", "push"} {
		t.Run(dir, func(t *testing.T) {
			r := newRig(t, Hooks{}, Hooks{})
			from, to := r.win, r.pix
			if dir == "push" {
				from, to = r.pix, r.win
			}
			data := randBytes(t, 1<<20)
			src := writeFile(t, t.TempDir(), "doc.bin", data)
			id, _ := from.svc.Offer(to.ID.DeviceID, OfferSource{Path: src})
			to.offered(t, id)
			// the file changes (same size) after it was hashed and offered
			data[len(data)/2] ^= 0xff
			if err := os.WriteFile(src, data, 0o644); err != nil {
				t.Fatal(err)
			}
			saveDir := t.TempDir()
			if err := to.svc.Accept(id, saveDir); err != nil {
				t.Fatal(err)
			}
			to.waitState(t, id, StateFailed)
			if tr, _, _ := to.DB.GetTransfer(id); tr.Error != "HASH_MISMATCH" {
				t.Fatalf("receiver error = %q", tr.Error)
			}
			if matches, _ := filepath.Glob(filepath.Join(saveDir, "*")); len(matches) != 0 {
				t.Fatalf("an unverified file was kept: %v", matches)
			}
			if got := filepath.Join(saveDir, ".*"); got != "" {
				if hidden, _ := filepath.Glob(got); len(hidden) != 0 {
					t.Fatalf("part file left: %v", hidden)
				}
			}
			from.waitState(t, id, StateFailed)
			if from.Find("file.failed", map[string]any{"transfer_id": id, "code": "HASH_MISMATCH"}) == nil {
				t.Fatal("the sender was not told that verification failed")
			}
		})
	}
}

func TestADownloadResumesAfterTheConnectionBreaks(t *testing.T) {
	var (
		mu       sync.Mutex
		seen     []int64 // every offset the receive loop was about to read at
		breakIdx = -1
	)
	r := newRig(t, Hooks{}, Hooks{Chunk: func(id string, off int64) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, off)
		if off >= 1<<20 && breakIdx < 0 {
			breakIdx = len(seen) - 1
			return io.ErrUnexpectedEOF // the connection drops here
		}
		return nil
	}})
	data := randBytes(t, 6<<20)
	src := writeFile(t, t.TempDir(), "movie.bin", data)
	id, _ := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	r.pix.offered(t, id)
	saveDir := t.TempDir()
	if err := r.pix.svc.Accept(id, saveDir); err != nil {
		t.Fatal(err)
	}
	r.pix.waitState(t, id, StateDone)
	if !bytes.Equal(readFile(t, filepath.Join(saveDir, "movie.bin")), data) {
		t.Fatal("the resumed file differs")
	}
	mu.Lock()
	defer mu.Unlock()
	if breakIdx < 0 {
		t.Fatal("the test never broke the connection")
	}
	// the first read after the break starts exactly where the data on disk ended
	// (>= 1 MiB), not at byte 0
	if resumed := seen[breakIdx+1]; resumed < 1<<20 || resumed != seen[breakIdx] {
		t.Fatalf("after the break the download continued at %d (broke at %d); it must resume from the part file", resumed, seen[breakIdx])
	}
}

func TestAnUploadResumesAfterTheConnectionBreaks(t *testing.T) {
	var aborted atomic.Bool
	r := newRig(t, Hooks{}, Hooks{Chunk: func(id string, off int64) error {
		if off >= 1<<20 && aborted.CompareAndSwap(false, true) {
			return io.ErrUnexpectedEOF
		}
		return nil
	}})
	data := randBytes(t, 5<<20)
	src := writeFile(t, t.TempDir(), "up.bin", data)
	id, _ := r.pix.svc.Offer(r.win.ID.DeviceID, OfferSource{Path: src})
	r.win.offered(t, id)
	saveDir := t.TempDir()
	if err := r.win.svc.Accept(id, saveDir); err != nil {
		t.Fatal(err)
	}
	r.win.waitState(t, id, StateDone)
	r.pix.waitState(t, id, StateDone)
	if !aborted.Load() {
		t.Fatal("the test never broke the connection")
	}
	if !bytes.Equal(readFile(t, filepath.Join(saveDir, "up.bin")), data) {
		t.Fatal("the resumed upload differs")
	}
}

func TestStartResumesADownloadFromItsPartFile(t *testing.T) {
	// Build the state a crash would leave: a sender with the file offered, a receiver
	// with an accepted transfer and the first part of the data on disk.
	r := &rig{win: newSide(t, "win", "windows", true, nil, Hooks{}), pix: newSide(t, "pixel", "android", false, nil, Hooks{})}
	testnet.Connect(t, r.win.Device, r.pix.Device, "notify", "notify")
	r.win.svc.Start()

	data := randBytes(t, 3<<20)
	src := writeFile(t, t.TempDir(), "big.bin", data)
	id, _ := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	r.pix.offered(t, id)
	r.win.waitState(t, id, StateOffered)

	saveDir := t.TempDir()
	tr, _, _ := r.pix.DB.GetTransfer(id)
	tr.State, tr.SaveDir = StateTransferring, saveDir
	if err := r.pix.DB.PutTransfer(tr); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partPath(tr), data[:1<<20+7], 0o600); err != nil {
		t.Fatal(err)
	}
	r.pix.svc.Start() // the "restart"
	r.pix.waitState(t, id, StateDone)
	if !bytes.Equal(readFile(t, filepath.Join(saveDir, "big.bin")), data) {
		t.Fatal("the file resumed after a restart differs")
	}
}

func TestADescriptorBackedSendCannotResumeAfterARestart(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	f, _ := os.Open(writeFile(t, t.TempDir(), "s.bin", randBytes(t, 4096)))
	id, err := r.pix.svc.Offer(r.win.ID.DeviceID, OfferSource{FD: dupFD(t, f), Name: "s.bin"})
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	r.pix.waitState(t, id, StateOffered)
	// simulate a restart: the in-memory descriptor is gone
	r.pix.svc.mu.Lock()
	for k, v := range r.pix.svc.sources {
		_ = v.Close()
		delete(r.pix.svc.sources, k)
	}
	r.pix.svc.started = false
	r.pix.svc.mu.Unlock()
	r.pix.svc.Start()
	r.pix.waitState(t, id, StateFailed)
	if tr, _, _ := r.pix.DB.GetTransfer(id); tr.Error != "SOURCE_LOST" {
		t.Fatalf("error = %q", tr.Error)
	}
}

func TestExpiredOffersCannotBeAcceptedAndAreSwept(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	src := writeFile(t, t.TempDir(), "a.bin", randBytes(t, 1000))
	id, _ := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	r.pix.offered(t, id)
	tr, _, _ := r.pix.DB.GetTransfer(id)
	tr.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	_ = r.pix.DB.PutTransfer(tr)
	if err := r.pix.svc.Accept(id, t.TempDir()); err == nil || !strings.Contains(err.Error(), "EXPIRED") {
		t.Fatalf("Accept of an expired offer = %v", err)
	}

	// the sender's own copy expires through the sweeper
	st, _, _ := r.win.DB.GetTransfer(id)
	st.ExpiresAt = time.Now().Add(-time.Second).Unix()
	_ = r.win.DB.PutTransfer(st)
	r.win.svc.sweep()
	if got, _, _ := r.win.DB.GetTransfer(id); got.State != StateExpired {
		t.Fatalf("state = %s", got.State)
	}
	if r.win.Find("file.failed", map[string]any{"transfer_id": id, "code": "EXPIRED"}) == nil {
		t.Fatal("no file.failed for the expired transfer")
	}
}

// ---- the HTTP surface ------------------------------------------------------------------

// rawClient talks to a side's server as another identity.
func rawClient(t *testing.T, as *identity.Identity, to *side) (*http.Client, string) {
	t.Helper()
	return peer.HTTPClient(peer.ClientTLS(as, to.ID.Fingerprint()), time.Second),
		"https://127.0.0.1:" + strconv.Itoa(to.Engine.ListenPort())
}

func TestOnlyTheCounterpartyMayFetchOrUpload(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	data := randBytes(t, 200_000)
	src := writeFile(t, t.TempDir(), "private.bin", data)
	id, _ := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	r.pix.offered(t, id)
	r.win.waitState(t, id, StateOffered)

	// a third device that is paired with the PC, but was not offered this file
	other := newSide(t, "tablet", "android", false, nil, Hooks{})
	r.win.Know(other.Device, 0, "notify")
	hc, base := rawClient(t, other.ID, r.win)
	for _, c := range []struct{ method, url string }{{"GET", "/v1/files/" + id}, {"HEAD", "/v1/files/" + id}, {"PUT", "/v1/files/" + id}} {
		req, _ := http.NewRequest(c.method, base+c.url, strings.NewReader("x"))
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Errorf("%s by an unrelated paired device = %d, want 404 (it must not learn the transfer exists)", c.method, resp.StatusCode)
		}
	}
	// an unpaired certificate is turned away by the server itself
	stranger, _ := identity.Generate("stranger")
	hc2, _ := rawClient(t, stranger, r.win)
	resp, err := hc2.Get(base + "/v1/files/" + id)
	if err != nil || resp.StatusCode != 403 {
		t.Fatalf("stranger GET = %v / %v, want 403", resp, err)
	}
	resp.Body.Close()
	// unknown ids
	hc3, base3 := rawClient(t, r.pix.ID, r.win)
	resp, _ = hc3.Get(base3 + "/v1/files/tr_0000000000000000")
	if resp.StatusCode != 404 {
		t.Fatalf("unknown id = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRangeRequestsAndHeadOnTheSenderSide(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	data := randBytes(t, 100_000)
	src := writeFile(t, t.TempDir(), "r.bin", data)
	id, _ := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	r.pix.offered(t, id)
	hc, base := rawClient(t, r.pix.ID, r.win)

	req, _ := http.NewRequest("GET", base+"/v1/files/"+id, nil)
	req.Header.Set("Range", "bytes=99000-")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || !bytes.Equal(got, data[99000:]) || resp.Header.Get("Content-Range") != "bytes 99000-99999/100000" {
		t.Fatalf("range = %d %q (%d bytes)", resp.StatusCode, resp.Header.Get("Content-Range"), len(got))
	}
	if resp.Header.Get("ETag") != `"`+sum(data)+`"` {
		t.Fatalf("ETag = %q", resp.Header.Get("ETag"))
	}
	hreq, _ := http.NewRequest("HEAD", base+"/v1/files/"+id, nil)
	hresp, err := hc.Do(hreq)
	if err != nil || hresp.StatusCode != 200 || hresp.Header.Get("X-Size") != "100000" || hresp.Header.Get("X-State") == "" {
		t.Fatalf("HEAD = %v / %v", hresp, err)
	}
	hresp.Body.Close()
}

func TestUploadProtocolViolationsAreRefused(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	data := randBytes(t, 50_000)
	src := writeFile(t, t.TempDir(), "u.bin", data)
	id, _ := r.pix.svc.Offer(r.win.ID.DeviceID, OfferSource{Path: src})
	r.win.offered(t, id)
	hc, base := rawClient(t, r.pix.ID, r.win)
	put := func(contentRange string, body []byte) (int, map[string]any) {
		req, _ := http.NewRequest("PUT", base+"/v1/files/"+id, bytes.NewReader(body))
		if contentRange != "" {
			req.Header.Set("Content-Range", contentRange)
		}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}

	// not accepted yet
	if status, _ := put("bytes 0-49999/50000", data); status != 409 {
		t.Fatalf("PUT before accept = %d, want 409", status)
	}
	holdBack(r.pix.svc) // Accept would let the real sender upload at once; drive the protocol by hand instead
	if err := r.win.svc.Accept(id, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	if status, _ := put("", data); status != 400 {
		t.Fatalf("missing Content-Range = %d", status)
	}
	if status, _ := put("bytes 0-49999/99999", data); status != 400 {
		t.Fatalf("wrong total = %d", status)
	}
	if status, _ := put("bytes 0-100/50000", data); status != 400 {
		t.Fatalf("body length that does not match the range = %d", status)
	}
	cur := partSizeOf(r.win, id)
	status, m := put("bytes 1000-49999/50000", data[1000:])
	if status != 409 || int64(m["offset"].(float64)) != cur {
		t.Fatalf("wrong start = %d %v, want 409 reporting offset %d", status, m, cur)
	}
	// the genuine rest completes it
	status, m = put(fmt.Sprintf("bytes %d-49999/50000", cur), data[cur:])
	if status != 200 || m["state"] != "complete" {
		t.Fatalf("final PUT = %d %v", status, m)
	}
	r.win.waitState(t, id, StateDone)
}

// holdBack stops a service from starting any further transfer goroutine.
func holdBack(s *Service) {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

func partSizeOf(s *side, id string) int64 {
	tr, _, _ := s.DB.GetTransfer(id)
	return partSize(tr)
}

func TestAWrongUploadIsRejectedWith422AndDeleted(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	data := randBytes(t, 30_000)
	src := writeFile(t, t.TempDir(), "w.bin", data)
	id, _ := r.pix.svc.Offer(r.win.ID.DeviceID, OfferSource{Path: src})
	r.win.offered(t, id)
	saveDir := t.TempDir()
	holdBack(r.pix.svc) // the hand-made upload must be the only one
	if err := r.win.svc.Accept(id, saveDir); err != nil {
		t.Fatal(err)
	}
	hc, base := rawClient(t, r.pix.ID, r.win)
	bad := randBytes(t, 30_000)
	req, _ := http.NewRequest("PUT", base+"/v1/files/"+id, bytes.NewReader(bad))
	req.Header.Set("Content-Range", "bytes 0-29999/30000")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 422 {
		t.Fatalf("a wrong upload = %d, want 422", resp.StatusCode)
	}
	r.win.waitState(t, id, StateFailed)
	if m, _ := filepath.Glob(filepath.Join(saveDir, "*")); len(m) != 0 {
		t.Fatalf("unverified data kept: %v", m)
	}
}

func TestSettingsValidationAndDefaults(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	if got := r.pix.svc.MaxFileBytes(); got != 4<<30 {
		t.Fatalf("default max = %d", got)
	}
	if got := r.pix.svc.DownloadDir(); got != filepath.Join(r.pix.dir, "received") {
		t.Fatalf("android default dir = %s", got)
	}
	_ = r.pix.DB.SetSetting(SettingDownloadDir, "/somewhere/else")
	if got := r.pix.svc.DownloadDir(); got != "/somewhere/else" {
		t.Fatalf("configured dir = %s", got)
	}
	_ = r.pix.DB.SetSetting(SettingMaxFileBytes, "garbage")
	if got := r.pix.svc.MaxFileBytes(); got != 4<<30 {
		t.Fatalf("garbage max must fall back, got %d", got)
	}
	for _, c := range []struct {
		k, v string
		ok   bool
	}{
		{SettingDownloadDir, "/abs/dir", true}, {SettingDownloadDir, "relative", false},
		{SettingMaxFileBytes, "1048576", true}, {SettingMaxFileBytes, "10", false}, {SettingMaxFileBytes, "x", false}, {"nope", "1", false},
	} {
		if err := ValidateSetting(c.k, c.v); (err == nil) != c.ok {
			t.Errorf("ValidateSetting(%s,%s) = %v", c.k, c.v, err)
		}
	}
}

func TestOfferValidation(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	for name, src := range map[string]OfferSource{
		"relative path": {Path: "relative.txt"},
		"missing":       {Path: filepath.Join(t.TempDir(), "nope")},
		"a directory":   {Path: t.TempDir()},
		"nothing":       {},
	} {
		if _, err := r.win.svc.Offer(r.pix.ID.DeviceID, src); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	f := writeFile(t, t.TempDir(), "x", []byte("x"))
	if _, err := r.win.svc.Offer("dev_"+strings.Repeat("a", 26), OfferSource{Path: f}); err == nil {
		t.Error("offered to an unknown device")
	}
	if err := r.win.svc.Accept("tr_0000000000000000", ""); err == nil {
		t.Error("accepted an unknown transfer")
	}
	if err := r.win.svc.Release("tr_0000000000000000"); err == nil {
		t.Error("released an unknown transfer")
	}
}

func TestReleaseDeletesThePrivateCopy(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	src := writeFile(t, t.TempDir(), "r.bin", randBytes(t, 5000))
	id, _ := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	r.pix.offered(t, id)
	if err := r.pix.svc.Release(id); err == nil {
		t.Fatal("released an unfinished transfer")
	}
	if err := r.pix.svc.Accept(id, ""); err != nil { // default dir on android: <data>/received
		t.Fatal(err)
	}
	r.pix.waitState(t, id, StateDone)
	tr, _, _ := r.pix.DB.GetTransfer(id)
	if !strings.HasPrefix(tr.Path, filepath.Join(r.pix.dir, "received")) {
		t.Fatalf("path = %s", tr.Path)
	}
	if err := r.pix.svc.Release(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tr.Path); !os.IsNotExist(err) {
		t.Fatal("the private copy still exists")
	}
	if got := r.pix.transfer(t, id); got.State != StateDone || got.Path != "" {
		t.Fatalf("after release = %+v", got)
	}
}

func TestListReportsNamesAndRates(t *testing.T) {
	r := newRig(t, Hooks{}, Hooks{})
	src := writeFile(t, t.TempDir(), "l.bin", randBytes(t, 1<<20))
	id, _ := r.win.svc.Offer(r.pix.ID.DeviceID, OfferSource{Path: src})
	r.pix.offered(t, id)
	list, err := r.pix.svc.List(0)
	if err != nil || len(list) != 1 || list[0].PeerName != "win" || list[0].Direction != "in" || list[0].Name != "l.bin" {
		t.Fatalf("List = %+v, %v", list, err)
	}
	if one, _ := r.pix.svc.List(1); len(one) != 1 {
		t.Fatal("limit ignored")
	}
}
