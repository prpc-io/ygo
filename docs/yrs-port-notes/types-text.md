# yrs port notes: Text shared type (plain-text path)

> Source: yrs `main`, files: `yrs/src/types/text.rs` (TextRef, TextPrelim, Text trait, free functions `find_position` / `insert` / `remove`), `yrs/src/block.rs` (`SplittableString` + `split_str` + `ItemContent::splice`/`try_squash` + `PrelimString`), `yrs/src/branch.rs` (`Branch::content_len`), `yrs/src/transaction.rs` (`TransactionMut::create_item`, `TransactionMut::delete`), `yrs/src/doc.rs:998-1006` (`OffsetKind`).
>
> **Scope: plain-text only.** Rich-text formatting (`ContentFormat`, `apply_delta`, `insert_with_attributes`, `insert_embed`, `Text::format`, `Text::diff`, `TextEvent::delta` reconstruction) is out of scope for the first commit and is summarised in section 11. Verbatim Rust quoted only for plain-text hot paths and the UTF-16 byte-offset conversion machinery.

The mental model from `text.rs:15-22`: Text is "a shared data type used for collaborative text editing… internally represented as a mutable double-linked list of text chunks - an optimization occurs during `Transaction::commit`, which allows to squash multiple consecutively inserted characters together as a single chunk of text even between transaction boundaries." So Text is structurally identical to Array — a `Branch` with a linked list of Items rooted at `branch.start`, `parent_sub == None`, integrated by YATA — **except**:

1. The countable unit is **UTF-16 code units of `ItemContent::String`**, not "1-per-Any-element". Every offset/length passed to `Text::insert`/`Text::remove_range` is a UTF-16 index.
2. Inserts always carry `ItemContent::String(SplittableString)`. Multiple `insert` calls produce multiple Items; commit-time squash merges adjacent same-client adjacent-clock string Items by string concat.
3. The mid-block split must split a string at a **UTF-16 boundary**, not a byte boundary; `SplittableString::block_offset` does the byte↔UTF-16 conversion.

Everything else (linked list, YATA integrate, tombstones, BlockStore, transactions) is identical to Array and already ported.

## 1. Public Rust types

### `TextRef` (`text.rs:89-102`)

```rust
#[repr(transparent)]
#[derive(Debug, Clone)]
pub struct TextRef(BranchPtr);

impl RootRef for TextRef {
    fn type_ref() -> TypeRef { TypeRef::Text }
}
impl SharedRef       for TextRef {}
impl Text            for TextRef {}
impl IndexedSequence for TextRef {}
#[cfg(feature = "weak")]
impl crate::Quotable for TextRef {}
```

Same `#[repr(transparent)]` newtype-around-`BranchPtr` pattern as `MapRef` and `ArrayRef`. `XmlTextRef` re-uses the same memory layout via `unsafe transmute` (`text.rs:104-109`) — we will not mirror that. `DeepObservable` and `Observable<Event = TextEvent>` at `text.rs:111-114`. `TryFrom<ItemPtr>` (`text.rs:135-145`) and `TryFrom<Out>` (`text.rs:147-156`) cast across types.

### `TextPrelim` (`text.rs:1445-1495`)

```rust
#[repr(transparent)]
#[derive(Debug, Clone, PartialEq, Eq, Hash, Default)]
pub struct TextPrelim(String);

impl Prelim for TextPrelim {
    type Return = TextRef;

    fn into_content(self, _txn: &mut TransactionMut) -> (ItemContent, Option<Self>) {
        let inner = Branch::new(TypeRef::Text);
        (ItemContent::Type(inner), Some(self))
    }

    fn integrate(self, txn: &mut TransactionMut, inner_ref: BranchPtr) {
        if !self.0.is_empty() {
            let text = TextRef::from(inner_ref);
            text.push(txn, &self.0);
        }
    }
}
```

Two-phase nested construction identical in shape to `ArrayPrelim`/`MapPrelim`: `into_content` returns an empty `Branch{type_ref: Text}` carried by `ItemContent::Type`; `integrate` runs after the parent Item is in the store and replays the seed string via `Text::push` (which calls `Text::insert(len, str)` — section 2). `From<TextPrelim> for In` (`text.rs:1474-1479`) wraps it as `In::Text(DeltaPrelim::from(...))` — only matters when the prelim API is exposed; we do hand-coded constructors instead.

### `PrelimString` — *internal* (`block.rs:2213-2223`)

```rust
#[derive(Debug)]
pub(crate) struct PrelimString(pub SmallString<[u8; 8]>);

impl Prelim for PrelimString {
    type Return = Unused;

    fn into_content(self, _txn: &mut TransactionMut) -> (ItemContent, Option<Self>) {
        (ItemContent::String(self.0.into()), None)
    }

    fn integrate(self, _txn: &mut TransactionMut, _inner_ref: BranchPtr) {}
}
```

This is what `Text::insert` actually constructs (`text.rs:218`). It wraps the user's `&str` in a `SmallString` (stack-allocated up to 8 bytes), hands it to `into_content` which produces `ItemContent::String(SplittableString)`. `Return = Unused` because plain-text inserts have no return value.

### `Text` trait (`text.rs:158-`)

Plain-text surface only (rich-text methods cited by line in section 11):

```rust
pub trait Text: AsRef<Branch> + Sized {
    fn len<T: ReadTxn>(&self, _txn: &T) -> u32 {                     // 160-162
        self.as_ref().content_len
    }

    fn insert(&self, txn: &mut TransactionMut, index: u32, chunk: &str);   // 212-231
    fn push(&self, txn: &mut TransactionMut, chunk: &str);                  // 353-356
    fn remove_range(&self, txn: &mut TransactionMut, index: u32, len: u32); // 361-368
    // ...and via the GetString trait:
    fn get_string<T: ReadTxn>(&self, txn: &T) -> String;                    // text.rs:116-133
}
```

`GetString` is its own trait (`text.rs:116-133`) implemented for `TextRef`; we collapse it into a single `Text.String()` Go method.

## 2. Plain-text Text trait methods — what they do

