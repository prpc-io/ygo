package encoding

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/Deln0r/ygo/internal/block"
)

func TestContent_RoundTrip_String(t *testing.T) {
	c := block.Content{Kind: block.KindString, Str: "hello"}
	buf := EncodeContent(nil, c)
	got, tail, err := DecodeContent(buf, uint8(block.KindString))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tail) != 0 {
		t.Errorf("trailing bytes: % x", tail)
	}
	if got.Kind != block.KindString || got.Str != "hello" {
		t.Errorf("got %+v, want KindString hello", got)
	}
}

func TestContent_RoundTrip_Binary(t *testing.T) {
	in := []byte{0x00, 0x01, 0x02, 0xff}
	c := block.Content{Kind: block.KindBinary, Bytes: in}
	buf := EncodeContent(nil, c)
	got, tail, err := DecodeContent(buf, uint8(block.KindBinary))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tail) != 0 {
		t.Errorf("trailing: % x", tail)
	}
	if !bytes.Equal(got.Bytes, in) {
		t.Errorf("got %v, want %v", got.Bytes, in)
	}
	// Decoder must copy — mutating the input buffer must not corrupt output.
	buf[len(buf)-1] = 0xaa
	if bytes.Equal(got.Bytes, in[:len(in)-1]) {
		t.Error("DecodeContent aliased input buffer")
	}
}

func TestContent_RoundTrip_Deleted(t *testing.T) {
	c := block.Content{Kind: block.KindDeleted, DeletedLen: 42}
	buf := EncodeContent(nil, c)
	got, _, err := DecodeContent(buf, uint8(block.KindDeleted))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.DeletedLen != 42 {
		t.Errorf("DeletedLen = %d, want 42", got.DeletedLen)
	}
}

func TestContent_RoundTrip_Any(t *testing.T) {
	c := block.Content{
		Kind: block.KindAny,
		Anys: []block.Any{"first", int64(42), true, nil, 3.14},
	}
	buf := EncodeContent(nil, c)
	got, _, err := DecodeContent(buf, uint8(block.KindAny))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got.Anys, c.Anys) {
		t.Errorf("got %v, want %v", got.Anys, c.Anys)
	}
}
