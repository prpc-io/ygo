# Tech debt

> Items intentionally deferred or acknowledged as incomplete during initial implementation. Each entry says what, why, when to address, and where in the source to find the relevant code or upstream reference. Update as items are resolved or new ones are accumulated.

## Block layer

### Item.Splice does not rewrite parent.Branch.Map for map-key tail items

- **Where:** `internal/block/item.go` `Item.Splice`.
- **What:** when the item being spliced is the most recent writer on a map-like parent (`Right == nil && ParentSub != nil`), yrs additionally rewrites `parent.Branch.Map[*ParentSub]` from the original item to the new right half (`block.rs:516-560`). We do not, because `Branch` is a stub until the types layer lands.
- **Impact today:** none — no code path queries `Branch.Map` yet.
- **Impact when types layer lands:** Map.Get on a map-key whose tail item was spliced would return the stale (left) half instead of the live (right) half. Map convergence relies on this map-slot pointer being current.
- **When to address:** when the types layer ships `Branch.Map`. Add the rewrite to `Item.Splice` (or move the responsibility to `Store.SplitBlock` if cleaner) and add a regression test.

### Item.Repair pass not implemented (partially mitigated)

- **Where:** missing helper in `internal/block`.
- **What:** yrs's `Item::repair(store)` (`block.rs:1368-1431`) runs before integrate during update apply. It resolves the new item's `Left` from `Origin` (via `MaterializeCleanEnd`), `Right` from `RightOrigin` (via `MaterializeCleanStart`), and fixes up an Unknown parent by inheriting from a resolved neighbour.
- **Mitigation today:** types-layer constructors inline the Origin → Left resolution they need (`Map.Set` reads `branch.Map[key]` directly to compute Origin). No standalone Repair helper exists.
- **When to address:** with the V1 update decoder layer, where incoming items arrive with raw Origin / RightOrigin IDs and need full pre-integrate resolution including parent-Unknown inheritance.

### Item.Integrate offset > 0 path stubbed

- **Where:** `internal/block/integrate.go` `Item.Integrate` first branch.
- **What:** when `Update.integrate` applies an item whose first `offset` clocks are already known locally (the suffix is the actual delta), yrs trims the item's left side via `get_item_clean_end` + `materialize` + `Content.splice` (`block.rs:569-583`). We return false without doing this.
- **Impact today:** none — no caller uses offset > 0 yet.
- **When to address:** with the V1 update decoder, which is the only producer of partial-offset integrates.

### Item.Integrate Named/ID parent resolution (resolved)

- **Was:** updates carrying ParentNamed or ParentID silently dropped.
- **Resolved by:** Repair handles all three parent kinds. ParentNamed via `ctx.GetOrCreateBranch`. ParentID via lookup of the parent Item, extraction of its embedded Branch from KindType Content (see `internal/block/repair.go`). Items whose ParentID points at an unseen clock queue in `encoding.Pending` and integrate on the next Apply that satisfies the dependency (proven by `TestNested_OutOfOrderApply_DrainsViaPending`).

### Item.Integrate Move/Subdoc/Weak/Format integrations not handled

- **Where:** `internal/block/integrate.go` `Item.Integrate` content-switch.
- **What:** yrs's integrate handles five content-specific paths (`block.rs:786-818`):
  - `KindMove`: re-integrates left+right moved ranges if they share a Move pointer.
  - `KindDoc`: registers child subdoc, emits `subdocs.added` / `loaded`.
  - `KindFormat`: `@todo searchmarker` no-op upstream — we mirror.
  - `KindType`: weak-link source materialization (only with `weak` feature).
  - Weak-link inheritance from left when overriding map-key.
  We handle only the `KindDeleted` case (set FlagDeleted) and skip the rest.
- **When to address:** Move post-MVP; Subdoc post-MVP; Weak post-MVP; Format with rich-text `Y.Text` formatting (Days 25-28 per ROADMAP).

### TransactionMut.changedTypes drops the parent_sub dimension

- **Where:** `internal/doc/transaction.go` `TransactionMut.AddChangedType` and `changedTypes` field.
- **What:** yrs records `(parent, parent_sub)` pairs so observers can fire per-map-key. Our map drops `parent_sub` and only keys by `*Branch`, so observer dispatch will conflate map-key changes that happen in the same transaction.
- **Impact today:** none — no observers exist.
- **When to address:** with the observer subsystem. Restructure to `map[*Branch]map[string]struct{}` (nil sub-key for positional changes) or a `[]changeRecord` slice.

### TransactionMut.deletedIDs is too narrow for the wire delete set

- **Where:** `internal/doc/transaction.go` `TransactionMut.deletedIDs` field.
- **What:** the wire delete set is RLE-encoded `(clientID, []ClockRange{Start, Len})` per client. Our `[]block.ID` records individual IDs, losing run information; squashing on emit is possible but suboptimal.
- **When to address:** with the IdSet layer. Replace with a real `IdSet` value and have `Delete(item)` insert `(item.ID, item.Len)` ranges directly.

