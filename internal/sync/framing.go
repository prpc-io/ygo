package sync

import (
	"errors"
	"fmt"

	"github.com/Deln0r/ygo/internal/lib0"
)

// Frame is the decoded outer-envelope message. Type indicates which
// MessageType discriminator the wire bytes started with; the
// remaining fields populate based on Type per
// docs/yrs-port-notes/protocol-sync.md §2.
//
// The decode helpers return a *Frame so callers can switch on Type
// without reaching back into raw bytes. Mutating fields on a Frame
// has no effect on the original wire bytes — the decoder copies.
type Frame struct {
	Type MessageType

	// SyncSub is set only when Type == MessageSync or MessageSyncReply.
	SyncSub SyncSubType

	// Payload is the post-discriminator bytes:
	//   MessageSync          → the inner update or state vector bytes
	//                          (already unwrapped from varbuffer)
	//   MessageAwareness     → awareness update bytes
	//                          (already unwrapped from varbuffer)
	//   MessageQueryAwareness→ nil (no payload)
	//   MessageStateless     → the stateless payload string bytes
	//   MessagePing/Pong     → nil
	//   other                → the raw remaining envelope bytes,
	//                          left opaque for caller-defined handling
	Payload []byte
}

// ErrTruncated wraps a decode failure where the envelope ended
// before all expected fields were read.
var ErrTruncated = errors.New("sync: envelope truncated")

// ErrUnknownSyncSubType is returned when a MessageSync envelope
// carries a sub-type outside the known {0, 1, 2} set. The receiver
// has no way to interpret an unknown sub-message; the caller should
// surface the error and close the connection per y-protocols
// convention.
var ErrUnknownSyncSubType = errors.New("sync: unknown sync sub-type")

// EncodeSyncStep1 builds a MessageSync envelope carrying SyncStep1.
// sv is the V1-encoded state vector bytes (as produced by
// encoding.EncodeStateVector). The envelope is self-delimited via
// the inner varbuffer and ready to be written as a single WS frame.
//
// Wire layout (port-note §1):
//
//	varuint(0)   = MessageSync
//	varuint(0)   = SyncStep1
//	varbuffer(sv)
func EncodeSyncStep1(sv []byte) []byte {
	buf := lib0.WriteVarUint(nil, uint64(MessageSync))
	buf = lib0.WriteVarUint(buf, uint64(SyncStep1))
	buf = lib0.WriteVarUint8Array(buf, sv)
	return buf
}

// EncodeSyncStep2 builds a MessageSync envelope carrying SyncStep2.
// update is the V1 update bytes (as produced by encoding.EncodeDiff
// or encoding.EncodeStateAsUpdate). An empty update is legal and
// signals "I have nothing the sender is missing" — see port-note
// gotcha 2.
func EncodeSyncStep2(update []byte) []byte {
	buf := lib0.WriteVarUint(nil, uint64(MessageSync))
	buf = lib0.WriteVarUint(buf, uint64(SyncStep2))
	buf = lib0.WriteVarUint8Array(buf, update)
	return buf
}

// EncodeSyncUpdate builds a MessageSync envelope carrying an
// unsolicited Update — the steady-state broadcast.
func EncodeSyncUpdate(update []byte) []byte {
	buf := lib0.WriteVarUint(nil, uint64(MessageSync))
	buf = lib0.WriteVarUint(buf, uint64(SyncUpdate))
	buf = lib0.WriteVarUint8Array(buf, update)
	return buf
}

// EncodeAwareness builds a MessageAwareness envelope carrying the
// given awareness update bytes (as produced by
// internal/awareness.Awareness.Encode).
//
// Wire layout: varuint(1) • varbuffer(awarenessUpdate)
func EncodeAwareness(awarenessUpdate []byte) []byte {
	buf := lib0.WriteVarUint(nil, uint64(MessageAwareness))
	buf = lib0.WriteVarUint8Array(buf, awarenessUpdate)
	return buf
}

// EncodeQueryAwareness builds an empty-payload QueryAwareness
// envelope — the client's request for the current full awareness
// snapshot. Server replies with an EncodeAwareness frame covering
// every known client.
func EncodeQueryAwareness() []byte {
	return lib0.WriteVarUint(nil, uint64(MessageQueryAwareness))
}

// EncodePong builds a MessagePong envelope — the application-layer
// ping reply (Hocuspocus only; y-websocket uses WS-level ping).
func EncodePong() []byte {
	return lib0.WriteVarUint(nil, uint64(MessagePong))
}

// DecodeEnvelope parses one envelope from b. Returns the decoded
// Frame plus the unconsumed tail (in case multiple envelopes are
// concatenated; over WebSocket each Read returns exactly one
// envelope and the tail is empty).
//
// Returns ErrTruncated when bytes run out mid-field, or
// ErrUnknownSyncSubType when a Sync envelope's inner discriminator
// is outside {0, 1, 2}.
func DecodeEnvelope(b []byte) (*Frame, []byte, error) {
	if len(b) < 1 {
		return nil, b, fmt.Errorf("%w: empty envelope", ErrTruncated)
	}
	typeU, n, err := lib0.ReadVarUint(b)
	if err != nil {
		return nil, b, fmt.Errorf("decode messageType: %w", err)
	}
	b = b[n:]
	mt := MessageType(typeU)

	frame := &Frame{Type: mt}

	switch mt {
	case MessageSync, MessageSyncReply:
		subU, n, err := lib0.ReadVarUint(b)
		if err != nil {
			return nil, b, fmt.Errorf("decode syncSubType: %w", err)
		}
		b = b[n:]
		sub := SyncSubType(subU)
		switch sub {
		case SyncStep1, SyncStep2, SyncUpdate:
			frame.SyncSub = sub
		default:
			return nil, b, fmt.Errorf("%w: %d", ErrUnknownSyncSubType, subU)
		}
		payload, n, err := lib0.ReadVarUint8Array(b)
		if err != nil {
			return nil, b, fmt.Errorf("decode sync payload: %w", err)
		}
		// Copy the payload so the caller can keep the Frame past
		// the next Read call's buffer reuse.
		frame.Payload = append([]byte(nil), payload...)
		return frame, b[n:], nil

	case MessageAwareness:
		payload, n, err := lib0.ReadVarUint8Array(b)
		if err != nil {
			return nil, b, fmt.Errorf("decode awareness payload: %w", err)
		}
		frame.Payload = append([]byte(nil), payload...)
		return frame, b[n:], nil

	case MessageQueryAwareness, MessagePing, MessagePong:
		// Empty payload — no further bytes consumed.
		return frame, b, nil

	default:
		// Unknown / not-yet-implemented type. Stash remaining bytes
		// opaquely; caller can forward to OnUnknownMessage hook or
		// drop. No further decoding here.
		frame.Payload = append([]byte(nil), b...)
		return frame, nil, nil
	}
}
