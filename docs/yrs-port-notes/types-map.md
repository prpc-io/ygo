# yrs port notes: Map shared type

> Source: yrs commit 639db2038fa44d09f628a2650fd900e3b109ad1e, files: yrs/src/types/map.rs, yrs/src/types/mod.rs (Entries iterator + EntryChange + event_keys), yrs/src/branch.rs (Branch struct, Branch::get, Branch::remove, BranchPtr), yrs/src/out.rs (Out enum), yrs/src/input.rs (In enum), yrs/src/transaction.rs (TransactionMut::create_item — the integration entry point used by Map::insert).

The mental model the file documents in its module-doc comment (`map.rs:19-27`): Map is "a collection used to store key-value entries in an unordered manner. Keys are always represented as UTF-8 strings… [it] uses logical last-write-wins principle, meaning the past updates are automatically overridden and discarded by newer ones, while concurrent updates made by different peers are resolved into a single value using document id seniority to establish order." The convergence guarantee lives entirely inside `Item::Integrate` — Map adds no CRDT logic of its own.

## 1. Public Rust types

### `MapRef` (`map.rs:60-62`)

```rust
#[repr(transparent)]
#[derive(Debug, Clone)]
pub struct MapRef(BranchPtr);
```

Single-field newtype around `BranchPtr` (a `NonNull<Branch>`, `branch.rs:28`). `#[repr(transparent)]` means `MapRef` and `BranchPtr` are layout-identical; this is what lets every shared type ref cast cheaply between each other. Three blanket impls slot it into the wider type system:

```rust
impl RootRef  for MapRef { fn type_ref() -> TypeRef { TypeRef::Map } }   // map.rs:64-68
impl SharedRef for MapRef {}                                              // map.rs:69
impl Map      for MapRef {}                                               // map.rs:70
impl DeepObservable for MapRef {}                                         // map.rs:72
impl Observable     for MapRef { type Event = MapEvent; }                 // map.rs:73-75
```

`From<BranchPtr>` (`map.rs:512-516`) is the constructor; there is no `MapRef::new`. Instances are produced by `Doc::get_or_insert_map` / by reading `Out::YMap` out of another shared type / via the `Prelim::integrate` path when a `MapPrelim` is being materialised inside a parent.

### `Map` trait (`map.rs:152-393`)

```rust
pub trait Map: AsRef<Branch> + Sized {
    fn len<T: ReadTxn>(&self, _txn: &T) -> u32 { ... }
    fn keys<'a, T: ReadTxn + 'a>(&'a self, txn: &'a T) -> Keys<'a, &'a T, T> { ... }
    fn values<'a, T: ReadTxn + 'a>(&'a self, txn: &'a T) -> Values<'a, &'a T, T> { ... }
    fn iter<'a, T: ReadTxn + 'a>(&'a self, txn: &'a T) -> MapIter<'a, &'a T, T> { ... }
    fn into_iter<'a, T: ReadTxn + 'a>(self, txn: &'a T) -> MapIntoIter<'a, T> { ... }
    fn insert<K, V>(&self, txn: &mut TransactionMut, key: K, value: V) -> V::Return
        where K: Into<Arc<str>>, V: Prelim;
    fn try_update<K, V>(&self, txn: &mut TransactionMut, key: K, value: V) -> bool
        where K: Into<Arc<str>>, V: Into<Any>;
    fn get_or_init<K, V>(&self, txn: &mut TransactionMut, key: K) -> V
        where K: Into<Arc<str>>, V: DefaultPrelim + TryFrom<Out>;
    fn remove(&self, txn: &mut TransactionMut, key: &str) -> Option<Out>;
    fn get<T: ReadTxn>(&self, txn: &T, key: &str) -> Option<Out>;
    fn get_as<T, V>(&self, txn: &T, key: &str) -> Result<V, Error>
        where T: ReadTxn, V: DeserializeOwned;
    fn contains_key<T: ReadTxn>(&self, _txn: &T, key: &str) -> bool;
    fn clear(&self, txn: &mut TransactionMut);
    #[cfg(feature = "weak")]
    fn link<T: ReadTxn>(&self, _txn: &T, key: &str) -> Option<crate::WeakPrelim<Self>>;
}
```

