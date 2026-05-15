package encoding

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestAny_RoundTrip_BasicTypes(t *testing.T) {
	cases := []any{
		nil,
		true,
		false,
		"",
		"hello",
		"привет",
		int64(0),
		int64(1),
		int64(-1),
		int64(math.MaxInt32),
		int64(math.MinInt32),
		float64(0),
		float64(3.14),
		float64(-0.5),
		math.MaxFloat64,
	}
	for _, in := range cases {
		buf := EncodeAny(nil, in)
		out, tail, err := DecodeAny(buf)
		if err != nil {
			t.Errorf("decode %v: %v", in, err)
			continue
		}
		if len(tail) != 0 {
			t.Errorf("trailing bytes after decoding %v: % x", in, tail)
		}
		if !reflect.DeepEqual(out, in) {
			t.Errorf("round-trip %v (%T): got %v (%T)", in, in, out, out)
		}
	}
}

func TestAny_LargeIntegerPromotesToFloat64(t *testing.T) {
	// Outside int32 range → encoded as float64 per lib0 writeAny.
	in := int64(math.MaxInt32) + 1
	buf := EncodeAny(nil, in)
	if buf[0] != AnyTagFloat64 {
		t.Errorf("expected Float64 tag (123), got %d", buf[0])
	}
	out, _, err := DecodeAny(buf)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, ok := out.(float64); !ok || got != float64(in) {
		t.Errorf("got %v (%T), want %v as float64", out, out, float64(in))
	}
}

func TestAny_DecodeUnsupportedTag(t *testing.T) {
	// Tags 116 (binary), 117 (array), 118 (object), 122 (bigint) all
	// unsupported in this commit. Decoder must return
	// ErrUnsupportedAnyTag rather than panic or silently mis-parse.
	for _, tag := range []uint8{AnyTagBinary, AnyTagArray, AnyTagObject, AnyTagBigInt} {
		_, _, err := DecodeAny([]byte{tag})
		if !errors.Is(err, ErrUnsupportedAnyTag) {
			t.Errorf("tag %d: err = %v, want ErrUnsupportedAnyTag", tag, err)
		}
	}
}

func TestAny_DecodeTruncated(t *testing.T) {
	if _, _, err := DecodeAny(nil); err == nil {
		t.Error("decode of empty input must error")
	}
}
