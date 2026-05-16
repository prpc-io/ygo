# yrs port notes: nested types (Y.Map inside Y.Map, Y.Array of Y.Map, etc.)

> Sources: yrs (main HEAD) — `yrs/src/block.rs` (`Item::repair`, `ItemContent::Type`), `yrs/src/types/mod.rs` (`TypeRef` + `TYPE_REFS_*`), `yrs/src/store.rs` (`Store::get_type` / `Store::get_or_create_type`), `yrs/src/types/map.rs` (`MapPrelim`, `Map::insert`). JS upstream — `yjs/src/structs/Item.js` (`ContentType`, `Y*RefID`), `yjs/src/ytype.js` (`YType._write`, `readYType`, `readContentType`), `yjs/src/utils/UpdateEncoder.js` + `UpdateDecoder.js` (`writeTypeRef`/`readTypeRef`). Line numbers below are accurate at fetch time.

Nested types are how a Y.Map can hold another Y.Map under a key and how a Y.Array can hold a Y.Array as an element — the same mechanism XmlFragment/XmlElement/XmlText use. Wire-format-wise the trick is a single Item content variant — `ContentType` (refID `7`, our `KindType = 7`) — whose payload is a fresh `Branch` carrying a varuint `TypeRef` discriminator that names the sub-type (Map=1, Array=0, Text=2, …). Items inside the nested type point at this owning Item via `Parent = TypePtr::ID(parentItemID)`; `Item::repair` walks that ID back to the parent and lifts the Branch out of its Content so YATA integration runs normally. Nested types are the gateway to XML, which is the gateway to ProseMirror/Tiptap — without them ygo cannot back any rich-text editor. We already have the substrate (Branch struct, `KindType = 7`); the gap is four pieces: a `TypeRef` field on Branch, the `ContentType` wire codec, the `ParentID` arm in `Repair`, and `Map.SetMap` / `Array.InsertMap` API.

---

## §1 TypeRef discriminator — exact wire constants

Authoritative source is `yjs/src/structs/Item.js:1382-1388` — the wire byte is whichever of these eight integers `_legacyTypeRef` resolved to. yrs mirrors them in `yrs/src/types/mod.rs:36-65`.

| Yjs (`Item.js`) | Value | yrs (`mod.rs`) | Go (proposed, `internal/block`) |
|---|---|---|---|
| `YArrayRefID`        | `0` | `TYPE_REFS_ARRAY`        | `TypeRefArray` |
| `YMapRefID`          | `1` | `TYPE_REFS_MAP`          | `TypeRefMap` |
| `YTextRefID`         | `2` | `TYPE_REFS_TEXT`         | `TypeRefText` |
| `YXmlElementRefID`   | `3` | `TYPE_REFS_XML_ELEMENT`  | `TypeRefXmlElement` |
| `YXmlFragmentRefID`  | `4` | `TYPE_REFS_XML_FRAGMENT` | `TypeRefXmlFragment` |
| `YXmlHookRefID`      | `5` | `TYPE_REFS_XML_HOOK`     | `TypeRefXmlHook` |
| `YXmlTextRefID`      | `6` | `TYPE_REFS_XML_TEXT`     | `TypeRefXmlText` |
| —                    | `7` | `TYPE_REFS_WEAK`         | *(deferred — yrs-only, `cfg(feature="weak")`)* |
| —                    | `9` | `TYPE_REFS_DOC`          | *(deferred — subdocs)* |
| —                    | `15` | `TYPE_REFS_UNDEFINED`   | `TypeRefUndefined` *(sentinel)* |

Wire byte is `varuint`, not packed — `UpdateEncoder.writeTypeRef(info)` calls `encoding.writeVarUint(this.restEncoder, info)` (`UpdateEncoder.js:81-83`); `UpdateDecoder.readTypeRef()` is `decoding.readVarUint(this.restDecoder)` (`UpdateDecoder.js:84-86`). For all in-use values (`0–6`, `9`, `15`) this is one byte, but the codec must use varuint. Declare these as `block.TypeRef uint8` constants in `internal/block/typeref.go` alongside the existing `ContentKind`.

---

## §2 ContentType wire format — byte-level

```
Item header bits  // info byte; low 4 bits = content ref number = 7 (KindType)
…                 // standard Item fields: ID, origin, rightOrigin, parent, parentSub, length
ContentType-body :=
    varuint(typeRef)                   // §1 — the eight constants above
  [ varstring(name) ]                  // ONLY for YXmlElementRefID(=3) and YXmlHookRefID(=5)
```

