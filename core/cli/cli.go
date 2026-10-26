// Package cli implements the cbridge command line: a thin client for the local
// control channel of a running ClipBridge (docs/api-spec.md §8, §9). It holds no
// state and no secrets; everything is asked of the running core.
package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"nightclip/core/facade"
)

// Exit codes (docs/api-spec.md §9).
const (
	ExitOK        = 0
	ExitUsage     = 2
	ExitNoServer  = 3
	ExitNotPaired = 4
	ExitIntegrity = 5
	ExitOffline   = 6
	ExitGone      = 7
	ExitConflict  = 8
	ExitFailed    = 1
)

// Env is everything the CLI touches, injectable for tests.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Dial connects to the running core's control channel.
	Dial func() (io.ReadWriteCloser, error)
	// Sleep is used by --wait; tests replace it.
	Sleep func(time.Duration)
}

type app struct {
	env  Env
	json bool
	conn io.ReadWriteCloser
	rd   *bufio.Reader
	seq  int
}

// Run executes one command line and returns the process exit code.
func Run(env Env, args []string) int {
	if env.Sleep == nil {
		env.Sleep = time.Sleep
	}
	a := &app{env: env}
	// the global flag may come first
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--json":
			a.json = true
			args = args[1:]
		default:
			return a.usage("unknown flag " + args[0])
		}
	}
	if len(args) == 0 {
		a.usage("")
		return ExitUsage
	}
	defer func() {
		if a.conn != nil {
			a.conn.Close()
		}
	}()
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version":
		fmt.Fprintf(env.Stdout, "cbridge (core %s, protocol clipbridge/v1)\n", facade.Version)
		return ExitOK
	case "status":
		return a.status()
	case "peers":
		return a.peers(rest)
	case "clip":
		return a.clip(rest)
	case "file":
		return a.file(rest)
	case "inbox":
		return a.inbox(rest)
	case "transfers":
		return a.transfers()
	case "settings":
		return a.settings(rest)
	case "doctor":
		return a.doctor()
	case "call":
		return a.raw(rest)
	case "help", "-h", "--help":
		a.usage("")
		return ExitOK
	}
	return a.usage("unknown command " + strconv.Quote(cmd))
}

func (a *app) usage(msg string) int {
	if msg != "" {
		fmt.Fprintln(a.env.Stderr, "cbridge:", msg)
	}
	fmt.Fprint(a.env.Stderr, `usage: cbridge [--json] <command>
  version
  status
  peers [list]
  clip send [--to PEER] [--text TEXT]     (reads stdin when --text is absent)
  file send [--to PEER] [--wait] PATH...
  file accept ID | file reject ID | file cancel ID
  inbox list [--limit N] | inbox show ID
  transfers [list]
  settings get [KEY]
  doctor
  call METHOD [JSON]                      (any method the control channel allows)
PEER is a device id, or a device name (a unique prefix is enough).
`)
	if msg != "" {
		return ExitUsage
	}
	return ExitOK
}

// ---- transport ------------------------------------------------------------------

type reply struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type callError struct {
	Code string
	Msg  string
	Exit int
}

func (e *callError) Error() string { return e.Code + ": " + e.Msg }

func exitFor(code string) int {
	switch code {
	case "NOT_PAIRED":
		return ExitNotPaired
	case "HASH_MISMATCH":
		return ExitIntegrity
	case "NO_TAILNET", "PEER_OFFLINE":
		return ExitOffline
	case "EXPIRED", "NOT_FOUND", "PAIRING_EXPIRED":
		return ExitGone
	case "SEQUENCE_GAP", "RATE_LIMITED":
		return ExitConflict
	case "INVALID", "METHOD_NOT_FOUND":
		return ExitUsage
	}
	return ExitFailed
}

// call performs one request/reply exchange.
func (a *app) call(method string, params any) (json.RawMessage, error) {
	if a.conn == nil {
		c, err := a.env.Dial()
		if err != nil {
			return nil, &callError{Code: "NO_SERVER", Msg: "ClipBridge is not running (or the control channel is unavailable)", Exit: ExitNoServer}
		}
		a.conn, a.rd = c, bufio.NewReaderSize(c, 1<<20)
	}
	a.seq++
	id := strconv.Itoa(a.seq)
	req, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if _, err := a.conn.Write(append(req, '\n')); err != nil {
		return nil, &callError{Code: "NO_SERVER", Msg: "the connection to ClipBridge was lost", Exit: ExitNoServer}
	}
	line, err := a.rd.ReadBytes('\n')
	if err != nil {
		return nil, &callError{Code: "NO_SERVER", Msg: "the connection to ClipBridge was lost", Exit: ExitNoServer}
	}
	var r reply
	if json.Unmarshal(line, &r) != nil {
		return nil, &callError{Code: "INTERNAL", Msg: "unreadable reply", Exit: ExitFailed}
	}
	if !r.OK {
		return nil, &callError{Code: r.Error.Code, Msg: r.Error.Message, Exit: exitFor(r.Error.Code)}
	}
	return r.Result, nil
}