The trait abstracts over `MapRef` and over types that wrap a Map-shaped Branch (XML attributes use the same Branch.map field, `branch.rs:186-192`). All methods have default impls and are dispatched through `self.as_ref()` returning `&Branch`.

### `MapPrelim` (`map.rs:518-589`)

```rust
#[repr(transparent)]
#[derive(Debug, Clone, PartialEq, Default)]
pub struct MapPrelim(HashMap<Arc<str>, In>);

impl Prelim for MapPrelim {
    type Return = MapRef;
    fn into_content(self, _txn: &mut TransactionMut) -> (ItemContent, Option<Self>) {
        let inner = Branch::new(TypeRef::Map);
        (ItemContent::Type(inner), Some(self))
    }
    fn integrate(self, txn: &mut TransactionMut, inner_ref: BranchPtr) {
        let map = MapRef::from(inner_ref);
        for (key, value) in self.0 {
            map.insert(txn, key, value);
        }
    }
}
```

Two-phase construction: `into_content` returns a fresh empty `Branch{type_ref: Map}` wrapped in `ItemContent::Type`; `integrate` is invoked after the parent Item is in the store and replays each `(key, value)` entry through `map.insert`, which recurses for nested `In::Map` / `In::Array`. `From<MapPrelim> for In` (`map.rs:540-545`), `FromIterator` + `From<[(K,V); N]>` (`map.rs:547-573`).

### `MapEvent` (`map.rs:598-645`)

```rust
pub struct MapEvent {
    pub(crate) current_target: BranchPtr,
    target: MapRef,
    keys: UnsafeCell<Result<HashMap<Arc<str>, EntryChange>, HashSet<Option<Arc<str>>>>>,
}
```

`keys` is lazily computed: stored as `Err(HashSet)` of changed keys until the first call to `keys(txn)` materialises it into `Ok(HashMap)` via `event_keys` (`mod.rs:884-935`). `target()` returns the `&MapRef`, `path()` returns the root-relative path (`map.rs:621-623`).

### Helper enums

```rust
pub enum Out {                                                            // out.rs:14-39
    Any(Any), YText(TextRef), YArray(ArrayRef), YMap(MapRef),
    YXmlElement(XmlElementRef), YXmlFragment(XmlFragmentRef),
    YXmlText(XmlTextRef), YDoc(Doc),
    #[cfg(feature = "weak")] YWeakLink(crate::WeakRef<BranchPtr>),
    UndefinedRef(BranchPtr),
}

pub enum In {                                                             // input.rs:14-26
    Any(Any), Text(DeltaPrelim), Array(ArrayPrelim), Map(MapPrelim),
    XmlElement(XmlElementPrelim), XmlFragment(XmlFragmentPrelim),
    XmlText(XmlDeltaPrelim), Doc(Doc),
    #[cfg(feature = "weak")] WeakLink(WeakPrelim<BranchPtr>),
}

pub enum EntryChange {                                                    // mod.rs:794-805
    Inserted(Out),
    Updated(Out, Out),   // (old, new) — only when overwriting a still-deleted prev
    Removed(Out),
}
```

`Out` is the read side, `In` is the write side. `Prelim` is the trait that lets any value (including `In` and `MapPrelim`) be turned into an `ItemContent` plus optional post-integration callback.

## 2. The Map trait public methods

