package types_test

import (
	"testing"

	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/encoding"
	"github.com/Deln0r/ygo/internal/types"
)

// TestNested_MapInMap_LocalRoundTrip is the simplest case: a Map
// holds a child Map at one key; writes to the child are visible
// when re-fetched through the parent.
func TestNested_MapInMap_LocalRoundTrip(t *testing.T) {
	d := doc.NewDocWithOptions(doc.Options{ClientID: 100})
	outer := types.NewMap(d.Branch("settings"))

	txn := d.WriteTxn()
	inner := outer.SetMap(txn, "theme")
	inner.Set(txn, "color", "blue")
	inner.Set(txn, "size", "large")
	txn.Commit()

	// Re-fetch through the parent and confirm we get the same data.
	got := outer.Get("theme")
	innerFromParent, ok := got.(*types.Map)
	if !ok {
		t.Fatalf("outer.Get(theme) returned %T, want *types.Map", got)
	}
	if v := innerFromParent.Get("color"); v != "blue" {
		t.Errorf("inner.Get(color) = %v, want blue", v)
	}
	if v := innerFromParent.Get("size"); v != "large" {
		t.Errorf("inner.Get(size) = %v, want large", v)
	}
}

// TestNested_ArrayInMap_LocalRoundTrip
func TestNested_ArrayInMap_LocalRoundTrip(t *testing.T) {
	d := doc.NewDocWithOptions(doc.Options{ClientID: 101})
	outer := types.NewMap(d.Branch("settings"))

	txn := d.WriteTxn()
	innerArr := outer.SetArray(txn, "tags")
	innerArr.Push(txn, "alpha", "beta", "gamma")
	txn.Commit()

	got := outer.Get("tags")
	arr, ok := got.(*types.Array)
	if !ok {
		t.Fatalf("outer.Get(tags) returned %T, want *types.Array", got)
	}
	if arr.Len() != 3 {
		t.Errorf("arr.Len = %d, want 3", arr.Len())
	}
	if v := arr.Get(1); v != "beta" {
		t.Errorf("arr.Get(1) = %v, want beta", v)
	}
}

// TestNested_TextInMap_LocalRoundTrip
func TestNested_TextInMap_LocalRoundTrip(t *testing.T) {
	d := doc.NewDocWithOptions(doc.Options{ClientID: 102})
	outer := types.NewMap(d.Branch("doc"))

	txn := d.WriteTxn()
	innerText := outer.SetText(txn, "body")
	innerText.Insert(txn, 0, "hello world")
	txn.Commit()

	got := outer.Get("body")
	tx, ok := got.(*types.Text)
	if !ok {
		t.Fatalf("outer.Get(body) returned %T, want *types.Text", got)
	}
	if s := tx.String(); s != "hello world" {
		t.Errorf("text.String = %q, want hello world", s)
	}
}

// TestNested_MapInArray verifies the symmetric direction.
func TestNested_MapInArray(t *testing.T) {
	d := doc.NewDocWithOptions(doc.Options{ClientID: 103})
	outerArr := types.NewArray(d.Branch("rows"))

	txn := d.WriteTxn()
	row0 := outerArr.InsertMap(txn, 0)
	row0.Set(txn, "name", "Alice")
	row0.Set(txn, "age", "30")

	row1 := outerArr.InsertMap(txn, 1)
	row1.Set(txn, "name", "Bob")
	txn.Commit()

	if outerArr.Len() != 2 {
		t.Fatalf("Len = %d, want 2", outerArr.Len())
	}
	got0 := outerArr.Get(0)
	m0, ok := got0.(*types.Map)
	if !ok {
		t.Fatalf("Get(0) = %T, want *types.Map", got0)
	}
	if v := m0.Get("name"); v != "Alice" {
		t.Errorf("row0.name = %v, want Alice", v)
	}
	got1 := outerArr.Get(1)
	m1, ok := got1.(*types.Map)
	if !ok {
		t.Fatalf("Get(1) = %T, want *types.Map", got1)
	}
	if v := m1.Get("name"); v != "Bob" {
		t.Errorf("row1.name = %v, want Bob", v)
	}
}

