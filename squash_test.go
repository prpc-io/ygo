package ygo_test

import (
	"testing"

	"github.com/Deln0r/ygo"
)

// TestSquash_MergesSequentialInserts proves commit-time squash collapses
// same-client adjacent-clock inserts (the per-character Text.Insert
// pattern) into a single item, which is what removes the V1 per-item
// size overhead.
func TestSquash_MergesSequentialInserts(t *testing.T) {
	d := ygo.NewDoc()
	txt := ygo.NewText(d, "t")

	// Ten separate transactions, each appending one character.
	for i, ch := range "abcdefghij" {
		txn := d.WriteTxn()
		_ = txt.Insert(txn, uint64(i), string(ch))
		txn.Commit()
	}

	if got := txt.String(); got != "abcdefghij" {
		t.Fatalf("text = %q, want abcdefghij", got)
	}

	// After squash the ten inserts collapse to one item, so the V1
	// encoding is far smaller than ten separate item records would be.
	// A purely per-item encoding of 10 single-char items runs ~90+
	// bytes; the squashed single item is well under 40.
	v1 := ygo.EncodeStateAsUpdate(d)
	if len(v1) > 40 {
		t.Errorf("V1 update is %d bytes; squash did not merge the inserts", len(v1))
	}
}

// TestSquash_ApplyPartialOverlap is the convergence guard for the paired
// Apply-side slicing: a peer that already has a prefix of a now-squashed
// block must integrate only the unknown tail, not drop the whole block.
func TestSquash_ApplyPartialOverlap(t *testing.T) {
	d1 := ygo.NewDoc()
	t1 := ygo.NewText(d1, "t")

	txn := d1.WriteTxn()
	_ = t1.Insert(txn, 0, "abc")
	txn.Commit()
	prefixUpdate := ygo.EncodeStateAsUpdate(d1) // covers clocks [0,3)

	txn = d1.WriteTxn()
	_ = t1.Insert(txn, 3, "def")
	txn.Commit()
	// d1 now holds one squashed item covering [0,6) = "abcdef".
	fullUpdate := ygo.EncodeStateAsUpdate(d1)

	// d2 first learns the prefix, then receives the full squashed block,
	// which overlaps the clocks it already has.
	d2 := ygo.NewDoc()
	if err := ygo.ApplyUpdate(d2, prefixUpdate); err != nil {
		t.Fatalf("apply prefix: %v", err)
	}
	if got := ygo.NewText(d2, "t").String(); got != "abc" {
		t.Fatalf("after prefix d2 = %q, want abc", got)
	}
	if err := ygo.ApplyUpdate(d2, fullUpdate); err != nil {
		t.Fatalf("apply full: %v", err)
	}
	if got := ygo.NewText(d2, "t").String(); got != "abcdef" {
		t.Errorf("after partial-overlap apply d2 = %q, want abcdef (right half was dropped)", got)
	}
}

// TestSquash_UndoStillSplits guards the squash/undo interaction: with
// grouping off, two separate-transaction inserts that squash into one
// item must still undo one at a time (split-on-delete).
func TestSquash_UndoStillSplits(t *testing.T) {
	d := ygo.NewDoc()
	txt := ygo.NewText(d, "t")
	um := ygo.NewUndoManagerWithOptions(d, ygo.UndoManagerOptions{CaptureTimeout: -1}, txt)
	defer um.Close()

	txn := d.WriteTxn()
	_ = txt.Insert(txn, 0, "foo")
	txn.Commit()
	txn = d.WriteTxn()
	_ = txt.Insert(txn, 3, "bar")
	txn.Commit()
	if txt.String() != "foobar" {
		t.Fatalf("text = %q, want foobar", txt.String())
	}

	um.Undo() // reverses only "bar"
	if got := txt.String(); got != "foo" {
		t.Errorf("after one Undo = %q, want foo (squashed item must split)", got)
	}
	um.Undo() // reverses "foo"
	if got := txt.String(); got != "" {
		t.Errorf("after second Undo = %q, want empty", got)
	}
	um.Redo()
	um.Redo()
	if got := txt.String(); got != "foobar" {
		t.Errorf("after two Redos = %q, want foobar", got)
	}
}
