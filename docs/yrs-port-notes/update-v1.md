# yrs port notes: V1 update wire format (encoder, decoder, state vector, delete set/IdSet, Update)

> Source: yrs commit `639db2038fa44d09f628a2650fd900e3b109ad1e` (main HEAD as of 2026-05-15). Files cited: `yrs/src/updates/encoder.rs`, `yrs/src/updates/decoder.rs`, `yrs/src/updates/mod.rs`, `yrs/src/update.rs`, `yrs/src/state_vector.rs`, `yrs/src/id_set.rs`, `yrs/src/ids.rs` (`IdRanges`/`IdMapInner` machinery), `yrs/src/store.rs` (`encode_diff`, `write_blocks_from`, `write_blocks_to`, `diff_state_vectors`, pending fields), `yrs/src/slice.rs` (`ItemSlice::encode`, `GCSlice::encode`), `yrs/src/block.rs` (`Item::encode`, `Item::info`, `ItemContent::encode`/`encode_slice`/`decode`, ref-number constants, info-byte flag constants), `yrs/src/transaction.rs::TransactionMut::apply_update`. There is no `yrs/src/updates/mod.rs` body beyond `pub mod decoder; pub mod encoder;` (`updates/mod.rs:1-2`).

> Notation: `[a..b)` is half-open, `[a..b]` inclusive. Field names in this note match the yrs source. `Range<u32>` from std means `start: u32, end: u32` interpreted as `[start..end)`.

---

## 1. Wire-format overview

Three independent wire entities:

**(a) StateVector** (`state_vector.rs:110-118`):
```
varuint  client_count
repeat client_count times:
    varuint  client_id   // ClientID.get() → u64 in default build
    varuint  clock       // u32
```
Empty SV is the single byte `0x00`. There is no header, no version byte, no length prefix beyond `client_count`. Iteration order on emit is **HashMap order — non-deterministic** (`state_vector.rs:113` iterates `self.iter()` which is `HashMap::iter`). See §4.

**(b) DeleteSet / IdSet** (`id_set.rs:320-345`):
```
varuint  client_count
repeat client_count times:
    encoder.reset_ds_cur_val()           // V1: no-op; V2: resets diff accumulator
    varuint  client_id                   // u64
    varuint  range_count
    repeat range_count times:
        varuint  start_clock             // V1: absolute; V2: diff from running cursor
        varuint  length                  // length, NOT end-clock
```
Range semantics: the in-memory representation is `Range<u32>` interpreted as half-open `[start..end)`. The wire encodes `start` and `end - start = length` (`id_set.rs:19-32`). Per-client iteration is over a `SmallVec<(Range<u32>, ())>` sorted by `start` with adjacent/overlapping ranges merged on insert (`ids.rs:119-265`). Across clients iteration is **`BTreeMap` order — sorted ascending by `client_id`** (`id_set.rs:134` defines `IdSet(IdMapInner<()>)` where `IdMapInner` wraps `BTreeMap<ClientID, IdRanges<T>>`, `ids.rs:583`). Empty IdSet is `0x00`.

**(c) V1 Update** (`update.rs:393-414` `encode_diff`, `update.rs:497-516` `decode`):
```
[block runs section]
varuint  client_count                    // # of distinct clients with blocks in this update
repeat client_count times:
    varuint  block_count                 // # of block records that follow
    varuint  client_id                   // u64
    varuint  clock_start                 // first block's starting clock (after offset)
    repeat block_count times:
        u8       info_byte               // see §6
        ...conditional fields...         // origin, right_origin, parent, parent_sub, content
[delete set section]
<IdSet wire bytes from (b)>
```
Empty Update is two bytes `[0x00, 0x00]` (`update.rs:97`: `Update::EMPTY_V1 = &[0, 0]`) — zero block-runs followed by zero-client delete-set.

Across-client iteration order on emit is **descending `client_id`** (`update.rs:407` and `store.rs:221`: `diff.sort_by(|a, b| b.0.cmp(&a.0))`). This is a wire-compat invariant, matching JS Yjs.

---

## 2. Encoder trait + V1Encoder struct (`encoder.rs`)

`Encoder` extends `Write` (`encoder.rs:30`):

```rust
pub trait Encoder: Write {
    fn to_vec(self) -> Vec<u8>;
    fn reset_ds_cur_val(&mut self);
    fn write_ds_clock(&mut self, clock: u32);
    fn write_ds_len(&mut self, len: u32);
    fn write_left_id(&mut self, id: &block::ID);
    fn write_right_id(&mut self, id: &block::ID);
    fn write_client(&mut self, client: ClientID);
    fn write_info(&mut self, info: u8);
    fn write_parent_info(&mut self, is_y_key: bool);
    fn write_type_ref(&mut self, info: u8);
    fn write_len(&mut self, len: u32);
    fn write_any(&mut self, any: &Any);
    fn write_json(&mut self, any: &Any);
    fn write_key(&mut self, string: &str);
}
```

`Write` (`encoding/write.rs:11-105`) provides the lower-level primitives the trait method bodies call:
`write_all`, `write_u8`, `write_u16`, `write_u32`, `write_u32_be`, `write_var<T>` (varuint/varint), `write_var_signed`, `write_buf` (lengthed), `write_string` (UTF-8 bytes prefixed by varuint byte-len — Yjs convention), `write_f32`, `write_f64`, `write_i64`, `write_u64`. Direct match to ygo's `internal/lib0` helpers.

V1 backing buffer is just `Vec<u8>` (`encoder.rs:78-87`):

```rust
pub struct EncoderV1 { buf: Vec<u8> }

impl EncoderV1 {
    pub fn new() -> Self { EncoderV1 { buf: Vec::with_capacity(1024) } }
    fn write_id(&mut self, id: &ID) {
        self.write_var(id.client.get());
        self.write_var(id.clock)
    }
}
```

V1 trait method bodies (`encoder.rs:104-167`) — every method either delegates straight to a `Write` primitive on the inner `Vec<u8>` or writes a varuint:

- `to_vec` → returns `self.buf` by move.
- `reset_ds_cur_val` → no-op (V1 does not delta-encode delete-set clocks). V2 zeros a running cursor.
- `write_ds_clock(c)` → `write_var(c)` (absolute u32).
- `write_ds_len(n)` → `write_var(n)` (absolute u32).
- `write_left_id(id)` / `write_right_id(id)` → both call the private `write_id` helper, which is `varuint(client_id.get()) ; varuint(clock)`.
- `write_client(c)` → `write_var(c.get())`.
- `write_info(b)` → single raw `u8`.
- `write_parent_info(b)` → `write_var(if b { 1u32 } else { 0u32 })` (a varuint, **not a single byte** — this is a one-byte payload only because `0` and `1` fit in a single varuint byte).
- `write_type_ref(b)` → single raw `u8`. The decoder counterpart comments `"In Yjs we use read_var_uint but use only 7 bit. So this is equivalent."` (`decoder.rs:164`).
- `write_len(n)` → `write_var(n)`.
- `write_any` → delegates to `Any::encode(self)` (the lib0 Any type-tag wire format — out of scope here, lives in `any.rs`).
- `write_json` → serialises `Any` to a JSON string then `write_string(json)`.
- `write_key(s)` → `write_string(s)` (V1; V2 maintains a key-table).

