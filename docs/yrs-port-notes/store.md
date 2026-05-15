# yrs port notes: block store layer

> Source: yrs commit 639db2038fa44d09f628a2650fd900e3b109ad1e, files: yrs/src/block_store.rs, yrs/src/store.rs (block-store-relevant surface only), yrs/src/slice.rs, yrs/src/id_set.rs (DeleteSet trait + IdSet public surface), yrs/src/block.rs (BlockCell, GC, BlockRange, ItemPtr — already documented in block.md but re-cited where the store contract depends on them).

## Public Rust types

### `ClientBlockList` (`block_store.rs:13-16`)

```rust
#[derive(PartialEq, Default)]
pub(crate) struct ClientBlockList {
    list: Vec<BlockCell>, // dense, sorted-by-clock list of all blocks created by one client
}
```

Crate-private (`pub(crate)`). The only field is the backing `Vec<BlockCell>`. Construction is via `Default::default()` (empty vec). Indexing operators (`Index`/`IndexMut`) are implemented (`block_store.rs:254-266`).

### `BlockCell` (`block.rs:180-273`, recap)

```rust
pub(crate) enum BlockCell {
    GC(GC),               // tombstoned/garbage-collected range, content discarded
    Block(Box<Item>),     // live or tombstoned-but-content-preserved item
}
```

`Box<Item>` is the heap allocation that gives `ItemPtr(NonNull<Item>)` its pinned address. `GC` is `{ start: u32, end: u32 }` (inclusive on both ends, `block.rs:275-279`).

### `BlockStore` (`block_store.rs:288-294`)

```rust
#[derive(PartialEq, Default)]
pub(crate) struct BlockStore {
    clients: HashMap<
        ClientID,
        ClientBlockList,
        BuildHasherDefault<ClientHasher>, // identity-style hasher for u64-ish ClientIDs
    >,
}
```

Crate-private. `ClientHasher` is in `crate::utils::client_hasher` (`block_store.rs:4`); it is an identity hasher that skips the standard sip-hash pass for the `ClientID` integer.

### `Iter` / `IterMut` (`block_store.rs:296-297`)

```rust
pub(crate) type Iter<'a>    = std::collections::hash_map::Iter<'a, ClientID, ClientBlockList>;
pub(crate) type IterMut<'a> = std::collections::hash_map::IterMut<'a, ClientID, ClientBlockList>;
```

Iteration order is `HashMap` order — non-deterministic. Code paths that need a deterministic order (`write_blocks_from`, `write_blocks_to`, `Blocks` iterator) explicitly sort entries afterwards (see `store.rs:174`, `store.rs:221`, `block_store.rs:496` "sorting to return higher client ids first").

### `Blocks<'a>` (`block_store.rs:486-525`)

```rust
pub(crate) struct Blocks<'a> {
    current_client: std::vec::IntoIter<(&'a ClientID, &'a ClientBlockList)>,
    current_block: Option<ClientBlockListIter<'a>>,
}
```

A flat iterator over all `&BlockCell` in the store, **iterating clients in descending `ClientID` order** (`block_store.rs:496`: `client_blocks.sort_by(|a, b| b.0.cmp(a.0));`). This ordering matters for the conflict-resolution algorithm and for wire compatibility with JS Yjs (higher client IDs win ties, encoded first).

### `BlockSlice` / `ItemSlice` / `GCSlice` (`slice.rs:7-85`, `slice.rs:93-279`, `slice.rs:281-326`)

```rust
pub(crate) enum BlockSlice {
    Item(ItemSlice),
    GC(GCSlice),
}

pub(crate) struct ItemSlice {
    pub ptr: ItemPtr, // pinned pointer to the underlying Item
    pub start: u32,   // inclusive offset within the block (0..=ptr.len()-1)
    pub end:   u32,   // inclusive offset within the block (start..=ptr.len()-1)
}

pub(crate) struct GCSlice {
    pub start: u32,
    pub end:   u32,
}
```

