package store

import "github.com/Deln0r/ygo/internal/block"

// StateVector summarizes what the local doc knows. For each client it
// holds the next free clock (exclusive). A peer can compare its own
// StateVector against a remote SV to compute the diff.
//
// Iteration order is non-deterministic; wire-emitting paths must sort
// by client ID before encoding. See store.md open question 4.
type StateVector map[uint64]uint64

// BlockStore owns the per-client block lists for a single Doc. It is
// not safe for concurrent access; the Doc layer holds the RWMutex.
type BlockStore struct {
	clients map[uint64]*ClientBlockList
}

// NewBlockStore returns an empty store.
func NewBlockStore() *BlockStore {
	return &BlockStore{clients: make(map[uint64]*ClientBlockList)}
}

// GetClient returns the per-client list for client c, or nil if no block
// has ever been inserted for that client.
//
// Mirrors yrs BlockStore::get_client (block_store.rs).
func (s *BlockStore) GetClient(c uint64) *ClientBlockList {
	return s.clients[c]
}

// GetClientMut returns the per-client list for c, creating an empty
// list lazily if absent. The only API that constructs ClientBlockLists.
//
// Mirrors yrs BlockStore::get_client_blocks_mut (block_store.rs:429-433).
func (s *BlockStore) GetClientMut(c uint64) *ClientBlockList {
	l, ok := s.clients[c]
	if !ok {
		l = NewClientBlockList()
		s.clients[c] = l
	}
	return l
}

// Contains reports whether the store has ever seen the given ID. Returns
// true even for IDs covered by GC cells — the underlying knowledge is
// preserved (matches JS Yjs and yrs semantics; see store.md open
// question 9).
func (s *BlockStore) Contains(id block.ID) bool {
	l, ok := s.clients[id.Client]
	if !ok {
		return false
	}
	return id.Clock < l.Clock()
}

// GetClock returns the next free clock for client c, or 0 if c is
// unknown. Convenience shortcut over GetClient(c).Clock().
//
// Mirrors yrs BlockStore::get_clock (block_store.rs:419-425).
func (s *BlockStore) GetClock(c uint64) uint64 {
	l, ok := s.clients[c]
	if !ok {
		return 0
	}
	return l.Clock()
}

// GetBlock returns the BlockCell containing id, or (zero, false) if no
// client list exists or id.Clock is out of that client's range.
func (s *BlockStore) GetBlock(id block.ID) (BlockCell, bool) {
	l, ok := s.clients[id.Client]
	if !ok {
		return BlockCell{}, false
	}
	idx, ok := l.FindPivot(id.Clock)
	if !ok {
		return BlockCell{}, false
	}
	return l.list[idx], true
}

// GetItem returns the *Item containing id. Returns nil for GC cells
// (callers must treat nil as "either not found or already collected";
// use Contains to disambiguate when it matters).
//
// Mirrors yrs BlockStore::get_item (block_store.rs:386-393).
func (s *BlockStore) GetItem(id block.ID) *block.Item {
	cell, ok := s.GetBlock(id)
	if !ok {
		return nil
	}
	return cell.AsItem()
}

// PushBlock appends a fresh Item to its client's list. The caller must
// guarantee item.ID.Clock == s.GetClock(item.ID.Client) — i.e. the new
// block's first clock is exactly the next free clock for that client.
// Violating this breaks invariant 1 of ClientBlockList; tests should
// follow with CheckInvariants.
//
// Mirrors yrs BlockStore::push_block (block_store.rs:314-326).
func (s *BlockStore) PushBlock(item *block.Item) {
	l := s.GetClientMut(item.ID.Client)
	l.Push(CellOfItem(item))
}

// PushGC appends a fresh GC range to its client's list, with the same
// monotonicity precondition as PushBlock.
//
// Mirrors yrs BlockStore::push_gc (block_store.rs:328-341).
func (s *BlockStore) PushGC(client, start, end uint64) {
	l := s.GetClientMut(client)
	l.Push(CellOfGC(start, end))
}

// GetItemCleanStart returns the slice from id.Clock to the end of its
// underlying block, without mutating anything. The caller can pass the
// returned slice to a future Materialize to physically split the block;
// here we only compute offsets.
//
// Returns (zero, false) when id is in a GC cell, in an unknown client,
// or out of range.
//
// Mirrors yrs BlockStore::get_item_clean_start (block_store.rs:399-403).
func (s *BlockStore) GetItemCleanStart(id block.ID) (ItemSlice, bool) {
	cell, ok := s.GetBlock(id)
	if !ok || cell.Kind != CellKindItem {
		return ItemSlice{}, false
	}
	it := cell.Item
	return ItemSlice{
		Ptr:   it,
		Start: id.Clock - it.ID.Clock,
		End:   it.Len - 1,
	}, true
}

// GetItemCleanEnd returns the slice from the beginning of id's block up
// to and including id. Symmetric to GetItemCleanStart.
//
// Mirrors yrs BlockStore::get_item_clean_end (block_store.rs:409-414).
func (s *BlockStore) GetItemCleanEnd(id block.ID) (ItemSlice, bool) {
	cell, ok := s.GetBlock(id)
	if !ok || cell.Kind != CellKindItem {
		return ItemSlice{}, false
	}
	it := cell.Item
	return ItemSlice{
		Ptr:   it,
		Start: 0,
		End:   id.Clock - it.ID.Clock,
	}, true
}

// GetStateVector materializes a fresh StateVector from the per-client
// Clock() values. No caching — yrs derives the SV on each call too
// (block_store.rs:357-364).
//
// Iteration order over s.clients is non-deterministic; the returned
// map's order is also non-deterministic. Encoding paths must sort by
// client ID before emitting bytes.
func (s *BlockStore) GetStateVector() StateVector {
	sv := make(StateVector, len(s.clients))
	for c, l := range s.clients {
		sv[c] = l.Clock()
	}
	return sv
}
