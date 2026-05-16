# yrs port notes: Awareness

> Source: yrs `main`, `yrs/src/sync/awareness.rs` (the `Awareness` struct, `AwarenessUpdate`, `AwarenessUpdateEntry`, `ClientState`, `Event`, `AwarenessUpdateSummary`). JS reference: `y-protocols` `master`, `src/awareness.js` (the `Awareness` class, `encodeAwarenessUpdate`, `applyAwarenessUpdate`, `modifyAwarenessUpdate`, `removeAwarenessStates`) and `src/sync.js` (for the surrounding sync-message envelope). Cross-reference: `y-protocols/PROTOCOL.md` §4 "Awareness protocol".

Awareness is the ephemeral, presence-style sibling of the document CRDT — it tracks per-client transient state (cursor position, user name/colour, selection range, focus) that is **not** persisted in the y-doc and **not** part of the YATA convergence machinery. Conceptually it is a last-write-wins map keyed by `clientID`, with a per-client monotone `clock` for conflict resolution and a wall-clock `lastUpdated` timestamp for timeout sweeps. Its wire format is a separate message family from V1 document updates (see `sync.js:38-40` for message-type IDs `0,1,2 = SyncStep1/SyncStep2/Update`; awareness sits one layer up as composite-message type `1` per `PROTOCOL.md:107-119`). The CRDT property is *eventual* only — there is no causal-history requirement, no delete-set, no state vector. A peer joining late simply learns whatever the active peers happen to broadcast next.

---

## 1. Public types

### Rust: `Awareness` struct (`awareness.rs:35-41`) — verbatim:

```rust
pub struct Awareness {
    doc: Doc,
    states: DashMap<ClientID, ClientState>,
    clock: Arc<dyn Clock>,
    on_update: Observer<AwarenessUpdateFn>,
    on_change: Observer<AwarenessUpdateFn>,
}
```

`ClientState` (`awareness.rs:585-590`) is the per-client entry:

```rust
pub struct ClientState {
    pub clock: u32,
    pub last_updated: Timestamp,
    pub data: Option<Arc<str>>,   // JSON string; None = client marked offline/removed
}
```

`AwarenessUpdate` / `AwarenessUpdateEntry` are the wire-encodable payload types (`awareness.rs:533-573`):

```rust
pub struct AwarenessUpdate {
    pub clients: HashMap<ClientID, AwarenessUpdateEntry>,
}

pub struct AwarenessUpdateEntry {
    pub clock: u32,
    pub json: Arc<str>,   // pre-serialized JSON; "null" sentinel for removal
}
```

Event types: `Event` (`awareness.rs:603-606`) wraps an `AwarenessUpdateSummary { added, updated, removed: Vec<ClientID> }` (`awareness.rs:510-518`). Two observer channels, `on_update` (fires on every applied entry) and `on_change` (fires only when the deep JSON content actually differs from the previous value).

### JS: `Awareness` class (`awareness.js:44-162`)

Mirror surface. Per-client state lives across **two** maps, not one:
- `states: Map<clientID, Object>` — the JSON-deserialized state object (or absent if removed).
- `meta:   Map<clientID, { clock: number, lastUpdated: number }>` — the per-client metadata.

yrs collapses these into a single `DashMap<ClientID, ClientState>` where `ClientState.data` is `Option<Arc<str>>` (the JSON-as-string, deliberately not parsed). Both representations encode the same logical tuple `(clock, lastUpdated, state|null)`; the split-vs-merged difference is a Go-translation question (see §6).

Notable JS-only constant: `export const outdatedTimeout = 30000` (`awareness.js:13`) — the 30-second sweep timer. yrs does not encode this as a constant in `awareness.rs`; the Rust version delegates timeout responsibility to the embedder (no `setInterval` runs inside `Awareness`).

---

## 2. State shape and wire encoding of state — the critical question

**The state field on the wire is a UTF-8 JSON string, NOT a lib0 Any TLV.** This is the most consequential interop fact in the whole layer.

JS encoder (`awareness.js:199-212`):
```js
encoding.writeVarUint(encoder, clientID)
encoding.writeVarUint(encoder, clock)
encoding.writeVarString(encoder, JSON.stringify(state))
```

JS decoder (`awareness.js:253-257`):
```js
const clientID = decoding.readVarUint(decoder)
let clock = decoding.readVarUint(decoder)
const state = JSON.parse(decoding.readVarString(decoder))
```