| method | what it does | block-layer ops |
|---|---|---|
| `len` (`map.rs:154-164`) | counts non-deleted entries by walking `branch.map.values()` | none — pure read of `Branch.map: HashMap<Arc<str>, ItemPtr>` (`branch.rs:192`). The TODO at `map.rs:158` notes this should probably be cached. |
| `keys` (`map.rs:168-170`) | returns `Keys` iterator over `Entries` (which already skips deleted, `mod.rs:677-683`) | reads `branch.map` |
| `values` (`map.rs:173-175`) | `Values` iterator; for each non-deleted item allocates `Vec<Out>` of length `item.len()` and calls `item.content.read(0, ..)` | reads each item's content slice |
| `iter` (`map.rs:179-181`) | `MapIter` returning `(&str, Out)` pairs by calling `item.content.get_last()` (`map.rs:415-422`) | reads `branch.map` + last-element extraction |
| `insert` (`map.rs:189-215`) | constructs an Item with `parent=branch, parent_sub=Some(key), origin=last_id_of_current_tail, content=value.into_content()`, integrates it | `TransactionMut::create_item` → `Item::new` → `Item::Integrate` |
| `try_update` (`map.rs:238-260`) | reads current tail; if it's `ItemContent::Any` whose last value `==` new value, returns `false`; else delegates to `insert` | read + conditional insert |
| `get_or_init` (`map.rs:265-279`) | `branch.get(txn, &key).try_into()`; on miss / wrong type, inserts `V::default_prelim()` and returns the new ref | read + conditional insert |
| `remove` (`map.rs:290-293`) | delegates to `Branch::remove` (`branch.rs:354-363`) which calls `txn.delete(item)` on the tail and returns the prior `Out` | `txn.delete(item)` |
| `get` (`map.rs:308-311`) | delegates to `Branch::get` (`branch.rs:321-328`) | pure read of tail |
| `get_as` (`map.rs:366-376`) | `get` → `to_json` → `from_any::<V>` (serde) | read |
| `contains_key` (`map.rs:379-385`) | reads `branch.map.get(key)` and checks `!is_deleted()` | read |
| `clear` (`map.rs:388-392`) | iterates `branch.map.iter()` and calls `txn.delete(ptr.clone())` on every entry, deleted or not | one `txn.delete` per entry |

Notice that **`branch.map` always points at the live tail** (the most recently integrated Item for that key, even if it is the tombstoned one). It is never walked further — a pointer dereference is the whole lookup. The YATA conflict-resolution loop in `Item::Integrate` is what re-pins `branch.map[key]` to the correct successor.

## 3. `Map::insert` — verbatim

```rust
// map.rs:189-215
fn insert<K, V>(&self, txn: &mut TransactionMut, key: K, value: V) -> V::Return
where
    K: Into<Arc<str>>,
    V: Prelim,
{
    let key = key.into();
    let pos = {
        let inner = self.as_ref();
        let left = inner.map.get(&key);
        ItemPosition {
            parent: BranchPtr::from(inner).into(),
            left: left.cloned(),
            right: None,
            index: 0,
            current_attrs: None,
        }
    };

    let ptr = txn
        .create_item(&pos, value, Some(key))
        .expect("Cannot insert empty value");
    if let Ok(integrated) = ptr.try_into() {
        integrated
    } else {
        panic!("Defect: unexpected integrated type")
    }
}
```

Line-by-line:

- `inner.map.get(&key)` (line 197) — single hash-map lookup. **The author does NOT walk `.right` to find a tail.** `branch.map[key]` already IS the tail; this is the YATA invariant maintained by `Item::Integrate`. (When a new Item arrives for a key, `Integrate` updates `branch.map[parent_sub]` to that new Item — see `block.rs` integrate path that we already ported.)
- `ItemPosition.left = left.cloned()` (line 200) — the current tail becomes the new Item's `left` field. **`right` is hard-coded to `None`** because for map keys there is never a "right" neighbour conceptually; tail-only.
- `index: 0` (line 202) and `current_attrs: None` (line 203) are ignored by the map insert path; they exist for sequence-style inserts.
- `txn.create_item(&pos, value, Some(key))` (line 208) — note `parent_sub: Some(key)`. This is what flags the Item as a map-key insert vs. a sequence insert. Inside `create_item` (`transaction.rs:863-910`):