Both `start` and `end` are **inclusive** (`slice.rs:101-103` debug-asserts `start <= end`; `slice.rs:115` defines `len() = end - start + 1`). `ItemSlice::clock_start = ptr.id.clock + start`, `clock_end = ptr.id.clock + end` (`slice.rs:105-111`).

### `IdSet` / `DeleteSet` trait (`id_set.rs:121-135`, `id_set.rs:420-429`)

```rust
#[derive(Default, Clone, PartialEq, Eq)]
pub struct IdSet(pub(crate) IdMapInner<()>); // BTreeMap<ClientID, IdRange> internally

pub(crate) trait DeleteSet {
    fn from_store(store: &BlockStore) -> Self;
    fn try_squash_with(&mut self, store: &mut Store);
    fn blocks(&self) -> Blocks<'_>; // ds-side block iterator
}
impl DeleteSet for IdSet { ... }
```

`IdSet` uses a `BTreeMap` (deterministic order) — distinct from `BlockStore::clients` which uses `HashMap`. Per-client value is `IdRange = IdRanges<()>` (a sorted `SmallVec<(Range<u32>, ())>` with merge-on-insert canonicalisation, `id_set.rs:34-40`). `Range<u32>` is encoded as `(clock, len)` pairs on the wire (`id_set.rs:19-32`).

`DeleteSet::from_store` walks every block in every client, and for each `block.is_deleted()` cell pushes `(clock_start, clock_end + 1)` half-open ranges into the per-client `IdRange` (`id_set.rs:432-448`). Note the conversion: `BlockCell::clock_range()` returns inclusive `(start, end)` — the delete-set encoding is half-open `[start, end+1)`.

## Internal invariants

1. **Per-client clock density.** `ClientBlockList::list` is contiguous in clock space: for each client `c`, the union of `[clock_start, clock_end]` for cells in `clients[c].list` equals exactly `[0, clock())`. There are no gaps and no overlaps. `ClientBlockList::clock()` (`block_store.rs:24-34`) computes the next free clock as `last.clock_end() + 1` — this would be wrong if gaps existed.

2. **Sorted by clock.** `clients[c].list[i].clock_start() < clients[c].list[i+1].clock_start()` for all `i`. `find_pivot` (`block_store.rs:42-68`) is a binary search that depends on this. (Insertion at `index+1` after `find_pivot` in `split_block`/`materialize` is the only sanctioned mutation.)

3. **Inclusive ranges, both sides.** `BlockCell::clock_range()` returns `(start, end)` inclusive on both ends (`block.rs:223-228`). `len = end - start + 1`. The store's `find_pivot` test is `start <= clock && clock <= end` (`block_store.rs:55-57`). Wire-format `len` field uses the same `end - start + 1`.

4. **`ItemPtr` stability.** `ItemPtr(NonNull<Item>)` (`block.rs:309-311`) is a pinned pointer. The cells in `ClientBlockList::list` use `BlockCell::Block(Box<Item>)`, so the `Item` lives on the heap and the `Box`'s inner address is stable across `Vec` resize. `ItemPtr::from(&Box<Item>)` reads the box's heap pointer (`block.rs:901-911`); growing/shifting the outer `Vec<BlockCell>` is safe because it moves the `Box`, not the `Item`.

5. **No two active blocks share an ID.** `BlockCell::PartialEq` for the `Block` variant compares only `a.id == b.id` (`block_store.rs:189`); the store treats `(client, clock)` as a primary key.

6. **`get_clock` is exclusive.** `BlockStore::get_clock(client)` returns the clock value of *the next* block to be inserted, never of an existing one (`block_store.rs:419-425`, doc comment "exclusive value"). Confirms invariant #1: the returned value is always `last.clock_end + 1`.

7. **Per-client list is created lazily but never destroyed.** `get_client_blocks_mut` uses `entry().or_insert_with(default)` (`block_store.rs:429-433`); there is no removal API. Empty `ClientBlockList`s are not constructed unless `get_client_blocks_mut` is called.

