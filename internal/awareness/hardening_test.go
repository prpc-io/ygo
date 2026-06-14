package awareness

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Deln0r/ygo/internal/lib0"
)

// TestDecodeUpdate_RejectsOversizedCountWithoutAllocating is the core
// DoS regression: a ten-byte blob declaring a colossal entry count
// must be rejected at the count check, before the slice
// pre-allocation that would otherwise try to reserve exabytes.
func TestDecodeUpdate_RejectsOversizedCountWithoutAllocating(t *testing.T) {
	blob := lib0.WriteVarUint(nil, math.MaxUint64) // count only, no entries
	if len(blob) > 10 {
		t.Fatalf("setup: varuint(MaxUint64) = %d bytes, want <= 10", len(blob))
	}
	_, _, err := decodeUpdate(blob)
	if err == nil {
		t.Fatal("decodeUpdate accepted MaxUint64 count; want rejection")
	}
}

// TestApply_RejectsOversizedCount confirms the cap surfaces through
// the public Apply path as ErrInvalidUpdate and leaves state intact.
func TestApply_RejectsOversizedCount(t *testing.T) {
	a := New(1)
	a.SetLocalState([]byte(`{"v":1}`))

	blob := lib0.WriteVarUint(nil, uint64(MaxUpdateEntries)+1)
	_, err := a.Apply(blob, "remote")
	if !errors.Is(err, ErrInvalidUpdate) {
		t.Fatalf("Apply err = %v, want ErrInvalidUpdate", err)
	}
	if len(a.states) != 1 {
		t.Errorf("states grew to %d on a rejected update; want 1 (local only)", len(a.states))
	}
}

// TestApply_RejectsOversizedPayload rejects a single entry whose JSON
// state exceeds the per-entry byte cap.
func TestApply_RejectsOversizedPayload(t *testing.T) {
	huge := bytes.Repeat([]byte("a"), MaxStatePayloadBytes+1)
	blob := encodeUpdate([]wireEntry{{ClientID: 10, Clock: 1, JSON: huge}})

	a := New(1)
	if _, err := a.Apply(blob, "remote"); !errors.Is(err, ErrInvalidUpdate) {
		t.Fatalf("Apply err = %v, want ErrInvalidUpdate", err)
	}
	if _, ok := a.States()[10]; ok {
		t.Error("oversized entry was applied; want dropped")
	}

	// A payload exactly at the limit is accepted.
	ok := bytes.Repeat([]byte("b"), MaxStatePayloadBytes)
	okBlob := encodeUpdate([]wireEntry{{ClientID: 11, Clock: 1, JSON: ok}})
	if _, err := a.Apply(okBlob, "remote"); err != nil {
		t.Fatalf("Apply at-limit payload err = %v, want nil", err)
	}
	if _, present := a.States()[11]; !present {
		t.Error("at-limit entry was dropped; want applied")
	}
}

// TestSetMaxClients_CapsNewClients verifies the presence cap refuses
// brand-new clientIDs once full, while existing clients keep updating
// and the local client is exempt.
func TestSetMaxClients_CapsNewClients(t *testing.T) {
	clock, _ := fixedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	a := NewWithClock(1, clock)
	a.SetMaxClients(2)
	a.SetLocalState([]byte(`{"v":1}`)) // states = {1}; len 1

	remote10 := NewWithClock(10, clock)
	remote10.SetLocalState([]byte(`{"a":1}`))
	if _, err := a.Apply(remote10.Encode(nil), "remote"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.States()[10]; !ok {
		t.Fatal("remote 10 not admitted under the cap")
	}

	// states is now full (local + 10). A third distinct client drops.
	remote20 := NewWithClock(20, clock)
	remote20.SetLocalState([]byte(`{"b":1}`))
	if _, err := a.Apply(remote20.Encode(nil), "remote"); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.States()[20]; ok {
		t.Error("remote 20 admitted past the cap; want dropped")
	}

	// The already-tracked client 10 can still update.
	remote10.SetLocalState([]byte(`{"a":2}`))
	if _, err := a.Apply(remote10.Encode(nil), "remote"); err != nil {
		t.Fatal(err)
	}
	if got := a.States()[10]; !bytes.Equal(got, []byte(`{"a":2}`)) {
		t.Errorf("existing client update blocked: states[10] = %s", got)
	}
	if len(a.states) != 2 {
		t.Errorf("states = %d, want 2 (local + 10)", len(a.states))
	}
}

// TestPurgeTombstones_DeletesOldTombstonesOnly confirms the GC frees
// only tombstones older than the threshold — never live entries, a
// fresh tombstone, or the local client.
func TestPurgeTombstones_DeletesOldTombstonesOnly(t *testing.T) {
	clock, nowPtr := fixedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	a := NewWithClock(1, clock)
	a.SetLocalState([]byte(`{"v":1}`))

	// Two remote clients go live, then one is tombstoned now.
	for _, id := range []uint64{10, 20} {
		r := NewWithClock(id, clock)
		r.SetLocalState([]byte(`{"x":1}`))
		if _, err := a.Apply(r.Encode(nil), "remote"); err != nil {
			t.Fatal(err)
		}
	}
	a.RemoveState(10) // old tombstone

	// Advance past the threshold, then create a fresh tombstone that
	// must survive this purge.
	*nowPtr = nowPtr.Add(60 * time.Second)
	a.RemoveState(20) // fresh tombstone

	purged := a.PurgeTombstones(30 * time.Second)
	if purged != 1 {
		t.Fatalf("purged = %d, want 1 (only the old tombstone)", purged)
	}
	if _, ok := a.states[10]; ok {
		t.Error("old tombstone 10 not purged")
	}
	if _, ok := a.states[20]; !ok {
		t.Error("fresh tombstone 20 was purged; want retained")
	}
	if _, ok := a.states[1]; !ok {
		t.Error("local client was purged")
	}
}

// TestPurgeTombstones_SparesLiveEntries confirms a non-tombstoned
// (still-online) entry is never deleted even when old.
func TestPurgeTombstones_SparesLiveEntries(t *testing.T) {
	clock, nowPtr := fixedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	a := NewWithClock(1, clock)

	r := NewWithClock(10, clock)
	r.SetLocalState([]byte(`{"x":1}`))
	if _, err := a.Apply(r.Encode(nil), "remote"); err != nil {
		t.Fatal(err)
	}

	*nowPtr = nowPtr.Add(time.Hour) // entry is old but still online
	if purged := a.PurgeTombstones(time.Minute); purged != 0 {
		t.Fatalf("purged = %d, want 0 (live entry must be spared)", purged)
	}
	if _, ok := a.States()[10]; !ok {
		t.Error("live entry 10 was purged")
	}
}
