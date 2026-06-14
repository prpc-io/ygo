package ygo_test

import (
	"testing"

	"github.com/Deln0r/ygo"
)

func collectArrayDeltas(a *ygo.Array) *[][]ygo.ArrayDeltaOp {
	out := &[][]ygo.ArrayDeltaOp{}
	a.Observe(func(e *ygo.ArrayEvent) { *out = append(*out, e.Delta) })
	return out
}

// TestArrayObserve_Delta checks Array.Observe deltas against the yjs
// YArrayEvent semantics captured from yjs@13.6.31: insert / delete /
// retain ops and the dropped trailing retain.
func TestArrayObserve_Delta(t *testing.T) {
	t.Run("insert head", func(t *testing.T) {
		d := ygo.NewDoc()
		a := ygo.NewArray(d, "a")
		ev := collectArrayDeltas(a)
		txn := d.WriteTxn()
		a.Push(txn, "x", "y", "z")
		txn.Commit()
		assertDeltas(t, *ev, [][]ygo.ArrayDeltaOp{
			{{Insert: []any{"x", "y", "z"}}},
		})
	})

	t.Run("retain then insert middle", func(t *testing.T) {
		d := ygo.NewDoc()
		a := ygo.NewArray(d, "a")
		txn := d.WriteTxn()
		a.Push(txn, "a", "b", "c")
		txn.Commit()
		ev := collectArrayDeltas(a)
		txn = d.WriteTxn()
		a.Insert(txn, 1, "X")
		txn.Commit()
		assertDeltas(t, *ev, [][]ygo.ArrayDeltaOp{
			{{Retain: 1}, {Insert: []any{"X"}}},
		})
	})

	t.Run("retain then delete (trailing retain dropped)", func(t *testing.T) {
		d := ygo.NewDoc()
		a := ygo.NewArray(d, "a")
		txn := d.WriteTxn()
		a.Push(txn, "a", "b", "c", "d")
		txn.Commit()
		ev := collectArrayDeltas(a)
		txn = d.WriteTxn()
		a.Delete(txn, 1, 2) // remove b,c -> [a,d]; trailing retain on d dropped
		txn.Commit()
		assertDeltas(t, *ev, [][]ygo.ArrayDeltaOp{
			{{Retain: 1}, {Delete: 2}},
		})
	})

	t.Run("insert and delete one txn", func(t *testing.T) {
		d := ygo.NewDoc()
		a := ygo.NewArray(d, "a")
		txn := d.WriteTxn()
		a.Push(txn, "a", "b", "c")
		txn.Commit()
		ev := collectArrayDeltas(a)
		txn = d.WriteTxn()
		a.Insert(txn, 1, "X") // [a,X,b,c]
		a.Delete(txn, 2, 1)   // remove b -> [a,X,c]; c's trailing retain dropped
		txn.Commit()
		assertDeltas(t, *ev, [][]ygo.ArrayDeltaOp{
			{{Retain: 1}, {Insert: []any{"X"}}, {Delete: 1}},
		})
	})

	t.Run("delete head no trailing retain", func(t *testing.T) {
		d := ygo.NewDoc()
		a := ygo.NewArray(d, "a")
		txn := d.WriteTxn()
		a.Push(txn, "a", "b", "c")
		txn.Commit()
		ev := collectArrayDeltas(a)
		txn = d.WriteTxn()
		a.Delete(txn, 0, 1) // remove a -> [b,c]; trailing retain dropped
		txn.Commit()
		assertDeltas(t, *ev, [][]ygo.ArrayDeltaOp{
			{{Delete: 1}},
		})
	})
}

// TestArrayObserve_RemoteUpdate confirms array observers fire on a
// remote update.
func TestArrayObserve_RemoteUpdate(t *testing.T) {
	src := ygo.NewDoc()
	sa := ygo.NewArray(src, "a")
	txn := src.WriteTxn()
	sa.Push(txn, 1, 2)
	txn.Commit()

	dst := ygo.NewDoc()
	da := ygo.NewArray(dst, "a")
	ev := collectArrayDeltas(da)
	if err := ygo.ApplyUpdate(dst, ygo.EncodeStateAsUpdate(src)); err != nil {
		t.Fatal(err)
	}
	assertDeltas(t, *ev, [][]ygo.ArrayDeltaOp{
		{{Insert: []any{int64(1), int64(2)}}},
	})
}

func assertDeltas(t *testing.T, got, want [][]ygo.ArrayDeltaOp) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Errorf("event %d: got %d ops, want %d: %+v vs %+v", i, len(got[i]), len(want[i]), got[i], want[i])
			continue
		}
		for j := range want[i] {
			g, w := got[i][j], want[i][j]
			if g.Delete != w.Delete || g.Retain != w.Retain || len(g.Insert) != len(w.Insert) {
				t.Errorf("event %d op %d: got %+v, want %+v", i, j, g, w)
				continue
			}
			for k := range w.Insert {
				if g.Insert[k] != w.Insert[k] {
					t.Errorf("event %d op %d insert[%d]: got %#v, want %#v", i, j, k, g.Insert[k], w.Insert[k])
				}
			}
		}
	}
}