| method | what it does | block-layer ops |
|---|---|---|
| `len(txn)` | Returns `branch.content_len`. **Note: Text uses `content_len`, not `block_len`** — see section 4. O(1). | none |
| `insert(txn, idx, str)` | Calls `find_position(this, txn, idx)` to locate the insert point (splitting any String item the index lands inside at a UTF-16 boundary), forwards past any deleted items at the cursor, then `txn.create_item(&pos, PrelimString(str), None)` to construct one Item with `ItemContent::String(SplittableString)`. | `find_position` → optional `Store::split_block` → `Item::new` + `Item::integrate` + `BlockStore::push_block` |
| `push(txn, str)` | `insert(txn, len(txn), str)` — append. | as above |
| `remove_range(txn, idx, len)` | `find_position(this, txn, idx)`, then walks `pos.right` rightward, splitting on partial-overlap at start (already done by `find_position`) and at end (when `len < content_len`), calling `txn.delete` on each fully-covered live String/Embed/Type item. | `Store::split_block` ×0-1, `TransactionMut::delete` ×N |
| `get_string(txn)` | Walks `branch.start` → `right` chain; for each non-deleted Item whose content is `ItemContent::String(s)`, appends `s` to a `String` builder. Skips everything else (Format, Embed, Deleted, Type). | none |

The `Text` trait does not expose a per-character iterator; reading is one-shot via `get_string`. This matches our planned `Text.String() string`.

## 3. UTF-16 storage and offset semantics — verbatim

This is the wire-format invariant. **All offsets in the encoded update format are UTF-16 code units.** The yrs storage representation is UTF-8 (`SmallString`), so every position arithmetic on a String item must convert. yrs further allows the *user-facing* offset to be either `OffsetKind::Bytes` or `OffsetKind::Utf16` per `Doc::Options::offset_kind` (`doc.rs:898-921`, default `Bytes`). **JS Yjs uses UTF-16 always.** We hard-code `OffsetKind::Utf16` for our port — both the user-facing API and the wire — to match Yjs behaviour. (yrs `Item::integrate` at `block.rs:569-583` already hard-codes `OffsetKind::Utf16` for splices triggered by Update application — comment at `block.rs:570-571`: "offset could be > 0 only in context of Update::integrate, is such case offset kind in use always means Yjs-compatible offset (utf-16)".)

### `OffsetKind` (`doc.rs:998-1006`)

```rust
#[repr(u8)]
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum OffsetKind {
    /// Compute editable strings length and offset using UTF-8 byte count.
    Bytes,
    /// Compute editable strings length and offset using UTF-16 chars count.
    Utf16,
}
```

We do not port the enum; we are UTF-16-only.

### `SplittableString` (`block.rs:1473-1527`) — verbatim

```rust
pub struct SplittableString {
    content: SmallString<[u8; 8]>,
}

impl SplittableString {
    pub fn len(&self, kind: OffsetKind) -> usize {
        let len = self.content.len();
        if len == 1 {
            len // quite often strings are single-letter, so we don't care about OffsetKind
        } else {
            match kind {
                OffsetKind::Bytes => len,
                OffsetKind::Utf16 => self.utf16_len(),
            }
        }
    }

    #[inline(always)]
    pub fn as_str(&self) -> &str { self.content.as_str() }

    #[inline(always)]
    pub fn utf16_len(&self) -> usize { self.encode_utf16().count() }

    /// Maps given offset onto block offset. This means, that given an `offset` provided
    /// in given `encoding` we want the output as a UTF-16 compatible offset (required
    /// by Yjs for compatibility reasons).
    pub(crate) fn block_offset(&self, offset: u32, kind: OffsetKind) -> u32 {
        match kind {
            OffsetKind::Utf16 => offset,
            OffsetKind::Bytes => {
                let mut remaining = offset;
                let mut i = 0;
                // since this offset is used to splitting later on - and we can only split entire
                // characters - we're computing by characters
                for c in self.content.chars() {
                    if remaining == 0 {
                        break;
                    }
                    remaining -= c.len_utf8() as u32;
                    i += c.len_utf16() as u32;
                }
                i
            }
        }
    }

    pub fn push_str(&mut self, str: &str) {
        self.content.push_str(str);
    }
}
```

Line-by-line for the port:

- **`len(Bytes)`** is byte count of the underlying UTF-8 storage. **`len(Utf16)`** walks `chars()` and sums `len_utf16()` per char (1 for BMP, 2 for non-BMP/surrogate pair). `utf16_len()` does the same via `encode_utf16().count()` — equivalent. The `len == 1` short-circuit is an ASCII-fast-path: a single-byte string has `len_bytes == len_utf16 == 1`.
- **`block_offset(offset, Utf16) = offset`** — identity. Caller is already in UTF-16 units; pass through.
- **`block_offset(offset, Bytes)`** walks `chars()` consuming `len_utf8` bytes per step until the byte budget is spent, accumulating UTF-16 units. Output is the equivalent UTF-16 offset that maps to the same character boundary. **Crucial property**: the input must land on a UTF-8 char boundary; otherwise `remaining -= len_utf8` underflows and the loop walks past intended splits. yrs guarantees this because byte offsets in `OffsetKind::Bytes` mode come from the user counting bytes between whole characters (per the `text.rs:172-189` doc example).
- **`push_str`** is plain `SmallString::push_str` — append UTF-8 bytes. Used by `try_squash` (section 8) when two adjacent String Items merge.

Since we are UTF-16-only, our Go port of `block_offset` is a no-op for the user-facing input but we DO need the inverse direction: given a UTF-16 offset, find the byte index in the Go `string` for slicing. That's `split_str` (next).

### `split_str` (`block.rs:1571-1590`) — verbatim

```rust
pub(crate) fn split_str(str: &str, offset: usize, kind: OffsetKind) -> (&str, &str) {
    fn map_utf16_offset(str: &str, offset: u32) -> u32 {
        let mut off = 0;
        let mut i = 0;
        for c in str.chars() {
            if i >= offset {
                break;
            }
            off += c.len_utf8() as u32;
            i += c.len_utf16() as u32;
        }
        off
    }

    let off = match kind {
        OffsetKind::Bytes => offset,
        OffsetKind::Utf16 => map_utf16_offset(str, offset as u32) as usize,
    };
    str.split_at(off)
}
```