## Types layer (Map and beyond)

### Map.Observe not implemented

- **Where:** missing method on `internal/types/map.go` `Map`.
- **What:** yrs's `Map::observe(callback)` registers a per-Map listener that fires on each transaction commit with a `MapEvent` describing changed keys (`map.rs` ~line 200). We have no observer subsystem yet.
- **Impact today:** none — no caller subscribes.
- **When to address:** with the broader observer subsystem (paired with `TransactionMut.Commit` lifecycle steps 3-6 — pre-emit observers, update event emit, after-commit observers). Pre-condition: rework `TransactionMut.changedTypes` to carry the `parent_sub` dimension (already tracked above).

### Map.Get value extraction handles only Any/String/Binary

- **Where:** `internal/types/map.go` `extractValue`.
- **What:** the user-visible value for `Map.Get(key)` is unpacked from `Content`. We handle `KindAny` (returning `Anys[0]`), `KindString` (returning `Str`), and `KindBinary` (returning `Bytes`). yrs additionally supports `KindEmbed`, `KindType` (nested shared types), `KindDoc` (subdocs), and the `Out`/`Value` enum dispatch.
- **When to address:** with Array, Text, and the typed `Any` replacement (covered separately under "Any type is a placeholder"). Map gets its full value coverage as a free side-effect.

### Map.Len is O(N) over branch.Map

- **Where:** `internal/types/map.go` `Map.Len`.
- **What:** `Len` iterates the entire `branch.Map` skipping tombstoned entries. yrs has a TODO at `map.rs:158` about caching live-count on the Branch; we'd inherit that optimization for free if/when Branch grows the cache.
- **When to address:** if benchmarks show Len in hot paths.

### Array position resolution is O(N) — no search-marker cache

- **Where:** `internal/types/array.go` `findInsertPosition`, `Get`, `Delete`.
- **What:** every operation that needs to translate a user-facing index into a *Item walks `branch.Start` linearly. Operations on long arrays (10K+ elements) become O(N) per op.
- **Why deferred:** per `docs/yrs-port-notes/types-array.md` finding 1, yrs main has no search-marker cache either. We're matching the executable spec; adding the cache is a pure-Go optimization unblocked by benchmarks, not a wire-format dependency.
- **When to address:** when a real workload shows position resolution in hot paths. Implementation: bounded-LRU `[]struct{ idx uint64; item *Item }` on Branch, updated heuristically when traversal cost exceeds a threshold (~80 entries per yrs INTERNALS.md).

### Array.Range value extraction handles only Any/String/Binary

- **Where:** `internal/types/array.go` `extractValueAt`.
- **What:** same shape as Map's `extractValue`. KindEmbed / KindType (nested types) / KindDoc (subdocs) / KindMove return nil on Get/Range.
- **When to address:** with the respective subsystems (nested-type construction, subdocs, Y.Array.move).

### Array.Move not implemented (Y.Array.move equivalent)

- **Where:** missing on `internal/types/array.go`.
- **What:** Y.Array supports `move(srcIdx, dstIdx)` to relocate an element. yrs implements via the ContentMove variant; we do not encode/decode/integrate Move at all.
- **When to address:** post-MVP per ROADMAP. Pre-conditions: ContentMove encode/decode in encoding layer, Move integration in Item.Integrate (deferred per integrate.md).

### Text rich-text formatting (resolved)

- **Was:** Text supported only plain-text Insert/Delete/String/Length. No format markers, no embeds, no Quill-style delta API.
- **Resolved by:** `internal/types/text_format.go` ships `Text.InsertWithAttributes(idx, str, attrs)`, `Text.Format(idx, len, attrs)`, `Text.InsertEmbed(idx, value)`, `Text.Range(fn)`, `Text.ToDelta()`, `Text.ApplyDelta(ops)`. KindFormat and KindEmbed wire format wired into `internal/encoding/content_codec.go` (note: both use `writeJSON = varstring(JSON.stringify(v))` per yjs UpdateEncoder.js, NOT lib0 Any). Tests cover open/close markers, format on existing range, attribute-clearing, embeds in delta, cross-client convergence, ApplyDelta dispatch over insert/retain/delete/embed/attrs, ToDelta→ApplyDelta round-trip.

### Text.Range / Text iteration not implemented

- **Where:** missing on `internal/types/text.go`.
- **What:** Map and Array expose `Range(fn)`; Text only exposes `String()` for full read. A streaming `Text.Range(fn func(chunk string, attrs *Attrs) bool)` would let observers / encoders iterate per-Item segments without materializing the full string.
- **When to address:** with rich-text formatting (rich-text Range yields chunks plus current attrs).

### Text Insert / Delete walk text content twice on mid-block hit

