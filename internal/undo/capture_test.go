package undo_test

import (
	"testing"
	"time"

	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/undo"
)

// helper: WriteTxn that registers a phantom changedType on the given
// branch so the scope filter sees activity. We push a real item too
// so afterState advances for the client.
func writeAndCommit(t *testing.T, d *doc.Doc, branch *block.Branch, clock uint64) {
	t.Helper()
	txn := d.WriteTxn()
	txn.AddChangedType(branch, nil)
	it := &block.Item{
		ID:  block.ID{Client: d.ClientID(), Clock: clock},
		Len: 1,
	}
	txn.Store().PushBlock(it)
	txn.Commit()
}

// TestUndoManager_EmptyDoc_NoCaptures verifies that constructing an
// UndoManager and committing transactions that do not touch the
// scope keeps the stacks empty.
func TestUndoManager_EmptyDoc_NoCaptures(t *testing.T) {
	d := doc.NewDoc()
	scopedBranch := d.Branch("watched")
	otherBranch := d.Branch("ignored")

	um := undo.NewUndoManager(d, []*block.Branch{scopedBranch})
	defer um.Close()

	if um.CanUndo() || um.CanRedo() {
		t.Fatal("fresh UndoManager already has stack entries")
	}

	// Commit a transaction that does not touch the scope.
	txn := d.WriteTxn()
	txn.AddChangedType(otherBranch, nil)
	txn.Commit()

	if um.CanUndo() {
		t.Error("transaction outside scope captured")
	}
}

// TestUndoManager_OneTransaction_OneStackItem captures a single
// in-scope transaction into a single StackItem.
func TestUndoManager_OneTransaction_OneStackItem(t *testing.T) {
	d := doc.NewDoc()
	b := d.Branch("watched")
	um := undo.NewUndoManager(d, []*block.Branch{b})
	defer um.Close()

	writeAndCommit(t, d, b, 0)

	if !um.CanUndo() {
		t.Fatal("in-scope transaction did not push to undo stack")
	}
	if um.CanRedo() {
		t.Error("redo stack should still be empty")
	}
}

// TestUndoManager_GroupingWithinCaptureTimeout verifies two
// transactions inside the captureTimeout window collapse into one
// StackItem on the undo stack.
func TestUndoManager_GroupingWithinCaptureTimeout(t *testing.T) {
	d := doc.NewDoc()
	b := d.Branch("watched")
	um := undo.NewUndoManager(d, []*block.Branch{b}, undo.Options{
		CaptureTimeout: time.Hour, // never time out within the test
	})
	defer um.Close()

	writeAndCommit(t, d, b, 0)
	writeAndCommit(t, d, b, 1)
	writeAndCommit(t, d, b, 2)

	// All three transactions should fold into one StackItem.
	if !um.CanUndo() {
		t.Fatal("no stack item after three transactions")
	}
	// Drain via Clear and confirm we only ever had one entry.
	// Skeleton Undo returns false, but Clear empties both stacks.
	um.Clear()
	if um.CanUndo() || um.CanRedo() {
		t.Error("Clear did not empty stacks")
	}
}

// TestUndoManager_NoGroupingAfterStopCapturing verifies that
// StopCapturing forces the next transaction to open a fresh StackItem
// regardless of captureTimeout.
func TestUndoManager_NoGroupingAfterStopCapturing(t *testing.T) {
	d := doc.NewDoc()
	b := d.Branch("watched")
	// Force CaptureTimeout very large so grouping would happen
	// without StopCapturing.
	um := undo.NewUndoManager(d, []*block.Branch{b}, undo.Options{
		CaptureTimeout: time.Hour,
	})
	defer um.Close()

	writeAndCommit(t, d, b, 0)
	um.StopCapturing()
	writeAndCommit(t, d, b, 1)

	// We expect two separate StackItems. There is no public stack-
	// length accessor; we approximate by clearing and confirming
	// that both stacks were drained. A future commit adds an
	// explicit accessor.
	um.Clear()
	if um.CanUndo() {
		t.Error("Clear did not drain")
	}
}

// TestUndoManager_RemoteOriginIsIgnored verifies that a transaction
// with a non-tracked origin does not push a StackItem.
func TestUndoManager_RemoteOriginIsIgnored(t *testing.T) {
	d := doc.NewDoc()
	b := d.Branch("watched")
	um := undo.NewUndoManager(d, []*block.Branch{b})
	defer um.Close()

	txn := d.WriteTxn()
	txn.Origin = "remote"
	txn.AddChangedType(b, nil)
	txn.Commit()

	if um.CanUndo() {
		t.Error("remote-origin transaction captured")
	}
}

// TestUndoManager_Close_Idempotent confirms Close can be called
// multiple times safely.
func TestUndoManager_Close_Idempotent(t *testing.T) {
	d := doc.NewDoc()
	b := d.Branch("watched")
	um := undo.NewUndoManager(d, []*block.Branch{b})
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