Side note: V2 (`encoder.rs:168-413`) wraps the buf in a `key_table` plus six RLE/diff-RLE column encoders flushed in a fixed order on `to_vec` with a leading `0x00` "feature flag". Out of scope for the V1 port.

---

## 3. Decoder trait + V1Decoder struct (`decoder.rs`)

`Decoder` extends `Read` (`decoder.rs:29-73`):

```rust
pub trait Decoder: Read {
    fn reset_ds_cur_val(&mut self);
    fn read_ds_clock(&mut self) -> Result<u32, Error>;
    fn read_ds_len(&mut self) -> Result<u32, Error>;
    fn read_left_id(&mut self) -> Result<ID, Error>;
    fn read_right_id(&mut self) -> Result<ID, Error>;
    fn read_client(&mut self) -> Result<ClientID, Error>;
    fn read_info(&mut self) -> Result<u8, Error>;
    fn read_parent_info(&mut self) -> Result<bool, Error>;
    fn read_type_ref(&mut self) -> Result<u8, Error>;
    fn read_len(&mut self) -> Result<u32, Error>;
    fn read_any(&mut self) -> Result<Any, Error>;
    fn read_json(&mut self) -> Result<Any, Error>;
    fn read_key(&mut self) -> Result<Arc<str>, Error>;
    fn read_to_end(&mut self) -> Result<&[u8], Error>;
}
```

V1 decoder is cursor-based, not streaming (`decoder.rs:76-92`):

```rust
pub struct DecoderV1<'a> { cursor: Cursor<'a> }

impl<'a> DecoderV1<'a> {
    pub fn new(cursor: Cursor<'a>) -> Self { DecoderV1 { cursor } }
    fn read_id(&mut self) -> Result<ID, Error> {
        let client: u64 = self.read_var()?;
        let clock = self.read_var()?;
        Ok(ID::new(ClientID::new(client as u64), clock))
    }
}
```

`Cursor` (`encoding/read.rs:43-66`) is `{ buf: &'a [u8], next: usize }` with `read_exact(len)` returning a borrowed slice and `read_u8` / `read_var` / `read_string` / `read_buf` delegations. Random-access by position is possible (`cursor.next` is `pub(crate)` settable) but not used in V1 decode flow. Method bodies `decoder.rs:116-193` mirror the encoder one-to-one. Notable: `read_parent_info` reads a `u32` varuint and tests `== 1`, mirroring the encoder's choice to emit `0u32`/`1u32` rather than a raw byte (`decoder.rs:158-159`).

---

## 4. StateVector encode/decode (`state_vector.rs`)

`StateVector` is a wrapper around `HashMap<ClientID, u32, BuildHasherDefault<ClientHasher>>` (`state_vector.rs:17-18`).

### Encode (`state_vector.rs:110-118`) — verbatim:

```rust
impl Encode for StateVector {
    fn encode<E: Encoder>(&self, encoder: &mut E) {
        encoder.write_var(self.len());
        for (&client, &clock) in self.iter() {
            encoder.write_var(client.get());
            encoder.write_var(clock);
        }
    }
}
```

Line by line:
- `:112` — emit varuint count = number of `(client, clock)` entries.
- `:113` — `self.iter()` is `HashMap::iter` (`state_vector.rs:72-74`). **Iteration order is non-deterministic HashMap order.** This is a wire-compat note: yrs and JS Yjs both emit in hash order, so consumers MUST NOT rely on sort order when comparing two SV byte slices for equality. Round-trip equality is by structural map-equality (`PartialEq` derive on `StateVector`).
- `:114` — varuint of `client_id.get()` (the `u64` value, with the ClientID niche-tag bits masked off — see block.md).
- `:115` — varuint of `clock` (`u32`).

Empty SV = `[0x00]` (count=0, no entries).

### Decode (`state_vector.rs:94-108`) — verbatim:

```rust
impl Decode for StateVector {
    fn decode<D: Decoder>(decoder: &mut D) -> Result<Self, Error> {
        let len = decoder.read_var::<u32>()? as usize;
        let mut sv = HashMap::with_capacity_and_hasher(len, BuildHasherDefault::default());
        let mut i = 0;
        while i < len {
            let client: u64 = decoder.read_var()?;
            let clock = decoder.read_var()?;
            sv.insert(ClientID::new(client), clock);
            i += 1;
        }
        Ok(StateVector(sv))
    }
}
```

Straightforward: read count, loop reading `(varuint client, varuint clock)` pairs. No delta/RLE in V1.

**ygo translation:** since our `BlockStore.GetStateVector()` returns `map[uint64]uint64`, our `StateVector` type is just `type StateVector map[uint64]uint64`. Encode iterates this map directly (Go map iteration is also intentionally randomised — same wire-order non-determinism, same compat). Decode allocates the map with `make(StateVector, count)`.

---

## 5. DeleteSet / IdSet encode/decode (`id_set.rs`, `ids.rs`)

There is no separate `DeleteSet` type on the wire. yrs has a `DeleteSet` **trait** (`id_set.rs` — was at `:121-135` per the existing store.md note; in current main `:206-318` is the inherent `IdSet` impl block; `IdSet` is the only on-wire type). Both delete-sets and undo/redo insert-sets and pending-DS slots all use `IdSet`.

### Per-range Range<u32> wire encode (`id_set.rs:19-32`) — verbatim:

```rust
impl Encode for Range<u32> {
    fn encode<E: Encoder>(&self, encoder: &mut E) {
        encoder.write_ds_clock(self.start);
        encoder.write_ds_len(self.end - self.start)
    }
}

impl Decode for Range<u32> {
    fn decode<D: Decoder>(decoder: &mut D) -> Result<Self, Error> {
        let clock = decoder.read_ds_clock()?;
        let len = decoder.read_ds_len()?;
        Ok(clock..(clock + len))
    }
}
```

`self.start..self.end` is half-open. The wire emits `(start, end - start)` = `(start, length)`. Decoder reconstructs `clock..(clock + len)`. **The `last_id`-style off-by-one flagged in `store.md` is irrelevant on the wire**: BlockRange uses `last_id = id.clock + len - 1` for inclusive logic, but here the encoded length is exactly `len = end - start` for the half-open range. The deletion of `n` clocks at start `s` → emit `(s, n)`, decode → `s..s+n`, which contains clocks `s, s+1, …, s+n-1`. Correct.

### Per-client IdRange wire (`id_set.rs:78-97`) — verbatim:

```rust
impl Encode for IdRanges<()> {
    #[inline]
    fn encode<E: Encoder>(&self, encoder: &mut E) {
        encoder.write_var(self.len() as u32);
        for (range, _) in self.iter() {
            range.encode(encoder);
        }
    }
}

impl Decode for IdRanges<()> {
    fn decode<D: Decoder>(decoder: &mut D) -> Result<Self, Error> {
        let len: u32 = decoder.read_var()?;
        let mut ranges = SmallVec::with_capacity(len as usize);
        for _ in 0..len {
            ranges.push((Range::decode(decoder)?, ()));
        }
        Ok(IdRanges::from_raw(ranges))
    }
}
```

