package encoding_test

import (
	"testing"

	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/encoding"
	"github.com/Deln0r/ygo/internal/types"
)

// TestDeleteSet_SplitAtStart_PreservesPrefix exercises the case
// where the wire delete-set range starts AFTER the existing item's
// start clock. Apply must split at the range start and tombstone
// only the right side.
func TestDeleteSet_SplitAtStart_PreservesPrefix(t *testing.T) {
	a := doc.NewDocWithOptions(doc.Options{ClientID: 1})
	at := types.NewText(a.Branch("body"))
	w := a.WriteTxn()
	_ = at.Insert(w, 0, "PRESERVE_DELETE_THIS")
	w.Commit()

	b := doc.NewDocWithOptions(doc.Options{ClientID: 2})
	if err := encoding.ApplyUpdate(b, encoding.EncodeStateAsUpdate(a)); err != nil {
		t.Fatal(err)
	}
	bt := types.NewText(b.Branch("body"))
	w = b.WriteTxn()
	if err := bt.Delete(w, 8, 12); err != nil { // delete "DELETE_THIS"
		t.Fatal(err)
	}
	w.Commit()
	if got, want := bt.String(), "PRESERVE"; got != want {
		t.Fatalf("B local: String = %q, want %q", got, want)
	}

	// A receives — must split A's contiguous item at clock 8 and
	// tombstone the right side only.
	if err := encoding.ApplyUpdate(a, encoding.EncodeStateAsUpdate(b)); err != nil {
		t.Fatal(err)
	}
	if got, want := at.String(), "PRESERVE"; got != want {
		t.Errorf("A after partial-tail delete: String = %q, want %q", got, want)
	}
}

// TestDeleteSet_SplitAtEnd_PreservesSuffix is the symmetric case.
func TestDeleteSet_SplitAtEnd_PreservesSuffix(t *testing.T) {
	a := doc.NewDocWithOptions(doc.Options{ClientID: 3})
	at := types.NewText(a.Branch("body"))
	w := a.WriteTxn()
	_ = at.Insert(w, 0, "DELETE_THIS_PRESERVE")
	w.Commit()

	b := doc.NewDocWithOptions(doc.Options{ClientID: 4})
	if err := encoding.ApplyUpdate(b, encoding.EncodeStateAsUpdate(a)); err != nil {
		t.Fatal(err)
	}
	bt := types.NewText(b.Branch("body"))
	w = b.WriteTxn()
	if err := bt.Delete(w, 0, 12); err != nil { // delete "DELETE_THIS_"
		t.Fatal(err)
	}
	w.Commit()
	if got, want := bt.String(), "PRESERVE"; got != want {
		t.Fatalf("B local: String = %q, want %q", got, want)
	}

	if err := encoding.ApplyUpdate(a, encoding.EncodeStateAsUpdate(b)); err != nil {
		t.Fatal(err)
	}
	if got, want := at.String(), "PRESERVE"; got != want {
		t.Errorf("A after partial-prefix delete: String = %q, want %q", got, want)
	}
}

// TestDeleteSet_SplitAtBothEnds_PreservesMiddleSlices is the dual-
// boundary case: A has "AAAA_DELETE_BBBB" as one Item; B deletes
// the middle "_DELETE_" leaving "AAAA" and "BBBB". A's apply must
// split at both 4 and 12 to keep the prefix and suffix.
func TestDeleteSet_SplitAtBothEnds_PreservesMiddleSlices(t *testing.T) {
	a := doc.NewDocWithOptions(doc.Options{ClientID: 5})
	at := types.NewText(a.Branch("body"))
	w := a.WriteTxn()
	_ = at.Insert(w, 0, "AAAA_DELETE_BBBB")
	w.Commit()

	b := doc.NewDocWithOptions(doc.Options{ClientID: 6})
	if err := encoding.ApplyUpdate(b, encoding.EncodeStateAsUpdate(a)); err != nil {
		t.Fatal(err)
	}
	bt := types.NewText(b.Branch("body"))
	w = b.WriteTxn()
	if err := bt.Delete(w, 4, 8); err != nil { // delete "_DELETE_"
		t.Fatal(err)
	}
	w.Commit()
	if got, want := bt.String(), "AAAABBBB"; got != want {
		t.Fatalf("B local: String = %q, want %q", got, want)
	}

	if err := encoding.ApplyUpdate(a, encoding.EncodeStateAsUpdate(b)); err != nil {
		t.Fatal(err)
	}
	if got, want := at.String(), "AAAABBBB"; got != want {
		t.Errorf("A after dual-boundary delete: String = %q, want %q (prefix+suffix preserved)", got, want)
	}
}

// TestDeleteSet_WholeItem_NoSplitNeeded sanity-checks the
// degenerate case where the delete range exactly matches the
// item bounds — split helpers should still produce the right
// result.
func TestDeleteSet_WholeItem_NoSplitNeeded(t *testing.T) {
	a := doc.NewDocWithOptions(doc.Options{ClientID: 7})
	at := types.NewText(a.Branch("body"))
	w := a.WriteTxn()
	_ = at.Insert(w, 0, "KEEP_DROP")
	_ = at.Insert(w, 9, "KEEP2")
	w.Commit()

	b := doc.NewDocWithOptions(doc.Options{ClientID: 8})
	if err := encoding.ApplyUpdate(b, encoding.EncodeStateAsUpdate(a)); err != nil {
		t.Fatal(err)
	}
	bt := types.NewText(b.Branch("body"))
	w = b.WriteTxn()
	if err := bt.Delete(w, 5, 4); err != nil { // delete "DROP"
		t.Fatal(err)
	}
	w.Commit()

	if err := encoding.ApplyUpdate(a, encoding.EncodeStateAsUpdate(b)); err != nil {
		t.Fatal(err)
	}
	if got, want := at.String(), "KEEP_KEEP2"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
