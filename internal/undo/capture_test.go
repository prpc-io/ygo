package undo_test

import (
	"testing"
	"time"

	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/types"
	"github.com/Deln0r/ygo/internal/undo"
)

// mapSet drives a real types.Map.Set through a WriteTxn with the
// default (nil) origin, the way an application would.
func mapSet(t *testing.T, d *doc.Doc, m *types.Map, key string, value any) {
	t.Helper()
	txn := d.WriteTxn()
	m.Set(txn, key, value)
	txn.Commit()
}

// TestUndoManager_OutOfScope_NoCapture verifies that mutating a branch
// outside the configured scope captures nothing.
func TestUndoManager_OutOfScope_NoCapture(t *testing.T) {
	d := doc.NewDoc()
	watched := types.NewMap(d.Branch("watched"))
	other := types.NewMap(d.Branch("ignored"))

	um := undo.NewUndoManager(d, []*block.Branch{watched.Branch()})
	defer um.Close()

	mapSet(t, d, other, "k", "v")

	if um.CanUndo() {
		t.Error("out-of-scope mutation captured")
	}
}

// TestUndoManager_InScope_Captures verifies a single in-scope Map.Set
// pushes one undo entry and leaves redo empty.
func TestUndoManager_InScope_Captures(t *testing.T) {
	d := doc.NewDoc()
	m := types.NewMap(d.Branch("watched"))
	um := undo.NewUndoManager(d, []*block.Branch{m.Branch()})
	defer um.Close()

	mapSet(t, d, m, "k", "v")

	if !um.CanUndo() {
		t.Fatal("in-scope Map.Set did not push undo entry")
	}
	if um.CanRedo() {
		t.Error("redo stack should be empty")
	}
}

// TestUndoManager_GroupingWithinCaptureTimeout verifies several Map
// writes inside the capture window collapse into a single undo entry.
func TestUndoManager_GroupingWithinCaptureTimeout(t *testing.T) {
	d := doc.NewDoc()
	m := types.NewMap(d.Branch("watched"))
	um := undo.NewUndoManager(d, []*block.Branch{m.Branch()}, undo.Options{
		CaptureTimeout: time.Hour, // never time out within the test
	})
	defer um.Close()

	mapSet(t, d, m, "a", 1)
	mapSet(t, d, m, "b", 2)
	mapSet(t, d, m, "c", 3)

	// One Undo should reverse all three grouped writes.
	if !um.Undo() {
		t.Fatal("Undo returned false")
	}
	if m.Get("a") != nil || m.Get("b") != nil || m.Get("c") != nil {
		t.Errorf("grouped Undo did not clear all keys: a=%v b=%v c=%v",
			m.Get("a"), m.Get("b"), m.Get("c"))
	}
	if um.CanUndo() {
		t.Error("undo stack should be empty after single grouped Undo")
	}
}

// TestUndoManager_StopCapturing_Splits verifies StopCapturing forces a
// fresh undo entry, so two writes need two Undos.
func TestUndoManager_StopCapturing_Splits(t *testing.T) {
	d := doc.NewDoc()
	m := types.NewMap(d.Branch("watched"))
	um := undo.NewUndoManager(d, []*block.Branch{m.Branch()}, undo.Options{
		CaptureTimeout: time.Hour,
	})
	defer um.Close()

	mapSet(t, d, m, "a", 1)
	um.StopCapturing()
	mapSet(t, d, m, "b", 2)

	// First Undo reverses only "b".
	um.Undo()
	if m.Get("b") != nil {
		t.Errorf("first Undo did not clear b: %v", m.Get("b"))
	}
	if m.Get("a") == nil {
		t.Errorf("first Undo wrongly cleared a")
	}
	// Second Undo reverses "a".
	um.Undo()
	if m.Get("a") != nil {
		t.Errorf("second Undo did not clear a: %v", m.Get("a"))
	}
}

// TestUndoManager_RemoteOriginIgnored verifies a non-tracked origin
// is not captured.
func TestUndoManager_RemoteOriginIgnored(t *testing.T) {
	d := doc.NewDoc()
	m := types.NewMap(d.Branch("watched"))
	um := undo.NewUndoManager(d, []*block.Branch{m.Branch()})
	defer um.Close()

	txn := d.WriteTxn()
	txn.Origin = "remote"
	m.Set(txn, "k", "v")
	txn.Commit()

	if um.CanUndo() {
		t.Error("remote-origin mutation captured")
	}
}

// TestUndoManager_Close_Idempotent confirms Close is safe to repeat.
func TestUndoManager_Close_Idempotent(t *testing.T) {
	d := doc.NewDoc()
	m := types.NewMap(d.Branch("watched"))
	um := undo.NewUndoManager(d, []*block.Branch{m.Branch()})
	um.Close()
	um.Close()
	if um.CanUndo() {
		t.Error("closed UndoManager reports CanUndo true")
	}
}

// TestUndoManager_PanicsOnEmptyScope confirms the contract.
func TestUndoManager_PanicsOnEmptyScope(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for empty scope")
		}
	}()
	d := doc.NewDoc()
	_ = undo.NewUndoManager(d, nil)
}
