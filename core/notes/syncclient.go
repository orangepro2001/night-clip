package notes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"

	"nightclip/core/peer"
	"nightclip/core/proto"
	"nightclip/core/store"
)

// The dialing side of the notes sync (see sync.go for the transport).

type peerHTTPError struct {
	status int
	code   string
	msg    string
}

func (e *peerHTTPError) Error() string {
	return fmt.Sprintf("notes sync: peer answered %d %s %s", e.status, e.code, e.msg)
}

func httpDo(ctx context.Context, c *http.Client, method, url string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&e)
		return &peerHTTPError{status: resp.StatusCode, code: e.Error.Code, msg: e.Error.Message}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxBodyRead)).Decode(out)
}

// remote is the other device as the sync round sees it: over HTTP in production, in-process in tests.
type remote interface {
	head(ctx context.Context) (headReply, error)
	digest(ctx context.Context, after string) (digestReply, error)
	pull(ctx context.Context, req pullRequest) (pullReply, error)
	push(ctx context.Context, req pushRequest) (pushReply, error)
}

type httpRemote struct {
	c    *http.Client
	base string
}

func (h httpRemote) head(ctx context.Context) (rep headReply, err error) {
	err = httpDo(ctx, h.c, http.MethodGet, h.base+pathHead, nil, &rep)
	return
}

func (h httpRemote) digest(ctx context.Context, after string) (rep digestReply, err error) {
	q := h.base + pathDigest
	if after != "" {
		q += "?after=" + after
	}
	err = httpDo(ctx, h.c, http.MethodGet, q, nil, &rep)
	return
}

func (h httpRemote) pull(ctx context.Context, req pullRequest) (rep pullReply, err error) {
	err = httpDo(ctx, h.c, http.MethodPost, h.base+pathPull, req, &rep)
	return
}

func (h httpRemote) push(ctx context.Context, req pushRequest) (rep pushReply, err error) {
	err = httpDo(ctx, h.c, http.MethodPost, h.base+pathPush, req, &rep)
	return
}

// setSync updates the sync row of a peer and tells the shells when the visible state changed.
func (s *Service) setSync(peerID string, mutate func(*store.NoteSyncRow)) {
	row, _, _ := s.db.GetNoteSync(peerID)
	prev := row
	row.PeerID = peerID
	mutate(&row)
	_ = s.db.PutNoteSync(row)
	if prev.State != row.State || prev.Behind != row.Behind {
		s.emit(map[string]any{"type": "notes.sync.updated", "peer_id": peerID, "state": row.State, "behind": row.Behind})
	}
}

func (s *Service) stillPaired(peerID string) bool {
	p, found, err := s.db.GetPeer(peerID)
	return err == nil && found && p.State == "paired"
}

// exchangeResult is what one round did.
type exchangeResult struct {
	ourSeq  int64 // our counter right after the pull, before the push was worked out
	refused int   // revisions the peer sent that failed validation
}

// syncPeer runs one round against a peer we dial.
func (s *Service) syncPeer(p store.Peer, force bool) {
	s.sync.mu.Lock()
	if s.sync.live[p.DeviceID] {
		s.sync.mu.Unlock()
		return
	}
	s.sync.live[p.DeviceID] = true
	s.sync.mu.Unlock()
	defer func() {
		s.sync.mu.Lock()
		delete(s.sync.live, p.DeviceID)
		s.sync.mu.Unlock()
	}()

	ep, err := s.e.Endpoint(p)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.sync.ctx, s.timing.HTTP)
	defer cancel()

	for _, b := range ep.Bases {
		r := httpRemote{c: ep.Client, base: b}
		if _, err := r.head(ctx); err == nil {
			s.runRound(ctx, p.DeviceID, r, force)
			return
		}
	}
	// offline, or not paired on their side: the link layer reports that
}

