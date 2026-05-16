package awareness

import (
	"fmt"
	"math"

	"github.com/Deln0r/ygo/internal/lib0"
)

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
	buf = buf[n:]

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
		buf = buf[n:]

		out = append(out, wireEntry{
			ClientID: clientID,
			Clock:    uint32(clockU),
			JSON:     []byte(jsonStr),
		})
	}
	return out, buf, nil
}
