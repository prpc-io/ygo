package utf16

import "testing"

func TestLength(t *testing.T) {
	cases := []struct {
		s    string
		want uint64
	}{
		{"", 0},
		{"hello", 5},            // 5 ASCII
		{"привет", 6},           // 6 Cyrillic, each 1 UTF-16 unit
		{"😀", 2},                // 1 non-BMP = 2 UTF-16 units
		{"a😀b", 4},              // 1 + 2 + 1
		{"😀😀", 4},               // 2 non-BMP
		{"hello 世界", 5 + 1 + 2}, // 5 ASCII + space + 2 BMP CJK
		{"�", 1},                // U+FFFD itself is BMP
	}
	for _, tc := range cases {
		if got := Length(tc.s); got != tc.want {
			t.Errorf("Length(%q) = %d, want %d", tc.s, got, tc.want)
		}
	}
}

func TestByteOffset_CleanBoundaries(t *testing.T) {
	cases := []struct {
		s        string
		utf16Off uint64
		wantByte int
	}{
		{"hello", 0, 0},
		{"hello", 5, 5},
		{"hello", 3, 3},
		{"a😀b", 0, 0},
		{"a😀b", 1, 1},
		{"a😀b", 3, 5}, // a (1 byte, 1 u16), 😀 (4 bytes, 2 u16); offset 3 = after 😀, byte 5
		{"a😀b", 4, 6},
		{"привет", 3, 6}, // 3 Cyrillic = 6 bytes
	}
	for _, tc := range cases {
		got, ok := ByteOffset(tc.s, tc.utf16Off)
		if !ok {
			t.Errorf("ByteOffset(%q, %d) ok=false; want ok=true", tc.s, tc.utf16Off)
			continue
		}
		if got != tc.wantByte {
			t.Errorf("ByteOffset(%q, %d) byteIdx = %d, want %d", tc.s, tc.utf16Off, got, tc.wantByte)
		}
	}
}

func TestByteOffset_SurrogateSplit(t *testing.T) {
	// Offset inside a surrogate pair: a😀 has a (1 unit) + 😀 (2
	// units). Offset 2 lands between 😀's two surrogates.
	got, ok := ByteOffset("a😀b", 2)
	if ok {
		t.Errorf("ByteOffset on mid-surrogate should return ok=false; got byteIdx=%d ok=true", got)
	}
	// Round-up: a is 1 byte, 😀 is 4 bytes — boundary AFTER 😀ay = byte 5.
	if got != 5 {
		t.Errorf("ByteOffset round-up byteIdx = %d, want 5", got)
	}
}

func TestSplitAt_CleanBoundary(t *testing.T) {
	cases := []struct {
		s         string
		off       uint64
		wantLeft  string
		wantRight string
	}{
		{"hello", 0, "", "hello"},
		{"hello", 5, "hello", ""},
		{"hello", 2, "he", "llo"},
		{"a😀b", 0, "", "a😀b"},
		{"a😀b", 1, "a", "😀b"},
		{"a😀b", 3, "a😀", "b"}, // after the emoji
		{"a😀b", 4, "a😀b", ""},
	}
	for _, tc := range cases {
		left, right := SplitAt(tc.s, tc.off)
		if left != tc.wantLeft || right != tc.wantRight {
			t.Errorf("SplitAt(%q, %d) = (%q, %q), want (%q, %q)",
				tc.s, tc.off, left, right, tc.wantLeft, tc.wantRight)
		}
	}
}

func TestSplitAt_SurrogateReplacement(t *testing.T) {
	// "a😀b" — split at UTF-16 offset 2 lands inside 😀.
	// Expect left = "a" + U+FFFD, right = U+FFFD + "b".
	left, right := SplitAt("a😀b", 2)
	wantLeft := "a" + Replacement
	wantRight := Replacement + "b"
	if left != wantLeft || right != wantRight {
		t.Errorf("SplitAt mid-surrogate = (%q, %q), want (%q, %q)", left, right, wantLeft, wantRight)
	}
	// UTF-16 length must be preserved: original "a😀b" = 4 units;
	// after split: "a�" = 2 units, "�b" = 2 units; total 4.
	if Length(left)+Length(right) != Length("a😀b") {
		t.Errorf("UTF-16 length not preserved: left=%d right=%d original=%d",
			Length(left), Length(right), Length("a😀b"))
	}
}

func TestSplitAt_RoundTripWithLength(t *testing.T) {
	// For arbitrary clean offsets, Length(left)+Length(right) == Length(s).
	cases := []struct {
		s   string
		off uint64
	}{
		{"hello world", 6},
		{"привет мир", 7},
		{"😀😀😀", 4},
	}
	for _, tc := range cases {
		left, right := SplitAt(tc.s, tc.off)
		gotLen := Length(left) + Length(right)
		wantLen := Length(tc.s)
		if gotLen != wantLen {
			t.Errorf("SplitAt(%q, %d): Length(left)+Length(right)=%d, want %d (left=%q right=%q)",
				tc.s, tc.off, gotLen, wantLen, left, right)
		}
	}
}
