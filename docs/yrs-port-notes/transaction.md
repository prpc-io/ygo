# yrs port notes: transaction layer

> Source: `yrs/src/transaction.rs`, `yrs/src/transact.rs` (the `Transact` and `AsyncTransact` traits and their `Doc` impls — what `doc.rs` calls into), plus narrow references to `yrs/src/store.rs` (`Store::pending`, `Store::pending_ds`, `StoreEvents`, `set_subdoc_data`), `yrs/src/block.rs` (`Item::integrate` populating `subdocs.added`/`loaded`), and `yrs/src/doc.rs` (only `Doc::load` and `Doc::destroy` for subdoc bookkeeping). The bulk of the `Doc` struct is intentionally out of scope per the porting brief.

## Public Rust types

### `ReadTxn` trait (`transaction.rs:30-288`)

```rust
pub trait ReadTxn: Sized {
    fn store(&self) -> &Store;
    fn state_vector(&self) -> StateVector { ... }
    fn snapshot(&self) -> Snapshot { ... }
    fn encode_state_from_snapshot<E: Encoder>(&self, snapshot: &Snapshot, encoder: &mut E) -> Result<(), Error>;
    fn encode_diff<E: Encoder>(&self, sv: &StateVector, encoder: &mut E);
    fn encode_diff_v1(&self, sv: &StateVector) -> Vec<u8>;
    fn encode_diff_v2(&self, sv: &StateVector) -> Vec<u8>;
    fn encode_state_as_update<E: Encoder>(&self, sv: &StateVector, encoder: &mut E);
    fn encode_state_as_update_v1(&self, sv: &StateVector) -> Vec<u8>;
    fn encode_state_as_update_v2(&self, sv: &StateVector) -> Vec<u8>;
    fn root_refs(&self) -> RootRefs;
    fn subdoc_guids(&self) -> SubdocGuids;
    fn subdocs(&self) -> SubdocsIter;
    fn get_text<N: Into<Arc<str>>>(&self, name: N) -> Option<TextRef>;
    fn get_array<N: Into<Arc<str>>>(&self, name: N) -> Option<ArrayRef>;
    fn get_map<N: Into<Arc<str>>>(&self, name: N) -> Option<MapRef>;
    fn get_xml_fragment<N: Into<Arc<str>>>(&self, name: N) -> Option<XmlFragmentRef>;
    fn get<S: AsRef<str>>(&self, name: S) -> Option<Out>;
    fn parent_doc(&self) -> Option<Doc>;
    fn branch_id(&self) -> Option<BranchID>;
    fn has_missing_updates(&self) -> bool;
}
```

The only abstract method is `store(&self) -> &Store` (`transaction.rs:31`). Everything else is a default impl that goes through `self.store()`. Both `Transaction` and `TransactionMut` implement it (`transaction.rs:425-430`, `467-472`).

### `WriteTxn` trait (`transaction.rs:290-372`)

```rust
pub trait WriteTxn: Sized {
    fn store_mut(&mut self) -> &mut Store;
    fn subdocs_mut(&mut self) -> &mut Subdocs;
    fn get_or_insert_text<N: Into<Arc<str>>>(&mut self, name: N) -> TextRef;
    fn get_or_insert_map<N: Into<Arc<str>>>(&mut self, name: N) -> MapRef;
    fn get_or_insert_array<N: Into<Arc<str>>>(&mut self, name: N) -> ArrayRef;
    fn get_or_insert_xml_fragment<N: Into<Arc<str>>>(&mut self, name: N) -> XmlFragmentRef;
    fn prune_pending(&mut self) -> Option<Update>;
}
```

Two abstract methods (`transaction.rs:291-292`). `subdocs_mut` lazily allocates `Subdocs` via `self.subdocs.get_or_init()` (`transaction.rs:480-482`) — the field is `Option<Box<Subdocs>>` and stays `None` for transactions that touch no subdoc.

### `Transaction<'doc>` (`transaction.rs:414-417`)

```rust
#[derive(Debug)]
pub struct Transaction<'doc> {
    store: RwLockReadGuard<'doc, Store>,
}
```

A tuple-of-one wrapping a read guard from `async_lock::RwLockReadGuard` (`transaction.rs:19`). The `'doc` lifetime is the only thing keeping it tied to the `Doc`. Construction is crate-private: `Transaction::new(store)` (`transaction.rs:420-422`).

### `TransactionMut<'doc>` (`transaction.rs:444-465`)