- **Where:** `internal/types/text.go` `findTextPosition`.
- **What:** when index lands inside a String item we call `Content.Len(KindString)` (which calls `utf16.Length` → walks the string) AND, immediately after the split point check, `Store.SplitBlock` → `Item.Splice` → `Content.splitString` → `utf16.SplitAt` (which calls `utf16.ByteOffset` → walks again). Two O(N) walks per split.
- **Why deferred:** premature; falls out of the broader storage-layout decision above.
- **When to address:** with the storage layout benchmark.

## Encoding layer (V1 wire format)

### StateVector / IdSet have no JS Yjs cross-language byte-equality fixtures

- **Where:** `internal/encoding/state_vector_test.go`, `idset_test.go` — only Go round-trip tests today.
- **What:** lib0 fixtures pass byte-equality with JS lib0 directly. SV and IdSet encode call lib0 primitives, so the per-byte arithmetic is correct, but we have not run a JS-encodes / Go-decodes round-trip against actual JS Yjs output.
- **Why this is a real gap, not a paranoia:** per `update-v1.md` gotcha 1, sort direction asymmetry is the easiest place to silently produce bytes JS Yjs rejects. Our determinism choice (sort ascending) matches yrs's BTreeMap iteration for IdSet but differs from JS Yjs's HashMap insertion order for StateVector. Decoding either way works (varuint-pair list is order-independent on read); encoding direction matters only for byte-equality, which is what fixtures would catch.
- **When to address:** with the Update encode/decode commit. Once Update bytes round-trip against JS Yjs, SV and IdSet are exercised end-to-end through real wire updates and the gap closes naturally.

### Update encode / decode partial — full client list, no slicing at SV boundary

- **Where:** `internal/encoding/update.go` `EncodeDiff`.
- **What:** the V1 Update wire format and Apply pipeline now work end-to-end in pure Go (encode → decode → apply produces converged Map state, verified by TestUpdate_TwoDocConvergence_*). What's still simplified vs yrs:
  - **No SV-boundary slicing on the first block of each client run.** yrs's `Store::write_blocks_from` calls `find_pivot(remoteClock)` then trims the first block via `ItemSlice::encode` with a partial start. We emit the entire client list. Wire is still valid; receivers integrate items they already have as no-ops (Contains check). Cost: redundant bytes proportional to remote-known prefix length.
  - **No partial-block origin override.** Per `update-v1.md` gotcha 4, sliced items must synthesize Origin = `(client, clock+start-1)`. Without slicing we don't trigger this; the gotcha returns when EncodeDiff gains slice-trim.
- **When to address:** when network-bandwidth-driven sync becomes a real cost (multi-MB docs over slow links). Pure-correctness test pipeline already passes.

### Item.Repair ParentID (resolved)

- **Was:** Repair returned `ErrParentIDUnresolved` for any ParentID reference.
- **Resolved by:** ParentID arm in `internal/block/repair.go` looks up the parent Item via `ctx.GetItem`, asserts the Content is KindType, and binds `it.Parent = {Kind: ParentBranch, Branch: parent.Content.Branch}`. Missing parents queue in `encoding.Pending` and retry on subsequent Drain passes. Tests in `internal/types/nested_test.go` cover Map-in-Map, Array-in-Map, Text-in-Map, Map-in-Array, deeper hierarchies, wire round-trip, cross-client convergence, and out-of-order delivery.

### Pending update buffer (resolved)

- **Was:** `Update.Apply` returned `ErrMissingDependency` whenever an item's Origin / RightOrigin / Parent-by-ID pointed at a clock the local store had not seen, aborting the whole apply.
- **Resolved by:** `internal/encoding/pending.go` adds `Pending` (per-doc queue of blocks + delete-set entries keyed by client, sorted by clock). `Update.Apply` now folds missing-dep items into Pending and runs `Pending.Drain` in a loop until fixed point. Pending state lives on `doc.Doc` via the opaque `pendingState any` slot (accessed through `TransactionMut.PendingState` / `SetPendingState` so the doc package does not import encoding). Top-level helper `encoding.ApplyUpdate(d, raw)` opens a write txn, decodes, applies, and commits. Inspection helpers: `encoding.HasPending`, `encoding.GetPending`, `encoding.MissingSV` (returns the SV a peer should fetch to drain the queue). 7 tests cover out-of-order delivery, stuck-pending MissingSV, DS-before-item retry, idempotency.
- **Remaining gap:** none for the MVP path. Optimization opportunities exist (batch the Drain inner loop with dependency-bucketed retry instead of per-client linear scan; emit a "this update added nothing" signal so callers can skip the retry pass when the pending buffer is already empty) but they are pure-perf and unblocked by benchmarks.

### Any TLV missing arrays / objects / buffers / bigint / float32

