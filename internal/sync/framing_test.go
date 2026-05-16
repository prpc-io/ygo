package sync

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeDecodeSyncStep1_RoundTrip(t *testing.T) {
	sv := []byte{0x01, 0xa1, 0x05, 0x02} // arbitrary SV-shaped bytes
	wire := EncodeSyncStep1(sv)

	frame, tail, err := DecodeEnvelope(wire)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tail) != 0 {
		t.Errorf("tail = %x, want empty", tail)
	}
	if frame.Type != MessageSync {
		t.Errorf("Type = %d, want MessageSync", frame.Type)
	}
	if frame.SyncSub != SyncStep1 {
		t.Errorf("SyncSub = %d, want SyncStep1", frame.SyncSub)
	}
	if !bytes.Equal(frame.Payload, sv) {
		t.Errorf("Payload = %x, want %x", frame.Payload, sv)
	}
}

func TestEncodeDecodeSyncStep2_RoundTrip(t *testing.T) {
	update := []byte{0x00, 0x00} // empty V1 update — 0 clients, 0 DS ranges
	wire := EncodeSyncStep2(update)

	frame, _, err := DecodeEnvelope(wire)
	if err != nil {
		t.Fatal(err)
	}
	if frame.SyncSub != SyncStep2 || !bytes.Equal(frame.Payload, update) {
		t.Errorf("got SyncSub=%d Payload=%x, want SyncStep2 %x", frame.SyncSub, frame.Payload, update)
	}
}

func TestEncodeDecodeSyncUpdate_RoundTrip(t *testing.T) {
	update := []byte{0x01, 0x02, 0x03}
	wire := EncodeSyncUpdate(update)

	frame, _, err := DecodeEnvelope(wire)
	if err != nil {
		t.Fatal(err)
	}
	if frame.SyncSub != SyncUpdate || !bytes.Equal(frame.Payload, update) {
		t.Errorf("got SyncSub=%d Payload=%x, want SyncUpdate %x", frame.SyncSub, frame.Payload, update)
	}
}

func TestEncodeDecodeAwareness_RoundTrip(t *testing.T) {
	awUpdate := []byte{0x01, 0x05, 0x0a, 0x07, '{', '"', 'a', '"', ':', '1', '}'}
	wire := EncodeAwareness(awUpdate)

	frame, _, err := DecodeEnvelope(wire)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Type != MessageAwareness || !bytes.Equal(frame.Payload, awUpdate) {
		t.Errorf("got Type=%d Payload=%x, want MessageAwareness %x", frame.Type, frame.Payload, awUpdate)
	}
}

func TestEncodeDecodeQueryAwareness_EmptyPayload(t *testing.T) {
	wire := EncodeQueryAwareness()
	if !bytes.Equal(wire, []byte{0x03}) {
		t.Errorf("wire = %x, want [03]", wire)
	}
	frame, _, err := DecodeEnvelope(wire)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Type != MessageQueryAwareness {
		t.Errorf("Type = %d, want MessageQueryAwareness", frame.Type)
	}
	if len(frame.Payload) != 0 {
		t.Errorf("Payload non-empty: %x", frame.Payload)
	}
}

func TestEncodeDecodePong_EmptyPayload(t *testing.T) {
	wire := EncodePong()
	if !bytes.Equal(wire, []byte{0x0a}) {
		t.Errorf("wire = %x, want [0a]", wire)
	}
	frame, _, err := DecodeEnvelope(wire)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Type != MessagePong {
		t.Errorf("Type = %d, want MessagePong", frame.Type)
	}
}

func TestDecode_TruncatedEnvelope_ReturnsErr(t *testing.T) {
	_, _, err := DecodeEnvelope(nil)
	if !errors.Is(err, ErrTruncated) {
		t.Errorf("got %v, want ErrTruncated", err)
	}
}

func TestDecode_UnknownSyncSubType_ReturnsErr(t *testing.T) {
	// Sync envelope with sub-type 99.
	wire := []byte{0x00, 0x63} // varuint(MessageSync), varuint(99)
	_, _, err := DecodeEnvelope(wire)
	if !errors.Is(err, ErrUnknownSyncSubType) {
		t.Errorf("got %v, want ErrUnknownSyncSubType", err)
	}
}

func TestDecode_UnknownMessageType_PayloadOpaque(t *testing.T) {
	// MessageType 99 with three opaque bytes after.
	wire := []byte{0x63, 0xaa, 0xbb, 0xcc}
	frame, tail, err := DecodeEnvelope(wire)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Type != 99 {
		t.Errorf("Type = %d, want 99", frame.Type)
	}
	want := []byte{0xaa, 0xbb, 0xcc}
	if !bytes.Equal(frame.Payload, want) {
		t.Errorf("Payload = %x, want %x", frame.Payload, want)
	}
	if len(tail) != 0 {
		t.Errorf("tail = %x, want empty", tail)
	}
}

func TestEncodeSyncStep1_WireBytesMatchKnownLayout(t *testing.T) {
	// Hand-built fixture: SV bytes are [0x01, 0x05].
	//   0x00       varuint(0) = MessageSync
	//   0x00       varuint(0) = SyncStep1
	//   0x02       varuint(2) = SV byte length
	//   0x01 0x05  SV payload
	sv := []byte{0x01, 0x05}
	got := EncodeSyncStep1(sv)
	want := []byte{0x00, 0x00, 0x02, 0x01, 0x05}
	if !bytes.Equal(got, want) {
		t.Errorf("got %x, want %x", got, want)
	}
}

func TestEncodeAwareness_WireBytesMatchKnownLayout(t *testing.T) {
	// Awareness payload bytes [0xaa]:
	//   0x01  varuint(1) = MessageAwareness
	//   0x01  varuint(1) = payload byte length
	//   0xaa  payload byte
	got := EncodeAwareness([]byte{0xaa})
	want := []byte{0x01, 0x01, 0xaa}
	if !bytes.Equal(got, want) {
		t.Errorf("got %x, want %x", got, want)
	}
}