```rust
pub struct TransactionMut<'doc> {
    pub(crate) store: RwLockWriteGuard<'doc, Store>,
    /// State vector of a current transaction at the moment of its creation.
    pub(crate) before_state: StateVector,
    /// Current state vector of a transaction, which includes all performed updates.
    pub(crate) after_state: StateVector,
    /// ID's of the blocks to be merged.
    pub(crate) merge_blocks: Vec<ID>,
    /// Describes the set of deleted items by ids.
    pub(crate) delete_set: IdSet,
    /// We store the reference that last moved an item. This is needed to compute the delta
    /// when multiple ContentMove move the same item.
    pub(crate) prev_moved: HashMap<ItemPtr, ItemPtr>,
    /// All types that were directly modified (property added or child inserted/deleted).
    /// New types are not included in this Set.
    pub(crate) changed: HashMap<TypePtr, HashSet<Option<Arc<str>>>>,
    pub(crate) changed_parent_types: Vec<BranchPtr>,
    pub(crate) subdocs: Option<Box<Subdocs>>,
    pub(crate) origin: Option<Origin>,
    doc: Doc,
    committed: bool,
}
```

Every field except `origin`, `doc`, and `committed` is consumed during `commit` (see commit lifecycle below). All the `pub(crate)` fields are mutated by the YATA integrate / delete / apply_update paths in the same module.

### `Subdocs` (`transaction.rs:1198-1203`)

```rust
#[derive(Default)]
pub struct Subdocs {
    pub(crate) added: HashMap<DocAddr, Doc>,
    pub(crate) removed: HashMap<DocAddr, Doc>,
    pub(crate) loaded: HashMap<DocAddr, Doc>,
}
```

Three sets keyed by `DocAddr` (the heap address of the inner `Store` arc; this is how yrs gives subdocs identity even when they share a guid). Lifetime: built up during the transaction, drained at commit to become a `SubdocsEvent`.

### `Origin` (`transaction.rs:1210-1213`)

```rust
#[repr(transparent)]
#[derive(Clone, Ord, PartialOrd, Eq, PartialEq, Hash)]
pub struct Origin(SmallVec<[u8; std::mem::size_of::<usize>()]>);
```

An opaque byte string, inline-stored up to `sizeof(usize)` (8 bytes on 64-bit). `From` impls (`transaction.rs:1220-1288`) cover `Pin<&T>` (the pointer address as bytes), `&[u8]`, `&str`, `String`, `ClientID`, and every primitive integer (each via `to_be_bytes`). `Display` prints hex-pair bytes wrapped in `Origin(...)` (`transaction.rs:1257-1265`).

### `Transact` trait (`transact.rs:10-84`)

```rust
pub trait Transact {
    fn try_transact(&self) -> Result<Transaction, TransactionAcqError>;
    fn try_transact_mut(&self) -> Result<TransactionMut, TransactionAcqError>;
    fn try_transact_mut_with<T: Into<Origin>>(&self, origin: T) -> Result<TransactionMut, TransactionAcqError>;
    fn transact_mut_with<T: Into<Origin>>(&self, origin: T) -> TransactionMut;
    fn transact(&self) -> Transaction;
    fn transact_mut(&self) -> TransactionMut;
}
```

Implemented for `Doc` at `transact.rs:86-132`. The blocking variants call `self.store.write_blocking()` / `read_blocking()` (`transact.rs:119`, `124`, `129`); the `try_*` variants call `try_read()` / `try_write()` and translate `None` to `TransactionAcqError::SharedAcqFailed` / `ExclusiveAcqFailed` (`transact.rs:88-99`).

### `AsyncTransact<'doc>` (`transact.rs:136-152`)

Async parallel of `Transact` returning `AcquireTransaction` / `AcquireTransactionMut` futures (`transact.rs:185-222`) that wrap `async_lock::futures::Read` / `Write` over `Store`.

### `TransactionAcqError` (`transact.rs:224-232`)

```rust
pub enum TransactionAcqError {
    SharedAcqFailed,           // "Failed to acquire read-only transaction. Drop read-write transaction and retry."
    ExclusiveAcqFailed,        // "Failed to acquire read-write transaction. Drop other transactions and retry."
    DocumentDropped,           // "All references to a parent document containing this structure has been dropped."
}
```

`DocumentDropped` is raised by shared-type transact wrappers (e.g. `TextRef.transact_mut()`), not by `Doc` directly — the `Doc` impl only ever produces the first two.

