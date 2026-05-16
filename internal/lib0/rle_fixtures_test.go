package lib0

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// rleFixture mirrors testdata/rle-fixtures.json (gen-rle.mjs).
type rleFixture struct {
	Primitive   string `json:"primitive"`
	Description string `json:"description"`
	Values      []any  `json:"values"`
	BytesHex    string `json:"bytes_hex"`
}

func loadRleFixtures(t *testing.T) []rleFixture {
	t.Helper()
	candidates := []string{
		"../../testdata/rle-fixtures.json",
		"../../../testdata/rle-fixtures.json",
	}
	var raw []byte
	var err error
	for _, p := range candidates {
		raw, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("read rle-fixtures.json: %v", err)
	}
	var fixtures []rleFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatalf("parse rle-fixtures.json: %v", err)
	}
	return fixtures
}

// TestFixtures_RleDecodeMatchesJS decodes every fixture's bytes
// via the matching Go primitive and asserts the value sequence
// matches what JS lib0 fed into the corresponding encoder.
//
// Decode-direction proof: lib0 → Go. The encode direction
// is also tested per-fixture by encoding the JS values in Go
// and asserting byte-equality with the JS output.
func TestFixtures_RleDecodeMatchesJS(t *testing.T) {
	fixtures := loadRleFixtures(t)
	if len(fixtures) == 0 {
		t.Fatal("no fixtures loaded")
	}
	for _, fix := range fixtures {
		fix := fix
		t.Run(fix.Primitive+"/"+fix.Description, func(t *testing.T) {
			bytes, err := hex.DecodeString(fix.BytesHex)
			if err != nil {
				t.Fatal(err)
			}
			switch fix.Primitive {
			case "rle":
				dec := NewRleDecoder(bytes, ReadUint8Func)
				for i, want := range fix.Values {
					v, err := dec.Read()
					if err != nil {
						t.Fatalf("read %d: %v", i, err)
					}
					wantU := uint64(want.(float64))
					if v != wantU {
						t.Errorf("read %d: got %d, want %d", i, v, wantU)
					}
				}
			case "uint-opt-rle":
				dec := NewUintOptRleDecoder(bytes)
				for i, want := range fix.Values {
					v, err := dec.Read()
					if err != nil {
						t.Fatalf("read %d: %v", i, err)
					}
					wantU := uint64(want.(float64))
					if v != wantU {
						t.Errorf("read %d: got %d, want %d", i, v, wantU)
					}
				}
			case "int-diff-opt-rle":
				dec := NewIntDiffOptRleDecoderFor(bytes)
				for i, want := range fix.Values {
					v, err := dec.Read()
					if err != nil {
						t.Fatalf("read %d: %v", i, err)
					}
					wantI := int64(want.(float64))
					if v != wantI {
						t.Errorf("read %d: got %d, want %d", i, v, wantI)
					}
				}
			case "string":
				dec, err := NewStringDecoder(bytes)
				if err != nil {
					t.Fatal(err)
				}
				for i, want := range fix.Values {
					v, err := dec.Read()
					if err != nil {
						t.Fatalf("read %d: %v", i, err)
					}
					wantS := want.(string)
					if v != wantS {
						t.Errorf("read %d: got %q, want %q", i, v, wantS)
					}
				}
			default:
				t.Fatalf("unknown primitive: %s", fix.Primitive)
			}
		})
	}
}

// TestFixtures_RleEncodeMatchesJS encodes the JS-supplied value
// sequence via the matching Go primitive and asserts byte-equality
// with the JS output. Bi-directional cross-language parity.
func TestFixtures_RleEncodeMatchesJS(t *testing.T) {
	fixtures := loadRleFixtures(t)
	for _, fix := range fixtures {
		fix := fix
		t.Run(fix.Primitive+"/"+fix.Description, func(t *testing.T) {
			want, _ := hex.DecodeString(fix.BytesHex)
			var got []byte
			switch fix.Primitive {
			case "rle":
				enc := NewRleEncoder(WriteUint8Func)
				for _, v := range fix.Values {
					enc.Write(uint64(v.(float64)))
				}
				got = enc.Bytes()
			case "uint-opt-rle":
				enc := NewUintOptRleEncoder()
				for _, v := range fix.Values {
					enc.Write(uint64(v.(float64)))
				}
				got = enc.Bytes()
			case "int-diff-opt-rle":
				enc := NewIntDiffOptRleEncoder()
				for _, v := range fix.Values {
					enc.Write(int64(v.(float64)))
				}
				got = enc.Bytes()
			case "string":
				enc := NewStringEncoder()
				for _, v := range fix.Values {
					enc.Write(v.(string))
				}
				got = enc.Bytes()
			default:
				t.Fatalf("unknown primitive: %s", fix.Primitive)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("byte mismatch:\n got  %x\n want %x", got, want)
			}
		})
	}
}
