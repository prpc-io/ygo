package benchmarks

import (
	"math/rand"
	"testing"

	"github.com/Deln0r/ygo"
)

// buildManyClientsMap returns a Doc into which N3 client docs have
// each set ONE entry in the named root Map under a per-client key.
// Each per-client update is captured via EncodeStateAsUpdate then
// applied to the merger Doc. Used by B3.1/B3.2/B3.3 with different
// value types.
//
// Per-client docs have ClientID = 1000+i to keep them stable for
// benchmark determinism.
func buildManyClientsMap(makeValue func(i int) any) *ygo.Doc {
	merger := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
	_ = ygo.NewMap(merger, "m")

	for i := 0; i < N3; i++ {
		c := ygo.NewDocWithOptions(ygo.Options{ClientID: uint64(1000 + i)})
		cm := ygo.NewMap(c, "m")
		txn := c.WriteTxn()
		cm.Set(txn, "k"+itoa(i), makeValue(i))
		txn.Commit()
		if err := ygo.ApplyUpdate(merger, ygo.EncodeStateAsUpdate(c)); err != nil {
			panic(err)
		}
	}
	return merger
}

// B3.1 — N3=489 clients concurrently set a numeric value under
// distinct keys in a shared Map.
func BenchmarkB3_1_ManyClientsSetNumber(b *testing.B) {
	build := func() *ygo.Doc {
		return buildManyClientsMap(func(i int) any { return int64(i) })
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

// B3.2 — N3 clients each set a JSON-style object (map[string]any)
// as the value in a shared Map. Exercises the Any TLV Object
// variant (tag 118) — keys + recursive Any values per element.
func BenchmarkB3_2_ManyClientsSetObject(b *testing.B) {
	val := map[string]any{"a": int64(1), "b": "x"}
	build := func() *ygo.Doc {
		return buildManyClientsMap(func(_ int) any { return val })
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

// B3.3 — N3 clients each set a short string value.
func BenchmarkB3_3_ManyClientsSetString(b *testing.B) {
	build := func() *ygo.Doc {
		return buildManyClientsMap(func(i int) any { return "val-" + itoa(i) })
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

// B3.4 — N3 clients each insert ONE character at a random position
// into a shared Array. Workload mirrors many concurrent writers
// on a growing list. Each per-client update is captured before
// the next client mutates, so they're causally ordered (chained
// inserts) rather than truly concurrent — this matches the
// upstream B3.4 shape (which is also sequential apply for
// reproducibility).
func BenchmarkB3_4_ManyClientsInsertArray(b *testing.B) {
	build := func() *ygo.Doc {
		r := rand.New(rand.NewSource(34))
		merger := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		_ = ygo.NewArray(merger, "a")

		for i := 0; i < N3; i++ {
			c := ygo.NewDocWithOptions(ygo.Options{ClientID: uint64(1000 + i)})
			ca := ygo.NewArray(c, "a")
			// Pull current state into the client so its insertion
			// index makes sense against the latest length.
			if i > 0 {
				if err := ygo.ApplyUpdate(c, ygo.EncodeStateAsUpdate(merger)); err != nil {
					panic(err)
				}
			}
			length := ca.Len()
			idx := uint64(0)
			if length > 0 {
				idx = uint64(r.Int63n(int64(length)))
			}
			txn := c.WriteTxn()
			ca.Insert(txn, idx, "x")
			txn.Commit()
			if err := ygo.ApplyUpdate(merger, ygo.EncodeStateAsUpdate(c)); err != nil {
				panic(err)
			}
		}
		return merger
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
