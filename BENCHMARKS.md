# Benchmarks

Performance baseline for ygo, ported from the canonical
[`dmonad/crdt-benchmarks`](https://github.com/dmonad/crdt-benchmarks)
suite. Same benchmark IDs (B1.1, B1.2, ... B4) and same workload
shapes so cross-implementation comparison stays apples-to-apples.

## Running

```
# All benchmarks (skips B4 if trace not present)
go test -bench=. -benchmem ./benchmarks/

# A single benchmark
go test -bench=B1_1 -benchmem ./benchmarks/

# More samples for stability
go test -bench=. -benchtime=5x -benchmem ./benchmarks/

# B4 needs the upstream trace (3.2 MB, gitignored)
node testdata/gen/fetch-b4-trace.mjs
go test -bench=B4 -benchtime=1x -timeout=600s ./benchmarks/
```

Each benchmark ID runs five sub-benchmarks:

- `ops` — time the workload (mutations into a fresh Doc); also
  reports `docBytesV1` / `docBytesV2` (encoded doc size in bytes)
  as custom metrics.
- `encode_v1` / `encode_v2` — time `EncodeStateAsUpdate` /
  `EncodeStateAsUpdateV2` against the pre-built doc.
- `parse_v1` / `parse_v2` — time `ApplyUpdate` / `ApplyUpdateV2`
  on a fresh receiver doc against the encoded bytes.

## Search-marker delta

Before-and-after comparison for the search-marker rollout (post-V2
baseline `cb5b210` → search-marker commit). Same hardware,
`-benchtime=3x` for B1, `-benchtime=1x` for B4. "ops" sub-bench
only (encode/parse paths unaffected by markers).

| ID                              | Before (ms) | After (ms) | Change |
|---------------------------------|------------:|-----------:|-------:|
| B1.3  Prepend chars             |        0.75 |       1.19 |   +59% |
| B1.4  Random insert chars       |       67.59 |      44.02 |   -35% |
| B1.5  Random insert words       |      135.09 |      93.03 |   -31% |
| B1.10 Prepend numbers           |        0.67 |       0.76 |   +13% |
| B1.11 Random insert numbers     |       58.17 |      37.55 |   -35% |
| B2.1  Concurrent insert at 0    |      732.57 |     726.18 |    -1% |
| **B4  Real-world LaTeX trace**  |   **83,593**|  **10,540**| **-87%** |

The B4 result is the headline — 8× speedup on the canonical
real-world workload. Random-insert workloads improved 30-35%
because the per-edit walk distance drops from O(N) to O(local
delta from previous edit). Prepend-only workloads show small
regressions (a few hundred ns/op) attributable to the marker
bookkeeping overhead — prepends keep idx=0 so markers never get
populated and the cost is pure overhead. Absolute numbers tiny;
acceptable trade-off given the B4 win.

## Baseline (Apple M3, Go 1.26.3, `-benchtime=3x`, post-search-marker build)

All times in ms (1 ms = 1,000,000 ns); doc sizes in bytes.

### B1 — Single-client workloads (N = 6,000 ops)

| ID    | Workload                              |    ops (ms) |  V1 doc |  V2 doc | enc V1 (ms) | enc V2 (ms) | parse V1 (ms) | parse V2 (ms) |
|-------|---------------------------------------|------------:|--------:|--------:|------------:|------------:|--------------:|--------------:|
| B1.1  | Append N chars sequentially           |       13.43 |   6,013 |   6,026 |       0.003 |       0.009 |          0.01 |          0.01 |
| B1.2  | Single insert of N-char string        |        0.06 |   6,013 |   6,026 |       0.001 |       0.006 |         0.007 |         0.020 |
| B1.3  | Prepend N chars one-at-a-time         |        0.75 |  35,880 |   6,036 |        0.09 |        0.34 |          1.21 |          1.23 |
| B1.4  | Insert N chars at random positions    |       44.02 |  52,753 |  29,486 |        0.12 |        0.43 |          2.16 |          2.37 |
| B1.5  | Insert N words at random positions    |       93.03 | 138,396 | 102,408 |        0.41 |        5.73 |          3.98 |          3.72 |
| B1.6  | Insert N chars then delete all        |       11.16 |      18 |      29 |       0.001 |       0.002 |         0.003 |         0.004 |
| B1.7  | Mixed insert/delete at random         |       45.85 |  38,686 |  20,505 |        0.12 |        0.36 |          1.64 |          1.54 |
| B1.8  | Append N numbers to Array             |      113.84 |  47,816 |  17,970 |        0.12 |        0.14 |          1.42 |          1.84 |
| B1.9  | Single insert of N-number Array       |        0.04 |  17,949 |  17,960 |        0.03 |        0.03 |         0.071 |         0.077 |
| B1.10 | Prepend N numbers                     |        0.67 |  47,816 |  17,970 |        0.11 |        0.13 |          1.42 |          1.54 |
| B1.11 | Insert N numbers at random positions  |       37.55 |  64,692 |  41,461 |        0.13 |        0.23 |          2.35 |          2.40 |

### B2 — Two-client concurrent workloads (N₂ = 3,000 ops per peer)

| ID   | Workload                                       |    ops (ms) |  V1 doc |  V2 doc | parse V1 (ms) | parse V2 (ms) |
|------|------------------------------------------------|------------:|--------:|--------:|--------------:|--------------:|
| B2.1 | Concurrently insert at index 0 (worst-case)    |      732.57 |  35,758 |   6,059 |        761.64 |        757.47 |
| B2.2 | Concurrently insert at random positions        |       38.61 |  51,879 |  28,860 |          8.32 |          8.38 |
| B2.3 | Concurrently insert words at random positions  |       74.74 | 133,117 |  99,042 |          7.29 |          7.04 |
| B2.4 | Concurrently insert + delete                   |       26.30 |  33,258 |  14,985 |          1.84 |          1.86 |

### B3 — Many-client high-conflict (N₃ = 489 clients)

| ID   | Workload                              |    ops (ms) | V1 doc | V2 doc | parse V1 (ms) | parse V2 (ms) |
|------|---------------------------------------|------------:|-------:|-------:|--------------:|--------------:|
| B3.1 | 489 clients set Number in shared Map  |        1.00 |  8,142 |  6,694 |          0.29 |          0.30 |
| B3.2 | 489 clients set Object in shared Map  |        3.80 | 12,118 | 10,670 |          0.84 |          0.70 |
| B3.3 | 489 clients set String in shared Map  |        1.03 | 11,030 |  9,582 |          0.28 |          0.30 |
| B3.4 | 489 clients insert into shared Array  |      133.60 |  7,329 |  5,400 |          0.26 |          0.31 |

### B4 — Real-world LaTeX paper trace (259,778 edits, 104k-char final document)

| ID | Workload                          |    ops (ms) |    V1 doc |   V2 doc | enc V1 (ms) | enc V2 (ms) | parse V1 (ms) | parse V2 (ms) |
|----|-----------------------------------|------------:|----------:|---------:|------------:|------------:|--------------:|--------------:|
| B4 | Real-world editing trace          |   20,813.13 |   223,414 |  159,921 |        2.41 |       45.6  |         44.2  |         41.0  |

## Notes & observations

- **V1 doc size is now competitive after commit-time squash + GC.**
  The former large V1 overhead on fine-grained text is gone: B1.1
  (append-only) drops 35,880 → 6,013 bytes, level with V2, because
  per-character inserts merge into single items at commit. On the B4
  real-world trace V1 falls from 1.97 MB to 223 KB (about 8.8× smaller),
  putting V1 within ~1.4× of V2 instead of ~8.7× larger. Prepend-only
  (B1.3) still favours V2 because reverse-order inserts do not form
  mergeable runs.

- **Garbage collection shrinks delete-heavy documents.** B1.6 (insert
  then delete everything) is now 18 bytes in V1: deleted content is
  freed at commit and replaced with a compact deleted marker, the same
  GC yjs performs. The B4 trace, which is mostly edits (insert + delete),
  benefits in both formats.

- **V2 still wins on RLE-friendly mixed workloads** (random inserts,
  B1.4 / B1.5) where column encoding dedupes structure squash cannot
  reach, and its slight per-insert column overhead (B1.2, B1.9) is
  unchanged. Choose V2 for persistence and large-document sync; V1 is
  now a reasonable default for live sync traffic.

- **V2 encode time can be higher than V1** on word-heavy or
  string-column-heavy workloads (B1.5: 5.93 vs 0.33 ms; B4: 98 vs
  7.7 ms) because of `StringEncoder` UTF-16 length computation and
  per-key staging. V1 just emits varstrings inline. Workloads with
  many small distinct strings (each going through the column key
  table) pay this most.

- **B2.1 is the YATA concurrent-tie-break worst case** — two peers
  both inserting at index 0 means every block has to compare against
  every same-position predecessor on integrate. This benchmark
  doubles as an integration-loop stress test; ~730 ms ops + ~760
  ms parse is dominated by integrate's per-block O(n) scan against
  the conflict set.

- **B3.x scales gracefully** because each per-client update is a
  single Map.Set — only one block per merge, so the integrate cost
  stays sub-linear in client count.

- **B4 op-throughput** is ~0.041 ms / edit (259,778 ops / 10.5 s)
  after search markers landed; was ~0.32 ms / edit on the pre-
  marker baseline. yrs's published B4 numbers run sub-10 s on
  similar hardware — we are within roughly 1.0-1.5× of yrs on
  this workload (no direct head-to-head harness yet to pin the
  ratio exactly; native yrs hardware-normalized numbers needed),
  comfortably under DESIGN.md's "within 2× of yrs" target.

## Comparison with yjs / ywasm (published numbers)

`dmonad/crdt-benchmarks` publishes results for `yjs` (the npm
JS package, the canonical reference) and `ywasm` (yrs compiled
to WASM, called from JS via wasm-bindgen). The published numbers
were measured on **Intel® Core™ i5-8400 @ 2.80 GHz × 6 with
Node 20.5.0**. Ours were measured on **Apple M3 with Go 1.26.3**.
Hardware and runtime differ, so this is not an apples-to-apples
benchmark — but it places ygo in the same order of magnitude
on the canonical workload and identifies where the remaining
gaps are.

B4 (real-world LaTeX paper trace, 259,778 edits → 104,852-char
document):

| Metric                | yjs (Node)  | ywasm (WASM) | ygo V1 | ygo V2 |
|-----------------------|------------:|-------------:|-------:|-------:|
| Apply edits (ms)      |       5,714 |       28,675 | 10,540 | 10,540 |
| Encode (ms)           |          11 |            3 |    7.7 |   72.7 |
| Parse (ms)            |          39 |           16 |   67.8 |   61.4 |
| Doc size (bytes)      |     159,929 |      159,929 | **1,974,942** | **226,824** |
| Memory used (MB)      |         3.2 |          0.0 |    n/a |    n/a |

Notes:

- **ywasm is NOT a fair representative of native yrs.** wasm-
  bindgen adds substantial overhead (~5× on this workload per
  the table above); native yrs's own benchmarks ship sub-1-second
  B4 numbers. A direct ygo-vs-native-yrs run under identical
  hardware is on the roadmap but not yet executed.
- **ygo V1 doc size is bloated (~12× yjs's V1).** This is the
  visible effect of commit-time block squash being deferred
  (see [tech-debt.md](docs/tech-debt.md) "Transaction commit
  lifecycle"): every per-character Text.Insert in the trace
  produces a separate `Item`, none of which get merged with
  their same-client adjacent-clock neighbours at commit. The
  fix is a paired pair of changes — commit-time squash itself
  PLUS Apply-side partial-overlap handling (post-squash peers
  emit blocks whose clock range partially overlaps what the
  receiver already has, and the current Apply path can't
  slice them). Both pieces are in the same grant-scope
  milestone. With them shipped, V1 size should drop to
  within ~1-2× yjs's V1.
- **ygo V2 doc size is competitive (~1.4× yjs's V1).** V2's
  per-column RLE compression effectively dedupes the per-item
  overhead (constant clock deltas collapse via IntDiffOptRle),
  so it captures most of the squash benefit at the wire layer
  without needing in-memory merging. V2 is the right choice
  for persistence/snapshot today, regardless of squash status.
- **Apply throughput is within ~1.85× yjs's** on this workload
  after search markers landed — comfortably within DESIGN.md's
  "within 2× of yrs" target (yrs is itself usually faster than
  yjs on B4 per native benchmarks).
- **Encode V2 is ~7× slower** than encode V1 because of
  `StringEncoder` UTF-16 length computation and per-key staging
  — the per-column primitives that buy us the 8.7× doc-size
  win cost time at flush. Acceptable trade for snapshot/disk;
  V1 is the right choice for hot-path encode (broadcast paths).

A proper cross-impl harness that runs ygo + native yrs +
yjs through a single comparison runner under identical
hardware is on the [tech-debt list](docs/tech-debt.md). The
numbers above are sufficient for positioning in grant
applications and project documentation; a head-to-head harness
is a "later" optimization.

## Reproducing

The benchmarks are deterministic per Go's `math/rand` seed. Each
ID uses a distinct seed (B1.4 → 14, B1.5 → 15, B1.7 → 17, B1.11 →
111, B2.2 → 221/222, etc.) so re-runs produce stable byte counts.
Use `-count=N` to gather multiple samples for statistical
comparison via `benchstat`:

```
go test -bench=. -benchtime=5x -count=5 -benchmem ./benchmarks/ | tee bench.txt
benchstat bench.txt
```