8. **`StateVector` is derived, not stored.** `get_state_vector` materialises a fresh `StateVector` from `clients.iter().map(|(c,l)| (c, l.clock()))` (`block_store.rs:357-364`); no caching. State vectors compare against `clock()` (exclusive), which is what JS Yjs's `StateVector` semantics expect.

9. **Squash preserves parent-map invariants.** `squash_left` and `squash_left_range_compaction` both rewrite the parent branch's `map[parent_sub]` entry from the right item's pointer to the left item's pointer when the right is the current map entry (`block_store.rs:201-209`, `237-245`). This keeps `Branch::map` consistent with the surviving block.

10. **GC squash is purely arithmetic.** Two adjacent GC cells squash by setting `left.end = right.end` and dropping `right` (`block_store.rs:195-197`, `229-231`). No Item bookkeeping involved.

11. **Range squash only handles GC ranges; Block-cell squashes are processed individually.** The comment at `block_store.rs:118` is explicit: "Block cells currently don't support range compaction due to the complexity of squashing Blocks." Each squashable Block pair gets its own single-element range pushed onto `squash_intervals`.

12. **`split_block` always inserts the right half at `index+1`.** `block_store.rs:437-451`: locate left-half index via `find_pivot(id.clock)`, splice via `Item::splice` which mutates the left half in place (truncates `len`) and produces a new `Box<Item>` for the right half, then `blocks.insert(index + 1, right.into())`. Invariants 1-3 are preserved because the splice is at an interior offset and the right half starts at `clock + offset`.

13. **DeleteSet half-open vs BlockCell inclusive.** `IdSet`/`IdRange` ranges are half-open `[start, end)` (`id_set.rs:439`: `deletes.insert(start..(end + 1))`). `BlockCell::clock_range()` returns inclusive `(start, end)`. Any code that crosses the boundary must apply the `+1`/`-1` correction.

## Edge cases the source handles explicitly

- **Empty list `clock()`** returns 0 (`block_store.rs:25-28`).
- **Single-element list `find_pivot`** still goes through the binary-search loop; the `right = self.list.len() - 1` underflow is impossible because `find_pivot` is only called from contexts that have already established the list is non-empty (see `get_block` -> `get_client` returns `None` for missing client, so no empty list reaches `find_pivot`). However, `find_pivot` does not itself guard against `self.list.is_empty()` — calling it on an empty list panics on `len() - 1`. The Go port must add an explicit length check or replicate the calling-side guarantee.
- **Initial probe at the right end.** `find_pivot` first checks `self[right]` — the common case ("just append at the end") returns immediately if `start == clock` (`block_store.rs:46-49`). The seeding `mid = ((clock / end) * right as u32) as usize` (`block_store.rs:51`) is an interpolation hint, not a standard binary-search midpoint. If `end == 0` this is a divide-by-zero. The loop then converges with classic `(left + right) / 2` updates. The Go port can simplify to a plain `sort.Search` since the interpolation is a micro-optimisation, not a correctness requirement.
- **`Item::splice(0)` returns `None`.** `block.rs:516-520`: a splice at offset 0 is a no-op. `BlockStore::split_block` therefore returns `None` for offset-0 calls — call sites must not assume a result.
- **`materialize` skips left-splice when slice is left-adjacent and skips right-splice when right-adjacent** (`store.rs:295-342`). If the slice already covers the whole underlying block, it returns the original `ItemPtr` without mutation.
- **`materialize` rewires `linked_by` for new (split) items.** When splicing creates a new `Item`, any weak-link entries pointing at the original are cloned to point at the new half (`store.rs:310-313`, `334-336`). Block store proper does not own `linked_by`; this lives on `Store`.
- **`get_item` returns `None` for GC cells.** `BlockStore::get_item` (`block_store.rs:386-393`) only returns `Some(ItemPtr)` for `BlockCell::Block`. Callers must treat absence as "either not found or already GC'd."
- **`get_item_clean_start` and `get_item_clean_end` return `ItemSlice` without splitting.** They just compute offsets relative to the underlying block (`block_store.rs:399-414`); the actual splice is deferred to `Store::materialize(slice)`. This is intentional — read-only paths can compute slices without mutating the store.
- **`encode_diff` writes a fresh delete set built from the current store** (`store.rs:211`: `let delete_set = IdSet::from_store(&self.blocks);`), not a stored one. The `pending_ds` field is for queued *incoming* deletes, not outgoing ones.