No nested Items, no child markers, no length prefix on the inner Branch — children of the nested type travel as ordinary Items elsewhere in the update stream, each pointing back via `Parent = ID(thisItemID)`.

`yjs/src/structs/Item.js:1507-1510` `ContentType.write`:

```js
write (encoder, _offset, _offsetEnd) { this.type._write(encoder) }
```

`yjs/src/ytype.js:1474-1484` `YType._write`:

```js
_write (encoder) {
  encoder.writeTypeRef(this._legacyTypeRef)
  switch (this._legacyTypeRef) {
    case YXmlElementRefID:
    case YXmlHookRefID: { encoder.writeKey(this.name); break }
  }
}
```

Read side, `yjs/src/ytype.js:2042` + `2153-2158`:

```js
export const readContentType = decoder => new ContentType(readYType(decoder))

export const readYType = decoder => {
  const typeRef = decoder.readTypeRef()
  const ytype = new YType(
    typeRef === YXmlElementRefID || typeRef === YXmlHookRefID
      ? decoder.readKey() : null)
  ytype._legacyTypeRef = typeRef
  return ytype
}
```

`ContentType.getRef()` returns `7` (`Item.js:1514-1516`) — the discriminator in `contentRefs[]` (`yjs/src/utils/encoding.js`, `contentRefs[7] = readContentType`). Matches our existing `block.KindType = 7` (`internal/block/content.go:26`).

Go encoder lands in `internal/encoding/v1_encode.go` alongside the other `KindXxx` writers: read `c.Branch.TypeRef`, `lib0.AppendVarUint`, then for `TypeRefXmlElement`/`TypeRefXmlHook` follow with `AppendVarString(c.Branch.Name)`. Decoder is the inverse.

---

## §3 Branch struct extensions

Today's `internal/block/stubs.go:17-46` `Branch` has `Start`, `Map`, `Item`, `BlockLen`, `ContentLen`, `Name`. yrs's `Branch` also carries `type_ref: TypeRef` (`mod.rs:70-81`) read at encode time (`YType._write`, §2) and used by the read path to materialize the right subclass.

Required addition:

```go
type Branch struct {
    Start      *Item
    Map        map[string]*Item
    Item       *Item
    BlockLen   uint64
    ContentLen uint64
    Name       string
    TypeRef    TypeRef // NEW
}
```

**Zero value collides with `TypeRefArray = 0`.** Use `TypeRefUndefined = 15` as the explicit "unset" sentinel — matches yrs's `TypeRef::Undefined` which `Store::get_or_create_type` writes when a root is looked up before its real type is known (`store.rs:119-133`, `Entry::Occupied` arm calling `repair_type_ref(type_ref)` on first typed access). Root branches created via `Doc.Map(name)` / `Doc.Array(name)` MUST set `TypeRef` at construction; nested branches created inside `ContentType` payloads MUST set `TypeRef` from the wire. Repair-time fixup (yrs `repair_type_ref`) is only needed if a root is referenced by `ParentNamed` before its typed `doc.GetOrCreateType` call lands — defer until that wiring exists.

Root branches do NOT emit `TypeRef` on the wire; only nested branches embedded in `ContentType` payloads do. Receivers learn the TypeRef of a root only on first observation of a `ContentType` Item parented to that root — or never, which is fine because both peers independently called `doc.Map(name)` with matching expectations.

---

## §4 Item.Repair: ParentID resolution

The only missing piece in `internal/block/repair.go` — today (`repair.go:62-63`) the `ParentID` arm returns `ErrParentIDUnresolved`. yrs's verbatim handling (`yrs/src/block.rs:1399-1413`, inside `Item::repair`):

```rust
TypePtr::ID(id) => {
    let ptr = store.blocks.get_item(id);
    if let Some(item) = ptr {
        match &item.content {
            ItemContent::Type(branch) => {
                TypePtr::Branch(BranchPtr::from(branch.as_ref()))
            }
            ItemContent::Deleted(_) => TypePtr::Unknown,
            other => {
                return Err(UpdateError::InvalidParent(
                    id.clone(), other.get_ref_number()))
            }
        }
    } else {
        TypePtr::Unknown
    }
}
```

Go translation, slotted into `Repair` (`repair.go:62`):

