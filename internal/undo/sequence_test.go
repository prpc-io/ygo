package undo_test

import (
	"testing"

	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/types"
	"github.com/Deln0r/ygo/internal/undo"
)

func arrInsert(t *testing.T, d *doc.Doc, a *types.Array, idx uint64, v any) {
	t.Helper()
	txn := d.WriteTxn()
	a.Insert(txn, idx, v)
	txn.Commit()
}

func arrDelete(t *testing.T, d *doc.Doc, a *types.Array, idx, n uint64) {
	t.Helper()
	txn := d.WriteTxn()
	a.Delete(txn, idx, n)
	txn.Commit()
}

func txtInsert(t *testing.T, d *doc.Doc, x *types.Text, idx uint64, s string) {
	t.Helper()
	txn := d.WriteTxn()
	if err := x.Insert(txn, idx, s); err != nil {
		t.Fatalf("Text.Insert: %v", err)
	}
	txn.Commit()
}

func txtDelete(t *testing.T, d *doc.Doc, x *types.Text, idx, n uint64) {
	t.Helper()
	txn := d.WriteTxn()
	if err := x.Delete(txn, idx, n); err != nil {
		t.Fatalf("Text.Delete: %v", err)
	}
	txn.Commit()
}

// TestUndoRedo_ArrayInsert_FreshElement: insert one element, undo
// (gone), redo (back).
func TestUndoRedo_ArrayInsert_FreshElement(t *testing.T) {
	d := doc.NewDoc()
	a := types.NewArray(d.Branch("list"))
	um := undo.NewUndoManager(d, []*block.Branch{a.Branch()})
	defer um.Close()

	arrInsert(t, d, a, 0, "x")
	if a.Len() != 1 || a.Get(0) != "x" {
		t.Fatalf("after insert: len=%d get0=%v", a.Len(), a.Get(0))
	}

	if !um.Undo() {
		t.Fatal("Undo returned false")
	}
	if a.Len() != 0 {
		t.Errorf("after Undo: len=%d, want 0", a.Len())
	}

	if !um.Redo() {
		t.Fatal("Redo returned false")
	}
	if a.Len() != 1 || a.Get(0) != "x" {
		t.Errorf("after Redo: len=%d get0=%v, want 1/x", a.Len(), a.Get(0))
	}
}

// TestUndoRedo_ArrayDelete_Restores: insert two, delete one, undo
// restores it at the original position.
func TestUndoRedo_ArrayDelete_Restores(t *testing.T) {
	d := doc.NewDoc()
	a := types.NewArray(d.Branch("list"))
	um := undo.NewUndoManager(d, []*block.Branch{a.Branch()})
	defer um.Close()

	arrInsert(t, d, a, 0, "a")
	um.StopCapturing()
	arrInsert(t, d, a, 1, "b")
	um.StopCapturing()
	arrDelete(t, d, a, 0, 1) // delete "a"
	if got := a.ToSlice(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("after delete: %v, want [b]", got)
	}

	// Undo the deletion: "a" comes back at index 0.
	if !um.Undo() {
		t.Fatal("Undo returned false")
	}
	got := a.ToSlice()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("after Undo of delete: %v, want [a b]", got)
	}
}

// TestUndoRedo_ArrayInsert_Repeatable cycles undo/redo on a sequence
// element several times.
func TestUndoRedo_ArrayInsert_Repeatable(t *testing.T) {
	d := doc.NewDoc()
	a := types.NewArray(d.Branch("list"))
	um := undo.NewUndoManager(d, []*block.Branch{a.Branch()})
	defer um.Close()

	arrInsert(t, d, a, 0, "v")
	for i := 0; i < 3; i++ {
		um.Undo()
		if a.Len() != 0 {
			t.Fatalf("cycle %d: after Undo len=%d, want 0", i, a.Len())
		}
		um.Redo()
		if a.Len() != 1 || a.Get(0) != "v" {
			t.Fatalf("cycle %d: after Redo len=%d get0=%v", i, a.Len(), a.Get(0))
		}
	}
}

// TestUndoRedo_TextInsert: insert text, undo, redo.
func TestUndoRedo_TextInsert(t *testing.T) {
	d := doc.NewDoc()
	x := types.NewText(d.Branch("doc"))
	um := undo.NewUndoManager(d, []*block.Branch{x.Branch()})
	defer um.Close()

	txtInsert(t, d, x, 0, "hello")
	if x.String() != "hello" {
		t.Fatalf("after insert: %q", x.String())
	}

	um.Undo()
	if x.String() != "" {
		t.Errorf("after Undo: %q, want empty", x.String())
	}

	um.Redo()
	if x.String() != "hello" {
		t.Errorf("after Redo: %q, want hello", x.String())
	}
}

// TestUndoRedo_TextDelete_Restores: insert text, delete a slice, undo
// restores the deleted run.
func TestUndoRedo_TextDelete_Restores(t *testing.T) {
	d := doc.NewDoc()
	x := types.NewText(d.Branch("doc"))
	um := undo.NewUndoManager(d, []*block.Branch{x.Branch()})
	defer um.Close()

	txtInsert(t, d, x, 0, "hello world")
	um.StopCapturing()
	txtDelete(t, d, x, 5, 6) // delete " world"
	if x.String() != "hello" {
		t.Fatalf("after delete: %q, want hello", x.String())
	}

	um.Undo()
	if x.String() != "hello world" {
		t.Errorf("after Undo of delete: %q, want 'hello world'", x.String())
	}
}