Rust mirrors this exactly (`awareness.rs:539-548` Encode, `:550-562` Decode):
```rust
fn encode<E: Encoder>(&self, encoder: &mut E) {
    encoder.write_var(self.clients.len());
    for (&client_id, e) in self.clients.iter() {
        encoder.write_var(client_id.get());
        encoder.write_var(e.clock);
        encoder.write_string(&e.json);
    }
}
```

Note the Rust `ClientState.data: Option<Arc<str>>` and `AwarenessUpdateEntry.json: Arc<str>` are deliberately **un-parsed** strings — yrs treats the JSON payload as an opaque blob, only round-tripping it through `serde_json` at the API boundary (`awareness.rs:183-186` `local_state`, `:217-221` `state`, `:239-243` `set_local_state`). The `NULL_STR: &str = "null"` sentinel (`awareness.rs:15`) is the literal four-byte string `"null"` written via `write_string` — i.e. on the wire it is `varuint(4) • 0x6E 0x75 0x6C 0x6C`. That is JSON-`null`, NOT a lib0 Any-null type-tag (which would be a single byte `126` per the lib0 Any spec).

**Recommendation for Go: follow JS y-protocols exactly.** Store `data` as `[]byte` (the raw JSON bytes) or `string`, and provide a typed `SetLocalState(v any) error` helper that calls `json.Marshal` and a `GetState[T any](clientID uint64) (T, error)` helper that calls `json.Unmarshal`. Do not invoke lib0 Any encoding for the state payload. Any JS-yrs interop fixture that did otherwise would not round-trip through a real JS Yjs server (Hocuspocus, y-websocket).

---

## 3. Wire format — byte-level layout

```
AwarenessUpdate :=
    varuint(client_count)
  • for each entry e:
        varuint(e.clientID)
      • varuint(e.clock)
      • varstring(e.json)               // JSON.stringify(state) — "null" string if removed
```

Citations: JS write loop `awareness.js:200-211`; JS read loop `awareness.js:253-257`; Rust encode `awareness.rs:540-547`; Rust decode `awareness.rs:551-562`; spec table `PROTOCOL.md:97-101`.

Iteration order: **not specified, not canonicalized**. JS iterates the `clients` array in caller-provided order (`awareness.js:200-204`). Rust iterates `HashMap::iter()` (`awareness.rs:542`) — non-deterministic. Two encodings of the same logical update will produce byte-different but semantically equivalent wire bytes. All Awareness round-trip tests MUST compare structurally, not byte-for-byte (same discipline as `StateVector` per `update-v1.md` §4).

No header, no version byte, no length prefix outside the leading `varuint(client_count)`. Empty update = single byte `0x00`.

When the payload is embedded inside the composite-protocol envelope (`PROTOCOL.md:107-119`), the framing is:
```
varuint(1)                              // AwarenessProtocol message-type tag
• varbuffer(AwarenessUpdate)            // varuint(len) • bytes
```

The double-length prefix (one for `varbuffer`, one inside `AwarenessUpdate` for `client_count`) is intentional — it lets a router forward an opaque awareness blob without decoding it.

---

## 4. Lifecycle — clock bumps, removal, timeouts

### Local update path (JS `setLocalState` `awareness.js:106-140`, Rust `set_local_state_raw` `awareness.rs:248-265`)

`clock` bumps on **every** local update, including no-op `setLocalState(currentState)` calls. JS: `clock = currMeta === undefined ? 0 : currMeta.clock + 1` (`awareness.js:109`). Rust: `state.clock += 1` (`awareness.rs:256`). The deep-equality check (`f.equalityDeep(prevState, state)` `awareness.js:132`; `prev != curr_state` `awareness.rs:281`) is used **only** to decide whether to fire the `change` event — it does NOT suppress the clock bump or the `update` event. This is deliberate: re-broadcasting the same state with a higher clock is how a client refreshes its presence against the timeout sweep (see below).

Initial clock for a never-seen client is `0` on JS and `1` on Rust (`awareness.rs:260` `ClientState::new(1, now, …)` for vacant entries). The discrepancy does not affect wire-compat because the receiver only ever compares `currClock < clock`; the absolute clock value is meaningless across peers.

### Remote apply path (JS `applyAwarenessUpdate` `awareness.js:246-300`, Rust `apply_update_internal` `awareness.rs:388-479`)

Apply iff `currClock < clock` (`awareness.js:261`; `awareness.rs:412`). The `currClock == clock && state == null` case (`awareness.js:261`; `awareness.rs:411`) is a special override: a tied clock with a removal wins over a tied clock with content. This is the disconnect-broadcast protocol — a peer announcing "client X is offline at clock N" can demote an active entry at clock N.

