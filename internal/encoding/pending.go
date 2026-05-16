package encoding

import (
	"sort"

	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/store"
)

// Pending buffers items and delete-set entries that arrived before
// their causal dependencies (Origin / RightOrigin / Parent-by-ID
// referring to clocks the local BlockStore has not yet seen).
//
// yrs's equivalent state lives in `Store::pending` (`store.rs` field
// `pending: Option<PendingUpdate>`) plus `pending_ds`
// (`Option<DeleteSet>`). We collapse both into a single Pending
// struct attached to the Doc via the opaque `pendingState any` slot.
//
// Lifecycle: ApplyUpdate folds the incoming update's blocks/DS into
// Pending, then runs Pending.Drain in a loop until no further
// progress is made. Items that successfully integrate are removed
// from Pending; the leftover stays for the next ApplyUpdate call.
//
// Concurrency: Pending is mutated only under the doc's write lock
// (acquired by the surrounding TransactionMut). No internal mutex.
type Pending struct {
	// Blocks groups queued wire-level Block records by clientID.
	// Per-client entries are kept in clock-ascending order to
	// preserve causal-prefix ordering — yrs relies on this when
	// dropping a contiguous prefix that has become satisfied.
	Blocks map[uint64][]Block

	// DeleteSet stores ranges whose target IDs are not yet present
	// in the local store. yrs calls this `pending_ds`. Each Drain
	// pass re-scans these ranges; those whose target now exists
	// get applied.
	DeleteSet *IdSet
}

// NewPending returns an empty buffer.
func NewPending() *Pending {
	return &Pending{
		Blocks:    map[uint64][]Block{},
		DeleteSet: NewIdSet(),
	}
}

// IsEmpty reports whether the buffer holds zero queued items.
func (p *Pending) IsEmpty() bool {
	if p == nil {
		return true
	}
	if len(p.Blocks) > 0 {
		return false
	}
	if p.DeleteSet == nil {
		return true
	}
	empty := true
	p.DeleteSet.Iterate(func(_ uint64, ranges []Range) {
		if len(ranges) > 0 {
			empty = false
		}
	})
	return empty
}

// BlockCount returns the total number of queued blocks across all
// clients. Useful for tests and observability ("how many items are
// stuck waiting?").
func (p *Pending) BlockCount() int {
	if p == nil {
		return 0
	}
	n := 0
	for _, list := range p.Blocks {
		n += len(list)
	}
	return n
}

// MissingSV returns the state vector describing what the local
// store is missing to satisfy every queued item's dependencies.
// Sync-protocol callers use this to request the gap from the peer
// ("send me everything since (client, clock) for each entry").
//
// For each pending item we walk Origin and RightOrigin; for each
// reference that points outside the current BlockStore, we record
// (clientID, clock+1) — the receiver wants every clock starting at
// that target, and the SV convention is "the smallest clock NOT
// yet seen". Parent-by-ID references contribute the same way.
//
// Returns an empty map when nothing is missing.
func (p *Pending) MissingSV(bs *store.BlockStore) store.StateVector {
	out := make(store.StateVector)
	if p == nil {
		return out
	}
	track := func(ref block.ID) {
		if bs.Contains(ref) {
			return
		}
		want := ref.Clock + 1
		if cur, ok := out[ref.Client]; !ok || want > cur {
			out[ref.Client] = want
		}
	}
	for _, list := range p.Blocks {
		for _, b := range list {
			if b.Kind != WireBlockItem || b.Item == nil {
				continue
			}
			it := b.Item
			if it.Origin != nil {
				track(*it.Origin)
			}
			if it.RightOrigin != nil {
				track(*it.RightOrigin)
			}
			if it.Parent.Kind == block.ParentID {
				track(it.Parent.ID)
			}
		}
	}
	return out
}

// addBlock appends b to the per-client queue, keeping the queue
// sorted by clock ascending so causal-prefix integration sees the
// earliest unsatisfied item first.
func (p *Pending) addBlock(client uint64, b Block) {
	list := p.Blocks[client]
	clock := blockStartClock(b)
	idx := sort.Search(len(list), func(i int) bool {
		return blockStartClock(list[i]) >= clock
	})
	// Skip exact-clock duplicates — re-applying the same update
	// shouldn't double-queue.
	if idx < len(list) && blockStartClock(list[idx]) == clock {
		return
	}
	list = append(list, Block{})
	copy(list[idx+1:], list[idx:])
	list[idx] = b
	p.Blocks[client] = list
}

