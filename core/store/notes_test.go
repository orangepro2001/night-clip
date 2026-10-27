package store

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func noteRev(id, note string, parents ...string) NoteRev {
	return NoteRev{RevID: id, NoteID: note, Parents: parents, Title: "title " + id, Body: "body of " + id + "\n",
		BodyHash: "sha256:" + id, FormatVersion: 1, OriginDevice: "dev_aaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: 1000}
}

func mustInsert(t *testing.T, tx *NotesTx, r NoteRev) {
	t.Helper()
	if _, err := tx.InsertRev(r); err != nil {
		t.Fatalf("InsertRev %s: %v", r.RevID, err)
	}
}

func TestNotesRevisionRoundTripAndHeads(t *testing.T) {
	db := openTestDB(t)
	err := db.NotesTx(func(tx *NotesTx) error {
		mustInsert(t, tx, noteRev("r1", "nt_a"))
		mustInsert(t, tx, noteRev("r2", "nt_a", "r1"))
		heads, _ := tx.Heads("nt_a")
		if !reflect.DeepEqual(heads, []string{"r2"}) {
			t.Fatalf("heads after a chain = %v", heads)
		}
		// a fork and a merge: heads follow the DAG, not insertion order
		mustInsert(t, tx, noteRev("r3", "nt_a", "r1"))
		heads, _ = tx.Heads("nt_a")
		if !reflect.DeepEqual(heads, []string{"r2", "r3"}) {
			t.Fatalf("heads after a fork = %v", heads)
		}
		mustInsert(t, tx, noteRev("r4", "nt_a", "r3", "r2"))
		heads, _ = tx.Heads("nt_a")
		if !reflect.DeepEqual(heads, []string{"r4"}) {
			t.Fatalf("heads after a merge = %v", heads)
		}
		got, found, err := tx.GetRev("r4")
		if err != nil || !found {
			t.Fatalf("GetRev: %v %v", found, err)
		}
		if !reflect.DeepEqual(got.Parents, []string{"r2", "r3"}) || got.Title != "title r4" || got.Body != "body of r4\n" || got.FormatVersion != 1 {
			t.Fatalf("round trip = %+v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNotesInsertIsIdempotentAndNeedsParents(t *testing.T) {
	db := openTestDB(t)
	_ = db.NotesTx(func(tx *NotesTx) error {
		mustInsert(t, tx, noteRev("r1", "nt_a"))
		again, err := tx.InsertRev(noteRev("r1", "nt_a"))
		if err != nil || again {
			t.Fatalf("second insert = %v, %v; want a quiet no-op", again, err)
		}
		if _, err := tx.InsertRev(noteRev("r2", "nt_a", "r_unknown")); !errors.Is(err, ErrMissingParent) {
			t.Fatalf("unknown parent: %v", err)
		}
		// a parent of another note is as good as missing: revisions never cross notes
		mustInsert(t, tx, noteRev("x1", "nt_b"))
		if _, err := tx.InsertRev(noteRev("r3", "nt_a", "x1")); !errors.Is(err, ErrMissingParent) {
			t.Fatalf("parent of another note: %v", err)
		}
		if has, _ := tx.HasRev("r2"); has {
			t.Fatal("a refused revision must not be stored")
		}
		return nil
	})
}

func TestNotesRollbackKeepsNothing(t *testing.T) {
	db := openTestDB(t)
	boom := errors.New("boom")
	err := db.NotesTx(func(tx *NotesTx) error {
		mustInsert(t, tx, noteRev("r1", "nt_a"))
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	_ = db.NotesView(func(tx *NotesTx) error {
		if has, _ := tx.HasRev("r1"); has {
			t.Fatal("a failed transaction left a revision behind")
		}
		return nil
	})
}

func TestNotesTextIsSealedAtRest(t *testing.T) {
	db := openTestDB(t)
	secret := "correct horse battery staple"
	_ = db.NotesTx(func(tx *NotesTx) error {
		r := noteRev("r1", "nt_a")
		r.Title, r.Body = secret+" title", secret+" body\n"
		mustInsert(t, tx, r)
		return tx.PutNote(NoteRow{NoteID: "nt_a", CreatedAt: 1, UpdatedAt: 1, HeadRev: "r1", Title: secret + " title", Preview: secret + " preview"})
	})
	raw, err := db.sql.Query(`SELECT title_enc, body_enc FROM note_revs`)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for raw.Next() {
		var a, b []byte
		_ = raw.Scan(&a, &b)
		if bytes.Contains(a, []byte(secret)) || bytes.Contains(b, []byte(secret)) {
			t.Fatal("revision text is stored in clear")
		}
	}
	var title, preview []byte
	if err := db.sql.QueryRow(`SELECT title_enc, preview_enc FROM notes`).Scan(&title, &preview); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(title, []byte(secret)) || bytes.Contains(preview, []byte(secret)) {
		t.Fatal("note title/preview is stored in clear")
	}
}

func TestNotesSealedCellsCannotBeMovedBetweenRows(t *testing.T) {
	db := openTestDB(t)
	_ = db.NotesTx(func(tx *NotesTx) error {
		mustInsert(t, tx, noteRev("r1", "nt_a"))
		mustInsert(t, tx, noteRev("r2", "nt_b"))
		return nil
	})
	if _, err := db.sql.Exec(`UPDATE note_revs SET body_enc = (SELECT body_enc FROM note_revs WHERE rev_id = 'r1') WHERE rev_id = 'r2'`); err != nil {
		t.Fatal(err)
	}
	err := db.NotesView(func(tx *NotesTx) error {
		_, _, err := tx.GetRev("r2")
		return err
	})
	if err == nil {
		t.Fatal("a sealed cell copied to another row opened")
	}
}

func TestNotesListPagesNewestFirstAndFiltersTrash(t *testing.T) {
	db := openTestDB(t)
	_ = db.NotesTx(func(tx *NotesTx) error {
		for i := 0; i < 7; i++ {
			id := fmt.Sprintf("nt_%02d", i)
			mustInsert(t, tx, noteRev("r"+id, id))
			if err := tx.PutNote(NoteRow{NoteID: id, CreatedAt: int64(i), UpdatedAt: int64(i / 2), HeadRev: "r" + id, Title: id, Deleted: i == 3}); err != nil {
				t.Fatal(err)
			}
		}
		return nil
	})
	var got []string
	_ = db.NotesView(func(tx *NotesTx) error {
		var cur *NoteCursor
		for {
			rows, next, err := tx.ListNotes(false, 2, cur)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				got = append(got, r.NoteID)
			}
			if next == nil {
				break
			}
			cur = next
		}
		trash, _, _ := tx.ListNotes(true, 10, nil)
		if len(trash) != 1 || trash[0].NoteID != "nt_03" {
			t.Fatalf("trash = %+v", trash)
		}
		return nil
	})
	// updated_at desc, ties by id desc, nt_03 is in the trash
	want := []string{"nt_06", "nt_05", "nt_04", "nt_02", "nt_01", "nt_00"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("pages = %v, want %v", got, want)
	}
}

func TestNotesDigestListsHeadsPerNote(t *testing.T) {
	db := openTestDB(t)
	_ = db.NotesTx(func(tx *NotesTx) error {
		for _, n := range []string{"nt_c", "nt_a", "nt_b"} {
			mustInsert(t, tx, noteRev("r1"+n, n))
			_ = tx.PutNote(NoteRow{NoteID: n, HeadRev: "r1" + n})
		}
		mustInsert(t, tx, noteRev("r2nt_b", "nt_b", "r1nt_b"))
		mustInsert(t, tx, noteRev("r3nt_b", "nt_b", "r1nt_b")) // a fork
		return nil
	})
	_ = db.NotesView(func(tx *NotesTx) error {
		d, err := tx.Digest("", 2)
		if err != nil {
			t.Fatal(err)
		}
		want := []NoteHeads{{"nt_a", []string{"r1nt_a"}}, {"nt_b", []string{"r2nt_b", "r3nt_b"}}}
		if !reflect.DeepEqual(d, want) {
			t.Fatalf("first page = %+v", d)
		}
		d, _ = tx.Digest("nt_b", 2)
		if !reflect.DeepEqual(d, []NoteHeads{{"nt_c", []string{"r1nt_c"}}}) {
			t.Fatalf("second page = %+v", d)
		}
		return nil
	})
}

func TestNotesHistoryPagesNewestFirst(t *testing.T) {
	db := openTestDB(t)
	_ = db.NotesTx(func(tx *NotesTx) error {
		prev := ""
		for i := 1; i <= 5; i++ {
			id := fmt.Sprintf("r%d", i)
			if prev == "" {
				mustInsert(t, tx, noteRev(id, "nt_a"))
			} else {
				mustInsert(t, tx, noteRev(id, "nt_a", prev))
			}
			prev = id
		}
		return nil
	})
	_ = db.NotesView(func(tx *NotesTx) error {
		page, next, err := tx.RevPage("nt_a", 2, 0)
		if err != nil || next == 0 || len(page) != 2 || page[0].RevID != "r5" || page[1].RevID != "r4" || page[0].Title != "title r5" {
			t.Fatalf("page 1 = %+v next=%d err=%v", page, next, err)
		}
		page, next, _ = tx.RevPage("nt_a", 2, next)
		if len(page) != 2 || page[0].RevID != "r3" || next == 0 {
			t.Fatalf("page 2 = %+v next=%d", page, next)
		}
		page, next, _ = tx.RevPage("nt_a", 2, next)
		if len(page) != 1 || page[0].RevID != "r1" || next != 0 {
			t.Fatalf("page 3 = %+v next=%d", page, next)
		}
		return nil
	})
}

func TestNotesSeqGrowsWithEveryRevision(t *testing.T) {
	db := openTestDB(t)
	_ = db.NotesTx(func(tx *NotesTx) error {
		if n, _ := tx.Seq(); n != 0 {
			t.Fatalf("empty seq = %d", n)
		}
		mustInsert(t, tx, noteRev("r1", "nt_a"))
		mustInsert(t, tx, noteRev("r2", "nt_a", "r1"))
		mustInsert(t, tx, noteRev("r2", "nt_a", "r1")) // duplicate: no change
		if n, _ := tx.Seq(); n != 2 {
			t.Fatalf("seq = %d, want 2", n)
		}
		return nil
	})
}

func TestNotesSyncRowDefaultsAndCascade(t *testing.T) {
	db := openTestDB(t)
	p := Peer{DeviceID: "dev_aaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "x", Platform: "android", CertFP: "fp1", State: "paired", PairedAt: 1}
	if err := db.PutPeer(p); err != nil {
		t.Fatal(err)
	}
	row, found, err := db.GetNoteSync(p.DeviceID)
	if err != nil || found || row.State != "idle" || row.PeerSeq != -1 || row.OurSeq != -1 {
		t.Fatalf("default row = %+v found=%v err=%v", row, found, err)
	}
	row.State, row.PeerSeq, row.OurSeq, row.Behind, row.Ahead, row.LastOKAt = "error", 7, 9, 2, 3, 55
	if err := db.PutNoteSync(row); err != nil {
		t.Fatal(err)
	}
	got, found, _ := db.GetNoteSync(p.DeviceID)
	if !found || got != row {
		t.Fatalf("stored row = %+v", got)
	}
	// a peer that was already removed: bookkeeping quietly has nothing to attach to
	if err := db.PutNoteSync(NoteSyncRow{PeerID: "dev_zzzzzzzzzzzzzzzzzzzzzzzzzz"}); err != nil {
		t.Fatalf("row for an unknown peer: %v", err)
	}
	if err := db.DeletePeer(p.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := db.GetNoteSync(p.DeviceID); found {
		t.Fatal("deleting a peer left its notes sync row")
	}
}

func TestNotesMigrationIsInThePublishedChain(t *testing.T) {
	// A database from before notes (schema v6) upgrades in place and keeps its data.
	dir := t.TempDir()
	db := openAt(t, dir, testKey(1))
	if err := db.SetSetting("capture", "on"); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{`DROP TABLE note_blob_refs`, `DROP TABLE note_blobs`, `DROP TABLE note_peer_state`, `DROP TABLE note_revs`, `DROP TABLE notes`, `UPDATE schema_version SET version = 6`} {
		if _, err := db.sql.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	_ = db.Close()
	db2 := openAt(t, dir, testKey(1))
	if v, found, _ := db2.GetSetting("capture"); !found || v != "on" {
		t.Fatalf("setting after upgrade = %q %v", v, found)
	}
	if err := db2.NotesTx(func(tx *NotesTx) error {
		_, err := tx.InsertRev(noteRev("r1", "nt_a"))
		return err
	}); err != nil {
		t.Fatalf("notes tables after upgrade: %v", err)
	}
}
