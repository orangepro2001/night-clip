package store

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestNoteBlobRoundTripSealingAndDedup(t *testing.T) {
	db := openTestDB(t)
	id := strings.Repeat("ab", 32)
	secret := []byte("the picture's secret bytes")
	_ = db.NotesTx(func(tx *NotesTx) error {
		in, err := tx.PutBlob(NoteBlob{BlobID: id, Size: int64(len(secret)), Mime: "image/png", Name: "secret name.png", Data: secret, CreatedAt: 5})
		if err != nil || !in {
			t.Fatalf("PutBlob = %v, %v", in, err)
		}
		again, err := tx.PutBlob(NoteBlob{BlobID: id, Size: 1, Mime: "x/y", Name: "other", Data: []byte("zz")})
		if err != nil || again {
			t.Fatalf("storing the same id again = %v, %v: it must be a no-op", again, err)
		}
		b, found, err := tx.GetBlob(id, true)
		if err != nil || !found || b.Name != "secret name.png" || !bytes.Equal(b.Data, secret) || b.Mime != "image/png" || b.CreatedAt != 5 {
			t.Fatalf("GetBlob = %+v %v %v", b, found, err)
		}
		meta, _, _ := tx.GetBlob(id, false)
		if meta.Data != nil || meta.Size != int64(len(secret)) {
			t.Fatalf("metadata only = %+v", meta)
		}
		if _, found, _ := tx.GetBlob(strings.Repeat("00", 32), true); found {
			t.Fatal("found a blob that is not there")
		}
		m, _ := tx.StoredBlobs([]string{id, strings.Repeat("11", 32)})
		if !m[id] || len(m) != 1 {
			t.Fatalf("StoredBlobs = %v", m)
		}
		return nil
	})
	var name, data []byte
	if err := db.sql.QueryRow(`SELECT name_enc, data_enc FROM note_blobs`).Scan(&name, &data); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, secret) || bytes.Contains(name, []byte("secret name")) {
		t.Fatal("an attachment is stored in clear")
	}
	// a sealed cell moved to another blob does not open
	other := strings.Repeat("cd", 32)
	_ = db.NotesTx(func(tx *NotesTx) error {
		_, _ = tx.PutBlob(NoteBlob{BlobID: other, Size: 1, Mime: "a/b", Name: "n", Data: []byte("x")})
		return nil
	})
	if _, err := db.sql.Exec(`UPDATE note_blobs SET data_enc = (SELECT data_enc FROM note_blobs WHERE blob_id = ?) WHERE blob_id = ?`, id, other); err != nil {
		t.Fatal(err)
	}
	if err := db.NotesView(func(tx *NotesTx) error { _, _, err := tx.GetBlob(other, true); return err }); err == nil {
		t.Fatal("a sealed cell copied to another row opened")
	}
}

func TestNoteBlobReferencesAndListing(t *testing.T) {
	db := openTestDB(t)
	a, b := strings.Repeat("aa", 32), strings.Repeat("bb", 32)
	_ = db.NotesTx(func(tx *NotesTx) error {
		if err := tx.SetBlobRefs("nt_1", []string{b, a}); err != nil {
			t.Fatal(err)
		}
		if err := tx.SetBlobRefs("nt_2", []string{a}); err != nil {
			t.Fatal(err)
		}
		refs, _ := tx.ReferencedBlobs()
		if !reflect.DeepEqual(refs, []string{a, b}) {
			t.Fatalf("referenced = %v", refs)
		}
		// the list is replaced, not added to
		_ = tx.SetBlobRefs("nt_1", nil)
		refs, _ = tx.ReferencedBlobs()
		if !reflect.DeepEqual(refs, []string{a}) {
			t.Fatalf("referenced after dropping a link = %v", refs)
		}
		if ok, _ := tx.BlobReferenced(a); !ok {
			t.Fatal("a is still referenced")
		}
		if ok, _ := tx.BlobReferenced(b); ok {
			t.Fatal("b is not referenced any more")
		}
		_, _ = tx.PutBlob(NoteBlob{BlobID: a, Size: 1, Mime: "a/b", Name: "n", Data: []byte("1"), CreatedAt: 2})
		_, _ = tx.PutBlob(NoteBlob{BlobID: b, Size: 2, Mime: "a/b", Name: "n", Data: []byte("22"), CreatedAt: 1})
		infos, _ := tx.BlobInfos()
		if len(infos) != 2 || infos[0].BlobID != b || infos[1].BlobID != a || infos[0].Data != nil {
			t.Fatalf("BlobInfos = %+v", infos)
		}
		_ = tx.DeleteBlob(a)
		if m, _ := tx.StoredBlobs([]string{a, b}); m[a] || !m[b] {
			t.Fatalf("after delete: %v", m)
		}
		return nil
	})
}