1. `parent := ctx.GetItem(it.Parent.ID)`. `IntegrateContext.GetItem` already exists (`integrate.go:13`).
2. **If parent Item is missing** — return a distinct `ErrParentIDMissing` so `encoding.Pending` can queue the Item as a dependency on the parent ID. yrs degrades to `TypePtr::Unknown` and lets YATA bail; we want explicit signalling so the pending-drain pass retries once the parent lands.
3. **If parent Item exists, switch on `parent.Content.Kind`:**
   - `KindType`: success. `it.Parent = Parent{Kind: ParentBranch, Branch: parent.Content.Branch}`. Do NOT overwrite `ParentSub` — yrs only inherits ParentSub in the `Unknown` arm, never the `ID` arm.
   - `KindDeleted`: degrade to `ParentUnknown` (yrs returns `TypePtr::Unknown`). Item integrates as a tombstone-of-a-tombstone, GC'd at next squash.
   - anything else: hard malformed-update error. Return `ErrInvalidParentContent` mirroring `UpdateError::InvalidParent`.

The parent Item must already be in the store before the child's `Repair` runs. yrs guarantees this via client-clock-ordered struct processing plus the pending-queue retry; we already replicate that in `encoding.Pending.Apply`. Interaction: `Pending` must, on each apply pass, retry Items whose `Repair` returned `ErrParentIDMissing` once the parent has arrived — add a parent-ID-dependency map alongside the existing `Origin`/`RightOrigin` clock maps. Test seed: `TestPending_NestedTypeParentArrivesLate` — push child-of-nested before parent, observe drain integrates child after parent lands.

---

## §5 Map.Set with a nested type

`outer.set('k', new Y.Map())` produces a single Item: `Parent = ID(outerBranch.Item.ID)` (or `Named("rootName")` if `outer` is a root), `ParentSub = "k"`, `Origin = outer.Map["k"].LastID()` (or nil), `Content = ContentType(freshBranch{TypeRef: TypeRefMap})`.

yrs uses `Prelim` + `MapPrelim` (`yrs/src/types/map.rs:575-590`):

```rust
impl Prelim for MapPrelim {
    type Return = MapRef;
    fn into_content(self, _txn: &mut TransactionMut) -> (ItemContent, Option<Self>) {
        let inner = Branch::new(TypeRef::Map);
        (ItemContent::Type(inner), Some(self))
    }
    fn integrate(self, txn: &mut TransactionMut, inner_ref: BranchPtr) {
        let map = MapRef::from(inner_ref);
        for (key, value) in self.0 { map.insert(txn, key, value); }
    }
}
```

We deferred the `Prelim`/`In` machinery (see `types-map.md` §9). To unblock nested types without porting that API, expose dedicated constructors on `*Map`/`*Array`:

```go
// Map.SetMap creates a fresh empty Y.Map nested under key. Mirrors
// `outer.set('k', new Y.Map())` in JS.
func (m *Map) SetMap(tx *doc.TransactionMut, key string) *Map {
    inner := &block.Branch{TypeRef: block.TypeRefMap, Map: map[string]*block.Item{}}
    item := block.NewMapInsertItem(tx, m.branch, key,
        block.Content{Kind: block.KindType, Branch: inner})
    inner.Item = item            // back-reference for ParentID resolution
    item.Integrate(tx, 0)
    return newMap(inner)
}

func (m *Map) SetArray(tx *doc.TransactionMut, key string) *Array
func (m *Map) SetText (tx *doc.TransactionMut, key string) *Text
```

Cleaner than overloading `Set(key, value any)` — the caller already knows statically whether they want a primitive or a nested type. Reserve `Set(key, value any)` for the JSON-shaped Any subset. Underlying Item-construction shape is yrs `Map::insert` (`map.rs:283-301`), used verbatim — only the value-side `Prelim::into_content` indirection changes.

**Set `inner.Item = item` immediately.** This back-reference is what lets future child Items emit `Parent = ParentID(item.ID)` on the wire — without it the encoder cannot recover the parent Item's ID from a `*Branch`.

---

## §6 Array.Insert with nested types

Same shape, indexed: `ParentSub = nil`, sequence-positioned (`Left = item-at-idx-1`, `Right = item-at-idx`), `Origin`/`RightOrigin` from Left/Right per the standard Array.Insert path.

```go
func (a *Array) InsertMap   (tx *doc.TransactionMut, idx int) *Map
func (a *Array) InsertArray (tx *doc.TransactionMut, idx int) *Array
func (a *Array) InsertText  (tx *doc.TransactionMut, idx int) *Text
```

Each one resolves Left/Right by walking the branch list, allocates the inner Branch with the right `TypeRef`, builds the Item via the array-insert factory, sets `inner.Item = item`, integrates, returns the typed wrapper. Batched mixed-content inserts defer until `Prelim` lands.

---

## §7 Reading nested values

