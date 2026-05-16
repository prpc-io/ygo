package benchmarks

import (
	"math/rand"
	"testing"

	"github.com/Deln0r/ygo"
)

// B1.1 — Append N=6000 characters sequentially to a Text.
func BenchmarkB1_1_AppendText(b *testing.B) {
	build := func() *ygo.Doc {
		d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		t := ygo.NewText(d, "doc")
		txn := d.WriteTxn()
		for j := 0; j < N; j++ {
			_ = t.Insert(txn, uint64(j), "a")
		}
		txn.Commit()
		return d
	}

	b.Run("ops", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := build()
			if i == 0 {
				reportSize(b, d)
			}
		}
	})
	runStandardSuite(b, build)
}

// B1.2 — Single insert of a 6000-character string.
func BenchmarkB1_2_InsertString(b *testing.B) {
	str := randString(12, N)
	build := func() *ygo.Doc {
		d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		t := ygo.NewText(d, "doc")
		txn := d.WriteTxn()
		_ = t.Insert(txn, 0, str)
		txn.Commit()
		return d
	}

	b.Run("ops", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := build()
			if i == 0 {
				reportSize(b, d)
			}
		}
	})
	runStandardSuite(b, build)
}

// B1.3 — Prepend N=6000 characters one at a time at index 0.
func BenchmarkB1_3_PrependText(b *testing.B) {
	build := func() *ygo.Doc {
		d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		t := ygo.NewText(d, "doc")
		txn := d.WriteTxn()
		for j := 0; j < N; j++ {
			_ = t.Insert(txn, 0, "a")
		}
		txn.Commit()
		return d
	}

	b.Run("ops", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := build()
			if i == 0 {
				reportSize(b, d)
			}
		}
	})
	runStandardSuite(b, build)
}

// B1.4 — Insert N=6000 characters at uniformly random positions.
func BenchmarkB1_4_InsertRandomText(b *testing.B) {
	build := func() *ygo.Doc {
		r := rand.New(rand.NewSource(14))
		d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		t := ygo.NewText(d, "doc")
		txn := d.WriteTxn()
		for j := 0; j < N; j++ {
			idx := uint64(0)
			if j > 0 {
				idx = uint64(r.Intn(j))
			}
			_ = t.Insert(txn, idx, "a")
		}
		txn.Commit()
		return d
	}

	b.Run("ops", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := build()
			if i == 0 {
				reportSize(b, d)
			}
		}
	})
	runStandardSuite(b, build)
}

// B1.5 — Insert N=6000 words at random positions. "Words" are
// 4-10 char ASCII tokens followed by a space.
func BenchmarkB1_5_InsertRandomWords(b *testing.B) {
	build := func() *ygo.Doc {
		r := rand.New(rand.NewSource(15))
		d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		t := ygo.NewText(d, "doc")
		txn := d.WriteTxn()
		for j := 0; j < N; j++ {
			w := randWord(r) + " "
			idx := uint64(0)
			length := t.Length()
			if length > 0 {
				idx = uint64(r.Int63n(int64(length)))
			}
			_ = t.Insert(txn, idx, w)
		}
		txn.Commit()
		return d
	}

	b.Run("ops", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := build()
			if i == 0 {
				reportSize(b, d)
			}
		}
	})
	runStandardSuite(b, build)
}

// B1.6 — Insert 6000 chars one-by-one then delete them all.
func BenchmarkB1_6_InsertThenDelete(b *testing.B) {
	build := func() *ygo.Doc {
		d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		t := ygo.NewText(d, "doc")
		txn := d.WriteTxn()
		for j := 0; j < N; j++ {
			_ = t.Insert(txn, uint64(j), "a")
		}
		_ = t.Delete(txn, 0, uint64(N))
		txn.Commit()
		return d
	}

	b.Run("ops", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := build()
			if i == 0 {
				reportSize(b, d)
			}
		}
	})
	runStandardSuite(b, build)
}

// B1.7 — Mixed insert/delete operations at random positions
// (N=6000 ops total, ~70% insert / 30% delete; ratio chosen so
// length stays positive throughout).
func BenchmarkB1_7_RandomInsertDelete(b *testing.B) {
	build := func() *ygo.Doc {
		r := rand.New(rand.NewSource(17))
		d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		t := ygo.NewText(d, "doc")
		txn := d.WriteTxn()
		for j := 0; j < N; j++ {
			length := t.Length()
			if r.Intn(10) < 3 && length > 0 {
				delLen := uint64(1 + r.Intn(3))
				if delLen > length {
					delLen = length
				}
				idx := uint64(r.Int63n(int64(length - delLen + 1)))
				_ = t.Delete(txn, idx, delLen)
			} else {
				idx := uint64(0)
				if length > 0 {
					idx = uint64(r.Int63n(int64(length)))
				}
				_ = t.Insert(txn, idx, "a")
			}
		}
		txn.Commit()
		return d
	}

	b.Run("ops", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := build()
			if i == 0 {
				reportSize(b, d)
			}
		}
	})
	runStandardSuite(b, build)
}

// B1.8 — Append N=6000 numbers sequentially to an Array.
func BenchmarkB1_8_AppendNumbers(b *testing.B) {
	build := func() *ygo.Doc {
		d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		a := ygo.NewArray(d, "doc")
		txn := d.WriteTxn()
		for j := 0; j < N; j++ {
			a.Push(txn, int64(j))
		}
		txn.Commit()
		return d
	}

	b.Run("ops", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := build()
			if i == 0 {
				reportSize(b, d)
			}
		}
	})
	runStandardSuite(b, build)
}

// B1.9 — Single insert of a 6000-element number array.
func BenchmarkB1_9_InsertArrayOfNumbers(b *testing.B) {
	vals := make([]any, N)
	for i := range vals {
		vals[i] = int64(i)
	}
	build := func() *ygo.Doc {
		d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		a := ygo.NewArray(d, "doc")
		txn := d.WriteTxn()
		a.InsertRange(txn, 0, vals)
		txn.Commit()
		return d
	}

	b.Run("ops", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := build()
			if i == 0 {
				reportSize(b, d)
			}
		}
	})
	runStandardSuite(b, build)
}

// B1.10 — Prepend N=6000 numbers (insert at index 0 each time).
func BenchmarkB1_10_PrependNumbers(b *testing.B) {
	build := func() *ygo.Doc {
		d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		a := ygo.NewArray(d, "doc")
		txn := d.WriteTxn()
		for j := 0; j < N; j++ {
			a.Insert(txn, 0, int64(j))
		}
		txn.Commit()
		return d
	}

	b.Run("ops", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := build()
			if i == 0 {
				reportSize(b, d)
			}
		}
	})
	runStandardSuite(b, build)
}

// B1.11 — Insert N=6000 numbers at uniformly random positions.
func BenchmarkB1_11_InsertRandomNumbers(b *testing.B) {
	build := func() *ygo.Doc {
		r := rand.New(rand.NewSource(111))
		d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		a := ygo.NewArray(d, "doc")
		txn := d.WriteTxn()
		for j := 0; j < N; j++ {
			idx := uint64(0)
			if j > 0 {
				idx = uint64(r.Intn(j))
			}
			a.Insert(txn, idx, int64(j))
		}
		txn.Commit()
		return d
	}

	b.Run("ops", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := build()
			if i == 0 {
				reportSize(b, d)
			}
		}
	})
	runStandardSuite(b, build)
}
