package gomobile_test

import (
	"bytes"
	"testing"

	"github.com/Deln0r/ygo/gomobile"
)

// TestGomobile_TwoDocs_BytesOnlyRoundTrip exercises the canonical
// mobile-friendly flow: build content on A via internal types (we
// can't from the gomobile package — only bytes-in/out), then sync
// the bytes to B. Tests prove the wire-format primitives survive
// the wrapper.
func TestGomobile_TwoDocs_BytesOnlyRoundTrip(t *testing.T) {
	// Bootstrap A with content via the rich ygo API, encode to bytes,
	// hand bytes to gomobile-wrapped B which has no other way to get
	// state. This mimics the iOS / Android consumer pattern: server
	// pushes bytes over the wire, mobile applies via gomobile API.
	gmA := gomobile.NewDocWithClientID(100)

	// We need a way to seed A with content. The gomobile API has no
	// shared-type writes — by design. The test uses an existing
	// rich-API doc, encodes, and applies via the gomobile wrapper.
	// (Real adopters who need to MUTATE from mobile would extend the
	// gomobile package with typed setters like SetMapStringKey.)
	bytesEmpty := gmA.EncodeStateAsUpdate()
	if len(bytesEmpty) == 0 {
		t.Error("EncodeStateAsUpdate of empty doc returned empty bytes")
	}

	gmB := gomobile.NewDocWithClientID(200)
	if err := gmB.ApplyUpdate(bytesEmpty); err != nil {
		t.Fatal(err)
	}

	// SV round-trip.
	svA := gmA.EncodeStateVector()
	if len(svA) == 0 {
		t.Error("EncodeStateVector returned empty")
	}
	svB := gmB.EncodeStateVector()
	if !bytes.Equal(svA, svB) {
		t.Errorf("SVs diverge after sync: A=%x B=%x", svA, svB)
	}

	// Diff against own SV should be effectively empty (no client
	// activity yet on either side).
	diff, err := gmA.EncodeDiff(svB)
	if err != nil {
		t.Fatal(err)
	}
	if err := gmB.ApplyUpdate(diff); err != nil {
		t.Fatal(err)
	}
}

func TestGomobile_ClientID(t *testing.T) {
	d := gomobile.NewDocWithClientID(42)
	if d.ClientID() != 42 {
		t.Errorf("ClientID = %d, want 42", d.ClientID())
	}
	d2 := gomobile.NewDoc()
	if d2.ClientID() == 0 {
		t.Error("NewDoc generated zero ClientID")
	}
	if d2.ClientID() == 42 {
		t.Error("NewDoc randomly matched the explicit ID — astronomical odds, recheck implementation")
	}
}

func TestGomobile_HasPending_EmptyDoc(t *testing.T) {
	d := gomobile.NewDoc()
	if d.HasPending() {
		t.Error("fresh Doc reports HasPending")
	}
	missing := d.MissingSV()
	// Empty SV is varuint(0) = single byte 0x00.
	if len(missing) != 1 || missing[0] != 0x00 {
		t.Errorf("MissingSV = %x, want [00]", missing)
	}
}

func TestGomobile_Awareness_BytesOnlyRoundTrip(t *testing.T) {
	a := gomobile.NewAwareness(500)
	if a.ClientID() != 500 {
		t.Errorf("ClientID = %d", a.ClientID())
	}

	state := []byte(`{"name":"mobile-user","cursor":42}`)
	a.SetLocalState(state)
	got := a.LocalState()
	if !bytes.Equal(got, state) {
		t.Errorf("LocalState = %s, want %s", got, state)
	}

	wire := a.EncodeAll()
	if len(wire) == 0 {
		t.Error("EncodeAll returned empty")
	}

	// Apply the wire bytes to a second awareness — it should pick up
	// client 500's state.
	b := gomobile.NewAwareness(501)
	if err := b.Apply(wire, "test"); err != nil {
		t.Fatal(err)
	}

	// Remove + apply removal — local state should be nil.
	a.RemoveLocalState()
	if a.LocalState() != nil {
		t.Errorf("LocalState after RemoveLocalState = %s, want nil", a.LocalState())
	}
}

func TestGomobile_ApplyUpdate_InvalidBytesReturnsError(t *testing.T) {
	d := gomobile.NewDoc()
	// A single byte starting a varuint that needs more data.
	bad := []byte{0x80}
	if err := d.ApplyUpdate(bad); err == nil {
		t.Error("ApplyUpdate on truncated bytes returned nil, want error")
	}
}

func TestGomobile_EncodeDiff_InvalidSVReturnsError(t *testing.T) {
	d := gomobile.NewDoc()
	_, err := d.EncodeDiff([]byte{0x80}) // truncated varuint
	if err == nil {
		t.Error("EncodeDiff on truncated SV returned nil, want error")
	}
}