Per-client iteration is over the inner `SmallVec<(Range<u32>, ())>` which is **maintained sorted ascending by `range.start`** with adjacent/overlapping merged on insert (`ids.rs:119-265` `IdRanges::insert_with`). The encoder relies on this ordering — it does NOT sort at emit time. Note `from_raw` (`ids.rs:540-544`) is called by `decode` and *does not re-sort or merge*; it trusts the wire input. **This means a malformed update (unsorted ranges) would silently produce a malformed in-memory IdRanges.** Not flagged in source as a TODO — yrs trusts upstream.

Adjacent-range merge logic on insert (`ids.rs:119-265`) — key conditional summary:
- Tail-fast path (`:126-146`): if `range.start >= last.start`, either push (disjoint, `range.start > last.end`), extend last (`range.start <= last.end`, same value), or push adjacent (`range.start == last.end`, different value).
- General path (`:148-181`): binary-search `partition_point` for `lo`, expand range with right-coalesce + left-coalesce neighbours when values match. For `IdRange = IdRanges<()>` the value type is `()` so values always "match" → all touching/overlapping ranges merge.

### IdSet wire (`id_set.rs:320-345`) — verbatim:

```rust
impl Encode for IdSet {
    fn encode<E: Encoder>(&self, encoder: &mut E) {
        encoder.write_var(self.0.len() as u32);
        for (&client_id, block) in self.0.iter() {
            encoder.reset_ds_cur_val();
            encoder.write_var(client_id.get());
            block.encode(encoder);
        }
    }
}

impl Decode for IdSet {
    fn decode<D: Decoder>(decoder: &mut D) -> Result<Self, Error> {
        let mut set = Self::new();
        let client_len: u32 = decoder.read_var()?;
        let mut i = 0;
        while i < client_len {
            decoder.reset_ds_cur_val();
            let client: u64 = decoder.read_var()?;
            let range = IdRange::decode(decoder)?;
            set.0.clients_mut().insert(ClientID::new(client), range);
            i += 1;
        }
        Ok(set)
    }
}
```

Line by line:
- `:322` — varuint client-count.
- `:323` — iterates `IdMapInner<()>` which is a `BTreeMap` (`ids.rs:583`). **Across-client order on wire is sorted ASCENDING by `client_id`.** This is the opposite of update.rs block-runs (descending) — see §6. Wire-compat critical.
- `:324` — `reset_ds_cur_val()` is per-client. No-op in V1; in V2 resets the diff accumulator that allows ranges within a client to be encoded as deltas.
- `:325` — varuint client_id.
- `:326` — delegate to `IdRanges::encode` (per-client range-count + ranges).

Empty IdSet = `[0x00]`.

---

## 6. V1 Update encode (`update.rs`, `store.rs`, `slice.rs`, `block.rs`)

There are two encode entry points to know:

1. `Store::encode_diff` (`store.rs:205-213`) — the **diff** path. Used by `encode_state_as_update_v1(remote_sv)`. Walks the live block store, filters by remote SV, emits per-client runs, then emits the live-store delete-set.
2. `Update::encode_diff` (`update.rs:393-414`) — the same logic but operating on a parsed-but-not-yet-integrated `Update` (e.g. for `merge_pending_v1`, transaction observer payloads).

Both produce identical wire format. The store path is the canonical one.

### Top-level: `Store::encode_diff` (`store.rs:205-213`) — verbatim:

```rust
pub fn encode_diff<E: Encoder>(&self, sv: &StateVector, encoder: &mut E) {
    //TODO: this could be actually 2 steps:
    // 1. create Diff of block store and remote state vector (it can have lifetime of bock store)
    // 2. make Diff implement Encode trait and encode it
    // this way we can add some extra utility method on top of Diff (like introspection) without need of decoding it.
    self.write_blocks_from(sv, encoder);
    let delete_set = IdSet::from_store(&self.blocks);
    delete_set.encode(encoder);
}
```

(FIXME at `store.rs:206-209` — yrs author's TODO, unrelated to wire format.)

Order: blocks first, then DS. Always.

### Per-client block runs: `Store::write_blocks_from` (`store.rs:215-243`) — verbatim:

```rust
pub(crate) fn write_blocks_from<E: Encoder>(&self, sv: &StateVector, encoder: &mut E) {
    let local_sv = self.blocks.get_state_vector();
    let mut diff = Self::diff_state_vectors(&local_sv, sv);

    // Write items with higher client ids first
    // This heavily improves the conflict algorithm.
    diff.sort_by(|a, b| b.0.cmp(&a.0));

    encoder.write_var(diff.len());
    for (client, clock) in diff {
        let blocks = self.blocks.get_client(&client).unwrap();
        let clock = clock.max(blocks.get(0).map(|i| i.clock_start()).unwrap_or_default()); // make sure the first id exists
        let start = blocks.find_pivot(clock).unwrap();
        // write # encoded structs
        encoder.write_var(blocks.len() - start);
        encoder.write_client(client);
        encoder.write_var(clock);
        let first_block = blocks.get(start).unwrap();
        // write first struct with an offset
        let offset = clock - first_block.clock_start();
        let mut slice = first_block.as_slice();
        slice.trim_start(offset);
        slice.encode(encoder);
        for i in (start + 1)..blocks.len() {
            let block = &blocks[i];
            block.as_slice().encode(encoder);
        }
    }
}
```

Line by line:
- `:216` — compute local SV (full store-state).
- `:217` — `diff_state_vectors(local, remote)` (`store.rs:245-259`) returns `Vec<(ClientID, u32)>` containing one entry per client where local has more clocks than remote. For each remote client where `local > remote`, push `(client, remote_clock)`. For each local client absent from remote (`remote.get(c) == 0`), push `(client, 0)`. **The same client may be pushed twice if it's both in remote and local-only-extra** — but `local > remote` already excludes the `remote == 0` case after the first loop, so this is OK in practice; still worth noting if porting.
- `:221` — sort **descending** by `client_id`. Wire-compat invariant. Unlike IdSet which sorts ascending.
- `:223` — varuint client-run-count.
- `:224-242` — per client:
  - `:226` — clamp `clock` to be at least the first block's `clock_start` (block 0 might start at clock>0 if we've GC'd a prefix — yrs's `BlockStore::get_clock` returns the **end** clock, so we can't blindly trust remote SV's lower bound).
  - `:227` — `find_pivot(clock)` returns the index of the first block whose range contains `clock`. **Panics on `unwrap()` if no such block** — caller responsibility.
  - `:229` — varuint `block_count = total - start`. **Block-count comes BEFORE client-id and clock_start**, contrary to the order one might guess from the docs. Wire-compat critical.
  - `:230` — varuint `client_id` via `write_client`.
  - `:231` — varuint `clock_start` (the absolute starting clock for the run, after clamp).
  - `:233-237` — first block: compute offset from its native start, build an `ItemSlice` trimmed at start, encode. The trim ensures we emit the right partial block when the run starts mid-block.
  - `:238-241` — subsequent blocks: full slice encode each.

