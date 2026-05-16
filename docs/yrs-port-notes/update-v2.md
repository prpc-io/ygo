# ygo port notes: V2 update wire format (column-oriented encoder, decoder, lib0 RLE primitives)

> Sources studied: yjs commit on `main` as of 2026-05-16. Files cited: yjs `src/utils/UpdateEncoder.js`, yjs `src/utils/UpdateDecoder.js`, lib0 `encoding.js` (master branch), yrs `yrs/src/updates/encoder.rs`, yrs `yrs/src/update.rs`. Verified against the V1 note (`update-v1.md`) so dispatch points, content kinds, and integrate semantics stay consistent.

> Scope: this note covers V2 *wire format* and its lib0 primitives only. Block-level semantics (content kinds, info-byte flags, parent / parent_sub / origin / right_origin) are unchanged between V1 and V2 — see `update-v1.md` §6 and §7. V2 changes only *how* the per-field byte streams are packed.

V2 is the alternative update wire format yjs ships alongside V1. It exists because the V1 stream-of-blocks layout repeats per-block client IDs, monotonically-incrementing clocks, and recurring map keys verbatim — bytes that compress dramatically when grouped into per-field columns. Adopters that move large updates (Hocuspocus's `Y.encodeStateAsUpdateV2`/`Y.applyUpdateV2` paths, some collaborative-editor backends snapshotting big documents to disk) pick V2 to cut update payload sizes by 30-70% on documents with repetitive structure (many clients, many short ops, dense maps). Wire format parity with V2 matters for ygo because it's the on-disk format for yjs-aware persistence layers (y-leveldb, y-indexeddb, Hocuspocus's SQLite/Postgres adapters all ship V2 paths) — without it, a ygo node cannot interop with an existing yjs persistence store. Implementation cost is dominated not by V2 itself (the per-column write methods are thin wrappers) but by the four lib0 stateful RLE primitives V2 depends on, which must be ported as a leaf package before V2 itself can land.

---

## 1. Column-oriented wire layout

V1 emits N block records back-to-back, each carrying every field inline:

```
[info₀ origin₀ rightOrigin₀ parent₀ parentSub₀ content₀]
[info₁ origin₁ rightOrigin₁ parent₁ parentSub₁ content₁]
…
```

V2 inverts the layout. The encoder fans field writes out to ten parallel buffers (one per field type), then concatenates them at `toUint8Array` time with length prefixes:

```
[0x00]                              // feature flag, always zero today
[varuint len | keyClockBytes]       // IntDiffOptRle column of key-table clock IDs
[varuint len | clientBytes]         // UintOptRle column of client IDs (per write_left_id/right_id/client)
[varuint len | leftClockBytes]      // IntDiffOptRle column of left-origin clocks
[varuint len | rightClockBytes]     // IntDiffOptRle column of right-origin clocks
[varuint len | infoBytes]           // RleEncoder<u8> column of info-bytes
[varuint len | stringBytes]         // StringEncoder column (parent root-type names, parent_sub keys, content strings)
[varuint len | parentInfoBytes]     // RleEncoder<u8> column of parent-is-name boolean tags
[varuint len | typeRefBytes]        // UintOptRle column of TypeRef tags inside ContentType
[varuint len | lenBytes]            // UintOptRle column of run-lengths (GC len, content len, etc.)
[restBytes]                         // raw byte stream for ContentAny / ContentBinary / ContentJSON / ContentDoc / ContentMove / ContentEmbed / ContentFormat values; NO length prefix
```

Byte ordering is fixed by `UpdateEncoderV2.toUint8Array()` at yjs `src/utils/UpdateEncoder.js:141-153` and mirrored in yrs `EncoderV2::to_vec` at `yrs/src/updates/encoder.rs` (column flush sequence in the same order). The first byte is a feature flag (`buf.write_u8(0)`) that today is always `0x00`; it exists as a forward-compat marker so future column-format extensions can rev the layout without re-versioning the whole update. Each column except the trailing `rest` buffer is wrapped via `writeVarUint8Array` (varuint length prefix followed by raw bytes). The `restEncoder` is appended raw with `writeUint8Array` — it consumes the rest of the buffer to EOF, which works because the surrounding update transport (the update bytes themselves) has its own length boundary supplied out-of-band.

Critically, the wire has **no magic header** distinguishing V1 from V2. A V2 decoder reading V1 bytes would consume the V1 block-runs count (often a small varuint, plausibly valid as a varuint column length) and then misparse the per-client headers as column bytes. Selection is the caller's responsibility — see §6.

---

## 2. Per-column encoders

Field-to-primitive mapping (yjs `src/utils/UpdateEncoder.js:127-138` constructor; yrs `yrs/src/updates/encoder.rs` `EncoderV2::new`):

| Column | Primitive | Compression strategy | Why this primitive |
|---|---|---|---|
| `keyClockEncoder` | `IntDiffOptRleEncoder` | Diff+RLE on 31-bit ints | Key-table assigns ascending integer IDs as new keys are seen; deltas are usually `+1` for streams of fresh keys and `0` for repeats. Diff-RLE collapses both. |
| `clientEncoder` | `UintOptRleEncoder` | RLE on absolute uints | A single client's run emits many consecutive blocks with the same client ID → RLE collapses to `[clientID, -count]`. |
| `leftClockEncoder` | `IntDiffOptRleEncoder` | Diff+RLE on 31-bit ints | Origin clocks within a run increment monotonically; consecutive items have `origin.clock = prev.clock + prev.len` → constant non-zero delta → diff-RLE collapses. |
| `rightClockEncoder` | `IntDiffOptRleEncoder` | Diff+RLE on 31-bit ints | Same shape as left, but tracks right-origin pointers (often null / repeating tail values → diff=0 RLE wins). |
| `infoEncoder` | `RleEncoder` with `writeUint8` | Basic RLE of raw bytes | Info bytes for a sequence of same-kind ops (e.g. character inserts) repeat exactly → basic RLE is enough, no diff needed (info is a bitmask, not numeric). |
| `stringEncoder` | `StringEncoder` | Concatenate + UintOptRle length column | All strings are joined into one big concat string written via `writeVarString`; lengths are a separate UintOptRle column appended after. Single decompression pass extracts both. |
| `parentInfoEncoder` | `RleEncoder` with `writeUint8` | Basic RLE of 0/1 bytes | Boolean tag for "parent is Y.name (root) vs parent is an item ID". Within a run of map-on-same-root inserts this is constantly `1` → RLE collapses to one byte plus count. |
| `typeRefEncoder` | `UintOptRleEncoder` | RLE on absolute uints | `TypeRef` tags inside `ContentType` (Array=0, Map=1, Text=2, XmlElement=3, XmlFragment=4, XmlHook=5, XmlText=6, …); usually small repeating ints when nested types are mass-created. |
| `lenEncoder` | `UintOptRleEncoder` | RLE on absolute uints | Run-lengths emitted for GC ranges, ContentDeleted, content-Any item counts, etc. Often the same length appears many times (e.g. single-char inserts → `len=1` repeated). |
| `restEncoder` | Plain `Encoder` (raw bytes) | None | Catch-all for content payloads that aren't worth column-encoding: `ContentAny` (lib0 Any TLV), `ContentBinary` (varbuf), `ContentJSON`, `ContentDoc`, `ContentMove`, `ContentEmbed`, `ContentFormat`. These are read/written inline by `writeAny`/`writeBuf`. |

The optimisation rule of thumb: **monotonic-ish integers go through `IntDiffOptRle`; repeating uints go through `UintOptRle`; small-domain bytes go through basic `Rle`; strings get bulk concatenation; everything else stays raw.**

### Encoding semantics of the RLE primitives

From lib0 `encoding.js`:

- `RleEncoder` (lines 522-557): pairs `(value, count-1)`. On `write(v)`: if `v == this.s` (current run value), increment count; else flush previous run as `writeVarUint(count - 1)` then write the new value via the user-supplied byte-writer (yjs uses `writeUint8`) and reset. Tail-flush happens on `toUint8Array`.
- `UintOptRleEncoder` (lines 649-676, flush helper lines 627-639): pairs `(value, count-2)` with a sign-bit trick. Single occurrences emit `writeVarInt(+value)`. Multiple occurrences emit `writeVarInt(-value)` followed by `writeVarUint(count - 2)`. The decoder distinguishes the two cases by the sign of the leading varint. Result: a single value costs just one varint; a run costs a sign-flipped varint plus a varuint counter.
- `IntDiffOptRleEncoder` (lines 749-779, flush helper lines 718-730): packs `(diff << 1) | hasCount` into the first varint of each run, where `hasCount=0` means single-occurrence and `hasCount=1` means a varuint counter follows. The shift means only 31 bits remain for the diff payload — V2 implicitly limits clocks to 31 bits. On `write(v)`: if `v - this.s == this.diff` (same delta as the current run), keep growing; else flush and reset with the new diff. The decoder maintains a `s` accumulator and reconstructs by adding each diff repeatedly.
- `StringEncoder` (lines 791-820): accumulates strings into a small staging buffer (flushes to `sarr` when `> 19` chars); on `toUint8Array` joins all into one big string, emits via `writeVarString`, then appends the UintOptRle length column verbatim (no length prefix on the appended block — readers know the StringEncoder's varstring boundary then read the rest as the length column).

`RleIntDiffEncoder` (lib0 lines 593-624) is defined but **not used by `UpdateEncoderV2`** in current yjs. Listed here for completeness — ygo doesn't need it for V2 parity but the primitive is small enough to port alongside the others.

---

## 3. Block-write pass (encoder side)

V2's per-block writer fans out to columns instead of streaming bytes. yjs `src/utils/UpdateEncoder.js`:

- `writeLeftID(id)` (lines 156-159): `clientEncoder.write(id.client); leftClockEncoder.write(id.clock)`.
- `writeRightID(id)` (lines 164-167): `clientEncoder.write(id.client); rightClockEncoder.write(id.clock)`.
- `writeClient(client)` (lines 171-173): `clientEncoder.write(client)`.
- `writeInfo(info)` (lines 177-179): `infoEncoder.write(info)`.
- `writeString(s)` (lines 183-185): `stringEncoder.write(s)`.
- `writeParentInfo(isYKey)` (lines 189-191): `parentInfoEncoder.write(isYKey ? 1 : 0)`.
- `writeTypeRef(info)` (lines 195-197): `typeRefEncoder.write(info)`.
- `writeLen(len)` (lines 201-205): `lenEncoder.write(len)`.
- `writeAny(any)` (lines 209-211): writes directly into `restEncoder` via the lib0 Any TLV.
- `writeBuf(buf)` (lines 215-217): writes varbuf directly into `restEncoder`.
- `writeKey(key)` (lines 227-240): key-table lookup. If `keyMap.has(key)` emit cached clock via `keyClockEncoder.write(cachedClock)`. Else emit `keyClockEncoder.write(this.keyClock)`, `stringEncoder.write(key)`, register `keyMap.set(key, this.keyClock++)`. **yrs note**: `yrs/src/updates/encoder.rs:281` carries the comment `//TODO: this is wrong (key_table is never updated), but this behavior matches Yjs`. The "bug" is that yjs never actually populates `keyMap` on the encoder side — every call falls into the "else" branch and writes a fresh string. The keyClock counter still increments but it always equals the index of the just-written string. We must replicate this bug-for-bug or break wire compat.

Pseudo-code for writing one `Item` block to V2 (semantically equivalent to V1's `ItemSlice::encode`, just with column dispatch):

```
fn writeItemV2(enc, item, slice):
  info = computeInfoByte(item)
  enc.infoEncoder.write(info)                              // → infoBytes column

  if info & HAS_ORIGIN:
    enc.clientEncoder.write(origin.client)                 // → clientBytes column
    enc.leftClockEncoder.write(origin.clock)               // → leftClockBytes column
  if info & HAS_RIGHT_ORIGIN:
    enc.clientEncoder.write(rightOrigin.client)            // → clientBytes column (shared!)
    enc.rightClockEncoder.write(rightOrigin.clock)         // → rightClockBytes column
  if cantCopyParentInfo:
    enc.parentInfoEncoder.write(isRootTypeName ? 1 : 0)    // → parentInfoBytes column
    if isRootTypeName:
      enc.stringEncoder.write(parentName)                  // → stringBytes column
    else:
      enc.clientEncoder.write(parentItemID.client)         // → clientBytes column (shared!)
      enc.leftClockEncoder.write(parentItemID.clock)       // → leftClockBytes column (shared!)
    if item.parentSub:
      enc.writeKey(item.parentSub)                         // → keyClockBytes + maybe stringBytes
  encodeContentV2(enc, item.content, slice)                // → typeRef/len/string/rest as content demands
```

Key invariant: **the `clientEncoder` is shared across `writeLeftID`, `writeRightID`, and `writeClient`** — every client-ID emission goes to the same column. The decoder reads them back in the same call order. This is why iteration order between encode and decode MUST be identical.

---

## 4. Block-read pass (decoder side)

The decoder constructs all column decoders upfront from the wire bytes, then pulls one value from each per block iteration. yjs `src/utils/UpdateDecoder.js:96-108` builds the UpdateDecoderV2 by reading column decoders in this exact order:

1. Feature flag byte (read and discarded, must be `0x00`).
2. `keyClockDecoder` from next `readVarUint8Array`.
3. `clientDecoder` from next `readVarUint8Array`.
4. `leftClockDecoder` from next `readVarUint8Array`.
5. `rightClockDecoder` from next `readVarUint8Array`.
6. `infoDecoder` from next `readVarUint8Array`.
7. `stringDecoder` from next `readVarUint8Array`.
8. `parentInfoDecoder` from next `readVarUint8Array`.
9. `typeRefDecoder` from next `readVarUint8Array`.
10. `lenDecoder` from next `readVarUint8Array`.
11. `restDecoder` = whatever remains (no length prefix, read to EOF).

Each `readVarUint8Array` reads a varuint length then a slice of that many bytes; the slice gets wrapped in a fresh `Decoder` and handed to the appropriate column-decoder constructor (which lazily decodes RLE pairs on demand via `.read()`).

Read methods then pull from the parallel decoders:

- `readLeftID()` (lines 111-114): `new ID(clientDecoder.read(), leftClockDecoder.read())`.
- `readRightID()` (lines 117-120): `new ID(clientDecoder.read(), rightClockDecoder.read())`.
- `readClient()` (lines 125-127): `clientDecoder.read()`.
- `readInfo()` (lines 131-133): `infoDecoder.read()`.
- `readString()` (lines 137-139): `stringDecoder.read()`.
- `readParentInfo()` (lines 143-145): `parentInfoDecoder.read() === 1`.
- `readTypeRef()` (lines 149-151): `typeRefDecoder.read()`.
- `readLen()` (lines 155-157): `lenDecoder.read()`.
- `readKey()` (lines 161-180): mirrors the encoder's table — read `keyClock` from `keyClockDecoder`, if the local `keys[]` array has it return cached, else read a string from `stringDecoder`, push to `keys[]`, return. (The keys array IS populated on decode, even though the encoder's keyMap isn't — asymmetric, intentional, matches yjs.)

The outer block-iteration loop is **the same as V1** — V2 only changes how individual `writeFoo`/`readFoo` calls are routed. The top-level structure (varuint client-count, per-client (block-count, client-id, clock-start) headers, per-block info-byte dispatch into Item/GC/Skip) is byte-identical conceptually to V1, but now those varuints actually go through the column decoders (so e.g. `read_var` for `block_count` becomes `lenDecoder.read()` semantically — though in practice yrs/yjs still write the top-level client-count and block-count as plain varuints into `restEncoder`, NOT through a column; verify against the actual `Update::encode_diff` calls when porting).

---

## 5. lib0 RLE primitives we need to port

Five primitives to ship in a new `internal/lib0/rle.go` (estimated effort calibrated against the V1 port which clocked in at ~250 LOC for varuint/varstring/varbuf):

| Primitive | Encoder + decoder LOC | Effort | Notes |
|---|---|---|---|
| `RleEncoder` / `RleDecoder` | ~60 LOC | Small | Pair of `(value, count-1)`. Caller supplies a value-writer func (`writeUint8` for V2 use). Stateful: tracks `s, count`. Tail-flush on `Bytes()`. |
| `UintOptRleEncoder` / `Decoder` | ~80 LOC | Small | `flushUintOptRleEncoder` packs `(value, count-2)` with sign-bit trick (positive = single, negative = run+count). 31-bit value limit because of the sign bit. |
| `IntDiffOptRleEncoder` / `Decoder` | ~100 LOC | Medium | `(diff << 1) \| hasCount` packed in first varint of each run; 31-bit diff range (one bit lost to the hasCount flag; the lib0 comment at line 745 says "only five bits remain" but that's per first byte of the varint, not the total). Tracks `s, count, diff` state. Decoder needs accumulator `s`. |
| `RleIntDiffEncoder` / `Decoder` | ~80 LOC | Small | NOT used by UpdateEncoderV2 today but it's a one-page primitive and ports cheaply; future-proof. Skip if cutting scope. |
| `StringEncoder` / `Decoder` | ~80 LOC | Medium | Concatenates strings into one `Encoder` then appends a `UintOptRle` length column. Encoder has a 19-char staging buffer for fewer slice copies. Decoder reverses: read varstring, then construct a UintOptRle decoder on the remaining bytes for the length stream, then slice the big string by accumulated lengths. |

Total RLE primitives: **~400-600 LOC** including round-trip tests against fixtures pulled from lib0's own test suite (use lib0's `encoding.test.js` test vectors as cross-language ground truth).

Pre-condition: **these primitives MUST ship in their own package (`internal/lib0/rle.go`) with zero dependencies on `internal/update` or `internal/state`**. They are a leaf module like `internal/lib0/varint.go` already is. V2 encoder/decoder code imports them, not the other way around.

---

## 6. V1 vs V2 interop

V1 and V2 are **wire-incompatible** — there is no leading version byte, no magic header, and no negotiation in the wire format itself (confirmed: `Update::EMPTY_V1 = &[0, 0]` at `yrs/src/update.rs:101`, `Update::EMPTY_V2 = &[0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0]` at `yrs/src/update.rs:102` — both start with `0x00` byte sequences, neither carries a self-identifying marker).

How peers actually know which version they're getting:

1. **Persistence layers** know per-store. y-leveldb writes the version into the LevelDB key prefix; y-indexeddb writes it as the object-store name. The selection is metadata, not in-update.
2. **Network sync (y-protocols/sync)** uses *different message-type codes* for V1 vs V2 update messages (yjs `y-protocols/sync.js`: `messageYjsUpdate = 2` vs `messageYjsUpdateV2 = 4`). The sync envelope says which decoder to use; the inner update bytes are version-agnostic.
3. **API selection** is the call site's job: yjs has `Y.applyUpdate` vs `Y.applyUpdateV2`, `Y.encodeStateAsUpdate` vs `Y.encodeStateAsUpdateV2`. yrs mirrors with `Update::decode_v1` vs `Update::decode_v2` (separate entry points; no autodetect).

A V2 decoder fed V1 bytes will misparse silently: the V1 leading `client_count` varuint gets read as the V2 keyClock column length, often producing valid-looking but garbage column data, which then crashes in `lenEncoder.read()` returning an absurd run-length, or worse: returns plausible but wrong block decodes. There is no checksum, no length-of-message field, and no parity check. The implication for ygo: **the public API MUST expose `ApplyUpdateV1` / `ApplyUpdateV2` as distinct functions and document that mixing is undefined behaviour.** No autodetect.

---

## 7. Go translation choices

**File layout:**

```
internal/
  lib0/
    rle.go              NEW — RleEncoder, UintOptRleEncoder, IntDiffOptRleEncoder, StringEncoder, RleIntDiffEncoder (+ decoders)
    rle_test.go         NEW — cross-language test vectors from lib0's encoding.test.js
  update/
    encoder.go          (V1 today, no change)
    decoder.go          (V1 today, no change)
    encoder_v2.go       NEW — EncoderV2 struct + methods, to_vec column concatenation
    decoder_v2.go       NEW — DecoderV2 struct + methods, constructor reads all column decoders upfront
    update.go           ADD: Update.EncodeV2(*EncoderV2), Update.DecodeV2(*DecoderV2) entry points; keep Encode/Decode as V1 aliases for back-compat with existing callers
```

**Why new files, not extending `encoder.go`:** V2 is genuinely a different shape — different struct fields (10 column buffers vs 1 raw buffer), different `to_vec` semantics (length-prefixed concatenation vs identity), different state (the key-table and per-column accumulators). Squeezing both into one file means `if version == V2` branches in every write method — ugly and easy to break wire-compat on. Keep them parallel. The shared interface (if we extract one) goes in a third file (`encoder_iface.go`) only if a real second user emerges; YAGNI for now.

**Dispatch entry points:**

```go
// V1 (existing today, unchanged):
func ApplyUpdate(d *doc.Doc, bytes []byte) error
func EncodeStateAsUpdate(d *doc.Doc, remoteSV state.StateVector) []byte

// V2 (new):
func ApplyUpdateV2(d *doc.Doc, bytes []byte) error
func EncodeStateAsUpdateV2(d *doc.Doc, remoteSV state.StateVector) []byte
```

NO version enum, NO autodetect, NO shared `ApplyUpdate(bytes, version)` signature. Caller picks per-call.

**Default:** V1 stays the default for ygo's own protocol-sync paths and persistence (matches what V1-only adopters will run). V2 is an opt-in second encoder available for Hocuspocus-compatibility and large-document optimisation. Document in package doc-comments that V1 and V2 are not interchangeable.

**Pre-conditions / ship order:**

1. **lib0 RLE primitives FIRST.** They are a self-contained leaf package with no encoding-layer dependencies. Ship them with their own test suite (port lib0's `encoding.test.js` cases) and merge in their own commit. This unblocks everything downstream.
2. V2 column encoders/decoders second. They depend only on lib0 (existing varint/varstring + new RLE).
3. `Update.EncodeV2` / `Update.DecodeV2` third. These wire the column encoders into the existing block-write/block-read loops. Block-level logic (content kinds, info-byte, parent dispatch) is unchanged from V1 and reused.
4. Cross-language fixtures fourth. JS yjs ships `Y.encodeStateAsUpdateV2` — generate fixtures from a pinned `yjs@^13.6.x` and assert byte-for-byte round-trip.

---

## 8. Estimated implementation scope

Breaking down by sub-layer with LOC + commit-count estimates:

| Sub-layer | LOC | Commits |
|---|---|---|
| lib0 RLE primitives (`internal/lib0/rle.go`) + tests | 400-600 | 1 |
| V2 column encoder/decoder (`encoder_v2.go`, `decoder_v2.go`) | 300-500 | 1 |
| `Update.EncodeV2` / `Update.DecodeV2` + `ApplyUpdateV2` / `EncodeStateAsUpdateV2` public API | 200-400 | 1 |
| Cross-language fixtures (Node generator script + Go test cases + testdata bytes) | 200 | 1 (combined with previous) |
| **Total** | **~1100-1700 LOC** | **3-4 commits** |

These estimates assume the V1 port is the baseline (it is — it shipped). The biggest unknown is the StringEncoder's varstring boundary handling on decode and the IntDiffOptRle 31-bit edge cases, both of which warrant careful test vectors. Budget +20% if fixtures expose subtle issues.

---

## 9. Gotchas — implementer must not miss

- **Column-encoder iteration order matters.** The order in `toUint8Array()` (keyClock → client → leftClock → rightClock → info → string → parentInfo → typeRef → len → rest) MUST exactly match the decoder's read order in `UpdateDecoderV2`'s constructor. Off-by-one in the order silently corrupts every block. There is no length-self-check, no checksum, and the bytes will plausibly decode as garbage. Hard-code the order in one named function on each side and assert equality in a test.
- **lib0 RLE encoders carry state — flushing on `toUint8Array` is non-obvious.** `RleEncoder`, `UintOptRleEncoder`, and `IntDiffOptRleEncoder` all maintain `(s, count[, diff])` tuples that hold the *current run*. The run is NOT written to bytes on `write(v)` — only on the next `write` of a different value (which flushes the previous run) OR on the terminal `toUint8Array()` call. If you forget the terminal flush, the last run silently disappears. Mirror this in Go: `Bytes()` must call the flush helper before returning the buffer.
- **DiffRle encodes DELTAS, not absolute values.** `IntDiffOptRleEncoder.write(v)` stores `diff = v - this.s`. The decoder reconstructs by maintaining its own running `s` accumulator and adding each decoded diff. Resetting state mid-stream (e.g. for a new client run) requires writing a record that breaks the current run — the encoder/decoder will then naturally re-sync on a fresh diff. There is no explicit "reset" marker — the diff just changes and starts a new run.
- **The 31-bit limit on `IntDiffOptRleEncoder`.** `(diff << 1) | hasCount` consumes one bit. Clock values feeding `leftClockEncoder`/`rightClockEncoder`/`keyClockEncoder` MUST fit in 31 bits. yjs documents are sub-2-billion-op in practice, so this is fine — but assertion-test it in Go to fail loudly if a clock overflows.
- **`StringEncoder` has TWO internal segments, not one.** `toUint8Array()` first writes `writeVarString(concatenated)` then APPENDS `writeUint8Array(lensE.toUint8Array())` raw without a length prefix. The decoder reads the varstring (which has its own internal length) then constructs a fresh decoder on the *remaining bytes of the StringEncoder column* for the length stream. The column's outer length prefix (in V2's toUint8Array) bounds the whole thing. Easy to mis-translate as "two length-prefixed sub-columns" — it's not.
- **The yjs `writeKey` bug.** yjs's `keyMap` is never actually populated on encode (the table-lookup branch never fires), so every `writeKey` call writes a fresh string into `stringEncoder` and increments `keyClock`. The decoder *does* populate its key-table, asymmetrically. yrs replicates this bug-for-bug (`yrs/src/updates/encoder.rs:281` comment). ygo MUST also replicate it — fixing the encoder would emit shorter bytes but the resulting wire would break compat with every existing yjs/yrs/Hocuspocus node.
- **Top-level block-run structure stays the same as V1.** Don't accidentally column-encode the outer `client_count`, per-client `block_count`, `client_id`, `clock_start` headers — these still go through the encoder's underlying byte stream (the `restEncoder` in V2 terms, effectively). Only per-block field writes (info, origin, right_origin, parent, parent_sub, content) get column-routed. Verify against yrs's `Update::encode_diff` paths.
- **V1 fixtures don't test V2; new cross-language fixtures needed.** The existing `testdata/wire/v1/*.bin` corpus tests only V1 byte sequences. V2 needs a parallel `testdata/wire/v2/*.bin` corpus generated from yjs `Y.encodeStateAsUpdateV2` with the same logical test cases (single insert, multi-client run, delete-set, every content kind). Don't try to derive V2 fixtures from V1 fixtures programmatically — that would just test our own encoder against itself; the whole point of the fixtures is to assert byte-level parity with yjs.
- **No version byte means no autodetect.** Document loudly in the public API that `ApplyUpdateV1` on V2 bytes (and vice versa) is undefined behaviour. If the caller doesn't know which version their bytes are, that's a layer-above problem (transport metadata, file-format header, etc.) — not something the update layer can recover from. Don't add a "try V1, fall back to V2" path; both can silently misparse the other.