// runRound is one complete sync with a reachable peer.
func (s *Service) runRound(ctx context.Context, peerID string, r remote, force bool) {
	head, err := r.head(ctx)
	if err != nil {
		return
	}
	var ourSeq int64
	if err := s.db.NotesView(func(tx *store.NotesTx) (err error) { ourSeq, err = tx.Seq(); return }); err != nil {
		return
	}
	row, _, _ := s.db.GetNoteSync(peerID)
	if !force && row.State == StateIdle && row.PeerSeq == head.Seq && row.OurSeq == ourSeq {
		return // nothing happened on either side since the last complete round
	}

	s.setSync(peerID, func(r *store.NoteSyncRow) { r.State, r.Detail = StateSyncing, "" })
	res, err := s.exchange(ctx, peerID, r)
	if !s.stillPaired(peerID) {
		return
	}
	if err == nil && res.refused > 0 {
		err = proto.Errorf(proto.CodeInvalid, "%d revisions from the other device were refused", res.refused)
	}
	if err != nil {
		state, detail := StateError, err.Error()
		var pe *proto.Error
		if errors.As(err, &pe) && pe.Code == proto.CodeUnsupportedFormat {
			state, detail = StatePeerOld, "the other device uses a newer note format: update this app"
		}
		s.setSync(peerID, func(r *store.NoteSyncRow) { r.State, r.Detail, r.Ahead = state, detail, 0 })
		return
	}
	// The counters are the ones from before the round (our own from just after the pull): anything
	// that happens during the round shows as news in the next poll instead of being missed. Our
	// pushes raise the peer's counter, which costs one extra, cheap round that finds nothing.
	s.setSync(peerID, func(r *store.NoteSyncRow) {
		r.State, r.Detail, r.LastOKAt = StateIdle, "", s.nowMs()
		r.PeerSeq, r.OurSeq, r.Behind, r.Ahead = head.Seq, res.ourSeq, 0, 0
	})
}

