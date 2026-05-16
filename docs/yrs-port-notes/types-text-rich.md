# yrs port notes: rich-text Text formatting (ContentFormat, ContentEmbed, ApplyDelta, ToDelta)

> Sources: yrs `main` — `yrs/src/types/text.rs` (`Text` trait rich-text methods, `find_position` Format handling, `insert`/`insert_format`/`insert_attributes`/`insert_negated_attributes`/`minimize_attr_changes`/`update_current_attributes` free functions, `DiffAssembler`), `yrs/src/block.rs` (`ItemContent::Format` and `Embed` variants, encode/decode, `is_countable`, `len`, `try_squash`, ref-number constants `BLOCK_ITEM_FORMAT_REF_NUMBER = 6` and `BLOCK_ITEM_EMBED_REF_NUMBER = 5`). JS upstream — `yjs/src/structs/Item.js` (`class ContentEmbed` 1008-1088, `class ContentFormat` 1093-1182), `yjs/src/ytype.js` (`ItemTextListPosition.forward`/`formatText` 96-213, `insertNegatedAttributes` 226-251, `updateCurrentAttributes` 260-267, `minimizeAttributeChanges` 276-288, `insertAttributes` 300-318, `insertContent` 330-350, `insertContentHelper` 359-382, `deleteText` 393-440, `YType.applyDelta` 1078-1120, `YType.toDelta` 835-1000+, `readContentEmbed` 2061, `readContentFormat` 2055). All line numbers accurate at fetch time.

Rich-text formatting is what turns Y.Text from a plain string CRDT into the document model Quill, ProseMirror, Tiptap, BlockNote and Lexical actually mount on top of: every "bold this range", "set link", "wrap heading 2" operation is encoded as zero-length `ContentFormat` marker Items spliced into the same linked list that already carries the character payloads, with embeds (inline images, mentions, math) as single-element `ContentEmbed` Items occupying one cursor position. Because XmlText is *literally* the same shared type with a different `TypeRef` discriminator (`TypeRefXmlText = 6`, see `nested-types.md` §1), every XML-flavoured editor stack — ProseMirror, Tiptap, the XmlFragment/XmlElement family — bottoms out in the rich-text Text machinery; without it, ygo can encode the structure of an XmlElement tree but not the runs of bold/italic/href inside the leaves, which is to say: not the actual editable document. The format-marker-Item model is YATA-native — markers carry the same Origin/RightOrigin/Parent fields as text Items, integrate into `branch.Start` the same way, are commutative under concurrent application, and survive deletion the same way — so the entire rich-text surface reduces to (a) two new wire-codec arms, (b) a small position-tracking sidecar that records "what formatting is active here" while walking the list, and (c) three new high-level APIs (`Format`, `InsertWithAttributes`, `InsertEmbed`, `ApplyDelta`, `ToDelta`) layered on the plain-text `Insert`/`Delete` already shipped.

---

## §1 ContentFormat wire format — byte-level

The Format variant is `ItemContent::Format(Arc<str>, Box<Any>)` (`yrs/src/block.rs:1636`). Doc-comment verbatim: *"Formatting attribute entry. Format attributes are not considered countable and don't contribute to an overall length of a collection they are applied to."* — `block.rs:1636`. Wire ref-number is **6**, declared `pub const BLOCK_ITEM_FORMAT_REF_NUMBER: u8 = 6;` at `block.rs:32` and confirmed by `ContentFormat.getRef()` returning the literal `6` in `yjs/src/structs/Item.js:1176-1181`.

```
Item header bits     // info byte; low 4 bits = content ref number = 6 (KindFormat)
…                    // standard Item fields: ID, origin, rightOrigin, parent, parentSub, length
ContentFormat-body :=
    varstring(key)   // lib0 writeKey  — the attribute name e.g. "bold", "color", "href"
    json(value)      // lib0 writeJSON — the attribute value; JSON-encoded lib0 Any
```

Encode is `block.rs:1844-1846`:

```rust
ItemContent::Format(k, v) => {
    encoder.write_key(k.as_ref());
    encoder.write_json(v.as_ref());
}
```

JS-side encode is `ContentFormat.write` at `yjs/src/structs/Item.js:1171-1174`:

```js
write (encoder, _offset, _offsetEnd) {
  encoder.writeKey(this.key)
  encoder.writeJSON(this.value)
}
```

Decode is `block.rs:1872-1876`:

```rust
BLOCK_ITEM_FORMAT_REF_NUMBER => Ok(ItemContent::Format(
    decoder.read_key()?,
    decoder.read_json()?.into(),
)),
```