This is the **UTF-16-offset → UTF-8-byte-offset converter**. Given a UTF-16 offset `i`, walks characters accumulating both `len_utf8` (the byte index) and `len_utf16` (the UTF-16 index); stops when UTF-16 index reaches the target. Returns the byte index for `str.split_at`. Used by `ItemContent::splice` (next) on String content.

**Surrogate-split behaviour**: `if i >= offset` — when a 4-byte UTF-8 character (= 2 UTF-16 code units) straddles the requested offset, the loop *crosses* it (after the iteration: `i = prev_i + 2`). If `offset == prev_i + 1` (odd, mid-surrogate), the next iteration sees `i (=prev_i+2) >= offset (=prev_i+1)` and breaks immediately — having advanced `off` by the full character's `len_utf8`. **The split lands AFTER the character**, not inside it. So `split_str` silently rounds the offset *up* to the next whole character. **This is yrs's actual behaviour for split-inside-surrogate: the right half loses one notional UTF-16 unit, the left half gains it.** No error, no replacement character, no detection. Per the existing `block.md` note, this is buggy by JS Yjs's lights.

### `ItemContent::splice` for `String` (`block.rs:1925-1952`) — verbatim

```rust
pub(crate) fn splice(&mut self, offset: usize, encoding: OffsetKind) -> Option<ItemContent> {
    match self {
        ItemContent::Any(value) => { ... }
        ItemContent::String(string) => {
            // compute offset given in unicode code points into byte position
            let (left, right) = split_str(&string, offset, encoding);
            let left: SplittableString = left.into();
            let right: SplittableString = right.into();

            //TODO: do we need that in Rust?
            //let split_point = left.chars().last().unwrap();
            //if split_point >= 0xD800 as char && split_point <= 0xDBFF as char {
            //    // Last character of the left split is the start of a surrogate utf16/ucs2 pair.
            //    // We don't support splitting of surrogate pairs because this may lead to invalid documents.
            //    // Replace the invalid character with a unicode replacement character (� / U+FFFD)
            //    left.replace_range((offset-1)..offset, "�");
            //    right.replace_range(0..1, "�");
            //}
            *self = ItemContent::String(left);

            Some(ItemContent::String(right))
        }
        ItemContent::Deleted(len) => { ... }
        ItemContent::JSON(value) => { ... }
        _ => None,
    }
}
```

Line-by-line:

- `split_str(&string, offset, encoding)` carves the underlying UTF-8 bytes at the byte index that corresponds to the UTF-16 offset (with the round-up-on-surrogate behaviour above).
- Both halves wrap into fresh `SplittableString`s.
- **The U+FFFD-replacement block (lines 1940-1948) is COMMENTED OUT.** TODO comment: "do we need that in Rust?". JS Yjs DOES perform this replacement: when a surrogate pair is split, the left half's high surrogate and the right half's low surrogate are each replaced with U+FFFD (replacement character). yrs's no-op silently produces an invalid Unicode boundary in the right half (orphan low surrogate). For our port: **replicate the JS Yjs behaviour, not the yrs no-op.** Rationale: a Yjs-encoded update from JS will never produce mid-surrogate offsets in the first place (the JS API doesn't allow it), but a Go-only client could; emitting U+FFFD is the defined fallback.

### Note on storage representation

yrs stores text as UTF-8 (`SmallString`) and pays the walk cost on every `block_offset` call. A `SplittableString` of length N has `block_offset(_, Utf16)` cost O(N). For our Go port, the same trade-off applies: we can either store a Go `string` (UTF-8, walk per call) or precompute a UTF-16 index. **Recommend UTF-8 storage for parity**; revisit if benchmarks prove the walk dominates.

## 4. Branch lengths: `block_len` vs `content_len` for Text

Branch carries two counters (`branch.rs:203-207`):

```rust
pub block_len: u32,
pub content_len: u32,
```

For Array, `block_len == content_len == count of countable elements`. For Text:

- `block_len` is the sum of `Item.len` values for live countable items. `Item.len` is set at `Item::new` to `content.len(OffsetKind::Utf16)` (`block.rs:1307`). So `block_len` = **total UTF-16 code units across live String items** — already what we want.
- `content_len` is `block_len` updated through `content_len(encoding)` per item. For the default `OffsetKind::Bytes` doc, `content_len` is byte-count; for `OffsetKind::Utf16` doc (and JS-compatible), `content_len == block_len` for String items.

`Text::len` returns `branch.content_len` (`text.rs:160-162`). **For our UTF-16-only port, `content_len` and `block_len` are interchangeable for Text and we only need one counter.** Use `block_len` to match Array. The Branch Go struct does not need a second field. Maintenance is already in place: `Item::Integrate` increments `branch.block_len += item.Len` for `parent_sub == None && countable` items; `TransactionMut::delete` decrements (`transaction.rs:725-729` per the array.md notes).

## 5. Position-to-Item resolution for Text — verbatim

`find_position` (`text.rs:734-804`) — Text-specific equivalent of `Branch::index_to_ptr`. Returns an `ItemPosition { parent, left, right, index, current_attrs }` (`block.rs:996-1002`) that records the cursor between two items (or at branch start).

```rust
fn find_position(this: BranchPtr, txn: &mut TransactionMut, index: u32) -> Option<ItemPosition> {
    let mut pos = {
        ItemPosition {
            parent: this.into(),
            left: None,
            right: this.start,
            index: 0,
            current_attrs: None,
        }
    };

    let mut format_ptrs = HashMap::new();
    let store = txn.store_mut();
    let encoding = store.offset_kind;
    let mut remaining = index;
    while let Some(right) = pos.right {
        if remaining == 0 {
            break;
        }

        if !right.is_deleted() {
            match &right.content {
                ItemContent::Format(key, value) => {
                    if let Any::Null = value.as_ref() {
                        format_ptrs.remove(key);
                    } else {
                        format_ptrs.insert(key.clone(), pos.right.clone());
                    }
                }
                _ => {
                    let mut block_len = right.len();
                    let content_len = right.content_len(encoding);
                    if remaining < content_len {
                        // split right item
                        let offset = if let ItemContent::String(str) = &right.content {
                            str.block_offset(remaining, encoding)
                        } else {
                            remaining
                        };
                        store
                            .blocks
                            .split_block(right, offset, OffsetKind::Utf16)
                            .unwrap();
                        block_len -= offset;
                        remaining = 0;
                    } else {
                        remaining -= content_len;
                    }
                    pos.index += block_len;
                }
            }
        }
        pos.left = pos.right.take();
        pos.right = if let Some(item) = pos.left.as_deref() {
            item.right
        } else {
            None
        };
    }

    for (_, block_ptr) in format_ptrs {
        if let Some(item) = block_ptr {
            if let ItemContent::Format(key, value) = &item.content {
                let attrs = pos.current_attrs.get_or_init();
                update_current_attributes(attrs, key, value.as_ref());
            }
        }
    }

    Some(pos)
}
```