### `RootRefs<'doc>` (`transaction.rs:1185-1196`)

```rust
pub struct RootRefs<'doc>(std::collections::hash_map::Iter<'doc, Arc<str>, Box<Branch>>);
```

Wraps the underlying `HashMap` iterator over root types — non-deterministic order.

## Internal invariants

1. **One write at a time, many reads coexist.** The lock is `async_lock::RwLock<Store>` (referenced by the guard types at `transaction.rs:19`). `try_transact_mut` returns `ExclusiveAcqFailed` if any other transaction (read or write) is live; `try_transact` returns `SharedAcqFailed` if a write is live (`transact.rs:88-99`).

2. **`TransactionMut` auto-commits on drop.** `impl Drop for TransactionMut` (`transaction.rs:485-489`) calls `self.commit()`. The `committed: bool` flag (`transaction.rs:464`, set on entry to commit at `transaction.rs:962-966`) makes commit idempotent: explicit `txn.commit()` followed by `drop(txn)` will not run the lifecycle twice.

3. **`before_state` is captured at construction.** `TransactionMut::new` calls `store.blocks.get_state_vector()` and stores it as `before_state` (`transaction.rs:497-502`). `after_state` is left at `StateVector::default()` until commit step 1 fills it (`transaction.rs:970`).

4. **Rollback is not supported.** Doc comment at `transaction.rs:443`: *"In Yrs transactions are always auto-committing all of their changes when dropped. Rollbacks are not supported (if some operations needs to be undone, this can be achieved using [UndoManager])"*. There is no `abort()` API and no transactional snapshot of the store.

5. **`changed` keys are `TypePtr::Branch(branch_ptr)`.** `add_changed_type` (`transaction.rs:1108-1118`) only inserts when the parent's own item was created *before* `before_state` (i.e. the parent is not new this transaction). Newly-created types do not generate change events. The `HashSet<Option<Arc<str>>>` value contains `None` for sequence-style parents and `Some(key)` for map-style entries.

6. **`merge_blocks` is a candidate list, not a guarantee.** It accumulates IDs from `apply_delete` (`transaction.rs:640`, `677`), `delete` recursion (`transaction.rs:790`), `split_by_snapshot` (`transaction.rs:1144`, `1159`), and weak-link `unlink` (`transaction.rs:1178`). At commit, each ID is looked up and squashed if a neighbour is present (`transaction.rs:1042-1052`); IDs that no longer exist or have no squashable neighbour are silently skipped.

7. **`delete_set` is canonical-on-the-fly.** Comment at `transaction.rs:968-969`: *"delete set is already canonical — every IdRange::push / IdRange::merge maintains sorted, non-overlapping, non-adjacent invariant on the fly."* No post-pass canonicalisation needed.

8. **`pending` and `pending_ds` survive across transactions.** They live on `Store` (`store.rs:46`, `store.rs:51`), not on `TransactionMut`. `apply_update` reads and writes them (`transaction.rs:807-861`); `prune_pending` drains them (`transaction.rs:355-371`). Doc-scoped, not transaction-scoped.

9. **`subdocs.loaded` is independent of `added`/`removed`.** `loaded` is populated by `Doc::load` (`doc.rs:760-776`) and by `Item::integrate` for `ItemContent::Doc` when `doc.should_load()` is true (`block.rs:800-802`). It is *never* drained at commit — it just rides along inside the `SubdocsEvent` for listeners to observe.

10. **`origin` is set once, at construction.** `TransactionMut::new` takes `Option<Origin>` (`transaction.rs:495`) and stores it; there is no setter. `try_transact_mut` passes `None`; `try_transact_mut_with(o)` passes `Some(o.into())` (`transact.rs:101-113`).

11. **`Subdocs` is lazily allocated.** `subdocs: Option<Box<Subdocs>>` (`transaction.rs:461`); allocated only via `self.subdocs.get_or_init()` (`transaction.rs:482`, `742`, `798`-in `block.rs`). Transactions that touch no subdoc never allocate the struct.

12. **`changed_parent_types` is filled at commit time, not eagerly.** `transaction.rs:548-551`: *"a list of root level types changed in a scope of the current transaction. This list is not filled right away, but as a part of TransactionMut::commit process."* Specifically, populated inside `call_type_observers` during commit step 3 (`transaction.rs:912-953`).

## Edge cases the source handles explicitly

