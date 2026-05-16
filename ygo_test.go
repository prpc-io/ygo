package ygo_test

import (
	"reflect"
	"testing"

	"github.com/Deln0r/ygo"
)

// TestPublicAPI_Smoke proves an external caller can do every
// primary operation using only the ygo package — no internal/*
// imports. This is the contract a gomobile-bind user (and any
// adopter writing against the public API) would follow.
func TestPublicAPI_Smoke(t *testing.T) {
	d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1000})

	m := ygo.NewMap(d, "settings")
	arr := ygo.NewArray(d, "items")
	tx := ygo.NewText(d, "body")

	w := d.WriteTxn()
	m.Set(w, "color", "blue")
	arr.Push(w, "alpha", "beta")
	_ = tx.InsertWithAttributes(w, 0, "hello", ygo.Attrs{"bold": true})
	w.Commit()

	// Reads — Map.Get, Array.ToSlice, Text.ToDelta.
	if got := m.Get("color"); got != "blue" {
		t.Errorf("Map color = %v, want blue", got)
	}
	if got := arr.Len(); got != 2 {
		t.Errorf("Array Len = %d, want 2", got)
	}
	delta := tx.ToDelta()
	want := []ygo.DeltaOp{{Insert: "hello", Attributes: ygo.Attrs{"bold": true}}}
	if !reflect.DeepEqual(delta, want) {
		t.Errorf("ToDelta = %+v, want %+v", delta, want)
	}
}

func TestPublicAPI_WireRoundTrip(t *testing.T) {
	src := ygo.NewDocWithOptions(ygo.Options{ClientID: 1100})
	m := ygo.NewMap(src, "k")
	w := src.WriteTxn()
	m.Set(w, "a", "1")
	w.Commit()

	bytes := ygo.EncodeStateAsUpdate(src)

	target := ygo.NewDoc()
	if err := ygo.ApplyUpdate(target, bytes); err != nil {
		t.Fatal(err)
	}
	tm := ygo.NewMap(target, "k")
	if got := tm.Get("a"); got != "1" {
		t.Errorf("target a = %v, want 1", got)
	}
}

func TestPublicAPI_StateVectorAndDiff(t *testing.T) {
	src := ygo.NewDocWithOptions(ygo.Options{ClientID: 1200})
	arr := ygo.NewArray(src, "items")
	w := src.WriteTxn()
	arr.Push(w, "first")
	w.Commit()

	sv := ygo.EncodeStateVector(src)
	if len(sv) == 0 {
		t.Error("EncodeStateVector returned empty bytes")
	}

	// Encode a diff against the same SV — should be effectively empty
	// (one varuint(0) for the client list + one varuint(0) for the DS).
	diff, err := ygo.EncodeDiff(src, sv)
	if err != nil {
		t.Fatal(err)
	}
	// Apply to a target — should be a no-op.
	target := ygo.NewDoc()
	if err := ygo.ApplyUpdate(target, diff); err != nil {
		t.Fatal(err)
	}
	tArr := ygo.NewArray(target, "items")
	if tArr.Len() != 0 {
		t.Errorf("target Array Len after empty-diff apply = %d, want 0", tArr.Len())
	}

	// Encode a diff against the empty SV — should carry the array item.
	diffEmpty, err := ygo.EncodeDiff(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	target2 := ygo.NewDoc()
	if err := ygo.ApplyUpdate(target2, diffEmpty); err != nil {
		t.Fatal(err)
	}
	tArr2 := ygo.NewArray(target2, "items")
	if tArr2.Len() != 1 {
		t.Errorf("target2 Array Len = %d, want 1", tArr2.Len())
	}
}

func TestPublicAPI_PendingHelpers(t *testing.T) {
	d := ygo.NewDoc()
	if ygo.HasPending(d) {
		t.Error("fresh Doc reports HasPending")
	}
	if missing := ygo.MissingSV(d); len(missing) == 0 || missing[0] != 0x00 {
		// Encoded empty SV is varuint(0) = single 0x00 byte.
		t.Errorf("fresh Doc MissingSV = %x, want [00]", missing)
	}
}

func TestPublicAPI_MergeUpdates(t *testing.T) {
	a := ygo.NewDocWithOptions(ygo.Options{ClientID: 1300})
	am := ygo.NewMap(a, "m")
	w := a.WriteTxn()
	am.Set(w, "k", "v")
	w.Commit()

	b := ygo.NewDocWithOptions(ygo.Options{ClientID: 1301})
	bm := ygo.NewMap(b, "m")
	w = b.WriteTxn()
	bm.Set(w, "k2", "v2")
	w.Commit()

	merged, err := ygo.MergeUpdates([][]byte{
		ygo.EncodeStateAsUpdate(a),
		ygo.EncodeStateAsUpdate(b),
	})
	if err != nil {
		t.Fatal(err)
	}

	target := ygo.NewDoc()
	if err := ygo.ApplyUpdate(target, merged); err != nil {
		t.Fatal(err)
	}
	tm := ygo.NewMap(target, "m")
	if tm.Get("k") != "v" || tm.Get("k2") != "v2" {
		t.Errorf("merged target missing keys: k=%v k2=%v", tm.Get("k"), tm.Get("k2"))
	}
}

func TestPublicAPI_Awareness(t *testing.T) {
	d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1400})
	a := ygo.NewAwareness(d.ClientID())
	a.SetLocalState([]byte(`{"name":"Alice"}`))

	state, ok := a.LocalState()
	if !ok || string(state) != `{"name":"Alice"}` {
		t.Errorf("LocalState = (%q, %v), want Alice", state, ok)
	}

	// DefaultAwarenessTimeout is a public constant.
	_ = ygo.DefaultAwarenessTimeout
}

func TestPublicAPI_NestedTypesAndXml(t *testing.T) {
	d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1500})

	// Nested Map-in-Map.
	root := ygo.NewMap(d, "root")
	w := d.WriteTxn()
	inner := root.SetMap(w, "inner")
	inner.Set(w, "k", "v")
	w.Commit()

	got := root.Get("inner")
	innerBack, ok := got.(*ygo.Map)
	if !ok {
		t.Fatalf("root.Get(inner) = %T, want *ygo.Map", got)
	}
	if v := innerBack.Get("k"); v != "v" {
		t.Errorf("inner.Get(k) = %v, want v", v)
	}

	// XML.
	frag := ygo.NewXmlFragment(d, "page")
	w = d.WriteTxn()
	p := frag.InsertXmlElement(w, 0, "p")
	p.SetAttribute(w, "class", "lede")
	pText := p.InsertXmlText(w, 0)
	_ = pText.Insert(w, 0, "hello")
	w.Commit()

	if got, want := frag.ToString(), `<p class="lede">hello</p>`; got != want {
		t.Errorf("XmlFragment.ToString = %q, want %q", got, want)
	}
}

// TestPublicAPI_Version exposes Version as a public constant.
func TestPublicAPI_Version(t *testing.T) {
	if ygo.Version == "" {
		t.Error("Version is empty")
	}
}