## Public methods we will need

For each, citing the Rust signature and the invariant relationships.

### `ClientBlockList::find_pivot(&self, clock: u32) -> Option<usize>` (`block_store.rs:42-68`)

Returns the index `i` such that `clients[c].list[i].clock_range()` contains `clock`. Returns `None` only if `clock >= list.clock()` (out of range). Maintains invariants 1-3. **Pitfall:** panics on empty list (see edge cases). The Go port should accept an empty-list precondition or check first.

### `ClientBlockList::get(&self, index: usize) -> Option<&BlockCell>` (`block_store.rs:36-38`)

Bounds-checked indexed access. Used by encoders that know an index from a prior `find_pivot`.

### `ClientBlockList::push(&mut self, cell)` and `insert(&mut self, index, cell)` (`block_store.rs:84-93`)

`push` appends; `insert` inserts at an arbitrary index (used by `split_block` and `materialize` for the right half). `push` preserves invariants 1-3 only when `cell.clock_start() == self.clock()`. Caller responsibility.

### `ClientBlockList::clock(&self) -> u32` (`block_store.rs:24-34`)

Returns next free clock for this client. Constant-time (peeks last cell). Used by state vector and append checks.

### `ClientBlockList::squash_left(&mut self, index: usize)` (`block_store.rs:224-251`)

Attempts to merge `list[index]` into `list[index-1]`. GC+GC always succeeds; Block+Block calls `Item::try_squash` (`block.rs:852`) and only merges on success. Updates parent-map references when needed (invariant 9). Used by `try_squash_with` and integration paths.

### `ClientBlockList::squash_left_range_compaction(&mut self, indices_range: RangeInclusive<usize>)` (`block_store.rs:131-217`)

Bulk squash: walks indices in reverse, accumulates squashable GC ranges and individual Block squashes into `squash_intervals`, then performs all `drain` operations in a second pass. Range parameter is inclusive on both ends. Asserts `start() <= end()`. Used by `IdSet::try_squash_with` to compact deleted regions in bulk.

### `BlockStore::contains(&self, id: &ID) -> bool` (`block_store.rs:306-312`)

`true` iff `id.clock < clients[id.client].clock()`. Note: returns `true` for *any* clock in range, including those covered by GC cells.

### `BlockStore::push_block(&mut self, block: Box<Item>)` (`block_store.rs:314-326`)

Appends an `Item` to the per-client list (creating it if absent). Caller must guarantee `block.id.clock == get_clock(block.id.client)` to preserve invariant 1.

### `BlockStore::push_gc(&mut self, gc: BlockRange)` (`block_store.rs:328-341`)

Same as `push_block` but for `BlockRange -> GC`.

### `BlockStore::get_block(&self, id: &ID) -> Option<&BlockCell>` (`block_store.rs:376-379`)

Find the cell containing `id.clock`. `None` if client absent or clock out of range.

### `BlockStore::get_item(&self, id: &ID) -> Option<ItemPtr>` (`block_store.rs:386-393`)

Returns `None` for GC cells (see edge cases). Returns a pinned `ItemPtr` otherwise.

### `BlockStore::get_item_clean_start(&self, id: &ID) -> Option<ItemSlice>` (`block_store.rs:399-403`)

Returns the slice from `id` to the end of its underlying block, *without splicing*. Equivalent to `ItemSlice::new(ptr, id.clock - ptr.id.clock, ptr.len() - 1)`.

