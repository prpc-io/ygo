# Tech debt

> Items intentionally deferred or acknowledged as incomplete during initial implementation. Each entry says what, why, when to address, and where in the source to find the relevant code or upstream reference. Update as items are resolved or new ones are accumulated.

## Block layer

### Item.Splice does not rewrite parent.Branch.Map for map-key tail items

- **Where:** `internal/block/item.go` `Item.Splice`.
- **What:** when the item being spliced is the most recent writer on a map-like parent (`Right == nil && ParentSub != nil`), yrs additionally rewrites `parent.Branch.Map[*ParentSub]` from the original item to the new right half (`block.rs:516-560`). We do not, because `Branch` is a stub until the types layer lands.
- **Impact today:** none — no code path queries `Branch.Map` yet.
- **Impact when types layer lands:** Map.Get on a map-key whose tail item was spliced would return the stale (left) half instead of the live (right) half. Map convergence relies on this map-slot pointer being current.
- **When to address:** when the types layer ships `Branch.Map`. Add the rewrite to `Item.Splice` (or move the responsibility to `Store.SplitBlock` if cleaner) and add a regression test.

### Item.Repair pass not implemented

- **Where:** missing helper in `internal/block`.
- **What:** yrs's `Item::repair(store)` (`block.rs:1368-1431`) runs before integrate during update apply. It resolves the new item's `Left` from `Origin` (via `MaterializeCleanEnd`), `Right` from `RightOrigin` (via `MaterializeCleanStart`), and fixes up an Unknown parent by inheriting from a resolved neighbour. Tests currently must pre-resolve these manually.
- **Why deferred:** Repair is needed by the update-apply path which doesn't exist yet (no V1 decoder). Until then, only test scenarios with locally-generated inserts run, and those construct items with already-resolved Left/Right.
- **When to address:** with the V1 update decoder layer.

### Item.Integrate offset > 0 path stubbed

- **Where:** `internal/block/integrate.go` `Item.Integrate` first branch.
- **What:** when `Update.integrate` applies an item whose first `offset` clocks are already known locally (the suffix is the actual delta), yrs trims the item's left side via `get_item_clean_end` + `materialize` + `Content.splice` (`block.rs:569-583`). We return false without doing this.
- **Impact today:** none — no caller uses offset > 0 yet.
- **When to address:** with the V1 update decoder, which is the only producer of partial-offset integrates.

### Item.Integrate Named/ID parent resolution returns drop

- **Where:** `internal/block/integrate.go` `Item.Integrate` second branch.
- **What:** yrs resolves `TypePtr::Named(name)` via `store.get_or_create_type` (lazily creates a root branch) and `TypePtr::ID(id)` by looking up the parent Item and reading `Type` content. We return true (drop) for any parent kind other than already-resolved `ParentBranch`.
- **Impact today:** updates carrying not-yet-resolved parent references silently get tombstoned. Real impact arrives once we accept updates from JS Yjs over the wire (which encodes parents as Named or ID, not as live Branch pointers).
- **When to address:** with the types layer, which owns the root-type registry. Pair with the V1 decoder so wire updates resolve correctly on application.

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

- **Where:** `internal/block/stubs.go` (`Branch`, `Move`).
- **What:** placeholder `type X struct{}` definitions so the block package compiles. (`Doc` was previously here; now lives in `internal/doc`. The block-layer `Doc` stub is now only referenced by `block.Content.Doc` / `block.Content.ParentDoc` for `KindDoc` payloads, which is read-only data the block layer never inspects.)
- **Why deferred:** by design — the block layer references these only through pointers and never inspects their internals. Real definitions land with their owning layers.
- **When to address:** `Branch` with the types layer; `Move` post-MVP per ROADMAP. The `block.Doc` stub stays even after `internal/doc.Doc` lands, because the block layer can't depend on the doc layer (would cycle); we'll bridge them through an interface when sub-document support arrives.

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

### gofmt not enforced before commit

- **Where:** developer workflow.
- **What:** CI's `golangci-lint` job rejected commit `5d68d3c` because two files (`internal/block/content.go` const block, `internal/store/client_blocks_test.go` slice literal alignment) had inconsistent spacing that gofmt normalizes. The mistake escaped because we ran `go vet` and `go test` locally but not `gofmt -l`.
- **Why deferred:** quick correction (`gofmt -w .`) was cheaper than blocking on a tooling change. The underlying gap remains.
- **When to address:** before the second time this happens. Concrete options, ordered by leverage:
  1. Add a `Makefile` target `make check` that runs `gofmt -l . && go vet ./... && go test ./... -race && golangci-lint run`. Mention in CONTRIBUTING.md as the pre-push contract.
  2. Add a `.git/hooks/pre-commit` template under `tools/git-hooks/` and a one-line install instruction in CONTRIBUTING.md (developer opt-in; not enforced).
  3. Add a `pre-push` hook installer that runs `make check` automatically.
  Recommendation: start with option 1 (zero-magic, discoverable). Option 3 is the long-term answer once we have collaborators.

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
