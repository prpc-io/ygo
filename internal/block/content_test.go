package block

import (
	"reflect"
	"testing"
)

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

func TestContent_Split_String(t *testing.T) {
	c := Content{Kind: KindString, Str: "hello"}
	right, err := c.Split(2)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if c.Str != "he" {
		t.Errorf("left = %q, want %q", c.Str, "he")
	}
	if right.Kind != KindString || right.Str != "llo" {
		t.Errorf("right = %+v, want KindString %q", right, "llo")
	}
}

func TestContent_Split_Any(t *testing.T) {
	c := Content{Kind: KindAny, Anys: []Any{1, 2, 3, 4, 5}}
	right, err := c.Split(2)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !reflect.DeepEqual(c.Anys, []Any{1, 2}) {
		t.Errorf("left.Anys = %v, want [1 2]", c.Anys)
	}
	if right.Kind != KindAny || !reflect.DeepEqual(right.Anys, []Any{3, 4, 5}) {
		t.Errorf("right = %+v, want KindAny [3 4 5]", right)
	}
}

func TestContent_Split_JSON(t *testing.T) {
	c := Content{Kind: KindJSON, JSONStrs: []string{"a", "b", "c"}}
	right, err := c.Split(1)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !reflect.DeepEqual(c.JSONStrs, []string{"a"}) {
		t.Errorf("left = %v, want [a]", c.JSONStrs)
	}
	if right.Kind != KindJSON || !reflect.DeepEqual(right.JSONStrs, []string{"b", "c"}) {
		t.Errorf("right = %+v, want KindJSON [b c]", right)
	}
}

func TestContent_Split_Deleted(t *testing.T) {
	c := Content{Kind: KindDeleted, DeletedLen: 10}
	right, err := c.Split(3)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if c.DeletedLen != 3 {
		t.Errorf("left.DeletedLen = %d, want 3", c.DeletedLen)
	}
	if right.Kind != KindDeleted || right.DeletedLen != 7 {
		t.Errorf("right = %+v, want KindDeleted 7", right)
	}
}

func TestContent_Split_OutOfRange(t *testing.T) {
	cases := []struct {
		name   string
		c      Content
		offset uint64
	}{
		{"string offset 0", Content{Kind: KindString, Str: "abc"}, 0},
		{"string offset == len", Content{Kind: KindString, Str: "abc"}, 3},
		{"string offset > len", Content{Kind: KindString, Str: "abc"}, 10},
		{"any offset 0", Content{Kind: KindAny, Anys: []Any{1, 2}}, 0},
		{"any offset == len", Content{Kind: KindAny, Anys: []Any{1, 2}}, 2},
		{"deleted offset 0", Content{Kind: KindDeleted, DeletedLen: 5}, 0},
		{"deleted offset == len", Content{Kind: KindDeleted, DeletedLen: 5}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.c.Split(tc.offset); err == nil {
				t.Errorf("Split(%d) on %+v: expected error, got nil", tc.offset, tc.c)
			}
		})
	}
}

func TestContent_Split_NonSplittable(t *testing.T) {
	cases := []ContentKind{KindBinary, KindEmbed, KindFormat, KindType, KindDoc, KindMove, KindSkip, KindGC}
	for _, kind := range cases {
		c := Content{Kind: kind}
		if _, err := c.Split(1); err == nil {
			t.Errorf("Split on Kind %d: expected error, got nil", kind)
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