// TestNested_DeeperHierarchy: Map { "users": Array of Maps with
// nested Array of strings each }. Exercises >2 levels of nesting.
func TestNested_DeeperHierarchy(t *testing.T) {
	d := doc.NewDocWithOptions(doc.Options{ClientID: 104})
	root := types.NewMap(d.Branch("root"))

	txn := d.WriteTxn()
	users := root.SetArray(txn, "users")
	alice := users.InsertMap(txn, 0)
	alice.Set(txn, "name", "Alice")
	tags := alice.SetArray(txn, "tags")
	tags.Push(txn, "admin", "founder")
	txn.Commit()

	// Read back through the chain.
	usersVal, ok := root.Get("users").(*types.Array)
	if !ok {
		t.Fatal("root.Get(users) not Array")
	}
	aliceVal, ok := usersVal.Get(0).(*types.Map)
	if !ok {
		t.Fatal("users.Get(0) not Map")
	}
	tagsVal, ok := aliceVal.Get("tags").(*types.Array)
	if !ok {
		t.Fatal("alice.Get(tags) not Array")
	}
	if tagsVal.Len() != 2 || tagsVal.Get(0) != "admin" || tagsVal.Get(1) != "founder" {
		t.Errorf("tags = %v, want [admin founder]", tagsVal.ToSlice())
	}
}

// TestNested_WireRoundTrip encodes a doc with nested types and
// replays it into a fresh target; final state must match.
func TestNested_WireRoundTrip(t *testing.T) {
	src := doc.NewDocWithOptions(doc.Options{ClientID: 200})
	root := types.NewMap(src.Branch("settings"))
	txn := src.WriteTxn()
	theme := root.SetMap(txn, "theme")
	theme.Set(txn, "color", "red")
	theme.Set(txn, "size", "small")
	txn.Commit()

	update := encoding.EncodeStateAsUpdate(src)

	target := doc.NewDoc()
	if err := encoding.ApplyUpdate(target, update); err != nil {
		t.Fatal(err)
	}

	targetRoot := types.NewMap(target.Branch("settings"))
	got := targetRoot.Get("theme")
	gotMap, ok := got.(*types.Map)
	if !ok {
		t.Fatalf("target root.Get(theme) = %T, want *types.Map", got)
	}
	if v := gotMap.Get("color"); v != "red" {
		t.Errorf("color = %v, want red", v)
	}
	if v := gotMap.Get("size"); v != "small" {
		t.Errorf("size = %v, want small", v)
	}
}

// TestNested_CrossClient_Convergence: two docs sharing updates
// converge to the same nested-type state.
func TestNested_CrossClient_Convergence(t *testing.T) {
	a := doc.NewDocWithOptions(doc.Options{ClientID: 300})
	aRoot := types.NewMap(a.Branch("data"))
	t1 := a.WriteTxn()
	aInner := aRoot.SetMap(t1, "users")
	aInner.Set(t1, "alice", "1")
	t1.Commit()

	// B observes A's full state.
	b := doc.NewDocWithOptions(doc.Options{ClientID: 301})
	if err := encoding.ApplyUpdate(b, encoding.EncodeStateAsUpdate(a)); err != nil {
		t.Fatal(err)
	}

	// B writes into the same nested map.
	bRoot := types.NewMap(b.Branch("data"))
	bInner, ok := bRoot.Get("users").(*types.Map)
	if !ok {
		t.Fatal("B's view of nested map is not *types.Map")
	}
	t2 := b.WriteTxn()
	bInner.Set(t2, "bob", "2")
	t2.Commit()

	// A receives B's update.
	if err := encoding.ApplyUpdate(a, encoding.EncodeStateAsUpdate(b)); err != nil {
		t.Fatal(err)
	}

	// Both A and B should see both keys.
	for label, d := range map[string]*doc.Doc{"A": a, "B": b} {
		root := types.NewMap(d.Branch("data"))
		inner, ok := root.Get("users").(*types.Map)
		if !ok {
			t.Errorf("%s: users not Map", label)
			continue
		}
		if inner.Get("alice") != "1" {
			t.Errorf("%s: alice = %v, want 1", label, inner.Get("alice"))
		}
		if inner.Get("bob") != "2" {
			t.Errorf("%s: bob = %v, want 2", label, inner.Get("bob"))
		}
	}
}

