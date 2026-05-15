package store

import (
	"testing"

	"github.com/Deln0r/ygo/internal/block"
)

func makeItem(client, clock, length uint64) *block.Item {
	return &block.Item{
		ID:  block.ID{Client: client, Clock: clock},
		Len: length,
	}
}

func TestClientBlockList_EmptyClock(t *testing.T) {
	l := NewClientBlockList()
	if got := l.Clock(); got != 0 {
		t.Errorf("empty list Clock=%d want 0", got)
	}
	if got := l.Len(); got != 0 {
		t.Errorf("empty list Len=%d want 0", got)
	}
	if _, ok := l.Get(0); ok {
		t.Error("Get(0) on empty list should return false")
	}
}

func TestClientBlockList_FindPivot_Empty(t *testing.T) {
	l := NewClientBlockList()
	// Must not panic on empty list (open question 1 from store.md —
	// yrs panics here, our Go port checks first).
	if _, ok := l.FindPivot(0); ok {
		t.Error("FindPivot on empty list should return false")
	}
	if _, ok := l.FindPivot(100); ok {
		t.Error("FindPivot on empty list with clock>0 should return false")
	}
}

func TestClientBlockList_FindPivot_SingleAtZero(t *testing.T) {
	// Open question 2 from store.md: yrs's interpolation divides by
	// zero when the only block ends at clock 0. Our sort.Search-based
	// FindPivot must handle this cleanly.
	l := NewClientBlockList()
	l.Push(CellOfItem(makeItem(1, 0, 1)))
	if i, ok := l.FindPivot(0); !ok || i != 0 {
		t.Errorf("FindPivot(0) on single len=1 block: got (%d, %v) want (0, true)", i, ok)
	}
	if _, ok := l.FindPivot(1); ok {
		t.Error("FindPivot(1) past end should return false")
	}
}

func TestClientBlockList_FindPivot_Multi(t *testing.T) {
	l := NewClientBlockList()
	l.Push(CellOfItem(makeItem(1, 0, 5)))    // covers 0..4
	l.Push(CellOfGC(5, 9))                   // covers 5..9
	l.Push(CellOfItem(makeItem(1, 10, 100))) // covers 10..109

	cases := []struct {
		clock   uint64
		wantIdx int
		wantOK  bool
	}{
		{0, 0, true},
		{4, 0, true},
		{5, 1, true},
		{7, 1, true},
		{9, 1, true},
		{10, 2, true},
		{109, 2, true},
		{110, 0, false}, // past end
		{1000, 0, false},
	}
	for _, tc := range cases {
		i, ok := l.FindPivot(tc.clock)
		if i != tc.wantIdx || ok != tc.wantOK {
			t.Errorf("FindPivot(%d)=(%d,%v) want (%d,%v)", tc.clock, i, ok, tc.wantIdx, tc.wantOK)
		}
	}

	if err := l.CheckInvariants(); err != nil {
		t.Errorf("invariants violated: %v", err)
	}
}

func TestClientBlockList_PushMonotonic(t *testing.T) {
	l := NewClientBlockList()
	for i := uint64(0); i < 5; i++ {
		l.Push(CellOfItem(makeItem(1, i*10, 10)))
		if got := l.Clock(); got != (i+1)*10 {
			t.Errorf("after push %d: Clock=%d want %d", i, got, (i+1)*10)
		}
	}
	if err := l.CheckInvariants(); err != nil {
		t.Errorf("invariants violated: %v", err)
	}
}

func TestClientBlockList_CheckInvariants_DetectsGap(t *testing.T) {
	l := NewClientBlockList()
	l.Push(CellOfItem(makeItem(1, 0, 5)))  // 0..4
	l.Push(CellOfItem(makeItem(1, 10, 5))) // 10..14 — gap at 5..9!
	if err := l.CheckInvariants(); err == nil {
		t.Error("CheckInvariants must detect the gap between 4 and 10")
	}
}

func TestClientBlockList_CheckInvariants_DetectsOverlap(t *testing.T) {
	l := NewClientBlockList()
	l.Push(CellOfItem(makeItem(1, 0, 10))) // 0..9
	l.Push(CellOfItem(makeItem(1, 5, 10))) // 5..14 — overlaps!
	if err := l.CheckInvariants(); err == nil {
		t.Error("CheckInvariants must detect the overlap")
	}
}

func TestClientBlockList_Insert(t *testing.T) {
	l := NewClientBlockList()
	l.Push(CellOfItem(makeItem(1, 0, 5)))  // 0..4
	l.Push(CellOfItem(makeItem(1, 10, 5))) // 10..14

	// Insert a fill cell at index 1 to bridge the gap.
	l.Insert(1, CellOfGC(5, 9))

	if l.Len() != 3 {
		t.Fatalf("Len=%d want 3", l.Len())
	}
	if c, _ := l.Get(1); c.Kind != CellKindGC || c.GC.Start != 5 || c.GC.End != 9 {
		t.Errorf("Get(1) = %+v, want GC{5,9}", c)
	}
	if err := l.CheckInvariants(); err != nil {
		t.Errorf("invariants violated after Insert: %v", err)
	}
}