JS-side decode is `readContentFormat` at `yjs/src/ytype.js:2055-2056`:

```js
export const readContentFormat = decoder =>
  new ContentFormat(decoder.readKey(), decoder.readJSON())
```

`write_key` / `read_key` use lib0's `writeAny`-string-with-key-cache encoding (the same key-cache the V2 encoder uses for ParentSub names — V1 emits it as a plain varstring). `write_json` / `read_json` is `Any` JSON-stringified to a varstring and parsed on read; not the binary lib0 Any encoding. Both sides agree. Important: **the `value` of a clear-attribute marker is `Any::Null`** (`yrs/src/types/text.rs:800-806` checks `if let Any::Null = value.as_ref()`); JS uses literal `null` (`ytype.js:262`). Receivers MUST interpret a Null-valued Format marker as *unset this attribute*, not as *set it to the value null*. The same wire byte carries both.

Go port: `internal/block/content.go:48,52` already reserves `Anys []Any` (Format value lives at `Anys[0]`) and `FormatKey string` (Format key); `content.go:24-25` already declares `KindFormat = 6`. The missing piece is the `internal/encoding/content_codec.go` arm — today the `KindFormat` case is in the "Skipped" comment at line 17. Add encoder: `lib0.AppendVarString(buf, c.FormatKey)` then `any_codec.AppendAnyJSON(buf, c.Anys[0])`; decoder: read varstring → `c.FormatKey`, read JSON-encoded Any → `c.Anys = []Any{val}`. Both helpers already exist in `internal/lib0` and `internal/encoding/any_codec.go`.

---

## §2 ContentEmbed wire format — byte-level

The Embed variant is `ItemContent::Embed(Any)` (`yrs/src/block.rs:1632`), doc-comment *"A single embedded JSON-like primitive value."*. Wire ref-number is **5**, `pub const BLOCK_ITEM_EMBED_REF_NUMBER: u8 = 5;` at `block.rs:35`, confirmed by `ContentEmbed.getRef()` returning `5` at `yjs/src/structs/Item.js:1083-1087`.

```
Item header bits      // info byte; low 4 bits = content ref number = 5 (KindEmbed)
…                     // standard Item fields: ID, origin, rightOrigin, parent, parentSub, length=1
ContentEmbed-body :=
    json(value)       // lib0 writeJSON — the embedded payload, a lib0 Any
```

Encode (`block.rs:1842-1843`):

```rust
ItemContent::Embed(s) => encoder.write_json(s),
```

JS-side encode at `yjs/src/structs/Item.js:1078-1080`:

```js
write (encoder, _offset, _offsetEnd) {
  encoder.writeJSON(this.embed)
}
```

Decode (`block.rs:1871`): `BLOCK_ITEM_EMBED_REF_NUMBER => Ok(ItemContent::Embed(decoder.read_json()?.into()))`. JS-side at `yjs/src/ytype.js:2061-2062`: `export const readContentEmbed = decoder => new ContentEmbed(decoder.readJSON())`.

Single value, no key, no length prefix beyond the standard Item `length` field (which is always 1 for Embed — `ContentEmbed.getLength()` returns `1` at `Item.js:1019-1021`). The payload is *any* JSON-encodable shape: typical editor uses are `{ image: "https://…" }`, `{ mention: { userId: "abc" } }`, `{ formula: "x^2" }`. The Embed payload is opaque to ygo — applications decode the JSON Any themselves.

Go port: extend `Content` to use the existing `Anys []Any` field for `KindEmbed` (the field comment at `content.go:48` already lists "KindEmbed (1 elem)" as a planned tenant). Encoder: `any_codec.AppendAnyJSON(buf, c.Anys[0])`. Decoder: read JSON Any → `c.Anys = []Any{val}`. `Content.Len(KindEmbed)` already returns 1 at `content.go:109-110`. `Content.IsCountable()` already returns true at `content.go:74-79` (Embed is not in the false-list).

---

## §3 Format-marker semantics — zero-length on wire, zero contribution to user-facing length

Format markers are zero-length-on-cursor: they occupy a slot in `branch.Start`'s linked list (with their own Origin, RightOrigin, parent, clock) but contribute 0 to the UTF-16 character index. Two upstream confirmations:

1. **`is_countable() == false` for Format**. yrs `block.rs` `ItemContent::is_countable` returns `false` for Format (the variant is in the false-list alongside Deleted, Move); confirmed by JS `ContentFormat.isCountable()` returning `false` at `Item.js:1120-1122`. Per the comment at `block.rs:1636` quoted in §1: "Format attributes are not considered countable and don't contribute to an overall length of a collection".
2. **`getContent()` returns `[]`** at `Item.js:1112-1115`: `getContent () { return [] }` — for delta/diff iteration purposes the Format marker has no visible content, only an attribute-state-change side effect on the cursor.

`Item::Integrate` (already-ported, `internal/block/integrate.go`) decides whether to add this Item's `Len` to the parent Branch's `BlockLen` based on the `Item.Countable` flag, which is set from `Content.IsCountable()` at construction. **Verify**: our existing `content.go:73-80` already returns `false` for `KindFormat`. So **Format Items, when integrated, do NOT increment `branch.BlockLen`** — exactly what we want. They DO still link into `branch.Start` (the YATA linked-list insertion in `Item::Integrate` is unconditional on Countable), they DO still hold a clock and consume one slot in `BlockStore`, they DO still tombstone normally under delete. **Two precise statements**:

- `Text.Length()` (our existing `internal/types/text.go:42`, reads `branch.BlockLen`) is unaffected by Format markers — the count is total UTF-16 code units of live String items plus 1-per-Embed plus 1-per-Type, and Format markers contribute 0. This matches `Text::len` in yrs at `text.rs:160-162` which reads `branch.content_len`.
- `branch.BlockLen` is NOT a count of all integrated Items — it is a count of *countable* item-len contributions. The total number of Items linked into `branch.Start` (live + tombstoned + Format markers + Embed + Type + String) is a different number, not currently surfaced and not equal to `BlockLen`. yrs doesn't expose it either. Don't accidentally bump `branch.BlockLen` in the integrate path for Format Items.

**Position-walking semantics**: when `find_position` or any cursor walker traverses a Format marker, it MUST advance past the marker without decrementing the remaining-distance budget. This is exactly the same rule as for tombstones. yrs's `find_position` already does this — `text.rs:778-838` quoted in `types-text.md` §5. Our existing `findTextPosition` in `internal/types/text.go` already treats non-`KindString` Items as walk-through (see `text.go:155-157, 207-208`), so it tolerates Format Items appearing in the chain even before we have a producer. **No change needed to plain-text walkers** — they already do the right thing. What we need to add is *sidecar state* that records *which* attributes are currently active at the cursor.

**Walk-left to determine current formatting**. To know "what formatting is active at index `i`?" you walk the linked list from `branch.Start` to position `i`, accumulating into an `Attrs map[string]any` every Format marker you pass: yrs `text.rs:800-806` inside `find_position`:

```rust
ItemContent::Format(key, value) => {
    if let Any::Null = value.as_ref() {
        format_ptrs.remove(key);
    } else {
        format_ptrs.insert(key.clone(), pos.right.clone());
    }
}
```

…then at `text.rs:830-837` after the loop, the active `format_ptrs` are reduced into `pos.current_attrs` via `update_current_attributes` (`text.rs:763-776`):

```rust
pub(crate) fn update_current_attributes(attrs: &mut Attrs, key: &str, value: &Any) {
    if let Any::Null = value {
        attrs.remove(key);
    } else {
        attrs.insert(key.into(), value.clone());
    }
}
```

JS-side equivalent is `ItemTextListPosition.forward` at `ytype.js:115-131`, which calls `updateCurrentAttributes` on every Format Item it passes (when not deleted). **No left/right asymmetry — the walk is always left-to-right from branch start; "walk-left" in the spec colloquially means "from the start up to but not past the cursor".**

---

## §4 Text.Format(idx, len, attrs) — apply attributes to an existing range

API: `func (t *Text) Format(tx *doc.TransactionMut, idx, length uint64, attrs Attrs) error`. yrs surface is `Text::format` at `text.rs:339-356`:

```rust
fn format(&self, txn: &mut TransactionMut, index: u32, len: u32, attributes: Attrs) {
    let this = BranchPtr::from(self.as_ref());
    if let Some(mut pos) = find_position(this, txn, index) {
        insert_format(this, txn, &mut pos, len, attributes)
    } else {
        panic!("Index {} is outside of the range.", index);
    }
}
```

`insert_format` is `text.rs:866-939`; the equivalent JS-side combined entry-point is `ItemTextListPosition.formatText` at `ytype.js:141-212`, which is the easier source to port because it bundles the three subroutines in execution order. Sketch:

