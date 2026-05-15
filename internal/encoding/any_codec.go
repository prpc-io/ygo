package encoding

import (
	"errors"
	"fmt"

	"github.com/Deln0r/ygo/internal/lib0"
)

// lib0 Any TLV type tags. Tag bytes are written as a single uint8
// followed by the variant-specific payload. Constants reproduced
// verbatim from lib0/encoding.js writeAny (and confirmed against
// docs/yrs-port-notes/update-v1.md content table).
const (
	AnyTagBinary    uint8 = 116
	AnyTagArray     uint8 = 117
	AnyTagObject    uint8 = 118
	AnyTagString    uint8 = 119
	AnyTagTrue      uint8 = 120
	AnyTagFalse     uint8 = 121
	AnyTagBigInt    uint8 = 122
	AnyTagFloat64   uint8 = 123
	AnyTagFloat32   uint8 = 124
	AnyTagInteger   uint8 = 125
	AnyTagNull      uint8 = 126
	AnyTagUndefined uint8 = 127
)

// ErrUnsupportedAnyTag is returned when DecodeAny encounters a type
// tag we do not yet support (BigInt, Float32, Object, Array, Binary).
var ErrUnsupportedAnyTag = errors.New("encoding: unsupported Any tag")

// EncodeAny appends the lib0 Any TLV encoding of v to buf.
//
// Supported variants in this commit: nil → null, bool → true/false,
// string → string, int / int64 → integer (varint, fits 32 bits) or
// float64 (otherwise), float64 → float64. The 32-bit integer cap
// matches lib0's writeAny which sniffs `BITS31` and falls through to
// float64 for larger numbers.
//
// Unsupported in this commit (panic): float32 (use float64 instead),
// big.Int / int64 outside int32 range as integer (auto-promotes to
// float64 with possible precision loss), []byte (use ContentBinary
// directly via the block layer for binary data), arrays, maps. These
// cover the value types common Map.Set users hit; richer Any
// support arrives with the Array / nested-types layer.
//
// Mirrors lib0/encoding.js writeAny.
func EncodeAny(buf []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(buf, AnyTagNull)
	case bool:
		if x {
			return append(buf, AnyTagTrue)
		}
		return append(buf, AnyTagFalse)
	case string:
		buf = append(buf, AnyTagString)
		return lib0.WriteVarString(buf, x)
	case int:
		if isInt32(int64(x)) {
			buf = append(buf, AnyTagInteger)
			return lib0.WriteVarInt(buf, int64(x))
		}
		buf = append(buf, AnyTagFloat64)
		return lib0.WriteFloat64(buf, float64(x))
	case int64:
		if isInt32(x) {
			buf = append(buf, AnyTagInteger)
			return lib0.WriteVarInt(buf, x)
		}
		buf = append(buf, AnyTagFloat64)
		return lib0.WriteFloat64(buf, float64(x))
	case float64:
		buf = append(buf, AnyTagFloat64)
		return lib0.WriteFloat64(buf, x)
	default:
		panic(fmt.Sprintf("encoding.EncodeAny: unsupported value type %T (supported: nil, bool, string, int, int64, float64; tracked in tech-debt.md)", v))
	}
}

// DecodeAny reads one lib0-Any-encoded value from buf and returns the
// value plus the unconsumed tail. Supports the same subset as
// EncodeAny; unsupported tags return ErrUnsupportedAnyTag without
// consuming bytes past the tag (callers should treat this as a
// stream-corrupting error rather than a recoverable skip).
func DecodeAny(buf []byte) (any, []byte, error) {
	if len(buf) < 1 {
		return nil, buf, lib0.ErrTruncated
	}
	tag := buf[0]
	buf = buf[1:]
	switch tag {
	case AnyTagNull, AnyTagUndefined:
		return nil, buf, nil
	case AnyTagTrue:
		return true, buf, nil
	case AnyTagFalse:
		return false, buf, nil
	case AnyTagString:
		s, n, err := lib0.ReadVarString(buf)
		if err != nil {
			return nil, buf, err
		}
		return s, buf[n:], nil
	case AnyTagInteger:
		v, n, err := lib0.ReadVarInt(buf)
		if err != nil {
			return nil, buf, err
		}
		return v, buf[n:], nil
	case AnyTagFloat64:
		v, n, err := lib0.ReadFloat64(buf)
		if err != nil {
			return nil, buf, err
		}
		return v, buf[n:], nil
	case AnyTagFloat32:
		v, n, err := lib0.ReadFloat32(buf)
		if err != nil {
			return nil, buf, err
		}
		return float64(v), buf[n:], nil
	default:
		return nil, buf, fmt.Errorf("%w: tag=%d", ErrUnsupportedAnyTag, tag)
	}
}

func isInt32(v int64) bool {
	const min32, max32 = int64(-1) << 31, int64(1)<<31 - 1
	return v >= min32 && v <= max32
}
