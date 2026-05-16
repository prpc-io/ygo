package types_test

import (
	"reflect"
	"testing"

	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/encoding"
	"github.com/Deln0r/ygo/internal/types"
)

func TestApplyDelta_PlainInsert(t *testing.T) {
	d := doc.NewDocWithOptions(doc.Options{ClientID: 1100})
	tx := types.NewText(d.Branch("body"))

	w := d.WriteTxn()
	if err := tx.ApplyDelta(w, []types.DeltaOp{
		{Insert: "Hello world"},
	}); err != nil {
		t.Fatal(err)
	}
	w.Commit()

	if got, want := tx.String(), "Hello world"; got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

func TestApplyDelta_InsertWithAttributes(t *testing.T) {
	d := doc.NewDocWithOptions(doc.Options{ClientID: 1101})
	tx := types.NewText(d.Branch("body"))

	w := d.WriteTxn()
	if err := tx.ApplyDelta(w, []types.DeltaOp{
		{Insert: "Hello ", Attributes: nil},
		{Insert: "world", Attributes: types.Attrs{"bold": true}},
		{Insert: "!", Attributes: nil},
	}); err != nil {
		t.Fatal(err)
	}
	w.Commit()

	delta := tx.ToDelta()
	want := []types.DeltaOp{
		{Insert: "Hello ", Attributes: nil},
		{Insert: "world", Attributes: types.Attrs{"bold": true}},
		{Insert: "!", Attributes: nil},
	}
	if !reflect.DeepEqual(delta, want) {
		t.Errorf("ToDelta = %+v, want %+v", delta, want)
	}
}

func TestApplyDelta_RetainWithFormat(t *testing.T) {
	d := doc.NewDocWithOptions(doc.Options{ClientID: 1102})
	tx := types.NewText(d.Branch("body"))

	w := d.WriteTxn()
	_ = tx.Insert(w, 0, "Hello world")
	w.Commit()

	// Apply: retain(7) skip "Hello, " → retain(5) format italic on "world"
	w = d.WriteTxn()
	if err := tx.ApplyDelta(w, []types.DeltaOp{
		{Retain: 6},
		{Retain: 5, Attributes: types.Attrs{"italic": true}},
	}); err != nil {
		t.Fatal(err)
	}
	w.Commit()

	delta := tx.ToDelta()
	want := []types.DeltaOp{
		{Insert: "Hello ", Attributes: nil},
		{Insert: "world", Attributes: types.Attrs{"italic": true}},
	}
	if !reflect.DeepEqual(delta, want) {
		t.Errorf("ToDelta = %+v, want %+v", delta, want)
	}
}

func TestApplyDelta_Delete(t *testing.T) {
	d := doc.NewDocWithOptions(doc.Options{ClientID: 1103})
	tx := types.NewText(d.Branch("body"))

	w := d.WriteTxn()
	_ = tx.Insert(w, 0, "Hello world")
	w.Commit()

	// Retain 5 ("Hello"), delete 6 (" world").
	w = d.WriteTxn()
	if err := tx.ApplyDelta(w, []types.DeltaOp{
		{Retain: 5},
		{Delete: 6},
	}); err != nil {
		t.Fatal(err)
	}
	w.Commit()

	if got, want := tx.String(), "Hello"; got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

func TestApplyDelta_MixedOps(t *testing.T) {
	// Compound transformation: take "alpha beta", apply bold to
	// "alpha", insert " (note)" before "beta", delete "beta".
	d := doc.NewDocWithOptions(doc.Options{ClientID: 1104})
	tx := types.NewText(d.Branch("body"))

	w := d.WriteTxn()
	_ = tx.Insert(w, 0, "alpha beta")
	w.Commit()

	w = d.WriteTxn()
	if err := tx.ApplyDelta(w, []types.DeltaOp{
		{Retain: 5, Attributes: types.Attrs{"bold": true}}, // alpha → bold
		{Retain: 1},        // skip space
		{Insert: "(note)"}, // before "beta"
		{Delete: 4},        // remove "beta"
	}); err != nil {
		t.Fatal(err)
	}
	w.Commit()

	if got, want := tx.String(), "alpha (note)"; got != want {
		t.Errorf("String = %q, want %q", got, want)
	}

	// Verify formatting on "alpha".
	delta := tx.ToDelta()
	if len(delta) < 2 || delta[0].Insert != "alpha" || delta[0].Attributes["bold"] != true {
		t.Errorf("delta[0] = %+v, want Insert=alpha bold=true", delta[0])
	}
}

func TestApplyDelta_Embed(t *testing.T) {
	d := doc.NewDocWithOptions(doc.Options{ClientID: 1105})
	tx := types.NewText(d.Branch("body"))

	w := d.WriteTxn()
	if err := tx.ApplyDelta(w, []types.DeltaOp{
		{Insert: "before"},
		{Embed: "embed-val"},
		{Insert: "after"},
	}); err != nil {
		t.Fatal(err)
	}
	w.Commit()

	delta := tx.ToDelta()
	if len(delta) != 3 {
		t.Fatalf("delta len = %d, want 3; got %+v", len(delta), delta)
	}
	if delta[0].Insert != "before" {
		t.Errorf("delta[0] = %+v", delta[0])
	}
	if delta[1].Embed != "embed-val" {
		t.Errorf("delta[1] = %+v, want Embed=embed-val", delta[1])
	}
	if delta[2].Insert != "after" {
		t.Errorf("delta[2] = %+v", delta[2])
	}
}

func TestApplyDelta_ToDeltaRoundTrip(t *testing.T) {
	// Building a doc via ToDelta then ApplyDelta on a fresh doc
	// must reproduce the same delta.
	src := doc.NewDocWithOptions(doc.Options{ClientID: 1106})
	stx := types.NewText(src.Branch("body"))
	w := src.WriteTxn()
	_ = stx.Insert(w, 0, "Hello ")
	_ = stx.InsertWithAttributes(w, 6, "world", types.Attrs{"bold": true})
	_ = stx.Insert(w, 11, "!")
	w.Commit()

	srcDelta := stx.ToDelta()

	target := doc.NewDocWithOptions(doc.Options{ClientID: 1107})
	tt := types.NewText(target.Branch("body"))
	w = target.WriteTxn()
	if err := tt.ApplyDelta(w, srcDelta); err != nil {
		t.Fatal(err)
	}
	w.Commit()

	gotDelta := tt.ToDelta()
	if !reflect.DeepEqual(gotDelta, srcDelta) {
		t.Errorf("round-trip delta:\n got  %+v\n want %+v", gotDelta, srcDelta)
	}
	if got, want := tt.String(), "Hello world!"; got != want {
		t.Errorf("target String = %q, want %q", got, want)
	}
}

func TestApplyDelta_EmptyOps_NoOp(t *testing.T) {
	d := doc.NewDocWithOptions(doc.Options{ClientID: 1108})
	tx := types.NewText(d.Branch("body"))

	w := d.WriteTxn()
	_ = tx.Insert(w, 0, "stable")
	w.Commit()

	w = d.WriteTxn()
	if err := tx.ApplyDelta(w, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.ApplyDelta(w, []types.DeltaOp{}); err != nil {
		t.Fatal(err)
	}
	w.Commit()

	if got, want := tx.String(), "stable"; got != want {
		t.Errorf("String = %q, want %q", got, want)
	}
}

func TestApplyDelta_CrossClient_Converges(t *testing.T) {
	// A creates baseline, B applies an insert+format delta, A
	// receives B's update — both converge. Avoid partial-item
	// deletes because the DeleteSet apply path does not split
	// items at range boundaries yet (see tech-debt.md "DeleteSet
	// apply does not split items at boundaries").
	a := doc.NewDocWithOptions(doc.Options{ClientID: 1109})
	at := types.NewText(a.Branch("body"))
	w := a.WriteTxn()
	_ = at.Insert(w, 0, "Hello world")
	w.Commit()

	b := doc.NewDocWithOptions(doc.Options{ClientID: 1110})
	if err := encoding.ApplyUpdate(b, encoding.EncodeStateAsUpdate(a)); err != nil {
		t.Fatal(err)
	}
	bt := types.NewText(b.Branch("body"))
	w = b.WriteTxn()
	if err := bt.ApplyDelta(w, []types.DeltaOp{
		{Retain: 6, Attributes: types.Attrs{"bold": true}},
		{Retain: 5, Attributes: types.Attrs{"italic": true}},
		{Insert: "!", Attributes: types.Attrs{"bold": true}},
	}); err != nil {
		t.Fatal(err)
	}
	w.Commit()

	if err := encoding.ApplyUpdate(a, encoding.EncodeStateAsUpdate(b)); err != nil {
		t.Fatal(err)
	}

	wantString := "Hello world!"
	if got := at.String(); got != wantString {
		t.Errorf("A String = %q, want %q", got, wantString)
	}
	if got := bt.String(); got != wantString {
		t.Errorf("B String = %q, want %q", got, wantString)
	}

	// Both should agree on formatting.
	if !reflect.DeepEqual(at.ToDelta(), bt.ToDelta()) {
		t.Errorf("deltas diverged:\n A=%+v\n B=%+v", at.ToDelta(), bt.ToDelta())
	}
}

func TestApplyDelta_RetainNoAttrs_PreservesFormatting(t *testing.T) {
	// Retain without Attributes must NOT touch existing formatting.
	d := doc.NewDocWithOptions(doc.Options{ClientID: 1111})
	tx := types.NewText(d.Branch("body"))

	w := d.WriteTxn()
	_ = tx.InsertWithAttributes(w, 0, "bold", types.Attrs{"bold": true})
	_ = tx.Insert(w, 4, " plain")
	w.Commit()

	w = d.WriteTxn()
	// Just retain all 10 chars without attributes — formatting unchanged.
	if err := tx.ApplyDelta(w, []types.DeltaOp{
		{Retain: 10},
	}); err != nil {
		t.Fatal(err)
	}
	w.Commit()

	delta := tx.ToDelta()
	want := []types.DeltaOp{
		{Insert: "bold", Attributes: types.Attrs{"bold": true}},
		{Insert: " plain", Attributes: nil},
	}
	if !reflect.DeepEqual(delta, want) {
		t.Errorf("delta after no-attr retain = %+v, want %+v", delta, want)
	}
}