// fail prints an error and returns its exit code.
func (a *app) fail(err error) int {
	var ce *callError
	if errors.As(err, &ce) {
		if a.json {
			b, _ := json.Marshal(map[string]any{"ok": false, "error": map[string]any{"code": ce.Code, "message": ce.Msg}})
			fmt.Fprintln(a.env.Stdout, string(b))
		} else {
			fmt.Fprintf(a.env.Stderr, "cbridge: %s\n", ce.Msg)
		}
		return ce.Exit
	}
	fmt.Fprintln(a.env.Stderr, "cbridge:", err)
	return ExitFailed
}

// emit prints the raw result in --json mode and reports whether it did.
func (a *app) emit(res json.RawMessage) bool {
	if !a.json {
		return false
	}
	fmt.Fprintln(a.env.Stdout, string(res))
	return true
}

// ---- peers ------------------------------------------------------------------

type peerInfo struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	State    string `json:"state"`
	Pending  int    `json:"outbox_pending"`
}

func (a *app) fetchPeers() ([]peerInfo, json.RawMessage, error) {
	res, err := a.call("peers.list", map[string]any{})
	if err != nil {
		return nil, nil, err
	}
	var out struct {
		Peers []peerInfo `json:"peers"`
	}
	_ = json.Unmarshal(res, &out)
	return out.Peers, res, nil
}

// resolvePeer maps a device id or a (prefix of a) name to a device id.
func (a *app) resolvePeer(want string) (string, error) {
	peers, _, err := a.fetchPeers()
	if err != nil {
		return "", err
	}
	if want == "" {
		var live []peerInfo
		for _, p := range peers {
			if p.State != "revoked" {
				live = append(live, p)
			}
		}
		switch len(live) {
		case 1:
			return live[0].DeviceID, nil
		case 0:
			return "", &callError{Code: "NOT_PAIRED", Msg: "no paired device", Exit: ExitNotPaired}
		}
		return "", &callError{Code: "INVALID", Msg: "several devices are paired; choose one with --to", Exit: ExitUsage}
	}
	var hits []peerInfo
	for _, p := range peers {
		if p.DeviceID == want {
			return p.DeviceID, nil
		}
		if strings.HasPrefix(strings.ToLower(p.Name), strings.ToLower(want)) && p.State != "revoked" {
			hits = append(hits, p)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0].DeviceID, nil
	case 0:
		return "", &callError{Code: "NOT_PAIRED", Msg: "no paired device matches " + strconv.Quote(want), Exit: ExitNotPaired}
	}
	return "", &callError{Code: "INVALID", Msg: strconv.Quote(want) + " matches several devices; use the device id", Exit: ExitUsage}
}

func (a *app) peers(args []string) int {
	if len(args) > 0 && args[0] != "list" {
		return a.usage("peers: unknown subcommand " + args[0])
	}
	peers, raw, err := a.fetchPeers()
	if err != nil {
		return a.fail(err)
	}
	if a.emit(raw) {
		return ExitOK
	}
	if len(peers) == 0 {
		fmt.Fprintln(a.env.Stdout, "no paired devices")
		return ExitOK
	}
	for _, p := range peers {
		fmt.Fprintf(a.env.Stdout, "%-20s %-8s %-11s queued %-3d %s\n", p.Name, p.Platform, p.State, p.Pending, p.DeviceID)
	}
	return ExitOK
}

// ---- status / doctor ------------------------------------------------------------------

