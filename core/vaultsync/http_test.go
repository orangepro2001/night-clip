package vaultsync

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
)

func TestConditionalWritesAndAccountIsolation(t *testing.T) {
	svc, err := NewServer(t.TempDir(), func(_ context.Context, token string) (string, error) {
		if token == "account-a" || token == "account-b" {
			return token, nil
		}
		return "", ErrAuth
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(svc)
	defer server.Close()
	ctx := context.Background()
	newData := func(marker byte) []byte {
		data := make([]byte, 100)
		copy(data, kdbx4Magic)
		data[99] = marker
		return data
	}
	const recoveryKey = "test-recovery-key"
	first, err := Put(ctx, server.Client(), server.URL, "account-a", "account-a", "", recoveryKey, newData(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Put(ctx, server.Client(), server.URL, "account-a", "account-a", "", recoveryKey, newData(2)); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate create: %v", err)
	}
	if _, err := Put(ctx, server.Client(), server.URL, "account-a", "account-a", first, "wrong", newData(2)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an account alone could overwrite an existing snapshot: %v", err)
	}
	results := make(chan error, 2)
	for _, marker := range []byte{3, 4} {
		go func() {
			_, err := Put(ctx, server.Client(), server.URL, "account-a", "account-a", first, recoveryKey, newData(marker))
			results <- err
		}()
	}
	succeeded, conflicts := 0, 0
	for range 2 {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicts != 1 {
		t.Fatalf("%d writes, %d conflicts", succeeded, conflicts)
	}
	if snap, err := Get(ctx, server.Client(), server.URL, "account-b", ""); err != nil || snap.Revision != "" {
		t.Fatalf("another subject could see the snapshot: %v", err)
	}
	if _, err := Get(ctx, server.Client(), server.URL, "invalid", ""); !errors.Is(err, ErrAuth) {
		t.Fatalf("unauthenticated read: %v", err)
	}
}
