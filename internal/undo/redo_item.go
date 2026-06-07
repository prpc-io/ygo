package undo

import (
	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
)

// followRedone walks the Redone chain from item to the latest live
// representative. When an item has been resurrected (its Redone points
// at a newer item), undoing the original insertion must act on that
// newer item, not the stale tombstone. Returns the input unchanged if
// it has never been redone.
func followRedone(txn *doc.TransactionMut, item *block.Item) *block.Item {
	cur := item
	for cur != nil && cur.Redone != nil {
		next := txn.MaterializeCleanStart(*cur.Redone)
		if next == nil {
			break
		}
		cur = next
	}
	return cur
}

// redoItem resurrects a previously deleted item by inserting a fresh
// item with a copy of its content, linked back to the original via
// Item.Redone. Returns the new live item, or nil if restoration is
// not possible (an unresolved parent, or a content kind the first cut
// does not handle).
//
// This is the map-key path of yjs's redoItem (ParentSub != nil). The
// sequence path (ParentSub == nil, Array / Text) lands in a follow-up;
// see docs/undo-manager-design.md.
//
// Caller holds the doc write lock via txn.
func redoItem(txn *doc.TransactionMut, item *block.Item) *block.Item {
	if item == nil {
		return nil
	}

	// Already redone: follow the chain to the current live item.
	if item.Redone != nil {
		return txn.MaterializeCleanStart(*item.Redone)
	}

	// First cut handles map-keyed items only. Sequence items
	// (ParentSub == nil) need the left/right redone-tracing logic
	// that arrives with Array / Text undo.
	if item.ParentSub == nil {
		return nil
	}

	// Parent must be a resolved branch. Nested-type parents whose
	// own item is deleted need recursive parent-redo; deferred.
	if item.Parent.Kind != block.ParentBranch || item.Parent.Branch == nil {
		return nil
	}
	parent := item.Parent.Branch
	if parent.Item != nil && parent.Item.IsDeleted() {
		// Parent type itself was deleted; recursive parent resurrection
		// is a follow-up. Refuse rather than produce a dangling item.
		return nil
	}

	// Content kinds with pointer payloads (nested type, move, doc) are
	// not faithfully restorable in the first cut.
	if !item.Content.CopyableForUndo() {
		return nil
	}

	// Map insert position: the current tail under this key becomes the
	// left neighbour, mirroring Map.Set. right is nil for map items.
	var left *block.Item
	var origin *block.ID
	if existing, ok := parent.Map[*item.ParentSub]; ok && existing != nil {
		left = existing
		lid := existing.LastID()
		origin = &lid
	}

	clientID := txn.Doc().ClientID()
	clock := txn.Store().GetClock(clientID)
	nextID := block.ID{Client: clientID, Clock: clock}

	keyCopy := *item.ParentSub
	redone := &block.Item{
		ID:        nextID,
		Len:       1,
		Origin:    origin,
		Left:      left,
		Content:   item.Content.Copy(),
		Parent:    block.Parent{Kind: block.ParentBranch, Branch: parent},
		ParentSub: &keyCopy,
		Flags:     block.FlagCountable,
	}
	redone.SetKeep(true)

	txn.Store().PushBlock(redone)
	if dropped := redone.Integrate(txn, 0); dropped {
		txn.Delete(redone)
		return nil
	}

	item.Redone = &nextID
	return redone
}
