# ygo

Pure-Go port of [Yjs](https://github.com/yjs/yjs), the CRDT framework for collaborative applications.

## Status

**Pre-alpha.** Not yet usable. Public API will change without notice.

| Component | Status |
|---|---|
| `internal/lib0` varint encoding | scaffold |
| `Doc` core | not started |
| `Map`, `Array`, `Text` shared types | not started |
| State Vector + V1 update encoding | not started |
| y-sync protocol | not started |
| Awareness CRDT | not started |
| `Store` interface + sqlite reference impl | not started |
| `cmd/ygo-server` (Hocuspocus-compat) | not started |
| `gomobile bind` build target | not started |

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