`diff_state_vectors` (`store.rs:245-259`):

```rust
fn diff_state_vectors(local_sv: &StateVector, remote_sv: &StateVector) -> Vec<(ClientID, u32)> {
    let mut diff = Vec::new();
    for (client, &remote_clock) in remote_sv.iter() {
        let local_clock = local_sv.get(client);
        if local_clock > remote_clock {
            diff.push((*client, remote_clock));
        }
    }
    for (client, _) in local_sv.iter() {
        if remote_sv.get(client) == 0 {
            diff.push((*client, 0));
        }
    }
    diff
}
```

### Per-block record: `ItemSlice::encode` (`slice.rs:181-233`) — verbatim:

```rust
pub fn encode<E: Encoder>(&self, encoder: &mut E) {
    let item = self.ptr.deref();
    let mut info = item.info();
    let origin = if self.adjacent_left() {
        item.origin
    } else {
        Some(ID::new(item.id.client, item.id.clock + self.start - 1))
    };
    if origin.is_some() {
        info |= HAS_ORIGIN;
    }
    let cant_copy_parent_info = info & (HAS_ORIGIN | HAS_RIGHT_ORIGIN) == 0;
    encoder.write_info(info);
    if let Some(origin_id) = origin {
        encoder.write_left_id(&origin_id);
    }
    if self.adjacent_right() {
        if let Some(right_origin_id) = item.right_origin.as_ref() {
            encoder.write_right_id(right_origin_id);
        }
    }
    if cant_copy_parent_info {
        match &item.parent {
            TypePtr::Branch(branch) => {
                if let Some(block) = branch.item {
                    encoder.write_parent_info(false);
                    encoder.write_left_id(block.id());
                } else if let Some(name) = branch.name.as_deref() {
                    encoder.write_parent_info(true);
                    encoder.write_string(name);
                } else {
                    unreachable!("Could not get parent branch info for item")
                }
            }
            TypePtr::Named(name) => {
                encoder.write_parent_info(true);
                encoder.write_string(name);
            }
            TypePtr::ID(id) => {
                encoder.write_parent_info(false);
                encoder.write_left_id(id);
            }
            TypePtr::Unknown => {
                panic!("Couldn't get item's parent")
            }
        }

        if let Some(parent_sub) = item.parent_sub.as_ref() {
            encoder.write_string(parent_sub.as_ref());
        }
    }
    item.content.encode_slice(encoder, self.start, self.end);
}
```

Line by line:

- `:183` — `info()` (`block.rs:1451-1457`):
  ```rust
  pub fn info(&self) -> u8 {
      let info = if self.origin.is_some() { HAS_ORIGIN } else { 0 }
          | if self.right_origin.is_some() { HAS_RIGHT_ORIGIN } else { 0 }
          | if self.parent_sub.is_some() { HAS_PARENT_SUB } else { 0 }
          | (self.content.get_ref_number() & 0b1111);
      info
  }
  ```
  Constants:
  - `HAS_RIGHT_ORIGIN = 0b01000000` = `0x40` (`block.rs:65`)
  - `HAS_ORIGIN       = 0b10000000` = `0x80` (`block.rs:68`)
  - `HAS_PARENT_SUB   = 0b00100000` = `0x20` (`block.rs:72`)
  - The low nibble (bits 0..3) is the content ref-number: `0..11` (see §6 content list).
  - Bits `0b00010000` (bit 4) and `0b00001100` (bits 2..3 not all used by ref nums up to 11) are effectively reserved/unused. Bit 4 is **not** observed touched by the encoder — but the decoder dispatches to `BLOCK_GC_REF_NUMBER (=0)` and `BLOCK_SKIP_REF_NUMBER (=10)` purely on `info as u8` matched directly, NOT on `info & 0b1111`. This means an Item whose info-byte happens to equal `10` (e.g. content-Move=11, ah no — wait: ref nums 0 and 10 are reserved for GC/Skip, no Item can have ref_num 0 or 10). Item ref_nums are 1,2,3,4,5,6,7,8,9,11.
- `:184-188` — origin override: if this slice does NOT start at the underlying item's clock-start, override `origin` to `(client, clock + start - 1)`, the ID immediately preceding the slice. This implements partial-block emission: a sliced item carries a synthetic origin pointing at its own previous clock.
- `:189-191` — recompute `info |= HAS_ORIGIN` after the override (the underlying item may have had `origin = None` originally).
- `:192` — `cant_copy_parent_info = (origin == None && right_origin == None)`. Only when there are no neighbour links can the decoder NOT infer parent from neighbours, so parent must be written. **Otherwise parent is omitted from the wire entirely** — the receiver looks up neighbour's parent during integrate.
- `:193` — emit info byte.
- `:194-196` — emit origin ID (only if `origin.is_some()` — checked again because the override on `:184` produced a fresh `Option`).
- `:197-201` — emit right_origin ID, **only if this slice ends at the underlying item's clock-end**. Otherwise the right-origin gets dropped because the slice doesn't actually reach that boundary.
- `:202-231` — parent emission (only when `cant_copy_parent_info`):
  - `Branch + has parent item ID` → `write_parent_info(false)` then `write_left_id(parent_item_id)`.
  - `Branch + has parent name` (root type) → `write_parent_info(true)` then `write_string(name)`.
  - `Named(name)` → `write_parent_info(true)` then `write_string(name)`.
  - `ID(id)` → `write_parent_info(false)` then `write_left_id(id)`.
  - `Unknown` → `panic!("Couldn't get item's parent")` — only happens in degenerate states; an item with parent=Unknown would have been GC'd before reaching this path (see `update.rs:265-267` where unintegrated items convert to GC range).
  - `:228-230` — emit `parent_sub` (the map key) if present. Only emitted when parent is also written; the receiver inherits parent_sub from neighbour otherwise.
- `:232` — emit content via `ItemContent::encode_slice(encoder, start, end)` (inclusive bounds, see §6 content table).

### Item::encode (full block, no slicing) (`block.rs:945-985`)

Used by `BlockCarrier::encode` (`update.rs:620-628`) for the in-memory `Update`-side path. Identical structure to `ItemSlice::encode` except no per-slice origin override and no slice-bounded content (calls `self.content.encode(encoder)`, not `encode_slice`). For wire-compat both must produce identical bytes when slice covers the full block.

### GC + Skip block records

GC: `GCSlice::encode` (`slice.rs:312-315`):
```rust
pub fn encode<E: Encoder>(&self, encoder: &mut E) {
    encoder.write_info(BLOCK_GC_REF_NUMBER);   // 0
    encoder.write_len(self.len());             // varuint
}
```

Skip: emitted by `BlockCarrier::encode_with_offset` (`update.rs:603-612`):
```rust
BlockCarrier::Skip(x) => {
    encoder.write_info(BLOCK_SKIP_REF_NUMBER); // 10
    encoder.write_var(x.len - offset);
}
```

Note Skip uses `write_var` directly (varuint) and GC uses `write_len` — in V1 these are identical (both `write_var<u32>`); in V2 they diverge.