func (a *app) status() int {
	res, err := a.call("status", map[string]any{})
	if err != nil {
		return a.fail(err)
	}
	if a.emit(res) {
		return ExitOK
	}
	var st struct {
		DeviceID   string     `json:"device_id"`
		Name       string     `json:"device_name"`
		Platform   string     `json:"platform"`
		Capture    string     `json:"capture"`
		Addrs      []string   `json:"listening_addrs"`
		Port       int        `json:"listening_port"`
		Peers      []peerInfo `json:"peers"`
		Dropped    int        `json:"events_dropped"`
		HasListen  bool       `json:"listen"`
		ConfigPort int        `json:"port"`
	}
	_ = json.Unmarshal(res, &st)
	w := a.env.Stdout
	fmt.Fprintf(w, "device:    %s (%s) %s\n", st.Name, st.Platform, st.DeviceID)
	switch {
	case !st.HasListen:
		fmt.Fprintln(w, "listening: no (this device dials out)")
	case len(st.Addrs) == 0:
		fmt.Fprintln(w, "listening: no tailnet address found")
	default:
		fmt.Fprintf(w, "listening: %s:%d\n", strings.Join(st.Addrs, ","), st.Port)
	}
	fmt.Fprintf(w, "sync:      %s\n", map[bool]string{true: "on", false: "off"}[st.Capture == "on"])
	fmt.Fprintf(w, "devices:   %d\n", len(st.Peers))
	for _, p := range st.Peers {
		fmt.Fprintf(w, "  %-18s %-11s queued %d\n", p.Name, p.State, p.Pending)
	}
	return ExitOK
}

func (a *app) doctor() int {
	res, err := a.call("diagnostics.get", map[string]any{})
	if err != nil {
		return a.fail(err)
	}
	if a.emit(res) {
		return ExitOK
	}
	var d struct {
		Core    string     `json:"core"`
		Listen  bool       `json:"listen"`
		Addrs   []string   `json:"listening_addrs"`
		Peers   []peerInfo `json:"peers"`
		Dropped int        `json:"events_dropped"`
	}
	_ = json.Unmarshal(res, &d)
	w := a.env.Stdout
	problems := 0
	check := func(ok bool, good, bad string) {
		if ok {
			fmt.Fprintln(w, "ok    ", good)
		} else {
			fmt.Fprintln(w, "FAIL  ", bad)
			problems++
		}
	}
	fmt.Fprintf(w, "core %s\n", d.Core)
	if d.Listen {
		check(len(d.Addrs) > 0, "listening on "+strings.Join(d.Addrs, ", "), "no Tailscale address found; is Tailscale running and logged in?")
	}
	check(len(d.Peers) > 0, fmt.Sprintf("%d paired device(s)", len(d.Peers)), "no paired devices")
	for _, p := range d.Peers {
		switch p.State {
		case "connected":
			fmt.Fprintf(w, "ok     %s is connected\n", p.Name)
		case "unpaired", "revoked":
			fmt.Fprintf(w, "FAIL   %s: %s (pair again)\n", p.Name, p.State)
			problems++
		default:
			fmt.Fprintf(w, "WARN   %s is %s (queued %d)\n", p.Name, p.State, p.Pending)
		}
	}
	if d.Dropped > 0 {
		fmt.Fprintf(w, "WARN   %d events were dropped because the app was not reading them\n", d.Dropped)
	}
	if problems > 0 {
		return ExitOffline
	}
	return ExitOK
}

// ---- clip ------------------------------------------------------------------

func (a *app) clip(args []string) int {
	if len(args) == 0 || args[0] != "send" {
		return a.usage("clip: expected 'send'")
	}
	var to, text string
	haveText := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--to":
			if i+1 >= len(args) {
				return a.usage("--to needs a value")
			}
			to = args[i+1]
			i++
		case "--text":
			if i+1 >= len(args) {
				return a.usage("--text needs a value")
			}
			text, haveText = args[i+1], true
			i++
		default:
			return a.usage("clip send: unexpected " + args[i])
		}
	}
	if !haveText {
		b, err := io.ReadAll(io.LimitReader(a.env.Stdin, 1<<20))
		if err != nil {
			return a.fail(err)
		}
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		return a.usage("nothing to send")
	}
	if !utf8.ValidString(text) {
		return a.usage("the text is not valid UTF-8")
	}
	params := map[string]any{"text": text}
	if to != "" {
		id, err := a.resolvePeer(to)
		if err != nil {
			return a.fail(err)
		}
		params["to"] = []string{id}
	}
	res, err := a.call("clip.send", params)
	if err != nil {
		return a.fail(err)
	}
	if a.emit(res) {
		return ExitOK
	}
	var out struct {
		Sent []struct{ Name string } `json:"sent"`
	}
	_ = json.Unmarshal(res, &out)
	names := make([]string, 0, len(out.Sent))
	for _, s := range out.Sent {
		names = append(names, s.Name)
	}
	fmt.Fprintf(a.env.Stdout, "sent to %s\n", strings.Join(names, ", "))
	return ExitOK
}

// ---- inbox ------------------------------------------------------------------

