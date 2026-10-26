package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nightclip/core/pipe"
)

// fakeCore answers control-channel requests from a script and records them.
type fakeCore struct {
	mu      sync.Mutex
	replies map[string]string
	calls   []call
}

type call struct {
	Method string
	Params map[string]any
}

func (f *fakeCore) Call(method, params string) string {
	var p map[string]any
	_ = json.Unmarshal([]byte(params), &p)
	f.mu.Lock()
	f.calls = append(f.calls, call{method, p})
	r, ok := f.replies[method]
	f.mu.Unlock()
	if !ok {
		return `{"ok":false,"error":{"code":"METHOD_NOT_FOUND","message":"no such method"}}`
	}
	return r
}

func (f *fakeCore) last(method string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].Method == method {
			return f.calls[i].Params
		}
	}
	return nil
}

func (f *fakeCore) count(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Method == method {
			n++
		}
	}
	return n
}

type result struct {
	code        int
	out, errOut string
}

func run(t *testing.T, f *fakeCore, stdin string, args ...string) result {
	t.Helper()
	srv := &pipe.Server{Call: f.Call}
	var stdout, stderr bytes.Buffer
	code := Run(Env{
		Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr, Sleep: func(time.Duration) {},
		Dial: func() (io.ReadWriteCloser, error) {
			a, b := net.Pipe()
			go srv.ServeConn(b)
			return a, nil
		},
	}, args)
	srv.Close()
	return result{code, stdout.String(), stderr.String()}
}

const dev1 = "dev_aaaaaaaaaaaaaaaaaaaaaaaaaa"
const dev2 = "dev_bbbbbbbbbbbbbbbbbbbbbbbbbb"

func twoPeers() string {
	return `{"ok":true,"result":{"peers":[
		{"device_id":"` + dev1 + `","name":"Pixel 8","platform":"android","state":"connected","outbox_pending":0},
		{"device_id":"` + dev2 + `","name":"Tablet","platform":"android","state":"offline","outbox_pending":3}]}}`
}

func TestVersionNeedsNoServer(t *testing.T) {
	var out bytes.Buffer
	code := Run(Env{Stdout: &out, Stderr: io.Discard, Dial: func() (io.ReadWriteCloser, error) { t.Fatal("dialled"); return nil, nil }}, []string{"version"})
	if code != ExitOK || !strings.Contains(out.String(), "clipbridge/v1") {
		t.Fatalf("version = %d %q", code, out.String())
	}
}

func TestNoServerIsExitCode3(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(Env{Stdout: io.Discard, Stderr: &stderr, Dial: func() (io.ReadWriteCloser, error) { return nil, os.ErrNotExist }}, []string{"status"})
	if code != ExitNoServer || !strings.Contains(stderr.String(), "not running") {
		t.Fatalf("code %d, stderr %q", code, stderr.String())
	}
	// --json keeps stdout machine readable even for errors
	var stdout bytes.Buffer
	code = Run(Env{Stdout: &stdout, Stderr: io.Discard, Dial: func() (io.ReadWriteCloser, error) { return nil, os.ErrNotExist }}, []string{"--json", "status"})
	var e struct {
		OK    bool
		Error struct{ Code string }
	}
	if code != ExitNoServer || json.Unmarshal(stdout.Bytes(), &e) != nil || e.OK || e.Error.Code != "NO_SERVER" {
		t.Fatalf("--json error: %d %q", code, stdout.String())
	}
}