### Content encoding per kind (`block.rs:1844-1872` `ItemContent::encode`)

Ref numbers (`block.rs:32-62`): `Deleted=1, JSON=2, Binary=3, String=4, Embed=5, Format=6, Type=7, Any=8, Doc=9, Move=11`. Plus block-level: `GC=0, Skip=10`.

For each content kind, the wire-side bytes that follow the info-byte (and any conditional ID/parent/parent_sub fields):

| Ref# | Variant | Wire bytes (after parent fields) |
|---|---|---|
| 0 | `GC` | `varuint len` (block-level, no parent fields ever) |
| 1 | `Deleted(len)` | `varuint len` |
| 2 | `JSON(Vec<String>)` | `varuint count` then `count` × `varstring` |
| 3 | `Binary(Vec<u8>)` | `varbuf` (varuint len + bytes) |
| 4 | `String(SplittableString)` | `varstring` |
| 5 | `Embed(Any)` | `write_json(Any)` = JSON-encoded as `varstring` |
| 6 | `Format(key, value)` | `write_key(key)` (`= varstring` in V1) then `write_json(value)` |
| 7 | `Type(Branch)` | `branch.type_ref.encode(encoder)` — see `types/mod.rs::TypeRef::encode` (1 ref-num byte + optional name varstring for XmlElement/XmlHook, etc.) |
| 8 | `Any(Vec<Any>)` | `varuint count` then `count` × `Any::encode` (lib0 Any TLV) |
| 9 | `Doc(_, Doc)` | `doc.options().encode(encoder)` — embedded sub-doc options blob |
| 10 | `Skip` | `varuint len` (block-level) |
| 11 | `Move(Box<Move>)` | `m.encode(encoder)` — see `move.rs` |

`encode_slice(start, end)` differs from `encode` for these variants only:
- `Deleted`: emits `end - start + 1` instead of `len` (slice length).
- `String`: emits the UTF-16-bounded substring `[start..=end]`.
- `JSON`: emits `end - start + 1` then strings `[start..=end]`.
- `Any`: emits `end - start + 1` then `Any` values `[start..=end]`.
- `Binary`, `Embed`, `Format`, `Type`, `Doc`, `Move`: not splittable, always emit full content.

`Deleted` / `Move` / `Format` / `Type` / `Doc` / `Embed` / `Binary` are **non-countable singletons** for splice purposes (`block.rs::Item::len` returns 1 for them). Only `String` / `JSON` / `Any` carry multiple countable elements per block.

---

## 7. V1 Update decode (`update.rs:497-516`, `update.rs:364-393`)

### Top-level (`update.rs:497-516`) — verbatim:

```rust
impl Decode for Update {
    fn decode<D: Decoder>(decoder: &mut D) -> Result<Self, Error> {
        // read blocks
        let clients_len: u32 = decoder.read_var()?;
        let mut clients = HashMap::with_hasher(BuildHasherDefault::default());
        clients.try_reserve(clients_len as usize)?;

        let mut blocks = UpdateBlocks { clients };
        for _ in 0..clients_len {
            let blocks_len = decoder.read_var::<u32>()? as usize;

            let client = decoder.read_client()?;
            let mut clock: u32 = decoder.read_var()?;
            let blocks = blocks
                .clients
                .entry(client)
                .or_insert_with(|| VecDeque::new());
            // Attempt to pre-allocate memory for the blocks. If the capacity overflows and
            // allocation fails, return an error.
            blocks.try_reserve(blocks_len)?;

            for _ in 0..blocks_len {
                let id = ID::new(client, clock);
                if let Some(block) = Self::decode_block(id, decoder)? {
                    // due to bug in the past it was possible for empty bugs to be generated
                    // even though they had no effect on the document store
                    clock += block.len();
                    blocks.push_back(block);
                }
            }
        }
        // read delete set
        let delete_set = IdSet::decode(decoder)?;
        Ok(Update { blocks, delete_set })
    }
}
```

Line by line:
- `:500` — varuint client-count.
- `:506-509` — per-client header: varuint `block_count`, varuint `client_id`, varuint `clock_start`.
- `:511-518` — per-block: `decode_block(id, decoder)` returns `Option<BlockCarrier>` (None = legacy bug — empty Item.new returned None — silently skip). Track running `clock` and bump by `block.len()` after each emission.
- `:521` — read delete-set into `Update.delete_set`.

Note: decode trusts the wire — does NOT sort or dedupe. The cross-client descending-by-client-id order is preserved into the in-memory `HashMap`, which is iteration-non-deterministic; subsequent re-encode will use HashMap order again. Round-trip preserves bytes only if the receiver re-sorts on encode (which `encode_diff` does — `:407` in `update.rs` re-sorts descending).

### Per-block decode: `Update::decode_block` (`update.rs:364-393`) — verbatim:

```rust
fn decode_block<D: Decoder>(id: ID, decoder: &mut D) -> Result<Option<BlockCarrier>, Error> {
    let info = decoder.read_info()?;
    match info {
        BLOCK_SKIP_REF_NUMBER => {
            let len: u32 = decoder.read_var()?;
            Ok(Some(BlockCarrier::Skip(BlockRange { id, len })))
        }
        BLOCK_GC_REF_NUMBER => {
            let len: u32 = decoder.read_len()?;
            Ok(Some(BlockCarrier::GC(BlockRange { id, len })))
        }
        info => {
            let cant_copy_parent_info = info & (HAS_ORIGIN | HAS_RIGHT_ORIGIN) == 0;
            let origin = if info & HAS_ORIGIN != 0 {
                Some(decoder.read_left_id()?)
            } else {
                None
            };
            let right_origin = if info & HAS_RIGHT_ORIGIN != 0 {
                Some(decoder.read_right_id()?)
            } else {
                None
            };
            let parent = if cant_copy_parent_info {
                if decoder.read_parent_info()? {
                    TypePtr::Named(decoder.read_string()?.into())
                } else {
                    TypePtr::ID(decoder.read_left_id()?)
                }
            } else {
                TypePtr::Unknown
            };
            let parent_sub: Option<Arc<str>> =
                if cant_copy_parent_info && (info & HAS_PARENT_SUB != 0) {
                    Some(decoder.read_string()?.into())
                } else {
                    None
                };
            let content = ItemContent::decode(decoder, info)?;
            let item = Item::new(id, None, origin, None, right_origin, parent, parent_sub, content);
            match item {
                None => Ok(None),
                Some(item) => Ok(Some(BlockCarrier::from(item))),
            }
        }
    }
}
```

Critical: `info` is matched as a **whole byte** for Skip/GC dispatch (`:365`/`:368`). Item content type comes from `info & 0b1111` later in `ItemContent::decode`. Since neither GC=0 nor Skip=10 matches a valid item ref-num (1..9, 11) when the high bits are all clear, Skip/GC are unambiguously identified by exact byte match.

Note also `:366` `decoder.read_var::<u32>()` for Skip vs `:369` `decoder.read_len()` for GC — same V1, but flagged because they diverge in V2 (Skip uses raw varuint, GC uses the len-encoder column).

