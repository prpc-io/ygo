package sync

import "testing"

// FuzzDecodeEnvelope feeds arbitrary bytes to the WebSocket frame
// decoder. The sole invariant is that it never panics on untrusted
// input; any error return is acceptable.
func FuzzDecodeEnvelope(f *testing.F) {
	// A valid SyncStep1 envelope built by the package's own encoder.
	f.Add(EncodeSyncStep1([]byte{0x00}))
	// Empty, single zero byte, and a truncated valid input.
	f.Add([]byte{})
	f.Add([]byte{0x00})
	f.Add(EncodeSyncStep1([]byte{0x01, 0x02})[:1])
	// Huge leading varint count to probe length-prefix handling.
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01})

	f.Fuzz(func(t *testing.T, data []byte) {
		// Invariant: never panics on arbitrary bytes. Errors are fine.
		DecodeEnvelope(data)
	})
}