- **Where:** `internal/encoding/any_codec.go`.
- **What:** `EncodeAny` and `DecodeAny` cover null/undefined, bool, string, int (≤ 31 bits), int64-or-larger-as-float64, float64. Tags 116 (binary), 117 (array), 118 (object), 122 (bigint), 124 (float32) are unsupported — DecodeAny returns `ErrUnsupportedAnyTag`; EncodeAny panics for the corresponding Go types.
- **Impact today:** Map.Set with primitive values round-trips. Map.Set with `[]any` arrays, `map[string]any` objects, or `[]byte` (use ContentBinary for binary instead) does not.
- **When to address:** with the Array shared type (which forces full Any coverage end-to-end), or when a real adopter hits the limitation.

### DeleteSet apply does not split items at range boundaries

- **Where:** `internal/encoding/update.go` `(*Update).Apply` delete-set loop + `internal/encoding/pending.go` `(*Pending).Drain` delete-set loop.
- **What:** when a wire delete-set range covers a SUBSET of an existing item's clocks, the apply code calls `txn.Delete(it)` on the whole item rather than splitting at the range boundaries first. yrs handles this via `MaterializeCleanStart` / `MaterializeCleanEnd` before tombstoning. Without splits, a peer that received the un-split item and then a partial-delete range tombstones the entire item.
- **Impact:** cross-client convergence breaks for the specific pattern "client A creates contiguous text; client B observes A; B uses `ApplyDelta` with a `Delete N` op that targets a sub-range of A's item; B sends the resulting wire update to A". A's local view loses MORE than B intended. `TestApplyDelta_CrossClient_Converges` was softened to use insert+format only to dodge this.
- **When to address:** v0.2 hardening alongside the EncodeDiff slice-trim work — both touch the same `MaterializeClean*` paths. Implementation: before `txn.Delete(it)`, call `MaterializeCleanStart(ID{client, r.Start})` and `MaterializeCleanEnd(ID{client, r.End - 1})` to ensure the item boundaries align with the range, then delete only the materialized middle item(s).

### Update.Apply does not handle Skip blocks

- **Where:** `internal/encoding/update.go` `Update.Apply`.
- **What:** Skip wire records (BLOCK_SKIP_REF_NUMBER = 10) reserve clock space without semantics; yrs uses them in V2 mostly. We decode them but Apply silently drops them. Re-encoding from the resulting store will not emit the same Skip ranges.
- **When to address:** if V2 encoding lands or if a wire trace shows JS Yjs emitting Skip in V1 (rare).

### Content encoding for Embed / Format / Type / Doc / Move / JSON not implemented

- **Where:** `internal/encoding/content_codec.go`.
- **What:** EncodeContent panics on these kinds; DecodeContent returns "unsupported content kind". Map.Get's `extractValue` already handles the read side (returns nil for unknown kinds), so wire-decoded items of these kinds would integrate but be invisible to Map readers.
- **When to address:** Embed with JS Y.Map embedding objects; Format with Y.Text rich-text; Type with nested shared types (Array/Map/Text inside another); Doc with subdocs; Move with Y.Array.move(); JSON is legacy (yrs supports decode, encode is rare).

### Cross-language JS Yjs → Go direction (resolved)

- **Was:** missing `testdata/gen/gen-yjs-update.mjs` + Go fixture test.
- **Resolved by:** Phase B3 fixture wiring. `testdata/yjs-updates.json` captures 8 scenarios (empty doc, single set, multi-key set, all primitive value types, LWW chain, set+delete, set→delete→set, unicode keys/values). `internal/encoding/fixture_test.go::TestFixtures_DecodeApplyJSYjsUpdates` decodes each via DecodeUpdate, applies to a fresh Doc via Update.Apply, and verifies the resulting Map state matches the expected JSON. All 8 pass under `-race`. CI workflow's `fixtures` job regenerates and runs both lib0 and yjs-update tests on every push.
- **What this proves:** bytes that JS Yjs (yjs@13.6.20) produces via `Y.encodeStateAsUpdate(doc)` are byte-equivalent to what our DecodeUpdate accepts as input. The half of binary-protocol-compat that matters most for adoption — being able to receive updates from existing JS Yjs deployments — is verified end-to-end.

### Cross-language Go → JS Yjs direction not yet tested

- **Where:** missing `testdata/gen/verify-go-bytes.mjs` + Go test that exec's Node.
- **What:** the reverse direction (Go encodes → JS decodes via `Y.applyUpdate(doc, bytes)`) is not yet tested. JS Yjs's decoder is more permissive than yrs's (it accepts a wider range of valid encodings), so Go-encoded bytes that round-trip in pure Go may still fail to apply on the JS side if our wire format diverges in any subtle way.
- **Why deferred:** requires Go test runner to exec a Node subprocess, pipe bytes via stdin/stdout or temp files, and parse JSON results. Not hard, but enough infrastructure that splitting from direction-one keeps each commit reviewable.
- **When to address:** the next-likely real-world failure mode is a Hocuspocus-compat server scenario where JS clients download Go-encoded snapshots. Address before that lands.