`Item::new(id, None /*left*/, origin, None /*right*/, right_origin, parent, parent_sub, content)` — note both `left` and `right` ItemPtrs are `None` at decode time. They're resolved at integrate-time via `Item::repair` (see §8).

### Pending update buffer

`Store` has two pending slots (`store.rs:43-51`):

```rust
pub(crate) pending: Option<PendingUpdate>,
pub(crate) pending_ds: Option<IdSet>,
```

`PendingUpdate` (`update.rs:631-634`):
```rust
pub struct PendingUpdate {
    pub update: Update,
    pub missing: StateVector,  // min clocks needed per client to retry
}
```

When does decode push to pending vs apply directly? Decode itself **never pushes to pending**. Pending is populated by `Update::integrate` (§8) when it discovers an item with an unsatisfied dependency (origin/right_origin/parent ID whose clock exceeds local SV). The integrate function returns `(Option<PendingUpdate>, Option<Update> /*unapplied DS*/)`, and `TransactionMut::apply_update` (`transaction.rs:807-859`) merges these into `store.pending` / `store.pending_ds`.

Retry trigger (`transaction.rs:809-832`): after each `apply_update` call, walk the existing pending's `missing` SV and check if **any** missing client's required clock is now satisfied by the just-updated `store.blocks`. If so, set `retry = true`, then at `:849-858` re-call `apply_update` recursively with the pending update merged in.

---

## 8. The integrate-flow at the update level (`update.rs:234-301`)

`Update::integrate` is the function that takes a parsed Update and tries to push every block into the store, repairing parent/origin/right_origin pointers (`Item::repair` at `block.rs:1368-1431` — covered in `integrate.md`) before each `Item::integrate` call. It returns `(Option<PendingUpdate>, Option<Update>)` for the unintegratable remainder + the unappliable delete-set ranges.

Verbatim (truncated to the integration loop body, `:234-301`):

```rust
pub(crate) fn integrate(
    mut self,
    txn: &mut TransactionMut,
) -> Result<(Option<PendingUpdate>, Option<Update>), UpdateError> {
    let remaining_blocks = if self.blocks.is_empty() {
        None
    } else {
        let mut store = txn.store_mut();
        let mut client_block_ref_ids: Vec<ClientID> =
            self.blocks.clients.keys().cloned().collect();
        client_block_ref_ids.sort();

        let mut current_client_id = client_block_ref_ids.pop().unwrap();
        let mut current_target = self.blocks.clients.get_mut(&current_client_id);
        let mut stack_head = if let Some(v) = current_target.as_mut() {
            v.pop_front()
        } else { None };

        let mut local_sv = store.blocks.get_state_vector();
        let mut missing_sv = StateVector::default();
        let mut remaining = UpdateBlocks::default();
        let mut stack = Vec::new();

        while let Some(mut block) = stack_head {
            if !block.is_skip() {
                let id = *block.id();
                if local_sv.contains(&id) {
                    let offset = local_sv.get(&id.client) as i32 - id.clock as i32;
                    if let Some(dep) = Self::missing(&block, &local_sv) {
                        stack.push(block);
                        // get the struct reader that has the missing struct
                        match self.blocks.clients.get_mut(&dep) {
                            Some(block_refs) if !block_refs.is_empty() => {
                                stack_head = block_refs.pop_front();
                                current_target = self.blocks.clients.get_mut(&current_client_id);
                                continue;
                            }
                            _ => {
                                // This update message causally depends on another update message that doesn't exist yet
                                missing_sv.set_min(dep, local_sv.get(&dep));
                                Self::return_stack(stack, &mut self.blocks, &mut remaining);
                                current_target = self.blocks.clients.get_mut(&current_client_id);
                                stack = Vec::new();
                            }
                        }
                    } else if offset == 0 || (offset as u32) < block.len() {
                        let offset = offset as u32;
                        let client = id.client;
                        local_sv.set_max(client, id.clock + block.len());
                        if let BlockCarrier::Item(item) = &mut block {
                            item.repair(store)?;
                        }
                        let should_delete = block.integrate(txn, offset);
                        let mut delete_ptr = if should_delete {
                            let ptr = block.as_item_ptr();
                            ptr
                        } else { None };
                        store = txn.store_mut();
                        match block {
                            BlockCarrier::Item(item) => {
                                if item.parent != TypePtr::Unknown {
                                    store.blocks.push_block(item)
                                } else {
                                    // parent is not defined. Integrate GC struct instead
                                    store.blocks.push_gc(BlockRange::new(item.id, item.len));
                                    delete_ptr = None;
                                }
                            }
                            BlockCarrier::GC(gc) => store.blocks.push_gc(gc),
                            BlockCarrier::Skip(_) => { /* do nothing */ }
                        }

                        if let Some(ptr) = delete_ptr {
                            txn.delete(ptr);
                        }
                        store = txn.store_mut();
                    }
                } else {
                    // update from the same client is missing
                    let id = block.id();
                    missing_sv.set_min(id.client, id.clock - 1);
                    stack.push(block);
                    Self::return_stack(stack, &mut self.blocks, &mut remaining);
                    current_target = self.blocks.clients.get_mut(&current_client_id);
                    stack = Vec::new();
                }
            }

            // iterate to next stackHead
            if !stack.is_empty() {
                stack_head = stack.pop();
            } else {
                match current_target.take() {
                    Some(v) if !v.is_empty() => {
                        stack_head = v.pop_front();
                        current_target = Some(v);
                    }
                    _ => {
                        if let Some((client_id, target)) =
                            Self::next_target(&mut client_block_ref_ids, &mut self.blocks)
                        {
                            stack_head = target.pop_front();
                            current_client_id = client_id;
                            current_target = Some(target);
                        } else {
                            break;
                        }
                    }
                };
            }
        }

        if remaining.is_empty() { None } else {
            Some(PendingUpdate {
                update: Update { blocks: remaining, delete_set: IdSet::new() },
                missing: missing_sv,
            })
        }
    };

    let remaining_ds = txn.apply_delete(&self.delete_set).map(|ds| {
        let mut update = Update::new();
        update.delete_set = ds;
        update
    });

    Ok((remaining_blocks, remaining_ds))
}
```

Key points:
- Sorts `client_block_ref_ids` ascending (`:240`) then pops the **highest** first — same descending-client wire-order invariant.
- Uses an explicit `stack` to chase missing dependencies across clients (`Self::missing` at `:303-338` returns the `ClientID` of the missing dep — origin / right_origin / parent / Move start/end / WeakLink quote_start/end).
- When it hits a dead wall (the missing client has no available blocks in this update), it dumps the stack + remaining current-client blocks into `remaining` via `return_stack` (`:353-363`) and continues with the next client.
- Calls `item.repair(store)` (`:260`) just before `block.integrate(txn, offset)` (`:261`) — this is the pre-integrate parent-resolution step we still need to implement (see §9, FIXME list).
- Items with `parent == TypePtr::Unknown` after repair get pushed as **GC ranges instead of items** (`:266-267`). This is the safety net for items whose parent we can't identify (e.g. parent was never sent).

