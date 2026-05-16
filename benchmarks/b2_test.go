package benchmarks

import (
	"math/rand"
	"testing"

	"github.com/Deln0r/ygo"
)

// buildTwoPeerMerged returns a Doc on which both peer-A and peer-B
// workloads have been applied (exchange-and-merge). Each peer runs
// its mutate closure independently against its own Text rooted at
// "doc"; updates are then exchanged via EncodeStateAsUpdate /
// ApplyUpdate so the returned Doc has the converged state.
//
// The Text wrappers are constructed BEFORE opening the write
// transaction — Doc.Branch acquires the doc's write lock and
// sync.RWMutex is non-reentrant, so building the Branch inside an
// open txn deadlocks.
//
// Used by B2.1 — B2.4 to build the post-merge doc for the standard
// encode/parse sub-benchmark suite.
func buildTwoPeerMerged(mutateA, mutateB func(*ygo.Text, *ygo.TransactionMut)) *ygo.Doc {
	a := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
	b := ygo.NewDocWithOptions(ygo.Options{ClientID: 2})
	tA := ygo.NewText(a, "doc")
	tB := ygo.NewText(b, "doc")

	txnA := a.WriteTxn()
	mutateA(tA, txnA)
	txnA.Commit()

	txnB := b.WriteTxn()
	mutateB(tB, txnB)
	txnB.Commit()

	if err := ygo.ApplyUpdate(a, ygo.EncodeStateAsUpdate(b)); err != nil {
		panic(err)
	}
	if err := ygo.ApplyUpdate(b, ygo.EncodeStateAsUpdate(a)); err != nil {
		panic(err)
	}
	return a
}

// B2.1 — Two clients each insert N2=3000 characters at index 0
// concurrently. The post-merge state has both runs interleaved
// or stacked deterministically per YATA.
func BenchmarkB2_1_ConcurrentInsertAt0(b *testing.B) {
	mutateA := func(t *ygo.Text, txn *ygo.TransactionMut) {
		for j := 0; j < N2; j++ {
			_ = t.Insert(txn, 0, "a")
		}
	}
	mutateB := func(t *ygo.Text, txn *ygo.TransactionMut) {
		for j := 0; j < N2; j++ {
			_ = t.Insert(txn, 0, "b")
		}
	}
	build := func() *ygo.Doc { return buildTwoPeerMerged(mutateA, mutateB) }

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

// B2.2 — Two clients each insert N2=3000 characters at random
// positions concurrently.
func BenchmarkB2_2_ConcurrentInsertRandom(b *testing.B) {
	mutateA := func(t *ygo.Text, txn *ygo.TransactionMut) {
		r := rand.New(rand.NewSource(221))
		for j := 0; j < N2; j++ {
			idx := uint64(0)
			if j > 0 {
				idx = uint64(r.Intn(j))
			}
			_ = t.Insert(txn, idx, "a")
		}
	}
	mutateB := func(t *ygo.Text, txn *ygo.TransactionMut) {
		r := rand.New(rand.NewSource(222))
		for j := 0; j < N2; j++ {
			idx := uint64(0)
			if j > 0 {
				idx = uint64(r.Intn(j))
			}
			_ = t.Insert(txn, idx, "b")
		}
	}
	build := func() *ygo.Doc { return buildTwoPeerMerged(mutateA, mutateB) }

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

// B2.3 — Two clients each insert N2=3000 words at random positions
// concurrently.
func BenchmarkB2_3_ConcurrentInsertWords(b *testing.B) {
	mutateA := func(t *ygo.Text, txn *ygo.TransactionMut) {
		r := rand.New(rand.NewSource(231))
		for j := 0; j < N2; j++ {
			w := randWord(r) + " "
			idx := uint64(0)
			if length := t.Length(); length > 0 {
				idx = uint64(r.Int63n(int64(length)))
			}
			_ = t.Insert(txn, idx, w)
		}
	}
	mutateB := func(t *ygo.Text, txn *ygo.TransactionMut) {
		r := rand.New(rand.NewSource(232))
		for j := 0; j < N2; j++ {
			w := randWord(r) + " "
			idx := uint64(0)
			if length := t.Length(); length > 0 {
				idx = uint64(r.Int63n(int64(length)))
			}
			_ = t.Insert(txn, idx, w)
		}
	}
	build := func() *ygo.Doc { return buildTwoPeerMerged(mutateA, mutateB) }

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

// B2.4 — Two clients perform mixed insert/delete (N2=3000 ops
// each) at random positions concurrently.
func BenchmarkB2_4_ConcurrentInsertDelete(b *testing.B) {
	mix := func(seed int64, ch string) func(*ygo.Text, *ygo.TransactionMut) {
		return func(t *ygo.Text, txn *ygo.TransactionMut) {
			r := rand.New(rand.NewSource(seed))
			for j := 0; j < N2; j++ {
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
					_ = t.Insert(txn, idx, ch)
				}
			}
		}
	}
	build := func() *ygo.Doc { return buildTwoPeerMerged(mix(241, "a"), mix(242, "b")) }

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
