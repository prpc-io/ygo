package block

import "testing"

func TestContentKind_RefNumbers(t *testing.T) {
	// Verbatim from yrs/src/block.rs:28-72. These values are wire
	// constants and must never change.
	cases := []struct {
		kind ContentKind
		want uint8
	}{
		{KindGC, 0},
		{KindDeleted, 1},
		{KindJSON, 2},
		{KindBinary, 3},
		{KindString, 4},
		{KindEmbed, 5},
		{KindFormat, 6},
		{KindType, 7},
		{KindAny, 8},
		{KindDoc, 9},
		{KindSkip, 10},
		{KindMove, 11},
	}
	for _, tc := range cases {
		c := Content{Kind: tc.kind}
		if got := c.RefNumber(); got != tc.want {
			t.Errorf("Kind %d: RefNumber=%d want %d", tc.kind, got, tc.want)
		}
		if uint8(tc.kind) != tc.want {
			t.Errorf("Kind constant value %d != wire ref %d", tc.kind, tc.want)
		}
	}
}

func TestContent_IsCountable(t *testing.T) {
	cases := []struct {
		kind ContentKind
		want bool
	}{
		// Countable per yrs ItemContent::is_countable
		{KindAny, true},
		{KindBinary, true},
		{KindJSON, true},
		{KindString, true},
		{KindEmbed, true},
		{KindType, true},
		{KindDoc, true},
		// Not countable
		{KindDeleted, false},
		{KindFormat, false},
		{KindMove, false},
		// Parallel cell kinds — not real Content variants
		{KindGC, false},
		{KindSkip, false},
	}
	for _, tc := range cases {
		c := Content{Kind: tc.kind}
		if got := c.IsCountable(); got != tc.want {
			t.Errorf("Kind %d: IsCountable=%v want %v", tc.kind, got, tc.want)
		}
	}
}

func TestContent_Len(t *testing.T) {
	cases := []struct {
		name string
		c    Content
		want uint64
	}{
		{"empty Any", Content{Kind: KindAny}, 0},
		{"three Any", Content{Kind: KindAny, Anys: []Any{1, 2, 3}}, 3},
		{"empty JSON", Content{Kind: KindJSON}, 0},
		{"two JSON", Content{Kind: KindJSON, JSONStrs: []string{"a", "b"}}, 2},
		{"ascii string", Content{Kind: KindString, Str: "hello"}, 5},
		{"empty string", Content{Kind: KindString, Str: ""}, 0},
		{"binary", Content{Kind: KindBinary, Bytes: []byte{1, 2, 3, 4}}, 1},
		{"embed", Content{Kind: KindEmbed, Anys: []Any{42}}, 1},
		{"type", Content{Kind: KindType, Branch: &Branch{}}, 1},
		{"doc", Content{Kind: KindDoc, Doc: &Doc{}}, 1},
		{"deleted 5", Content{Kind: KindDeleted, DeletedLen: 5}, 5},
		{"skip 7", Content{Kind: KindSkip, DeletedLen: 7}, 7},
		{"format", Content{Kind: KindFormat, FormatKey: "bold"}, 1},
		{"move", Content{Kind: KindMove, Move: &Move{}}, 1},
		{"gc returns 0", Content{Kind: KindGC}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Len(OffsetUtf16); got != tc.want {
				t.Errorf("Len=%d want %d", got, tc.want)
			}
		})
	}
}
