package store

import (
	"fmt"
	"sort"
)

// ClientBlockList is the dense, clock-ordered list of every block
// produced by a single client. Invariants enforced:
//
//  1. Per-client clock density: the union of cell ranges equals
//     [0, Clock()) with no gaps and no overlaps.
//  2. Sorted by clock: list[i].ClockStart < list[i+1].ClockStart.
//  3. Inclusive ranges: ClockStart and ClockEnd are both inclusive;
//     Len = End - Start + 1.
//
// See docs/yrs-port-notes/store.md "Internal invariants".
type ClientBlockList struct {
	list []BlockCell
}

// NewClientBlockList returns an empty list. Capacity is reserved for
// the common case of a few-dozen blocks per client.
func NewClientBlockList() *ClientBlockList {
	return &ClientBlockList{list: make([]BlockCell, 0, 16)}
}

// Len returns the number of cells in the list.
func (l *ClientBlockList) Len() int { return len(l.list) }

// Get returns the cell at index i. Returns the zero BlockCell and false
// if i is out of range.
func (l *ClientBlockList) Get(i int) (BlockCell, bool) {
	if i < 0 || i >= len(l.list) {
		return BlockCell{}, false
	}
	return l.list[i], true
}

// Clock returns the next free clock for this client. Equal to
// list[len-1].ClockEnd() + 1, or 0 for an empty list. Constant-time.
//
// Mirrors yrs ClientBlockList::clock (block_store.rs:24-34). Used for
// state vector materialization and append-precondition checks.
func (l *ClientBlockList) Clock() uint64 {
	if len(l.list) == 0 {
		return 0
	}
	return l.list[len(l.list)-1].ClockEnd() + 1
}

// FindPivot returns the index of the cell whose [ClockStart, ClockEnd]
// inclusive range contains the given clock. Returns (0, false) if the
// list is empty or the clock is past Clock().
//
// Implementation uses sort.Search rather than yrs's hand-rolled
// interpolation+binary search (block_store.rs:42-68). The interpolation
// is a micro-optimisation; sort.Search costs at most log2(N) extra
// comparisons and avoids the divide-by-zero edge case yrs has when the
// list contains a single block ending at clock 0.
func (l *ClientBlockList) FindPivot(clock uint64) (int, bool) {
	if len(l.list) == 0 {
		return 0, false
	}
	i := sort.Search(len(l.list), func(i int) bool {
		return l.list[i].ClockEnd() >= clock
	})
	if i == len(l.list) || l.list[i].ClockStart() > clock {
		return 0, false
	}
	return i, true
}

// Push appends a cell to the list. Caller must guarantee
// cell.ClockStart() == l.Clock() to preserve invariant 1; in tests,
// follow with CheckInvariants.
//
// Mirrors yrs ClientBlockList::push (block_store.rs:84-87).
func (l *ClientBlockList) Push(cell BlockCell) {
	l.list = append(l.list, cell)
}

// Insert places cell at the given index, shifting later cells right.
// Used by split_block and materialize for the new right-half cell;
// caller must compute index via FindPivot and validate that the insert
// keeps invariants 1-3.
//
// Mirrors yrs ClientBlockList::insert (block_store.rs:89-93).
func (l *ClientBlockList) Insert(index int, cell BlockCell) {
	l.list = append(l.list, BlockCell{})
	copy(l.list[index+1:], l.list[index:])
	l.list[index] = cell
}

// CheckInvariants validates structural invariants 1-3 across the list.
// Returns the first error found, or nil if everything is consistent.
//
// Test-only utility; production code should not call this on the hot
// path. Recommended pattern: defer l.CheckInvariants() in any test that
// performs Push / Insert / SquashLeft to catch regressions immediately.
func (l *ClientBlockList) CheckInvariants() error {
	var nextClock uint64 = 0
	for i, c := range l.list {
		if c.ClockEnd() < c.ClockStart() {
			return fmt.Errorf("cell %d: inverted range [%d, %d]", i, c.ClockStart(), c.ClockEnd())
		}
		if c.ClockStart() != nextClock {
			return fmt.Errorf("cell %d: gap or overlap (expected start %d, got %d)", i, nextClock, c.ClockStart())
		}
		nextClock = c.ClockEnd() + 1
	}
	return nil
}
