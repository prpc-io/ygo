package types

import (
	"reflect"
	"testing"

	"github.com/Deln0r/ygo/internal/doc"
)

func TestArray_EmptyLen(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))
	if got := a.Len(); got != 0 {
		t.Errorf("empty Len = %d, want 0", got)
	}
	if got := a.Get(0); got != nil {
		t.Errorf("Get(0) on empty = %v, want nil", got)
	}
}

func TestArray_PushSingle(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "hello")
	txn.Commit()

	if got := a.Len(); got != 1 {
		t.Errorf("Len = %d, want 1", got)
	}
	if got := a.Get(0); got != "hello" {
		t.Errorf("Get(0) = %v, want hello", got)
	}
}

func TestArray_InsertAtZero_EmptyArray(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Insert(txn, 0, "first")
	txn.Commit()

	if got := a.Get(0); got != "first" {
		t.Errorf("Get(0) = %v, want first", got)
	}
}

func TestArray_PushMultipleSeparate_GetEach(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "a")
	a.Push(txn, "b")
	a.Push(txn, "c")
	txn.Commit()

	if got := a.Len(); got != 3 {
		t.Errorf("Len = %d, want 3", got)
	}
	for i, want := range []string{"a", "b", "c"} {
		if got := a.Get(uint64(i)); got != want {
			t.Errorf("Get(%d) = %v, want %v", i, got, want)
		}
	}
}

func TestArray_PushBatch_PackedAsSingleItem(t *testing.T) {
	// Per types-array.md finding 3: a single Push(...) of multiple
	// values packs into ONE ContentAny Item with Len == count.
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "a", "b", "c", "d", "e")
	txn.Commit()

	if got := a.Len(); got != 5 {
		t.Errorf("Len = %d, want 5", got)
	}
	for i, want := range []string{"a", "b", "c", "d", "e"} {
		if got := a.Get(uint64(i)); got != want {
			t.Errorf("Get(%d) = %v, want %v", i, got, want)
		}
	}

	// Confirm it's a single item by walking branch.Start.
	count := 0
	for cur := a.branch.Start; cur != nil; cur = cur.Right {
		count++
	}
	if count != 1 {
		t.Errorf("branch.Start linked-list has %d items; expected 1 packed Item with Len=5", count)
	}
}

func TestArray_InsertInMiddle_SplitsPackedRun(t *testing.T) {
	// Push 5 packed, then insert at idx=2. The original Item must
	// split into [0,2) and [2,5); the new "X" Item lands between
	// the two halves, giving total Len 6.
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "a", "b", "c", "d", "e")
	a.Insert(txn, 2, "X")
	txn.Commit()

	want := []string{"a", "b", "X", "c", "d", "e"}
	if got := a.ToSlice(); !reflect.DeepEqual(got, asAnySlice(want)) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestArray_DeleteSingleAtIndex(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "a", "b", "c", "d")
	a.Delete(txn, 1, 1) // remove "b"
	txn.Commit()

	want := []string{"a", "c", "d"}
	if got := a.ToSlice(); !reflect.DeepEqual(got, asAnySlice(want)) {
		t.Errorf("got %v, want %v", got, want)
	}
	if a.Len() != 3 {
		t.Errorf("Len after delete = %d, want 3", a.Len())
	}
}

func TestArray_DeleteRangeAcrossPackedRun(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "a", "b", "c", "d", "e", "f")
	a.Delete(txn, 2, 3) // remove "c", "d", "e"
	txn.Commit()

	want := []string{"a", "b", "f"}
	if got := a.ToSlice(); !reflect.DeepEqual(got, asAnySlice(want)) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestArray_DeleteAtStart(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "a", "b", "c")
	a.Delete(txn, 0, 1)
	txn.Commit()

	want := []string{"b", "c"}
	if got := a.ToSlice(); !reflect.DeepEqual(got, asAnySlice(want)) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestArray_DeleteAtEnd(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "a", "b", "c")
	a.Delete(txn, 2, 1)
	txn.Commit()

	want := []string{"a", "b"}
	if got := a.ToSlice(); !reflect.DeepEqual(got, asAnySlice(want)) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestArray_DeleteEntireArray(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "a", "b", "c")
	a.Delete(txn, 0, a.Len())
	txn.Commit()

	if a.Len() != 0 {
		t.Errorf("Len = %d, want 0", a.Len())
	}
	if a.Get(0) != nil {
		t.Error("Get on empty (post-delete) should return nil")
	}
}

func TestArray_DeleteOutOfRangeClips(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "a", "b")
	a.Delete(txn, 1, 100) // length runs past end; should clip
	txn.Commit()

	if got := a.ToSlice(); !reflect.DeepEqual(got, asAnySlice([]string{"a"})) {
		t.Errorf("got %v, want [a]", got)
	}
}

func TestArray_RangeSkipsDeleted(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "a", "b", "c", "d")
	a.Delete(txn, 1, 2) // remove b, c
	txn.Commit()

	var seen []any
	a.Range(func(_ uint64, v any) bool {
		seen = append(seen, v)
		return true
	})
	if !reflect.DeepEqual(seen, asAnySlice([]string{"a", "d"})) {
		t.Errorf("Range visited %v, want [a d]", seen)
	}
}

func TestArray_RangeEarlyStop(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "a", "b", "c")
	txn.Commit()

	count := 0
	a.Range(func(_ uint64, _ any) bool {
		count++
		return false
	})
	if count != 1 {
		t.Errorf("count after early stop = %d, want 1", count)
	}
}

func TestArray_VariousValueTypes(t *testing.T) {
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "string", int64(42), 3.14, true, nil, false)
	txn.Commit()

	wantSlice := []any{"string", int64(42), 3.14, true, nil, false}
	if got := a.ToSlice(); !reflect.DeepEqual(got, wantSlice) {
		t.Errorf("got %v, want %v", got, wantSlice)
	}
}

func TestArray_InsertBetweenSeparatePushes(t *testing.T) {
	// Push three single values (creates three separate Items), then
	// insert at the boundary between item 1 and item 2.
	d := doc.NewDoc()
	a := NewArray(d.Branch("x"))

	txn := d.WriteTxn()
	a.Push(txn, "a")
	a.Push(txn, "b")
	a.Push(txn, "c")
	a.Insert(txn, 2, "X")
	txn.Commit()

	want := []string{"a", "b", "X", "c"}
	if got := a.ToSlice(); !reflect.DeepEqual(got, asAnySlice(want)) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestArray_BranchAccessor(t *testing.T) {
	d := doc.NewDoc()
	b := d.Branch("x")
	a := NewArray(b)
	if a.Branch() != b {
		t.Error("Branch() should return the wrapped *block.Branch")
	}
}

func asAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, v := range in {
		out[i] = v
	}
	return out
}
