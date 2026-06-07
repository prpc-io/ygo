package undo_test

import (
	"testing"

	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/types"
	"github.com/Deln0r/ygo/internal/undo"
)

// TestUndoRedo_MapSet_FreshKey is the core round-trip: set a key,
// undo (key disappears), redo (key reappears).
func TestUndoRedo_MapSet_FreshKey(t *testing.T) {
	d := doc.NewDoc()
	m := types.NewMap(d.Branch("settings"))
	um := undo.NewUndoManager(d, []*block.Branch{m.Branch()})
	defer um.Close()

	mapSet(t, d, m, "theme", "dark")
	if got := m.Get("theme"); got != "dark" {
		t.Fatalf("after Set, Get = %v, want dark", got)
	}

	if !um.Undo() {
		t.Fatal("Undo returned false")
	}
	if got := m.Get("theme"); got != nil {
		t.Errorf("after Undo, Get = %v, want nil", got)
	}
	if !um.CanRedo() {
		t.Error("redo stack empty after Undo")
	}

	if !um.Redo() {
		t.Fatal("Redo returned false")
	}
	if got := m.Get("theme"); got != "dark" {
		t.Errorf("after Redo, Get = %v, want dark", got)
	}
}

// TestUndoRedo_MapSet_Repeatable verifies multiple undo/redo cycles on
// the same key keep converging to the right state.
func TestUndoRedo_MapSet_Repeatable(t *testing.T) {
	d := doc.NewDoc()
	m := types.NewMap(d.Branch("m"))
	um := undo.NewUndoManager(d, []*block.Branch{m.Branch()})
	defer um.Close()

	mapSet(t, d, m, "k", "v")

	for i := 0; i < 3; i++ {
		um.Undo()
		if m.Get("k") != nil {
			t.Fatalf("cycle %d: after Undo Get = %v, want nil", i, m.Get("k"))
		}
		um.Redo()
		if m.Get("k") != "v" {
			t.Fatalf("cycle %d: after Redo Get = %v, want v", i, m.Get("k"))
		}
	}
}

// TestUndoRedo_TwoKeys_Independent verifies undoing one key does not
// disturb another, when each Set is its own stack item.
func TestUndoRedo_TwoKeys_Independent(t *testing.T) {
	d := doc.NewDoc()
	m := types.NewMap(d.Branch("m"))
	um := undo.NewUndoManager(d, []*block.Branch{m.Branch()})
	defer um.Close()

	mapSet(t, d, m, "a", 1)
	um.StopCapturing()
	mapSet(t, d, m, "b", 2)

	// Undo the "b" write only.
	um.Undo()
	if m.Get("b") != nil {
		t.Errorf("Undo did not clear b: %v", m.Get("b"))
	}
	if got := m.Get("a"); got != int64(1) && got != 1 {
		t.Errorf("Undo disturbed a: got %v, want 1", got)
	}

	// Redo brings "b" back.
	um.Redo()
	if got := m.Get("b"); got != int64(2) && got != 2 {
		t.Errorf("Redo did not restore b: got %v, want 2", got)
	}
}

// TestUndoRedo_NoOpWhenEmpty verifies Undo / Redo on empty stacks
// return false and do nothing.
func TestUndoRedo_NoOpWhenEmpty(t *testing.T) {
	d := doc.NewDoc()
	m := types.NewMap(d.Branch("m"))
	um := undo.NewUndoManager(d, []*block.Branch{m.Branch()})
	defer um.Close()

	if um.Undo() {
		t.Error("Undo on empty stack returned true")
	}
	if um.Redo() {
		t.Error("Redo on empty stack returned true")
	}
}

// TestUndoRedo_OverwriteKey checks the LWW case: set a key twice, then
// undo. After undo the key should hold the first value, not vanish.
func TestUndoRedo_OverwriteKey(t *testing.T) {
	d := doc.NewDoc()
	m := types.NewMap(d.Branch("m"))
	um := undo.NewUndoManager(d, []*block.Branch{m.Branch()})
	defer um.Close()

	mapSet(t, d, m, "k", "first")
	um.StopCapturing()
	mapSet(t, d, m, "k", "second")
	if m.Get("k") != "second" {
		t.Fatalf("after overwrite Get = %v, want second", m.Get("k"))
	}

	// Undo the second write: key should revert to "first".
	um.Undo()
	if got := m.Get("k"); got != "first" {
		t.Errorf("after Undo of overwrite, Get = %v, want first", got)
	}

	// Undo the first write: key should vanish.
	um.Undo()
	if got := m.Get("k"); got != nil {
		t.Errorf("after Undo of first write, Get = %v, want nil", got)
	}
}