### Surrogate-pair split (resolved)

- **Was:** Content.splitString sliced by byte offset; mid-surrogate splits would emit invalid UTF-8.
- **Resolved by:** new `internal/utf16` package with `Length`, `ByteOffset`, `SplitAt`. `Content.Len(KindString)` now returns UTF-16 code unit count via `utf16.Length` (matches yrs `Item::new` and JS Yjs Item.Len semantics, gotcha 1 from types-text.md). `Content.splitString` calls `utf16.SplitAt` which performs the JS Yjs U+FFFD replacement on mid-surrogate splits (yrs has the same code commented out at `block.rs:1940-1948`; we follow JS Yjs not yrs per types-text.md gotcha 3). Test `TestText_InsertSurrogateSplit_UsesU_FFFD` proves the behaviour.

### Wire info bit 4 reserved, no decoder check

- **Where:** `internal/block/flags.go` (constants only) and the future update decoder.
- **What:** bit 4 (`0b0001_0000`) of the Item info byte is currently unused — content kinds max at 11 (`0b1011`), all presence flags live in bits 5-7. If a future Yjs version starts using bit 4, our decoder must NOT silently mask it.
- **Why deferred:** the V1 decoder doesn't exist yet.
- **When to address:** when implementing the V1 decoder.
- **Action item at that time:** assert `info & 0b0001_0000 == 0` and return a versioning error if it's set; carry the failure into a fixture-based regression test.

### ContentString storage layout: O(N) UTF-16 walk per slice

- **Where:** `internal/block/content.go` `Content.Str string`, `internal/utf16/utf16.go` `ByteOffset` / `SplitAt`.
- **What:** strings stored as Go `string` (UTF-8). UTF-16 offset → byte offset translation walks chars on every call. Same O(N) cost yrs accepts (`SplittableString::block_offset` walks `chars()` per call). Two faster layouts when benchmarks demand: `[]uint16` storage (zero-cost slicing, memory cost) or precomputed `[]uint16Index→byteIndex` index for blocks past N units.
- **Why deferred:** matches yrs's choice; cost is invisible until B3 (text-heavy) benchmarks land.
- **When to address:** after `dmonad/crdt-benchmarks` B3 port shows text editing in hot path. Pair with the "Text Insert / Delete walks twice" entry below.

### Any type is a placeholder

- **Where:** `internal/block/stubs.go` `type Any = any`.
- **What:** yrs's `Any` is a tagged union (Bool, Number, BigInt, String, Buffer, Array, Map, Null, Undefined). Wire encoding requires deterministic serialization of each variant; an opaque `any` doesn't give us that.
- **Why deferred:** the encoder layer is what enforces wire compat. The current block layer only constructs/inspects Any values; it doesn't serialize them.
- **When to address:** when implementing the V1 update encoder.
- **Reference:** `yrs/src/any.rs`. Port into a proper Go tagged union and replace the alias.

### Forward-dependency stubs are empty types

- **Where:** `internal/block/stubs.go` (`Branch`, `Move`).
- **What:** placeholder `type X struct{}` definitions so the block package compiles. (`Doc` was previously here; now lives in `internal/doc`. The block-layer `Doc` stub is now only referenced by `block.Content.Doc` / `block.Content.ParentDoc` for `KindDoc` payloads, which is read-only data the block layer never inspects.)
- **Why deferred:** by design — the block layer references these only through pointers and never inspects their internals. Real definitions land with their owning layers.
- **When to address:** `Branch` with the types layer; `Move` post-MVP per ROADMAP. The `block.Doc` stub stays even after `internal/doc.Doc` lands, because the block layer can't depend on the doc layer (would cycle); we'll bridge them through an interface when sub-document support arrives.

## Public API surface

### gomobile bind subset (partially resolved)

- **Was:** the marquee "pure-Go = gomobile bind works" claim was structurally correct (no CGO) but operationally untested; an adopter running `gomobile bind github.com/Deln0r/ygo` would find most of the rich API silently filtered out (`any` / `map` / callbacks / non-byte slices).
- **Partially resolved by:** new `gomobile/` package exporting only the bindable subset — `Doc` and `Awareness` wrappers with bytes-in/bytes-out methods (NewDoc / NewDocWithClientID / ApplyUpdate / EncodeStateAsUpdate / EncodeStateVector / EncodeDiff / HasPending / MissingSV / NewAwareness / SetLocalState / LocalState / RemoveLocalState / EncodeAll / Apply). 6 tests prove the bytes-only round-trip. Package builds clean with `CGO_ENABLED=0`. Shared-type mutation (Map.Set / Array.Push / Text.Insert) is NOT exposed here — adopters either bytes-only or extend this package with typed setters.
- **Remaining gaps:**
  1. **Actual `gomobile bind` verification** — CI does not run `gomobile bind -target=ios` or `-target=android` because the toolchain requires Xcode / Android NDK. The Go-side surface is structurally compatible; the actual generated Java / Swift bindings have not been inspected.
  2. **Typed mobile shared-type API** — if a real mobile adopter wants Map.SetString / Array.PushStringSlice etc., the gomobile package can be extended with monomorphic typed variants (~200-400 LOC depending on how many type combinations matter).
