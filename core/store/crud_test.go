package store

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"nightclip/core/proto"
)

func samplePeer(id string) Peer {
	return Peer{
		DeviceID:     id,
		Name:         "name-" + id,
		Platform:     "windows",
		CertFP:       "fp-" + id,
		TailnetAddrs: []string{"100.64.0.1", "fd7a:115c:a1e0::1"},
		Port:         47821,
		Role:         "auto",
		State:        "paired",
		ApplyMode:    "notify",
		PairedAt:     1000,
		LastSeenAt:   2000,
	}
}

func mustPutPeer(t *testing.T, db *DB, id string) Peer {
	t.Helper()
	p := samplePeer(id)
	if err := db.PutPeer(p); err != nil {
		t.Fatalf("PutPeer %s: %v", id, err)
	}
	return p
}

func sampleOutbox(event, peer string, seq int64) OutboxRow {
	return OutboxRow{
		EventID:   event,
		PeerID:    peer,
		Sequence:  seq,
		Kind:      "clip.text",
		Envelope:  []byte(`{"event_id":"` + event + `","secret":"clipboard-secret-` + event + `"}`),
		CreatedAt: 100,
		ExpiresAt: 200,
	}
}

func sampleInbox(event, peer string, seq int64) InboxRow {
	return InboxRow{
		EventID:    event,
		PeerID:     peer,
		Sequence:   seq,
		Kind:       "clip.text",
		Payload:    []byte(`{"text":"payload-secret-` + event + `"}`),
		ReceivedAt: 100,
		ExpiresAt:  200,
	}
}

func TestPeerRoundTripAndUpsert(t *testing.T) {
	db := openTestDB(t)

	if _, found, err := db.GetPeer("dev_missing"); err != nil || found {
		t.Fatalf("GetPeer(missing) = found %v, err %v; want not found", found, err)
	}

	want := mustPutPeer(t, db, "dev_a")
	got, found, err := db.GetPeer("dev_a")
	if err != nil || !found {
		t.Fatalf("GetPeer: found %v, err %v", found, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("peer round trip:\n got %+v\nwant %+v", got, want)
	}

	want.Name = "renamed"
	want.State = "revoked"
	want.TailnetAddrs = []string{"100.64.0.9"}
	want.LastSeenAt = 3000
	if err := db.PutPeer(want); err != nil {
		t.Fatalf("PutPeer update: %v", err)
	}
	got, _, err = db.GetPeer("dev_a")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("peer after upsert:\n got %+v\nwant %+v", got, want)
	}
	all, err := db.ListPeers()
	if err != nil || len(all) != 1 {
		t.Fatalf("upsert must not duplicate: %d peers, err %v", len(all), err)
	}
}

func TestPeerNilAddrsStoredAsEmptyList(t *testing.T) {
	db := openTestDB(t)
	p := samplePeer("dev_a")
	p.TailnetAddrs = nil
	if err := db.PutPeer(p); err != nil {
		t.Fatal(err)
	}
	got, _, err := db.GetPeer("dev_a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.TailnetAddrs) != 0 {
		t.Fatalf("addrs = %v, want empty", got.TailnetAddrs)
	}
}

func TestPeerFingerprintLookupAndUniqueness(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")

	got, found, err := db.FindPeerByFingerprint("fp-dev_a")
	if err != nil || !found || got.DeviceID != "dev_a" {
		t.Fatalf("FindPeerByFingerprint = %+v, found %v, err %v", got, found, err)
	}
	if _, found, err := db.FindPeerByFingerprint("fp-unknown"); err != nil || found {
		t.Fatalf("unknown fingerprint: found %v, err %v", found, err)
	}

	// A fingerprint pins exactly one device.
	clash := samplePeer("dev_b")
	clash.CertFP = "fp-dev_a"
	if err := db.PutPeer(clash); err == nil {
		t.Fatal("two devices with the same fingerprint must be rejected")
	}
}

func TestPeerStateIsConstrained(t *testing.T) {
	db := openTestDB(t)
	p := samplePeer("dev_a")
	p.State = "bogus"
	if err := db.PutPeer(p); err == nil {
		t.Fatal("invalid peer state must be rejected")
	}
}

