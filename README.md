# ygo

[![CI](https://github.com/Deln0r/ygo/actions/workflows/test.yml/badge.svg)](https://github.com/Deln0r/ygo/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Deln0r/ygo.svg)](https://pkg.go.dev/github.com/Deln0r/ygo)
[![Go Report Card](https://goreportcard.com/badge/github.com/Deln0r/ygo)](https://goreportcard.com/report/github.com/Deln0r/ygo)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.22%2B-00ADD8.svg)](go.mod)
[![Yjs Protocol](https://img.shields.io/badge/Yjs%20protocol-V1%20%2B%20V2-7c3aed.svg)](https://github.com/yjs/yjs)
[![Live Demo](https://img.shields.io/badge/live%20demo-ygo.deln0r.com-22c55e.svg)](https://ygo.deln0r.com)

Pure-Go port of [Yjs](https://github.com/yjs/yjs), the CRDT framework for collaborative applications.

ygo speaks the **Yjs V1 and V2 wire formats byte-for-byte**, so JavaScript clients running `yjs@13.x` synchronize directly with Go servers (and vice versa) — both directions verified by **109 cross-language fixture scenarios** against `yjs@13.6.20`. The bundled WebSocket server is Hocuspocus-compatible. No CGO; `gomobile bind` produces verified iOS xcframework and Android AAR.

**Live demo:** open [ygo.deln0r.com](https://ygo.deln0r.com) in two browser tabs and start typing — same protocol any standard Yjs ecosystem client speaks, with a pure-Go server behind it.

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

## Status

**Alpha. Public API may change before v1.0.** The CRDT engine and wire format are production-stable in the sense that they have been validated bidirectionally against `yjs@13.6.20`; the API surface (function signatures, package layout) may still see small refinements.

| Layer | Status |
|---|---|
| `internal/lib0` varint + RLE encoding | done; verified byte-equivalent vs JS `lib0@0.2.93` (40 + 16 fixtures) |
| `internal/block` (Item, Content, Branch, Splice, Integrate-YATA, TrySquash, Repair, search markers) | done; full YATA conflict resolution + per-branch LRU position cache |
| `internal/store` (BlockStore, ItemSlice, Materialize) | done |
| `internal/doc` (Doc, Transaction, TransactionMut) | done; lock semantics + root-branch registry |
| `internal/encoding` (StateVector, IdSet, Update encode/decode/apply, Pending buffer, V1 + V2 codecs) | done; JS Yjs → Go proven by 25 V1 + 29 V2 fixture scenarios; Go → JS proven by 42 reverse fixtures (Map / Array / Text / XmlFragment) |
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
| V2 update encoding | done; lib0 RLE primitives + column encoder/decoder + `Update.{EncodeV2,DecodeV2}` + public `ygo.{EncodeStateAsUpdateV2,EncodeDiffV2,ApplyUpdateV2}`; bidirectional cross-language fixtures vs `yjs@13.6.20` |
| dmonad/crdt-benchmarks B1-B4 port | done; B1.1-B1.11 / B2.1-B2.4 / B3.1+3+4 / B4 (260k-edit real-world LaTeX trace). Baseline in [BENCHMARKS.md](BENCHMARKS.md). After search markers landed, B4 op-throughput is within ~1.1× of yrs's published numbers |
| Undo manager / Snapshots / Subdocs / Y.Array.move / GC merging | planned for v1.0; see [ROADMAP](#roadmap) |

## Goals

1. **Binary protocol compatibility** with [yjs](https://github.com/yjs/yjs) v13.x in both V1 and V2 wire formats. Byte-for-byte. JS clients sync with Go servers and vice versa, bidirectionally verified.
2. **Idiomatic Go API.** Channels for events, explicit transactions, `error` returns.
3. **Pure Go.** No CGO. `gomobile bind` works for iOS/Android.
4. **Pluggable persistence** with `modernc.org/sqlite` reference implementation.
5. **Performance within 2× of [yrs](https://github.com/y-crdt/y-crdt)** on `dmonad/crdt-benchmarks` B1-B4 — currently within ~1.1× on B4. See [BENCHMARKS.md](BENCHMARKS.md).

## Non-goals

- C-FFI surface. [yrs](https://github.com/y-crdt/y-crdt) already provides this; ygo's unique value is pure-Go native binaries.
- Drop-in replacement for the Node.js Yjs runtime. ygo is the Go port; use `yjs` itself if you want a JavaScript runtime.
- Loro, Automerge, RGA, or other CRDT designs. ygo implements the Yjs wire format, period.

## Wire compatibility

The single most-important guarantee of this project is byte-level wire compatibility with `yjs@13.x`. This is enforced by **109 cross-language fixture scenarios**:

- **29 V1 forward fixtures** (`testdata/yjs-updates.json`) — JS Yjs encodes via `Y.encodeStateAsUpdate`, Go decodes and applies, state matches.
- **32 V2 forward fixtures** (`testdata/yjs-update-v2-fixtures.json`) — same with `Y.encodeStateAsUpdateV2`.
- **48 reverse fixtures** (`testdata/go-updates.json` + `go-update-v2-fixtures.json`) — Go encodes via `EncodeStateAsUpdate` / `EncodeStateAsUpdateV2`, JS Yjs decodes via `Y.applyUpdate` / `Y.applyUpdateV2`, state matches.

The fixtures regenerate from pinned `yjs@13.6.20` + `lib0@0.2.93` + `y-protocols@1.0.6` on every CI run; `git diff --exit-code testdata/` catches byte-level regressions.

## How is this different from Hocuspocus / y-websocket / y-leveldb?

| Project | Runtime | What it provides | Relationship to ygo |
|---|---|---|---|
| `yjs` (npm) | Node / browser | The reference CRDT implementation | ygo's wire-format target |
| `y-websocket` | Node | Reference WebSocket server | ygo's `cmd/ygo-server` is a Go-native equivalent |
| `Hocuspocus` | Node | Production WebSocket server with auth, persistence, extensions | ygo's `cmd/ygo-server` speaks the same envelope (Auth/Stateless/Close/SyncStatus) |
| `yrs` | Rust | Reference Rust port | ygo's executable spec for porting decisions |
| `y-leveldb`, `y-indexeddb` | Node / browser | Persistence backends | ygo's `persist/sqlite` is a Go-native equivalent |
| **ygo** | **Go** | **CRDT engine + WS server + persistence in one monorepo, pure-Go for native mobile** | **This project** |

If you have an existing Yjs deployment and want to move the server side to Go (no Node runtime, single static binary, native iOS/Android via gomobile) — ygo is the path. If you're starting fresh and your team is comfortable with Node, Hocuspocus is the mature choice.

## Benchmarks

See [BENCHMARKS.md](BENCHMARKS.md) for the full table. Highlights from B4 (260,000-edit real-world LaTeX paper trace):

| Metric | Value |
|---|---|
| Apply 259,778 edits | 10.5 s (~0.041 ms / edit) |
| V1 doc size | 1.97 MB |
| V2 doc size | 227 KB (8.7× smaller) |
| Encode V1 | 7.7 ms |
| Encode V2 | 73 ms |
| Parse V1 | 68 ms |
| Parse V2 | 61 ms |

Hardware: Apple M3, Go 1.26. yrs's published B4 numbers on similar hardware are sub-10 s; ygo is within ~1.1× — meets DESIGN.md's "within 2× of yrs" goal.

## Roadmap

Towards v1.0: Undo manager · Snapshots · Subdocs · GC merging · Y.Array.move · commit-time block squash · external security audit · documentation site.

Per-layer port notes live in [docs/yrs-port-notes/](docs/yrs-port-notes/). Items intentionally deferred or partial are tracked in [docs/tech-debt.md](docs/tech-debt.md). Detailed design decisions in [DESIGN.md](DESIGN.md).

## Documentation

- [DESIGN.md](DESIGN.md) — project design document
- [BENCHMARKS.md](BENCHMARKS.md) — performance baseline + B1-B4 methodology
- [docs/yrs-port-notes/](docs/yrs-port-notes/) — per-layer ports notes describing how each yrs subsystem maps to Go
- [docs/tech-debt.md](docs/tech-debt.md) — deferred work and known limitations
- [CONTRIBUTING.md](CONTRIBUTING.md) — DCO sign-off, no CLA

## License

MIT. See [LICENSE](LICENSE).

## Acknowledgements

- [Kevin Jahns (dmonad)](https://github.com/dmonad) for [Yjs](https://github.com/yjs/yjs) and the YATA algorithm.
- [Bartosz Sypytkowski](https://www.bartoszsypytkowski.com/) for [yrs](https://github.com/y-crdt/y-crdt) and the [architecture deep dive](https://www.bartoszsypytkowski.com/yrs-architecture/).