func TestStatusAndPeersAndDoctor(t *testing.T) {
	f := &fakeCore{replies: map[string]string{
		"status": `{"ok":true,"result":{"device_id":"` + dev1 + `","device_name":"Desk","platform":"windows","listen":true,
			"listening_addrs":["100.64.0.5"],"listening_port":47821,"capture":"on","peers":[{"name":"Pixel 8","state":"connected","outbox_pending":0}]}}`,
		"peers.list": twoPeers(),
		"diagnostics.get": `{"ok":true,"result":{"core":"0.0.1","listen":true,"listening_addrs":["100.64.0.5"],"peers":[
			{"name":"Pixel 8","state":"connected"},{"name":"Tablet","state":"offline","outbox_pending":3}],"events_dropped":0}}`,
	}}
	r := run(t, f, "", "status")
	for _, want := range []string{"Desk (windows)", "100.64.0.5:47821", "sync:      on", "Pixel 8", "connected"} {
		if r.code != 0 || !strings.Contains(r.out, want) {
			t.Fatalf("status output lacks %q:\n%s", want, r.out)
		}
	}
	r = run(t, f, "", "peers")
	if r.code != 0 || !strings.Contains(r.out, "Tablet") || !strings.Contains(r.out, "queued 3") {
		t.Fatalf("peers:\n%s", r.out)
	}
	if r = run(t, f, "", "--json", "peers", "list"); !strings.HasPrefix(r.out, `{"peers"`) {
		t.Fatalf("--json peers = %q", r.out)
	}
	r = run(t, f, "", "doctor")
	if r.code != ExitOK || !strings.Contains(r.out, "WARN   Tablet is offline") || !strings.Contains(r.out, "ok     Pixel 8 is connected") {
		t.Fatalf("doctor (%d):\n%s", r.code, r.out)
	}
	f.replies["diagnostics.get"] = `{"ok":true,"result":{"core":"0.0.1","listen":true,"listening_addrs":[],"peers":[{"name":"Pixel 8","state":"unpaired"}]}}`
	r = run(t, f, "", "doctor")
	if r.code != ExitOffline || !strings.Contains(r.out, "no Tailscale address") || !strings.Contains(r.out, "pair again") {
		t.Fatalf("failing doctor (%d):\n%s", r.code, r.out)
	}
}

func TestClipSendResolvesPeersAndReadsStdin(t *testing.T) {
	f := &fakeCore{replies: map[string]string{
		"peers.list": twoPeers(),
		"clip.send":  `{"ok":true,"result":{"sent":[{"device_id":"` + dev1 + `","name":"Pixel 8","event_id":"e"}]}}`,
	}}
	r := run(t, f, "", "clip", "send", "--to", "pixel", "--text", "hello")
	if r.code != 0 || !strings.Contains(r.out, "sent to Pixel 8") {
		t.Fatalf("send (%d): %s %s", r.code, r.out, r.errOut)
	}
	p := f.last("clip.send")
	if p["text"] != "hello" || len(p["to"].([]any)) != 1 || p["to"].([]any)[0] != dev1 {
		t.Fatalf("clip.send params = %v", p)
	}
	// text from stdin, every device
	if r = run(t, f, "from a pipe\nsecond line\n", "clip", "send"); r.code != 0 {
		t.Fatalf("stdin send: %d %s", r.code, r.errOut)
	}
	if p := f.last("clip.send"); p["text"] != "from a pipe\nsecond line\n" || p["to"] != nil {
		t.Fatalf("stdin params = %v", p)
	}
	// ambiguous and unknown devices
	if r = run(t, f, "", "clip", "send", "--to", "t", "--text", "x"); r.code != 0 { // "t" matches only Tablet
		t.Fatalf("unique prefix: %d %s", r.code, r.errOut)
	}
	f.replies["peers.list"] = `{"ok":true,"result":{"peers":[{"device_id":"` + dev1 + `","name":"Pixel A","state":"connected"},{"device_id":"` + dev2 + `","name":"Pixel B","state":"connected"}]}}`
	if r = run(t, f, "", "clip", "send", "--to", "pixel", "--text", "x"); r.code != ExitUsage || !strings.Contains(r.errOut, "several") {
		t.Fatalf("ambiguous: %d %s", r.code, r.errOut)
	}
	if r = run(t, f, "", "clip", "send", "--text", "x"); r.code != 0 || f.last("clip.send")["to"] != nil {
		t.Fatalf("no --to means every device: %d %v", r.code, f.last("clip.send"))
	}
	if r = run(t, f, "", "clip", "send", "--to", "nokia", "--text", "x"); r.code != ExitNotPaired {
		t.Fatalf("unknown device: %d", r.code)
	}
	if r = run(t, f, "   \n", "clip", "send"); r.code != ExitUsage {
		t.Fatalf("empty stdin: %d", r.code)
	}
}

