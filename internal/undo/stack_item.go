package undo

import "github.com/Deln0r/ygo/internal/encoding"

// StackItem records one captured change for Undo or Redo.
//
// Insertions covers item ID ranges created during the captured
// transaction window. Undo deletes everything in Insertions; Redo
// recreates it.
//
// Deletions covers item ID ranges tombstoned during the captured
// window. Undo restores them (via the redoItem chain in Item.Redone);
// Redo re-deletes the restorations.
//
// Meta is an arbitrary user payload — typically used by editor
// integrations to store and restore a selection range across an
// Undo / Redo round-trip. The core UndoManager logic never reads or
// writes Meta; only the consumer does.
type StackItem struct {
	Insertions *encoding.IdSet
	Deletions  *encoding.IdSet
	Meta       map[string]any
}

// newStackItem returns a StackItem with empty IdSets ready to accept
// Insert calls and a nil Meta map (callers populate Meta on demand).
func newStackItem() *StackItem {
	return &StackItem{
		Insertions: encoding.NewIdSet(),
		Deletions:  encoding.NewIdSet(),
	}
}

// merge folds the ranges from other into si. Used when an
// AfterTransaction handler decides to append to the existing top of
// stack rather than open a new StackItem (capture-timeout grouping).
// Meta is not merged: the most recent captured Meta wins via
// last-writer; if the consumer set Meta on a previous item and wants
// to preserve it, they should set it again on the next change.
func (si *StackItem) merge(other *StackItem) {
	mergeIdSets(si.Insertions, other.Insertions)
	mergeIdSets(si.Deletions, other.Deletions)
	if other.Meta != nil {
		si.Meta = other.Meta
	}
}

// mergeIdSets inserts every range from src into dst. Existing ranges
// are auto-merged by IdSet.Insert.
func mergeIdSets(dst, src *encoding.IdSet) {
	if src == nil {
		return
	}
	src.Iterate(func(client uint64, ranges []encoding.Range) {
		for _, r := range ranges {
			dst.Insert(client, r.Start, r.Length)
		}
	})
}