1. **Resolve cursor at `idx`** via `find_position` / `findTextPosition`, populating `pos.current_attrs` from the format markers encountered en route (§3). Start-split happens here if `idx` lands mid-String.
2. **`minimize_attr_changes(pos, attrs)`** (`text.rs:751-761` is implicit inside `insert`; JS standalone at `ytype.js:276-288`). Walk forward past any Format markers at the cursor whose `(key, value)` already equals what the user is requesting — these are no-ops, skip them so we don't emit redundant markers.
3. **`insert_attributes(branch, txn, pos, attrs)`** (`text.rs:741-761` calls it; standalone JS at `ytype.js:300-318`). For each `(key, val)` in `attrs`: compare against `pos.current_attrs.get(key)`. If unchanged, skip. If changed, **emit a new `KindFormat` Item** at the cursor with `(key, val)`, integrate it via the usual YATA path, advance the cursor past it. Build `negated_attrs: map[string]any` mapping each emitted key to the value that was active before (so we know what to restore at the range end).
4. **Walk forward `length` UTF-16 units**, mirroring the loop body in `ItemTextListPosition.formatText` at `ytype.js:148-207`:
   - On hitting a **String/Embed/Type Item**: subtract its content-length from `length`; on a mid-block hit, end-split via `getItemCleanStart` (our `Store.SplitBlock`) so the format boundary lands cleanly. Advance.
   - On hitting a **Format Item** with `key` in `attrs`: if its value equals the new `attrs[key]`, **delete the old marker** (tombstone it via `txn.Delete`) — it's now redundant because our just-emitted opening marker has the same effect; also remove from `negated_attrs` since we no longer need to restore. If its value differs and we still have `length > 0`, update `negated_attrs[key] = oldValue` so the closing marker restores the right thing. If `length == 0` and the cursor is in a trailing-Format region with a key we didn't override, break (no need to clobber unrelated formatting past the range).
   - On hitting a **Format Item** with `key` NOT in `attrs`: just record it into `pos.current_attrs` and advance — it's outside our scope of changes.
5. **`insert_negated_attributes(branch, txn, pos, negated_attrs)`** (`text.rs` free function; JS at `ytype.js:226-251`). At the now-advanced cursor (end of range), for each key in `negated_attrs` that wasn't already neutralised by a Format marker we passed, emit a closing `KindFormat` Item restoring the previous value (or `Any::Null` to clear). Walk past trailing redundant Format markers first (`ytype.js:228-240`) to avoid stacking duplicates.

This is significantly more involved than plain-text `Insert`. The Go port should live in a dedicated file (see §9) and use a `cursorState` struct mirroring `ItemTextListPosition`:

```go
type textCursor struct {
    left, right *block.Item
    index       uint64
    attrs       Attrs    // currentAttributes — accumulated by walk
}
func (c *textCursor) forward() { /* per ytype.js:115-131 */ }
```

`Text.Format` itself is thin:

```go
func (t *Text) Format(tx *doc.TransactionMut, idx, length uint64, attrs Attrs) error {
    if length == 0 || len(attrs) == 0 { return nil }
    cur, err := findTextCursor(tx, t.branch, idx)   // §3 walk, populates cur.attrs
    if err != nil { return err }
    return formatRange(tx, t.branch, cur, length, attrs)
}
```

`formatRange` is the loop in step 4 + bookends in 3/5. Concrete shape: ~120 lines of Go, dominated by the format-marker dance — straightforward translation of `ytype.js:141-212`.

---

## §5 Text.InsertWithAttributes(idx, str, attrs)

API: `func (t *Text) InsertWithAttributes(tx *doc.TransactionMut, idx uint64, str string, attrs Attrs) error`. yrs is `Text::insert_with_attributes` at `text.rs:280-295`:

```rust
fn insert_with_attributes(&self, txn: &mut TransactionMut, index: u32, chunk: &str, attributes: Attrs) {
    if chunk.is_empty() { return; }
    let this = BranchPtr::from(self.as_ref());
    if let Some(mut pos) = find_position(this, txn, index) {
        let value = block::PrelimString(chunk.into());
        insert(this, txn, &mut pos, value, attributes);
    } else {
        panic!("The type or the position doesn't exist!");
    }
}
```

The free function `insert` at `text.rs:741-761` is the shared core also used by `apply_delta`:

```rust
fn insert<P: Prelim>(branch: BranchPtr, txn: &mut TransactionMut, pos: &mut ItemPosition,
                    value: P, mut attributes: Attrs) -> Option<ItemPtr> {
    pos.unset_missing(&mut attributes);
    minimize_attr_changes(pos, &attributes);
    let negated_attrs = insert_attributes(branch, txn, pos, attributes);
    let item = if let Some(item) = txn.create_item(&pos, value, None) {
        pos.right = Some(item);
        pos.forward();
        Some(item)
    } else { None };
    insert_negated_attributes(branch, txn, pos, negated_attrs);
    item
}
```

