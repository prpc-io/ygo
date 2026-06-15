package awareness

import "testing"

// FuzzAwarenessApply drives Apply with arbitrary bytes. The contract:
// Apply never panics on garbage input and returns (Summary, error)
// with the state unchanged on decode error. A panic fails the fuzz run
// automatically; returned errors are expected and not asserted.
func FuzzAwarenessApply(f *testing.F) {
	// valid single-entry update (clientID 7, clock 1, state {"x":1})
	f.Add(encodeUpdate([]wireEntry{{ClientID: 7, Clock: 1, JSON: []byte(`{"x":1}`)}}))
	// valid removal entry (null sentinel)
	f.Add(encodeUpdate([]wireEntry{{ClientID: 7, Clock: 2, JSON: []byte(NullStateJSON)}}))
	f.Add([]byte{})           // empty
	f.Add([]byte{0x00})       // zero-count update: valid, no entries
	f.Add([]byte{0x01, 0x07}) // truncated: count 1 but entry data missing
	// huge leading varuint count to probe length-prefix amplification
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01})

	f.Fuzz(func(t *testing.T, data []byte) {
		a := New(7)
		before := a.States()

		// must not panic on arbitrary bytes
		_, err := a.Apply(data, "fuzz")
		if err != nil {
			// decode error => state unchanged
			if got := a.States(); len(got) != len(before) {
				t.Fatalf("state mutated on decode error: %d entries", len(got))
			}
		}
	})
}