func blockStartClock(b Block) uint64 {
	switch b.Kind {
	case WireBlockItem:
		if b.Item != nil {
			return b.Item.ID.Clock
		}
	case WireBlockGC, WireBlockSkip:
		return b.ID.Clock
	}
	return 0
}

// itemMissingDep reports whether any of an Item's wire-level
// dependency references point to a clock the local store has not
// seen yet. Used both during the first Apply pass and during
// Drain to decide if an item is still stuck.
func itemMissingDep(bs *store.BlockStore, it *block.Item) bool {
	if it == nil {
		return false
	}
	if it.Origin != nil && !bs.Contains(*it.Origin) {
		return true
	}
	if it.RightOrigin != nil && !bs.Contains(*it.RightOrigin) {
		return true
	}
	if it.Parent.Kind == block.ParentID && !bs.Contains(it.Parent.ID) {
		return true
	}
	return false
}

// foldUpdate merges every block and delete-set entry from u into p.
// Items that the local store already has are silently dropped (no
// need to retry them).
func (p *Pending) foldUpdate(u *Update, bs *store.BlockStore) {
	for client, list := range u.Blocks {
		for _, b := range list {
			switch b.Kind {
			case WireBlockItem:
				if b.Item != nil && bs.Contains(b.Item.ID) {
					continue
				}
				p.addBlock(client, b)
			case WireBlockGC, WireBlockSkip:
				if bs.Contains(b.ID) {
					continue
				}
				p.addBlock(client, b)
			}
		}
	}
	if u.DeleteSet != nil {
		u.DeleteSet.Iterate(func(client uint64, ranges []Range) {
			for _, r := range ranges {
				p.DeleteSet.Insert(client, r.Start, r.Length)
			}
		})
	}
}

// Drain attempts to integrate every queued block whose dependencies
// are now satisfied. Returns the number of blocks that successfully
// integrated this pass. Apply callers loop on Drain until it
// returns 0, which means the queue has reached its fixed point for
// this transaction.
//
// The order of work within a pass: iterate clients in ascending
// clientID, items within a client in clock-ascending order. A block
// with a still-missing dep is skipped (left in the queue) — the
// causal-prefix property of yrs updates means a later clock for
// the same client may yet integrate (depends on a different chain),
// so we do not short-circuit on first failure within a client.
func (p *Pending) Drain(txn *doc.TransactionMut) int {
	bs := txn.Store()
	progress := 0

	// Block integration pass.
	clients := make([]uint64, 0, len(p.Blocks))
	for c := range p.Blocks {
		clients = append(clients, c)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i] < clients[j] })

	for _, c := range clients {
		list := p.Blocks[c]
		remaining := list[:0]
		for _, b := range list {
			switch b.Kind {
			case WireBlockGC:
				if bs.Contains(b.ID) {
					continue
				}
				bs.PushGC(c, b.ID.Clock, b.ID.Clock+b.Len-1)
				progress++
			case WireBlockSkip:
				// Skip blocks reserve clock space without semantics;
				// we drop them once they reach the front of the queue
				// (matches Apply's current handling).
				progress++
			case WireBlockItem:
				it := b.Item
				if it == nil {
					continue
				}
				if bs.Contains(it.ID) {
					continue
				}
				if itemMissingDep(bs, it) {
					remaining = append(remaining, b)
					continue
				}
				if err := block.Repair(it, txn); err != nil {
					// Repair failed (typically ParentIDUnresolved
					// when nested types not yet supported) — leave
					// queued; future code may resolve it.
					remaining = append(remaining, b)
					continue
				}
				bs.PushBlock(it)
				if dropped := it.Integrate(txn, 0); dropped {
					txn.Delete(it)
				}
				progress++
			}
		}
		if len(remaining) == 0 {
			delete(p.Blocks, c)
		} else {
			p.Blocks[c] = remaining
		}
	}

	// Delete-set pass: rebuild a per-client remaining list whose
	// ranges still point at unseen IDs.
	if p.DeleteSet != nil {
		remaining := NewIdSet()
		p.DeleteSet.Iterate(func(client uint64, ranges []Range) {
			for _, r := range ranges {
				clock := r.Start
				end := r.End()
				for clock < end {
					cell, ok := bs.GetBlock(block.ID{Client: client, Clock: clock})
					if !ok {
						// Unseen — re-queue the rest of this range.
						remaining.Insert(client, clock, end-clock)
						break
					}
					if it := cell.AsItem(); it != nil && !it.IsDeleted() {
						txn.Delete(it)
						progress++
					}
					clock = cell.ClockEnd() + 1
				}
			}
		})
		p.DeleteSet = remaining
	}
	return progress
}
