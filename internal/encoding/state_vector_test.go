package encoding

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/Deln0r/ygo/internal/store"
)

func TestStateVector_Empty(t *testing.T) {
	sv := store.StateVector{}
	got := EncodeStateVector(sv, nil)
	want := []byte{0x00} // varuint(0) = single zero byte
	if !bytes.Equal(got, want) {
		t.Errorf("encode empty SV = % x, want % x", got, want)
	}

	dec, tail, err := DecodeStateVector(got)
	if err != nil {
		t.Fatalf("decode empty SV: %v", err)
	}
	if len(tail) != 0 {
		t.Errorf("trailing bytes after decode: % x", tail)
	}
	if len(dec) != 0 {
		t.Errorf("decoded SV not empty: %v", dec)
	}
}

func TestStateVector_RoundTrip(t *testing.T) {
	cases := []store.StateVector{
		{1: 5},
		{1: 5, 2: 10, 3: 100},
		{99: 1, 1: 99},                          // out-of-order keys; encode sorts
		{1: 0, 2: 0},                            // zero clocks
		{1<<40 + 7: 1<<40 + 11, 42: 0xdeadbeef}, // wide values
	}
	for i, sv := range cases {
		bytes := EncodeStateVector(sv, nil)
		dec, tail, err := DecodeStateVector(bytes)
		if err != nil {
			t.Errorf("case %d decode: %v", i, err)
			continue
		}
		if len(tail) != 0 {
			t.Errorf("case %d trailing bytes: % x", i, tail)
		}
		if !reflect.DeepEqual(map[uint64]uint64(dec), map[uint64]uint64(sv)) {
			t.Errorf("case %d round-trip diverged: in=%v out=%v", i, sv, dec)
		}
	}
}

func TestStateVector_DeterministicEncode(t *testing.T) {
	// Two equivalent SVs constructed in different insertion orders
	// must produce byte-identical encodings.
	a := store.StateVector{}
	a[1] = 10
	a[2] = 20
	a[3] = 30

	b := store.StateVector{}
	b[3] = 30
	b[1] = 10
	b[2] = 20

	if !bytes.Equal(EncodeStateVector(a, nil), EncodeStateVector(b, nil)) {
		t.Error("equivalent SVs produced different bytes; sort is non-deterministic")
	}
}

func TestStateVector_AppendsToBuf(t *testing.T) {
	sv := store.StateVector{1: 5}
	prefix := []byte{0xff, 0xfe}
	out := EncodeStateVector(sv, prefix)
	if len(out) <= len(prefix) {
		t.Fatal("encode did not append")
	}
	if !bytes.Equal(out[:len(prefix)], prefix) {
		t.Error("encode overwrote prefix bytes")
	}
}

func TestStateVector_DecodeTruncated(t *testing.T) {
	sv := store.StateVector{1: 5, 2: 10}
	full := EncodeStateVector(sv, nil)
	for cut := 1; cut < len(full); cut++ {
		if _, _, err := DecodeStateVector(full[:cut]); err == nil {
			t.Errorf("decode of truncated %d/%d bytes did not error", cut, len(full))
		}
	}
}
