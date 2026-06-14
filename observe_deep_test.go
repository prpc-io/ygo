package ygo_test

import (
	"reflect"
	"testing"

	"github.com/Deln0r/ygo"
)

// eventPath returns the Path of whichever event type e is.
func eventPath(e any) []any {
	switch ev := e.(type) {
	case *ygo.MapEvent:
		return ev.Path
	case *ygo.ArrayEvent:
		return ev.Path
	case *ygo.TextEvent:
		return ev.Path
	}
	return nil
}

// TestObserveDeep_Path checks that a deep observer on a root map fires
// for changes to nested types, with the path from the root to the
// changed type, matching yjs@13.6.31 observeDeep.
func TestObserveDeep_Path(t *testing.T) {
	d := ygo.NewDoc()
	root := ygo.NewMap(d, "root")
	txn := d.WriteTxn()
	child := root.SetMap(txn, "child")
	list := root.SetArray(txn, "list")
	txn.Commit()

	var paths [][]any
	root.ObserveDeep(func(evs []any) {
		for _, e := range evs {
			paths = append(paths, eventPath(e))
		}
	})

	txn = d.WriteTxn()
	child.Set(txn, "k", "v")
	txn.Commit()

	txn = d.WriteTxn()
	list.Push(txn, "x")
	txn.Commit()

	want := [][]any{{"child"}, {"list"}}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

// TestObserveDeep_NestedIndex checks an array-index path segment: a map
// nested inside an array element resolves to a numeric path segment.
func TestObserveDeep_NestedIndex(t *testing.T) {
	d := ygo.NewDoc()
	root := ygo.NewArray(d, "root")
	txn := d.WriteTxn()
	root.Push(txn, "a", "b") // indices 0,1
	inner := root.InsertMap(txn, 2)
	txn.Commit()

	var paths [][]any
	root.ObserveDeep(func(evs []any) {
		for _, e := range evs {
			paths = append(paths, eventPath(e))
		}
	})

	txn = d.WriteTxn()
	inner.Set(txn, "x", "y")
	txn.Commit()

	want := [][]any{{2}}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

// TestObserveDeep_FiresOnSelf confirms a deep observer also fires for a
// change to the observed type itself, with an empty path.
func TestObserveDeep_FiresOnSelf(t *testing.T) {
	d := ygo.NewDoc()
	m := ygo.NewMap(d, "m")
	fired := 0
	var gotPath []any
	m.ObserveDeep(func(evs []any) {
		fired++
		gotPath = eventPath(evs[0])
	})
	txn := d.WriteTxn()
	m.Set(txn, "k", "v")
	txn.Commit()
	if fired != 1 {
		t.Fatalf("fired %d, want 1", fired)
	}
	if len(gotPath) != 0 {
		t.Errorf("path = %v, want empty for self change", gotPath)
	}
}
