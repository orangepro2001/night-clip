package vaultsync

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"
)

const PairPath = "/v1/pair"
const PairLifetime = 5 * time.Minute

var ErrPairExpired = errors.New("pairing expired")
var ErrPairBusy = errors.New("pairing already exists")
var ErrPairMissing = errors.New("no pending pairing")

// Pair is the relay's public transcript. Payload is returned only to the
// requester presenting its per-attempt capability.
type Pair struct {
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	Commitment string `json:"commitment,omitempty"`
	Requester  string `json:"requester,omitempty"`
	Approver   string `json:"approver,omitempty"`
	Nonce      string `json:"nonce,omitempty"`
	Payload    string `json:"payload,omitempty"`
	State      string `json:"state,omitempty"`
	ExpiresAt  int64  `json:"expires_at,omitempty"`
}

type PairRequest struct {
	Pair
	Action     string `json:"action"`
	Capability string `json:"capability,omitempty"`
	Signature  string `json:"signature,omitempty"`
}

func PairCall(ctx context.Context, hc *http.Client, base, token string, action PairRequest) (Pair, error) {
	body, err := json.Marshal(action)
	if err != nil {
		return Pair{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+PairPath, bytes.NewReader(body))
	if err != nil {
		return Pair{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return Pair{}, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.Header.Get(protocolHeader) != "1" {
		return Pair{}, ErrUnavailable
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return Pair{}, ErrAuth
	case http.StatusNotFound:
		return Pair{}, ErrPairMissing
	case http.StatusGone:
		return Pair{}, ErrPairExpired
	case http.StatusConflict:
		return Pair{}, ErrPairBusy
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return Pair{}, ErrInvalid
	case http.StatusOK:
		var pair Pair
		if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4096)).Decode(&pair); err != nil {
			return Pair{}, ErrInvalid
		}
		return pair, nil
	default:
		return Pair{}, ErrUnavailable
	}
}

type pairRelay struct {
	mu       sync.Mutex
	requests map[string]*pairEntry
}

type pairEntry struct {
	Pair
	requesterCap string
	approverCap  string
}

func (s *Server) pairHTTP(w http.ResponseWriter, r *http.Request, subject, path string) {
	var q PairRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&q); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.pairs.mu.Lock()
	defer s.pairs.mu.Unlock()
	p, status := s.pairs.handle(subject, path, q, time.Now())
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(p)
}

func (r *pairRelay) handle(subject, path string, q PairRequest, now time.Time) (Pair, int) {
	p := r.requests[subject]
	if p != nil && now.Unix() >= p.ExpiresAt {
		delete(r.requests, subject)
		p = nil
	}
	if q.Action == "start" {
		return r.start(subject, path, p, q, now)
	}
	if p == nil {
		if q.ID != "" {
			return Pair{}, http.StatusGone
		}
		return Pair{}, http.StatusNotFound
	}
	if q.Action == "peek" {
		if p.State != "waiting" {
			return Pair{}, http.StatusNotFound
		}
		out := p.Pair
		out.Payload = ""
		return out, http.StatusOK
	}
	if q.ID != p.ID {
		return Pair{}, http.StatusGone
	}
	requester := q.Capability != "" && q.Capability == p.requesterCap
	approver := q.Capability != "" && q.Capability == p.approverCap
	switch q.Action {
	case "prepare":
		if pairBytes(q.Capability, 24) == nil || pairBytes(q.Approver, 32) == nil || requester {
			return Pair{}, http.StatusBadRequest
		}
		if p.State == "waiting" {
			p.State, p.Approver, p.approverCap = "preparing", q.Approver, q.Capability
		} else if !approver || q.Approver != p.Approver {
			return Pair{}, http.StatusConflict
		}
	case "reveal":
		if !requester || pairBytes(q.Requester, 32) == nil || pairBytes(q.Nonce, 24) == nil ||
			PairCommit(p.ID, q.Nonce, q.Requester) != p.Commitment {
			return Pair{}, http.StatusBadRequest
		}
		if p.State == "preparing" {
			p.State, p.Requester, p.Nonce = "compare", q.Requester, q.Nonce
		} else if p.Requester != q.Requester || p.Nonce != q.Nonce {
			return Pair{}, http.StatusConflict
		}
	case "approve":
		if !approver || p.State != "compare" || pairBytes(q.Payload, pairCipherSize) == nil {
			return Pair{}, http.StatusConflict
		}
		public, _, err := readRecord(path)
		if err != nil {
			return Pair{}, http.StatusServiceUnavailable
		}
		out := p.Pair
		out.Payload = q.Payload
		if !ed25519.Verify(public, pairApproval(subject, out), pairBytes(q.Signature, ed25519.SignatureSize)) {
			return Pair{}, http.StatusUnprocessableEntity
		}
		p.State, p.Payload = "approved", q.Payload
	case "cancel":
		if !requester && !approver {
			return Pair{}, http.StatusNotFound
		}
		// Retain a tombstone until expiry to bound repeated comparison attempts.
		if p.State != "approved" || requester {
			p.State, p.Payload = "cancelled", ""
		}
	case "status":
		if !requester && !approver {
			return Pair{}, http.StatusNotFound
		}
	default:
		return Pair{}, http.StatusBadRequest
	}
	out := p.Pair
	if !requester {
		out.Payload = ""
	}
	return out, http.StatusOK
}

func (r *pairRelay) start(subject, path string, p *pairEntry, q PairRequest, now time.Time) (Pair, int) {
	if pairBytes(q.ID, 24) == nil || pairBytes(q.Capability, 24) == nil || pairBytes(q.Commitment, 32) == nil ||
		len(q.Name) > 160 {
		return Pair{}, http.StatusBadRequest
	}
	if p != nil {
		if p.ID == q.ID && p.requesterCap == q.Capability && p.Commitment == q.Commitment {
			return p.Pair, http.StatusOK
		}
		return Pair{}, http.StatusConflict
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return Pair{}, http.StatusNotFound
	} else if err != nil {
		return Pair{}, http.StatusServiceUnavailable
	}
	for sub, stale := range r.requests {
		if now.Unix() >= stale.ExpiresAt {
			delete(r.requests, sub)
		}
	}
	if len(r.requests) >= 1024 {
		return Pair{}, http.StatusServiceUnavailable
	}
	if r.requests == nil {
		r.requests = make(map[string]*pairEntry)
	}
	result := Pair{ID: q.ID, Name: q.Name, State: "waiting", Commitment: q.Commitment, ExpiresAt: now.Add(PairLifetime).Unix()}
	r.requests[subject] = &pairEntry{Pair: result, requesterCap: q.Capability}
	return result, http.StatusOK
}
