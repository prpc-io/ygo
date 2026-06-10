package ygo_test

import (
	"testing"

	"github.com/Deln0r/ygo"
)

// TestRelativePosition_FollowsUndoneDeletion confirms resolution walks
// the Redone chain: an anchor on a character that was deleted and then
// restored by undo resolves to the restored copy, not the boundary.
func TestRelativePosition_FollowsUndoneDeletion(t *testing.T) {
	d := ygo.NewDoc()
	txt := ygo.NewText(d, "t")
	txn := d.WriteTxn()
	if err := txt.Insert(txn, 0, "abc"); err != nil {
		t.Fatal(err)
	}
	txn.Commit()

	rpos, err := ygo.CreateRelativePositionFromTypeIndex(txt, 1, 0) // on 'b'
	if err != nil {
		t.Fatal(err)
	}

	um := ygo.NewUndoManager(d, txt)
	defer um.Close()

	txn = d.WriteTxn()
	if err := txt.Delete(txn, 1, 1); err != nil { // "ac"
		t.Fatal(err)
	}
	txn.Commit()

	abs, ok := ygo.CreateAbsolutePositionFromRelativePosition(d, rpos)
	if !ok || abs.Index != 1 {
		t.Fatalf("after delete: got (%d, %v), want (1, true)", abs.Index, ok)
	}

	if !um.Undo() { // restore 'b'
		t.Fatal("undo failed")
	}
	if got := txt.String(); got != "abc" {
		t.Fatalf("after undo text = %q, want %q", got, "abc")
	}
	abs, ok = ygo.CreateAbsolutePositionFromRelativePosition(d, rpos)
	if !ok || abs.Index != 1 {
		t.Errorf("after undo: got (%d, %v), want (1, true) via Redone chain", abs.Index, ok)
	}
}

// TestRelativePosition_MissingAssocDecodesAsZero confirms a truncated
// rpos (older producers omitted assoc) decodes with Assoc 0, matching
// yjs's hasContent guard.
func TestRelativePosition_MissingAssocDecodesAsZero(t *testing.T) {
	// tag 0 (item), client 21, clock 6 — and no assoc byte.
	rpos, err := ygo.DecodeRelativePosition([]byte{0x00, 0x15, 0x06})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rpos.Assoc != 0 {
		t.Errorf("Assoc = %d, want 0", rpos.Assoc)
	}
	if rpos.Item == nil || rpos.Item.Client != 21 || rpos.Item.Clock != 6 {
		t.Errorf("Item = %+v, want {21 6}", rpos.Item)
	}
}

// TestRelativePosition_EncodeEmptyAnchor confirms encoding a zero-value
// RelativePosition errors instead of emitting bytes a peer would
// misresolve.
func TestRelativePosition_EncodeEmptyAnchor(t *testing.T) {
	if _, err := ygo.EncodeRelativePosition(ygo.RelativePosition{}); err == nil {
		t.Error("encoding empty anchor succeeded, want error")
	}
}

// TestRelativePosition_UnseenAnchor confirms an anchor from a peer this
// replica has not synced with reports unresolvable rather than a bogus
// index.
func TestRelativePosition_UnseenAnchor(t *testing.T) {
	a := ygo.NewDoc()
	ta := ygo.NewText(a, "t")
	txn := a.WriteTxn()
	if err := ta.Insert(txn, 0, "hello"); err != nil {
		t.Fatal(err)
	}
	txn.Commit()
	rpos, err := ygo.CreateRelativePositionFromTypeIndex(ta, 2, 0)
	if err != nil {
		t.Fatal(err)
	}

	b := ygo.NewDoc() // never synced with a
	if _, ok := ygo.CreateAbsolutePositionFromRelativePosition(b, rpos); ok {
		t.Error("anchor from unseen peer resolved, want unresolvable")
	}
}