**Self-defense against remote eviction** (`awareness.js:264-267`; `awareness.rs:416-419`): if a remote update claims to remove the *local* client's state and the local state is still non-null, the receiver bumps its own clock by 1 instead of removing — and on the next broadcast cycle this surfaces as a "I'm still here" message at the higher clock that supersedes the stale removal. This prevents network races where a stale disconnect notice arrives after the client has already reconnected.

### Timeout sweep (JS only, `awareness.js:64-82`)

`setInterval` running every `outdatedTimeout / 10 = 3 seconds`:
1. If the local entry's `lastUpdated` is older than `outdatedTimeout / 2 = 15 seconds`, re-broadcast the local state (heartbeat — clock bumps).
2. Walk remote entries; any with `lastUpdated > 30 seconds` ago get removed via `removeAwarenessStates(this, remove, 'timeout')` (`awareness.js:172-192`), which deletes from `states` and emits `change`/`update` events.

yrs does NOT run a sweep timer (`awareness.rs` has no `setInterval` equivalent). The embedder must drive timeouts themselves — typically by polling `Awareness::meta(client_id) -> Option<(u32, Timestamp)>` (`awareness.rs:224-227`) and calling `Awareness::remove_state(client_id)` (`awareness.rs:196-214`) on entries older than threshold. The 30-second figure is a `y-protocols` convention, not a hard requirement.

### Removal semantics on the wire

Removal = a wire entry with `state == null` (encoded as the literal JSON string `"null"`). The receiver's `states` map drops the entry but the `meta`/`ClientState` retains the bumped `clock` — this is necessary so a subsequent stale revival at a lower clock is rejected by the `currClock < clock` check. In Rust the `data: None` slot remains (`awareness.rs:421` `state.data = None`) holding the clock; in JS the `states.delete` plus `meta.set(…)` split achieves the same (`awareness.js:269-274`).

---

## 5. Event model

Both implementations expose two parallel channels:

| Channel | JS | Rust | Fires when |
|---|---|---|---|
| `update` | `awareness.js:139,190,295-299` | `awareness.rs:292,466` `on_update` | every applied entry, even no-op deep-equal re-broadcasts |
| `change` | `awareness.js:137,189,290-294` | `awareness.rs:288,463` `on_change` | only when the JSON state content actually differs from previous |

Payload shape: `{ added: ClientID[], updated: ClientID[], removed: ClientID[] }` plus an `origin` tag. The `origin` parameter mirrors `TransactionMut::Origin` from the document layer — used to distinguish `'local'` (`awareness.js:137,139`) from remote-applied (caller-supplied at `applyAwarenessUpdate(awareness, update, origin)` `awareness.js:246`). yrs `Origin` (`awareness.rs:13` import) is the same opaque-byte-tag type as transactions use; passed through `apply_update_with` / `apply_update_summary_with` (`:351-386`).

The `change`-vs-`update` distinction matters in practice: UI redraws should subscribe to `change` (avoid redraws on heartbeats), debug logging should subscribe to `update` (see all traffic).

---

## 6. Go translation choices

Recommend `internal/awareness/awareness.go` with the following surface:

```go
type ClientState struct {
    Clock       uint32
    LastUpdated time.Time
    Data        []byte    // raw JSON bytes; nil = removed/offline
}

type Awareness struct {
    clientID uint64
    mu       sync.RWMutex
    states   map[uint64]ClientState
    subs     subscribers   // see below
    clock    func() time.Time
}

func New(clientID uint64) *Awareness
func NewWithClock(clientID uint64, clock func() time.Time) *Awareness

func (a *Awareness) ClientID() uint64
func (a *Awareness) LocalState() ([]byte, bool)
func (a *Awareness) SetLocalState(jsonState []byte)
func (a *Awareness) SetLocalStateJSON(v any) error
func (a *Awareness) RemoveLocalState()
func (a *Awareness) States() map[uint64][]byte          // snapshot
func (a *Awareness) Meta(clientID uint64) (clock uint32, lastUpdated time.Time, ok bool)
func (a *Awareness) RemoveState(clientID uint64)
func (a *Awareness) SweepOutdated(threshold time.Duration) (removed []uint64)

func (a *Awareness) Encode(clientIDs []uint64) []byte    // empty slice = all known
func (a *Awareness) Apply(update []byte, origin any) (Summary, error)

type Summary struct { Added, Updated, Removed []uint64 }
```

