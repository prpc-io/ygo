package types

import (
	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
)

// Map is the user-facing wrapper around a map-like Branch. Construct
// via NewMap; usually obtained as types.NewMap(d.Branch("name")).
//
// Map exposes Set / Get / Delete / Has / Len / Range / Clear plus a
// Branch accessor for low-level integration.
type Map struct {
	branch *block.Branch
}

// NewMap wraps the given branch as a Map. The branch's Map field is
// initialized lazily if nil — typical for a freshly-created root
// branch from Doc.Branch(name).
func NewMap(branch *block.Branch) *Map {
	if branch.Map == nil {
		branch.Map = map[string]*block.Item{}
	}
	return &Map{branch: branch}
}

// Branch returns the underlying *block.Branch. Useful for
// cross-package wiring (e.g. observers, encoders) that need to
// operate at the block layer.
func (m *Map) Branch() *block.Branch { return m.branch }

// Set stores value under key in this map. Concurrent writes from
// different replicas converge to the same final value — the writer
// with the higher (clientID, clock) wins per YATA.
//
// value may be of any type; the encoding-layer port (deferred per
// tech-debt.md) will decide how each Go type maps to a wire variant.
// For now value is stored as ContentAny, which round-trips through
// reflection.
//
// Mirrors yrs Map::insert (yrs/src/types/map.rs ~line 73).
func (m *Map) Set(txn *doc.TransactionMut, key string, value any) {
	clientID := txn.Doc().ClientID()
	clock := txn.Store().GetClock(clientID)

	var origin *block.ID
	var left *block.Item
	if existing, ok := m.branch.Map[key]; ok {
		// Per types-map.md finding 1: branch.Map[key] is the tail
		// already. Per finding 2: Origin = left.LastID().
		left = existing
		lid := left.LastID()
		origin = &lid
	}

	keyCopy := key // borrow-stable string for ParentSub pointer
	item := &block.Item{
		ID:        block.ID{Client: clientID, Clock: clock},
		Len:       1,
		Origin:    origin,
		Left:      left,
		Content:   block.Content{Kind: block.KindAny, Anys: []block.Any{value}},
		Parent:    block.Parent{Kind: block.ParentBranch, Branch: m.branch},
		ParentSub: &keyCopy,
		Flags:     block.FlagCountable,
	}

	txn.Store().PushBlock(item)
	if dropped := item.Integrate(txn, 0); dropped {
		txn.Delete(item)
	}
}

// Get returns the current value under key, or nil if the key is
// absent or its tail item is tombstoned.
//
// Per types-map.md finding 5: this is one-step. branch.Map[key] is
// the LWW winner; if it's deleted there is no live predecessor to
// fall back to.
func (m *Map) Get(key string) any {
	item, ok := m.branch.Map[key]
	if !ok || item.IsDeleted() {
		return nil
	}
	return extractValue(item.Content)
}

// Has reports whether key has a live (non-tombstoned) entry.
func (m *Map) Has(key string) bool {
	item, ok := m.branch.Map[key]
	return ok && !item.IsDeleted()
}

// Delete tombstones the entry under key. Calling Delete when the
// key is absent or already tombstoned is a no-op.
//
// Per types-map.md finding 3: Delete does NOT clear branch.Map[key].
// The map entry continues to point at the tombstoned item so a
// subsequent Set can chain off it as Origin/Left, preserving YATA
// convergence for concurrent writers that didn't see the delete yet.
func (m *Map) Delete(txn *doc.TransactionMut, key string) {
	item, ok := m.branch.Map[key]
	if !ok || item.IsDeleted() {
		return
	}
	txn.Delete(item)
}

// Len returns the number of live (non-tombstoned) entries.
//
// O(N) over the map size: iterates and counts. yrs has a TODO at
// map.rs:158 about caching this on the Branch — we'd inherit that
// optimization for free if we add the cache to Branch. Tracked in
// tech-debt.md if it shows up in benchmarks.
func (m *Map) Len() int {
	n := 0
	for _, item := range m.branch.Map {
		if !item.IsDeleted() {
			n++
		}
	}
	return n
}

// Range iterates over live (key, value) pairs in unspecified order
// (Go map iteration). The callback returns false to stop early.
func (m *Map) Range(fn func(key string, value any) bool) {
	for key, item := range m.branch.Map {
		if item.IsDeleted() {
			continue
		}
		if !fn(key, extractValue(item.Content)) {
			return
		}
	}
}

// Clear deletes every entry in the map.
//
// Per types-map.md finding 4: yrs's Map::clear calls txn.delete on
// every entry, including already-tombstoned ones. Our txn.Delete is
// idempotent (early-returns on IsDeleted), so we filter here purely
// to avoid the no-op — semantics match yrs either way.
func (m *Map) Clear(txn *doc.TransactionMut) {
	for _, item := range m.branch.Map {
		if !item.IsDeleted() {
			txn.Delete(item)
		}
	}
}

// extractValue unpacks a Content into the user-visible value. Only
// the variants the first Map port emits are handled; richer kinds
// (Type, Doc, Embed, etc.) require the types-layer recursive
// construction we have not built yet.
func extractValue(c block.Content) any {
	switch c.Kind {
	case block.KindAny:
		if len(c.Anys) > 0 {
			return c.Anys[0]
		}
	case block.KindString:
		return c.Str
	case block.KindBinary:
		return c.Bytes
	}
	return nil
}
