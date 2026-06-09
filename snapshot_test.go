package ygo_test

import (
	"testing"

	"github.com/Deln0r/ygo"
)

// gcOff returns a doc with GC disabled, the prerequisite for snapshots.
func gcOff() *ygo.Doc {
	return ygo.NewDocWithOptions(ygo.Options{DisableGC: true})
}

// TestSnapshot_RestoreText is the canonical yjs example: snapshot a
// doc, mutate it, then reconstruct the snapshot state.
func TestSnapshot_RestoreText(t *testing.T) {
	d := gcOff()
	txt := ygo.NewText(d, "t")

	txn := d.WriteTxn()
	_ = txt.Insert(txn, 0, "world!")
	txn.Commit()

	snap := ygo.CreateSnapshot(d)

	txn = d.WriteTxn()
	_ = txt.Insert(txn, 0, "hello ")
	txn.Commit()
	if got := txt.String(); got != "hello world!" {
		t.Fatalf("after second insert: %q", got)
	}

	restored, err := ygo.RestoreSnapshot(d, snap)
	if err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	rtxt := ygo.NewText(restored, "t")
	if got := rtxt.String(); got != "world!" {
		t.Errorf("restored text = %q, want %q", got, "world!")
	}
	// The live doc is unchanged.
	if got := txt.String(); got != "hello world!" {
		t.Errorf("source mutated: %q", got)
	}
}

// TestSnapshot_RestoreMap checks map state reconstruction across a
// snapshot boundary, including a key added after the snapshot.
func TestSnapshot_RestoreMap(t *testing.T) {
	d := gcOff()
	m := ygo.NewMap(d, "m")

	txn := d.WriteTxn()
	m.Set(txn, "a", "1")
	m.Set(txn, "b", "2")
	txn.Commit()

	snap := ygo.CreateSnapshot(d)

	txn = d.WriteTxn()
	m.Set(txn, "c", "3") // added after snapshot
	m.Delete(txn, "a")   // deleted after snapshot
	txn.Commit()

	restored, err := ygo.RestoreSnapshot(d, snap)
	if err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	rm := ygo.NewMap(restored, "m")
	if got := rm.Get("a"); got != "1" {
		t.Errorf("restored a = %v, want 1 (deletion after snapshot must not apply)", got)
	}
	if got := rm.Get("b"); got != "2" {
		t.Errorf("restored b = %v, want 2", got)
	}
	if got := rm.Get("c"); got != nil {
		t.Errorf("restored c = %v, want nil (added after snapshot)", got)
	}
}

// TestSnapshot_RestoreArray checks array reconstruction with a delete
// that happened before the snapshot (must be reflected) and inserts
// after (must not).
func TestSnapshot_RestoreArray(t *testing.T) {
	d := gcOff()
	a := ygo.NewArray(d, "a")

	txn := d.WriteTxn()
	a.Insert(txn, 0, "x")
	a.Insert(txn, 1, "y")
	a.Insert(txn, 2, "z")
	a.Delete(txn, 1, 1) // delete "y" before snapshot
	txn.Commit()

	snap := ygo.CreateSnapshot(d)

	txn = d.WriteTxn()
	a.Insert(txn, 2, "w") // after snapshot
	txn.Commit()

	restored, err := ygo.RestoreSnapshot(d, snap)
	if err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	ra := ygo.NewArray(restored, "a")
	got := ra.ToSlice()
	if len(got) != 2 || got[0] != "x" || got[1] != "z" {
		t.Errorf("restored array = %v, want [x z]", got)
	}
}

// TestSnapshot_RestoreRequiresGCDisabled confirms the guard.
func TestSnapshot_RestoreRequiresGCDisabled(t *testing.T) {
	d := ygo.NewDoc() // GC enabled
	m := ygo.NewMap(d, "m")
	txn := d.WriteTxn()
	m.Set(txn, "k", "v")
	txn.Commit()
	snap := ygo.CreateSnapshot(d)

	if _, err := ygo.RestoreSnapshot(d, snap); err != ygo.ErrSnapshotGC {
		t.Errorf("RestoreSnapshot with GC on: err = %v, want ErrSnapshotGC", err)
	}
}

// TestSnapshot_EncodeDecodeRoundTrip checks the public encode/decode
// pair preserves snapshot identity.
func TestSnapshot_EncodeDecodeRoundTrip(t *testing.T) {
	d := gcOff()
	a := ygo.NewArray(d, "a")
	txn := d.WriteTxn()
	a.Insert(txn, 0, "x")
	a.Insert(txn, 1, "y")
	a.Delete(txn, 0, 1)
	txn.Commit()

	snap := ygo.CreateSnapshot(d)
	enc := ygo.EncodeSnapshot(snap)
	dec, err := ygo.DecodeSnapshot(enc)
	if err != nil {
		t.Fatalf("DecodeSnapshot: %v", err)
	}
	if !ygo.EqualSnapshots(snap, dec) {
		t.Error("snapshot did not survive encode/decode round-trip")
	}
}

// TestSnapshot_RestoreMidBlockBoundary exercises the block-split path:
// a single packed run is snapshotted partway through, then extended.
func TestSnapshot_RestoreMidBlockBoundary(t *testing.T) {
	d := gcOff()
	txt := ygo.NewText(d, "t")

	txn := d.WriteTxn()
	_ = txt.Insert(txn, 0, "abc") // one packed run, clocks 0..2
	txn.Commit()

	snap := ygo.CreateSnapshot(d) // boundary at clock 3 (end of run)

	txn = d.WriteTxn()
	_ = txt.Insert(txn, 3, "def") // extends clocks 3..5
	txn.Commit()

	restored, err := ygo.RestoreSnapshot(d, snap)
	if err != nil {
		t.Fatalf("RestoreSnapshot: %v", err)
	}
	if got := ygo.NewText(restored, "t").String(); got != "abc" {
		t.Errorf("restored = %q, want abc", got)
	}
}