**Independence from `Doc`.** In JS, `new Awareness(doc)` couples them tightly (`awareness.js:48-87`: takes `doc.clientID`, registers a `doc.on('destroy')` handler). yrs already loosened this — `Awareness::new(doc)` still owns a `Doc` (`awareness.rs:36`) but the only thing it reads is `doc.client_id()`. For Go, **decouple completely**: `New(clientID uint64)` takes only the ID. Lets users run Awareness without spinning up a doc (e.g. presence-only servers, fixtures, tests), matches the cleaner half of the yrs design, and avoids importing `internal/doc` into `internal/awareness`.

**Events: callbacks, not channels.** JS uses `ObservableV2` (callback list); yrs uses `Observer<F>` (also callback list, despite the doc-comments calling it "channel receiver"). Recommend Go callback-registration:

```go
type Sub func(summary Summary, origin any)
func (a *Awareness) OnUpdate(f Sub) (unsub func())
func (a *Awareness) OnChange(f Sub) (unsub func())
```

Reason: a channel would force the caller to spawn a goroutine drainer, and dropping into a select on every state-set would dominate the cost of the operation. Callback-list matches both reference implementations and matches what `internal/types` already does for document events. Hand back an `unsub` closure for symmetry with yrs's `Subscription`.

**State storage.** Single `map[uint64]ClientState` with `Data []byte` (nil means removed but clock retained), matching yrs's collapsed shape. Do NOT split into two maps à la JS — the split was an artefact of `Map.delete` not preserving metadata; in Go a struct-valued map handles both fields cleanly.

**JSON, not lib0 Any.** Use `encoding/json` directly. The state on the wire is raw JSON bytes — keep it raw inside `ClientState.Data` and only `Unmarshal` at the API boundary if the caller asks. This mirrors yrs's `Arc<str>` discipline (`awareness.rs:189` `local_state_raw`) and avoids a Marshal/Unmarshal round trip on every Apply.

**Wire-format functions** live in `internal/awareness/wire.go`: `encodeUpdate(entries []entry) []byte`, `decodeUpdate(b []byte) ([]entry, error)`. Reuse `internal/lib0`'s `AppendVarUint` / `AppendVarString` / `ReadVarUint` / `ReadVarString` — same primitives the document layer already uses.

**Timeout sweep is the caller's job.** Provide `SweepOutdated(threshold time.Duration) []uint64` that returns the IDs it pruned and fires `change`/`update` events for them, but do not run a background goroutine inside `Awareness`. The caller decides whether to wrap it in a `time.Ticker`. Matches yrs's choice and avoids surprising goroutine lifecycle inside a passive data structure.

---

## 7. Gotchas — implementer must not miss

1. **State on the wire is a JSON string, NOT lib0 Any.** `varstring(JSON.stringify(state))` per `awareness.js:209`. A lib0-Any encoding of the state would be silently rejected by every JS Yjs client and Hocuspocus server. The removal sentinel is the literal four-byte UTF-8 string `"null"` (`awareness.rs:15` `NULL_STR`), not the lib0 Any null tag-byte.
2. **Clock bumps on every local set, including no-op `setLocalState(currentState)`.** `awareness.js:109` `clock + 1` runs unconditionally; the deep-equality check at `:132` only affects the `change` event. A naive Go implementation that suppresses the bump on equal-state will break the 15-second heartbeat-vs-timeout dance and clients will be spuriously evicted.
3. **Removal keeps the entry locally with `data: None` / nil so the clock survives.** Drop the data, retain the clock metadata. Otherwise a stale revival message at clock `N-1` would resurrect a removed client (`awareness.js:269-274`; `awareness.rs:421` keeps `ClientState`, only sets `data = None`).
4. **Self-eviction defense.** If a remote update tries to remove the **local** client's state while the local state is still non-null, do NOT remove — bump the local clock by 1 instead (`awareness.js:264-267`; `awareness.rs:416-419`). Fixes the reconnect-race where a stale "I disconnected" arrives after the client is back.
5. **Awareness is independent of Doc.** Despite the JS constructor taking a `Y.Doc`, the only thing read is `doc.clientID`. Decouple in Go — `New(clientID uint64)` is the cleaner shape and matches the trajectory of the yrs design. The only callback the JS version registers on the doc is `destroy` (`awareness.js:83-85`), which is trivially replaced by an explicit `Close()` method in Go.
6. **No state vector, no causal history.** A late-joining peer just receives whatever full snapshot the active peers send next (`AwarenessUpdate` carries the union of `clients` the sender chooses, `awareness.js:199-204` — caller picks the set). Do not attempt to reconcile against a missing-updates set; there is no such concept.
7. **Iteration order is non-deterministic on encode.** `HashMap`/`Map`/`map[…]…` iteration order is unspecified in all three languages. Round-trip byte-equality tests will fail; compare structurally (per-client `(clock, json)` set equality).