- **Idempotent commit.** `if self.committed { return; }` (`transaction.rs:963-966`). Calling `commit()` then dropping is safe.
- **Pending re-application loop.** After integrating an `apply_update`, if any pending block's `missing.clock` is now `< get_clock(client)`, the transaction recursively `apply_update`s the previously-pending update and its delete set (`transaction.rs:849-858`). This is a finite recursion: each recursive call strictly reduces the pending set.
- **Skip blocks during delete.** `apply_delete` distinguishes between `is_deleted` cells (skipped — already a tombstone) and `Skip` cells / GC cells (added back to `unapplied` because they cannot be deleted) (`transaction.rs:684-690`).
- **Unknown client during delete.** If `apply_delete` is given a range for a client not in `blocks`, the entire range is added to `unapplied` (`transaction.rs:700-706`) so it can be queued as `pending_ds`.
- **Recursive type deletion.** `delete()` recurses into `ItemContent::Type`'s sequence and map children (`transaction.rs:748-768`). For each child it calls `self.delete(ptr)`; if the child was *already* deleted it pushes the ID onto `merge_blocks` so commit can attempt a squash (`transaction.rs:783-792`).
- **Subdoc move-by-deletion.** Deleting an `ItemContent::Doc` item: if the subdoc was added in this same transaction (`subdocs.added.remove(&addr).is_some()`), treat it as never added; otherwise insert into `subdocs.removed` (`transaction.rs:740-747`).
- **`delete_set.is_empty() && before_state == after_state` short-circuits update emission.** `StoreEvents::emit_update_v1`/`v2` skip the work entirely if the transaction had no effect (`store.rs:572-590`).
- **`split_by_snapshot` may split blocks that already exist.** `transaction.rs:1130-1162` walks `snapshot.state_map` and splits each block whose internal clock straddles the snapshot boundary; a `//TODO: we technically don't need to physically split underlying item in two if we were to use block slices all the way down.` (`transaction.rs:1154-1155`) flags this as a future optimisation.
- **`add_changed_type` filters new branches.** Only triggers when `parent.item.id.clock < before_state.get(client) && !parent.is_deleted()` (`transaction.rs:1109-1117`). For root branches (`parent.item.is_none()`), always triggers.
- **`commit` step 6 uses `find_pivot(before_clock).unwrap().max(1)`** (`transaction.rs:1032`). The `.max(1)` ensures the squash loop never tries to squash index 0 into a non-existent index -1; this is a documented invariant of `squash_left(i)` (it requires `i >= 1`).
- **Subdoc `client_id` propagation at commit.** When new subdocs are committed, each subdoc's `store.client_id` is overwritten with the parent doc's `client_id` (`transaction.rs:1067-1073`) — subdocs share the parent's client identity. This is also where `set_subdoc_data` propagates the parent's `collection_id`.

## Public methods we will need

### `ReadTxn::store(&self) -> &Store` (`transaction.rs:31`)

Single abstract method. Returns the `Store` reference (deref of the read or write guard).

### `ReadTxn::state_vector(&self) -> StateVector` (`transaction.rs:34-36`)

Delegates to `store.blocks.get_state_vector()`. Already available on our `BlockStore`.

### `ReadTxn::snapshot(&self) -> Snapshot` (`transaction.rs:40-46`)

Returns `Snapshot::new(state_vector, IdSet::from_store(blocks))`. Defer until `Snapshot` lands.

### `ReadTxn::has_missing_updates(&self) -> bool` (`transaction.rs:284-287`)

`store.pending.is_some() || store.pending_ds.is_some()`.

### `ReadTxn::get_text/get_array/get_map/get_xml_fragment/get` (`transaction.rs:194-259`)

Lookup by name. `get` (`transaction.rs:243-259`) is the type-erased variant returning `Out`. For the Go port, port `Get` returning a discriminated `Value` and per-type wrappers (`GetText`, `GetMap`, etc.) on top.

### `WriteTxn::store_mut(&mut self) -> &mut Store` and `subdocs_mut(&mut self) -> &mut Subdocs` (`transaction.rs:291-292`)

The two abstract methods. `subdocs_mut` lazily allocates the `Subdocs` struct.

### `WriteTxn::get_or_insert_*` (`transaction.rs:304-351`)

Same as the `ReadTxn` getters but creates the root branch if absent. Will be implemented on top of `Store::get_or_create_type`.

### `WriteTxn::prune_pending(&mut self) -> Option<Update>` (`transaction.rs:355-371`)