Line-by-line for our scope (ignore the Format branch + `current_attrs`, those are rich-text):

- Start with `pos = { parent: branch, left: None, right: branch.start, index: 0 }`. At index 0, the loop breaks immediately and returns this — the "before any item" cursor.
- Walk `right` rightward. **Skip deleted items entirely**: `pos.left = right; pos.right = right.right` advances past tombstones without consuming any of `remaining`. Tombstones contribute zero to user-facing positions but stay in the linked list.
- For `ItemContent::Format(...)`: track which formatting attrs are active at the cursor (rich-text bookkeeping). For our scope: skip — but in production text we WILL encounter Format items in the chain even on plain-text editing if a remote peer applied formatting. **Treat Format items as deleted-equivalent in our walk: advance, don't decrement `remaining`, don't split.** This is what yrs does (the `_ =>` arm runs only for non-Format content).
- For non-Format content (`String`, `Embed`, `Type`):
  - `block_len = right.len()` (UTF-16 units, the cached count).
  - `content_len = right.content_len(encoding)` — for `OffsetKind::Utf16`, equal to `block_len`; for `Bytes`, byte count. Plain-text-only port: use `right.len()` directly.
  - **`remaining < content_len` (interior hit) — SPLIT**. For `ItemContent::String`, convert `remaining` (UTF-16) to a UTF-16 byte offset via `str.block_offset(remaining, encoding)`; for `Embed`/`Type` (always `len == 1`), the offset is just `remaining`. Then `store.blocks.split_block(right, offset, OffsetKind::Utf16)` — **the split passes `OffsetKind::Utf16` regardless of doc encoding** because the underlying `Item::splice`/`ItemContent::splice` always speaks UTF-16 internally for storage layout.
  - **`remaining >= content_len` (boundary or beyond)** — decrement `remaining` and advance.
  - On exit (`remaining == 0`), `pos.right` points at the item to the right of the insert position; `pos.left` at the item to the left (or None).

**Critical invariants to mirror in Go:**

