package block

import "testing"

func TestItem_LastID(t *testing.T) {
	cases := []struct {
		name string
		item Item
		want ID
	}{
		{"single element", Item{ID: ID{1, 5}, Len: 1}, ID{1, 5}},
		{"three elements", Item{ID: ID{1, 5}, Len: 3}, ID{1, 7}},
		{"len 100", Item{ID: ID{42, 0}, Len: 100}, ID{42, 99}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.item.LastID(); !got.Equal(tc.want) {
				t.Errorf("LastID=%v want %v", got, tc.want)
			}
		})
	}
}

func TestItem_Info(t *testing.T) {
	originID := ID{1, 0}
	rightOriginID := ID{1, 5}
	subKey := "color"

	cases := []struct {
		name string
		item Item
		want uint8
	}{
		{
			"string content, no flags",
			Item{
				ID:      ID{1, 0},
				Len:     5,
				Content: Content{Kind: KindString, Str: "hello"},
			},
			uint8(KindString), // 0b0000_0100 = 4
		},
		{
			"string content with origin",
			Item{
				ID:      ID{1, 0},
				Len:     5,
				Origin:  &originID,
				Content: Content{Kind: KindString},
			},
			InfoHasOrigin | uint8(KindString), // 0b1000_0100
		},
		{
			"all three presence flags + Any content",
			Item{
				ID:          ID{1, 0},
				Len:         3,
				Origin:      &originID,
				RightOrigin: &rightOriginID,
				ParentSub:   &subKey,
				Content:     Content{Kind: KindAny, Anys: []Any{1, 2, 3}},
			},
			InfoHasOrigin | InfoHasRightOrigin | InfoHasParentSub | uint8(KindAny), // 0b1110_1000
		},
		{
			"deleted content, no presence",
			Item{
				ID:      ID{1, 0},
				Len:     2,
				Content: Content{Kind: KindDeleted, DeletedLen: 2},
			},
			uint8(KindDeleted), // 0b0000_0001
		},
		{
			"max content kind (Move=11) + all presence",
			Item{
				ID:          ID{1, 0},
				Len:         1,
				Origin:      &originID,
				RightOrigin: &rightOriginID,
				ParentSub:   &subKey,
				Content:     Content{Kind: KindMove, Move: &Move{}},
			},
			InfoHasOrigin | InfoHasRightOrigin | InfoHasParentSub | 0b1011, // 0b1110_1011
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.item.Info(); got != tc.want {
				t.Errorf("Info=0b%08b (0x%02x) want 0b%08b (0x%02x)",
					got, got, tc.want, tc.want)
			}
		})
	}
}

func TestItem_FlagAccessors(t *testing.T) {
	it := &Item{}

	// All flags start clear.
	if it.IsDeleted() || it.IsCountable() || it.IsKeep() || it.IsLinked() {
		t.Fatal("fresh item must have all flags clear")
	}

	// Set each, verify each, leave others alone.
	it.SetDeleted(true)
	if !it.IsDeleted() || it.IsCountable() || it.IsKeep() || it.IsLinked() {
		t.Fatalf("after SetDeleted(true): flags=0b%016b", it.Flags)
	}

	it.SetCountable(true)
	if !it.IsDeleted() || !it.IsCountable() || it.IsKeep() || it.IsLinked() {
		t.Fatalf("after SetCountable(true): flags=0b%016b", it.Flags)
	}

	it.SetKeep(true)
	it.SetLinked(true)
	if !it.IsDeleted() || !it.IsCountable() || !it.IsKeep() || !it.IsLinked() {
		t.Fatalf("after all set: flags=0b%016b", it.Flags)
	}

	// Bit positions match yrs constants.
	want := FlagDeleted | FlagCountable | FlagKeep | FlagLinked
	if it.Flags != want {
		t.Errorf("flags=0b%016b want 0b%016b", it.Flags, want)
	}

	// Clear each, verify others remain.
	it.SetDeleted(false)
	if it.IsDeleted() {
		t.Error("SetDeleted(false) did not clear")
	}
	if !it.IsCountable() || !it.IsKeep() || !it.IsLinked() {
		t.Error("SetDeleted(false) cleared other flags")
	}
}

func TestItem_FlagBitsDoNotOverlap(t *testing.T) {
	all := []uint16{FlagKeep, FlagCountable, FlagDeleted, FlagMarked, FlagLinked}
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			if a&b != 0 {
				t.Errorf("flag bits overlap: 0b%016b & 0b%016b = 0b%016b", a, b, a&b)
			}
		}
	}
}

func TestInfoBits_NoOverlapWithContentNibble(t *testing.T) {
	// Wire info byte presence flags must live in bits 5-7 only.
	// Content kinds are 0..11 = 4 bits, occupying bits 0-3.
	// Bit 4 is currently unused. Verify presence flags don't leak.
	for _, presence := range []uint8{InfoHasOrigin, InfoHasRightOrigin, InfoHasParentSub} {
		if presence&InfoContentMask != 0 {
			t.Errorf("presence flag 0b%08b overlaps content nibble", presence)
		}
		if presence&0b0001_0000 != 0 {
			t.Errorf("presence flag 0b%08b uses reserved bit 4", presence)
		}
	}
}

func TestItem_EqualByID(t *testing.T) {
	a := &Item{ID: ID{1, 5}, Len: 3, Content: Content{Kind: KindString, Str: "abc"}}
	b := &Item{ID: ID{1, 5}, Len: 99, Content: Content{Kind: KindAny}} // same ID, different everything else
	c := &Item{ID: ID{1, 6}, Len: 3, Content: Content{Kind: KindString, Str: "abc"}}

	if !a.EqualByID(b) {
		t.Error("same ID, different content: should be EqualByID")
	}
	if a.EqualByID(c) {
		t.Error("different clock: should not be EqualByID")
	}
	if !((*Item)(nil)).EqualByID(nil) {
		t.Error("nil.EqualByID(nil) should be true")
	}
	if a.EqualByID(nil) || ((*Item)(nil)).EqualByID(a) {
		t.Error("nil EqualByID non-nil should be false")
	}
}

func TestParent_IsResolved(t *testing.T) {
	cases := []struct {
		name string
		p    Parent
		want bool
	}{
		{"unknown", Parent{Kind: ParentUnknown}, false},
		{"named", Parent{Kind: ParentNamed, Named: "root"}, false},
		{"id", Parent{Kind: ParentID, ID: ID{1, 5}}, false},
		{"branch nil", Parent{Kind: ParentBranch, Branch: nil}, false},
		{"branch resolved", Parent{Kind: ParentBranch, Branch: &Branch{}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.IsResolved(); got != tc.want {
				t.Errorf("IsResolved=%v want %v", got, tc.want)
			}
		})
	}
}