### `BlockStore::get_item_clean_end(&self, id: &ID) -> Option<ItemSlice>` (`block_store.rs:409-414`)

Returns the slice from the beginning of `id`'s block up to and including `id`. Equivalent to `ItemSlice::new(ptr, 0, id.clock - ptr.id.clock)`.

### `BlockStore::get_clock(&self, client: &ClientID) -> u32` (`block_store.rs:419-425`)

Returns `0` for unknown clients (avoids the `Option`). Exclusive value (next free clock).

### `BlockStore::get_client_blocks_mut(&mut self, client: ClientID) -> &mut ClientBlockList` (`block_store.rs:429-433`)

Get-or-insert. The only "lazy creation" path for a client list.

### `BlockStore::split_block(&mut self, block: ItemPtr, offset: u32, encoding: OffsetKind) -> Option<ItemPtr>` (`block_store.rs:437-451`)

Splices `block` at `offset` (per `encoding`: `OffsetKind::Utf16` or `Bytes`); inserts the new right half at `index+1`. Returns the right-half pointer. `None` only when `offset == 0` (delegates to `Item::splice`).

### `BlockStore::split_block_inner(&mut self, block: ItemPtr, offset: u32) -> Option<ItemPtr>` (`block_store.rs:453-455`)

Convenience wrapper hard-coding `OffsetKind::Utf16` (used during update integration where Yjs wire compatibility forces utf-16 offsets).

### `BlockStore::get_state_vector(&self) -> StateVector` (`block_store.rs:357-364`)

Materialises a fresh `StateVector` from the per-client `clock()` values.

### `BlockStore::iter` / `iter_mut` (`block_store.rs:344-351`)

`HashMap` iteration. Non-deterministic; callers that need order sort manually.

### `Store::materialize(&mut self, slice: ItemSlice) -> ItemPtr` (`store.rs:295-342`)

Lives on `Store`, not `BlockStore`, because it needs `linked_by`. Splits the underlying block on both sides as needed so that the resulting `ItemPtr` covers exactly the slice's `[start, end]` range. Skips left-splice if `slice.adjacent_left()`, skips right-splice if `slice.adjacent_right()`. Always uses `OffsetKind::Utf16`.

### `Store::write_blocks_from(&self, sv: &StateVector, encoder: &mut E)` (`store.rs:215-243`)

Encodes diff blocks for each client where local clock > remote clock. Sorts by descending client ID before writing (invariant: wire format expects this for JS Yjs conflict semantics).

### `Store::write_blocks_to(&self, sv: &StateVector, encoder: &mut E)` (`store.rs:164-195`)

Symmetric helper used by `encode_state_from_snapshot`.

### `IdSet::from_store(store: &BlockStore) -> IdSet` (`id_set.rs:432-448`)

Builds the delete set by scanning every block; required for `encode_diff`.

## Concrete Go translation choices

### Storage shape per client: `[]BlockCell` with embedded `*Item`

**Decision: `[]BlockCell` where each `BlockCell` is a tagged struct with `*Item` for the Block variant and inline `GC` for the GC variant.** The block.md note already established that the Go `Item` lives behind a `*Item` for pointer stability. `BlockCell` itself is small (12-16 bytes); shifting the cell struct on insert is fine because the underlying `*Item` doesn't move.

Tradeoff: `[]BlockCell` shifts on `insert`, but each shift moves only the small struct, not the `Item`. This matches yrs (Rust shifts the `Box<Item>` pointer, not the `Item`).

### `BlockCell` representation in Go

```go
type BlockKind uint8

const (
    BlockKindGC   BlockKind = 0
    BlockKindItem BlockKind = 1
)

type BlockCell struct {
    Kind BlockKind
    GC   GC      // valid when Kind == BlockKindGC
    Item *Item   // valid when Kind == BlockKindItem; nil otherwise
}
```