JS-side equivalent is `insertContent` at `ytype.js:330-350` — identical sequence, less generic. Strategy in Go:

1. Empty string → no-op (matches `text.rs:281-283`).
2. Find cursor (`findTextCursor`).
3. `pos.unset_missing(&mut attributes)` (`text.rs:741` calls it; JS equivalent at `ytype.js:331-335`): for any key currently active at the cursor that the user did NOT mention in `attrs`, add `attrs[key] = null` so the insert region explicitly clears it. **Without this, "insert plain text inside a bold region" would inherit bold** — and the user asking for `attrs = {}` plainly didn't want that. Per JS:

```js
currPos.currentAttributes.forEach((_val, key) => {
  if (attributes[key] === undefined) attributes[key] = null
})
```

4. `minimize_attr_changes` — skip redundant openers.
5. `insert_attributes` — emit opening Format markers for each changed `(key, val)`; build `negated_attrs`.
6. Construct the String Item (same code path as plain `Insert`), integrate, advance cursor.
7. `insert_negated_attributes` — emit closing markers from `negated_attrs`.

The new String Item is sandwiched between an opening Format-marker run and a closing Format-marker run. If `attrs == nil || len(attrs) == 0` AND `pos.current_attrs` is empty, both wing runs are zero-Item and the result is bit-identical to plain `Insert`. If `attrs == nil` AND `pos.current_attrs` is NOT empty, `unset_missing` populates `attrs` with `null` for each active key — the wing runs clear/restore them around the insert. **Subtle but load-bearing: `Insert` (no attrs variant) inherits the cursor's active formatting; `InsertWithAttributes(_, _, _, nil)` (or `Attrs{}`) does NOT.** Match yrs/JS exactly.

---

## §6 Text.InsertEmbed(idx, value) — single Embed Item

API: `func (t *Text) InsertEmbed(tx *doc.TransactionMut, idx uint64, value Any) error` (no attrs variant for the first commit; defer `InsertEmbedWithAttributes`). yrs `Text::insert_embed` at `text.rs:318-334`:

```rust
fn insert_embed<V>(&self, txn: &mut TransactionMut, index: u32, content: V) -> V::Return
where V: Into<EmbedPrelim<V>> + Prelim,
{
    let this = BranchPtr::from(self.as_ref());
    if let Some(pos) = find_position(this, txn, index) {
        let ptr = txn.create_item(&pos, content.into(), None).expect("cannot insert empty value");
        if let Ok(integrated) = ptr.try_into() { integrated }
        else { panic!("Defect: embedded return type doesn't match.") }
    } else { panic!("The type or the position doesn't exist!"); }
}
```

Plain mechanical: `findTextCursor(idx)`, construct one Item with `Content{Kind: KindEmbed, Anys: []Any{value}}`, integrate. The Item's `Len` is 1 (per `content.go:109-110`), `IsCountable()` is true, so it consumes one cursor position. The deleted-skip loop from plain `Insert` (`text.go:87`) applies — walk past tombstones before constructing.

`insert_embed_with_attributes` at `text.rs:336-356` is the same with the §5 wrapper around it. Defer.

---

## §7 Text.ApplyDelta(delta) — the Quill delta API

API: `func (t *Text) ApplyDelta(tx *doc.TransactionMut, delta []DeltaOp) error`, where `DeltaOp` is a tagged union. The wire-level shape is the Quill delta format: `[{insert: "text", attributes: {bold: true}}, {retain: 5, attributes: {color: "red"}}, {delete: 2}]`. yrs `Text::apply_delta` at `text.rs:256-278`:

```rust
fn apply_delta<D, P>(&self, txn: &mut TransactionMut, delta: D)
where D: IntoIterator<Item = Delta<P>>, P: Prelim,
{
    let branch = BranchPtr::from(self.as_ref());
    let mut pos = ItemPosition {
        parent: TypePtr::Branch(branch),
        left: None, right: branch.start, index: 0,
        current_attrs: Some(Box::new(Attrs::new())),
    };
    for delta in delta {
        match delta {
            Delta::Inserted(value, attrs) => {
                let attrs = attrs.map(|a| *a).unwrap_or_default();
                insert(branch, txn, &mut pos, DeltaChunk(value), attrs);
            }
            Delta::Deleted(len) => remove(txn, &mut pos, len),
            Delta::Retain(len, attrs) => {
                let attrs = attrs.map(|a| *a).unwrap_or_default();
                insert_format(branch, txn, &mut pos, len, attrs);
            }
        }
    }
}
```

