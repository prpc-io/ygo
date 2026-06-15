package types

import (
	"bytes"
	"testing"

	"github.com/Deln0r/ygo/internal/block"
)

// FuzzDecodeRelativePosition feeds arbitrary bytes to the cursor/anchor
// decoder. Primary invariant: never panics on untrusted input. When a
// value decodes, re-encoding it must round-trip identically (the
// grammar has no trailing-garbage path, so this cannot false-positive).
func FuzzDecodeRelativePosition(f *testing.F) {
	// Valid anchors built via the package's own encoder.
	tname := "root"
	for _, rpos := range []RelativePosition{
		{Item: &block.ID{Client: 1, Clock: 2}, Assoc: 0},
		{TName: &tname, Assoc: -1},
		{Type: &block.ID{Client: 9, Clock: 0}, Assoc: 1},
	} {
		if enc, err := EncodeRelativePosition(rpos); err == nil {
			f.Add(enc)
		}
	}
	// Edge cases: empty, single byte, truncated item anchor (tag only),
	// and a huge leading varuint tag.
	f.Add([]byte{})
	f.Add([]byte{0x00})
	f.Add([]byte{0x00, 0x01})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01})

	f.Fuzz(func(t *testing.T, data []byte) {
		rpos, err := DecodeRelativePosition(data)
		if err != nil {
			return
		}
		enc, err := EncodeRelativePosition(rpos)
		if err != nil {
			t.Fatalf("re-encode of decoded value failed: %v", err)
		}
		got, err := DecodeRelativePosition(enc)
		if err != nil {
			t.Fatalf("re-decode failed: %v", err)
		}
		reenc, err := EncodeRelativePosition(got)
		if err != nil {
			t.Fatalf("re-encode after re-decode failed: %v", err)
		}
		if !bytes.Equal(enc, reenc) {
			t.Fatalf("not idempotent: % x -> % x", enc, reenc)
		}
	})
}
