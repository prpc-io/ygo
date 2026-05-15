# Tech debt

> Items intentionally deferred or acknowledged as incomplete during initial implementation. Each entry says what, why, when to address, and where in the source to find the relevant code or upstream reference. Update as items are resolved or new ones are accumulated.

## Block layer

### Item.Splice not implemented

- **Where:** `internal/block/item.go` (no method yet).
- **What:** `Item.Splice(offset uint64, kind OffsetKind) *Item` — splits an item mid-clock-range and returns the new right half with proper origin/right_origin propagation and linked-list relinking.
- **Why deferred:** requires content slicing semantics + ID arithmetic + linked-list pointer rewriting. Without the block store there is no realistic place to register the returned right half, so splice in isolation is hard to test meaningfully.
- **When to address:** after `BlockStore` lands.
- **Reference:** [yrs/src/block.rs:516-560](https://github.com/y-crdt/y-crdt/blob/main/yrs/src/block.rs) `ItemPtr::splice`. Distill into Go after store skeleton.

### Item.Integrate / try_squash not implemented

- **Where:** `internal/block/item.go` (no method yet).
- **What:** the YATA conflict-resolution loop that places a remote item into the local list, plus `try_squash` that merges adjacent same-client items at commit.
- **Why deferred:** depends on store + transaction layers (parent.map walking, `txn.delete` on map-key tombstone, `txn.merge_blocks` at commit).
- **When to address:** after Transaction lifecycle is in place.
- **Reference:** `docs/yrs-port-notes/block.md` "Edge cases" → "Conflict resolution algorithm" + [yrs/src/block.rs:619-684](https://github.com/y-crdt/y-crdt/blob/main/yrs/src/block.rs). **Port byte-for-byte from yrs, do not reconstruct from prose.**

### Surrogate-pair split returns invalid UTF-16

- **Where:** `internal/block/content.go` `Content.Str` (Go `string`, currently no surrogate handling).
- **What:** when a String item splits mid-surrogate-pair (a 4-byte UTF-8 character whose UTF-16 form is a high+low pair), each half should be replaced with U+FFFD per JS Yjs behaviour. yrs has commented-out code for this; we currently do nothing.
- **Why deferred:** the failure mode only materializes once the Text shared type exposes splittable strings. Splittable string content right now stores plain Go strings; switch to `[]uint16` storage with the Text type lands and fix this then.
- **When to address:** Text shared-type implementation (Days 25-28 per ROADMAP).
- **Reference:** yrs/src/block.rs:1940-1948 (commented out — we do NOT want the no-op) and `yjs/src/structs/ContentString.js` for the JS behaviour we DO want.

### Wire info bit 4 reserved, no decoder check

- **Where:** `internal/block/flags.go` (constants only) and the future update decoder.
- **What:** bit 4 (`0b0001_0000`) of the Item info byte is currently unused — content kinds max at 11 (`0b1011`), all presence flags live in bits 5-7. If a future Yjs version starts using bit 4, our decoder must NOT silently mask it.
- **Why deferred:** the V1 decoder doesn't exist yet.
- **When to address:** when implementing the V1 decoder.
- **Action item at that time:** assert `info & 0b0001_0000 == 0` and return a versioning error if it's set; carry the failure into a fixture-based regression test.

### ContentString storage layout not benchmarked

- **Where:** `internal/block/content.go` `Content.Str string`.
- **What:** strings stored as Go `string` (UTF-8). Wire offsets are UTF-16. Slicing by UTF-16 offset on UTF-8 storage is O(N) per access. Two viable layouts for hot paths: `[]uint16` (memory cost, zero-cost slicing) or `string` plus a precomputed `[]uint16Index→byteIndex` table for blocks past N elements.
- **Why deferred:** the right answer depends on B3 (text-heavy) benchmark numbers. Premature optimization without measurements is worse than the simple form.
- **When to address:** after Text type is implemented and B3 from `dmonad/crdt-benchmarks` ports.
- **Action item at that time:** measure both layouts on B3, pick the winner, update `docs/yrs-port-notes/block.md`.

### Any type is a placeholder

- **Where:** `internal/block/stubs.go` `type Any = any`.
- **What:** yrs's `Any` is a tagged union (Bool, Number, BigInt, String, Buffer, Array, Map, Null, Undefined). Wire encoding requires deterministic serialization of each variant; an opaque `any` doesn't give us that.
- **Why deferred:** the encoder layer is what enforces wire compat. The current block layer only constructs/inspects Any values; it doesn't serialize them.
- **When to address:** when implementing the V1 update encoder.
- **Reference:** `yrs/src/any.rs`. Port into a proper Go tagged union and replace the alias.

### Forward-dependency stubs are empty types

- **Where:** `internal/block/stubs.go` (`Branch`, `Move`, `Doc`).
- **What:** placeholder `type X struct{}` definitions so the block package compiles.
- **Why deferred:** by design — the block layer references these only through pointers and never inspects their internals. Real definitions land with their owning layers.
- **When to address:** `Branch` with the types layer; `Doc` with the doc layer; `Move` post-MVP per ROADMAP.

### ID and Item width: uint64 vs yrs u32

- **Where:** `internal/block/id.go` `ID.Clock uint64`; `internal/block/item.go` `Item.Len uint64`.
- **What:** yrs uses `u32` for clock and len; we use `uint64`. Costs 4 bytes per ID and per Item, accepts wire values up to 2^64-1.
- **Why intentional:** lib0 varuints can carry values up to 64 bits. Yrs would error on a clock above 2^32; we accept it. This is a defensive widening, not a bug.
- **When to revisit:** if memory profiling shows ID storage dominating allocations on large docs. Unlikely.

## Open questions captured but not resolved

The following are flagged in `docs/yrs-port-notes/block.md` § "Open questions" — re-read at the time the relevant code is touched:

1. **`BlockRange::last_id` off-by-one wart in yrs** (`clock + len` vs `Item::last_id`'s `clock + len - 1`). Affects skip blocks / delete set encoding; verify against Yjs wire format when porting the encoder.
2. **`ITEM_FLAG_MARKED` "not used atm"** — kept as a constant for bit-position equivalence with future Yjs versions; before declaring it dead, grep yrs for usage.
3. **`small-client` feature in yrs** (32-bit ClientID) — we target 53-bit ClientIDs always; ignore this code path.
4. **`Item::PartialEq` semantics** — Go `EqualByID` matches by ID only, mirroring how yrs's derived equality reduces through `ItemPtr::PartialEq`. If full structural equality (content + flags + neighbours) is needed for tests, add a separate `EqualDeep` helper rather than overloading `EqualByID`.
5. **Format-attribute integration is a stub in yrs** (`@todo searchmarker`). For rich text we may need to consult upstream Yjs directly rather than trust yrs's path.

## How this file evolves

- Resolve an item: delete the entry. The git history preserves it. Do not strike-through; keep the file short.
- New deferral: add an entry. Always include the four headings (Where / What / Why / When to address) plus a Reference if upstream code or docs exist.
- A deferral that ages past 6 months without resolution becomes a candidate for either resolving or formally moving to "out of scope" status in DESIGN.md.