Avoid `interface{}` — it costs an extra heap allocation on the integration path and breaks `==`. Helper methods `ClockStart()`, `ClockEnd()`, `ClockRange()`, `Len()`, `IsDeleted()`, `AsSlice()`, `AsItem()` mirror `block.rs:204-261`.

### Per-client map

Yrs uses `HashMap<ClientID, ClientBlockList, BuildHasherDefault<ClientHasher>>` — an identity hash on the `ClientID` u64. In Go: `map[ClientID]*ClientBlockList`. Go's built-in `map` already hashes integers cheaply; no custom hasher needed.

Wrap in a struct (not a bare map) so we can attach methods and keep room for future optimisation:

```go
type BlockStore struct {
    clients map[ClientID]*ClientBlockList
}
```

Use `*ClientBlockList` (not value) so `BlockStore.GetClient(c)` returning a pointer doesn't get invalidated by future map writes (Go map values are not addressable).

### `ClientBlockList`

```go
type ClientBlockList struct {
    list []BlockCell
}
```

Methods: `Clock()`, `Get(i)`, `FindPivot(clock)`, `Push(cell)`, `Insert(i, cell)`, `Len()`, `SquashLeft(i)`, `SquashLeftRangeCompaction(start, end int)`. Iteration via index loop; no need for an iterator type in Go.

### Binary-search routine

Replace yrs's hand-rolled interpolation+binary search (`block_store.rs:42-68`) with `sort.Search` from the standard library:

```go
func (l *ClientBlockList) FindPivot(clock uint64) (int, bool) {
    if len(l.list) == 0 {
        return 0, false
    }
    i := sort.Search(len(l.list), func(i int) bool {
        return l.list[i].ClockEnd() >= clock
    })
    if i == len(l.list) || l.list[i].ClockStart() > clock {
        return 0, false
    }
    return i, true
}
```

**Pitfalls:**
- Yrs returns `Option<usize>`; Go convention is `(int, bool)`.
- yrs uses inclusive ranges, so the predicate is `ClockEnd() >= clock` (matches `start <= clock <= end`). Do **not** translate to half-open semantics — wire format uses inclusive `(start, end)` with `len = end-start+1`.
- yrs does **not** guard against empty list. Our `FindPivot` should — invariant violated if we don't.
- The interpolation seeding in yrs is a micro-optimisation; `sort.Search` doesn't do this, but the cost is at most `log2(N)` comparisons and not on the hot path.

### `ClientBlockList` integrity / test stubs

yrs has no explicit integrity assertion method; invariants are maintained by construction. We should add an internal test-only verifier:

```go
func (l *ClientBlockList) checkInvariants() error {
    var nextClock uint64 = 0
    for i, c := range l.list {
        if c.ClockStart() != nextClock {
            return fmt.Errorf("gap or overlap at index %d: expected clock %d, got start %d", i, nextClock, c.ClockStart())
        }
        if c.ClockEnd() < c.ClockStart() {
            return fmt.Errorf("inverted range at index %d: [%d, %d]", i, c.ClockStart(), c.ClockEnd())
        }
        nextClock = c.ClockEnd() + 1
    }
    return nil
}
```

Call after every `Push`/`Insert`/`SquashLeft*` in tests. Production code skips it.

### Unsafe / raw-pointer handling

yrs leans on `Box<Item>` + `ItemPtr(NonNull<Item>)` for stable references; `Box::into_raw`-style escape hatches let it pass pointers around without lifetime annotations. The `unsafe impl Send/Sync for ItemPtr` (`block.rs:313-314`) sidesteps Rust's aliasing rules.

Go's `*Item` is the natural equivalent: stable address (the `Item` lives wherever `new(Item)` allocated it; the GC won't move it as long as we hold a pointer), no aliasing rules to break, native `==`. The yrs `Item::deref`/`deref_mut` (`block.rs:887-899`) become pointer dereferences in Go.

