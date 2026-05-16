// Package benchmarks ports the dmonad/crdt-benchmarks B1-B4 workload
// suite to ygo. The benchmark IDs (B1.1 .. B4) match the upstream
// spec verbatim so cross-implementation comparison stays apples-to-
// apples — see README in https://github.com/dmonad/crdt-benchmarks
// for the canonical descriptions.
//
// Methodology per benchmark:
//   - "ops" sub-benchmark times the workload itself (mutations into
//     a fresh Doc), no encode.
//   - "encode_v1" / "encode_v2" sub-benchmarks time the wire
//     encoder against a pre-built Doc; report doc-size in bytes
//     via b.ReportMetric.
//   - "parse_v1" / "parse_v2" sub-benchmarks time DecodeUpdate +
//     Apply against a fresh receiver Doc.
//
// Run: go test -bench=. -benchmem ./benchmarks/
// Run one: go test -bench=B1_1 -benchmem ./benchmarks/
//
// B4 expects the upstream automerge-paper trace at
// testdata/b4-trace.json.gz — skipped if absent. See
// testdata/gen/fetch-b4-trace.sh.
package benchmarks

import (
	"math/rand"
	"strconv"
	"testing"

	"github.com/Deln0r/ygo"
)

// N is the canonical operation count per dmonad/crdt-benchmarks B1
// workloads (6,000 ops). Kept as a const so all B1.x scenarios use
// the same scale for cross-comparison.
const N = 6000

// N2 is the per-peer op count for B2 concurrent workloads. Each of
// two peers does N2 ops, so total ops is 2*N2 = 6000 (matches B1).
const N2 = N / 2

// N3 is the client count for B3 high-conflict workloads. Spec says
// "20 * sqrt(N)" where N=600 → 489 clients. We use the spec value
// directly rather than recomputing.
const N3 = 489

// reportSize attaches the V1+V2 encoded sizes of d as custom metrics
// on the benchmark. Call once at the end of an "ops" sub-benchmark
// after the doc has been fully built — the cost of encoding is
// excluded from the timing.
func reportSize(b *testing.B, d *ygo.Doc) {
	b.Helper()
	v1 := ygo.EncodeStateAsUpdate(d)
	v2 := ygo.EncodeStateAsUpdateV2(d)
	b.ReportMetric(float64(len(v1)), "docBytesV1")
	b.ReportMetric(float64(len(v2)), "docBytesV2")
}

// benchEncodeV1 / benchEncodeV2 / benchParseV1 / benchParseV2 are
// the four canonical wire-format sub-benchmarks. Each takes a
// pre-built doc and reports avg time across b.N iterations.
func benchEncodeV1(b *testing.B, build func() *ygo.Doc) {
	d := build()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ygo.EncodeStateAsUpdate(d)
	}
}

func benchEncodeV2(b *testing.B, build func() *ygo.Doc) {
	d := build()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = ygo.EncodeStateAsUpdateV2(d)
	}
}

func benchParseV1(b *testing.B, build func() *ygo.Doc) {
	d := build()
	bytes := ygo.EncodeStateAsUpdate(d)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst := ygo.NewDoc()
		if err := ygo.ApplyUpdate(dst, bytes); err != nil {
			b.Fatalf("ApplyUpdate: %v", err)
		}
	}
}

func benchParseV2(b *testing.B, build func() *ygo.Doc) {
	d := build()
	bytes := ygo.EncodeStateAsUpdateV2(d)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dst := ygo.NewDoc()
		if err := ygo.ApplyUpdateV2(dst, bytes); err != nil {
			b.Fatalf("ApplyUpdateV2: %v", err)
		}
	}
}

// runStandardSuite runs all four wire-format sub-benchmarks against
// a build-doc closure. Call from each B*_test top-level after
// timing the "ops" workload itself.
func runStandardSuite(b *testing.B, build func() *ygo.Doc) {
	b.Helper()
	b.Run("encode_v1", func(b *testing.B) { benchEncodeV1(b, build) })
	b.Run("encode_v2", func(b *testing.B) { benchEncodeV2(b, build) })
	b.Run("parse_v1", func(b *testing.B) { benchParseV1(b, build) })
	b.Run("parse_v2", func(b *testing.B) { benchParseV2(b, build) })
}

// randString returns a deterministic pseudo-random string of length n.
// The PRNG is seeded per-call so test runs are reproducible
// independent of test order.
func randString(seed int64, n int) string {
	r := rand.New(rand.NewSource(seed))
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte('a' + r.Intn(26))
	}
	return string(buf)
}

// randWord returns a deterministic pseudo-random ASCII word of
// length 4-10.
func randWord(r *rand.Rand) string {
	n := 4 + r.Intn(7)
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte('a' + r.Intn(26))
	}
	return string(buf)
}

// itoa is a small helper to avoid pulling strconv into every test.
func itoa(i int) string { return strconv.Itoa(i) }