```rust
let origin = if let Some(item) = pos.left.as_deref() {
    Some(item.last_id())  // origin = last_id() of left, NOT left.id
} else {
    None
};
let id = ID::new(client_id, store.get_local_state());
let (mut content, remainder) = value.into_content(self);
let mut block = Item::new(id, left, origin, right, right.map(|r| r.id().clone()),
                          pos.parent.clone(), parent_sub, content)?;
let mut block_ptr = ItemPtr::from(&mut block);
block_ptr.integrate(self, 0);                     // ← YATA loop
self.store_mut().blocks.push_block(block);
if let Some(remainder) = remainder {
    remainder.integrate(self, inner_ref.unwrap().into())   // ← MapPrelim recursion
}
```

So the answer to the structural questions:

- **Origin construction**: `origin = left.last_id()`, where `left = branch.map[key]` (or `None` if the key is fresh). `last_id()` is `ID{client, clock + len - 1}` — the *last* clock the left Item covers, not its first. This matters because the left Item might span multiple clocks.
- **Left neighbour resolution**: by direct hash lookup. No walking. The trait does NOT do `MaterializeCleanEnd` / split — there is no offset, and a map-key Item is conceptually len-1 anyway (it carries one value).
- **No `MaterializeCleanEnd`/split is invoked.** Map keys do not get split; only sequence positions do.
- **Value type**: `V: Prelim`. `Prelim` is implemented for `In`, `MapPrelim`, `ArrayPrelim`, `TextPrelim`, all `T: Into<Any>` (numbers, strings, bool, bytes, Option, Vec, HashMap, …) via blanket impls in `block.rs`. So `insert(txn, "k", 42)`, `insert(txn, "k", "hello")`, `insert(txn, "k", true)`, `insert(txn, "k", MapPrelim::from([...]))` all compile.

There is no separate `insert_at` for maps; the indexed variant only exists on `Array`.

## 4. `Map::get` — verbatim

```rust
// map.rs:308-311
fn get<T: ReadTxn>(&self, txn: &T, key: &str) -> Option<Out> {
    let ptr = BranchPtr::from(self.as_ref());
    ptr.get(txn, key)
}

// branch.rs:321-328
pub(crate) fn get<T: ReadTxn>(&self, _txn: &T, key: &str) -> Option<Out> {
    let item = self.map.get(key)?;
    if !item.is_deleted() {
        item.content.get_last()
    } else {
        None
    }
}
```

- **Reads `branch.map[key]` — the live tail — directly.** No back-walking through deleted predecessors. If the tail is tombstoned, `None` is returned; older non-deleted insertions are not surfaced. This is the correct LWW semantic: once you've overwritten or deleted, prior values are gone from the read view (though they remain in the block store for sync purposes).
- The `_txn` parameter is unused. The read needs no transaction state because every relevant fact is already in the Branch — but the API takes one to enforce the read-lock discipline at compile time.
- `item.content.get_last()` extracts the last element of the content slice as an `Out`. Map-key Items typically carry a single value (length 1), so `get_last` returns that value. The mapping `ItemContent → Out` lives in `out.rs` and `block.rs:get_last`; we ported the variants we need in `internal/block`.
- There is no `get_at` on Map — that's an Array-only operation.

## 5. `Map::remove` — verbatim

```rust
// map.rs:290-293
fn remove(&self, txn: &mut TransactionMut, key: &str) -> Option<Out> {
    let ptr = BranchPtr::from(self.as_ref());
    ptr.remove(txn, key)
}

// branch.rs:352-363
pub(crate) fn remove(&self, txn: &mut TransactionMut, key: &str) -> Option<Out> {
    let item = *self.map.get(key)?;
    let prev = if !item.is_deleted() {
        item.content.get_last()
    } else {
        None
    };
    txn.delete(item);
    prev
}
```

- Reads `branch.map[key]`; if the key never existed, returns `None`.
- Captures the last value of the tail (only if the tail is not already deleted) so it can be returned to the caller.
- Calls `txn.delete(item)` to tombstone. **`branch.map[key]` is NOT cleared** — it keeps pointing at the now-tombstoned Item. This is intentional: the next `insert` for the same key will look up `branch.map[key]`, find the tombstoned Item, and use it as `left` so the new Item is YATA-positioned right after the tombstone. If we cleared the entry, the new Item would have `origin=None` and would race ambiguously against any concurrent insert.
- `len` and `iter` and `contains_key` skip deleted entries explicitly (`is_deleted()` check), so the tombstoned tail is invisible to readers but still serves as the YATA anchor for future inserts.

