package lib0

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureFile mirrors the JSON shape emitted by testdata/gen/gen-lib0.mjs.
type fixtureFile struct {
	Generator string    `json:"generator"`
	Cases     []fixture `json:"cases"`
}

type fixture struct {
	Kind     string  `json:"kind"`
	Name     string  `json:"name"`
	ValueU64 *uint64 `json:"valueU64,omitempty"`
	ValueI64 *int64  `json:"valueI64,omitempty"`
	ValueStr *string `json:"valueStr,omitempty"`
	ValueBuf *string `json:"valueBufHex,omitempty"`
	ValueF32 *string `json:"valueF32,omitempty"`
	ValueF64 *string `json:"valueF64,omitempty"`
	BytesHex string  `json:"bytesHex"`
}

// TestFixtures runs the JS lib0 reference fixtures captured at testdata/lib0.json.
// If the fixture file does not exist (e.g. fresh checkout without Node.js installed),
// the test is skipped. CI must regenerate it via testdata/gen/gen-lib0.mjs.
func TestFixtures(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "lib0.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("testdata/lib0.json not present; run testdata/gen/gen-lib0.mjs to regenerate")
		}
		t.Fatalf("read fixture: %v", err)
	}
	var ff fixtureFile
	if err := json.Unmarshal(data, &ff); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(ff.Cases) == 0 {
		t.Fatal("fixture file has no cases")
	}
	for _, c := range ff.Cases {
		t.Run(c.Kind+"/"+c.Name, func(t *testing.T) {
			want := mustHex(t, c.BytesHex)
			switch c.Kind {
			case "varuint":
				if c.ValueU64 == nil {
					t.Fatal("varuint case missing valueU64")
				}
				got := WriteVarUint(nil, *c.ValueU64)
				if !bytes.Equal(got, want) {
					t.Fatalf("encode mismatch: got % x want % x", got, want)
				}
				dec, n, err := ReadVarUint(want)
				if err != nil || n != len(want) || dec != *c.ValueU64 {
					t.Fatalf("decode: got %d (%d bytes, %v), want %d", dec, n, err, *c.ValueU64)
				}
			case "varint":
				if c.ValueI64 == nil {
					t.Fatal("varint case missing valueI64")
				}
				got := WriteVarInt(nil, *c.ValueI64)
				if !bytes.Equal(got, want) {
					t.Fatalf("encode mismatch: got % x want % x", got, want)
				}
				dec, n, err := ReadVarInt(want)
				if err != nil || n != len(want) || dec != *c.ValueI64 {
					t.Fatalf("decode: got %d (%d bytes, %v), want %d", dec, n, err, *c.ValueI64)
				}
			case "varstring":
				if c.ValueStr == nil {
					t.Fatal("varstring case missing valueStr")
				}
				got := WriteVarString(nil, *c.ValueStr)
				if !bytes.Equal(got, want) {
					t.Fatalf("encode mismatch: got % x want % x", got, want)
				}
				dec, n, err := ReadVarString(want)
				if err != nil || n != len(want) || dec != *c.ValueStr {
					t.Fatalf("decode: got %q (%d bytes, %v), want %q", dec, n, err, *c.ValueStr)
				}
			case "varbuffer":
				if c.ValueBuf == nil {
					t.Fatal("varbuffer case missing valueBufHex")
				}
				payload := mustHex(t, *c.ValueBuf)
				got := WriteVarUint8Array(nil, payload)
				if !bytes.Equal(got, want) {
					t.Fatalf("encode mismatch: got % x want % x", got, want)
				}
				dec, n, err := ReadVarUint8Array(want)
				if err != nil || n != len(want) || !bytes.Equal(dec, payload) {
					t.Fatalf("decode: got % x (%d bytes, %v), want % x", dec, n, err, payload)
				}
			case "float32":
				if c.ValueF32 == nil {
					t.Fatal("float32 case missing valueF32")
				}
				v := mustFloat32(t, *c.ValueF32)
				got := WriteFloat32(nil, v)
				if !bytes.Equal(got, want) {
					t.Fatalf("encode mismatch: got % x want % x", got, want)
				}
			case "float64":
				if c.ValueF64 == nil {
					t.Fatal("float64 case missing valueF64")
				}
				v := mustFloat64(t, *c.ValueF64)
				got := WriteFloat64(nil, v)
				if !bytes.Equal(got, want) {
					t.Fatalf("encode mismatch: got % x want % x", got, want)
				}
			default:
				t.Fatalf("unknown fixture kind: %q", c.Kind)
			}
		})
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		var b byte
		for j, c := range s[i : i+2] {
			var nibble byte
			switch {
			case c >= '0' && c <= '9':
				nibble = byte(c - '0')
			case c >= 'a' && c <= 'f':
				nibble = byte(c-'a') + 10
			case c >= 'A' && c <= 'F':
				nibble = byte(c-'A') + 10
			default:
				t.Fatalf("bad hex %q", s)
			}
			b |= nibble << uint(4*(1-j))
		}
		out[i/2] = b
	}
	return out
}

func mustFloat32(t *testing.T, s string) float32 {
	t.Helper()
	switch s {
	case "+Inf":
		return float32(math.Inf(1))
	case "-Inf":
		return float32(math.Inf(-1))
	case "NaN":
		return float32(math.NaN())
	}
	var f float32
	if _, err := jsonNumber(s, &f); err != nil {
		t.Fatalf("parse float32 %q: %v", s, err)
	}
	return f
}

func mustFloat64(t *testing.T, s string) float64 {
	t.Helper()
	switch s {
	case "+Inf":
		return math.Inf(1)
	case "-Inf":
		return math.Inf(-1)
	case "NaN":
		return math.NaN()
	}
	var f float64
	if _, err := jsonNumber(s, &f); err != nil {
		t.Fatalf("parse float64 %q: %v", s, err)
	}
	return f
}

func jsonNumber(s string, dst any) (int, error) {
	return len(s), json.Unmarshal([]byte(s), dst)
}
