# yrs port notes

Per-layer working notes generated incrementally as each layer of the Rust [`yrs`](https://github.com/y-crdt/y-crdt) source is studied and ported into Go. Each note is the durable memory for that layer: what struct shapes look like in Rust, what invariants hold, what edge cases the implementation handles, and what would be lost if naive Go translation were attempted.

## Workflow per layer

1. Read the corresponding `yrs/src/<file>.rs` — typically via a focused subagent fetch rather than loading the whole tree into the main context.
2. Distill into a concise note (target: 500–1500 words) with:
   - Public types and their Rust signatures.
   - Internal invariants (origin pointer immutability, clock-per-element, etc.).
   - Edge cases the source explicitly handles (split mid-Item, pending updates buffered on causal gap, GC retaining shell, …).
   - Concrete Go translation choices we will make, with rationale.
3. Commit the note before writing any Go code for that layer.
4. Refine the note as integration questions surface during implementation.

## Layer index

| Layer | yrs source | Note file | Status |
|---|---|---|---|
| Encoding primitives | `yrs/src/encoding/` | n/a — shipped as `internal/lib0/`, verified against 40 JS fixtures | done |
| Block / Item | `yrs/src/block.rs` | [`block.md`](block.md) | done; shipped as `internal/block/` |
| Block store | `yrs/src/store.rs` | [`store.md`](store.md) | done; shipped as `internal/store/` |
| Item.Integrate (YATA) + try_squash | `yrs/src/block.rs:562-846` | [`integrate.md`](integrate.md) | done; shipped in `internal/block/integrate.go` |
| Transaction lifecycle | `yrs/src/transaction.rs` | [`transaction.md`](transaction.md) | done; shipped as `internal/doc/` |
| Map shared type | `yrs/src/types/map.rs` | [`types-map.md`](types-map.md) | done; shipped as `internal/types/` |
| Array shared type | `yrs/src/types/array.rs` | [`types-array.md`](types-array.md) | done; shipped as `internal/types/Array`; 8 cross-language fixture scenarios passing |
| Text shared type (plain-text) | `yrs/src/types/text.rs` | [`types-text.md`](types-text.md) | done; shipped as `internal/types/Text`; 9 cross-language fixture scenarios passing including non-BMP / surrogate-split U+FFFD |
| Update encoding (V1) | `yrs/src/update.rs`, encoder/decoder | [`update-v1.md`](update-v1.md) | done; JS Yjs → Go direction proven by 8 fixture scenarios; Go → JS reverse direction tracked in tech-debt |
| y-sync protocol | `y-protocols/sync.js` + Hocuspocus envelope | [`protocol-sync.md`](protocol-sync.md) | done; reference for implementation |
| Awareness | `yrs/src/sync/awareness.rs` + `y-protocols/awareness.js` | [`awareness.md`](awareness.md) | done; reference for implementation |
| Nested-type construction | `yrs/src/block.rs:1368-1431` + `yjs/src/structs/ContentType.js` | [`nested-types.md`](nested-types.md) | done; reference for implementation |

## Why per-layer rather than monolithic

The full `yrs` source is on the order of tens of thousands of lines and exceeds what fits comfortably in any single working session. Reading it all upfront is also inefficient: details forgotten between reading and porting are details that have to be re-read anyway. Per-layer notes localize the cost to the moment of actual need.

The notes are not a replacement for reading the source when implementing tricky logic (the YATA integration loop, V2 column encoding). They are reference material that lets a future session skip re-discovering the layer's structure, naming, and assumed invariants.
