package lib0

import (
	"testing"
)

// roundTripRle runs a sequence through the encoder + decoder and
// asserts the sequence returns unchanged. Used as the unit test
// shape for every RLE primitive.

func TestRle_SingleValue(t *testing.T) {
	enc := NewRleEncoder(WriteUint8Func)
	enc.Write(42)
	buf := enc.Bytes()

	dec := NewRleDecoder(buf, ReadUint8Func)
	v, err := dec.Read()
	if err != nil {
		t.Fatal(err)
	}
	if v != 42 {
		t.Errorf("got %d, want 42", v)
	}
}

func TestRle_LongRun(t *testing.T) {
	enc := NewRleEncoder(WriteUint8Func)
	for i := 0; i < 100; i++ {
		enc.Write(7)
	}
	enc.Write(8) // forces flush of the 100-long run
	buf := enc.Bytes()

	dec := NewRleDecoder(buf, ReadUint8Func)
	for i := 0; i < 100; i++ {
		v, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if v != 7 {
			t.Errorf("read %d: got %d, want 7", i, v)
		}
	}
	v, err := dec.Read()
	if err != nil {
		t.Fatal(err)
	}
	if v != 8 {
		t.Errorf("trailing: got %d, want 8", v)
	}
}

func TestRle_AlternatingValues(t *testing.T) {
	values := []uint64{1, 2, 1, 2, 1, 2}
	enc := NewRleEncoder(WriteUint8Func)
	for _, v := range values {
		enc.Write(v)
	}
	buf := enc.Bytes()

	dec := NewRleDecoder(buf, ReadUint8Func)
	for i, want := range values {
		v, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if v != want {
			t.Errorf("read %d: got %d, want %d", i, v, want)
		}
	}
}

func TestUintOptRle_SingleAndRun(t *testing.T) {
	// Encodes [1, 2, 3, 3, 3] — first three are singles (positive
	// varints), fourth+fifth become a run (negative-3 + count).
	values := []uint64{1, 2, 3, 3, 3}
	enc := NewUintOptRleEncoder()
	for _, v := range values {
		enc.Write(v)
	}
	buf := enc.Bytes()

	dec := NewUintOptRleDecoder(buf)
	for i, want := range values {
		v, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if v != want {
			t.Errorf("read %d: got %d, want %d", i, v, want)
		}
	}
}

func TestUintOptRle_ZeroValueRun(t *testing.T) {
	// Edge case: 0 with count >= 2 needs the "negative zero"
	// sentinel — writeVarIntSigned must emit the sign bit even
	// when magnitude is 0.
	values := []uint64{0, 0, 0, 0, 0}
	enc := NewUintOptRleEncoder()
	for _, v := range values {
		enc.Write(v)
	}
	buf := enc.Bytes()

	dec := NewUintOptRleDecoder(buf)
	for i, want := range values {
		v, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if v != want {
			t.Errorf("read %d: got %d, want %d", i, v, want)
		}
	}
}

func TestUintOptRle_LargeValue(t *testing.T) {
	// 30-bit value — fits comfortably in the 31-bit signed range
	// the encoder supports.
	v := uint64(1 << 30)
	enc := NewUintOptRleEncoder()
	enc.Write(v)
	enc.Write(v)
	enc.Write(v)
	buf := enc.Bytes()

	dec := NewUintOptRleDecoder(buf)
	for i := 0; i < 3; i++ {
		got, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if got != v {
			t.Errorf("read %d: got %d, want %d", i, got, v)
		}
	}
}

func TestIntDiffOptRle_MonotonicIncrement(t *testing.T) {
	// Encodes [10, 11, 12, 13, 14] — all deltas are +1, so this
	// compresses to one (diff=1, count=5) record.
	values := []int64{10, 11, 12, 13, 14}
	enc := NewIntDiffOptRleEncoder()
	for _, v := range values {
		enc.Write(v)
	}
	buf := enc.Bytes()

	dec := NewIntDiffOptRleDecoderFor(buf)
	for i, want := range values {
		v, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if v != want {
			t.Errorf("read %d: got %d, want %d", i, v, want)
		}
	}
}

func TestIntDiffOptRle_MixedDeltas(t *testing.T) {
	// Encodes [3, 1100, 1101, 1050, 0] — deltas are
	// [+3, +1097, +1, -51, -1050], all distinct, so each value
	// gets its own record (no compression but correct round-trip).
	values := []int64{3, 1100, 1101, 1050, 0}
	enc := NewIntDiffOptRleEncoder()
	for _, v := range values {
		enc.Write(v)
	}
	buf := enc.Bytes()

	dec := NewIntDiffOptRleDecoderFor(buf)
	for i, want := range values {
		v, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if v != want {
			t.Errorf("read %d: got %d, want %d", i, v, want)
		}
	}
}