type inboxItem struct {
	EventID    string `json:"event_id"`
	FromName   string `json:"from_name"`
	Text       string `json:"text"`
	Sensitive  bool   `json:"sensitive"`
	State      string `json:"state"`
	ReceivedAt int64  `json:"received_at"`
}

func (a *app) inbox(args []string) int {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		limit := 20
		for i := 1; i < len(args); i++ {
			if args[i] == "--limit" && i+1 < len(args) {
				n, err := strconv.Atoi(args[i+1])
				if err != nil || n < 1 {
					return a.usage("--limit must be a positive number")
				}
				limit = n
				i++
			} else {
				return a.usage("inbox list: unexpected " + args[i])
			}
		}
		res, err := a.call("inbox.list", map[string]any{"limit": limit})
		if err != nil {
			return a.fail(err)
		}
		if a.emit(res) {
			return ExitOK
		}
		var out struct {
			Items []inboxItem `json:"items"`
		}
		_ = json.Unmarshal(res, &out)
		if len(out.Items) == 0 {
			fmt.Fprintln(a.env.Stdout, "inbox is empty")
		}
		for _, it := range out.Items {
			text := oneLine(it.Text, 60)
			if it.Sensitive {
				text = "(sensitive, hidden)"
			}
			fmt.Fprintf(a.env.Stdout, "%s  %-12s %-9s %s  %s\n", time.Unix(it.ReceivedAt, 0).Format("01-02 15:04"), it.FromName, it.State, text, it.EventID)
		}
		return ExitOK
	case "show":
		if len(args) != 2 {
			return a.usage("inbox show EVENT_ID")
		}
		res, err := a.call("inbox.get", map[string]any{"event_id": args[1]})
		if err != nil {
			return a.fail(err)
		}
		if a.emit(res) {
			return ExitOK
		}
		var it inboxItem
		_ = json.Unmarshal(res, &it)
		if it.Sensitive {
			fmt.Fprintln(a.env.Stderr, "this item looks sensitive; its text is only shown in the ClipBridge window")
			return ExitOK
		}
		fmt.Fprint(a.env.Stdout, it.Text)
		if !strings.HasSuffix(it.Text, "\n") {
			fmt.Fprintln(a.env.Stdout)
		}
		return ExitOK
	}
	return a.usage("inbox: unknown subcommand " + args[0])
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		return string(r[:max-1]) + "…"
	}
	return s
}

// ---- files ------------------------------------------------------------------

