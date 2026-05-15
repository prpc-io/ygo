package store

import (
	"testing"

	"github.com/Deln0r/ygo/internal/block"
)

func TestBlockCell_Item_Accessors(t *testing.T) {
	it := &block.Item{
		ID:      block.ID{Client: 1, Clock: 10},
		Len:     5,
		Content: block.Content{Kind: block.KindString, Str: "hello"},
	}
	c := CellOfItem(it)

	if c.Kind != CellKindItem {
		t.Errorf("Kind=%d want %d", c.Kind, CellKindItem)
	}
	if got := c.ClockStart(); got != 10 {
		t.Errorf("ClockStart=%d want 10", got)
	}
	if got := c.ClockEnd(); got != 14 {
		t.Errorf("ClockEnd=%d want 14", got)
	}
	if got := c.Len(); got != 5 {
		t.Errorf("Len=%d want 5", got)
	}
	if c.IsDeleted() {
		t.Error("fresh item should not be deleted")
	}
	if c.AsItem() != it {
		t.Error("AsItem should return underlying *Item")
	}

	// Tombstoning the item must reflect through the cell.
	it.SetDeleted(true)
	if !c.IsDeleted() {
		t.Error("tombstoned item: cell IsDeleted should be true")
	}
}

func TestBlockCell_GC_Accessors(t *testing.T) {
	c := CellOfGC(20, 24)

	if c.Kind != CellKindGC {
		t.Errorf("Kind=%d want %d", c.Kind, CellKindGC)
	}
	if got := c.ClockStart(); got != 20 {
		t.Errorf("ClockStart=%d want 20", got)
	}
	if got := c.ClockEnd(); got != 24 {
		t.Errorf("ClockEnd=%d want 24", got)
	}
	if got := c.Len(); got != 5 {
		t.Errorf("Len=%d want 5", got)
	}
	if !c.IsDeleted() {
		t.Error("GC cell must report IsDeleted=true")
	}
	if c.AsItem() != nil {
		t.Error("AsItem on GC cell must return nil")
	}
}

func TestGC_Len(t *testing.T) {
	cases := []struct {
		gc   GC
		want uint64
	}{
		{GC{Start: 0, End: 0}, 1},
		{GC{Start: 5, End: 9}, 5},
		{GC{Start: 100, End: 199}, 100},
	}
	for _, tc := range cases {
		if got := tc.gc.Len(); got != tc.want {
			t.Errorf("GC{%d,%d}.Len=%d want %d", tc.gc.Start, tc.gc.End, got, tc.want)
		}
	}
}

func TestItemSlice_Geometry(t *testing.T) {
	it := &block.Item{ID: block.ID{Client: 1, Clock: 100}, Len: 10}

	// Full slice
	full := ItemSlice{Ptr: it, Start: 0, End: 9}
	if !full.AdjacentLeft() || !full.AdjacentRight() {
		t.Error("full slice must be adjacent on both sides")
	}
	if full.Len() != 10 {
		t.Errorf("Len=%d want 10", full.Len())
	}
	if full.ClockStart() != 100 || full.ClockEnd() != 109 {
		t.Errorf("ClockStart/End = %d, %d want 100, 109", full.ClockStart(), full.ClockEnd())
	}

	// Middle slice
	mid := ItemSlice{Ptr: it, Start: 3, End: 6}
	if mid.AdjacentLeft() || mid.AdjacentRight() {
		t.Error("middle slice must not be adjacent on either side")
	}
	if mid.Len() != 4 {
		t.Errorf("Len=%d want 4", mid.Len())
	}
	if mid.ClockStart() != 103 || mid.ClockEnd() != 106 {
		t.Errorf("ClockStart/End = %d, %d want 103, 106", mid.ClockStart(), mid.ClockEnd())
	}

	// Single-clock block (open question 5 in store.md)
	tiny := &block.Item{ID: block.ID{Client: 1, Clock: 50}, Len: 1}
	one := ItemSlice{Ptr: tiny, Start: 0, End: 0}
	if !one.AdjacentLeft() || !one.AdjacentRight() {
		t.Error("len=1 block: slice must be adjacent on both sides")
	}
	if one.Len() != 1 {
		t.Errorf("len=1: Len=%d want 1", one.Len())
	}
}