func TestListPeersOrderedByName(t *testing.T) {
	db := openTestDB(t)
	for _, c := range []struct{ id, name string }{{"dev_1", "zeta"}, {"dev_2", "alpha"}, {"dev_3", "mid"}} {
		p := samplePeer(c.id)
		p.Name = c.name
		if err := db.PutPeer(p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.ListPeers()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range got {
		names = append(names, p.Name)
	}
	if want := []string{"alpha", "mid", "zeta"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
}

func TestDeletePeerCascadesToLinkAndOutbox(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")
	mustPutPeer(t, db, "dev_b")
	for _, id := range []string{"dev_a", "dev_b"} {
		if err := db.SetLink(Link{PeerID: id, NextSeqOut: 5, RecvCursor: 4}); err != nil {
			t.Fatal(err)
		}
		if err := db.InsertOutbox(sampleOutbox("ev-"+id, id, 1)); err != nil {
			t.Fatal(err)
		}
	}

	if err := db.DeletePeer("dev_a"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := db.GetPeer("dev_a"); found {
		t.Fatal("peer survived DeletePeer")
	}
	if _, found, _ := db.GetLink("dev_a"); found {
		t.Fatal("link survived peer deletion")
	}
	if _, found, _ := db.GetOutbox("ev-dev_a"); found {
		t.Fatal("outbox row survived peer deletion")
	}
	if _, found, _ := db.GetLink("dev_b"); !found {
		t.Fatal("deleting dev_a removed dev_b's link")
	}
	if _, found, _ := db.GetOutbox("ev-dev_b"); !found {
		t.Fatal("deleting dev_a removed dev_b's outbox row")
	}
	if err := db.DeletePeer("dev_never_existed"); err != nil {
		t.Fatalf("deleting an absent peer must not fail: %v", err)
	}
}

func TestLinkDefaultsAndUpsert(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")

	l, found, err := db.GetLink("dev_a")
	if err != nil || found {
		t.Fatalf("fresh link: found %v, err %v", found, err)
	}
	if l.PeerID != "dev_a" || l.NextSeqOut != 1 || l.RecvCursor != 0 {
		t.Fatalf("default link = %+v, want {dev_a 1 0}", l)
	}

	if err := db.SetLink(Link{PeerID: "dev_a", NextSeqOut: 7, RecvCursor: 3, UpdatedAt: 50}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetLink(Link{PeerID: "dev_a", NextSeqOut: 8, RecvCursor: 4}); err != nil {
		t.Fatal(err)
	}
	l, found, err = db.GetLink("dev_a")
	if err != nil || !found {
		t.Fatalf("GetLink: found %v, err %v", found, err)
	}
	if l.NextSeqOut != 8 || l.RecvCursor != 4 {
		t.Fatalf("link after upsert = %+v", l)
	}
	if l.UpdatedAt == 0 || l.UpdatedAt == 50 {
		t.Fatalf("UpdatedAt = %d, want it defaulted to now on the second write", l.UpdatedAt)
	}
}

func TestLinkRequiresKnownPeer(t *testing.T) {
	db := openTestDB(t)
	if err := db.SetLink(Link{PeerID: "dev_ghost", NextSeqOut: 1}); err == nil {
		t.Fatal("link for an unknown peer must violate the foreign key")
	}
}

func TestOutboxRoundTripAndSealing(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")

	row := sampleOutbox("ev-1", "dev_a", 1)
	if err := db.InsertOutbox(row); err != nil {
		t.Fatal(err)
	}

	got, found, err := db.GetOutbox("ev-1")
	if err != nil || !found {
		t.Fatalf("GetOutbox: found %v, err %v", found, err)
	}
	if !bytes.Equal(got.Envelope, row.Envelope) {
		t.Fatalf("envelope mismatch:\n got %s\nwant %s", got.Envelope, row.Envelope)
	}
	if got.State != "queued" || got.Attempts != 0 || got.AckedAt != 0 || got.AckResult != "" {
		t.Fatalf("fresh row = %+v", got)
	}

	// The envelope must never be readable in the database file or via SQL.
	var raw []byte
	if err := db.sql.QueryRow(`SELECT envelope_enc FROM outbox WHERE event_id = 'ev-1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("clipboard-secret")) {
		t.Fatal("envelope stored in clear")
	}

	if _, found, err := db.GetOutbox("ev-missing"); err != nil || found {
		t.Fatalf("missing outbox row: found %v, err %v", found, err)
	}
}

func TestOutboxListIsOrderedFilteredAndLimited(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")
	mustPutPeer(t, db, "dev_b")
	// Insert out of order; list must come back by sequence.
	for _, seq := range []int64{3, 1, 2, 5, 4} {
		if err := db.InsertOutbox(sampleOutbox(fmt.Sprintf("a-%d", seq), "dev_a", seq)); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.InsertOutbox(sampleOutbox("b-1", "dev_b", 1)); err != nil {
		t.Fatal(err)
	}

	seqs := func(rows []OutboxRow) []int64 {
		var s []int64
		for _, r := range rows {
			s = append(s, r.Sequence)
		}
		return s
	}

	all, err := db.ListOutbox("dev_a", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 2, 3, 4, 5}; !reflect.DeepEqual(seqs(all), want) {
		t.Fatalf("all = %v, want %v", seqs(all), want)
	}
	after, err := db.ListOutbox("dev_a", 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{4, 5}; !reflect.DeepEqual(seqs(after), want) {
		t.Fatalf("after 3 = %v, want %v", seqs(after), want)
	}
	limited, err := db.ListOutbox("dev_a", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 2}; !reflect.DeepEqual(seqs(limited), want) {
		t.Fatalf("limit 2 = %v, want %v", seqs(limited), want)
	}
	other, err := db.ListOutbox("dev_b", 0, 0)
	if err != nil || len(other) != 1 || other[0].EventID != "b-1" {
		t.Fatalf("dev_b list = %+v, err %v", other, err)
	}
	for _, r := range all {
		if !bytes.Contains(r.Envelope, []byte(r.EventID)) {
			t.Fatalf("row %s carries the wrong envelope %s", r.EventID, r.Envelope)
		}
	}
}

func TestOutboxRejectsDuplicateSequenceAndEventID(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")
	if err := db.InsertOutbox(sampleOutbox("ev-1", "dev_a", 1)); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertOutbox(sampleOutbox("ev-2", "dev_a", 1)); err == nil {
		t.Fatal("a second event with the same (peer, sequence) must fail")
	}
	if err := db.InsertOutbox(sampleOutbox("ev-1", "dev_a", 2)); err == nil {
		t.Fatal("a second event with the same event_id must fail")
	}
	if err := db.InsertOutbox(sampleOutbox("ev-3", "dev_ghost", 1)); err == nil {
		t.Fatal("an event for an unknown peer must violate the foreign key")
	}
	rows, err := db.ListOutbox("dev_a", 0, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("failed inserts left rows behind: %d rows, err %v", len(rows), err)
	}
}

func TestOutboxLifecycle(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")
	if err := db.InsertOutbox(sampleOutbox("ev-1", "dev_a", 1)); err != nil {
		t.Fatal(err)
	}

	get := func() OutboxRow {
		t.Helper()
		r, found, err := db.GetOutbox("ev-1")
		if err != nil || !found {
			t.Fatalf("GetOutbox: found %v, err %v", found, err)
		}
		return r
	}

	if err := db.MarkOutboxSent("ev-1"); err != nil {
		t.Fatal(err)
	}
	if r := get(); r.State != "sent" || r.Attempts != 1 {
		t.Fatalf("after first send = %+v", r)
	}
	// A resend after reconnect counts as another attempt.
	if err := db.MarkOutboxSent("ev-1"); err != nil {
		t.Fatal(err)
	}
	if r := get(); r.State != "sent" || r.Attempts != 2 {
		t.Fatalf("after resend = %+v", r)
	}

	if err := db.MarkOutboxAcked("ev-1", "applied", 150); err != nil {
		t.Fatal(err)
	}
	r := get()
	if r.State != "acked" || r.AckedAt != 150 || r.AckResult != "applied" {
		t.Fatalf("after ack = %+v", r)
	}
	if !bytes.Contains(r.Envelope, []byte("clipboard-secret-ev-1")) {
		t.Fatal("ack must not disturb the sealed envelope")
	}

	// An acked row never goes back to sent.
	if err := db.MarkOutboxSent("ev-1"); err != nil {
		t.Fatal(err)
	}
	if r := get(); r.State != "acked" || r.Attempts != 2 {
		t.Fatalf("acked row was resent: %+v", r)
	}
}

func TestExpireOutbox(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")
	mk := func(event string, seq, expires int64) {
		t.Helper()
		r := sampleOutbox(event, "dev_a", seq)
		r.ExpiresAt = expires
		if err := db.InsertOutbox(r); err != nil {
			t.Fatal(err)
		}
	}
	mk("queued-old", 1, 100)
	mk("sent-old", 2, 100)
	mk("acked-old", 3, 100)
	mk("queued-fresh", 4, 500)
	mk("boundary", 5, 200)
	if err := db.MarkOutboxSent("sent-old"); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkOutboxAcked("acked-old", "applied", 90); err != nil {
		t.Fatal(err)
	}

	n, err := db.ExpireOutbox(200)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("expired %d rows, want 3 (queued-old, sent-old, boundary)", n)
	}
	state := func(id string) string {
		r, _, err := db.GetOutbox(id)
		if err != nil {
			t.Fatal(err)
		}
		return r.State
	}
	for id, want := range map[string]string{
		"queued-old":   "expired",
		"sent-old":     "expired",
		"boundary":     "expired",
		"acked-old":    "acked",
		"queued-fresh": "queued",
	} {
		if got := state(id); got != want {
			t.Errorf("%s state = %s, want %s", id, got, want)
		}
	}
	if n, err := db.ExpireOutbox(200); err != nil || n != 0 {
		t.Fatalf("second sweep changed %d rows, err %v; want 0", n, err)
	}
}

func TestOutboxStateIsConstrained(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")
	r := sampleOutbox("ev-1", "dev_a", 1)
	r.State = "bogus"
	if err := db.InsertOutbox(r); err == nil {
		t.Fatal("invalid outbox state must be rejected")
	}
}

func TestInboxDedupeByEventID(t *testing.T) {
	db := openTestDB(t)

	row := sampleInbox("ev-1", "dev_a", 1)
	inserted, err := db.InsertInbox(row)
	if err != nil || !inserted {
		t.Fatalf("first insert: inserted %v, err %v", inserted, err)
	}
	// Same event again — even with a different payload and sequence — is a dupe
	// and must leave the stored row untouched.
	again := row
	again.Sequence = 9
	again.Payload = []byte(`{"text":"tampered"}`)
	inserted, err = db.InsertInbox(again)
	if err != nil || inserted {
		t.Fatalf("duplicate insert: inserted %v, err %v; want false, nil", inserted, err)
	}

	got, found, err := db.GetInbox("ev-1")
	if err != nil || !found {
		t.Fatalf("GetInbox: found %v, err %v", found, err)
	}
	if !bytes.Equal(got.Payload, row.Payload) || got.Sequence != 1 {
		t.Fatalf("duplicate overwrote the stored event: %+v", got)
	}
	if got.State != "received" || got.AppliedAt != 0 {
		t.Fatalf("fresh inbox row = %+v", got)
	}

	if ok, err := db.HasInbox("ev-1"); err != nil || !ok {
		t.Fatalf("HasInbox(ev-1) = %v, %v", ok, err)
	}
	if ok, err := db.HasInbox("ev-2"); err != nil || ok {
		t.Fatalf("HasInbox(ev-2) = %v, %v", ok, err)
	}
	if _, found, err := db.GetInbox("ev-2"); err != nil || found {
		t.Fatalf("GetInbox(missing): found %v, err %v", found, err)
	}
}

func TestInboxSequenceConflictIsAnError(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.InsertInbox(sampleInbox("ev-1", "dev_a", 1)); err != nil {
		t.Fatal(err)
	}
	inserted, err := db.InsertInbox(sampleInbox("ev-2", "dev_a", 1))
	if err == nil || inserted {
		t.Fatalf("same (peer, sequence), different event: inserted %v, err %v; want an error", inserted, err)
	}
	// The same sequence from another peer is fine.
	if inserted, err := db.InsertInbox(sampleInbox("ev-3", "dev_b", 1)); err != nil || !inserted {
		t.Fatalf("other peer, same sequence: inserted %v, err %v", inserted, err)
	}
}

func TestInboxPayloadIsSealed(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.InsertInbox(sampleInbox("ev-1", "dev_a", 1)); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := db.sql.QueryRow(`SELECT payload_enc FROM inbox WHERE event_id = 'ev-1'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("payload-secret")) {
		t.Fatal("payload stored in clear")
	}
}

func TestInboxListNewestFirstWithLimit(t *testing.T) {
	db := openTestDB(t)
	for i, at := range []int64{300, 100, 200} {
		r := sampleInbox(fmt.Sprintf("ev-%d", i), "dev_a", int64(i+1))
		r.ReceivedAt = at
		if _, err := db.InsertInbox(r); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := db.ListInbox(0)
	if err != nil {
		t.Fatal(err)
	}
	var times []int64
	for _, r := range rows {
		times = append(times, r.ReceivedAt)
		if !bytes.Contains(r.Payload, []byte(r.EventID)) {
			t.Fatalf("row %s carries the wrong payload %s", r.EventID, r.Payload)
		}
	}
	if want := []int64{300, 200, 100}; !reflect.DeepEqual(times, want) {
		t.Fatalf("order = %v, want %v", times, want)
	}
	limited, err := db.ListInbox(2)
	if err != nil || len(limited) != 2 || limited[0].ReceivedAt != 300 {
		t.Fatalf("limit 2 = %d rows, err %v", len(limited), err)
	}
}

func TestInboxStateTransitions(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.InsertInbox(sampleInbox("ev-1", "dev_a", 1)); err != nil {
		t.Fatal(err)
	}

	if err := db.SetInboxState("ev-1", "applied", 500); err != nil {
		t.Fatal(err)
	}
	got, _, _ := db.GetInbox("ev-1")
	if got.State != "applied" || got.AppliedAt != 500 {
		t.Fatalf("after apply = %+v", got)
	}

	// A later state change without a timestamp keeps the earlier applied_at.
	if err := db.SetInboxState("ev-1", "ignored", 0); err != nil {
		t.Fatal(err)
	}
	got, _, _ = db.GetInbox("ev-1")
	if got.State != "ignored" || got.AppliedAt != 500 {
		t.Fatalf("after ignore = %+v; applied_at must be kept", got)
	}

	if err := db.SetInboxState("ev-1", "bogus", 0); err == nil {
		t.Fatal("invalid inbox state must be rejected")
	}
}

func TestExpireInboxOnlyTouchesUnappliedRows(t *testing.T) {
	db := openTestDB(t)
	for i, id := range []string{"received-old", "applied-old", "received-fresh"} {
		r := sampleInbox(id, "dev_a", int64(i+1))
		r.ExpiresAt = 100
		if id == "received-fresh" {
			r.ExpiresAt = 900
		}
		if _, err := db.InsertInbox(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.SetInboxState("applied-old", "applied", 50); err != nil {
		t.Fatal(err)
	}

	n, err := db.ExpireInbox(200)
	if err != nil || n != 1 {
		t.Fatalf("ExpireInbox = %d, %v; want 1", n, err)
	}
	for id, want := range map[string]string{
		"received-old":   "expired",
		"applied-old":    "applied",
		"received-fresh": "received",
	} {
		got, _, err := db.GetInbox(id)
		if err != nil || got.State != want {
			t.Errorf("%s state = %q (err %v), want %q", id, got.State, err, want)
		}
	}
}

// Ciphertext is bound to its row: copying it onto another event must not
// decrypt, and the failure is a stable code rather than garbage plaintext.
func TestSealedColumnsAreBoundToTheirRow(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")
	if err := db.InsertOutbox(sampleOutbox("ev-1", "dev_a", 1)); err != nil {
		t.Fatal(err)
	}
	if err := db.InsertOutbox(sampleOutbox("ev-2", "dev_a", 2)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertInbox(sampleInbox("in-1", "dev_a", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertInbox(sampleInbox("in-2", "dev_a", 2)); err != nil {
		t.Fatal(err)
	}

	if _, err := db.sql.Exec(
		`UPDATE outbox SET envelope_enc = (SELECT envelope_enc FROM outbox WHERE event_id = 'ev-1') WHERE event_id = 'ev-2'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.GetOutbox("ev-2"); errCode(t, err) != string(proto.CodeDBUnavailable) {
		t.Fatalf("GetOutbox with moved ciphertext: err = %v, want %s", err, proto.CodeDBUnavailable)
	}
	if _, err := db.ListOutbox("dev_a", 0, 0); errCode(t, err) != string(proto.CodeDBUnavailable) {
		t.Fatalf("ListOutbox with moved ciphertext: err = %v, want %s", err, proto.CodeDBUnavailable)
	}

	if _, err := db.sql.Exec(
		`UPDATE inbox SET payload_enc = (SELECT payload_enc FROM inbox WHERE event_id = 'in-1') WHERE event_id = 'in-2'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.GetInbox("in-2"); errCode(t, err) != string(proto.CodeDBUnavailable) {
		t.Fatalf("GetInbox with moved ciphertext: err = %v, want %s", err, proto.CodeDBUnavailable)
	}
	if _, err := db.ListInbox(0); errCode(t, err) != string(proto.CodeDBUnavailable) {
		t.Fatalf("ListInbox with moved ciphertext: err = %v, want %s", err, proto.CodeDBUnavailable)
	}
}

func TestSealedRowsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir, testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.PutPeer(samplePeer("dev_a")); err != nil {
		t.Fatal(err)
	}
	out := sampleOutbox("ev-1", "dev_a", 1)
	in := sampleInbox("in-1", "dev_a", 1)
	if err := db.InsertOutbox(out); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InsertInbox(in); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db2 := openAt(t, dir, testKey(1))
	gotOut, found, err := db2.GetOutbox("ev-1")
	if err != nil || !found || !bytes.Equal(gotOut.Envelope, out.Envelope) {
		t.Fatalf("outbox after reopen: %+v, found %v, err %v", gotOut, found, err)
	}
	gotIn, found, err := db2.GetInbox("in-1")
	if err != nil || !found || !bytes.Equal(gotIn.Payload, in.Payload) {
		t.Fatalf("inbox after reopen: %+v, found %v, err %v", gotIn, found, err)
	}
}

func TestSettingsGetSetDelete(t *testing.T) {
	db := openTestDB(t)

	if _, found, err := db.GetSetting("theme"); err != nil || found {
		t.Fatalf("unset key: found %v, err %v", found, err)
	}
	if err := db.SetSetting("theme", "dark"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting("theme", "light"); err != nil {
		t.Fatal(err)
	}
	if v, found, err := db.GetSetting("theme"); err != nil || !found || v != "light" {
		t.Fatalf("theme = %q, found %v, err %v", v, found, err)
	}
	// An empty value is a value, not an absent key.
	if err := db.SetSetting("empty", ""); err != nil {
		t.Fatal(err)
	}
	if v, found, err := db.GetSetting("empty"); err != nil || !found || v != "" {
		t.Fatalf("empty = %q, found %v, err %v", v, found, err)
	}
	if err := db.DeleteSetting("theme"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := db.GetSetting("theme"); found {
		t.Fatal("setting survived DeleteSetting")
	}
	if err := db.DeleteSetting("theme"); err != nil {
		t.Fatalf("deleting an absent key must not fail: %v", err)
	}
}

func TestClipStateRoundTrip(t *testing.T) {
	db := openTestDB(t)

	zero, err := db.GetClipState()
	if err != nil || zero != (ClipState{}) {
		t.Fatalf("fresh clip state = %+v, err %v; want zero value", zero, err)
	}

	want := ClipState{
		LastAppliedEventID: "ev-1",
		LastAppliedHash:    "sha256:aaaa",
		LastLocalSentHash:  "sha256:bbbb",
		SuppressUntil:      12345,
	}
	if err := db.SetClipState(want); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetClipState()
	if err != nil || got != want {
		t.Fatalf("clip state = %+v, err %v; want %+v", got, err, want)
	}

	want.LastAppliedEventID = "ev-2"
	want.SuppressUntil = 0
	if err := db.SetClipState(want); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetClipState()
	if err != nil || got != want {
		t.Fatalf("clip state after update = %+v, err %v; want %+v", got, err, want)
	}
	var rows int
	if err := db.sql.QueryRow(`SELECT count(*) FROM clip_state`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("clip_state has %d rows, err %v; want exactly 1", rows, err)
	}
}

func TestRevokedFingerprints(t *testing.T) {
	db := openTestDB(t)

	if ok, err := db.IsRevoked("fp-x"); err != nil || ok {
		t.Fatalf("fresh IsRevoked = %v, %v", ok, err)
	}
	if err := db.AddRevokedFP("fp-x", 10); err != nil {
		t.Fatal(err)
	}
	// Re-adding is a no-op and keeps the original timestamp.
	if err := db.AddRevokedFP("fp-x", 99); err != nil {
		t.Fatalf("re-adding a revoked fingerprint must not fail: %v", err)
	}
	if ok, err := db.IsRevoked("fp-x"); err != nil || !ok {
		t.Fatalf("IsRevoked(fp-x) = %v, %v", ok, err)
	}
	if ok, err := db.IsRevoked("fp-y"); err != nil || ok {
		t.Fatalf("IsRevoked(fp-y) = %v, %v", ok, err)
	}
	var at int64
	if err := db.sql.QueryRow(`SELECT revoked_at FROM revoked_fps WHERE cert_fp = 'fp-x'`).Scan(&at); err != nil || at != 10 {
		t.Fatalf("revoked_at = %d, err %v; want 10", at, err)
	}
}

// Revocation outlives the peer row: deleting the peer must not un-revoke it.
func TestRevocationSurvivesPeerDeletion(t *testing.T) {
	db := openTestDB(t)
	p := mustPutPeer(t, db, "dev_a")
	if err := db.AddRevokedFP(p.CertFP, 10); err != nil {
		t.Fatal(err)
	}
	if err := db.DeletePeer("dev_a"); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.IsRevoked(p.CertFP); err != nil || !ok {
		t.Fatalf("IsRevoked after DeletePeer = %v, %v; want true", ok, err)
	}
}

func TestEnqueueOutboxAllocatesSequencesAtomically(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")
	mustPutPeer(t, db, "dev_b")

	build := func(id string) func(int64) (OutboxRow, error) {
		return func(seq int64) (OutboxRow, error) {
			r := sampleOutbox(id, "ignored", 0)
			r.Envelope = []byte(fmt.Sprintf(`{"event_id":%q,"sequence":%d}`, id, seq))
			return r, nil
		}
	}
	for i, want := range []int64{1, 2, 3} {
		r, err := db.EnqueueOutbox("dev_a", build(fmt.Sprintf("ev-%d", i)))
		if err != nil || r.Sequence != want || r.PeerID != "dev_a" || r.State != "queued" {
			t.Fatalf("enqueue %d = %+v, %v; want sequence %d", i, r, err, want)
		}
		if !bytes.Contains(r.Envelope, []byte(fmt.Sprintf(`"sequence":%d`, want))) {
			t.Fatalf("build did not see the allocated sequence: %s", r.Envelope)
		}
	}
	if r, err := db.EnqueueOutbox("dev_b", build("other")); err != nil || r.Sequence != 1 {
		t.Fatalf("links are independent: %+v, %v", r, err)
	}
	l, _, _ := db.GetLink("dev_a")
	if l.NextSeqOut != 4 {
		t.Fatalf("next_seq_out = %d, want 4", l.NextSeqOut)
	}

	// A failing build consumes no sequence and stores nothing.
	if _, err := db.EnqueueOutbox("dev_a", func(int64) (OutboxRow, error) { return OutboxRow{}, errors.New("nope") }); err == nil {
		t.Fatal("build error swallowed")
	}
	// A failing insert (duplicate event_id) rolls the sequence back too.
	if _, err := db.EnqueueOutbox("dev_a", build("ev-0")); err == nil {
		t.Fatal("duplicate event_id accepted")
	}
	if l, _, _ := db.GetLink("dev_a"); l.NextSeqOut != 4 {
		t.Fatalf("a failed enqueue moved next_seq_out to %d", l.NextSeqOut)
	}
	if r, err := db.EnqueueOutbox("dev_a", build("ev-4")); err != nil || r.Sequence != 4 {
		t.Fatalf("next enqueue = %+v, %v; want sequence 4", r, err)
	}
	if _, err := db.EnqueueOutbox("dev_ghost", build("g")); err == nil {
		t.Fatal("enqueue for an unknown peer must violate the foreign key")
	}
}

func TestEnqueueOutboxConcurrentCallersNeverShareASequence(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")
	const n = 40
	var wg sync.WaitGroup
	seqs := make(chan int64, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := db.EnqueueOutbox("dev_a", func(seq int64) (OutboxRow, error) {
				x := sampleOutbox(fmt.Sprintf("ev-%d", i), "dev_a", seq)
				return x, nil
			})
			if err != nil {
				t.Error(err)
				return
			}
			seqs <- r.Sequence
		}(i)
	}
	wg.Wait()
	close(seqs)
	seen := map[int64]bool{}
	for s := range seqs {
		if seen[s] {
			t.Fatalf("sequence %d allocated twice", s)
		}
		seen[s] = true
	}
	for want := int64(1); want <= n; want++ {
		if !seen[want] {
			t.Fatalf("sequence %d missing (gaps: %v)", want, seen)
		}
	}
}

func TestSetRecvCursorOnlyMovesForwardAndKeepsNextSeq(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")
	if err := db.SetLink(Link{PeerID: "dev_a", NextSeqOut: 9, RecvCursor: 2}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ set, want int64 }{{5, 5}, {3, 5}, {5, 5}, {8, 8}} {
		if err := db.SetRecvCursor("dev_a", c.set); err != nil {
			t.Fatal(err)
		}
		l, _, _ := db.GetLink("dev_a")
		if l.RecvCursor != c.want || l.NextSeqOut != 9 {
			t.Fatalf("after SetRecvCursor(%d): %+v, want cursor %d next 9", c.set, l, c.want)
		}
	}
	// no prior link row: created with next_seq_out 1
	mustPutPeer(t, db, "dev_b")
	if err := db.SetRecvCursor("dev_b", 4); err != nil {
		t.Fatal(err)
	}
	if l, found, _ := db.GetLink("dev_b"); !found || l.RecvCursor != 4 || l.NextSeqOut != 1 {
		t.Fatalf("fresh link = %+v found %v", l, found)
	}
}

func TestPurgeOutboxAndPendingCount(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")
	mk := func(id string, seq int64, expires int64) {
		r := sampleOutbox(id, "dev_a", seq)
		r.ExpiresAt = expires
		if err := db.InsertOutbox(r); err != nil {
			t.Fatal(err)
		}
	}
	mk("acked-old", 1, 100)
	mk("expired-old", 2, 100)
	mk("expired-new", 3, 100)
	mk("queued", 4, 100000)
	mk("sent", 5, 100000)
	_ = db.MarkOutboxAcked("acked-old", "applied", 50)
	_ = db.MarkOutboxSent("sent")
	if _, err := db.ExpireOutbox(200); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkOutboxAcked("expired-new", "applied", 900); err != nil { // acked late: kept longer
		t.Fatal(err)
	}

	if n, err := db.CountPendingOutbox("dev_a"); err != nil || n != 2 {
		t.Fatalf("pending = %d, %v; want 2 (queued + sent)", n, err)
	}
	n, err := db.PurgeOutbox(500)
	if err != nil || n != 2 {
		t.Fatalf("purged %d, %v; want acked-old and expired-old", n, err)
	}
	for id, wantFound := range map[string]bool{"acked-old": false, "expired-old": false, "expired-new": true, "queued": true, "sent": true} {
		if _, found, _ := db.GetOutbox(id); found != wantFound {
			t.Errorf("%s found = %v, want %v", id, found, wantFound)
		}
	}
}

func TestTouchPeerAndSetPeerState(t *testing.T) {
	db := openTestDB(t)
	mustPutPeer(t, db, "dev_a")
	if err := db.TouchPeer("dev_a", []string{"100.64.0.7"}, 777); err != nil {
		t.Fatal(err)
	}
	p, _, _ := db.GetPeer("dev_a")
	if !reflect.DeepEqual(p.TailnetAddrs, []string{"100.64.0.7"}) || p.LastSeenAt != 777 {
		t.Fatalf("after touch = %+v", p)
	}
	if err := db.TouchPeer("dev_a", nil, 888); err != nil {
		t.Fatal(err)
	}
	p, _, _ = db.GetPeer("dev_a")
	if !reflect.DeepEqual(p.TailnetAddrs, []string{"100.64.0.7"}) || p.LastSeenAt != 888 {
		t.Fatalf("empty addrs must keep the stored ones: %+v", p)
	}
	if err := db.SetPeerState("dev_a", "revoked"); err != nil {
		t.Fatal(err)
	}
	if p, _, _ = db.GetPeer("dev_a"); p.State != "revoked" {
		t.Fatalf("state = %s", p.State)
	}
	if err := db.SetPeerState("dev_a", "bogus"); err == nil {
		t.Fatal("invalid state accepted")
	}
}

func TestInboxKeepsTheSensitiveFlag(t *testing.T) {
	db := openTestDB(t)
	for i, sens := range []bool{false, true} {
		r := sampleInbox(fmt.Sprintf("ev-%d", i), "dev_a", int64(i+1))
		r.Sensitive = sens
		if _, err := db.InsertInbox(r); err != nil {
			t.Fatal(err)
		}
		got, _, _ := db.GetInbox(r.EventID)
		if got.Sensitive != sens {
			t.Fatalf("GetInbox sensitive = %v, want %v", got.Sensitive, sens)
		}
	}
	rows, _ := db.ListInbox(0)
	flags := map[string]bool{}
	for _, r := range rows {
		flags[r.EventID] = r.Sensitive
	}
	if flags["ev-0"] || !flags["ev-1"] {
		t.Fatalf("ListInbox flags = %v", flags)
	}
}

func TestMigrationV2UpgradesAV1Database(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir, testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	// Turn the fresh database back into a v1 one.
	if _, err := db.sql.Exec(`ALTER TABLE inbox DROP COLUMN sensitive`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`UPDATE schema_version SET version = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.sql.Exec(`INSERT INTO inbox (event_id, peer_id, sequence, kind, payload_enc, received_at, expires_at, state)
		VALUES ('old', 'dev_a', 1, 'clipboard', x'00', 1, 2, 'received')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	db2 := openAt(t, dir, testKey(1))
	var sensitive int
	if err := db2.sql.QueryRow(`SELECT sensitive FROM inbox WHERE event_id = 'old'`).Scan(&sensitive); err != nil || sensitive != 0 {
		t.Fatalf("existing rows must default to not sensitive: %d, %v", sensitive, err)
	}
	var version int
	_ = db2.sql.QueryRow(`SELECT version FROM schema_version`).Scan(&version)
	if version != len(migrations) {
		t.Fatalf("version = %d", version)
	}
}

func TestPruneAndClearInbox(t *testing.T) {
	db := openTestDB(t)
	for i := 0; i < 6; i++ {
		r := sampleInbox(fmt.Sprintf("ev-%d", i), "dev_a", int64(i+1))
		r.ReceivedAt = int64(100 + i)
		if _, err := db.InsertInbox(r); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := db.PruneInbox(0); err != nil || n != 0 {
		t.Fatalf("PruneInbox(0) = %d, %v; must delete nothing", n, err)
	}
	n, err := db.PruneInbox(4)
	if err != nil || n != 2 {
		t.Fatalf("PruneInbox(4) = %d, %v", n, err)
	}
	for _, gone := range []string{"ev-0", "ev-1"} {
		if ok, _ := db.HasInbox(gone); ok {
			t.Errorf("%s should have been pruned (oldest)", gone)
		}
	}
	if ok, _ := db.HasInbox("ev-5"); !ok {
		t.Error("newest row pruned")
	}
	if err := db.ClearInbox(); err != nil {
		t.Fatal(err)
	}
	if rows, _ := db.ListInbox(0); len(rows) != 0 {
		t.Fatalf("%d rows after ClearInbox", len(rows))
	}
}
