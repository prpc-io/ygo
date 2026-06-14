package server_test

import (
	"testing"
	"time"

	"github.com/Deln0r/ygo/internal/awareness"
	syncpkg "github.com/Deln0r/ygo/internal/sync"
	"github.com/Deln0r/ygo/server"
)

// TestServer_AwarenessCap_FloodDoesNotReachPeers is the load-bearing
// DoS property: when one peer floods a room with fabricated
// clientIDs, the per-room cap drops the excess AND the server does
// not relay the dropped entries to other peers. A connected peer's
// view stays bounded by the cap, not by how much the attacker sent.
func TestServer_AwarenessCap_FloodDoesNotReachPeers(t *testing.T) {
	wsURL, _ := startTestServer(t, server.Options{
		OriginPatterns:      []string{"*"},
		MaxAwarenessClients: 2,
		AwarenessTimeout:    time.Hour, // keep the sweep out of the way
	})

	// B is a peer that must never see more than the cap.
	b := dialClient(t, wsURL, "flood", 400)
	defer b.close()
	b.read(t) // initial SyncStep1

	// A floods five fabricated presence clients, one frame each.
	a := dialClient(t, wsURL, "flood", 300)
	defer a.close()
	a.read(t) // initial SyncStep1
	for _, id := range []uint64{1000, 1001, 1002, 1003, 1004} {
		fake := awareness.New(id)
		fake.SetLocalState([]byte(`{"x":1}`))
		a.write(t, syncpkg.EncodeAwareness(fake.Encode([]uint64{id})))
	}

	// B applies every awareness frame it receives until it has seen
	// the two the cap admitted (1000, 1001).
	b.readUntil(t, func(f *syncpkg.Frame) bool {
		if f.Type != syncpkg.MessageAwareness {
			return false
		}
		if _, err := b.awareness.Apply(f.Payload, "server"); err != nil {
			t.Fatal(err)
		}
		_, ok0 := b.awareness.States()[1000]
		_, ok1 := b.awareness.States()[1001]
		return ok0 && ok1
	})

	// The dropped clientIDs must never have been relayed, and B's
	// view is exactly the two accepted entries.
	states := b.awareness.States()
	if len(states) != 2 {
		t.Fatalf("B sees %d presence clients, want 2 (cap); states=%v", len(states), keysOf(states))
	}
	for _, dropped := range []uint64{1002, 1003, 1004} {
		if _, ok := states[dropped]; ok {
			t.Errorf("dropped clientID %d was relayed to a peer", dropped)
		}
	}
}

func keysOf(m map[uint64][]byte) []uint64 {
	out := make([]uint64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestServer_AwarenessSweep_EvictsSilentClient verifies the server's
// periodic sweep marks a connected-but-silent presence entry offline
// and broadcasts the removal to peers. A connected client that stops
// heartbeating past the timeout is evicted; a peer learns its cursor
// is gone without that client ever disconnecting.
func TestServer_AwarenessSweep_EvictsSilentClient(t *testing.T) {
	wsURL, _ := startTestServer(t, server.Options{
		OriginPatterns:   []string{"*"},
		AwarenessTimeout: time.Second, // tick floors to 1s
	})

	// A announces presence, then goes silent (no heartbeat).
	a := dialClient(t, wsURL, "sweep", 300)
	defer a.close()
	a.read(t) // initial SyncStep1
	a.awareness.SetLocalState([]byte(`{"name":"Alice"}`))
	a.write(t, syncpkg.EncodeAwareness(a.awareness.Encode([]uint64{300})))
	a.readUntil(t, func(f *syncpkg.Frame) bool { return f.Type == syncpkg.MessageAwareness })

	// B connects and confirms it first sees A's presence.
	b := dialClient(t, wsURL, "sweep", 400)
	defer b.close()
	sawA := false
	for i := 0; i < 5 && !sawA; i++ {
		f := b.read(t)
		if f.Type == syncpkg.MessageAwareness {
			if _, err := b.awareness.Apply(f.Payload, "server"); err != nil {
				t.Fatal(err)
			}
			_, sawA = b.awareness.States()[300]
		}
	}
	if !sawA {
		t.Fatal("B never saw A's presence")
	}

	// A stays connected but silent. The sweep must evict 300 and
	// broadcast the removal; B applies it and 300 disappears.
	b.readUntil(t, func(f *syncpkg.Frame) bool {
		if f.Type != syncpkg.MessageAwareness {
			return false
		}
		if _, err := b.awareness.Apply(f.Payload, "server"); err != nil {
			t.Fatal(err)
		}
		_, present := b.awareness.States()[300]
		return !present
	})
}