JS-side at `ytype.js:1078-1120` is the same dispatch but routes string-inserts through `insertContent`, array-inserts through `insertContentHelper`, retains through `currPos.formatText`, deletes through `deleteText`. Key observation: **the cursor is built ONCE at branch.Start and advanced through the entire delta** — it is NOT re-resolved between ops. A `retain 5` advances the cursor 5 units (formatting along the way if `attrs` are given); an `insert` plops at the current cursor; a `delete` tombstones forward. The order of ops in the delta is significant; the index field is implicit (cumulative position).

Go port:

```go
type DeltaOp struct {
    Insert any        // string OR Any (for embed); nil for retain/delete
    Retain uint64     // 0 if not retain
    Delete uint64     // 0 if not delete
    Attrs  Attrs      // optional; nil treated as empty for retain, see §5 for insert
}

func (t *Text) ApplyDelta(tx *doc.TransactionMut, delta []DeltaOp) error {
    cur := &textCursor{left: nil, right: t.branch.Start, index: 0, attrs: Attrs{}}
    for _, op := range delta {
        switch {
        case op.Insert != nil:
            if s, ok := op.Insert.(string); ok {
                if err := insertString(tx, t.branch, cur, s, op.Attrs); err != nil { return err }
            } else {
                if err := insertEmbed(tx, t.branch, cur, op.Insert.(Any)); err != nil { return err }
            }
        case op.Retain > 0:
            if err := formatRange(tx, t.branch, cur, op.Retain, op.Attrs); err != nil { return err }
        case op.Delete > 0:
            if err := deleteRange(tx, cur, op.Delete); err != nil { return err }
        }
    }
    return nil
}
```

Where `insertString`, `insertEmbed`, `formatRange`, `deleteRange` are the building blocks from §4–§6 plus the existing `Text.Delete` path refactored to take a `*textCursor`. **Transaction semantics**: ApplyDelta runs in one `TransactionMut`; if any sub-op errors, the txn rolls back (no partial-application; see §10 gotcha). yrs panics on out-of-range; we return `error` per our existing precedent (`text.go:78,134-140`).

---

## §8 Text.ToDelta() / Range / iteration — the read side

Plain `Text.String()` (already shipped at `internal/types/text.go:48-59`) returns just the concatenated String content, skipping Format / Embed / Type / Deleted. Rich-text consumers need the *delta representation* — every contiguous run of same-attributes text emitted as one delta op, with embeds and nested types appearing as `{insert: <obj>, attributes: …}` operations.

yrs entry point is `Text::diff_range` at `text.rs:398-427`, delegating to `DiffAssembler::process` (`text.rs:582-710`). JS is `YType.toDelta` at `ytype.js:835-1000+`. The walker:

1. Initialise `currentAttributes = {}`, walk `branch.Start` rightward.
2. For each live Item:
   - `ContentString` → emit `{insert: s, attributes: clone(currentAttributes)}`.
   - `ContentEmbed` → emit `{insert: c.embed, attributes: clone(currentAttributes)}`.
   - `ContentType` → emit `{insert: <wrapper>, attributes: clone(currentAttributes)}` or recurse for deep-delta.
   - `ContentFormat(key, value)` → `updateCurrentAttributes(currentAttributes, key, value)`. Emit no op.
   - Deleted Items → skip in the simple case; in snapshot/diff mode they become `{retain: n, attributes: {ychange: …}}` (defer).
3. Adjacent inserts with identical attributes coalesce (the delta builder in JS does this automatically; we should mimic in Go).

For the first commit we don't need the full snapshot/observer/attribution machinery in `ytype.js:835-1000`; we need just the basic "walk and emit" loop. **Recommend Go API**:

```go
// Range walks the live content of the Text emitting (text-chunk, attrs)
// pairs in document order. fn returns false to abort iteration early.
// String chunks are UTF-8 Go strings; the attrs map should be treated
// as read-only by callers (it is reused across calls — clone if you
// need to retain it).
func (t *Text) Range(fn func(chunk string, attrs Attrs) bool)

// RangeAny is the iteration variant that surfaces Embed and Type
// items in addition to text. The `value` is a `string` for text runs,
// an `Any` for embeds, and a `*Map`/`*Array`/`*Text` for nested types.
func (t *Text) RangeAny(fn func(value any, attrs Attrs) bool)

// ToDelta returns the full Quill delta as a slice. Convenience wrapper
// over RangeAny. Each DeltaOp is the result of one Range callback.
func (t *Text) ToDelta() []DeltaOp
```

