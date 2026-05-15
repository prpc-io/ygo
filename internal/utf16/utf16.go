// Package utf16 provides UTF-16 code-unit length and offset helpers
// over Go's UTF-8 strings. JS Yjs and the V1 wire format speak UTF-16
// natively (Item.Len, Text indices, split offsets are all UTF-16
// units); we store strings as Go-idiomatic UTF-8 and convert at the
// boundary.
//
// See docs/yrs-port-notes/types-text.md gotcha 1: Item.Len is always
// UTF-16 code units; misreading it as bytes diverges from the JS
// peer on the first non-BMP character.
package utf16

import "unicode/utf8"

// Replacement is the Unicode REPLACEMENT CHARACTER (U+FFFD) used by
// SplitAt when the requested split lands inside a surrogate pair.
const Replacement = "�"

// Length returns the number of UTF-16 code units required to encode s.
// ASCII chars and BMP chars contribute 1; non-BMP chars (e.g. emoji)
// contribute 2 (a surrogate pair).
//
// Equivalent to JS `s.length` for the same string.
func Length(s string) uint64 {
	var n uint64
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// ByteOffset returns the byte index in s that corresponds to the
// given UTF-16 code unit offset.
//
// ok=true: the offset lands cleanly between two characters.
// ok=false: the offset is interior to a surrogate pair, i.e. the
//
//	caller asked to split a non-BMP character. In that case
//	byteIdx is the byte boundary AFTER the straddled char,
//	matching yrs's silent round-up. SplitAt below uses this
//	signal to apply U+FFFD replacement.
//
// Offsets past the end of s (in UTF-16 units) clip to len(s) with
// ok=true.
func ByteOffset(s string, utf16Offset uint64) (byteIdx int, ok bool) {
	if utf16Offset == 0 {
		return 0, true
	}
	var u uint64
	for byteIdx < len(s) {
		r, size := utf8.DecodeRuneInString(s[byteIdx:])
		var charU16 uint64 = 1
		if r > 0xFFFF {
			charU16 = 2
		}
		// Will this char's high surrogate land at the requested
		// offset boundary, or do we straddle it?
		if u+charU16 > utf16Offset {
			// The target offset is inside this char's surrogate
			// pair. Round up past the char.
			return byteIdx + size, false
		}
		u += charU16
		byteIdx += size
		if u == utf16Offset {
			return byteIdx, true
		}
	}
	// Past end of string.
	return len(s), true
}

// SplitAt splits s at the given UTF-16 code unit offset. Returns
// (left, right) such that Length(left) + Length(right) == Length(s)
// in the clean case.
//
// If the offset lands inside a surrogate pair, both halves' boundary
// chars are replaced with U+FFFD (matching JS Yjs behaviour;
// docs/yrs-port-notes/types-text.md gotcha 3 — yrs's no-op silently
// produces an orphan low surrogate). The replaced chars consume the
// same UTF-16 budget (1 unit each), so total Length is preserved.
func SplitAt(s string, utf16Offset uint64) (left, right string) {
	byteIdx, ok := ByteOffset(s, utf16Offset)
	if ok {
		return s[:byteIdx], s[byteIdx:]
	}
	// Surrogate-pair split. The straddled char occupies
	// s[byteIdx-prevSize:byteIdx] (4 UTF-8 bytes, 2 UTF-16 units).
	// Replace it with U+FFFD on each side: left half's tail goes to
	// U+FFFD, right half's head goes to U+FFFD.
	_, prevSize := utf8.DecodeLastRuneInString(s[:byteIdx])
	return s[:byteIdx-prevSize] + Replacement, Replacement + s[byteIdx:]
}
