package facade

import (
	"context"
	"crypto/ecdh"
	"errors"
	"strings"
	"time"
	"unicode"

	"moonpassword/core/vault"
	"moonpassword/core/vaultsync"
)

type syncPair struct {
	vault                       *vault.Vault
	subject, cap, nonce, public string
	key                         *ecdh.PrivateKey
	transcript                  vaultsync.Pair
	channel                     *vaultsync.PairChannel
	expires                     time.Time
	requester                   bool
	mismatches                  int
}

type pairView struct {
	State  string `json:"state"`
	Code   string `json:"code,omitempty"`
	Device string `json:"device,omitempty"`
}

func (s *cloudSync) pairCall(method, params string, binding *syncBinding) (any, error) {
	open := s.core.currentVault()
	if method == "sync.pair_cancel" {
		p := s.pair
		s.pair = nil
		if p != nil {
			ctx, done := context.WithTimeout(s.ctx, 4*time.Second)
			defer done()
			_, token, err := s.core.account.syncCredentials(ctx, p.subject, false)
			if err == nil {
				_, _ = vaultsync.PairCall(ctx, s.http, s.base, token, p.request("cancel"))
			}
		}
		return pairView{State: "cancelled"}, nil
	}
	if open == nil {
		s.pair = nil
		return nil, publicSyncError(vault.ErrLocked)
	}
	p := s.pair
	if p != nil && (p.vault != open || !time.Now().Before(p.expires)) {
		s.pair = nil
		return pairView{State: "expired"}, nil
	}
	subject := ""
	if p != nil {
		subject = p.subject
	} else if binding != nil {
		subject = binding.Subject
	}
	ctx, done := context.WithTimeout(s.ctx, 8*time.Second)
	defer done()
	subject, token, err := s.core.account.syncCredentials(ctx, subject, false)
	if err != nil {
		return nil, publicSyncError(err)
	}
	call := func(q vaultsync.PairRequest) (vaultsync.Pair, error) {
		return vaultsync.PairCall(ctx, s.http, s.base, token, q)
	}
	if p == nil {
		switch method {
		case "sync.pair_start", "sync.pair_prepare":
			requester := method == "sync.pair_start"
			if requester == (binding != nil) {
				return nil, syncError("PAIR_STATE", "incorrect vault state for pairing")
			}
			p, err = s.openPair(open, subject, requester, call)
			if err != nil {
				return nil, pairError(err)
			}
			s.pair = p
			return pairView{State: "waiting", Device: p.transcript.Name}, nil
		default:
			return pairView{State: "idle"}, nil
		}
	}
	if p.requester == (binding != nil) || (binding != nil && binding.Subject != p.subject) {
		s.pair = nil
		return nil, syncError("PAIR_STATE", "incorrect vault state for pairing")
	}
	if method == "sync.pair_approve" {
		var input struct {
			Code string `json:"code"`
		}
		if err := parseParams(params, &input); err != nil {
			return nil, err
		}
		if p.requester || p.channel == nil {
			return nil, syncError("PAIR_CONFIRM", "compare the current code on both devices first")
		}
		if input.Code != p.channel.Code {
			p.mismatches++
			if p.mismatches >= 3 {
				s.pair = nil
				_, _ = call(p.request("cancel"))
				return pairView{State: "cancelled"}, nil
			}
			return nil, syncError("PAIR_CONFIRM", "the comparison code did not match")
		}
		if s.core.currentVault() != open {
			s.pair = nil
			return nil, publicSyncError(vault.ErrLocked)
		}
		q := p.request("approve")
		q.Payload = p.channel.Seal(binding.Key)
		q.Signature = vaultsync.SignPair(subject, q.Pair, binding.Key)
		if _, err := call(q); err != nil {
			return nil, pairError(err)
		}
		s.pair = nil
		return pairView{State: "sent"}, nil
	}
	remote, err := call(p.request("status"))
	if errors.Is(err, vaultsync.ErrPairExpired) || errors.Is(err, vaultsync.ErrPairMissing) {
		s.pair = nil
		return pairView{State: "expired"}, nil
	}
	if err != nil {
		return nil, pairError(err)
	}
	if remote.State == "cancelled" {
		s.pair = nil
		return pairView{State: "cancelled"}, nil
	}
	if !p.accepts(remote) {
		s.pair = nil
		return nil, pairError(vaultsync.ErrInvalid)
	}
	if p.requester && remote.State == "preparing" {
		// Freeze the approver key shown before disclosing the committed key.
		p.transcript.Approver = remote.Approver
		q := p.request("reveal")
		q.Requester, q.Nonce = p.public, p.nonce
		remote, err = call(q)
		if err != nil {
			return nil, pairError(err)
		}
		if !p.accepts(remote) {
			s.pair = nil
			return nil, pairError(vaultsync.ErrInvalid)
		}
	}
	if remote.State != "compare" && remote.State != "approved" {
		return pairView{State: "waiting", Device: p.transcript.Name}, nil
	}
	if p.channel == nil {
		peer := remote.Requester
		if p.requester {
			peer = remote.Approver
		}
		p.channel, err = vaultsync.NewPairChannel(p.key, subject, remote, peer)
		if err != nil {
			s.pair = nil
			return nil, pairError(err)
		}
		p.transcript.Requester, p.transcript.Nonce = remote.Requester, remote.Nonce
	}
	if remote.State == "approved" && p.requester {
		key, err := p.channel.Open(remote.Payload)
		if err != nil {
			s.pair = nil
			return nil, pairError(err)
		}
		if s.core.currentVault() != open {
			s.pair = nil
			return nil, publicSyncError(vault.ErrLocked)
		}
		if _, err := s.enableFor("join", key, subject); err != nil {
			return nil, pairError(err)
		}
		s.pair = nil
		return pairView{State: "connected"}, nil
	}
	if remote.State == "approved" {
		s.pair = nil
		return pairView{State: "sent"}, nil
	}
	return pairView{State: "compare", Code: p.channel.Code, Device: p.transcript.Name}, nil
}