Drains both `pending` and `pending_ds`, returns a merged `Update` (or `None` if both were empty). Used by users who want to reset their pending state.

### `TransactionMut::new(doc: Doc, store: RwLockWriteGuard<'doc, Store>, origin: Option<Origin>)` (`transaction.rs:492-512`)

Crate-private constructor. Captures `before_state`, zeroes everything else.

### `TransactionMut::commit(&mut self)` (`transaction.rs:962-1096`)

The lifecycle. See *Commit lifecycle* below.

### `TransactionMut::apply_update(&mut self, update: Update) -> Result<(), UpdateError>` (`transaction.rs:807-861`)

Integrates a remote update into this transaction's store. Updates the `pending` and `pending_ds` fields. May recurse if integration unblocks previously-pending updates.

### `TransactionMut::apply_delete(&mut self, ds: &IdSet) -> Option<IdSet>` (`transaction.rs:607-714`)

Pure delete-set application. Returns the `unapplied` portion (rangess that referenced unknown clients or GC'd ranges).

### `TransactionMut::delete(&mut self, item: ItemPtr) -> bool` (`transaction.rs:718-795`)

Deletes a single item, recursing into child types. Returns `true` if delete actually happened, `false` if the item was already deleted. The `false` case is significant for callers (used by `delete()` recursion at line 785 to decide whether to push to `merge_blocks`).

### `TransactionMut::create_item<T: Prelim>(&mut self, pos, value, parent_sub) -> Option<ItemPtr>` (`transaction.rs:863-910`)

Allocates a new local item, integrates it, pushes to block store, and recursively integrates any `Prelim` remainder.

### `TransactionMut::encode_update / encode_update_v1 / encode_update_v2` (`transaction.rs:571-603`)

Encodes only the inserts and deletes performed *within this transaction* (uses `before_state` as the diff baseline and `self.delete_set` for deletes).

### `TransactionMut::origin(&self) -> Option<&Origin>` / `before_state` / `after_state` / `delete_set` / `changed_parent_types` (`transaction.rs:514-551`)

Read-only accessors for observers.

### `TransactionMut::events()` / `events_mut()` (`transaction.rs:518-524`)

Access to `Store::events` (`StoreEvents`).

### `TransactionMut::has_added(id) / has_deleted(id)` (`transaction.rs:1121-1128`)

Diagnostic predicates: `has_added` is `id.clock >= before_state.get(client)`; `has_deleted` is `delete_set.contains(id)`.

### `TransactionMut::add_changed_type(parent: BranchPtr, parent_sub: Option<Arc<str>>)` (`transaction.rs:1108-1118`)

Records a change for observer dispatch. Filtered by the "parent existed before this txn" predicate.

### `TransactionMut::gc(&mut self, delete_set: Option<&IdSet>)` (`transaction.rs:1104-1106`)

Manual GC trigger. Unrelated to commit — useful when `skip_gc` was set on the doc but the user wants to GC explicitly.

### `Transact::try_transact / transact / try_transact_mut / transact_mut / try_transact_mut_with / transact_mut_with` (`transact.rs:18-83`)

The public entry points. The blocking variants block on the `Store` `RwLock`; the `try_*` variants return `TransactionAcqError`.

## Concrete Go translation choices

### Two transaction types, no traits

Drop `ReadTxn` / `WriteTxn` traits entirely. Go interfaces should be defined where consumed, not where provided, and the only consumer of these traits inside yrs is the public API surface (`get_text(&self, txn)`-style helpers). Provide:

```go
type Transaction struct {
    store *Store           // pinned for lifetime of the txn; unlocked in Close()
    rmu   *sync.RWMutex    // back-reference for unlock
    closed bool
}

type TransactionMut struct {
    store *Store
    mu    *sync.RWMutex
    doc   *Doc

    beforeState         StateVector
    afterState          StateVector
    mergeBlocks         []ID
    deleteSet           *IdSet
    prevMoved           map[*Item]*Item
    changed             map[BranchID]map[string]struct{} // map of parent -> set of subkeys (use "" for the None case; or use a *string if we want to distinguish)
    changedParentTypes  []*Branch
    subdocs             *Subdocs                          // lazily allocated
    origin              *Origin                           // nil = no origin
    committed           bool
}
```

`changed` keys: yrs uses `TypePtr` (which can be `Branch`, `Named`, `ID`, `Unknown`). We store branch identity by `BranchID` (the Go equivalent we already have in `internal/block`); root types and nested types both resolve to a single `BranchID`. The `Option<Arc<str>>` value becomes a `map[string]struct{}` where the empty string sentinel represents `None`. Document this. Alternative: use `map[*string]struct{}` with `nil` for `None`.

### `RWMutex` for the doc, not async

Use `sync.RWMutex` on `Doc`. `Doc.Transact()` acquires `RLock`, returns `*Transaction`. `Doc.TransactMut(origin)` acquires `Lock`, returns `*TransactionMut`. Auto-commit on close (see lifetime contract). Go's `sync.RWMutex` is **not re-entrant** — same as `async_lock::RwLock`. yrs's test at `transact.rs:241-272` confirms this: a thread calling `try_transact_mut` after another thread has the lock will fail; calling `transact_mut` blocks. The Go port matches naturally.

### Lifetime contract: `Doc.Transact(fn func(*Transaction))` callback API

Without lifetimes, the safe pattern is a callback that scopes the transaction:

```go
func (d *Doc) Transact(fn func(*Transaction)) {
    d.mu.RLock()
    txn := &Transaction{store: d.store, mu: &d.mu}
    defer func() { txn.closed = true; d.mu.RUnlock() }()
    fn(txn)
}

func (d *Doc) TransactMut(origin *Origin, fn func(*TransactionMut)) {
    d.mu.Lock()
    txn := newTransactionMut(d, origin)
    defer func() {
        txn.commit()
        d.mu.Unlock()
    }()
    fn(txn)
}
```

Document loudly: *"The Transaction value passed to the callback must not be retained past the callback's return. Holding it after return causes data races."* Optionally add a `defer` check against `txn.closed` on every method.

A second pattern (matches yrs more closely) is `txn := d.TransactMut()`-returns-a-deferable-handle. We can provide both; the callback form is the one tutorials use.

### Origin: byte slice with constructors

```go
type Origin []byte

func OriginFromString(s string) Origin   { return Origin(s) }
func OriginFromBytes(b []byte) Origin    { return append(Origin(nil), b...) } // copy!
func OriginFromUint64(v uint64) Origin   { b := make([]byte, 8); binary.BigEndian.PutUint64(b, v); return b }
func OriginFromClientID(c ClientID) Origin { return OriginFromUint64(uint64(c)) }
```

Skip the `SmallVec` inline-storage optimisation — Go's slice header is 24 bytes already; the savings are negligible vs. yrs and the API stays simpler. **Important:** the byte constructor must copy (origins outlive the transaction in observer callbacks).

### `Subdocs` translation

```go
type Subdocs struct {
    Added   map[DocAddr]*Doc
    Removed map[DocAddr]*Doc
    Loaded  map[DocAddr]*Doc
}
```

`DocAddr` in yrs is `(uintptr, ClientID)`-style identity tied to the `Arc<RwLock<Store>>` address. In Go, use the address of `Doc.store` (`uintptr(unsafe.Pointer(d.store))`) plus the doc's guid for stability — or simply the guid string if we accept that two distinct `Doc` instances with the same guid collapse.

Lazy allocation pattern:

```go
func (t *TransactionMut) subdocsMut() *Subdocs {
    if t.subdocs == nil {
        t.subdocs = &Subdocs{
            Added: map[DocAddr]*Doc{}, Removed: map[DocAddr]*Doc{}, Loaded: map[DocAddr]*Doc{},
        }
    }
    return t.subdocs
}
```

### Commit lifecycle ordering

Implement `commit()` in this exact order, mirroring `transaction.rs:962-1096`:

1. **Idempotency guard** — `if t.committed { return }; t.committed = true` (`transaction.rs:963-966`).
2. **Capture `afterState`** — `t.afterState = t.store.blocks.GetStateVector()` (`transaction.rs:970`).
3. **Trigger type observers** — for each `(branch_ptr, subs)` in `changed`, call `branch.trigger(self, subs)` and recursively walk parents/links to populate `changed_parent_types` and the per-branch deep-event cache (`transaction.rs:973-991`).
4. **Trigger deep observers** — for each `(branch, event_indexes)` in `changed_parents`, call `branch.trigger_deep(self, &events)` (`transaction.rs:993-1011`).
5. **`emit_after_transaction`** — fires `after_transaction_events` observers (`transaction.rs:1014-1017`).
6. **GC** — if `!store.skip_gc`: `GCCollector::collect(self)` (`transaction.rs:1019-1022`).
7. **Squash delete set** — `self.delete_set.try_squash_with(&mut self.store)` (`transaction.rs:1024-1025`).
8. **Squash newly inserted blocks** — for each `(client, after_clock)` in `after_state` that differs from `before_state`, walk `[max(find_pivot(before_clock), 1) .. len-1]` in reverse calling `squash_left(i)` (`transaction.rs:1027-1039`).
9. **Squash `merge_blocks` candidates** — for each `id`, look up the cell, attempt `squash_left(i+1)` else `squash_left(i)` (`transaction.rs:1041-1052`).
10. **`emit_transaction_cleanup`** then **`emit_update_v1`** then **`emit_update_v2`** (`transaction.rs:1054-1061`). Note: the v1/v2 update events skip emission entirely when the transaction was a no-op (`store.rs:572-590`).
11. **Apply subdoc add/remove to `store.subdocs`**, set client_id and collection_id on each added subdoc, then fire `subdocs_events` and finally call `subdoc.destroy(self)` for each removed entry (`transaction.rs:1063-1095`).

The order is observable: observers in step 5 see *pre*-squash, *pre*-GC state; observers in step 10 see post-squash, post-GC state. Do not reorder. Document this prominently.

### `TransactionMut.commit()` is `func (t *TransactionMut) commit()` (unexported)

Public commit comes from the callback closing in `Doc.TransactMut`. If we also expose a non-callback API (`txn := doc.TransactMut(origin); ...; txn.Close()`), `Close()` calls `commit()` then `Unlock()`.

### `apply_update` / `apply_delete`

Port faithfully. The recursion in `apply_update` (`transaction.rs:849-858`) is bounded — translate to a loop with a single re-entry rather than recursive call to keep the Go stack predictable. Not strictly required for correctness; just style.

### Pending state: leave on Store

Mirror yrs exactly: `Store.Pending *PendingUpdate` and `Store.PendingDS *IdSet`. They are doc-scoped, written by `apply_update`, read by `Encode*`-state methods, drained by `prune_pending`. Do not move them onto `TransactionMut`.

## Forward dependencies

- **`internal/store`** (already shipped): `Store`, `BlockStore`, `BlockCell`, `ItemSlice`, `Materialize`, `GetStateVector`. All present.
- **`internal/block`** (already shipped except `Item.Integrate`): `Item`, `ID`, `BranchID`. The commit lifecycle does not require `Item.Integrate`, but `apply_update` does — port `apply_update` *after* `Item.Integrate`.
- **`internal/idset` (not yet ported)** — `IdSet`, `IdRange`, `from_store`, `try_squash_with`, `merge_with`, `contains`. Required for `delete_set`, `apply_delete`, `pending_ds`. **Port immediately before transaction layer.**
- **`internal/types/branch` (partial)** — `Branch`, `BranchPtr`-equivalent (`*Branch`). Need `Branch.trigger(txn, subs)` and `Branch.trigger_deep(txn, &events)` methods stubs; can defer real observer wiring until shared types land.
- **`internal/update` (not yet ported)** — `Update`, `Update::integrate`, `Update::merge_updates`, `PendingUpdate`. `apply_update` cannot be implemented without these. The non-`apply_update` parts of the transaction layer (commit, delete, create_item) can be ported and tested first using a stub `Update`.
- **`internal/encoder` (partial)** — `Encoder` / `EncoderV1` / `EncoderV2`. Needed for `encode_update*`. Stub for now.
- **`internal/observer` (not yet ported)** — `Observer<F>`, subscription handles. Needed for `StoreEvents` to fire. Can stub as a no-op observer for v1.
- **`internal/snapshot` (not yet ported)** — `Snapshot`, `Snapshot::new`. Needed for `ReadTxn.snapshot()` and `split_by_snapshot`. Defer.
- **`internal/event` (not yet ported)** — `Event`, `Events`, `SubdocsEvent`, `UpdateEvent`, `TransactionCleanupEvent`. Needed for observer dispatch. Stub for v1.
- **`internal/gc` (not yet ported)** — `GCCollector::collect(txn)` and `collect_all(txn, ds)`. Step 6 and `txn.gc()` depend on it. Can be a no-op stub initially (only effect: deleted blocks remain as blocks instead of becoming GC cells).
- **`internal/types/move` (not yet ported)** — `prev_moved` and `ContentMove::delete`/`integrate_block`. Can defer; `prev_moved` initialises empty and the commit lifecycle does not consume it directly.
- **`internal/weak` (not yet ported, gated on cargo `weak` feature)** — `linked_by`, `unlink`. Defer; matches yrs's `#[cfg(feature = "weak")]` gating (`transaction.rs:750-753`, `1164-1181`).

## Open questions

1. **Re-entrancy.** `async_lock::RwLock` is not re-entrant. yrs's pattern is to never call `transact*` from within an existing transaction on the same doc. The test at `transact.rs:255` is explicit: `// let mut txn = d.try_transact_mut().unwrap(); // this will hang forever`. **Decision needed for Go port:** match this (`sync.RWMutex` is also non-re-entrant; matches naturally) and document loudly, or detect re-entrancy and panic with a useful error. Recommend: detect via a per-doc goroutine-local marker (using a `sync/atomic` counter on `Doc` plus checking from inside `Transact` for goroutine ID — but goroutine IDs are not officially exposed). Simpler: document and rely on the deadlock to surface bugs in tests.

2. **`changed` value type for the `None` case.** yrs's `Option<Arc<str>>` distinguishes "the parent's sequence list changed" (`None`) from "key X on the parent's map changed" (`Some(k)`). In Go, the choices are `*string` (allows nil), `string` with sentinel `""` (collides if a real key is empty), or a wrapper struct `{ Key string; Present bool }`. **Recommend:** `*string` — clearest semantics, only one allocation per distinct key per transaction.

3. **`prev_moved` lifecycle.** The field is read in `apply_delete` (`transaction.rs:633-637`, `659-665`), `split_by_snapshot` (`transaction.rs:1138-1142`), and is populated by `ContentMove::integrate_block`. We have no `ContentMove` yet. Confirm: leaving `prev_moved` empty for the move-disabled v1 port is safe (`apply_delete` only reads it when `item.moved.is_some()`, which is always false without move).

4. **`DocAddr` identity.** In Rust `DocAddr` is the heap address of an `Arc<RwLock<Store>>` — distinct subdocs with the same guid have distinct `DocAddr`s. In Go, `unsafe.Pointer(d.store)` is the closest equivalent, but we need to confirm subdoc semantics: does Yjs ever distinguish "the same subdoc loaded twice"? If not, key by guid. If yes, we need a stable per-instance ID generator.

5. **`origin` equality semantics.** Origin equality is byte-wise (`#[derive(Eq, PartialEq, Hash)]` on `SmallVec<[u8; 8]>` at `transaction.rs:1211`). UndoManager and observers compare origins by value. Go: implement `Origin.Equal(other Origin) bool` as `bytes.Equal`; if used as map key, convert to `string([]byte)` (Go strings are immutable byte sequences and hashable).

6. **`commit` step 8 underflow on first txn.** When `before_state.get(client) == 0` and the client is brand new, `find_pivot(0)` returns `Some(0)`, then `.max(1)` returns `1`, and `i = blocks.len() - 1 >= 1` only if `blocks.len() >= 2`. Single-block-insert transactions skip the squash loop entirely (correct: nothing to squash). Confirm Go port handles `blocks.len() < 2` — `for i := len(blocks)-1; i >= firstChange; i--` naturally skips when `len < 2`.

7. **Subdoc `destroy` runs *after* `subdocs_events` fires.** `transaction.rs:1080-1094`: the event sees the about-to-be-destroyed `Doc` instances; the `destroy()` call comes after. Document this — observers must not assume the doc is still valid by the time they actually act on it.

8. **`encode_state_as_update_v1`/`v2` calls `merge_pending_v1`/`v2` which `unwrap()` the result of `merge_updates_v1`/`v2`** (`transaction.rs:388`, `406`). If pending data is malformed, this panics. Confirm whether yrs treats this as an unrecoverable invariant violation or a bug; the Go port should at minimum return an error rather than panicking.

9. **The `//TODO` at `transaction.rs:1154-1155`** about `split_by_snapshot` not needing to physically split blocks if block-slices were used end-to-end. Future optimisation; the Go port can match yrs's current behaviour for now.

10. **`call_type_observers` event ordering.** Step 4 does `events.iter()` from a `HashMap<BranchPtr, Vec<usize>>` (`transaction.rs:994`), which is non-deterministic. The comment at `transaction.rs:995-996` says *"sort events by path length so that top-level events are fired first"* — but the actual sort is just `events.iter()` collected into a `Vec<&Event>`. The path-length sort comment may describe intent rather than implementation. Confirm against JS Yjs whether deep-observer dispatch order is part of the contract; if so, our Go port should sort explicitly.