// peerDigest reads the whole digest of the peer: note id -> head revisions.
func peerDigest(ctx context.Context, r remote) (map[string][]string, error) {
	theirs := map[string][]string{}
	after := ""
	for {
		rep, err := r.digest(ctx, after)
		if err != nil {
			return nil, err
		}
		for _, it := range rep.Items {
			theirs[it.NoteID] = it.Heads
		}
		if rep.Next == "" {
			return theirs, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		after = rep.Next
	}
}

// exchange pulls what the peer has that we lack, then pushes what it lacks.
func (s *Service) exchange(ctx context.Context, peerID string, r remote) (res exchangeResult, err error) {
	theirs, err := peerDigest(ctx, r)
	if err != nil {
		return res, err
	}

	// Which notes does the peer know more about than we do?
	var need []string
	err = s.db.NotesView(func(tx *store.NotesTx) error {
		ids := make([]string, 0, len(theirs))
		for id := range theirs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			for _, h := range theirs[id] {
				has, err := tx.HasRev(h)
				if err != nil {
					return err
				}
				if !has {
					need = append(need, id)
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	s.setSync(peerID, func(r *store.NoteSyncRow) { r.Behind = len(need) })

	// Pull. A note whose answer was cut short is asked again with an updated "have".
	for len(need) > 0 {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if !s.stillPaired(peerID) {
			return res, nil
		}
		n := len(need)
		if n > pullNotes {
			n = pullNotes
		}
		batch := need[:n]
		need = need[n:]
		for iter := 0; len(batch) > 0; iter++ {
			if iter > 1000 {
				return res, proto.Errorf(proto.CodeInternal, "the other device's answers do not end")
			}
			var req pullRequest
			if err := s.db.NotesView(func(tx *store.NotesTx) error {
				for _, id := range batch {
					have, err := haveFor(tx, id)
					if err != nil {
						return err
					}
					req.Items = append(req.Items, pullItem{NoteID: id, Have: have})
				}
				return nil
			}); err != nil {
				return res, err
			}
			rep, err := r.pull(ctx, req)
			if err != nil {
				return res, err
			}
			applied, err := s.applyWire(rep.Revs)
			if err != nil {
				return res, err
			}
			if err := firstUnsupported(applied); err != nil {
				return res, err
			}
			res.refused += len(applied.Rejected)
			if len(rep.More) == 0 {
				break
			}
			if applied.Inserted == 0 {
				return res, proto.Errorf(proto.CodeInternal, "the other device's answer made no progress")
			}
			batch = rep.More
		}
	}

	// Push what the peer lacks; this includes the merge revisions the pull may have made.
	if err := s.db.NotesView(func(tx *store.NotesTx) (err error) { res.ourSeq, err = tx.Seq(); return }); err != nil {
		return res, err
	}
	return res, s.push(ctx, peerID, r, theirs)
}

// firstUnsupported turns a rejection for a too-new format into the error that reports it.
func firstUnsupported(r pushReply) error {
	for _, rj := range r.Rejected {
		if rj.Code == string(proto.CodeUnsupportedFormat) {
			return proto.Errorf(proto.CodeUnsupportedFormat, "%s", rj.Reason)
		}
	}
	return nil
}

// haveFor names the newest revisions of a note we hold: its heads and their nearest ancestors.
func haveFor(tx *store.NotesTx, noteID string) ([]string, error) {
	g, err := loadGraph(tx, noteID)
	if err != nil {
		return nil, err
	}
	heads, err := tx.Heads(noteID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	queue := append([]string(nil), heads...)
	var out []string
	for len(queue) > 0 && len(out) < haveDepth {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		queue = append(queue, g.parents[id]...)
	}
	return out, nil
}

// push sends the peer every revision of the notes whose heads differ from the peer's, except those
// the peer's heads already cover.
func (s *Service) push(ctx context.Context, peerID string, r remote, theirs map[string][]string) error {
	var jobs []string
	err := s.db.NotesView(func(tx *store.NotesTx) error {
		after := ""
		for {
			d, err := tx.Digest(after, digestPage)
			if err != nil {
				return err
			}
			for _, n := range d {
				if !sameStrings(n.Heads, theirs[n.NoteID]) {
					jobs = append(jobs, n.NoteID)
				}
			}
			if len(d) < digestPage {
				return nil
			}
			after = d[len(d)-1].NoteID
		}
	})
	if err != nil {
		return err
	}
	s.setSync(peerID, func(r *store.NoteSyncRow) { r.Ahead = len(jobs) })

	var batch []WireRev
	budget := batchBudget{}
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		rep, err := r.push(ctx, pushRequest{Revs: batch})
		if err != nil {
			return err
		}
		if err := firstUnsupported(rep); err != nil {
			return err
		}
		batch, budget = nil, batchBudget{}
		return nil
	}
	for _, noteID := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !s.stillPaired(peerID) {
			return nil
		}
		var revs []store.NoteRev
		err := s.db.NotesView(func(tx *store.NotesTx) error {
			g, err := loadGraph(tx, noteID)
			if err != nil {
				return err
			}
			covered := ancestorsOf(g, theirs[noteID])
			want := map[string]struct{}{}
			for id := range g.parents {
				if _, ok := covered[id]; !ok {
					want[id] = struct{}{}
				}
			}
			for _, id := range topological(g, want) {
				r, found, err := tx.GetRev(id)
				if err != nil {
					return err
				}
				if found {
					revs = append(revs, r)
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, rev := range revs {
			if !budget.take(rev) {
				if err := flush(); err != nil {
					return err
				}
				budget.take(rev)
			}
			batch = append(batch, toWire(rev))
		}
	}
	return flush()
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// ---- status for the shells ----------------------------------------------------------------------------

// PeerStatus is one device in the notes sync list.
type PeerStatus struct {
	PeerID        string `json:"peer_id"`
	Name          string `json:"name"`
	Connected     bool   `json:"connected"`
	State         string `json:"state"`
	Detail        string `json:"detail,omitempty"`
	LastSyncAt    int64  `json:"last_sync_at"` // Unix milliseconds, 0 = never
	Behind        int    `json:"behind"`
	ConflictCount int    `json:"conflict_count"`
}

// StatusResult is notes.sync_status.
type StatusResult struct {
	Peers       []PeerStatus `json:"peers"`
	PendingPush int          `json:"pending_push"`
}

// Status lists every paired device with its notes sync state.
func (s *Service) Status() (StatusResult, error) {
	res := StatusResult{Peers: []PeerStatus{}}
	peers, err := s.db.ListPeers()
	if err != nil {
		return res, err
	}
	conn := map[string]bool{}
	if s.e != nil {
		for _, st := range s.e.PeerStatuses() {
			conn[st.DeviceID] = st.State == peer.StateConnected
		}
	}
	conflicts := 0
	_ = s.db.NotesView(func(tx *store.NotesTx) (err error) { conflicts, err = tx.CountConflictNotes(); return })
	for _, p := range peers {
		if p.State != "paired" {
			continue
		}
		row, _, _ := s.db.GetNoteSync(p.DeviceID)
		state := row.State
		if state == "" {
			state = StateIdle
		}
		res.Peers = append(res.Peers, PeerStatus{PeerID: p.DeviceID, Name: p.Name, Connected: conn[p.DeviceID], State: state, Detail: row.Detail,
			LastSyncAt: row.LastOKAt, Behind: row.Behind, ConflictCount: conflicts})
		res.PendingPush += row.Ahead
	}
	return res, nil
}
