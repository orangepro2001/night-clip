package notes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// Transport (notes/docs/architecture.md §4, decided in development-plan.md §3): a handful of HTTP
// routes on the same mutually pinned TLS server the other features use, so notes never share the
// WebSocket with clipboard events or file chunks and a big file transfer cannot delay a note. Like the
// vault sync, the listening device only answers; the dialing device asks, pulls what it lacks and
// pushes what the other lacks, so one round leaves both sides with the same revisions. Every device
// then runs the same deterministic head resolution (resolve.go) over the same revisions.
//
// A round is cheap when nothing changed: GET /v1/notes/head returns a counter that only grows when
// the other device stores a revision, and the dialer remembers the counters it saw at the end of the
// last complete round.

const (
	pathHead   = "/v1/notes/head"
	pathDigest = "/v1/notes/digest"
	pathPull   = "/v1/notes/pull"
	pathPush   = "/v1/notes/push"

	digestPage   = 500      // notes per digest page
	pullNotes    = 50       // notes per pull request
	haveDepth    = 64       // ancestors of our heads named in a pull, so the server need not resend shared history
	maxBatchRevs = 400      // revisions per pull answer / push request
	maxBatchSize = 6 << 20  // bytes of revision text per pull answer / push request
	maxBodyRead  = 10 << 20 // a request body larger than this is refused outright
	maxPullIDs   = 50       // notes named in one pull request
	maxPushRevs  = 2 * maxBatchRevs
)

// Sync states, as shown to the user.
const (
	StateIdle    = "idle"
	StateSyncing = "syncing"
	StateError   = "error"
	StatePeerOld = "peer_old" // the other device speaks a newer note format: update this app
)

type syncState struct {
	procMu sync.Mutex // one batch of received revisions is applied at a time
	kick   chan struct{}
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	run    bool
	live   map[string]bool // peers a round is running for
}

func (s *Service) initSync() {
	s.sync.kick = make(chan struct{}, 1)
	s.sync.live = map[string]bool{}
	s.sync.ctx, s.sync.cancel = context.WithCancel(context.Background())
	if s.e == nil {
		return
	}
	s.e.Handle("GET "+pathHead, http.HandlerFunc(s.serveHead), false)
	s.e.Handle("GET "+pathDigest, http.HandlerFunc(s.serveDigest), false)
	s.e.Handle("POST "+pathPull, http.HandlerFunc(s.servePull), false)
	s.e.Handle("POST "+pathPush, http.HandlerFunc(s.servePush), false)
}

// Start begins polling the peers we dial.
func (s *Service) Start() {
	s.sync.mu.Lock()
	if s.sync.run || s.e == nil {
		s.sync.mu.Unlock()
		return
	}
	s.sync.run = true
	s.sync.mu.Unlock()
	s.sync.wg.Add(1)
	go s.loop()
}

// Close stops the loop and any round that is running.
func (s *Service) Close() {
	s.sync.mu.Lock()
	s.sync.cancel() // under the lock: SyncNow checks the context before it adds to the wait group
	s.sync.mu.Unlock()
	s.sync.wg.Wait()
}

// Kick asks for a sync round now: a note changed here, a link came up.
func (s *Service) Kick() {
	select {
	case s.sync.kick <- struct{}{}:
	default:
	}
}

// OnPeerState is the engine's peer.state hook.
func (s *Service) OnPeerState(peerID, state string) {
	if state == peer.StateConnected {
		s.Kick()
	}
}

// Forget drops what was kept for a peer that was unpaired.
func (s *Service) Forget(peerID string) { _ = s.db.DeleteNoteSync(peerID) }

func (s *Service) loop() {
	defer s.sync.wg.Done()
	t := time.NewTicker(s.timing.Poll)
	defer t.Stop()
	for {
		select {
		case <-s.sync.ctx.Done():
			return
		case <-t.C:
		case <-s.sync.kick:
		}
		s.round(false)
	}
}

// SyncNow runs a round with one peer (or every dialed peer) right away, ignoring the "nothing new"
// shortcut. It returns once the round has been started.
func (s *Service) SyncNow(peerID string) error {
	if s.e == nil {
		return proto.Errorf(proto.CodeNotInitialized, "sync is not available")
	}
	if peerID != "" {
		p, found, err := s.db.GetPeer(peerID)
		if err != nil {
			return err
		}
		if !found || p.State != "paired" {
			return proto.Errorf(proto.CodeNotPaired, "this device is not paired")
		}
	}
	s.sync.mu.Lock()
	if s.sync.ctx.Err() != nil {
		s.sync.mu.Unlock()
		return proto.Errorf(proto.CodeNotInitialized, "sync is shutting down")
	}
	s.sync.wg.Add(1)
	s.sync.mu.Unlock()
	go func() {
		defer s.sync.wg.Done()
		s.roundFor(peerID, true)
	}()
	return nil
}

func (s *Service) round(force bool) { s.roundFor("", force) }

