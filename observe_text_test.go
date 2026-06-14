package ygo_test

import (
	"testing"

	"github.com/Deln0r/ygo"
)

func collectTextDeltas(t *ygo.Text) *[][]ygo.DeltaOp {
	out := &[][]ygo.DeltaOp{}
	t.Observe(func(e *ygo.TextEvent) { *out = append(*out, e.Delta) })
	return out
}

func txtInsert(d *ygo.Doc, t *ygo.Text, idx uint64, s string) {
	txn := d.WriteTxn()
	if err := t.Insert(txn, idx, s); err != nil {
		panic(err)
	}
	txn.Commit()
}

// TestTextObserve_Delta checks Text.Observe deltas against the yjs
// YTextEvent semantics captured from yjs@13.6.31, including a format
// range (retain with attributes).
func TestTextObserve_Delta(t *testing.T) {
	t.Run("insert", func(t *testing.T) {
		d := ygo.NewDoc()
		txt := ygo.NewText(d, "t")
		ev := collectTextDeltas(txt)
		txtInsert(d, txt, 0, "hello")
		assertTextDeltas(t, *ev, [][]ygo.DeltaOp{
			{{Insert: "hello"}},
		})
	})

	t.Run("retain then insert", func(t *testing.T) {
		d := ygo.NewDoc()
		txt := ygo.NewText(d, "t")
		txtInsert(d, txt, 0, "abc")
		ev := collectTextDeltas(txt)
		txtInsert(d, txt, 1, "X")
		assertTextDeltas(t, *ev, [][]ygo.DeltaOp{
			{{Retain: 1}, {Insert: "X"}},
		})
	})

	t.Run("retain then delete", func(t *testing.T) {
		d := ygo.NewDoc()
		txt := ygo.NewText(d, "t")
		txtInsert(d, txt, 0, "abcd")
		ev := collectTextDeltas(txt)
		txn := d.WriteTxn()
		_ = txt.Delete(txn, 1, 2)
		txn.Commit()
		assertTextDeltas(t, *ev, [][]ygo.DeltaOp{
			{{Retain: 1}, {Delete: 2}},
		})
	})

	t.Run("delete head", func(t *testing.T) {
		d := ygo.NewDoc()
		txt := ygo.NewText(d, "t")
		txtInsert(d, txt, 0, "abc")
		ev := collectTextDeltas(txt)
		txn := d.WriteTxn()
		_ = txt.Delete(txn, 0, 1)
		txn.Commit()
		assertTextDeltas(t, *ev, [][]ygo.DeltaOp{
			{{Delete: 1}},
		})
	})

	t.Run("format bold range", func(t *testing.T) {
		d := ygo.NewDoc()
		txt := ygo.NewText(d, "t")
		txtInsert(d, txt, 0, "abcd")
		ev := collectTextDeltas(txt)
		txn := d.WriteTxn()
		_ = txt.Format(txn, 1, 2, ygo.Attrs{"bold": true})
		txn.Commit()
		assertTextDeltas(t, *ev, [][]ygo.DeltaOp{
			{{Retain: 1}, {Retain: 2, Attributes: ygo.Attrs{"bold": true}}},
		})
	})

	t.Run("insert with active bold carries attributes", func(t *testing.T) {
		d := ygo.NewDoc()
		txt := ygo.NewText(d, "t")
		ev := collectTextDeltas(txt)
		txn := d.WriteTxn()
		_ = txt.InsertWithAttributes(txn, 0, "hi", ygo.Attrs{"bold": true})
		txn.Commit()
		assertTextDeltas(t, *ev, [][]ygo.DeltaOp{
			{{Insert: "hi", Attributes: ygo.Attrs{"bold": true}}},
		})
	})
}

func assertTextDeltas(t *testing.T, got, want [][]ygo.DeltaOp) {
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
			if g.Insert != w.Insert || g.Retain != w.Retain || g.Delete != w.Delete {
				t.Errorf("event %d op %d: got %+v, want %+v", i, j, g, w)
			}
			if len(g.Attributes) != len(w.Attributes) {
				t.Errorf("event %d op %d attrs: got %v, want %v", i, j, g.Attributes, w.Attributes)
				continue
			}
			for k, wv := range w.Attributes {
				if g.Attributes[k] != wv {
					t.Errorf("event %d op %d attr %q: got %#v, want %#v", i, j, k, g.Attributes[k], wv)
				}
			}
		}
	}
}
