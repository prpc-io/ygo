package ygo_test

import (
	"bytes"
	"testing"

	"github.com/Deln0r/ygo"
)

// TestGC_FreesDeletedContent proves a default (GC-enabled) doc replaces
// deleted item content with a deleted marker, so the payload no longer
// rides on the wire, matching yjs's commit-time GC.
func TestGC_FreesDeletedContent(t *testing.T) {
	d := ygo.NewDoc()
	txt := ygo.NewText(d, "t")

	txn := d.WriteTxn()
	_ = txt.Insert(txn, 0, "secret")
	txn.Commit()

	txn = d.WriteTxn()
	_ = txt.Delete(txn, 0, 6) // delete it all
	txn.Commit()

	upd := ygo.EncodeStateAsUpdate(d)
	if bytes.Contains(upd, []byte("secret")) {
		t.Errorf("deleted content 'secret' still present in V1 update; GC did not free it")
	}
}

// TestGC_DisabledKeepsContent confirms a DisableGC doc retains deleted
// content (needed for snapshots / time-travel).
func TestGC_DisabledKeepsContent(t *testing.T) {
	d := ygo.NewDocWithOptions(ygo.Options{DisableGC: true})
	txt := ygo.NewText(d, "t")

	txn := d.WriteTxn()
	_ = txt.Insert(txn, 0, "secret")
	txn.Commit()
	txn = d.WriteTxn()
	_ = txt.Delete(txn, 0, 6)
	txn.Commit()

	upd := ygo.EncodeStateAsUpdate(d)
	if !bytes.Contains(upd, []byte("secret")) {
		t.Errorf("DisableGC doc dropped deleted content; snapshots would break")
	}
}

// TestGC_UndoOfDeletionSurvivesGC is the keep-interaction guard: with GC
// enabled, an UndoManager must still be able to undo a deletion (restore
// the content), which requires GC to skip the items it keeps.
func TestGC_UndoOfDeletionSurvivesGC(t *testing.T) {
	d := ygo.NewDoc() // GC enabled
	txt := ygo.NewText(d, "t")
	um := ygo.NewUndoManager(d, txt)
	defer um.Close()

	txn := d.WriteTxn()
	_ = txt.Insert(txn, 0, "hello")
	txn.Commit()
	um.StopCapturing()

	txn = d.WriteTxn()
	_ = txt.Delete(txn, 0, 5) // delete "hello"
	txn.Commit()
	if txt.String() != "" {
		t.Fatalf("after delete = %q, want empty", txt.String())
	}

	// Undo the deletion: content must come back even though GC ran.
	um.Undo()
	if got := txt.String(); got != "hello" {
		t.Errorf("after Undo of deletion = %q, want hello (GC freed kept content?)", got)
	}

	// Redo deletes again.
	um.Redo()
	if got := txt.String(); got != "" {
		t.Errorf("after Redo = %q, want empty", got)
	}
}

// TestGC_RemoteDeletedContentRoundTrips confirms that a GC'd update from
// one replica applies cleanly to another and converges.
func TestGC_RemoteDeletedContentRoundTrips(t *testing.T) {
	d1 := ygo.NewDoc()
	a := ygo.NewArray(d1, "a")
	txn := d1.WriteTxn()
	a.Insert(txn, 0, "x")
	a.Insert(txn, 1, "y")
	a.Insert(txn, 2, "z")
	txn.Commit()
	txn = d1.WriteTxn()
	a.Delete(txn, 1, 1) // delete "y" -> GC'd
	txn.Commit()

	d2 := ygo.NewDoc()
	if err := ygo.ApplyUpdate(d2, ygo.EncodeStateAsUpdate(d1)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := ygo.NewArray(d2, "a").ToSlice()
	if len(got) != 2 || got[0] != "x" || got[1] != "z" {
		t.Errorf("converged array = %v, want [x z]", got)
	}
}