`Range` mirrors the `Map.Range(fn func(key string, value any) bool)` pattern already in `internal/types/map.go` — same close-over-fn iterator shape Go callers expect.

---

## §9 Go translation choices

### File layout

Recommend **new file `internal/types/text_format.go`** (with companion `text_format_test.go`) rather than extending the existing `text.go`. Rationale:

- `text.go` is already ~250 lines of plain-text path; adding ~400 lines of format/embed/applyDelta on top makes it hard to read and review.
- The format dance touches its own auxiliary types (`Attrs`, `textCursor`, `DeltaOp`, the `formatRange` / `insertAttributes` / `insertNegatedAttributes` / `minimizeAttrChanges` helpers) that have no consumers in the plain-text path. Localising them keeps the plain-text surface easy to audit.
- `Text` struct stays in `text.go` (one definition); methods can hang off it from multiple files, idiomatic Go.
- Mirrors yrs's own organisation — `text.rs` is monolithic, but the JS port already splits things between `Item.js` (the content classes) and `ytype.js` (the algorithms); we follow JS here.

`internal/utf16/` already exists — no further utility-package additions needed.

### `Attrs` type

```go
// internal/types/text_format.go
//
// Attrs is the attribute map carried on Format markers, InsertWithAttributes
// calls, and Delta operations. Values are lib0 Any-shaped (string, float,
// bool, nil, or map[string]Any / []Any for nested objects). nil value means
// "clear this attribute" (matches yrs Any::Null and JS literal null).
type Attrs = map[string]Any
```

Use **type alias** (`= map[string]Any`), not a named type — keeps user-facing ergonomics: `Attrs{"bold": true}` is cleaner than `Attrs(map[string]Any{"bold": true})`. The `Any` value type is whatever our `internal/encoding/any_codec.go` defines as the public Any (currently `block.Any`; re-export from `types` package if needed).

### Any value extraction at the attr boundary

When comparing `attrs[key]` against `currentAttrs[key]` (the equality check that drives `minimize_attr_changes`, `insert_attributes`, `formatText`), the comparison must be **structural deep-equality on the Any value**, not Go `==`. Two `map[string]Any` instances with the same contents but different pointers must compare equal; two `[]Any` slices with the same elements must compare equal. **Use the `Any.Equal(other Any) bool` method that already exists in our encoding package** (or add it if absent; yrs uses `Any: PartialEq`, JS uses the equalAttrs helper at `ytype.js:163,232,281`). Do NOT use `reflect.DeepEqual` — it works but allocates and is slower on the hot path; bespoke Any comparator is straightforward and matches yrs/JS semantics for `Any::Null == Any::Null`.

When a Format marker arrives over the wire, `Content.Anys[0]` is set to the decoded Any. When user code calls `Format(..., Attrs{"bold": true})`, we store `true` directly as the Any (the Go bool fits the Any shape via the existing Any conversion). Provide a small `anyOf(v any) Any` helper in `text_format.go` that wraps user-supplied primitives — string, bool, int/float, nil, nested map/slice — into the Any tagged union.

### Test fixture story

Replicating JS Yjs deltas on Go is the cross-language check. Add a `cross_text_format_fixtures.json` in `internal/encoding/testdata/` containing: JS-encoded updates from scripted scenarios (insert + format + insert-embed + apply-delta sequences) and the expected `ToDelta()` output (also JS-encoded). The Go test decodes the update, applies it, calls `t.ToDelta()`, compares structurally to the fixture's expected. Covers wire round-trip + walker correctness in one shot.

---

## §10 Gotchas — implementer must not miss

1. **Format markers MUST NOT contribute to `Text.Length()`.** Format `Item.Countable == false`, so `Item.Integrate` already excludes them from `branch.BlockLen` — but if you accidentally set `Countable = true` when constructing the Format Item (or take a shortcut and bypass `Content.IsCountable()`), every applied format inflates the user's text length and breaks every offset downstream. Verify with a test that does `Insert("ab"); Format(0, 2, {bold: true}); assert Length() == 2`, not 3.

2. **Format markers DO occupy a slot in the linked list and DO get a clock value.** They are counted by `BlockStore` (one Item, one clock unit per marker). State vectors, update encoding, GC, and the `Pending` clock-dependency tracking all see them as regular Items. A Format marker emitted by client A at clock 17 is referenced as `(A, 17)` by any Item whose `Origin` happens to land on it. **`branch.BlockLen` does NOT count them** (because of countable=false), but `client.NextClock()` does. These two counters are not the same thing — do not conflate them.