func (s *Service) roundFor(only string, force bool) {
	peers, err := s.db.ListPeers()
	if err != nil {
		return
	}
	connected := map[string]bool{}
	for _, st := range s.e.PeerStatuses() {
		connected[st.DeviceID] = st.State == peer.StateConnected
	}
	for _, p := range peers {
		if s.sync.ctx.Err() != nil {
			return
		}
		if only != "" && p.DeviceID != only || p.State != "paired" || !connected[p.DeviceID] || !s.e.WeDial(p) {
			continue
		}
		s.syncPeer(p, force)
	}
}

// ---- wire shapes ----------------------------------------------------------------------------------

type headReply struct {
	Seq   int64 `json:"seq"`
	Notes int   `json:"notes"`
}

type digestItem struct {
	NoteID string   `json:"note_id"`
	Heads  []string `json:"heads"`
}

type digestReply struct {
	Items []digestItem `json:"items"`
	Next  string       `json:"next,omitempty"`
}

type pullItem struct {
	NoteID string   `json:"note_id"`
	Have   []string `json:"have"`
}

type pullRequest struct {
	Items []pullItem `json:"items"`
}

type pullReply struct {
	Revs []WireRev `json:"revs"`
	More []string  `json:"more,omitempty"` // notes whose answer was cut short: ask again with the new "have"
}

type pushRequest struct {
	Revs []WireRev `json:"revs"`
}

