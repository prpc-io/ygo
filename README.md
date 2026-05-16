# ygo

Pure-Go port of [Yjs](https://github.com/yjs/yjs), the CRDT framework for collaborative applications.

## Status

**Pre-alpha.** Not yet usable as a library. Public API will change without notice. The CRDT engine works end-to-end and round-trips with JS Yjs for the implemented surface.

| Layer | Status |
|---|---|
| `internal/lib0` varint encoding | done; verified byte-equivalent vs JS lib0 (40 fixtures) |
| `internal/block` (Item, Content, Branch, Splice, Integrate-YATA, TrySquash, Repair) | done; full YATA conflict resolution |
| `internal/store` (BlockStore, ItemSlice, Materialize) | done |
| `internal/doc` (Doc, Transaction, TransactionMut) | done; lock semantics + root-branch registry |
| `internal/encoding` (StateVector, IdSet, Update encode/decode/apply, Pending buffer) | done; JS Yjs → Go cross-language proven by 25 fixture scenarios (Map + Array + Text); pending buffer queues out-of-order items and drains automatically on subsequent applies |
| `internal/utf16` (UTF-16 length / byte-offset / surrogate-aware split) | done |
| `internal/types/Map` (Set / Get / Delete / Has / Len / Range / Clear + SetMap / SetArray / SetText) | done; nested-type construction supported |
| `internal/types/Array` (Insert / InsertRange / Push / Delete / Get / Len / Range / ToSlice + InsertMap / InsertArray / InsertText) | done; nested-type construction supported |
| `internal/types/Text` (Insert / Delete / String / Length + InsertWithAttributes / Format / InsertEmbed / Range / ToDelta / ApplyDelta) | done; full rich-text + Quill delta batch API |
| Nested-type construction (Map-in-Map, Array-in-Map, etc., to arbitrary depth) | done; ContentType wire format + Repair ParentID resolution + pending-queue retry |
| `internal/types/Xml*` (XmlFragment, XmlElement, XmlText) | done; ProseMirror/Tiptap/BlockNote unblocked. XmlHook (legacy) deferred. |
| Persistence (`Store` interface + `modernc.org/sqlite` reference impl) | done; append-only update log, Flush compaction, LoadDoc / GetStateVector / GetDiff helpers; pure-Go (no CGO) |
| y-sync protocol (`internal/sync`) | done; full Hocuspocus message subset (Sync + Awareness + QueryAwareness + Auth + Stateless + BroadcastStateless + Close + SyncStatus); per-document Auth permission scoping deferred ([tech-debt](docs/tech-debt.md)) |
| Awareness (`internal/awareness`) | done; LWW presence map, JSON wire payload per y-protocols, self-eviction defense, SweepOutdated; cross-language JS y-protocols fixture deferred ([tech-debt](docs/tech-debt.md)) |
| `server/` (WebSocket sync server) | done; `http.Handler` mount-anywhere shape, per-doc broadcaster, persists every applied update to optional `persist.Store`, awareness disconnect tombstones |
| `cmd/ygo-server` (Hocuspocus-compat binary) | done; stand-alone WS server with optional sqlite persistence via `-store` flag |
| `gomobile/` (bytes-only subset for iOS/Android) | done; bindable `Doc` + `Awareness` wrappers with bytes-in/bytes-out methods only; pure-Go (no CGO); `gomobile bind` toolchain verification deferred ([tech-debt](docs/tech-debt.md)) |
| `gomobile bind` build target | not started |
| Go → JS reverse-direction wire fixture | done; `cmd/gen-go-fixtures` captures 21 V1 + 21 V2 scenarios (Map / Array / Text / XmlFragment); `testdata/gen/validate-go-fixtures.mjs` proves `Y.applyUpdate` / `Y.applyUpdateV2` accept Go-encoded bytes. All 42 pass; CI fixtures job re-runs on every push |
| V2 update encoding | done; lib0 RLE primitives + column encoder/decoder + `Update.EncodeV2` / `Update.DecodeV2` + public `ygo.{EncodeStateAsUpdateV2,EncodeDiffV2,ApplyUpdateV2}`; JS Yjs → Go V2 direction proven by 29 cross-language fixture scenarios (Map + Array + Text + RLE-flexing) against `yjs@13.6.20` `Y.encodeStateAsUpdateV2` |
| Snapshots / undo manager / sub-documents | not started |
| dmonad/crdt-benchmarks B1-B4 port | done; `benchmarks/` package implements B1.1-B1.11, B2.1-B2.4, B3.1/3/4 (B3.2 blocked on Any-TLV object support — tech-debt) and B4 (260k-edit real-world LaTeX trace). Baseline numbers in [BENCHMARKS.md](BENCHMARKS.md). V2 wire 6-8× smaller than V1 on RLE-friendly workloads. After search markers landed, B4 op-throughput is within ~1.1× of yrs's published numbers (DESIGN.md "within 2×" target met) |
| Array / Text search-marker cache | done; per-branch bounded-LRU cache (`internal/block/search_marker.go`) cuts B4 from 84 s → 10.5 s (8× speedup). Random-position workloads gain 30-35% |

Roadmap and per-layer port notes live in [docs/yrs-port-notes/](docs/yrs-port-notes/). Items intentionally deferred or partial are tracked in [docs/tech-debt.md](docs/tech-debt.md).

## Goals

1. **Binary protocol compatibility** with [yjs](https://github.com/yjs/yjs) v13.x V1 update format. Byte-for-byte. JS clients sync with Go servers and vice versa.
2. **Idiomatic Go API.** Channels for events, explicit transactions, `error` returns.
3. **Pure Go.** No CGO. `gomobile bind` works for iOS/Android.
4. **Pluggable persistence** with `modernc.org/sqlite` reference implementation.
5. **Performance within 2x** of [yrs](https://github.com/y-crdt/y-crdt) on `dmonad/crdt-benchmarks` B1-B4. See [BENCHMARKS.md](BENCHMARKS.md) for current baseline.

## Non-goals

- C-FFI surface. yrs already provides this.
- Drop-in replacement for Node.js Yjs runtime. We are the Go port; use `yjs` if you want JS.
- Loro / Automerge / RGA. ygo is Yjs-format-only.

## Documentation

See [DESIGN.md](DESIGN.md) for the project design document.

## License

MIT. See [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Sign-off required (Developer Certificate of Origin). No CLA.

## Acknowledgements

- [Kevin Jahns (dmonad)](https://github.com/dmonad) for [Yjs](https://github.com/yjs/yjs) and the YATA algorithm.
- [Bartosz Sypytkowski](https://www.bartoszsypytkowski.com/) for [yrs](https://github.com/y-crdt/y-crdt) and the [architecture deep dive](https://www.bartoszsypytkowski.com/yrs-architecture/).
