package encoding

import (
	"bytes"
	"reflect"
	"sort"
	"testing"
)

func TestIdSet_Empty(t *testing.T) {
	s := NewIdSet()
	got := s.Encode(nil)
	want := []byte{0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("encode empty IdSet = % x, want % x", got, want)
	}

	dec, tail, err := DecodeIdSet(got)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tail) != 0 {
		t.Errorf("trailing: % x", tail)
	}
	if dec.ClientCount() != 0 {
		t.Errorf("decoded ClientCount=%d, want 0", dec.ClientCount())
	}
}

func TestIdSet_InsertSingleRange(t *testing.T) {
	s := NewIdSet()
	s.Insert(1, 5, 3) // range [5, 8)

	if !s.Contains(1, 5) {
		t.Error("Contains(1, 5) = false, want true")
	}
	if !s.Contains(1, 7) {
		t.Error("Contains(1, 7) = false, want true (range end-1)")
	}
	if s.Contains(1, 8) {
		t.Error("Contains(1, 8) = true, want false (range end is exclusive)")
	}
	if s.Contains(1, 4) {
		t.Error("Contains(1, 4) = true, want false (before start)")
	}
	if s.Contains(2, 5) {
		t.Error("Contains(2, 5) = true, want false (different client)")
	}
}

func TestIdSet_InsertMergeAdjacent(t *testing.T) {
	s := NewIdSet()
	s.Insert(1, 0, 5)  // [0, 5)
	s.Insert(1, 5, 5)  // [5, 10) — adjacent, should merge to [0, 10)
	s.Insert(1, 10, 5) // [10, 15) — adjacent, should merge to [0, 15)

	collected := collectRanges(s, 1)
	if len(collected) != 1 {
		t.Fatalf("expected 1 merged range, got %d: %v", len(collected), collected)
	}
	if collected[0] != (Range{Start: 0, Length: 15}) {
		t.Errorf("merged range = %v, want {0, 15}", collected[0])
	}
}

func TestIdSet_InsertMergeOverlapping(t *testing.T) {
	s := NewIdSet()
	s.Insert(1, 0, 10) // [0, 10)
	s.Insert(1, 5, 10) // [5, 15) — overlaps, should merge to [0, 15)

	collected := collectRanges(s, 1)
	if len(collected) != 1 {
		t.Fatalf("expected 1 merged range, got %d", len(collected))
	}
	if collected[0] != (Range{Start: 0, Length: 15}) {
		t.Errorf("merged = %v, want {0, 15}", collected[0])
	}
}

func TestIdSet_InsertNonAdjacent(t *testing.T) {
	s := NewIdSet()
	s.Insert(1, 0, 5)  // [0, 5)
	s.Insert(1, 10, 5) // [10, 15) — gap, should remain separate
	s.Insert(1, 20, 5) // [20, 25) — another gap

	collected := collectRanges(s, 1)
	want := []Range{{0, 5}, {10, 5}, {20, 5}}
	if !reflect.DeepEqual(collected, want) {
		t.Errorf("ranges = %v, want %v", collected, want)
	}
}

func TestIdSet_InsertOutOfOrderMerges(t *testing.T) {
	s := NewIdSet()
	// Insert in scrambled order; result should still be sorted and merged.
	s.Insert(1, 20, 5) // [20, 25)
	s.Insert(1, 0, 5)  // [0, 5)
	s.Insert(1, 10, 5) // [10, 15)
	s.Insert(1, 5, 5)  // [5, 10) — bridges 0..5 and 10..15

	collected := collectRanges(s, 1)
	want := []Range{{0, 15}, {20, 5}}
	if !reflect.DeepEqual(collected, want) {
		t.Errorf("ranges = %v, want %v", collected, want)
	}
}

func TestIdSet_InsertZeroLengthIgnored(t *testing.T) {
	s := NewIdSet()
	s.Insert(1, 5, 0) // no-op
	if s.ClientCount() != 0 {
		t.Errorf("zero-length insert created a client entry; ClientCount=%d", s.ClientCount())
	}
}

func TestIdSet_RoundTrip(t *testing.T) {
	s := NewIdSet()
	s.Insert(1, 0, 10)
	s.Insert(1, 20, 5)
	s.Insert(2, 100, 1000)
	s.Insert(99, 0, 1)

	encoded := s.Encode(nil)
	dec, tail, err := DecodeIdSet(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tail) != 0 {
		t.Errorf("trailing: % x", tail)
	}

	// Compare structurally: same clients, same ranges per client.
	if dec.ClientCount() != s.ClientCount() {
		t.Errorf("ClientCount diverged: in=%d out=%d", s.ClientCount(), dec.ClientCount())
	}
	for c := range s.clients {
		decRanges := collectRanges(dec, c)
		origRanges := collectRanges(s, c)
		if !reflect.DeepEqual(decRanges, origRanges) {
			t.Errorf("client %d ranges diverged: in=%v out=%v", c, origRanges, decRanges)
		}
	}
}

func TestIdSet_EncodeIterationIsAscending(t *testing.T) {
	s := NewIdSet()
	// Insert in scrambled client-ID order; encode must emit ascending.
	s.Insert(99, 0, 1)
	s.Insert(1, 0, 1)
	s.Insert(50, 0, 1)

	var seen []uint64
	s.Iterate(func(client uint64, _ []Range) {
		seen = append(seen, client)
	})

	expected := []uint64{1, 50, 99}
	if !reflect.DeepEqual(seen, expected) {
		t.Errorf("iteration order = %v, want %v (ascending)", seen, expected)
	}
}

func TestIdSet_DeterministicEncode(t *testing.T) {
	a := NewIdSet()
	a.Insert(1, 0, 5)
	a.Insert(2, 0, 5)

	b := NewIdSet()
	b.Insert(2, 0, 5)
	b.Insert(1, 0, 5)

	if !bytes.Equal(a.Encode(nil), b.Encode(nil)) {
		t.Error("equivalent IdSets produced different bytes")
	}
}

func TestIdSet_ContainsAfterMerge(t *testing.T) {
	s := NewIdSet()
	s.Insert(1, 0, 5)
	s.Insert(1, 10, 5)
	s.Insert(1, 5, 5) // bridges to single [0, 15)

	for clock := uint64(0); clock < 15; clock++ {
		if !s.Contains(1, clock) {
			t.Errorf("Contains(1, %d) = false after merge; want true", clock)
		}
	}
	if s.Contains(1, 15) {
		t.Error("Contains(1, 15) = true; range is half-open")
	}
}

// collectRanges returns the sorted ranges for a single client, copied
// out so tests aren't reading aliased internal state.
func collectRanges(s *IdSet, client uint64) []Range {
	ranges := s.clients[client]
	out := make([]Range, len(ranges))
	copy(out, ranges)
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}