## 6. `iter`, `keys`, `values`, `len`

All four route through the `Entries` iterator (`mod.rs:640-684`):

```rust
pub(crate) struct Entries<'a, B, T> {
    iter: std::collections::hash_map::Iter<'a, Arc<str>, ItemPtr>,
    txn: B,
    _marker: PhantomData<T>,
}

impl<'a, B, T> Iterator for Entries<'a, B, T>
where B: Borrow<T>, T: ReadTxn,
{
    type Item = (&'a str, &'a Item);
    fn next(&mut self) -> Option<Self::Item> {
        let (mut key, mut ptr) = self.iter.next()?;
        while ptr.is_deleted() {
            (key, ptr) = self.iter.next()?;
        }
        Some((key, ptr))
    }
}
```

Deleted entries are skipped at the iterator level — every consumer (`MapIter`, `Keys`, `Values`) gets a pre-filtered stream. `len` (`map.rs:154-164`) does its own loop instead of `entries.count()` because the `_txn` parameter is unused there and the loop can avoid trait dispatch; functionally equivalent. The TODO at `map.rs:158` flags that caching the count in the Branch would avoid an O(n) walk — open question, not done.

`MapIter::next` (`map.rs:415-422`) skips entries whose `content.get_last()` is `None` by self-recursion — a safety net for content variants that might legitimately have no last value. (In practice `content.get_last()` returns `Some` for every supported content variant, so this branch is paranoia.)

`MapIntoIter` (`map.rs:425-448`) clones `branch.map` so the iterator owns its data and does not need to borrow the Branch. It does NOT skip deleted entries up-front — instead it relies on `content.get_last()` returning something usable. This is a minor inconsistency with `MapIter` and is worth flagging when porting; for safety, replicate the `is_deleted` skip.

## 7. `MapEvent` shape

Three accessors:

- `target() -> &MapRef` (`map.rs:616-618`) — the Map that emitted this event.
- `path() -> Path` (`map.rs:621-623`) — the path from the *current observer's* root down to the emitting Map. `current_target` field tracks where in the tree the event has bubbled to in the deep-observe propagation.
- `keys(txn) -> &HashMap<Arc<str>, EntryChange>` (`map.rs:627-644`) — lazily materialised. The internal state starts as `Err(HashSet<Option<Arc<str>>>)` (the raw set of touched keys collected during the transaction) and is replaced with `Ok(HashMap)` on first access via `event_keys`.

`event_keys` (`mod.rs:884-935`) classifies each touched key by examining the current tail and its `prev = item.left`:

- If `item.id.clock >= txn.before_state[client]` (added in this txn) and `txn.has_deleted(&item.id)` (also deleted in this txn) and `prev` exists and `prev` was deleted: emit `Removed(prev_value)`.
- If added in this txn, not deleted, prev exists and prev was deleted: emit `Updated(old, new)`.
- If added in this txn, not deleted, no live deleted prev: emit `Inserted(new)`.
- If pre-existing and deleted in this txn: emit `Removed(old)`.

All three EntryChange variants exist (`mod.rs:794-805`). For the first port commit we will not implement observers; `MapEvent` is documented for completeness and as a target for the observer port commit.

## 8. Concrete Go translation choices

### File layout

- New package `internal/types` with `internal/types/map.go` and `internal/types/map_test.go`. Future siblings: `array.go`, `text.go`, `xmlfragment.go`.
- Public re-export from a top-level `ygo` package once we wire the user-facing API; for now `internal/types.Map` is fine — tests in this commit live in-package.

### Map struct

```go
// internal/types/map.go
type Map struct {
    branch *block.Branch
}

func newMap(branch *block.Branch) *Map { return &Map{branch: branch} }
```

