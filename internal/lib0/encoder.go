package lib0

import (
	"encoding/binary"
	"math"
)

// WriteVarUint appends the LEB128-style varuint encoding of n to buf and returns
// the extended slice. Bytes use 7 bits of value with the MSB as the continuation
// flag. This matches lib0's writeVarUint.
func WriteVarUint(buf []byte, n uint64) []byte {
	for n > 0x7f {
		buf = append(buf, byte(n&0x7f)|0x80)
		n >>= 7
	}
	return append(buf, byte(n&0x7f))
}

// WriteVarInt appends the zigzag-then-LEB128 encoding of n to buf and returns
// the extended slice. The sign is folded into the LSB of the first byte: bit 6
// holds the sign, bits 0-5 hold the lowest 6 bits of |n|, bit 7 is continuation.
// This matches lib0's writeVarInt.
func WriteVarInt(buf []byte, n int64) []byte {
	var sign byte
	if n < 0 {
		sign = 0x40
		// Flip to positive magnitude. math.MinInt64 case: ^uint64(0)>>1 + 1 = 1<<63.
		if n == math.MinInt64 {
			return writeVarIntMagnitude(buf, sign, 1<<63)
		}
		n = -n
	}
	return writeVarIntMagnitude(buf, sign, uint64(n))
}

func writeVarIntMagnitude(buf []byte, sign byte, mag uint64) []byte {
	// First byte: 6 bits of magnitude + sign bit + continuation if more.
	first := byte(mag&0x3f) | sign
	mag >>= 6
	if mag == 0 {
		return append(buf, first)
	}
	buf = append(buf, first|0x80)
	for mag > 0x7f {
		buf = append(buf, byte(mag&0x7f)|0x80)
		mag >>= 7
	}
	return append(buf, byte(mag&0x7f))
}

// WriteVarString appends the lib0 varstring encoding of s to buf: a varuint
// length prefix in bytes (UTF-8) followed by the UTF-8 bytes themselves.
func WriteVarString(buf []byte, s string) []byte {
	buf = WriteVarUint(buf, uint64(len(s)))
	return append(buf, s...)
}

// WriteVarUint8Array appends the lib0 varbuffer encoding of b to buf: a varuint
// length prefix followed by the raw bytes.
func WriteVarUint8Array(buf, b []byte) []byte {
	buf = WriteVarUint(buf, uint64(len(b)))
	return append(buf, b...)
}

// WriteUint8 appends a single byte.
func WriteUint8(buf []byte, n uint8) []byte {
	return append(buf, n)
}

// WriteUint16 appends a little-endian 16-bit unsigned integer.
func WriteUint16(buf []byte, n uint16) []byte {
	return binary.LittleEndian.AppendUint16(buf, n)
}

// WriteUint32 appends a little-endian 32-bit unsigned integer.
func WriteUint32(buf []byte, n uint32) []byte {
	return binary.LittleEndian.AppendUint32(buf, n)
}

// WriteFloat32 appends a big-endian IEEE-754 32-bit float. lib0 uses big-endian
// for floats to match the DataView default in JS.
func WriteFloat32(buf []byte, f float32) []byte {
	return binary.BigEndian.AppendUint32(buf, math.Float32bits(f))
}

// WriteFloat64 appends a big-endian IEEE-754 64-bit float.
func WriteFloat64(buf []byte, f float64) []byte {
	return binary.BigEndian.AppendUint64(buf, math.Float64bits(f))
}
