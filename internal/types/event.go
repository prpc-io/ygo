package types

import (
	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
)

func init() {
	// Bridge the doc -> types direction without an import cycle: doc
	// invokes this hook at Commit to dispatch shared-type events.
	doc.TypeEventHook = dispatchEvents
}

// KeyChange describes one map key's change within a transaction: the
// kind of change and the value the key held before it. Mirrors yjs's
// YMapEvent change entry ({ action, oldValue }).
type KeyChange struct {
	// Action is "add", "update", or "delete".
	Action string
	// OldValue is the value the key held before this transaction; nil
	// for an "add".
	OldValue any
}

// MapEvent is delivered to Map observers after a transaction that
// changed the map. Mirrors yjs YMapEvent.
type MapEvent struct {
	// Target is the map that changed.
	Target *Map
	// Keys maps each changed key to how it changed. Only keys whose
	// observable value actually changed appear (a key added and
	// removed in the same transaction is omitted, matching yjs).
	Keys map[string]KeyChange
}

// KeysChanged returns the set of keys that changed, for callers that
// only need the names.
func (e *MapEvent) KeysChanged() []string {
	out := make([]string, 0, len(e.Keys))
	for k := range e.Keys {
		out = append(out, k)
	}
	return out
}

// Observe registers fn to run after every transaction that changes
// this map, with a MapEvent describing the changed keys. Returns an
// unsubscribe function. Observers fire on local edits and on remote
// updates applied via ApplyUpdate.
//
// Mirrors yjs ymap.observe. The callback runs while the doc write
// lock is held; it must not start a new transaction on the same doc.
func (m *Map) Observe(fn func(*MapEvent)) func() {
	adapter := func(ev any) {
		if e, ok := ev.(*MapEvent); ok {
			fn(e)
		}
	}
	m.branch.Observers = append(m.branch.Observers, adapter)
	idx := len(m.branch.Observers) - 1
	return func() {
		// Detach by clearing the slot; keep indices stable so a later
		// unsubscribe does not target the wrong observer.
		if idx < len(m.branch.Observers) {
			m.branch.Observers[idx] = nil
		}
	}
}

// dispatchEvents is installed as doc.TypeEventHook. For each branch
// changed this transaction that has observers, it builds the
// appropriate event and fires them. Only Map events are implemented
// in this cut; Array / Text deltas land in a follow-up.
func dispatchEvents(t *doc.TransactionMut) {
	for _, br := range t.ChangedTypes() {
		if !hasObservers(br) {
			continue
		}
		if keys := t.ChangedKeys(br); len(keys) > 0 {
			fireMapEvent(t, br, keys)
		}
	}
}

func hasObservers(br *block.Branch) bool {
	for _, o := range br.Observers {
		if o != nil {
			return true
		}
	}
	return false
}

// fireMapEvent builds a MapEvent for a changed map branch following
// yjs YMapEvent.keys semantics, then dispatches it to the branch's
// observers.
func fireMapEvent(t *doc.TransactionMut, br *block.Branch, changed map[string]struct{}) {
	before := t.BeforeState()
	adds := func(it *block.Item) bool {
		return it != nil && it.ID.Clock >= before[it.ID.Client]
	}
	deletes := func(it *block.Item) bool {
		return it != nil && t.DeletedThisTxn(it.ID)
	}

	keys := make(map[string]KeyChange, len(changed))
	for key := range changed {
		item := br.Map[key] // current winner (may be deleted)
		if item == nil {
			continue
		}
		var change KeyChange
		var skip bool
		if adds(item) {
			prev := item.Left
			for prev != nil && adds(prev) {
				prev = prev.Left
			}
			if deletes(item) {
				if prev != nil && deletes(prev) {
					change = KeyChange{Action: "delete", OldValue: extractValue(prev.Content)}
				} else {
					skip = true // added and removed this txn: net no-op
				}
			} else {
				if prev != nil && deletes(prev) {
					change = KeyChange{Action: "update", OldValue: extractValue(prev.Content)}
				} else {
					change = KeyChange{Action: "add"}
				}
			}
		} else {
			if deletes(item) {
				change = KeyChange{Action: "delete", OldValue: extractValue(item.Content)}
			} else {
				skip = true // no observable change
			}
		}
		if !skip {
			keys[key] = change
		}
	}
	// Fire even when keys ends up empty (e.g. a key added and removed
	// in the same transaction nets to no-op): yjs still delivers the
	// event, with an empty changes.keys, because the type was touched.

	ev := &MapEvent{Target: &Map{branch: br}, Keys: keys}
	for _, o := range br.Observers {
		if o != nil {
			o(ev)
		}
	}
}