`Map` is a thin pointer-wrapper over `*block.Branch`, mirroring `MapRef(BranchPtr)`. Equality is pointer-equality on the underlying Branch (which is what yrs's `MapRef::eq` does via `BranchPtr.id()` comparison, `map.rs:98-102`).

### Doc-level root-type registry

```go
// internal/doc/doc.go (extension)
func (d *Doc) Map(name string) *types.Map {
    d.mu.Lock()
    defer d.mu.Unlock()
    if b, ok := d.roots[name]; ok {
        if b.Name == "" { b.Name = name }
        return types.NewMapFromBranch(b)   // exported constructor for the registry path
    }
    b := block.NewBranch(block.TypeRefMap)
    b.Name = name
    d.roots[name] = b
    return types.NewMapFromBranch(b)
}
```

Same `*Map` is returned across calls for the same name (we look up by name, not identity). Doc owns `roots map[string]*block.Branch`. Mirror of yrs `Doc::get_or_insert_map` which goes through `Store::get_or_create_type` (`branch.rs:704-723` analogue).

### Value type

First-pass: accept `any` (Go `interface{}`) for both the value-in (Prelim) and value-out (Out) positions:

```go
func (m *Map) Set(tx *doc.TransactionMut, key string, value any) error
func (m *Map) Get(tx doc.ReadTxn,        key string) (any, bool)
```

We translate `any` into `block.Content` at insert-time via a small `contentFromAny` helper covering the JSON-ish Any subset (`nil`, `bool`, `int64`, `float64`, `string`, `[]byte`, `map[string]any`, `[]any`). On the read side, `contentToAny` is the inverse. Nested shared types (`*Map`, `*Array`) and Doc subdocs are deferred — see section 9.

Once the encoding layer (Day 21-24) lands the equivalent of yrs's `Any` enum we will tighten `any` to a typed `Value` union. The `any`-based API stays as a convenience overload.

### Item-construction helper

The map insert path needs Origin and Left pre-resolved. We expose this from `internal/block`:

```go
// internal/block/item_factory.go
func NewMapInsertItem(
    ctx IntegrateContext,    // gives us the next ID and store ops
    parent *Branch,
    key string,
    content Content,
) *Item {
    var left *Item
    var origin *ID
    if cur, ok := parent.Map[key]; ok {
        left = cur
        lid := cur.LastID()           // {Client, Clock + Len - 1}
        origin = &lid
    }
    id := ctx.NextID()                // ID{Client: store.ClientID, Clock: store.LocalState}
    return &Item{
        ID:        id,
        Origin:    origin,
        Left:      left,
        Right:     nil,
        RightOrigin: nil,
        Parent:    Parent{Branch: parent},
        ParentSub: key,
        Content:   content,
    }
}
```

Then `Map.Set` becomes:

```go
func (m *Map) Set(tx *doc.TransactionMut, key string, value any) error {
    content, err := contentFromAny(value)
    if err != nil { return err }
    item := block.NewMapInsertItem(tx, m.branch, key, content)
    return item.Integrate(tx, 0)      // already implemented
}
```

`item.Integrate` already updates `parent.Map[key]` to the new Item as part of the YATA loop (the `parentSub != ""` branch), so we get the tail-tracking invariant for free.

### Read / Delete / Has / Len

Direct ports of `branch.rs:321-328`, `branch.rs:354-363`, `map.rs:379-385`, `map.rs:154-164`:

```go
func (m *Map) Get(_ doc.ReadTxn, key string) (any, bool) {
    item, ok := m.branch.Map[key]
    if !ok || item.Deleted() { return nil, false }
    v, ok := item.Content.GetLast()
    if !ok { return nil, false }
    return v, true
}

func (m *Map) Delete(tx *doc.TransactionMut, key string) (any, bool) {
    item, ok := m.branch.Map[key]
    if !ok { return nil, false }
    var prev any
    var hadPrev bool
    if !item.Deleted() {
        prev, hadPrev = item.Content.GetLast()
    }
    tx.Delete(item)                  // does NOT clear m.branch.Map[key]
    return prev, hadPrev
}

func (m *Map) Has(_ doc.ReadTxn, key string) bool {
    item, ok := m.branch.Map[key]
    return ok && !item.Deleted()
}

func (m *Map) Len(_ doc.ReadTxn) int {
    n := 0
    for _, it := range m.branch.Map {
        if !it.Deleted() { n++ }
    }
    return n
}
```

### Iteration

`Iter`, `Keys`, `Values` return Go-style `func(yield func(K, V) bool)` range-over-func iterators (Go 1.23+) — or, for older toolchains, `func() (string, any, bool)` next-style. Each one filters out `item.Deleted()` entries up-front (matching yrs `Entries::next`, `mod.rs:677-683`).

## 9. Deferred for the first commit

| Feature | Why defer | Unblocking condition |
|---|---|---|
| Observers (`Observable for MapRef`, `MapEvent`, `event_keys` classification) | Requires Branch.observers + Branch.deep_observers fields, observer registry, transaction-end notification phase. Not on Day 14 critical path. | After Doc commit hooks land; before we ship the public `ygo` package surface. |
| `MapPrelim` recursive nested-type construction | Requires `Prelim` trait + `In` enum. The two-phase `into_content`/`integrate` pattern is straightforward but adds API surface we have not validated yet. | When we add `*Array` (so we have at least two Prelim implementors). |
| Full `Out`/`In` enum coverage (Doc subdocs, XmlElement/Fragment/Text, weak links, structured Any) | None of these have been ported as Content variants in `internal/block` yet. | One commit per shared type after Map lands. |
| `try_update` (`map.rs:238-260`) | Not a correctness primitive; it is an API ergonomics shortcut on top of `insert`. | After `Set`/`Get` are stable. |
| `get_or_init` (`map.rs:265-279`) | Requires `DefaultPrelim` trait + casting. | Same commit as Prelim. |
| `get_as` (deserde-into-typed) | Requires `to_json` and a Go equivalent of serde's `from_any`. | Optional ergonomics; out of scope for Yjs parity. |
| `clear` | Trivial loop, but needs `Delete` on every item to be efficient (no batch); fine to ship in commit 2. | Immediately after `Set`/`Delete` land. |
| `link` (weak refs) | Behind `cfg(feature = "weak")` in yrs; we do not port the weak-ref subsystem in v1. | Indefinitely deferred. |

## 10. Tests for the first Map commit

In `internal/types/map_test.go`:

1. **Set then Get** — single key, single value; `Get` returns the value with `ok==true`; `Has` returns true; `Len==1`.
2. **Set twice, tail wins** — `Set("k", 1)` then `Set("k", 2)`; `Get("k")` returns `2`. Verify `branch.Map["k"]` points at the second Item, the first Item has `Right == second`, `Left == nil`, `Origin == nil`; second Item has `Origin == first.LastID()`. Confirms `parent.map` is being maintained as the tail by `Item.Integrate`.
3. **Delete after Set → Get returns missing** — `Get` returns `nil, false`; `Has` returns false; `Len==0`. Verify `branch.Map["k"]` is still non-nil and points at the tombstoned Item (matches `branch.rs:354-363` "do not clear map[key]"). Then `Set("k", 3)` should produce a third Item with `Origin == tombstone.LastID()`.
4. **Set / Has / Len consistency** — set 5 distinct keys, delete 2, verify `Len==3`, `Has` matches truth-table for each, `Iter` yields exactly the 3 live keys, in unspecified order.
5. **Two Doc instances, manually-copied Items, converged state** — build the Items from doc1's transaction, push them into doc2's BlockStore via the integrate path (no encoding yet — that's Day 21-24). Concurrent inserts on the same key from clients 1 and 2: after both have integrated each other's Items, both Maps must `Get("k")` to the same value (the one from the higher-id client, per `map.rs:23-26`). Concurrent inserts on different keys: both keys readable on both sides. Insert + delete commuted: tombstone wins regardless of order.

These five tests exercise every block-level invariant the Map type relies on (tail-tracking, tombstone-as-origin, YATA conflict ordering) and surface-level API correctness in one go. None of them depends on encoding, observers, or nested shared types.
