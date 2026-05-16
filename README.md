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
| `internal/types/Map` (Set / Get / Delete / Has / Len / Range / Clear) | done |
| `internal/types/Array` (Insert / InsertRange / Push / Delete / Get / Len / Range / ToSlice) | done |
| `internal/types/Text` plain-text (Insert / Delete / String / Length) | done; rich-text formatting deferred ([tech-debt](docs/tech-debt.md)) |
| `internal/types/Xml*` | not started |
| Persistence (`Store` interface + `modernc.org/sqlite` reference impl) | done; append-only update log, Flush compaction, LoadDoc / GetStateVector / GetDiff helpers; pure-Go (no CGO) |
| y-sync protocol (WebSocket framing) | not started |
| Awareness (`internal/awareness`) | done; LWW presence map, JSON wire payload per y-protocols, self-eviction defense, SweepOutdated; cross-language JS y-protocols fixture deferred ([tech-debt](docs/tech-debt.md)) |
| `cmd/ygo-server` (Hocuspocus-compat) | not started |
| `gomobile bind` build target | not started |
| Go → JS reverse-direction wire fixture | not started; tracked in [docs/tech-debt.md](docs/tech-debt.md) |
| V2 update encoding | not started |
| Snapshots / undo manager / sub-documents | not started |

Roadmap and per-layer port notes live in [docs/yrs-port-notes/](docs/yrs-port-notes/). Items intentionally deferred or partial are tracked in [docs/tech-debt.md](docs/tech-debt.md).

## Goals

1. **Binary protocol compatibility** with [yjs](https://github.com/yjs/yjs) v13.x V1 update format. Byte-for-byte. JS clients sync with Go servers and vice versa.
2. **Idiomatic Go API.** Channels for events, explicit transactions, `error` returns.
3. **Pure Go.** No CGO. `gomobile bind` works for iOS/Android.
4. **Pluggable persistence** with `modernc.org/sqlite` reference implementation.
5. **Performance within 2x** of [yrs](https://github.com/y-crdt/y-crdt) on `dmonad/crdt-benchmarks` B1-B4.

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