The delete-set is applied via `txn.apply_delete(&self.delete_set)` (`:295-298`) — returns any unappliable ranges (clocks for which no block exists yet) as `Some(IdSet)`, which becomes `pending_ds`.

---

## 9. Concrete Go translation choices

| Decision | Choice |
|---|---|
| Encoder type | `internal/update.Encoder` = `struct { buf []byte }` with append-style methods. Mirrors `EncoderV1` (`encoder.rs:78-87`). All trait methods become value methods on `*Encoder`. The `Write` primitives (varuint, varstring, varbuf, raw u8/u32) stay in `internal/lib0` and the encoder methods call into them with `e.buf = lib0.AppendVarUint(e.buf, n)` etc. Do NOT alias to a function set — yrs keeps them on a struct so that V2 can swap state-bearing impls; we want the same option door open. |
| Decoder type | `internal/update.Decoder` = `struct { buf []byte; pos int }` mirroring `Cursor + DecoderV1`. Methods return `(value, error)` pairs in Go style. Position-tracking by index (no slicing) so error reporting can include a byte offset. |
| StateVector type | Already exists indirectly as `map[uint64]uint64` in `internal/store`. **Promote to its own file**: `internal/state/vector.go` defining `type StateVector map[uint64]uint64` plus `Encode(*update.Encoder)` and `DecodeStateVector(*update.Decoder) (StateVector, error)`. Avoid wrapping in a struct — Go's map identity gives us free `len`, iteration, indexing, comparison-via-reflect.DeepEqual, and zero allocations on decode pre-sized. Empty SV is `StateVector{}` (nil-safe; `len(nil)==0`). |
| IdSet type | `internal/state/idset.go`: `type IdSet map[uint64]*ClientRanges` where `ClientRanges` wraps a `[]Range` kept sorted-and-merged. Methods: `Insert(id ID, length uint32)`, `Encode(*update.Encoder)`, `DecodeIdSet(*update.Decoder) (IdSet, error)`, `Contains(ID) bool`, `IsEmpty() bool`, `MergeWith(IdSet)`, `IterClients() []uint64` (returns sorted-ascending for encode), `IterRanges(client uint64) []Range`. The merge-on-insert logic from `ids.rs:119-265` ports cleanly — implement the tail-fast path first since it's the hot path for sequential deletes. |
| DeleteSet vs IdSet | **Collapse to one type** (`IdSet`). yrs does the same: `IdSet` is the on-wire and in-memory type for both DS and undo/redo insert sets. The `DeleteSet` trait at `id_set.rs` exists only for the `from_store` ctor, which we'll add as a freestanding `IdSetFromStore(*store.BlockStore) IdSet` constructor. |
| Update struct | `internal/update/update.go`: `type Update struct { Blocks UpdateBlocks; DeleteSet state.IdSet }`. `UpdateBlocks` = `map[uint64]*[]BlockCarrier` (per-client deque-equivalent — Go's slice with `append` + occasional front-pop via `[1:]` slice is fine; deques aren't worth a custom container for the merge-updates use case at small N). `BlockCarrier` is a tagged union: `type BlockCarrier struct { Kind BlockKind; Item *block.Item; Range block.BlockRange }` with `Kind ∈ {KindItem, KindGC, KindSkip}`. |
| Pending update queue | Single `Pending *PendingUpdate` and `PendingDS state.IdSet` fields on `*store.Store` (or `Doc`). yrs's `Option<PendingUpdate>` is exactly one slot — when a new update arrives with leftover unintegratable blocks, those get **merged** into the existing pending via `Update::merge_updates` (`update.rs:415-491`), not queued. The retry-loop check in `transaction.rs:813-818` tests whether any `pending.missing` clock is now satisfied; if so, recursively re-apply. Same shape in Go. |
| Encoder version selection | API surface uses explicit functions: `EncodeStateAsUpdateV1(d *Doc, remoteSV state.StateVector) []byte` (and `EncodeStateVectorV1`, `ApplyUpdateV1`). When V2 lands, add `…V2` siblings. **Do NOT** add a `Version` enum parameter — yrs's `encode_v1` / `encode_v2` separation is cleaner than a runtime branch and makes it harder to accidentally cross versions. Keep the `Encoder` struct V1-only for now; if V2 ports happen, define an interface `Encoder` and have `EncoderV1` / `EncoderV2` concrete impls then. Premature interface = needless boxing today. |

Package layout proposal:

```
internal/
  lib0/              (already shipped)
  block/             (already shipped)
  store/             (already shipped)
  doc/               (already shipped)
  types/             (already shipped — Map)
  state/             NEW — StateVector, IdSet, range merging
  update/            NEW — Encoder, Decoder, Update, PendingUpdate, decode_block, encode_diff
```

`internal/update` may need to import `internal/state` and `internal/block`; `internal/state` should be a leaf (only depends on `internal/lib0` and `internal/block` for `ID`/`ClientID`).

---

## 10. Cross-language fixture plan

Generate fixtures with a Node script that imports `yjs` and writes byte arrays + textual descriptions to `testdata/wire/v1/*.bin` + `*.json`. Each fixture has a paired Go test asserting both `Encode(parsed_state) == bytes` AND `Decode(bytes) == parsed_state`.

Minimum coverage:

1. **Empty SV** — `Y.encodeStateVector(new Y.Doc())` → expect `[0x00]`. Verifies single-byte empty case.
2. **Empty Update** — encode a brand-new doc as update → expect `[0x00, 0x00]` (yrs `Update::EMPTY_V1`). Same byte literal as above on V1.
3. **Single client, single string set** — `doc.getMap().set('k','v')` on a doc with `clientID=1`, encode SV + Update. Verifies HAS_PARENT_SUB path, content-Any path, root-type-Named parent path. Smallest non-trivial update.
4. **Single client, multi-block run** — sequence of `text.insert()` calls producing 3+ blocks for one client. Verifies the run-block-count + first-block-clock-start fields and the offset/trim_start logic for runs starting mid-block.
5. **Multi-client with descending-client-id wire order** — clients `1` and `2`, both insert; encode and assert client `2`'s run appears in the wire bytes BEFORE client `1`'s run. Captures the descending-sort invariant.
6. **State-vector-bounded diff** — client A makes 5 ops, client B makes 3 ops, then encode `Y.encodeStateAsUpdate(docA, Y.encodeStateVector(docB))` — should emit only the 5 ops not seen by B. Tests `diff_state_vectors` and trim_start.
7. **DeleteSet — single range** — `text.delete(0, 3)` after insert; verify DS = `{client → [(start, 3)]}`.
8. **DeleteSet — merge on insert** — `text.delete(0,2); text.delete(2,2)` → wire emits a single range `(0,4)` because `IdRanges::insert` coalesces adjacent. Tests `ids.rs:119-265` merge logic.
9. **DeleteSet — multi-client** — two clients each delete, encode DS, assert clients sorted ASCENDING in the bytes (opposite of update-block order). Tests the BTreeMap iteration discipline.
10. **All ContentKind variants** (where externally producible from JS Yjs):
    - `ContentAny` (8): `map.set('k', 42)` (number) or `array.push([{a:1}])`.
    - `ContentString` (4): `text.insert(0,'abc')`.
    - `ContentBinary` (3): `map.set('k', new Uint8Array([1,2,3]))`.
    - `ContentDeleted` (1): not emitted by encode of a live store; only appears as result of GC during decode of a previously-seen item that's been content-stripped. Build via post-deletion + GC pass.
    - `ContentJSON` (2): legacy — JS Yjs only emits this for ancient document formats; skip unless we have a vintage fixture.
    - `ContentEmbed` (5): `text.insert(0, {bold:true})` insert as XML embed in a Text/XmlText.
    - `ContentFormat` (6): `text.format(0, 3, {bold:true})`.
    - `ContentType` (7): `map.set('k', new Y.Map())` (nested type).
    - `ContentDoc` (9): `map.set('k', new Y.Doc())` (sub-doc).
    - `ContentMove` (11): JS Yjs does not currently emit Move blocks — yrs-only feature. Skip unless we cross-test against another yrs node.
    - GC (block-level 0) and Skip (block-level 10) are produced via GC pass / pending-fragment cases.