- **When to address:** (1) when a mobile adopter brings their toolchain. (2) when a mobile adopter brings a concrete use case.

## Sync protocol / WebSocket server

### Hocuspocus extensions (Auth, Stateless, Close, SyncStatus) not implemented

- **Where:** `internal/sync/handler.go` `HandleFrame` default branch silently drops these message types.
- **What:** the full Hocuspocus envelope adds 5 message types beyond the bare y-websocket subset (Sync + Awareness + QueryAwareness): MessageAuth (2), MessageStateless (5), MessageBroadcastStateless (6), MessageClose (7), MessageSyncStatus (8). We decode them — DecodeEnvelope returns a Frame with the raw payload — but the dispatcher ignores them. Servers using ygo will not echo Stateless messages back, will not perform auth handshakes, and will not emit SyncStatus acks.
- **Impact today:** clients that REQUIRE these (Hocuspocus's hosted offering with auth tokens, custom extensions using Stateless for app-level RPC) will not work. Pure y-websocket clients and the Sync+Awareness subset of Hocuspocus clients work fine.
- **When to address:** v0.2. Auth first (Options.OnAuthenticate callback + MessageClose with permission-denied reason code 4401, per port-note "Auth flow"). Stateless second (Options.OnStateless callback). SyncStatus third (low-priority — ack signal that most clients ignore).

### Cross-language y-websocket / Hocuspocus fixture (resolved)

- **Was:** byte-level wire format asserted only via hand-built fixtures in `framing_test.go` and pure-Go round-trip in `handler_test.go`.
- **Resolved by:** `testdata/gen/gen-sync.mjs` captures 6 envelope scenarios (SyncStep1 from empty + non-empty doc, SyncStep2 with array state, SyncUpdate incremental text insert, Awareness frame, QueryAwareness handshake) using y-protocols/sync + Hocuspocus outer-tag layout. `internal/sync/fixtures_test.go` decodes each via DecodeEnvelope and verifies Type / SyncSub / Payload match. Reverse-encode test for QueryAwareness (only deterministic-payload type) confirms byte-equality.

### Broadcast fan-out is O(N) per update with no rate limiting

- **Where:** `server/server.go` `(*conn).broadcast`.
- **What:** every applied SyncUpdate triggers a synchronous send to every connection on the doc. For docs with hundreds of active connections, a single rapid edit producer can saturate the server's write loop. There is no batching, deduplication, or backpressure.
- **Why deferred:** no real workload yet. The pure-Go reference implementations of y-websocket and Hocuspocus run the same naive fan-out at adopter scale.
- **When to address:** if benchmarks show fan-out latency dominating. Mitigations in priority order: (a) per-connection bounded write queue with drop-oldest policy, (b) batch updates within a small time window (5-10ms), (c) per-doc dedicated fan-out goroutine to amortize lock acquisition.

### Persistence: every SyncUpdate is stored as a separate row

- **Where:** `server/server.go` `(*conn).maybePersist` calls `Store.StoreUpdate` per envelope.
- **What:** an active doc with 1000 small edits accumulates 1000 rows in the underlying store before any compaction runs. Flush runs only on the last-disconnect path (see `releaseConn`). For an always-connected doc, the log grows unbounded between server restarts.
- **When to address:** with an in-server auto-flush heuristic. Simple shape: per-doc counter, when N >= 200 schedule a Flush. Pre-condition: lift `Flush` to be safe under concurrent `StoreUpdate` (it already wraps everything in a SQLite transaction, but rest of the API surface should be audited).

### V2 update encoding not supported

- **Where:** `internal/encoding/update.go` (V1-only). Reflected at protocol layer in `internal/sync/handler.go` — SyncStep2 / SyncUpdate payloads are passed straight to `encoding.ApplyUpdate` which only knows V1.
- **What:** Hocuspocus extensions may call `Y.encodeStateAsUpdateV2`. We have no V2 codec; a V2 frame would fail to decode and trip the SyncStep2/Update handler's apply-error path, closing the connection with an InternalError code.
- **When to address:** v0.3 workstream — full V2 codec implementation. Until then, document that the server speaks V1 only and clients must avoid V2 mode (the default in current JS Yjs is V1).

## Awareness layer

### Cross-language JS y-protocols fixture (resolved)

- **Was:** awareness had only hand-built byte fixtures plus pure-Go round-trip tests; no proof against y-protocols' `encodeAwarenessUpdate` output.
- **Resolved by:** `testdata/gen/gen-awareness.mjs` captures 6 scenarios (name+color, cursor position, two clients, empty state, unicode, set-then-removed); `internal/awareness/fixtures_test.go` decodes each via Apply, verifies state map matches JS via structural JSON equality, plus reverse-direction encode test for single-client scenarios. Wired into CI's fixtures job; runs on every push. Also flagged a JS-impl quirk: `encodeAwarenessUpdate` iterates the caller-supplied client list which is normally `getStates().keys()` (excludes removed), so capturing removal entries requires explicitly passing `meta.keys()` instead.

### Apply returns updated for self-eviction defense bump

- **Where:** `internal/awareness/awareness.go` `Apply` self-eviction branch.
- **What:** when a remote tries to evict the local client and the local clock is bumped instead, we record the local clientID in `Summary.Updated`. yrs records a similar entry in its `AwarenessUpdateSummary::updated` (`awareness.rs:418`); JS records it as part of the regular update flow. The Updated reporting is semantically slightly incorrect — nothing about the local state actually changed except the clock — but matches the reference behaviour.
- **Impact today:** observers see a no-op Update event with the local clientID. Cosmetic only.
- **When to address:** if observers do something behavioural with Updated entries (e.g. re-render UI for the local cursor). Until then, consistency with reference impl wins.

### No background timeout sweep

- **Where:** `internal/awareness/awareness.go` — `SweepOutdated` is exposed but no goroutine drives it.
- **What:** the y-protocols JS implementation runs an internal `setInterval` every 3 seconds. yrs leaves it to the embedder. We follow yrs.
- **Impact today:** none — `SweepOutdated` is documented; callers wrap it in a `time.Ticker` when they need it.
- **When to address:** never, unless ergonomics complaints arrive. Background goroutines inside passive data structures violate the "no surprise lifecycle" Go convention.

## XML types (mostly resolved)

### Resolved: XmlFragment / XmlElement / XmlText shipped

- **Was:** missing XML wrapper layer; ProseMirror / Tiptap / BlockNote unusable as JS clients against a ygo server.
- **Resolved by:** `internal/types/xml.go` ships `XmlFragment`, `XmlElement`, `XmlText` wrappers. XmlElement carries nodeName + attributes (Map-like via `branch.Map`) + positional children (Array-like via `branch.Start`); XmlText embeds the regular Text wrapper to inherit all rich-text methods. ToString renderers produce HTML-like output with sorted attribute keys. 11 tests cover Element/Attribute/RemoveAttribute, nested DOM round-trip, self-closing render for empty elements, rich-text inside XmlText, wire round-trip preserves structure, cross-client structural-edit convergence, Range over children. The wire-format machinery (`ContentType` with optional nodeName) was already in place from the nested-type commit.
- **Remaining:** `XmlHook` (legacy JS Yjs embed type carrying an arbitrary opaque value) is deferred — yrs marks it as legacy too. No adopter has asked.

## Persistence layer

### GetStateVector replays the full update log every call

- **Where:** `internal/persist/persist.go` `GetStateVector`.
- **What:** the helper builds a fresh `Doc`, replays every stored update through `Update.Apply`, then encodes the resulting state vector. Cost is O(total update bytes) per call. y-leveldb sidesteps this by caching the state vector in a `(docName, "sv")` meta row updated alongside each `StoreUpdate`.
- **Impact today:** correctness is unaffected; large documents (many MB of updates) pay a measurable latency on sync-protocol SV exchanges.
- **When to address:** when sync server lands and SV requests become hot. Either (a) maintain a meta column updated transactionally with every `StoreUpdate`, or (b) auto-flush at N updates so the replay walks a single snapshot.

### No auto-flush trigger

- **Where:** `internal/persist/sqlite/sqlite.go` `(*Store).Flush`.
- **What:** compaction is opt-in. Callers that never call `Flush` accumulate one row per `StoreUpdate` indefinitely. The log stays correct (replay produces converged state) but storage grows linearly with edit count.
- **When to address:** with the sync-server layer, which is the natural place to insert a per-doc heuristic (e.g. "flush after every 100 stored updates"). Until then, library callers either schedule their own periodic flush or accept the growth.

### modernc.org/sqlite dependency pin is fragile

- **Where:** `go.mod`.
- **What:** to keep `go 1.22` minimum compat we pin `modernc.org/sqlite v1.29.10` plus its transitive `modernc.org/libc@v1.49.3`, `modernc.org/memory@v1.8.0`, `modernc.org/gc/v3@v3.0.0-20240801135723-a856999a2e4a`, `golang.org/x/sys@v0.30.0`. Newer versions of any of these bump the required Go to 1.23+ or 1.25+.
- **Impact today:** none — tests pass, builds reproducibly.
- **When to address:** when we deliberately bump the Go minimum (next time the CI matrix retires `1.22`). At that point unpin everything and let `go mod tidy` pick latest.

## Doc / Transaction layer

### TransactionMut.Commit lifecycle is a stub

- **Where:** `internal/doc/transaction.go` `(*TransactionMut).Commit`.
- **What:** yrs runs an 11-step commit lifecycle (squash mergeBlocks, GC eligible deleted, fire pre-emit observers, emit V1 update event, emit subdoc events, fire after-commit observers, etc. — see `docs/yrs-port-notes/transaction.md` § "Commit lifecycle"). We currently only release the write lock and mark the txn closed.
- **Why deferred:** every step depends on a layer that does not yet exist (squash needs `Item.TrySquash` + `ClientBlockList.SquashLeft`; GC needs `gc.go`; observers need an observer subsystem; update emit needs the V1 encoder; subdocs need subdoc support).
- **Impact today:** `mergeBlocks`, `deletedIDs`, `changedTypes` accumulate across the transaction but are dropped at Commit. Memory leak per transaction; no observer notifications; updates not emitted. None of this matters until callers exist.
- **When to address:** unblock each step as the underlying layer lands. Squash first (right after Item.Integrate, since squash is what consumes mergeBlocks). Update emission second (with V1 encoder). Observers third. GC and subdocs last.

### Transaction lifetime not enforced

- **Where:** `internal/doc/transaction.go` `Transaction`, `TransactionMut`.
- **What:** yrs uses a `'doc` lifetime parameter on `Transaction<'doc>` so the borrow checker rejects code that retains a transaction past the doc's lifetime or past the explicit drop. Go has no equivalent. A caller can capture the `*Transaction` returned by `Doc.ReadTxn()` in an outer variable and use it after `Close()`, dereferencing a doc whose lock is no longer held.
- **Why deferred:** runtime checks (e.g. `t.closed` panic on every method) add overhead and noise. The contract is documented in the type doc; mature OSS Go projects (`database/sql.Tx`, `bbolt.Tx`) take the same documentation-only approach.
- **When to address:** if a real bug surfaces that's traceable to retained-after-close. Add a `valid bool` plus panic-on-stale-access; the cost is one branch per method.

### Origin observer dispatch not wired

- **Where:** `internal/doc/transaction.go` `TransactionMut.Origin`.
- **What:** yrs's transactions carry an `Origin` opaque value that observers see in events, used to e.g. tell local edits apart from remote-applied updates (`transaction.rs:1210-1288`). We expose the field but do not yet have an observer subsystem to thread it through.
- **When to address:** with the observer subsystem (paired with the V1 update emit step in the commit lifecycle).

### Subdoc tracking not implemented

- **Where:** `internal/doc/transaction.go` (no `subdocs` field).
- **What:** yrs accumulates `subdocs.added / loaded / removed` sets on TransactionMut and emits `SubdocEvent` at commit. We have no subdoc support at all yet.
- **When to address:** post-MVP; subdocs are an advanced feature, not required for v0.1 scope per DESIGN.md.

### Doc options surface is minimal

- **Where:** `internal/doc/doc.go` `Options`.
- **What:** yrs's `Options` carries auto-load, should-load, GUID, encoder version, collection-id, and other fields. We carry only `DisableGC` and a deterministic `ClientID` override.
- **Why deferred:** none of the other yrs options are reachable from any code path we have ported. Adding them now would be premature surface.
- **When to address:** when porting Doc.load (subdocs) for `AutoLoad`, when porting V2 encoding for `EncoderVersion`, etc. — option-by-option, only when something in the codebase consumes the value.

### ID and Item width: uint64 vs yrs u32

- **Where:** `internal/block/id.go` `ID.Clock uint64`; `internal/block/item.go` `Item.Len uint64`.
- **What:** yrs uses `u32` for clock and len; we use `uint64`. Costs 4 bytes per ID and per Item, accepts wire values up to 2^64-1.
- **Why intentional:** lib0 varuints can carry values up to 64 bits. Yrs would error on a clock above 2^32; we accept it. This is a defensive widening, not a bug.
- **When to revisit:** if memory profiling shows ID storage dominating allocations on large docs. Unlikely.

## Process / tooling

### Local pre-push lint (resolved on this commit)

- **Was:** CI lint kept rejecting pushes because local pre-push only ran `go vet` + tests, not gofmt and not golangci-lint. Two repeat incidents (`5d68d3c` gofmt; `085269d`/`58360cf`/`b1b117f` unused symbols). Local brew installs golangci-lint v2 while CI uses v1.64.8 — configs incompatible.
- **Resolved by:**
  1. CI workflow now pins `version: v1.64.8` (was `latest`). Reproducible.
  2. Root `Makefile` exposes `make check` (gofmt + vet + test + lint) and `make lint-install` (`go install ...@v1.64.8`) for the matching local linter.
- **Remaining:** add a `pre-push` hook installer once collaborators arrive. Tracked separately if needed.

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
