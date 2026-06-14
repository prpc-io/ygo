package awareness

import (
	"fmt"
	"math"

	"github.com/Deln0r/ygo/internal/lib0"
)

// MaxUpdateEntries bounds the number of per-client records a single
// awareness update may declare. decodeUpdate rejects any frame whose
// entry count exceeds this BEFORE allocating: the count is an
// attacker-controlled varuint, and a ten-byte blob can otherwise
// claim billions of entries, forcing a multi-exabyte slice
// pre-allocation — the classic length-prefix amplification DoS. This
// is THE allocation guard; the limit sits far above any real room's
// presence set while keeping the bounded pre-allocation trivially
// small.
const MaxUpdateEntries = 65536

// MaxStatePayloadBytes bounds a single client's JSON state on the
// wire. lib0.ReadVarString already bounds its read by the remaining
// buffer (no length-driven pre-alloc), so this is policy, not an OOM
// guard: defense-in-depth so one peer cannot pin memory with an
// oversized entry on callers whose transport imposes no read limit.
// Cursor, selection, and user-identity payloads run well under a
// kilobyte; 64 KiB is generous. (The server's WebSocket read limit
// is tighter still, so this never rejects legitimate server traffic.)
const MaxStatePayloadBytes = 64 * 1024

// wireEntry is the per-client wire-format record assembled before
// encoding. JSON is the raw payload bytes — either the user's
// JSON state or the literal four-byte "null" sentinel for removals.
type wireEntry struct {
	ClientID uint64
	Clock    uint32
	JSON     []byte
}

// encodeUpdate writes the V1 awareness wire bytes for entries.
//
// Wire layout per docs/yrs-port-notes/awareness.md §3:
//
//	varuint(count)
//	for each entry:
//	    varuint(clientID)
//	    varuint(clock)
//	    varstring(json)
//
// Iteration order matches the caller-supplied slice. No
// canonicalization happens here — round-trip byte equality is not
// part of the wire-format contract.
func encodeUpdate(entries []wireEntry) []byte {
	buf := lib0.WriteVarUint(nil, uint64(len(entries)))
	for _, e := range entries {
		buf = lib0.WriteVarUint(buf, e.ClientID)
		buf = lib0.WriteVarUint(buf, uint64(e.Clock))
		buf = lib0.WriteVarString(buf, string(e.JSON))
	}
	return buf
}

// decodeUpdate parses V1 awareness wire bytes into entries plus
// the unconsumed tail (in case multiple updates are concatenated).
//
// Returns an error if the clock value exceeds uint32. yrs and JS
// keep clocks in u32 / safe-integer range; a larger value indicates
// a protocol violation or corruption rather than a legitimate
// awareness update.
func decodeUpdate(buf []byte) ([]wireEntry, []byte, error) {
	count, n, err := lib0.ReadVarUint(buf)
	if err != nil {
		return nil, buf, fmt.Errorf("decode count: %w", err)
	}
	if count > MaxUpdateEntries {
		return nil, buf, fmt.Errorf("update entry count %d exceeds limit %d", count, MaxUpdateEntries)
	}
	buf = buf[n:]

	// count is bounded above, so the pre-allocation cannot be abused.
	out := make([]wireEntry, 0, count)
	for i := uint64(0); i < count; i++ {
		clientID, n, err := lib0.ReadVarUint(buf)
		if err != nil {
			return nil, buf, fmt.Errorf("decode entry[%d] clientID: %w", i, err)
		}
		buf = buf[n:]

		clockU, n, err := lib0.ReadVarUint(buf)
		if err != nil {
			return nil, buf, fmt.Errorf("decode entry[%d] clock: %w", i, err)
		}
		buf = buf[n:]
		if clockU > math.MaxUint32 {
			return nil, buf, fmt.Errorf("decode entry[%d] clock %d exceeds uint32", i, clockU)
		}

		jsonStr, n, err := lib0.ReadVarString(buf)
		if err != nil {
			return nil, buf, fmt.Errorf("decode entry[%d] json: %w", i, err)
		}
		if len(jsonStr) > MaxStatePayloadBytes {
			return nil, buf, fmt.Errorf("decode entry[%d] json payload %d bytes exceeds limit %d", i, len(jsonStr), MaxStatePayloadBytes)
		}
		buf = buf[n:]

		out = append(out, wireEntry{
			ClientID: clientID,
			Clock:    uint32(clockU),
			JSON:     []byte(jsonStr),
		})
	}
	return out, buf, nil
}
