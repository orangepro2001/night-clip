package vaultsync

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPairRelayRejectsUnapprovedWritesAndExpires(t *testing.T) {
	service, err := NewServer(t.TempDir(), func(_ context.Context, token string) (string, error) {
		if token == "one" || token == "two" {
			return token, nil
		}
		return "", ErrAuth
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(service)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	recovery := strings.Repeat("A", 43)
	snapshot := make([]byte, 100)
	copy(snapshot, kdbx4Magic)
	rev, err := Put(ctx, server.Client(), server.URL, "one", "one", "", recovery, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	call := func(token, action, cap string, p Pair, signature string) (Pair, error) {
		return PairCall(ctx, server.Client(), server.URL, token, PairRequest{Pair: p, Action: action, Capability: cap, Signature: signature})
	}
	newKey, newPub, err := NewPairKey()
	if err != nil {
		t.Fatal(err)
	}
	oldKey, oldPub, err := NewPairKey()
	if err != nil {
		t.Fatal(err)
	}
	requester, approver, nonce := PairID(), PairID(), PairID()
	p := Pair{ID: PairID(), Name: "New device"}
	p.Commitment = PairCommit(p.ID, nonce, newPub)
	p, err = call("one", "start", requester, p, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := call("two", "peek", "", Pair{}, ""); !errors.Is(err, ErrPairMissing) {
		t.Fatalf("another account could inspect the request: %v", err)
	}
	p.Approver = oldPub
	if _, err := call("one", "prepare", approver, p, ""); err != nil {
		t.Fatal(err)
	}
	p.Requester, p.Nonce = newPub, PairID()
	if _, err := call("one", "reveal", requester, p, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("relay accepted a different commitment: %v", err)
	}
	p.Nonce = nonce
	p, err = call("one", "reveal", requester, p, "")
	if err != nil {
		t.Fatal(err)
	}
	oldChannel, err := NewPairChannel(oldKey, "one", p, newPub)
	if err != nil {
		t.Fatal(err)
	}
	newChannel, err := NewPairChannel(newKey, "one", p, oldPub)
	if err != nil || newChannel.Code != oldChannel.Code {
		t.Fatal("comparison code did not agree")
	}
	p.Payload = oldChannel.Seal(recovery)
	if _, err := call("one", "approve", approver, p, SignPair("one", p, "unrelated")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("OAuth alone could approve the transfer: %v", err)
	}
	if _, err := call("one", "approve", approver, p, SignPair("one", p, recovery)); err != nil {
		t.Fatal(err)
	}
	approved, err := call("one", "status", requester, p, "")
	if err != nil {
		t.Fatal(err)
	}
	if key, err := newChannel.Open(approved.Payload); err != nil || key != recovery || strings.Contains(approved.Payload, recovery) {
		t.Fatal("encrypted transfer did not preserve the recovery key")
	}
	if got, err := Get(ctx, server.Client(), server.URL, "one", rev); err != nil || !got.Unchanged {
		t.Fatalf("pairing changed the cloud snapshot: %v", err)
	}
	service.pairs.mu.Lock()
	service.pairs.requests["one"].ExpiresAt = time.Now().Add(-time.Second).Unix()
	service.pairs.mu.Unlock()
	if _, err := call("one", "status", requester, p, ""); !errors.Is(err, ErrPairExpired) {
		t.Fatalf("expired ciphertext was served: %v", err)
	}
}