1. The split offset passed to `Store.SplitBlock` is **a UTF-16 offset within the String item**, derived from the user's UTF-16 cursor index minus the cumulative UTF-16 length of preceding items. There is no byte arithmetic at this layer.
2. **Deleted items do NOT consume cursor distance.** This matches Array's walk and our existing `findPosition`.
3. **Format items also do not consume cursor distance** (they're not countable). For our plain-text first commit, we still need to walk past them safely — they may exist in the chain even if we never create them, because applied updates from a JS peer can introduce them. Treat them as `is_deleted == true`-equivalent for walk purposes.
4. After the split, the loop exits with `pos.left = right` (the LEFT half post-split) and `pos.right = right.right` (the new RIGHT half post-split, which is what `split_block` linked into `right.right`). Same shape as `Branch::index_to_ptr`.

The `format_ptrs` post-loop block (`text.rs:794-801`) reconstructs the active attribute set at the cursor — pure rich-text. Drop in our port.

## 6. Insert path — verbatim

`Text::insert` (`text.rs:212-231`):

```rust
fn insert(&self, txn: &mut TransactionMut, index: u32, chunk: &str) {
    if chunk.is_empty() {
        return;
    }
    let this = BranchPtr::from(self.as_ref());
    if let Some(mut pos) = find_position(this, txn, index) {
        let value = crate::block::PrelimString(chunk.into());
        while let Some(right) = pos.right.as_ref() {
            if right.is_deleted() {
                // skip over deleted blocks, just like Yjs does
                pos.forward();
            } else {
                break;
            }
        }
        txn.create_item(&pos, value, None);
    } else {
        panic!("The type or the position doesn't exist!");
    }
}
```

Line-by-line:

- **Empty chunk: no-op.** Mirror in Go (`Text.Insert("")` returns nil silently).
- `find_position(this, txn, index)` resolves the cursor (section 5) — splitting any String item the index lands inside.
- `value = PrelimString(chunk.into())` — wraps the `&str` in the prelim that produces `ItemContent::String(SplittableString)`.
- **Deleted-skip loop** (`text.rs:219-225`): after `find_position`, walk `pos.right` past any deleted items. `pos.forward()` (`block.rs:1005-1027`) advances the cursor; for non-deleted String/Embed it would also advance `pos.index`, but here we only enter when `right.is_deleted()` so the index doesn't move. **Comment says "just like Yjs does."** Effect: the new Item is inserted *after* any tombstones at the cursor, attaching to the first live successor. This matters for YATA convergence: two concurrent inserts at the same index, one of them encountering tombstones, must agree on the same `right_origin`.
- `txn.create_item(&pos, value, None)` (`transaction.rs:863-910`) — see verbatim below. Constructs one Item, integrates it, pushes it.

`TransactionMut::create_item` (`transaction.rs:863-910`):

```rust
pub(crate) fn create_item<T: Prelim>(
    &mut self,
    pos: &ItemPosition,
    value: T,
    parent_sub: Option<Arc<str>>,
) -> Option<ItemPtr> {
    let (left, right, origin, id) = {
        let store = self.store_mut();
        let left = pos.left;
        let right = pos.right;
        let origin = if let Some(item) = pos.left.as_deref() {
            Some(item.last_id())
        } else {
            None
        };
        let client_id = store.client_id;
        let id = ID::new(client_id, store.get_local_state());

        (left, right, origin, id)
    };
    let (mut content, remainder) = value.into_content(self);
    let inner_ref = if let ItemContent::Type(inner_ref) = &mut content {
        Some(BranchPtr::from(inner_ref))
    } else {
        None
    };
    let mut block = Item::new(
        id,
        left,
        origin,
        right,
        right.map(|r| r.id().clone()),
        pos.parent.clone(),
        parent_sub,
        content,
    )?;
    let mut block_ptr = ItemPtr::from(&mut block);

    block_ptr.integrate(self, 0);

    self.store_mut().blocks.push_block(block);

    if let Some(remainder) = remainder {
        remainder.integrate(self, inner_ref.unwrap().into())
    }

    Some(block_ptr)
}
```

Same shape as `BlockIter::insert_contents` for Array (per the array.md):

- `id = ID::new(client_id, get_local_state())` — local clock.
- `origin = pos.left.last_id()` — last clock the left Item covers (matters for squashed runs).
- `right_origin = right.id` — Item ID of the right neighbour.
- `parent = pos.parent` (always `TypePtr::Branch(text_branch)` for our Text path).
- `parent_sub = None` — positional Item, not map-keyed.
- `Item::new(...)?` returns `None` if `content.len(Utf16) == 0`; the wrapper `Text::insert` already filters empty input, so this won't fire.
- `block_ptr.integrate(self, 0)` — YATA loop. For `parent_sub == None`: link into `branch.start` between `left` and `right`, increment `branch.block_len += item.len`, `branch.content_len += item.content_len(encoding)`. Already ported.
- `push_block` registers in BlockStore.
- `remainder.integrate(...)` is the nested-prelim recursion — for `PrelimString` the remainder is `None`, so this is a no-op. (For `TextPrelim` used as a nested type seed in another shared collection, this is where the seed string gets pushed into the freshly-integrated Branch.)

### Edge cases

- **Empty Text, insert at 0**: `find_position(0)` returns immediately with `pos.right = branch.start = None`, `pos.left = None`. New Item: `origin = None, left = None, right = None, right_origin = None`. `Item.Integrate` sees `left == None` and prepends to `branch.start` (already-ported behaviour).
- **Append (`index == len`)**: `find_position(len)` walks all live items; `remaining` reaches 0 just as `pos.right` becomes `None` (or stays on a tombstone). After the deleted-skip loop, `pos.right == None`, `pos.left == last_live_item`. New Item: `origin = last.last_id(), left = last, right = None, right_origin = None`. Appended.
- **Mid-string insert (target Item is `ContentString` with `Len > 1`)**: `find_position` calls `Store::split_block(right, utf16_offset, Utf16)`. The String item splits into a left half retaining `id` with the first `utf16_offset` UTF-16 units, and a right half with new ID `(client, clock + utf16_offset)` and the remaining UTF-16 units. `pos.left` becomes the new left half; `pos.right` becomes the new right half. The split offset INSIDE the String content is computed by `SplittableString::block_offset`, which we have already shown converts UTF-16-offset to whatever-the-encoding-is. With `OffsetKind::Utf16` everywhere, `block_offset` is the identity and the actual byte-slicing happens inside `ItemContent::splice` → `split_str(_, offset, Utf16)` → `map_utf16_offset` → `str.split_at(byte_index)`. **The byte-index resolution is the only place UTF-8 bytes show up in the Text hot path.**

### Multiple calls vs. squashing

`Text::insert("h", 0)` then `Text::insert("i", 1)` produces TWO Items, each with `len == 1`. Squashing happens at commit time via `try_squash` (section 8) — the two Items merge into one `ItemContent::String("hi")` with `len == 2` if and only if they are same-client + adjacent-clock + adjacent-position. **This is identical to Array's squashing behaviour.**

A user who knows they have a long string up-front should call `Text::push("hello world")` once, not character-by-character — it produces one Item directly without relying on commit-time merge.

## 7. Delete path — verbatim

`Text::remove_range` (`text.rs:361-368`):

```rust
fn remove_range(&self, txn: &mut TransactionMut, index: u32, len: u32) {
    let this = BranchPtr::from(self.as_ref());
    if let Some(mut pos) = find_position(this, txn, index) {
        remove(txn, &mut pos, len)
    } else {
        panic!("The type or the position doesn't exist!");
    }
}
```

`remove` free function (`text.rs:806-863`):

```rust
fn remove(txn: &mut TransactionMut, pos: &mut ItemPosition, len: u32) {
    let encoding = txn.store().offset_kind;
    let mut remaining = len;
    let start = pos.right.clone();
    let start_attrs = pos.current_attrs.clone();
    while let Some(item) = pos.right.as_deref() {
        if remaining == 0 {
            break;
        }

        if !item.is_deleted() {
            match &item.content {
                ItemContent::Embed(_) | ItemContent::String(_) | ItemContent::Type(_) => {
                    let content_len = item.content_len(encoding);
                    let ptr = pos.right.unwrap();
                    if remaining < content_len {
                        // split block
                        let offset = if let ItemContent::String(s) = &item.content {
                            s.block_offset(remaining, encoding)
                        } else {
                            len
                        };
                        remaining = 0;
                        txn.store_mut()
                            .blocks
                            .split_block(ptr, offset, OffsetKind::Utf16);
                    } else {
                        remaining -= content_len;
                    };
                    txn.delete(ptr);
                }
                _ => {}
            }
        }

        pos.forward();
    }

    if remaining > 0 {
        panic!(
            "Couldn't remove {} elements from an array. Only {} of them were successfully removed.",
            len,
            len - remaining
        );
    }

    if let (Some(start), Some(start_attrs), Some(end_attrs)) =
        (start, start_attrs, pos.current_attrs.as_mut())
    {
        clean_format_gap(txn, Some(start), pos.right, start_attrs.as_ref(), end_attrs.as_mut());
    }
}
```

Line-by-line:

- Walk `pos.right` rightward for up to `len` UTF-16 code units.
- **Deleted items**: skip via the outer `if !item.is_deleted()` guard, but `pos.forward()` still advances the cursor. Tombstones contribute zero to the delete budget.
- **String / Embed / Type items**: the only deletable content kinds. (Format items are skipped — they do not consume `remaining` and are not deleted; rich-text path handles them separately in `clean_format_gap`.)
  - `content_len = item.content_len(encoding)` — UTF-16 units for our port.
  - **`remaining < content_len` (mid-block end of range — END SPLIT)**: convert `remaining` to a String-internal UTF-16 offset via `s.block_offset(remaining, encoding)`; for Embed/Type the offset is `len` (this looks like a bug in yrs — should be `remaining` not `len` — but for `Len == 1` Embed/Type items the condition `remaining < 1` requires `remaining == 0` which already exits the loop, so the wrong value is never used). Then `split_block(ptr, offset, OffsetKind::Utf16)` carves the item into a left half (which we will tombstone) and a right half (which survives). `remaining = 0`.
  - **`remaining >= content_len` (full-item delete)**: subtract and continue.
  - `txn.delete(ptr)` (`transaction.rs:718+`) tombstones the item: sets the deleted flag, decrements `parent.block_len` and `parent.content_len`, inserts `(item.id, item.len)` into `txn.delete_set`, schedules an event for the parent Branch.
- `pos.forward()` advances to the next item.
- **Panic on under-delivery**: `remaining > 0` after the loop means the user asked to delete past the end. **Mirror as a returned error** in our Go port (we use `error` instead of panic per the `Array.RemoveRange` precedent).

The trailing `clean_format_gap` (`text.rs:852-862`) is rich-text bookkeeping: when a delete range starts/ends inside a formatting region, ensure attribute markers are balanced. Drop in our port.

**Note on START split**: unlike yrs Array's `BlockIter::delete` which handles both start-split and end-split inside the delete loop, here the **start split was already done by `find_position`** when the user's `index` landed mid-block. So `remove` only needs to handle the end split. Cleaner separation — mirror this in Go.

## 8. TrySquash for `ContentString` — verbatim

`ItemContent::try_squash` (`block.rs:1972-1993`):

```rust
pub fn try_squash(&mut self, other: &Self) -> bool {
    //TODO: change `other` to Self (not ref) and return type to Option<Self> (none if merge succeeded)
    match (self, other) {
        (ItemContent::Any(v1), ItemContent::Any(v2)) => {
            v1.append(&mut v2.clone());
            true
        }
        (ItemContent::Deleted(v1), ItemContent::Deleted(v2)) => {
            *v1 = *v1 + *v2;
            true
        }
        (ItemContent::JSON(v1), ItemContent::JSON(v2)) => {
            v1.append(&mut v2.clone());
            true
        }
        (ItemContent::String(v1), ItemContent::String(v2)) => {
            v1.push_str(v2.as_str());
            true
        }
        _ => false,
    }
}
```

For two adjacent same-client adjacent-clock String Items: `v1.push_str(v2.as_str())` — UTF-8 byte concat. After squash, the merged Item's `len` (UTF-16 units) is the sum of the originals' `len`s; `block_len` and `content_len` on the parent Branch are unaffected (the counters track totals across live items, and the squash neither adds nor removes content).

**Cross-check against our existing `Content.TrySquash` for `KindString`** (`internal/block/content.go:201-202`): `c.Str += other.Str`. Already correct — **no change needed**. The squash predicate (same client, adjacent clocks, parent + parent_sub match, both live, both undeleted) is in `Item.TrySquash` and is type-agnostic.

## 9. TextEvent shape

`TextEvent` (`text.rs:1213-`):

```rust
pub struct TextEvent {
    pub(crate) current_target: BranchPtr,
    target: TextRef,
    delta: UnsafeCell<Option<Vec<Delta>>>,
}

impl TextEvent {
    pub fn target(&self) -> &TextRef { ... }
    pub fn path(&self) -> Path { ... }
    pub fn delta(&self, txn: &TransactionMut) -> &[Delta] { ... }   // lazily computed
}
```

`Delta` (Quill-style): `Inserted(Out, Option<Box<Attrs>>)`, `Deleted(u32)`, `Retain(u32, Option<Box<Attrs>>)`. The `delta()` method walks the linked list cross-referencing `txn.before_state` / `txn.delete_set` to reconstruct the per-transaction delta — implementation at `text.rs:1249-` is ~200 lines because of Format-marker reconciliation.

**Out of scope for first commit.** When we add Text observers, the lazy-delta computation is non-trivial; defer alongside Map/Array observers. A `Text` Branch still participates in transaction-end change tracking, but no observer callback fires until we wire up the event surface.

## 10. Concrete Go translation choices

### File layout

- `internal/types/text.go`, `internal/types/text_test.go`. Mirror of `array.go`/`map.go`.
- Doc-level registry: `Doc.Text(name string) *types.Text` parallel to `Doc.Array` / `Doc.Map`. Same `roots map[string]*block.Branch`; the Branch's `TypeRef` distinguishes — add `block.TypeRefText` if not present.

### `Text` struct

```go
// internal/types/text.go
type Text struct {
    branch *block.Branch
}

func newText(branch *block.Branch) *Text { return &Text{branch: branch} }

func (t *Text) Length(_ doc.ReadTxn) int { return int(t.branch.BlockLen) }
```

Same thin pointer-wrapper. For UTF-16-only port, `Branch.BlockLen` IS the total UTF-16 code units across live String items (because `Item.Len` is set to UTF-16 count at construction). No second counter needed.

### UTF-16 helpers — new package

Add `internal/utf16/utf16.go` (small, focused; avoids cluttering `internal/block`):

```go
// Length returns the number of UTF-16 code units required to encode s.
// ASCII char = 1, BMP char = 1, non-BMP char (emoji) = 2.
func Length(s string) uint64

// ByteOffset converts a UTF-16 code unit offset into the corresponding
// UTF-8 byte index in s. ok=false if the offset lands inside a surrogate
// pair (i.e. between the two UTF-16 units of a 4-byte UTF-8 char). When
// ok=false, the returned byte index is the byte boundary AFTER the
// straddled char (matching yrs split_str's silent round-up behaviour);
// the bool lets callers decide to apply U+FFFD replacement instead.
func ByteOffset(s string, utf16Offset uint64) (byteIdx int, ok bool)

// SplitAt splits s at the given UTF-16 offset. If the offset lands inside
// a surrogate pair, both halves' boundary chars are replaced with U+FFFD
// (matching JS Yjs behaviour, NOT yrs's no-op).
func SplitAt(s string, utf16Offset uint64) (left, right string)
```

Implementation walks `for _, r := range s` accumulating `utf8.RuneLen(r)` for byte index and `1 + boolToInt(r > 0xFFFF)` for UTF-16 index. Standard `utf16.Encode` allocates; we can do it in-place because Go runes give us the codepoint directly.

### `Content.Len(KindString)` change

Update `internal/block/content.go:98-104` from `uint64(len(c.Str))` to `utf16.Length(c.Str)`. **Behavioural change**: existing callers that compared `Content.Len` against byte counts will break. **Survey**:

- `Map.Set` wraps in `KindAny`, not `KindString` (`internal/types/map.go:62`). Safe — Map fixture tests with non-ASCII Map values exercise `KindAny.Len = len(Anys)`, which is already correct.
- `Array.Insert` wraps in `KindAny` for `Into<Any>` values, which includes strings as `Any.Str`. Strings inside `KindAny` are NOT `KindString` items; their length contribution is "1 per element" via `len(c.Anys)`. Safe.
- `Item.TrySquash` uses `Item.Len` arithmetic; squashing two String items concats `c.Str` and the resulting `Item.Len = utf16.Length(merged)`. **Verify**: the squash code in `Item.TrySquash` should recompute `Item.Len = a.Len + b.Len` (not re-derive from content). If it re-derives, recomputation needs to call the new UTF-16-aware `Len`. Either is correct as long as consistency holds.
- Wire encode/decode reads/writes `Item.Len` directly. With `Item.Len = UTF-16 units`, this matches the JS Yjs wire format — unchanged.

### `Content.SplitString` change

Update `internal/block/content.go:146-150` to:

1. Accept the offset as **UTF-16 units** (not bytes).
2. Call `utf16.SplitAt(c.Str, offset)` → `(left, right string)` with U+FFFD replacement on surrogate-split.
3. Set `c.Str = left`; return `Content{Kind: KindString, Str: right}`.

### `Text.Insert(txn, idx, str)` API

```go
// internal/types/text.go
//
// Insert inserts str at the UTF-16 code-unit position idx. idx must be in
// [0, t.Length(txn)]. Returns an error if out of range.
//
// Multiple consecutive Insert calls produce separate Items; commit-time
// squash merges adjacent same-client adjacent-clock String items.
func (t *Text) Insert(tx *doc.TransactionMut, idx uint64, str string) error {
    if str == "" {
        return nil
    }
    length := uint64(t.branch.BlockLen)
    if idx > length {
        return fmt.Errorf("ygo: text index %d out of range [0, %d]", idx, length)
    }
    pos, err := findTextPosition(tx, t.branch, idx)
    if err != nil {
        return err
    }
    // Skip past tombstones at the cursor (matches text.rs:219-225)
    for pos.Right != nil && pos.Right.Deleted() {
        pos.forward()
    }
    content := block.Content{Kind: block.KindString, Str: str}
    item := block.NewTextInsertItem(tx, t.branch, pos.Left, pos.Right, content)
    return item.Integrate(tx, 0)
}
```

`findTextPosition` is a near-copy of `Array.findPosition` but the unit is UTF-16 code units of String items (and Format items are walk-through, just like deleted items). For our first commit we will not yet encounter Format items in any test — but the walk should already tolerate them (skip without consuming `remaining`) so we don't break when rich-text peers join.

### `Text.Delete(txn, idx, length)` API

```go
func (t *Text) Delete(tx *doc.TransactionMut, idx, length uint64) error {
    total := uint64(t.branch.BlockLen)
    if idx+length > total {
        return fmt.Errorf("ygo: text range [%d, %d) out of bounds [0, %d)", idx, idx+length, total)
    }
    if length == 0 {
        return nil
    }
    pos, err := findTextPosition(tx, t.branch, idx)  // start split done here
    if err != nil {
        return err
    }
    return removeTextRange(tx, pos, length)          // end split + tx.Delete loop
}
```

Direct port of `text.rs:806-851`'s `remove`, minus the rich-text `start_attrs`/`clean_format_gap` block.

### `Text.String() string` API

```go
func (t *Text) String(_ doc.ReadTxn) string {
    var b strings.Builder
    for cur := t.branch.Start; cur != nil; cur = cur.Right {
        if cur.Deleted() { continue }
        if cur.Content.Kind == block.KindString {
            b.WriteString(cur.Content.Str)
        }
    }
    return b.String()
}
```

Direct port of `text.rs:120-132`. Skips Format / Embed / Type / Deleted by virtue of the Kind check; concatenates only KindString. Returns Go UTF-8 string — no UTF-16 conversion at the read boundary.

### `block.NewTextInsertItem` factory

Identical to `NewArrayInsertItem` (per array.md): `parent_sub == ""`, `right_origin = right.Id`, `origin = left.LastID()`. The factory is type-agnostic; the only difference for Text is that the caller hands it a `KindString` Content. **Recommend reusing `NewArrayInsertItem`** since the construction logic is identical — rename to `NewPositionalInsertItem` if naming reads cleaner, or keep two trivially-aliased factories.

## 11. Out of scope for first Text commit

Mention only — defer all of these to a future rich-text commit:

- **`ContentFormat`** (`block.rs` ItemContent variant + `text.rs:756-762` find_position handling + `text.rs:875-`'s `insert_format`): rich-text attribute markers (bold, italic, etc.) inserted as zero-length items wrapping a range. Already a defined ContentKind in our `internal/block/content.go` but no producer.
- **`Text::apply_delta`** (`text.rs:233-265`): Quill-style delta (`Inserted` / `Deleted` / `Retain`) batch application. Single biggest API surface for rich-text editing in Yjs.
- **`Text::insert_with_attributes`** (`text.rs:275-292`): inserts text wrapped in a Format range.
- **`Text::insert_embed`** / `insert_embed_with_attributes` (`text.rs:301-350`): inserts a non-text payload (binary, nested shared type) inline in the text stream as `ItemContent::Embed` or `ItemContent::Type`.
- **`Text::format`** (`text.rs:372-379`): wraps an existing range with formatting attributes.
- **`Text::diff`** / `diff_range` (`text.rs:288-310`-ish): returns `Vec<Diff>` of formatted chunks — the read-with-formatting counterpart to `get_string`.
- **`TextEvent::delta`** (`text.rs:1242-1300+`): per-transaction Quill delta for observers. Lazy-computed; non-trivial ~200-line walker. The yjs.dev docs flag the delta detail with a [todo] of their own; our port can defer with citation.

## 12. Tests for the first Text commit

### Go-only tests (`internal/types/text_test.go`)

1. **Empty Text**: `t.Length(tx) == 0`; `t.String(tx) == ""`.
2. **Insert at 0 in empty**: `t.Insert(tx, 0, "hello")`. Verify `branch.Start` points at one Item with `Left == nil`, `Right == nil`, `Origin == nil`, `RightOrigin == nil`, `Content.Kind == KindString`, `Content.Str == "hello"`, `Item.Len == 5`, `branch.BlockLen == 5`. `t.String(tx) == "hello"`.
3. **Insert at end**: build "hello", then `Insert(5, " world")`. Verify two items linked left-to-right, second's `Left == first`, `Origin == first.LastID()`, `Right == nil`. `String == "hello world"`, `Length == 11`.
4. **Insert in middle (split)**: build "helloworld" as one Item, then `Insert(5, " ")`. Verify the Item split into `"hello"` (left half retains original ID) + `" "` (the new insert, fresh ID) + `"world"` (right half, ID `(client, clock+5)`). `String == "hello world"`, `Length == 11`. Inspect linked-list integrity.
5. **Insert sequence builds via TrySquash**: `Insert(0, "a")`, `Insert(1, "b")`, `Insert(2, "c")` in the same transaction. After commit, verify only ONE Item exists with `Content.Str == "abc"`, `Item.Len == 3`, `branch.BlockLen == 3`. (Pre-commit may show 3 items.)
6. **Delete single char**: build "hello", `Delete(2, 1)`. Verify `String == "helo"`, `Length == 4`. The original Item is split into `"he"` + tombstoned `"l"` + `"lo"`.
7. **Delete range**: build "hello world", `Delete(5, 1)` removes the space. Then `Delete(0, 5)` removes "hello". Verify `String == "world"`.
8. **Delete entire**: build "hello", `Delete(0, 5)`. `String == ""`, `Length == 0`. Tombstone retained in linked list.
9. **Length matches String length in UTF-16 units**: insert "a😀b" (where 😀 = U+1F600, 4 UTF-8 bytes, 2 UTF-16 units). `Length == 4` (1 + 2 + 1). `String == "a😀b"` (4 bytes representation per Go).
10. **Mid-surrogate split rejection / U+FFFD**: build "😀😀" in one Item (`Length == 4`). `Insert(1, "X")` — split lands in the middle of the first surrogate pair. With U+FFFD replacement (recommended): result is `"�" + "X" + "�😀"`, `Length == 5`. **Test asserts the U+FFFD behaviour and contrasts with yrs's silent round-up.** Document the choice.
11. **Walk past tombstones on insert**: build "hello", `Delete(2, 2)` (tombstone "ll"), `Insert(2, "X")`. Verify "X" is inserted *after* the tombstones, producing `String == "heXo"`. Linked list: `"he"` + tombstoned `"ll"` + `"X"` + `"o"`.

### Cross-language tests (against JS Yjs fixtures)

1. **Pure ASCII**: JS Yjs `text.insert(0, "hello")` → encode update → Go decode + apply → `Text.String() == "hello"`, `Length == 5`.
2. **Cyrillic / 2-byte UTF-8**: JS `text.insert(0, "привет")` → Go applies → `String == "привет"`, `Length == 6` (each Cyrillic letter is 1 UTF-16 unit, 2 UTF-8 bytes).
3. **Emoji / non-BMP**: JS `text.insert(0, "😀")` → Go applies → `String == "😀"`, `Length == 2` (one surrogate pair). Verify the byte-level wire content matches.
4. **Mid-string insert preserving offsets**: JS `text.insert(0, "hello"); text.insert(5, " "); text.insert(6, "world")` → Go applies → `String == "hello world"`. Bidirectional: have Go produce the same updates and confirm JS decodes to "hello world".
5. **Mid-string delete**: JS `text.insert(0, "hello world"); text.delete(5, 1)` → Go applies → `String == "helloworld"`.
6. **Concurrent inserts at same index** (round-trip): build same initial state on both sides, both insert at index 0 in disjoint transactions, exchange updates, verify both sides converge to the same string and same item ordering (lower client-id wins per YATA).

These exercise: `findTextPosition`, the split path with UTF-16 offsets, `Text.String` round-trip, squashing under cross-client encoding, and the U+FFFD / surrogate handling on the read boundary.

## 13. Open questions / FIXMEs in source

- **`block.rs:1940-1948`** (`ItemContent::splice` for String): commented-out U+FFFD replacement on surrogate-pair split. yrs TODO: "do we need that in Rust?" — answer per JS Yjs is "yes". **Our port: implement it** (recommended in section 10's `utf16.SplitAt`).
- **`text.rs:826`** (`remove`): `let offset = ... else { len }` — the `else` arm uses the outer `len` parameter instead of `remaining`. Looks like a typo; harmless because Embed/Type have `Len == 1` and the branch is only reached when `remaining < 1`, which means `remaining == 0` and the loop has already exited. **Don't replicate the typo in Go.**
- **`block.rs:1973`** (`try_squash`): TODO to change `other` from ref to owned `Self` and return `Option<Self>`. API-shape only; doesn't affect behaviour.
- **`text.rs:570-571`** (the comment in `Item::integrate`): "offset could be > 0 only in context of Update::integrate, in such case offset kind in use always means Yjs-compatible offset (utf-16)". Confirms wire is always UTF-16. We hard-code this throughout.
- **`branch.rs:203-207`**: `block_len` and `content_len` are both maintained but for UTF-16-only docs they hold the same value for Text. yrs keeps both for `OffsetKind::Bytes` mode. Our port collapses to one (`BlockLen`).
- **`text.rs:219-225`** (deleted-skip loop in `insert`): comment "skip over deleted blocks, just like Yjs does". This is a YATA-convergence requirement, not a perf optimisation — concurrent inserts must agree on which live successor they attach to. **Must replicate exactly.**
- **`text.rs:756-762`** (Format handling in `find_position`): on encountering an active format marker mid-walk, register it in `format_ptrs`; on encountering a `Null`-valued format marker (= clear), remove from `format_ptrs`. We can ignore the `format_ptrs` machinery for plain text but must still walk past Format items without consuming `remaining`.