3. **`ApplyDelta` is one transaction; errors must roll back.** yrs panics mid-delta if any op is malformed; our error-returning idiom requires that we either (a) apply the entire delta or (b) leave the doc untouched. The simplest implementation runs the loop inside the existing `TransactionMut` and relies on the transaction's commit-only-on-success machinery; if a per-op `error` bubbles up, the txn is dropped without commit. Verify the `TransactionMut` drop path actually discards in-flight Item appends — if not, fix that before shipping `ApplyDelta`. Without this, a partial apply leaves the doc in an inconsistent state visible to the next observer.

4. **Embed counts as 1 character position; iterating the text "skipping" an embed is wrong.** A document `"a" + embed + "b"` has `Length() == 3`. Cursor 2 lands *after* the embed, cursor 1 lands *between* "a" and the embed. `String()` returns `"ab"` (Embed is non-`KindString`, skipped) — but `Length()` does include it. Document this prominently in the `Length` and `String` doc comments because users will read `len(t.String()) == 2` and expect `Length() == 2`. They are intentionally different — the editor needs the position to refer to.

5. **Format markers MUST round-trip through encode/decode/integrate.** The wire-format arms in §1 are straightforward, but the integration path needs care: `Item.Integrate` for a Format Item still has to insert into `branch.Start` via the YATA loop — the only thing that's different is the `Countable` flag's effect on `BlockLen`. **Test**: create a doc with a Format marker, encode update V1, decode on a fresh doc, verify the new doc's `branch.Start` chain contains the Format Item at the correct YATA position and `Length()` is unchanged from the source.

6. **Format markers do NOT squash with adjacent Format markers.** `ContentFormat.mergeWith` returns `false` unconditionally at `Item.js:1142-1145`, and yrs `try_squash` for Format returns `false` (the `_ => false` arm at `block.rs:1972-1993` — Format is not in the squashable list, alongside Embed and Type). Our existing `Content.TrySquash` at `content.go:198-225` already only enables squash for Any/Deleted/JSON/String — Format is correctly excluded. **Do not "optimise" by merging two adjacent `(bold: true)` markers into one** — even though semantically redundant, the wire identity matters for YATA convergence (their distinct clocks anchor distinct downstream Origins).

7. **Format markers participate in `Pending`'s clock-dependency tracking.** A Format Item arriving with `Origin = (A, 17)` blocks on `(A, 17)` arriving first, the same as any other Item. No new code needed — `Pending` is content-agnostic — but if any plumbing accidentally treats `KindFormat` as "skip Pending registration" (e.g. an early-out for non-countable Items somewhere upstream of Pending), Format Items will be silently dropped. Audit any `IsCountable()`-driven branches outside the Branch-counter maintenance code (there should be none).

8. **`Null`-valued Format means clear, not literal-null.** Both encode and decode preserve `Any::Null` in `Content.Anys[0]`, but the *interpretation* at integration / walk time differs from any other Any value: encountering a `(key, Null)` Format marker should remove `key` from `currentAttrs`, not set `currentAttrs[key] = Null`. yrs and JS both check explicitly (`text.rs:800-806`, `ytype.js:262-264`). The `updateCurrentAttributes` helper is the single point that needs this branch — keep it in one place so the rule isn't open-coded in multiple call sites.

9. **`InsertWithAttributes(_, _, _, nil)` differs from `Insert(_, _)`**. The former calls `unset_missing` which fills in `null` for every currently-active attribute the user didn't mention, producing opening Format markers that clear them; the latter simply inherits the cursor's active formatting. If a user *wants* "insert text inheriting current formatting", they call `Insert`; if they want "insert text in plain unformatted form", they call `InsertWithAttributes(_, _, _, Attrs{})`. Document this in the method comments — it is non-obvious and a frequent source of editor bugs in the JS world.

10. **Surrogate-pair splits and Format markers can collide.** §3 of `types-text.md` documents the surrogate-pair U+FFFD replacement we do on mid-pair splits. If a `Format` call lands at a UTF-16 offset that is mid-surrogate inside a String Item, the same U+FFFD logic applies on the resulting `Store.SplitBlock`. **Verify** that the format-marker insertion happens AFTER the split returns, so the marker sits between the (now U+FFFD-terminated) left half and the (U+FFFD-prefixed) right half — not embedded inside what used to be a surrogate pair.