func TestErrorCodesMapToExitCodes(t *testing.T) {
	cases := map[string]int{
		"NOT_PAIRED": ExitNotPaired, "HASH_MISMATCH": ExitIntegrity, "NO_TAILNET": ExitOffline, "PEER_OFFLINE": ExitOffline,
		"EXPIRED": ExitGone, "NOT_FOUND": ExitGone, "RATE_LIMITED": ExitConflict, "INVALID": ExitUsage, "INTERNAL": ExitFailed,
	}
	for code, want := range cases {
		f := &fakeCore{replies: map[string]string{"transfers.list": `{"ok":false,"error":{"code":"` + code + `","message":"boom"}}`}}
		if r := run(t, f, "", "transfers"); r.code != want || !strings.Contains(r.errOut, "boom") {
			t.Errorf("%s -> exit %d (want %d), stderr %q", code, r.code, want, r.errOut)
		}
	}
}

func TestInboxListAndShow(t *testing.T) {
	f := &fakeCore{replies: map[string]string{
		"inbox.list": `{"ok":true,"result":{"items":[
			{"event_id":"e1","from_name":"Pixel","text":"hello   there\nworld","sensitive":false,"state":"received","received_at":1790000000},
			{"event_id":"e2","from_name":"Pixel","text":"P@ssw0rd!2024","sensitive":true,"state":"received","received_at":1790000100}]}}`,
		"inbox.get": `{"ok":true,"result":{"event_id":"e2","text":"","redacted":true,"sensitive":true}}`,
	}}
	r := run(t, f, "", "inbox", "list", "--limit", "5")
	if r.code != 0 || !strings.Contains(r.out, "hello there world") || strings.Contains(r.out, "P@ssw0rd") || !strings.Contains(r.out, "sensitive, hidden") {
		t.Fatalf("inbox list:\n%s", r.out)
	}
	if f.last("inbox.list")["limit"].(float64) != 5 {
		t.Fatal("limit not passed")
	}
	if r = run(t, f, "", "inbox", "show", "e2"); r.out != "" || r.code != 0 || !strings.Contains(r.errOut, "only shown in the ClipBridge window") {
		t.Fatalf("inbox show of a sensitive item = %q / %q", r.out, r.errOut)
	}
	for _, bad := range [][]string{{"inbox", "list", "--limit", "0"}, {"inbox", "list", "--limit", "x"}, {"inbox", "show"}, {"inbox", "nope"}} {
		if r = run(t, f, "", bad...); r.code != ExitUsage {
			t.Errorf("%v -> %d", bad, r.code)
		}
	}
	long := strings.Repeat("ab", 100)
	if got := oneLine(long, 10); len([]rune(got)) != 10 || !strings.HasSuffix(got, "…") {
		t.Errorf("oneLine = %q", got)
	}
}

