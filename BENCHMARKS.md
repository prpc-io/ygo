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

## Baseline (Apple M3, Go 1.26.3, `-benchtime=3x`, V2 step 4 build at `4341c20`)

All times in ms (1 ms = 1,000,000 ns); doc sizes in bytes.

### B1 — Single-client workloads (N = 6,000 ops)

| ID    | Workload                              |    ops (ms) |  V1 doc |  V2 doc | enc V1 (ms) | enc V2 (ms) | parse V1 (ms) | parse V2 (ms) |
|-------|---------------------------------------|------------:|--------:|--------:|------------:|------------:|--------------:|--------------:|
| B1.1  | Append N chars sequentially           |       72.77 |  35,880 |   6,036 |        0.11 |        0.34 |          1.20 |          1.19 |
| B1.2  | Single insert of N-char string        |        0.06 |   6,013 |   6,026 |       0.001 |       0.006 |         0.007 |         0.020 |
| B1.3  | Prepend N chars one-at-a-time         |        0.75 |  35,880 |   6,036 |        0.09 |        0.34 |          1.21 |          1.23 |
| B1.4  | Insert N chars at random positions    |       67.59 |  52,753 |  29,486 |        0.12 |        0.50 |          2.22 |          2.32 |
| B1.5  | Insert N words at random positions    |      135.09 | 138,396 | 102,408 |        0.33 |        5.93 |          4.01 |          3.91 |
| B1.6  | Insert N chars then delete all        |       72.56 |  35,885 |   6,041 |        0.15 |        0.47 |          1.70 |          1.73 |
| B1.7  | Mixed insert/delete at random         |       45.85 |  38,686 |  20,505 |        0.12 |        0.36 |          1.64 |          1.54 |
| B1.8  | Append N numbers to Array             |      113.84 |  47,816 |  17,970 |        0.12 |        0.14 |          1.42 |          1.84 |
| B1.9  | Single insert of N-number Array       |        0.04 |  17,949 |  17,960 |        0.03 |        0.03 |         0.071 |         0.077 |
| B1.10 | Prepend N numbers                     |        0.67 |  47,816 |  17,970 |        0.11 |        0.13 |          1.42 |          1.54 |
| B1.11 | Insert N numbers at random positions  |       58.17 |  64,692 |  41,461 |        0.16 |        0.25 |          2.41 |          2.38 |

### B2 — Two-client concurrent workloads (N₂ = 3,000 ops per peer)

| ID   | Workload                                       |    ops (ms) |  V1 doc |  V2 doc | parse V1 (ms) | parse V2 (ms) |
|------|------------------------------------------------|------------:|--------:|--------:|--------------:|--------------:|
| B2.1 | Concurrently insert at index 0 (worst-case)    |      732.57 |  35,758 |   6,059 |        761.64 |        757.47 |
| B2.2 | Concurrently insert at random positions        |       38.61 |  51,879 |  28,860 |          8.32 |          8.38 |
| B2.3 | Concurrently insert words at random positions  |       74.74 | 133,117 |  99,042 |          7.29 |          7.04 |
| B2.4 | Concurrently insert + delete                   |       26.30 |  37,338 |  17,861 |          1.84 |          1.86 |

### B3 — Many-client high-conflict (N₃ = 489 clients)

| ID   | Workload                              |    ops (ms) | V1 doc | V2 doc | parse V1 (ms) | parse V2 (ms) |
|------|---------------------------------------|------------:|-------:|-------:|--------------:|--------------:|
| B3.1 | 489 clients set Number in shared Map  |        1.00 |  8,142 |  6,694 |          0.29 |          0.30 |
| B3.2 | 489 clients set Object in shared Map  |     skipped |    n/a |    n/a |           n/a |           n/a |
| B3.3 | 489 clients set String in shared Map  |        1.03 | 11,030 |  9,582 |          0.28 |          0.30 |
| B3.4 | 489 clients insert into shared Array  |      133.60 |  7,329 |  5,400 |          0.26 |          0.31 |

### B4 — Real-world LaTeX paper trace (259,778 edits, 104k-char final document)

| ID | Workload                          |    ops (ms) |    V1 doc |   V2 doc | enc V1 (ms) | enc V2 (ms) | parse V1 (ms) | parse V2 (ms) |
|----|-----------------------------------|------------:|----------:|---------:|------------:|------------:|--------------:|--------------:|
| B4 | Real-world editing trace          |   83,593.28 | 1,974,942 |  226,824 |        7.67 |       98.35 |         71.68 |         64.43 |

## Notes & observations

- **V2 doc-size wins are dramatic on RLE-friendly workloads.** B1.1
  (append-only) and B1.3 (prepend-only) both compress 35,880 → 6,036
  bytes (6× smaller) because clock deltas are constant. B4 trace
  compresses 1.97 MB → 227 KB (8.7× smaller) — V2 is the right
  choice for persistence layers and large-document sync.

- **V2 doc-size loss appears only on bulk single inserts** (B1.2,
  B1.9 — 6,013 vs 6,026 / 17,949 vs 17,960) where the V2 column
  overhead (10 length prefixes + feature flag = 11+ bytes) isn't
  amortized over many records.

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

- **B4 op-throughput** is ~0.32 ms / edit (259,778 ops / 83.6 s).
  yrs's published B4 numbers run faster (sub-10s on similar
  hardware) thanks to search markers, which we deferred to
  post-MVP (tracked in [docs/tech-debt.md](docs/tech-debt.md)
  under "Array.findInsertPosition lacks search markers"). The
  current numbers establish a baseline; closing the gap to
  DESIGN.md's "within 2× of yrs" target is a separate optimization
  workstream.

## Comparison with yrs

A side-by-side run under identical hardware would require driving
both ygo and yrs through a single harness (the upstream
`dmonad/crdt-benchmarks` JS bench runner shells out to native
modules; we'd need a Go adapter). That harness is on the roadmap
but out of scope for this commit. For now the numbers above are
ygo absolute; cross-impl comparison is left as future work,
tracked in [tech-debt.md](docs/tech-debt.md).

The DESIGN.md target is "within 2× of yrs" — these baselines tell
us where the gaps are:

- **Wire format size** — V2 numbers are competitive with yrs's V2
  (per public dmonad/crdt-benchmarks results, yrs encodes B4 to
  ~150 KB; ygo gets 227 KB — within 1.5×).
- **Op throughput on real traces (B4)** — biggest gap; search-
  marker work is the unlock.
- **Encode/parse times** — within ~10× ms-range of yrs; mostly
  unoptimized but not dominated by algorithmic gaps.

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