type rejection struct {
	RevID  string `json:"rev_id"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

type pushReply struct {
	Accepted int         `json:"accepted"` // revisions now stored (new or already there)
	Inserted int         `json:"inserted"` // of those, how many were new
	Rejected []rejection `json:"rejected,omitempty"`
}

// ---- server side (the device that is dialed) ------------------------------------------------------

func (s *Service) caller(w http.ResponseWriter, r *http.Request) (store.Peer, bool) {
	c := peer.PeerFromContext(r.Context())
	if c.Peer == nil {
		writeErr(w, http.StatusForbidden, string(proto.CodeNotPaired), "this device is not paired")
		return store.Peer{}, false
	}
	// the record may have changed since the connection was admitted
	p, found, err := s.db.GetPeer(c.Peer.DeviceID)
	if err != nil || !found || p.State != "paired" {
		writeErr(w, http.StatusForbidden, string(proto.CodeNotPaired), "this device is not paired")
		return store.Peer{}, false
	}
	return p, true
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg}})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// The route handlers are thin: the work is in the do* methods, which the tests (and the in-process
// remote) call directly.

func (s *Service) serveHead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.caller(w, r); !ok {
		return
	}
	rep, err := s.doHead()
	s.reply(w, rep, err)
}

func (s *Service) serveDigest(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.caller(w, r); !ok {
		return
	}
	rep, err := s.doDigest(r.URL.Query().Get("after"))
	s.reply(w, rep, err)
}

func (s *Service) servePull(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.caller(w, r); !ok {
		return
	}
	var req pullRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, string(proto.CodeInvalid), "malformed request")
		return
	}
	rep, err := s.doPull(req)
	s.reply(w, rep, err)
}

func (s *Service) servePush(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.caller(w, r); !ok {
		return
	}
	var req pushRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyRead)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, string(proto.CodeInvalid), "malformed request")
		return
	}
	rep, err := s.doPush(req)
	s.reply(w, rep, err)
}

func (s *Service) reply(w http.ResponseWriter, rep any, err error) {
	if err != nil {
		var pe *proto.Error
		if errors.As(err, &pe) && pe.Code == proto.CodeInvalid {
			writeErr(w, http.StatusBadRequest, string(proto.CodeInvalid), "malformed request")
			return
		}
		writeErr(w, http.StatusInternalServerError, string(proto.CodeDBUnavailable), "temporarily unavailable")
		return
	}
	writeJSON(w, rep)
}

func (s *Service) doHead() (headReply, error) {
	var rep headReply
	err := s.db.NotesView(func(tx *store.NotesTx) (err error) {
		rep.Seq, err = tx.Seq()
		return err
	})
	return rep, err
}

func (s *Service) doDigest(after string) (digestReply, error) {
	if after != "" && !noteIDRe.MatchString(after) {
		return digestReply{}, proto.Errorf(proto.CodeInvalid, "after is malformed")
	}
	rep := digestReply{Items: []digestItem{}}
	err := s.db.NotesView(func(tx *store.NotesTx) error {
		d, err := tx.Digest(after, digestPage)
		for _, n := range d {
			rep.Items = append(rep.Items, digestItem{NoteID: n.NoteID, Heads: n.Heads})
		}
		return err
	})
	if len(rep.Items) == digestPage {
		rep.Next = rep.Items[len(rep.Items)-1].NoteID
	}
	return rep, err
}

func (s *Service) doPull(req pullRequest) (pullReply, error) {
	if len(req.Items) > maxPullIDs {
		return pullReply{}, proto.Errorf(proto.CodeInvalid, "too many notes")
	}
	rep := pullReply{Revs: []WireRev{}}
	err := s.db.NotesView(func(tx *store.NotesTx) error {
		budget := batchBudget{}
		for _, it := range req.Items {
			if !noteIDRe.MatchString(it.NoteID) || len(it.Have) > 4*haveDepth {
				return proto.Errorf(proto.CodeInvalid, "malformed request")
			}
			revs, complete, err := missingFor(tx, it.NoteID, it.Have, &budget)
			if err != nil {
				return err
			}
			rep.Revs = append(rep.Revs, revs...)
			if !complete {
				rep.More = append(rep.More, it.NoteID)
			}
		}
		return nil
	})
	return rep, err
}

func (s *Service) doPush(req pushRequest) (pushReply, error) {
	if len(req.Revs) > maxPushRevs {
		return pushReply{}, proto.Errorf(proto.CodeInvalid, "too many revisions")
	}
	return s.applyWire(req.Revs)
}

// batchBudget caps what one answer may carry.
type batchBudget struct{ revs, bytes int }

func (b *batchBudget) take(r store.NoteRev) bool {
	size := len(r.Title) + len(r.Body) + 400
	if b.revs >= maxBatchRevs || b.bytes+size > maxBatchSize && b.revs > 0 {
		return false
	}
	b.revs++
	b.bytes += size
	return true
}

// ancestorsOf returns the revisions of one note that the given revisions (those that exist) descend
// from, including themselves.
func ancestorsOf(g *graph, ids []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, id := range ids {
		if _, ok := g.parents[id]; !ok {
			continue
		}
		for r := range g.ancestors(id) {
			out[r] = struct{}{}
		}
	}
	return out
}

// topological orders revisions so that parents come before children (ties by id, so the order is
// the same everywhere).
func topological(g *graph, ids map[string]struct{}) []string {
	var order []string
	done := map[string]bool{}
	var keys []string
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := g.meta[keys[i]], g.meta[keys[j]]
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt
		}
		return keys[i] < keys[j]
	})
	var visit func(id string)
	visit = func(id string) {
		if done[id] {
			return
		}
		done[id] = true
		ps := append([]string(nil), g.parents[id]...)
		sort.Strings(ps)
		for _, p := range ps {
			if _, in := ids[p]; in {
				visit(p)
			}
		}
		order = append(order, id)
	}
	for _, id := range keys {
		visit(id)
	}
	return order
}

// missingFor returns the revisions of a note that the asker does not have, parents first, given what
// the asker says it has. complete is false when the budget ran out before everything was included.
func missingFor(tx *store.NotesTx, noteID string, have []string, budget *batchBudget) ([]WireRev, bool, error) {
	g, err := loadGraph(tx, noteID)
	if err != nil {
		return nil, false, err
	}
	covered := ancestorsOf(g, have)
	want := map[string]struct{}{}
	for id := range g.parents {
		if _, ok := covered[id]; !ok {
			want[id] = struct{}{}
		}
	}
	var out []WireRev
	for _, id := range topological(g, want) {
		r, found, err := tx.GetRev(id)
		if err != nil {
			return nil, false, err
		}
		if !found {
			continue
		}
		if !budget.take(r) {
			return out, false, nil
		}
		out = append(out, toWire(r))
	}
	return out, true, nil
}

// ---- applying what a peer sent ----------------------------------------------------------------------

// applyWire validates and stores revisions (parents first, as sent) and resolves the notes they touch.
// A revision that fails validation is rejected on its own; the rest of the batch still applies.
func (s *Service) applyWire(in []WireRev) (pushReply, error) {
	s.sync.procMu.Lock()
	defer s.sync.procMu.Unlock()
	var rep pushReply
	var ch changes
	err := s.db.NotesTx(func(tx *store.NotesTx) error {
		touched := map[string]bool{}
		var order []string
		for _, w := range in {
			r, err := validateWire(w)
			if err != nil {
				var pe *proto.Error
				code, reason := string(proto.CodeInvalid), err.Error()
				if errors.As(err, &pe) {
					code, reason = string(pe.Code), pe.Message
				}
				rep.Rejected = append(rep.Rejected, rejection{RevID: w.RevID, Code: code, Reason: reason})
				continue
			}
			inserted, err := tx.InsertRev(r)
			if errors.Is(err, store.ErrMissingParent) {
				rep.Rejected = append(rep.Rejected, rejection{RevID: w.RevID, Code: "MISSING_PARENT", Reason: "a parent revision is not known"})
				continue
			}
			if err != nil {
				return err
			}
			rep.Accepted++
			if inserted {
				rep.Inserted++
			}
			if inserted && !touched[r.NoteID] {
				touched[r.NoteID] = true
				order = append(order, r.NoteID)
			}
		}
		for _, id := range order {
			o, err := reconcile(tx, id)
			if err != nil {
				return err
			}
			ch.add(o)
		}
		return nil
	})
	if err != nil {
		return pushReply{}, err
	}
	s.flush(ch)
	return rep, nil
}
