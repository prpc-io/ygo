package ygo_test

import (
	"testing"

	"github.com/Deln0r/ygo"
)

// TestUndoManager_PublicAPI exercises the top-level UndoManager surface
// the way an external consumer would: construct shared types, wrap them
// in an UndoManager, edit, undo, redo.
func TestUndoManager_PublicAPI(t *testing.T) {
	d := ygo.NewDoc()
	m := ygo.NewMap(d, "settings")
	um := ygo.NewUndoManager(d, m)
	defer um.Close()

	txn := d.WriteTxn()
	m.Set(txn, "theme", "dark")
	txn.Commit()

	if got := m.Get("theme"); got != "dark" {
		t.Fatalf("Get after Set = %v, want dark", got)
	}
	if !um.CanUndo() {
		t.Fatal("CanUndo false after edit")
	}

	if !um.Undo() {
		t.Fatal("Undo returned false")
	}
	if got := m.Get("theme"); got != nil {
		t.Errorf("Get after Undo = %v, want nil", got)
	}

	if !um.Redo() {
		t.Fatal("Redo returned false")
	}
	if got := m.Get("theme"); got != "dark" {
		t.Errorf("Get after Redo = %v, want dark", got)
	}
}

// TestUndoManager_MultiScope verifies an UndoManager can watch more
// than one shared type at once.
func TestUndoManager_MultiScope(t *testing.T) {
	d := ygo.NewDoc()
	m := ygo.NewMap(d, "m")
	a := ygo.NewArray(d, "a")
	um := ygo.NewUndoManager(d, m, a)
	defer um.Close()

	txn := d.WriteTxn()
	m.Set(txn, "k", "v")
	txn.Commit()

	// Force a fresh undo step so the array insert below is not grouped
	// with the map set above (both happen within the capture window in
	// a fast test).
	um.StopCapturing()

	txn2 := d.WriteTxn()
	a.Insert(txn2, 0, "x")
	txn2.Commit()

	// Undo reverses the array insert (most recent).
	um.Undo()
	if a.Len() != 0 {
		t.Errorf("after Undo array len = %d, want 0", a.Len())
	}
	if m.Get("k") != "v" {
		t.Errorf("map disturbed by array undo: %v", m.Get("k"))
	}

	// Undo reverses the map set.
	um.Undo()
	if m.Get("k") != nil {
		t.Errorf("after second Undo map k = %v, want nil", m.Get("k"))
	}
}

// TestUndoManager_WithOptions verifies the options constructor compiles
// and behaves (every edit its own step when grouping is disabled).
func TestUndoManager_WithOptions(t *testing.T) {
	d := ygo.NewDoc()
	a := ygo.NewArray(d, "a")
	um := ygo.NewUndoManagerWithOptions(d, ygo.UndoManagerOptions{
		CaptureTimeout: -1, // disable grouping: each txn is its own step
	}, a)
	defer um.Close()

	txn := d.WriteTxn()
	a.Insert(txn, 0, "x")
	txn.Commit()
	txn2 := d.WriteTxn()
	a.Insert(txn2, 1, "y")
	txn2.Commit()

	// With grouping disabled, one Undo reverses only the last insert.
	um.Undo()
	if a.Len() != 1 {
		t.Errorf("after one Undo len = %d, want 1 (grouping disabled)", a.Len())
	}
}