// TestNested_OutOfOrderApply_DrainsViaPending exercises the
// ParentID pending path: client A creates a nested Map; client B
// (which has observed A) writes into that nested Map. B's Items
// carry Parent = ParentID(A's parent-Item ID). If the target
// receives B's update before A's, B's items must queue in pending
// and drain once A arrives.
//
// Two separate clients are required because EncodeDiff currently
// emits whole client lists (see tech-debt) — we cannot split a
// single client's history into causally-separated chunks.
func TestNested_OutOfOrderApply_DrainsViaPending(t *testing.T) {
	a := doc.NewDocWithOptions(doc.Options{ClientID: 401})
	aRoot := types.NewMap(a.Branch("data"))
	t1 := a.WriteTxn()
	aRoot.SetMap(t1, "child")
	t1.Commit()

	// B observes A's state.
	b := doc.NewDocWithOptions(doc.Options{ClientID: 402})
	if err := encoding.ApplyUpdate(b, encoding.EncodeStateAsUpdate(a)); err != nil {
		t.Fatal(err)
	}
	bRoot := types.NewMap(b.Branch("data"))
	bInner, ok := bRoot.Get("child").(*types.Map)
	if !ok {
		t.Fatal("B's view of child not Map")
	}
	t2 := b.WriteTxn()
	bInner.Set(t2, "k", "v")
	t2.Commit()

	// Filter A out of B's encoded diff — emit only B's blocks.
	rtxn := b.ReadTxn()
	bSV := rtxn.Store().GetStateVector()
	rtxn.Close()
	remoteAlreadyHasA := map[uint64]uint64{401: bSV[401]}
	rtxn = b.ReadTxn()
	bOnly := encoding.EncodeDiff(b, rtxn, remoteAlreadyHasA)
	rtxn.Close()

	target := doc.NewDoc()
	if err := encoding.ApplyUpdate(target, bOnly); err != nil {
		t.Fatal(err)
	}

	// B's item references A's parent Item via ParentID. A's items
	// are not yet present, so B's item queues.
	rtxn = target.ReadTxn()
	if !encoding.HasPending(rtxn) {
		rtxn.Close()
		t.Fatal("expected pending after B-only apply")
	}
	rtxn.Close()

	// Apply A's state — pending drains.
	if err := encoding.ApplyUpdate(target, encoding.EncodeStateAsUpdate(a)); err != nil {
		t.Fatal(err)
	}

	targetRoot := types.NewMap(target.Branch("data"))
	innerVal, ok := targetRoot.Get("child").(*types.Map)
	if !ok {
		t.Fatal("target child not Map after drain")
	}
	if v := innerVal.Get("k"); v != "v" {
		t.Errorf("k = %v, want v", v)
	}

	rtxn = target.ReadTxn()
	defer rtxn.Close()
	if encoding.HasPending(rtxn) {
		t.Errorf("pending still has %d blocks after drain",
			encoding.GetPending(rtxn).BlockCount())
	}
}

// TestNested_BranchTypeRef_DefaultsAreSafe documents that a fresh
// Branch{} reads as TypeRefArray (zero value matches wire byte for
// Array), and that constructors must set TypeRef explicitly for
// non-Array branches.
func TestNested_BranchTypeRef_DefaultsAreSafe(t *testing.T) {
	zero := &block.Branch{}
	if zero.TypeRef != block.TypeRefArray {
		t.Errorf("zero Branch.TypeRef = %d, want TypeRefArray (0)", zero.TypeRef)
	}

	d := doc.NewDoc()
	root := types.NewMap(d.Branch("root"))
	txn := d.WriteTxn()
	inner := root.SetMap(txn, "m")
	txn.Commit()

	if inner.Branch().TypeRef != block.TypeRefMap {
		t.Errorf("SetMap inner TypeRef = %d, want TypeRefMap", inner.Branch().TypeRef)
	}
}
