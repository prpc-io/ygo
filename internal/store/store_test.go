package store

import (
	"testing"

	"github.com/Deln0r/ygo/internal/block"
)

func TestBlockStore_EmptyContains(t *testing.T) {
	s := NewBlockStore()
	if s.Contains(block.ID{Client: 1, Clock: 0}) {
		t.Error("empty store should not contain any ID")
	}
	if got := s.GetClock(1); got != 0 {
		t.Errorf("GetClock for unknown client=%d want 0", got)
	}
	if s.GetItem(block.ID{Client: 1, Clock: 0}) != nil {
		t.Error("GetItem on empty store should return nil")
	}
}

func TestBlockStore_PushBlockAndLookups(t *testing.T) {
	s := NewBlockStore()
	it := makeItem(42, 0, 5)
	s.PushBlock(it)

	// Containment within range
	if !s.Contains(block.ID{Client: 42, Clock: 0}) {
		t.Error("Contains should be true for clock 0")
	}
	if !s.Contains(block.ID{Client: 42, Clock: 4}) {
		t.Error("Contains should be true for clock 4 (inclusive end)")
	}
	if s.Contains(block.ID{Client: 42, Clock: 5}) {
		t.Error("Contains should be false for clock 5 (past exclusive Clock())")
	}
	if s.Contains(block.ID{Client: 99, Clock: 0}) {
		t.Error("Contains should be false for unknown client")
	}

	// GetClock advanced past the inserted block
	if got := s.GetClock(42); got != 5 {
		t.Errorf("GetClock=%d want 5", got)
	}

	// GetItem returns the same pointer
	if got := s.GetItem(block.ID{Client: 42, Clock: 2}); got != it {
		t.Errorf("GetItem returned %v, want %v", got, it)
	}

	// GetBlock returns the cell containing the queried clock
	cell, ok := s.GetBlock(block.ID{Client: 42, Clock: 3})
	if !ok || cell.Kind != CellKindItem || cell.Item != it {
		t.Errorf("GetBlock returned %+v, %v", cell, ok)
	}
}

func TestBlockStore_PushGC_GetItemReturnsNil(t *testing.T) {
	s := NewBlockStore()
	s.PushGC(7, 0, 9)

	if !s.Contains(block.ID{Client: 7, Clock: 5}) {
		t.Error("Contains should be true for IDs covered by GC")
	}
	if s.GetItem(block.ID{Client: 7, Clock: 5}) != nil {
		t.Error("GetItem on GC cell must return nil")
	}
	cell, ok := s.GetBlock(block.ID{Client: 7, Clock: 5})
	if !ok || cell.Kind != CellKindGC {
		t.Errorf("GetBlock should return the GC cell, got %+v", cell)
	}
}

func TestBlockStore_StateVector(t *testing.T) {
	s := NewBlockStore()
	if got := s.GetStateVector(); len(got) != 0 {
		t.Errorf("empty store SV size=%d want 0", len(got))
	}

	s.PushBlock(makeItem(1, 0, 10))
	s.PushBlock(makeItem(1, 10, 5))
	s.PushBlock(makeItem(2, 0, 3))
	s.PushGC(3, 0, 7)

	sv := s.GetStateVector()
	expected := map[uint64]uint64{
		1: 15,
		2: 3,
		3: 8,
	}
	if len(sv) != len(expected) {
		t.Fatalf("SV size=%d want %d", len(sv), len(expected))
	}
	for c, want := range expected {
		if got, ok := sv[c]; !ok || got != want {
			t.Errorf("SV[%d]=%d (ok=%v) want %d", c, got, ok, want)
		}
	}
}

func TestBlockStore_GetItemCleanStartEnd(t *testing.T) {
	s := NewBlockStore()
	it := makeItem(1, 100, 10) // covers clocks 100..109
	s.PushBlock(it)

	// Clean start at clock 103: slice should be [3, 9]
	slc, ok := s.GetItemCleanStart(block.ID{Client: 1, Clock: 103})
	if !ok {
		t.Fatal("GetItemCleanStart should succeed")
	}
	if slc.Ptr != it || slc.Start != 3 || slc.End != 9 {
		t.Errorf("GetItemCleanStart returned {Start:%d End:%d}, want {3, 9}", slc.Start, slc.End)
	}

	// Clean end at clock 103: slice should be [0, 3]
	slc, ok = s.GetItemCleanEnd(block.ID{Client: 1, Clock: 103})
	if !ok {
		t.Fatal("GetItemCleanEnd should succeed")
	}
	if slc.Ptr != it || slc.Start != 0 || slc.End != 3 {
		t.Errorf("GetItemCleanEnd returned {Start:%d End:%d}, want {0, 3}", slc.Start, slc.End)
	}

	// Clean operations on GC cells return false
	s.PushGC(2, 0, 5)
	if _, ok := s.GetItemCleanStart(block.ID{Client: 2, Clock: 3}); ok {
		t.Error("GetItemCleanStart on GC cell must return false")
	}
	if _, ok := s.GetItemCleanEnd(block.ID{Client: 2, Clock: 3}); ok {
		t.Error("GetItemCleanEnd on GC cell must return false")
	}
}

func TestBlockStore_GetClientLazy(t *testing.T) {
	s := NewBlockStore()
	if s.GetClient(99) != nil {
		t.Error("GetClient on unknown client must return nil (no lazy creation)")
	}
	l := s.GetClientMut(99)
	if l == nil {
		t.Fatal("GetClientMut must lazy-create the list")
	}
	if l.Len() != 0 {
		t.Errorf("freshly created list Len=%d want 0", l.Len())
	}
	if s.GetClient(99) == nil {
		t.Error("after GetClientMut, GetClient must return the same list")
	}
}

func TestBlockStore_MultiClient(t *testing.T) {
	s := NewBlockStore()
	s.PushBlock(makeItem(1, 0, 100))
	s.PushBlock(makeItem(2, 0, 50))
	s.PushBlock(makeItem(1, 100, 50))

	// Independent clocks per client
	if got := s.GetClock(1); got != 150 {
		t.Errorf("client 1 clock=%d want 150", got)
	}
	if got := s.GetClock(2); got != 50 {
		t.Errorf("client 2 clock=%d want 50", got)
	}

	// Independent lookups
	if it := s.GetItem(block.ID{Client: 1, Clock: 75}); it == nil || it.ID.Client != 1 {
		t.Errorf("GetItem returned %+v, want client 1 item", it)
	}
	if it := s.GetItem(block.ID{Client: 2, Clock: 25}); it == nil || it.ID.Client != 2 {
		t.Errorf("GetItem returned %+v, want client 2 item", it)
	}

	// Per-client invariants intact
	for _, c := range []uint64{1, 2} {
		if err := s.GetClient(c).CheckInvariants(); err != nil {
			t.Errorf("client %d invariants violated: %v", c, err)
		}
	}
}
