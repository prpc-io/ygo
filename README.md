# Ygo

[![CI](https://github.com/Deln0r/ygo/actions/workflows/test.yml/badge.svg)](https://github.com/Deln0r/ygo/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Deln0r/ygo.svg)](https://pkg.go.dev/github.com/Deln0r/ygo)
[![Go Report Card](https://goreportcard.com/badge/github.com/Deln0r/ygo?cache=v1.0.0)](https://goreportcard.com/report/github.com/Deln0r/ygo)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.22%2B-00ADD8.svg)](go.mod)
[![Yjs Protocol](https://img.shields.io/badge/Yjs%20protocol-V1%20%2B%20V2-7c3aed.svg)](https://github.com/yjs/yjs)
[![Live Demo](https://img.shields.io/badge/live%20demo-ygo.deln0r.com-22c55e.svg)](https://ygo.deln0r.com)
[![Codeberg Mirror](https://img.shields.io/badge/codeberg-mirror-2185d0?logo=codeberg&logoColor=white)](https://codeberg.org/Deln0r/ygo)

Pure-Go port of [Yjs](https://github.com/yjs/yjs), the CRDT framework for collaborative applications.

Ygo speaks the **Yjs V1 and V2 wire formats byte-for-byte**. JavaScript clients running `yjs@13.x` synchronize directly with Go servers and vice versa, with both directions verified through **124 cross-language fixture scenarios** generated from `yjs@13.6.31`. The same fixture suite doubles as a [cross-implementation conformance check](docs/reearth-cross-test/) for other pure-Go Yjs ports. The bundled WebSocket server is Hocuspocus-compatible. No CGO; `gomobile bind` produces verified iOS xcframework and Android AAR.

## Highlights

- **Byte-for-byte wire compatibility, verified in both directions.** 124 cross-language fixtures (generated from `yjs@13.6.31`) cover the V1 and V2 update formats, snapshots, subdocuments, awareness, and the sync protocol, JS to Go and Go to JS. The suite runs in CI on every push, so a regression in either direction fails the build.
- **Pure Go, no CGO.** Builds for any Go target, compiles to WASM, and cross-compiles freely. `gomobile bind` produces a verified iOS xcframework and Android AAR. No V8, no embedded JavaScript engine, no Rust FFI bridge.
- **Complete CRDT type set.** Map, Array, Text (rich-text formatting, Quill deltas, embeds), XML types, Awareness, UndoManager, Snapshots / time-travel, and Subdocuments.
- **Compact encoding.** Commit-time block squash collapses per-character edits into single items (about 1 byte per character in V1), and garbage collection frees deleted content at commit. On a real-world editing trace V1 document size drops from ~1.97 MB to ~223 KB, competitive with V2.
- **Forward-looking wire handling.** 53-bit client IDs throughout, byte-verified above 2^32, ready for the wider client-ID space `yjs@14` introduces.
- **Ready-to-run server.** A Hocuspocus-compatible WebSocket server with optional sqlite persistence ships in `cmd/ygo-server`.
- **EU-sovereign mirror** on [codeberg.org/Deln0r/ygo](https://codeberg.org/Deln0r/ygo), auto-synced from GitHub on every push for adopters who prefer or require EU-hosted code infrastructure.

**Live demo:** open [ygo.deln0r.com](https://ygo.deln0r.com) in two browser tabs and start typing. Same protocol any standard Yjs ecosystem client speaks, with a pure-Go server behind it.

## Quick start

```go
package main

import (
    "fmt"

    "github.com/Deln0r/ygo"
)

func main() {
    src := ygo.NewDoc()
    m := ygo.NewMap(src, "settings")

    txn := src.WriteTxn()
    m.Set(txn, "theme", "dark")
    m.Set(txn, "lang", "go")
    txn.Commit()

    // Encode the source doc's full state as wire bytes.
    update := ygo.EncodeStateAsUpdate(src)

    // Apply to a fresh peer doc — same bytes JS Yjs's Y.applyUpdate consumes.
    dst := ygo.NewDoc()
    if err := ygo.ApplyUpdate(dst, update); err != nil {
        panic(err)
    }

    dstMap := ygo.NewMap(dst, "settings")
    fmt.Println(dstMap.Get("theme")) // dark
}
```

For a collaborative server backend, see [`cmd/ygo-server`](cmd/ygo-server) — a stand-alone Hocuspocus-compatible WebSocket server with optional sqlite persistence:

```bash
go run ./cmd/ygo-server -addr :1234 -store data.db
```

### Undo / Redo

Wrap any shared types in an `UndoManager` to get scoped, grouped Undo / Redo:

```go
d := ygo.NewDoc()
m := ygo.NewMap(d, "settings")

um := ygo.NewUndoManager(d, m) // watch m; defaults to local edits, 500ms grouping
defer um.Close()

txn := d.WriteTxn()
m.Set(txn, "theme", "dark")
txn.Commit()

um.Undo() // m no longer has "theme"
um.Redo() // "theme" == "dark" again
```

Only local edits under the watched types are captured; remote updates applied via `ApplyUpdate` are not. Rapid edits inside the capture-timeout window collapse into one undo step; call `um.StopCapturing()` to force a boundary. The semantics match `yjs@13.6.31`'s `UndoManager`, checked by cross-language conformance fixtures.

### Snapshots / time-travel

Capture a point in a document's history and reconstruct it later. The source doc must have GC disabled so deleted content is retained:

```go
d := ygo.NewDocWithOptions(ygo.Options{DisableGC: true})
txt := ygo.NewText(d, "t")

txn := d.WriteTxn()
txt.Insert(txn, 0, "world!")
txn.Commit()

snap := ygo.CreateSnapshot(d)         // mark this moment
saved := ygo.EncodeSnapshot(snap)     // persist it (byte-compatible with Y.encodeSnapshot)

txn = d.WriteTxn()
txt.Insert(txn, 0, "hello ")          // doc moves on
txn.Commit()

restored, _ := ygo.RestoreSnapshot(d, snap) // reconstruct the marked state
ygo.NewText(restored, "t").String()         // "world!"
```

The snapshot wire format (`EncodeSnapshot` / `DecodeSnapshot`) is byte-compatible with `yjs@13.6.31`'s `Y.encodeSnapshot`, verified by cross-language fixtures including multi-client delete-set ordering. `RestoreSnapshot` mirrors `Y.createDocFromSnapshot`.

### Subdocuments

Nest a `Y.Doc` inside a Map. The parent stores a reference (the subdoc's GUID); the subdocument's own content syncs as a separate update stream:

```go
d := ygo.NewDoc()
m := ygo.NewMap(d, "m")

txn := d.WriteTxn()
sub := m.SetDoc(txn, "child") // nest a new subdocument
txn.Commit()

// after syncing the parent to another replica:
got, ok := m.GetDoc(d, "child") // got.GUID() == sub.GUID()
```

The `ContentDoc` wire format (GUID + options) is byte-compatible with `yjs@13.6.31`, verified by cross-language fixtures. Lifecycle events are observable via `d.OnSubdocs` (added / removed / loaded GUIDs per transaction); `SetDocWithOptions(..., autoLoad)` and `subdoc.Load()` drive the loaded set, so a sync provider knows which nested documents to fetch.

## Status

**Feature-complete and stable.** The CRDT engine, the V1 and V2 wire formats, and the full type set above are validated bidirectionally against `yjs@13.6.31` and exercised in CI on every push. The public API is considered stable for the v1.x line; changes follow semantic versioning, with new functionality as minor releases and breaking changes deferred to a future major.

| Layer | Status |
|---|---|
| `internal/lib0` varint + RLE encoding | done; verified byte-equivalent vs JS `lib0@0.2.117` (40 + 16 fixtures) |
| `internal/block` (Item, Content, Branch, Splice, Integrate-YATA, TrySquash, Repair, search markers) | done; full YATA conflict resolution + per-branch LRU position cache |
| `internal/store` (BlockStore, ItemSlice, Materialize) | done |
| `internal/doc` (Doc, Transaction, TransactionMut) | done; lock semantics + root-branch registry |
| `internal/encoding` (StateVector, IdSet, Update encode/decode/apply, Pending buffer, V1 + V2 codecs) | done; JS Yjs → Go proven by 29 V1 + 32 V2 fixture scenarios; Go → JS proven by 48 reverse fixtures (Map / Array / Text / XmlFragment) |
| `internal/utf16` (UTF-16 length / byte-offset / surrogate-aware split) | done |
| `internal/types/Map` (Set / Get / Delete / Has / Len / Range / Clear + SetMap / SetArray / SetText) | done; nested-type construction supported |
| `internal/types/Array` (Insert / InsertRange / Push / Delete / Get / Len / Range / ToSlice + InsertMap / InsertArray / InsertText) | done; nested-type construction supported |
| `internal/types/Text` (Insert / Delete / String / Length + InsertWithAttributes / Format / InsertEmbed / Range / ToDelta / ApplyDelta) | done; full rich-text + Quill delta batch API |
| Nested-type construction (Map-in-Map, Array-in-Map, etc., to arbitrary depth) | done; ContentType wire format + Repair ParentID resolution + pending-queue retry |
| `internal/types/Xml*` (XmlFragment, XmlElement, XmlText) | done; ProseMirror/Tiptap/BlockNote unblocked. XmlHook (legacy) deferred. |
| Persistence (`Store` interface + `modernc.org/sqlite` reference impl) | done; append-only update log, Flush compaction, LoadDoc / GetStateVector / GetDiff helpers; pure-Go (no CGO) |
| y-sync protocol (`internal/sync`) | done; full Hocuspocus message subset (Sync + Awareness + QueryAwareness + Auth + Stateless + BroadcastStateless + Close + SyncStatus); per-document Auth permission scoping deferred ([tech-debt](docs/tech-debt.md)) |
| Awareness (`internal/awareness`) | done; LWW presence map, JSON wire payload per y-protocols, self-eviction defense, SweepOutdated |
| `server/` (WebSocket sync server) | done; `http.Handler` mount-anywhere shape, per-doc broadcaster, persists every applied update to optional `persist.Store`, awareness disconnect tombstones |
| `cmd/ygo-server` (Hocuspocus-compat binary) | done; stand-alone WS server with optional sqlite persistence via `-store` flag |
| `gomobile/` (bytes-only subset for iOS/Android) | done; bindable `Doc` + `Awareness` wrappers with bytes-in/bytes-out methods only; pure-Go (no CGO). Both targets **verified end-to-end** on Xcode 16 + NDK 27 + Go 1.26: produces a valid `Ygo.xcframework` (real-device arm64 + simulator universal, 6.6 + 13 MB) and a valid Android `.aar` (4 archs incl. arm64-v8a / armeabi-v7a / x86 / x86_64, 8.4 MB), each drop-in for the respective IDE. See [gomobile/README.md](gomobile/README.md) for the exact commands. |
| V2 update encoding | done; lib0 RLE primitives + column encoder/decoder + `Update.{EncodeV2,DecodeV2}` + public `ygo.{EncodeStateAsUpdateV2,EncodeDiffV2,ApplyUpdateV2}`; bidirectional cross-language fixtures vs `yjs@13.6.31` |
| dmonad/crdt-benchmarks B1-B4 port | done; B1.1-B1.11 / B2.1-B2.4 / B3.1+3+4 / B4 (260k-edit real-world LaTeX trace). Baseline in [BENCHMARKS.md](BENCHMARKS.md). |
| `UndoManager` (`internal/undo`) | done; scoped Undo / Redo over Map / Array / Text with capture-timeout grouping, tracked-origin filtering, and a `Redone` chain for deletion restore. Cross-language conformance vs `yjs@13.6.31` (7 scenarios) |
| Snapshots (`CreateSnapshot` / `EncodeSnapshot` / `RestoreSnapshot`) | done; V1 wire format byte-compatible with `yjs@13.6.31` (cross-language fixtures incl. multi-client), `RestoreSnapshot` mirrors `Y.createDocFromSnapshot` |
| Subdocuments (`Map.SetDoc` / `Map.GetDoc`) | done; `ContentDoc` wire format (GUID + options) byte-compatible with `yjs@13.6.31`, cross-language fixtures. Lifecycle events via OnSubdocs / autoLoad / Load |
| Wire client-ID width | 53-bit client IDs throughout (`uint64` + varint), byte-verified against `yjs@13.6.31` for IDs above 2^32. Forward-compatible with the wider client-ID space yjs@14 introduces |
| Commit-time block squash | done; merges same-client adjacent-clock items at commit (~1 byte/char V1), paired with Apply-side partial-overlap slicing for correct remote integration of merged blocks |
| GC merging | done; deleted content is freed at commit (ContentDeleted, byte-aligned with yjs) and adjacent deleted runs are merged. Deleting a nested shared type recursively collapses its whole subtree into garbage-collected runs (cross-language fixtures), matching yjs. Skipped when GC is disabled or for items an UndoManager keeps |

## Goals

1. **Binary protocol compatibility** with [Yjs](https://github.com/yjs/yjs) v13.x in both V1 and V2 wire formats. Byte-for-byte. JS clients sync with Go servers and vice versa, bidirectionally verified.
2. **Idiomatic Go API.** Channels for events, explicit transactions, `error` returns.
3. **Pure Go.** No CGO. `gomobile bind` works for iOS/Android.
4. **Pluggable persistence** with `modernc.org/sqlite` reference implementation.
5. **Performance within 2× of [yrs](https://github.com/y-crdt/y-crdt)** on `dmonad/crdt-benchmarks` B1-B4. See [BENCHMARKS.md](BENCHMARKS.md).

## Non-goals

- C-FFI surface. [Yrs](https://github.com/y-crdt/y-crdt) already provides this; Ygo's unique value is pure-Go native binaries.
- Drop-in replacement for the Node.js Yjs runtime. Ygo is the Go port; use `yjs` itself if you want a JavaScript runtime.
- Loro, Automerge, RGA, or other CRDT designs. Ygo implements the Yjs wire format, period.

## Wire compatibility

The single most-important guarantee of this project is byte-level wire compatibility with `yjs@13.x`. This is enforced by **109 cross-language fixture scenarios**:

- **29 V1 forward fixtures** (`testdata/yjs-updates.json`) — JS Yjs encodes via `Y.encodeStateAsUpdate`, Go decodes and applies, state matches.
- **32 V2 forward fixtures** (`testdata/yjs-update-v2-fixtures.json`) — same with `Y.encodeStateAsUpdateV2`.
- **48 reverse fixtures** (`testdata/go-updates.json` + `go-update-v2-fixtures.json`) — Go encodes via `EncodeStateAsUpdate` / `EncodeStateAsUpdateV2`, JS Yjs decodes via `Y.applyUpdate` / `Y.applyUpdateV2`, state matches.

The fixtures regenerate from pinned `yjs@13.6.31` + `lib0@0.2.117` + `y-protocols@1.0.7` on every CI run; `git diff --exit-code testdata/` catches byte-level regressions.

## How is this different from Hocuspocus / y-websocket / y-leveldb?

| Project | Runtime | What it provides | Relationship to Ygo |
|---|---|---|---|
| `yjs` (npm) | Node / browser | The reference CRDT implementation | Ygo's wire-format target |
| `y-websocket` | Node | Reference WebSocket server | Ygo's `cmd/ygo-server` is a Go-native equivalent |
| `Hocuspocus` | Node | Production WebSocket server with auth, persistence, extensions | Ygo's `cmd/ygo-server` speaks the same 8-message envelope (Sync / Awareness / QueryAwareness / Auth / Stateless / BroadcastStateless / Close / SyncStatus) |
| `yrs` | Rust | Reference Rust port | Ygo's executable spec for porting decisions |
| `y-leveldb`, `y-indexeddb` | Node / browser | Persistence backends | Ygo's `persist/sqlite` is a Go-native equivalent |
| **Ygo** | **Go** | **CRDT engine + WS server + persistence in one monorepo, pure-Go for native mobile** | **This project** |

If you have an existing Yjs deployment and want to move the server side to Go (no Node runtime, single static binary, native iOS / Android via gomobile) — Ygo is the path. If you're starting fresh and your team is comfortable with Node, Hocuspocus is the mature choice.

## Benchmarks

See [BENCHMARKS.md](BENCHMARKS.md) for the full table. Highlights from B4 (259,778-edit real-world LaTeX paper trace) on Apple M3, Go 1.26:

| Metric | Ygo V1 | Ygo V2 | yjs (Node, Intel i5-8400) | ywasm (Intel i5-8400) |
|---|---|---|---|---|
| Apply all edits | 10.5 s | 10.5 s | 5.7 s | 28.7 s |
| Encoded doc size | 1.97 MB | **227 KB** | 160 KB | 160 KB |
| Encode time | 7.7 ms | 73 ms | 11 ms | 3 ms |
| Parse time | 68 ms | 61 ms | 39 ms | 16 ms |

**How to read this:**

- **Apply throughput** — within ~1.85× of native yjs on different hardware (Apple M3 vs Intel i5-8400; the M3 is generally faster so the real ratio is closer than the wall-clock suggests). Native yrs publishes sub-10-s numbers on similar hardware, putting Ygo within roughly 1.0-1.5× of yrs and comfortably under the DESIGN.md "within 2×" target. (ywasm is yrs compiled to WebAssembly and is not representative of native yrs — wasm overhead inflates it ~5×.)
- **V2 doc size** is competitive with yjs at 1.4× — V2's per-column RLE encoding effectively dedupes per-item overhead at the wire layer.
- **V1 doc size** is now competitive: commit-time block squash merges same-client adjacent-clock items at commit, so per-character `Text.Insert` runs collapse into single items. A 2,000-character sequential insert encodes to ~1.0 byte/char in V1 (previously ~12× larger when every character was its own Item). Squash ships with the paired Apply-side partial-overlap handling that keeps remote integration correct when a peer sends a merged block overlapping the receiver's state.

A direct head-to-head harness against native yrs under identical hardware is on the roadmap but not yet run; the numbers above are honest absolute figures with hardware caveats.

## Roadmap

Towards v1.0: benchmarks refresh · documentation site · external security audit. (Undo manager, Snapshots, Subdocuments, commit-time block squash, GC merging: done.)

Per-layer port notes live in [docs/yrs-port-notes/](docs/yrs-port-notes/). Items intentionally deferred or partial are tracked in [docs/tech-debt.md](docs/tech-debt.md). Detailed design decisions in [DESIGN.md](DESIGN.md).

## Documentation

- [DESIGN.md](DESIGN.md) — project design document
- [BENCHMARKS.md](BENCHMARKS.md) — performance baseline + B1-B4 methodology
- [gomobile/README.md](gomobile/README.md) — iOS xcframework + Android AAR build instructions
- [docs/yrs-port-notes/](docs/yrs-port-notes/) — per-layer port notes describing how each yrs subsystem maps to Go
- [docs/tech-debt.md](docs/tech-debt.md) — deferred work and known limitations
- [CONTRIBUTING.md](CONTRIBUTING.md) — DCO sign-off, no CLA

## License

MIT. See [LICENSE](LICENSE).

## Acknowledgements

- [Kevin Jahns (dmonad)](https://github.com/dmonad) for [Yjs](https://github.com/yjs/yjs) and the YATA algorithm.
- [Bartosz Sypytkowski](https://www.bartoszsypytkowski.com/) for [yrs](https://github.com/y-crdt/y-crdt) and the [architecture deep dive](https://www.bartoszsypytkowski.com/yrs-architecture/).