API ergonomics changes vs yrs:
- No need for `ItemPtr::from(&Box<Item>)` — pass `*Item` directly.
- No `Send`/`Sync` ceremony — Go has no built-in concurrency safety contract; document that `BlockStore` is not safe for concurrent access without external synchronisation (matches yrs's `RwLock<Store>` wrapper at `store.rs:498`).
- `Item::splice` returning `Option<Box<Item>>` becomes `(*Item, bool)` or `*Item` (nil = no split).
- `Box<Item>::into` for `BlockCell::Block` becomes `BlockCell{Kind: BlockKindItem, Item: ptr}` literal.
- The yrs `From<&mut Box<Item>>` impls (`block.rs:901-911`) for ItemPtr have no Go equivalent; replace with helper constructors.

### `ItemSlice` / `BlockSlice` / `GCSlice` translation

```go
type ItemSlice struct {
    Ptr   *Item
    Start uint64 // inclusive
    End   uint64 // inclusive (Start <= End)
}

type GCSlice struct {
    Start uint64
    End   uint64
}

type BlockSliceKind uint8
const (
    BlockSliceKindItem BlockSliceKind = 0
    BlockSliceKindGC   BlockSliceKind = 1
)

type BlockSlice struct {
    Kind BlockSliceKind
    Item ItemSlice
    GC   GCSlice
}
```

Same struct-with-discriminator pattern as `BlockCell`. `Len() = End - Start + 1`.

### `IdSet` / `DeleteSet`

`IdSet` uses `BTreeMap<ClientID, IdRange>` for deterministic iteration. Go has no built-in ordered map; either:
- `map[ClientID]*IdRange` plus a sorted `[]ClientID` cache, OR
- Use a third-party tree (e.g. google/btree), OR
- Sort on demand inside `Encode` (acceptable: encode is called once per outgoing update).

Recommend **sort on demand** — keeps the hot read paths plain map lookups and only pays for sorting at encode time, matching the access pattern.

`IdRange` is a sorted `SmallVec<(Range<u32>, ())>`. In Go: `[]ClockRange` with merge-on-insert. The `()` value type is unused — we don't need the `IdMapInner<V>` generic; just `[]ClockRange`.

```go
type ClockRange struct {
    Start uint64 // inclusive
    End   uint64 // exclusive (matches yrs Range<u32> semantics)
}
```

Critical: `IdRange` ranges are **half-open** (`id_set.rs:439`); `BlockCell::clock_range()` is **inclusive**. Document this on every conversion site.

### Squash semantics

Port `squash_left_range_compaction` faithfully — the two-pass design (collect intervals in reverse, then drain in bulk) is deliberate: it avoids index invalidation. In Go, the equivalent is:

```go
func (l *ClientBlockList) SquashLeftRangeCompaction(start, end int) {
    // walk indices in reverse
    // accumulate []squashRange
    // second pass: drain l.list[start:end+1] for each range
}
```

`Vec::drain(start..=end)` becomes `l.list = append(l.list[:start], l.list[end+1:]...)`. Watch element-shift cost; consider building the new slice once at the end instead of multiple draining ops.

## Forward dependencies

- **`crate::block`** (already ported): `BlockCell`, `Item`, `GC`, `BlockRange`, `ItemPtr`, `ID`, `ClientID`. Store layer assumes `Item::splice` and `Item::try_squash` exist (we have stubs, need to fill them in to make squash/split work end-to-end).
- **`crate::types::Branch` / `BranchPtr`** (`store.rs:3`, used by `materialize` for `linked_by` and by `squash` for parent-map rewiring `block_store.rs:201-209`): the store reaches into `branch.map` by `parent_sub` key. We have `Branch` stubs in `internal/block`; flesh out `Branch.Map` access for the squash path.
- **`crate::slice::ItemSlice`**: the slice type is closely coupled with the store — port it alongside.
- **`crate::types::TypePtr`** (used in `squash_left` to resolve right item's parent for map rewiring): need `TypePtr::Branch` discriminator at minimum.
- **`crate::id_set::IdSet`** + `DeleteSet` trait: store does **not** own the delete set during normal operation (it's a transaction-level concept), but `Store::encode_diff` builds one from the block store, and `IdSet::try_squash_with` mutates the store's `ClientBlockList`s. Port `IdSet` after the store but before integration.
- **`crate::transaction::TransactionMut`**: integrate/splice paths route through it, but the **block store itself does not depend on transactions** — it exposes plain `&mut self` methods. The transaction layer wraps these. Good news for porting order.
- **`crate::updates::encoder::Encoder`**: `Store::write_blocks_from` and `Store::encode_diff` use it. Port encoder interface alongside or stub it for now.
- **`crate::doc::Options`**, **`StateVector`**, **`OffsetKind`**: needed by `Store::new`, `BlockStore::get_state_vector`, `split_block`. `OffsetKind` is already a stub in our block layer.
- **`linked_by: HashMap<ItemPtr, HashSet<BranchPtr>>`** (`store.rs:62`): weak-link bookkeeping read+written by `materialize`. The block store does not own it; `Store` does. Defer until weak-link types land.

## Open questions

1. **`find_pivot` empty-list panic.** yrs's `find_pivot` (`block_store.rs:42-68`) does `self.list.len() - 1` without an empty-list check. Is this an intentional precondition documented elsewhere, or a latent bug only avoided by happenstance of the call sites? Our Go port should add the check defensively.

2. **`find_pivot` interpolation correctness when `end == 0`.** `mid = ((clock / end) * right as u32) as usize` (`block_store.rs:51`) divides by `end`. If a client's first block has `clock_end == 0` (a single insertion at clock 0), the seeding divides by zero. Skipped in our Go port via `sort.Search`.

3. **`BlockStore` does not provide a way to remove a client or shrink a `ClientBlockList`.** Confirmed by code search — only `push`, `insert`, `drain` (via squash). Is this intentional permanence or is removal handled at a higher layer?

4. **Wire format and HashMap order.** `BlockStore::clients` is a `HashMap` (non-deterministic). All wire-emitting paths sort by descending client ID before emitting. Confirm no other code path iterates the raw HashMap while emitting bytes — if there is, the wire will diverge from JS Yjs across Go runs. Our Go port should centralise sorted emission.

5. **`ItemSlice.end` for a one-clock block.** `ItemSlice::adjacent_right()` is `self.end == self.ptr.len() - 1`. For `len=1`, that's `self.end == 0` — combined with `start=0`, slice covers the single clock. Confirmed consistent in `slice.rs:147-161`. No bug, but worth a unit test.

6. **`split_block` returning `None` for offset 0.** `BlockStore::split_block` returns the right half as `Option<ItemPtr>`. `Item::splice` returns `None` when `offset == 0`. If our Go port returns `(*Item, bool)` and a caller ignores the bool, we silently corrupt. Add a debug-assert.

7. **`materialize` `OffsetKind` is hard-coded to `Utf16`** (`store.rs:309`, `store.rs:332`). Our Go port should mirror this — never accept an `OffsetKind` parameter on `Materialize`.

8. **The `//todo: txn merge blocks insert?` comments at `store.rs:316` and `store.rs:338`** suggest yrs may not be tracking newly-materialised blocks in `txn.merge_blocks` for later squash. This could be a known omission — worth checking the `materialize` test coverage.

9. **`BlockStore::contains` returns `true` for GC'd ranges.** A caller checking `if !store.contains(id) { fetch_remote(id) }` will not re-fetch GC'd ranges. JS Yjs behaviour: GC ranges are still "known" — confirmed intentional. Document for future readers.

10. **`Blocks::new` sorts by descending `ClientID`** (`block_store.rs:496`) but the `BlockStore::iter`/`iter_mut` exposed publicly do not. If we expose deterministic iteration in the Go port for everything, we'd diverge from yrs's API surface — but our internal callers all sort anyway. Decide: expose only sorted iterators in Go (simpler, slower) or both (matches yrs).