func TestFileSendOfferAcceptRejectCancelAndWait(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.bin")
	_ = os.WriteFile(a, []byte("x"), 0o644)
	f := &fakeCore{replies: map[string]string{
		"peers.list":  twoPeers()[:0] + `{"ok":true,"result":{"peers":[{"device_id":"` + dev1 + `","name":"Pixel","state":"connected"}]}}`,
		"file.offer":  `{"ok":true,"result":{"transfer_id":"tr_1111111111111111"}}`,
		"file.accept": `{"ok":true,"result":{}}`, "file.reject": `{"ok":true,"result":{}}`, "file.cancel": `{"ok":true,"result":{}}`,
	}}
	r := run(t, f, "", "file", "send", a)
	if r.code != 0 || !strings.Contains(r.out, "tr_1111111111111111") {
		t.Fatalf("file send (%d): %s %s", r.code, r.out, r.errOut)
	}
	if p := f.last("file.offer"); p["to"] != dev1 || p["path"] != a {
		t.Fatalf("file.offer params = %v", p)
	}
	if r = run(t, f, "", "file", "send", filepath.Join(dir, "missing")); r.code != ExitUsage {
		t.Fatalf("missing file: %d", r.code)
	}
	if r = run(t, f, "", "file", "send", dir); r.code != ExitUsage {
		t.Fatalf("directory: %d", r.code)
	}
	if r = run(t, f, "", "file", "accept", "tr_1"); r.code != 0 || f.last("file.accept")["save_dir"] != nil {
		t.Fatalf("accept: %d %v", r.code, f.last("file.accept"))
	}
	if r = run(t, f, "", "file", "accept", "tr_1", "--dir", dir); r.code != ExitUsage {
		t.Fatalf("accept --dir must be refused (a pipe client cannot choose the folder): %d", r.code)
	}
	for _, c := range []string{"reject", "cancel"} {
		if r = run(t, f, "", "file", c, "tr_1"); r.code != 0 || f.count("file."+c) != 1 {
			t.Fatalf("%s: %d", c, r.code)
		}
	}

	// --wait: polls until the transfer is final, with the right exit code
	var polls int
	f.replies["transfers.list"] = `{"ok":true,"result":{"transfers":[{"transfer_id":"tr_1111111111111111","state":"transferring","name":"a.bin"}]}}`
	go func() {}()
	waitRun := func(final string) result {
		f.mu.Lock()
		polls = 0
		f.replies["transfers.list"] = `{"ok":true,"result":{"transfers":[{"transfer_id":"tr_1111111111111111","state":"transferring","name":"a.bin"}]}}`
		f.mu.Unlock()
		srv := &pipe.Server{Call: func(m, p string) string {
			if m == "transfers.list" {
				f.mu.Lock()
				polls++
				if polls >= 3 {
					f.replies["transfers.list"] = final
				}
				f.mu.Unlock()
			}
			return f.Call(m, p)
		}}
		var out, errb bytes.Buffer
		code := Run(Env{Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb, Sleep: func(time.Duration) {},
			Dial: func() (io.ReadWriteCloser, error) { x, y := net.Pipe(); go srv.ServeConn(y); return x, nil }}, []string{"file", "send", "--wait", a})
		srv.Close()
		return result{code, out.String(), errb.String()}
	}
	if r = waitRun(`{"ok":true,"result":{"transfers":[{"transfer_id":"tr_1111111111111111","state":"done","name":"a.bin"}]}}`); r.code != 0 || polls < 3 {
		t.Fatalf("--wait success: %d after %d polls", r.code, polls)
	}
	if r = waitRun(`{"ok":true,"result":{"transfers":[{"transfer_id":"tr_1111111111111111","state":"failed","name":"a.bin","error":"HASH_MISMATCH"}]}}`); r.code != ExitIntegrity {
		t.Fatalf("--wait hash mismatch: %d", r.code)
	}
	if r = waitRun(`{"ok":true,"result":{"transfers":[{"transfer_id":"tr_1111111111111111","state":"rejected","name":"a.bin"}]}}`); r.code != ExitGone {
		t.Fatalf("--wait rejected: %d", r.code)
	}
}

func TestSettingsRawCallsAndUsage(t *testing.T) {
	f := &fakeCore{replies: map[string]string{
		"settings.get": `{"ok":true,"result":{"settings":{"capture":"on","clip_ttl_s":"900"}}}`,
		"status":       `{"ok":true,"result":{"x":1}}`,
	}}
	r := run(t, f, "", "settings", "get")
	if r.code != 0 || r.out != "capture = on\nclip_ttl_s = 900\n" {
		t.Fatalf("settings get:\n%s", r.out)
	}
	f.replies["settings.get"] = `{"ok":true,"result":{"key":"capture","value":"on"}}`
	if r = run(t, f, "", "settings", "get", "capture"); r.out != "on\n" {
		t.Fatalf("settings get capture = %q", r.out)
	}
	if r = run(t, f, "", "call", "status", `{"a":1}`); r.code != 0 || strings.TrimSpace(r.out) != `{"x":1}` {
		t.Fatalf("call: %d %q", r.code, r.out)
	}
	// a method the pipe does not allow is refused by the server and reported
	if r = run(t, f, "", "call", "pair.begin"); r.code != ExitUsage {
		t.Fatalf("call pair.begin: %d", r.code)
	}
	for _, bad := range [][]string{{}, {"nonsense"}, {"--wat", "status"}, {"call"}, {"call", "status", "not json"}, {"clip"}, {"clip", "send", "--to"}, {"file"}, {"file", "send"}, {"peers", "x"}, {"settings"}} {
		if r = run(t, f, "", bad...); r.code != ExitUsage {
			t.Errorf("%v -> %d, want usage", bad, r.code)
		}
	}
	if r = run(t, f, "", "help"); r.code != ExitOK || !strings.Contains(r.errOut, "usage: cbridge") {
		t.Fatalf("help: %d", r.code)
	}
}

func TestHumanSizes(t *testing.T) {
	for n, want := range map[int64]string{0: "0B", 1023: "1023B", 1024: "1.0K", 1536: "1.5K", 5 << 20: "5.0M", 3 << 30: "3.0G"} {
		if got := human(n); got != want {
			t.Errorf("human(%d) = %s, want %s", n, got, want)
		}
	}
}
