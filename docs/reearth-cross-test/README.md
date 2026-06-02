# Cross-implementation conformance test: `reearth/ygo` v1.19.0

This directory contains a harness that exercises another pure-Go port of Yjs, [`github.com/reearth/ygo`](https://github.com/reearth/ygo) v1.19.0, against the same `yjs@13.6.20`-anchored fixture suite that Ygo itself uses for its bidirectional binary-protocol-compat tests.

The purpose is plain due-diligence cross-validation. Two pure-Go ports of the same wire format ought to converge on the same reference fixtures; where they do not, the discrepancy is worth recording.

## Headline

| Metric | Count |
|--------|-------|
| Scenarios run | 114 |
| Pass | 97 |
| Fail | 17 |
| Pass rate | 85.1% |

Of the 17 failures, 10 are real data-correctness gaps (parser crash or silent data loss on reference yjs bytes). The remaining 7 are either XML serialisation style differences (`<p></p>` vs `<p/>`, both valid XML) or harness false positives where the source fixture file did not record an `expected_xml` field against which to compare.

The same 114 scenarios all pass on Ygo. The failing inputs are reference `yjs@13.6.20`-produced bytes, not artefacts of fixture generation.

## The 10 data-correctness failures, deduplicated to 3 patterns

### Pattern A: Last-Write-Wins on a duplicate Map key

Setting the same Map key twice within one update batch should resolve to the last value (standard LWW semantic in Yjs).

| Wire | Direction | Symptom |
|------|-----------|---------|
| V1 | yjs-encoded reference | Parser crash: `crdt: invalid update: encoding: unexpected end of input` |
| V1 | Ygo-encoded | Parser crash: `crdt: invalid update: encoding: unknown Any tag` |
| V2 | yjs-encoded reference | Silent data loss: empty Map returned instead of `{k: "third"}` |
| V2 | Ygo-encoded | Silent data loss: empty Map returned instead of `{k: "third"}` |

Two different V1 error paths on the same logical input suggest two independently-affected V1 decoder code paths.

### Pattern B: Map set + delete + set on the same key

The same root cause as Pattern A, with an explicit delete in the middle.

| Wire | Direction | Symptom |
|------|-----------|---------|
| V1 | yjs-encoded reference | Parser crash |
| V2 | yjs-encoded reference | Silent empty Map |

### Pattern C: Empty-string Map key dropped

Yjs supports empty strings as Map keys; the `""` key is a valid identifier in the wire format. `reearth/ygo` decodes other keys in the same payload correctly but drops the empty-key entry.

Reproduces in all four combinations (V1 / V2, both directions).

## What works correctly

- All 19 V1 Array scenarios (yjs-encoded + Ygo-encoded)
- All 23 V1 Text scenarios
- All 20 V2 Array scenarios
- All 24 V2 Text scenarios
- 13 of 23 Map scenarios (single-set, multi-key, unicode keys, primitive value types, V2 RLE-compressed many-key payloads)

`reearth/ygo` reliably handles Array, Text, and the simpler Map operations. Failures cluster narrowly around Map conflict-resolution semantics and the empty-key edge case.

## Reproducing the run

```bash
cd docs/reearth-cross-test
go mod download
go run main.go         # summary only
go run main.go -v      # per-scenario verbose
```

The harness reads fixture files from the parent repository's `testdata/` directory. Adjust the `-v1`, `-v2`, `-xml`, `-rv1`, `-rv2` flags if running from elsewhere.

Outputs of a 2026-06-02 run are saved alongside the harness as `output.txt` (summary) and `output-verbose.txt` (per-scenario).

## What this is, and what it isn't

This is a black-box wire-format compatibility test driven by `yjs@13.6.20` reference fixtures. It tests whether `reearth/ygo` correctly decodes and applies bytes that the canonical Yjs implementation produces, and whether it produces bytes that decode to the same end state. It does not test:

- Concurrency or convergence semantics under multi-client mixed-order Apply
- `gomobile bind` compatibility on iOS or Android
- Sync protocol (`y-sync`) message framing
- Awareness protocol (`y-protocols`) interop
- Performance or memory characteristics

These would be useful additional comparison vectors but were not run for this report.

## Maintainer note

If the `reearth/ygo` maintainers want to look at the failing fixtures directly, every failing scenario is a row in one of the JSON files in `../testdata/`: `yjs-updates.json` (V1 forward), `yjs-update-v2-fixtures.json` (V2 forward), `go-updates.json` (V1 reverse), `go-update-v2-fixtures.json` (V2 reverse). Each row has a `description`, an `update_hex` field with the bytes, and an `expected_map` / `expected_array` / `expected_text` / `expected_xml` field with what the reference implementation arrived at. Patch contributions or upstream discussion welcome.