func TestIntDiffOptRle_NegativeDeltas(t *testing.T) {
	values := []int64{100, 90, 80, 70, 60}
	enc := NewIntDiffOptRleEncoder()
	for _, v := range values {
		enc.Write(v)
	}
	buf := enc.Bytes()

	dec := NewIntDiffOptRleDecoderFor(buf)
	for i, want := range values {
		v, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if v != want {
			t.Errorf("read %d: got %d, want %d", i, v, want)
		}
	}
}

func TestIntDiffOptRle_ZeroDeltaRun(t *testing.T) {
	// Same value many times → delta=0 run → packs into one
	// record. Edge case: the encoded diff is 0 with the
	// count-follows bit, which goes through writeVarIntSigned
	// with magnitude=1 (encoded = 0*2 + 1 = 1).
	values := []int64{42, 42, 42, 42, 42}
	enc := NewIntDiffOptRleEncoder()
	for _, v := range values {
		enc.Write(v)
	}
	buf := enc.Bytes()

	dec := NewIntDiffOptRleDecoderFor(buf)
	for i, want := range values {
		v, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if v != want {
			t.Errorf("read %d: got %d, want %d", i, v, want)
		}
	}
}

func TestStringEncoder_RoundTrip(t *testing.T) {
	strings := []string{"hello", "world", "foo", "bar", "baz"}
	enc := NewStringEncoder()
	for _, s := range strings {
		enc.Write(s)
	}
	buf := enc.Bytes()

	dec, err := NewStringDecoder(buf)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range strings {
		got, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if got != want {
			t.Errorf("read %d: got %q, want %q", i, got, want)
		}
	}
}

func TestStringEncoder_EmptyString(t *testing.T) {
	strings := []string{"", "a", "", "bc"}
	enc := NewStringEncoder()
	for _, s := range strings {
		enc.Write(s)
	}
	buf := enc.Bytes()

	dec, err := NewStringDecoder(buf)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range strings {
		got, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if got != want {
			t.Errorf("read %d: got %q, want %q", i, got, want)
		}
	}
}

func TestStringEncoder_LongStringTriggersStaging(t *testing.T) {
	// The JS impl flushes to sarr when s grows past 19 chars.
	// We mirror — verify a string longer than 19 chars round-
	// trips correctly.
	strings := []string{"abcdefghijklmnopqrstuvwxyz", "short", "another moderately long string here"}
	enc := NewStringEncoder()
	for _, s := range strings {
		enc.Write(s)
	}
	buf := enc.Bytes()

	dec, err := NewStringDecoder(buf)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range strings {
		got, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if got != want {
			t.Errorf("read %d: got %q, want %q", i, got, want)
		}
	}
}

func TestStringEncoder_NonBMPCharacter(t *testing.T) {
	// Emoji = non-BMP = 2 UTF-16 code units per character.
	// StringEncoder length stream stores UTF-16 lengths, not
	// byte lengths — round-trip must preserve.
	strings := []string{"hello", "🚀", "world"}
	enc := NewStringEncoder()
	for _, s := range strings {
		enc.Write(s)
	}
	buf := enc.Bytes()

	dec, err := NewStringDecoder(buf)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range strings {
		got, err := dec.Read()
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if got != want {
			t.Errorf("read %d: got %q, want %q", i, got, want)
		}
	}
}

func TestUintOptRle_KnownByteLayout_SingleValue(t *testing.T) {
	// Hand-verified: writing a single value 42 should produce
	// writeVarInt(+42) which is one byte: 0x2A (42 fits in 6
	// bits, no continuation, no sign).
	enc := NewUintOptRleEncoder()
	enc.Write(42)
	buf := enc.Bytes()
	if len(buf) != 1 || buf[0] != 42 {
		t.Errorf("got %x, want [2a]", buf)
	}
}

func TestUintOptRle_KnownByteLayout_RunOfThree(t *testing.T) {
	// Writing [5, 5, 5] should produce writeVarInt(-5) +
	// writeVarUint(count-2=1). VarInt(-5) = 0x45 (sign bit
	// 0x40 + magnitude 5). VarUint(1) = 0x01.
	enc := NewUintOptRleEncoder()
	enc.Write(5)
	enc.Write(5)
	enc.Write(5)
	buf := enc.Bytes()
	if len(buf) != 2 || buf[0] != 0x45 || buf[1] != 0x01 {
		t.Errorf("got %x, want [45 01]", buf)
	}
}