func (s *cloudSync) openPair(open *vault.Vault, subject string, requester bool, call func(vaultsync.PairRequest) (vaultsync.Pair, error)) (*syncPair, error) {
	key, public, err := vaultsync.NewPairKey()
	if err != nil {
		return nil, err
	}
	p := &syncPair{vault: open, subject: subject, requester: requester, cap: vaultsync.PairID(),
		key: key, public: public, expires: time.Now().Add(vaultsync.PairLifetime)}
	if requester {
		p.transcript.ID, p.nonce = vaultsync.PairID(), vaultsync.PairID()
		p.transcript.Commitment = vaultsync.PairCommit(p.transcript.ID, p.nonce, public)
		// Limit the display label independently of transport size, and omit bidi/control characters.
		for _, r := range strings.TrimSpace(s.core.cfg.DeviceName) {
			if !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r) && len(p.transcript.Name)+len(string(r)) <= 120 {
				p.transcript.Name += string(r)
			}
		}
		reply, err := call(p.request("start"))
		if err != nil {
			return nil, err
		}
		if !p.accepts(reply) || reply.State != "waiting" || reply.Approver != "" || reply.Requester != "" {
			return nil, vaultsync.ErrInvalid
		}
	} else {
		p.transcript, err = call(vaultsync.PairRequest{Action: "peek"})
		if err != nil {
			return nil, err
		}
		if p.transcript.State != "waiting" || p.transcript.ID == "" || p.transcript.Commitment == "" ||
			p.transcript.Requester != "" || p.transcript.Approver != "" {
			return nil, vaultsync.ErrInvalid
		}
		p.transcript.Approver = public
		reply, err := call(p.request("prepare"))
		if err != nil {
			return nil, err
		}
		if !p.accepts(reply) {
			return nil, vaultsync.ErrInvalid
		}
	}
	return p, nil
}

func (p *syncPair) request(action string) vaultsync.PairRequest {
	return vaultsync.PairRequest{Pair: p.transcript, Action: action, Capability: p.cap}
}

func (p *syncPair) accepts(q vaultsync.Pair) bool {
	if q.ID != p.transcript.ID || q.Commitment != p.transcript.Commitment || q.Name != p.transcript.Name ||
		(p.transcript.Approver != "" && q.Approver != p.transcript.Approver) ||
		(p.transcript.Requester != "" && q.Requester != p.transcript.Requester) {
		return false
	}
	if q.State == "compare" || q.State == "approved" {
		return q.Approver != "" && q.Requester != "" && (!p.requester || (q.Requester == p.public && q.Nonce == p.nonce)) &&
			vaultsync.PairCommit(q.ID, q.Nonce, q.Requester) == q.Commitment
	}
	return q.Requester == "" && q.Nonce == ""
}

func pairError(err error) error {
	switch {
	case errors.Is(err, vaultsync.ErrPairMissing):
		return syncError("PAIR_MISSING", "no pending pairing request")
	case errors.Is(err, vaultsync.ErrPairBusy):
		return syncError("PAIR_BUSY", "a recent pairing request already exists")
	case errors.Is(err, vaultsync.ErrPairExpired), errors.Is(err, vaultsync.ErrInvalid):
		return syncError("PAIR_INVALID", "pairing is no longer valid")
	default:
		return publicSyncError(err)
	}
}