11. **Pending update fragmentation** — apply an update that references an origin from a client whose blocks haven't arrived yet → assert decoder produces an Update, integrator returns it as PendingUpdate with non-empty `missing` SV. Re-encode the doc state and assert the pending fragment is included in the byte output (verifies `merge_pending_v1` path).
12. **Round-trip preservation** — decode then re-encode arbitrary updates from the fixtures above and assert the bytes match. Catches HashMap iteration drift (the StateVector and per-client UpdateBlocks decode to a non-deterministic map; re-encode must apply the canonical sort to recover wire-stable bytes).

Run the JS fixture generator pinned to Yjs `^13.6.x` (lock the version in package.json). yrs and JS Yjs share the V1 wire format definition by Yjs spec, but both have shipped subtle fixes — pin to avoid drift.

---

## 11. Open questions / FIXMEs

**Confirmed wire-format facts (no ambiguity):**
- StateVector: HashMap order, no header, no version byte. Empty = `[0x00]`.
- IdSet: BTreeMap order (ascending client_id), no header. Empty = `[0x00]`.
- Update: descending client_id order for block runs, no header, no magic. Empty = `[0x00, 0x00]`.
- Info-byte content nibble = low 4 bits; flag bits = `HAS_ORIGIN=0x80`, `HAS_RIGHT_ORIGIN=0x40`, `HAS_PARENT_SUB=0x20`. Bits `0x10`, `0x08`, `0x04` are unused but the decoder dispatches Skip/GC by **whole-byte equality** to `10`/`0` so they MUST stay zero on emission.
- Range-on-wire is `(start, length)` not `(start, end)`. `length = end - start` for half-open `[start..end)`.
- Origin override on partial-block emission: synthetic origin `(client, clock + start - 1)` when slice doesn't start at block boundary (`slice.rs:184-188`).

**FIXME / TODO comments captured:**
- `store.rs:206-209` — `//TODO: this could be actually 2 steps:` (refactor `encode_diff` into a Diff type + Encode impl). Cosmetic; no wire impact.
- `update.rs:30-33` — `/** @todo this should be refactored. I'm currently using this to add blocks to the Update */` on `UpdateBlocks::add_block`. Internal API hygiene.
- `update.rs:343` — `// TODO: remove the unsafe block` in `next_target`. Borrow-checker workaround; Go has no equivalent issue.
- `encoder.rs:281` — `//TODO: this is wrong (key_table is never updated), but this behavior matches Yjs` in `EncoderV2::write_key`. **Important wire-compat note for V2**, irrelevant for V1.
- `block.rs:1940-1948` — multiline comment about UTF-16 surrogate-pair splitting; commented-out logic that was in y.js but yrs left out. May matter for our String content port if we hit splittable strings with surrogates; flag but don't fix unless test fails.
- `transaction.rs:855-856` — pending retry recursively re-applies; yrs has no recursion-depth bound. Pathological pending chains could blow the stack — port with explicit iterative retry loop instead.

**NOT-CONFIRMED / things to verify with fixtures:**
- The `store.md` note about `BlockRange::last_id` being off-by-one is **not a wire-format issue** — `last_id = start + len - 1` is the correct inclusive last clock for a length-`len` range starting at `start`. The wire encodes `(start, len)`, the in-memory has `(start, len)` and `last_id` is just a derived helper. Confirmed safe.
- yrs's `encode` for `StateVector` iterates HashMap order — but JS Yjs also iterates Map insertion order, which is also non-deterministic across implementations. Two SVs with the same `(client, clock)` pairs in different orders are SEMANTICALLY equal but BYTE-different. **All round-trip tests must compare structurally, not byte-for-byte**, except where they re-encode after a known-canonical sort. (Same point applies to update block-runs — but those are sorted descending on emit, so byte-for-byte works for update blocks.)
- `write_parent_info` writes a varuint-encoded `0u32`/`1u32` (`encoder.rs:142-143`), not a single byte. For 0 and 1 this is 1 byte each — but a hostile encoder writing `varuint(0x80 | 1)` followed by `0x00` would encode `1` in 2 bytes and yrs's `read_parent_info` would still read it as `1`. **NOT-CONFIRMED whether JS Yjs encodes always as 1 byte; assume yes, but test.**
- Empty Update constant `Update::EMPTY_V2 = &[0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0]` (`update.rs:100`). The 13 bytes are 9 empty column-buffers + 1 feature-flag + 3 padding from the always-empty key_clock/etc encoders. V1 `EMPTY_V1 = &[0, 0]`. Confirmed.
- No magic header / version byte at the front of either V1 SV, V1 IdSet, or V1 Update. Receivers know the version out-of-band (e.g. `decode_v1` vs `decode_v2` API choice). Wire-compat: a V2 update bytes-as-V1 will misparse silently — **callers MUST pick the right decoder**.

**Surprises:**
- The asymmetry: Update block-runs sort DESCENDING by client, IdSet sorts ASCENDING. Both are wire-mandatory. Easy to get wrong.
- The `info & 0b1111` mask is applied INSIDE `ItemContent::decode` (`block.rs:1875`), not in `decode_block`. So an item info byte of e.g. `0x84` (HAS_ORIGIN|content=4) decodes content as `String` even though `0x84 != 4`. The dispatch on `info` (whole byte) for Skip/GC at `update.rs:365-369` is correct only because Skip=10 and GC=0 are reserved low-nibble values that no Item ever uses (Item ref nums are 1,2,3,4,5,6,7,8,9,11), AND because Items always have one of HAS_ORIGIN/HAS_RIGHT_ORIGIN/HAS_PARENT_SUB or content in the low nibble making the whole byte ≠ 0 and ≠ 10 in practice. **Edge case: an Item with no origin, no right_origin, no parent_sub, and content ref 10 would collide with Skip — but no such ref number exists.** Safe by construction.
- `Item::new` (called at `update.rs:385-389`) returns `Option<Item>` — there exist legacy bytes that produce no Item (zero-length content, etc.). The decoder silently skips these (`update.rs:512`). Replicate this skip in Go to remain compat-tolerant of legacy doc bytes.