type transferInfo struct {
	ID        string `json:"transfer_id"`
	PeerName  string `json:"peer_name"`
	Direction string `json:"direction"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	State     string `json:"state"`
	Offset    int64  `json:"offset"`
	Error     string `json:"error"`
	Path      string `json:"path"`
}

func (a *app) transfers() int {
	res, err := a.call("transfers.list", map[string]any{"limit": 50})
	if err != nil {
		return a.fail(err)
	}
	if a.emit(res) {
		return ExitOK
	}
	var out struct {
		Transfers []transferInfo `json:"transfers"`
	}
	_ = json.Unmarshal(res, &out)
	if len(out.Transfers) == 0 {
		fmt.Fprintln(a.env.Stdout, "no transfers")
	}
	for _, t := range out.Transfers {
		arrow := "->"
		if t.Direction == "in" {
			arrow = "<-"
		}
		extra := ""
		if t.Error != "" {
			extra = " " + t.Error
		}
		fmt.Fprintf(a.env.Stdout, "%s %s %-12s %-12s %s/%s %s%s\n", t.ID, arrow, t.PeerName, t.State, human(t.Offset), human(t.Size), t.Name, extra)
	}
	return ExitOK
}

func human(n int64) string {
	const k = 1024
	switch {
	case n >= k*k*k:
		return fmt.Sprintf("%.1fG", float64(n)/(k*k*k))
	case n >= k*k:
		return fmt.Sprintf("%.1fM", float64(n)/(k*k))
	case n >= k:
		return fmt.Sprintf("%.1fK", float64(n)/k)
	}
	return fmt.Sprintf("%dB", n)
}

func (a *app) file(args []string) int {
	if len(args) == 0 {
		return a.usage("file: expected send, accept, reject or cancel")
	}
	switch args[0] {
	case "send":
		return a.fileSend(args[1:])
	case "accept":
		if len(args) < 2 {
			return a.usage("file accept ID")
		}
		if len(args) > 2 {
			return a.usage("file accept: unexpected " + args[2] + " (the file goes to the download folder set in ClipBridge)")
		}
		return a.simple("file.accept", map[string]any{"transfer_id": args[1]}, "accepted")
	case "reject", "cancel":
		if len(args) != 2 {
			return a.usage("file " + args[0] + " ID")
		}
		return a.simple("file."+args[0], map[string]any{"transfer_id": args[1]}, args[0]+"ed")
	}
	return a.usage("file: unknown subcommand " + args[0])
}

func (a *app) simple(method string, params map[string]any, done string) int {
	res, err := a.call(method, params)
	if err != nil {
		return a.fail(err)
	}
	if !a.emit(res) {
		fmt.Fprintln(a.env.Stdout, done)
	}
	return ExitOK
}

func (a *app) fileSend(args []string) int {
	var to string
	wait := false
	var paths []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--to":
			if i+1 >= len(args) {
				return a.usage("--to needs a value")
			}
			to = args[i+1]
			i++
		case "--wait":
			wait = true
		default:
			paths = append(paths, args[i])
		}
	}
	if len(paths) == 0 {
		return a.usage("file send needs at least one path")
	}
	peerID, err := a.resolvePeer(to)
	if err != nil {
		return a.fail(err)
	}
	var ids []string
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return a.fail(err)
		}
		if st, err := os.Stat(abs); err != nil || !st.Mode().IsRegular() {
			fmt.Fprintf(a.env.Stderr, "cbridge: %s is not a readable file\n", p)
			return ExitUsage
		}
		res, err := a.call("file.offer", map[string]any{"to": peerID, "path": abs})
		if err != nil {
			return a.fail(err)
		}
		var out struct {
			ID string `json:"transfer_id"`
		}
		_ = json.Unmarshal(res, &out)
		ids = append(ids, out.ID)
		if a.json {
			fmt.Fprintln(a.env.Stdout, string(res))
		} else {
			fmt.Fprintf(a.env.Stdout, "offered %s as %s\n", p, out.ID)
		}
	}
	if !wait {
		return ExitOK
	}
	return a.waitTransfers(ids)
}

// waitTransfers polls until every transfer reached a final state.
func (a *app) waitTransfers(ids []string) int {
	deadline := time.Now().Add(25 * time.Hour)
	for time.Now().Before(deadline) {
		res, err := a.call("transfers.list", map[string]any{"limit": 200})
		if err != nil {
			return a.fail(err)
		}
		var out struct {
			Transfers []transferInfo `json:"transfers"`
		}
		_ = json.Unmarshal(res, &out)
		byID := map[string]transferInfo{}
		for _, t := range out.Transfers {
			byID[t.ID] = t
		}
		open := 0
		exit := ExitOK
		for _, id := range ids {
			t := byID[id]
			switch t.State {
			case "done":
			case "rejected", "cancelled":
				fmt.Fprintf(a.env.Stderr, "cbridge: %s was %s\n", t.Name, t.State)
				exit = ExitGone
			case "failed", "expired":
				fmt.Fprintf(a.env.Stderr, "cbridge: %s %s %s\n", t.Name, t.State, t.Error)
				if t.Error == "HASH_MISMATCH" {
					exit = ExitIntegrity
				} else if exit == ExitOK {
					exit = ExitFailed
				}
			default:
				open++
			}
		}
		if open == 0 {
			return exit
		}
		a.env.Sleep(500 * time.Millisecond)
	}
	return ExitFailed
}

// ---- settings / raw ------------------------------------------------------------------

func (a *app) settings(args []string) int {
	if len(args) == 0 || args[0] != "get" {
		return a.usage("settings: expected 'get [KEY]'")
	}
	params := map[string]any{}
	if len(args) > 1 {
		params["key"] = args[1]
	}
	res, err := a.call("settings.get", params)
	if err != nil {
		return a.fail(err)
	}
	if a.emit(res) {
		return ExitOK
	}
	var out struct {
		Key      string            `json:"key"`
		Value    string            `json:"value"`
		Settings map[string]string `json:"settings"`
	}
	_ = json.Unmarshal(res, &out)
	if out.Key != "" {
		fmt.Fprintln(a.env.Stdout, out.Value)
		return ExitOK
	}
	keys := make([]string, 0, len(out.Settings))
	for k := range out.Settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(a.env.Stdout, "%s = %s\n", k, out.Settings[k])
	}
	return ExitOK
}

func (a *app) raw(args []string) int {
	if len(args) < 1 || len(args) > 2 {
		return a.usage("call METHOD [JSON]")
	}
	var params any = map[string]any{}
	if len(args) == 2 {
		if json.Unmarshal([]byte(args[1]), &params) != nil {
			return a.usage("the parameters are not valid JSON")
		}
	}
	res, err := a.call(args[0], params)
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprintln(a.env.Stdout, string(res))
	return ExitOK
}