`Map.Get(key) (any, bool)` and `Array.Get(idx) (any, bool)` must discriminate at the `Content.Kind` level. Today's `Map.Get` calls `item.Content.GetLast()` which returns the last element of the content slice; for `KindType` `GetLast` is undefined — the Branch is the whole payload.

Shared helper:

```go
func extractValue(it *block.Item) (any, bool) {
    switch it.Content.Kind {
    case block.KindType:
        switch it.Content.Branch.TypeRef {
        case block.TypeRefMap:   return newMap (it.Content.Branch), true
        case block.TypeRefArray: return newArray(it.Content.Branch), true
        case block.TypeRefText:  return newText (it.Content.Branch), true
        case block.TypeRefXmlElement,
             block.TypeRefXmlFragment,
             block.TypeRefXmlText:
            return nil, false    // deferred
        default: return nil, false
        }
    default: return it.Content.GetLast()
    }
}
```

Wrappers are stateless pointer-wrappers over `*Branch`, constructed fresh per Get. Identity is pointer equality on the underlying Branch — two `Get("k")` calls return two `*Map` values sharing the same `*Branch` and therefore the same observable state. yrs equivalent is `Out::YMap(MapRef)`/`Out::YArray(ArrayRef)` in `out.rs:14-39`; discrimination happens in `ItemContent::get_last` (yrs `block.rs`) which returns the variant matching the Branch's `type_ref`.

---

## §8 Gotchas — implementer must not miss

1. **`Branch.TypeRef` MUST be set at Branch construction.** Default-zero is `TypeRefArray = 0`, which silently mis-encodes every nested Map as an Array. Use `TypeRefUndefined = 15` as the only safe zero and assert non-Undefined in the encoder. Root branches must set TypeRef at creation — no late repair path unless a peer observes a `ContentType` Item parented to them.

2. **`Branch.Item` MUST be set immediately after constructing a `ContentType` Item.** Back-reference the encoder follows to emit `Parent = ParentID(branch.Item.ID)` for children. Without it, a child Item added to the nested Map falls through to `ParentUnknown` and either crashes the encoder or produces an unresolvable wire packet.

3. **`Item::repair` runs BEFORE the parent Item is consumed.** The Branch is owned by the parent Item's Content; if the parent is GC'd or squashed between Repair-time and Integrate-time, the Branch pointer dangles. Run all Repair calls up front during update-apply, before any squash pass, and snapshot the resolved `*Branch` into `it.Parent.Branch` while you can.

4. **yrs's `TypePtr` has a `Branch(BranchPtr)` variant — the post-Repair representation.** Our `Parent.Kind == ParentBranch` corresponds to it. Do not confuse `TypePtr::Branch` (resolved) with `TypePtr::ID` (pre-Repair) when reading yrs source. Once `Repair` succeeds, `it.Parent.Kind` is always `ParentBranch` regardless of which arm fired — never inspect `Parent.ID` / `Parent.Named` after Repair returns ok.

5. **The TypeRef varuint goes INSIDE the ContentType payload, not in the Item info byte.** Item info-byte low-4-bits = `7` (content ref). The TypeRef varuint follows the standard Item header (ID, origin, rightOrigin, parent, parentSub, length) as part of the content payload (`YType._write`, `ytype.js:1474-1484`). Misplacing it breaks wire compat with both Yjs and yrs receivers.

6. **`ParentSub` of nested-Map children is the INNER key.** A child Item added via `nested.Set("inner_key", v)` has `Parent = ID(outerItem.ID)` AND `ParentSub = "inner_key"` — never the outer key. The outer key only lives on the containing `ContentType` Item. When `Repair` resolves `ParentID` it must NOT overwrite the child's `ParentSub` — yrs only inherits ParentSub in the `Unknown` arm (`block.rs:1381-1413`). Today's `repair.go:46-54` is correct on this; preserve the behaviour when adding the `ParentID` arm.

7. **XmlElement and XmlHook carry an extra `name` string on the wire** (`ytype.js:1479-1483`, `readYType` `ytype.js:2155`). Encoder emits `AppendVarString(branch.Name)` after the TypeRef byte for `TypeRefXmlElement (=3)` and `TypeRefXmlHook (=5)` only. For Map/Array/Text/XmlFragment/XmlText there is no name on the wire.

8. **`Pending` must learn parent-Item-ID dependencies.** Today `Pending` tracks missing-Origin / missing-RightOrigin clocks per (client, clock). Add a third map: missing-ParentID. When `Repair` returns `ErrParentIDMissing`, register the dependency; on each pending-drain pass, retry Items whose parent Item has since arrived. Without this, a nested-type Item that arrives before its parent in the update stream will be silently dropped and the receiving peer desyncs.
