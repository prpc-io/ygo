// Package awareness implements the y-protocols Awareness layer —
// the ephemeral, presence-style sibling of the document CRDT used
// to track per-client transient state like cursor positions, user
// names, and selection ranges.
//
// Awareness is NOT part of YATA. It is a last-write-wins map keyed
// by clientID, with a per-client monotonic clock and a wall-clock
// LastUpdated timestamp used for timeout sweeps. Wire-format
// compatibility is against y-protocols (the package every JS Yjs
// client speaks); yrs is mirrored where its design diverges from
// JS in ways that affect Go translation choices. See
// docs/yrs-port-notes/awareness.md for the full reference.
//
// State payloads on the wire are raw JSON strings, NOT lib0 Any
// TLV. Awareness stores them as opaque []byte and only invokes
// encoding/json at the typed-helper boundary
// (SetLocalStateJSON / DecodeStateJSON). This matches yrs's
// Arc<str> discipline and avoids unnecessary marshal/unmarshal
// round-trips inside Apply.
//
// Awareness is independent of doc.Doc. The local clientID is
// passed at construction time; no doc.Doc reference is required.
// Embedders that already have a Doc typically use d.ClientID() to
// keep the two layers in sync.
package awareness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// NullStateJSON is the literal four-byte JSON string "null" that
// y-protocols and yrs emit when a client is marked offline. The
// removal sentinel is JSON-null at the wire level — NOT the lib0
// Any null tag byte. Per docs/yrs-port-notes/awareness.md gotcha 1.
const NullStateJSON = "null"

// DefaultTimeout is the y-protocols convention for stale-entry
// eviction (30 seconds, per y-protocols/awareness.js:13 outdatedTimeout).
// It is a convention, not a hard requirement; callers pick their
// own threshold when calling SweepOutdated.
const DefaultTimeout = 30 * time.Second

// ClientState is the per-client awareness entry. Stored in the
// Awareness state map; exposed only via accessor methods so the
// internal mutex protects mutation.
//
// Data is nil for removed clients; the entry is retained so the
// Clock survives — without it a stale "I'm back" message at a
// lower clock could resurrect a removed client.
type ClientState struct {
	Clock       uint32
	LastUpdated time.Time
	Data        []byte
}

// Summary is the event payload describing which clients were
// affected by a single Apply call or local mutation. Field
// semantics match y-protocols/awareness.js:
//
//   - Added: clientIDs that were not previously known.
//   - Updated: clientIDs whose entry was applied (clock bumped),
//     regardless of whether content changed.
//   - Removed: clientIDs marked offline by this batch.
//
// OnChange subscribers receive a Summary where Updated is filtered
// to the subset whose Data actually differs from the previous
// value — heartbeats (re-broadcast of unchanged state) are filtered
// out. OnUpdate subscribers receive the full Summary.
type Summary struct {
	Added, Updated, Removed []uint64
}

// IsEmpty reports whether the summary touches zero clients. Used
// to skip no-op callback dispatches.
func (s Summary) IsEmpty() bool {
	return len(s.Added) == 0 && len(s.Updated) == 0 && len(s.Removed) == 0
}

// Sub is the subscriber callback shape registered via OnUpdate or
// OnChange. origin is the caller-supplied tag forwarded through
// Apply (mirrors doc.TransactionMut.Origin); use it to distinguish
// local vs remote-applied changes.
//
// Subscribers MUST NOT call Awareness mutator methods from inside
// the callback — the surrounding lock has already been released,
// but a re-entrant Set/Remove could trigger a chain of nested
// events that complicates reasoning. If a callback needs to mutate
// state, dispatch the work to another goroutine.
type Sub func(summary Summary, origin any)

// Awareness tracks per-client ephemeral state. Safe for concurrent
// use; an internal RWMutex serializes mutation and a separate
// mutex protects the subscriber list.
type Awareness struct {
	clientID uint64
	now      func() time.Time

	mu     sync.RWMutex
	states map[uint64]*ClientState

	subMu       sync.Mutex
	nextSubID   atomic.Uint64
	onUpdateSub map[uint64]Sub
	onChangeSub map[uint64]Sub
}

// New returns an Awareness instance for the given local clientID
// using time.Now as the timestamp source.
func New(clientID uint64) *Awareness {
	return NewWithClock(clientID, time.Now)
}

// NewWithClock returns an Awareness instance with a caller-supplied
// time source. Used by tests that need deterministic LastUpdated
// values; pass time.Now in production.
func NewWithClock(clientID uint64, now func() time.Time) *Awareness {
	return &Awareness{
		clientID:    clientID,
		now:         now,
		states:      map[uint64]*ClientState{},
		onUpdateSub: map[uint64]Sub{},
		onChangeSub: map[uint64]Sub{},
	}
}

// ClientID returns the local client's identifier passed to New.
func (a *Awareness) ClientID() uint64 { return a.clientID }

// LocalState returns a copy of the local client's current state
// JSON bytes. The second return reports whether a local state is
// set; false means the client either never set a state or was
// removed via RemoveLocalState.
func (a *Awareness) LocalState() ([]byte, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s, ok := a.states[a.clientID]
	if !ok || s.Data == nil {
		return nil, false
	}
	out := make([]byte, len(s.Data))
	copy(out, s.Data)
	return out, true
}

// SetLocalState sets the local client's state to the given raw
// JSON bytes. The clock bumps unconditionally — even no-op equal
// sets — per the 15-second heartbeat invariant
// (y-protocols/awareness.js:106-140). The deep-equality check
// affects only whether OnChange fires; OnUpdate always fires.
//
// A nil jsonState argument is equivalent to RemoveLocalState. To
// set a JSON-null state explicitly, pass []byte("null") — but note
// that this triggers Removal semantics on the wire and is
// indistinguishable from removal for receiving peers.
func (a *Awareness) SetLocalState(jsonState []byte) {
	if jsonState == nil {
		a.RemoveLocalState()
		return
	}

	a.mu.Lock()
	prev, existed := a.states[a.clientID]
	var prevData []byte
	if existed {
		prevData = prev.Data
	}
	now := a.now()
	newClock := uint32(0)
	if existed {
		newClock = prev.Clock + 1
	}
	stateCopy := make([]byte, len(jsonState))
	copy(stateCopy, jsonState)
	a.states[a.clientID] = &ClientState{
		Clock:       newClock,
		LastUpdated: now,
		Data:        stateCopy,
	}
	a.mu.Unlock()

	summary := Summary{}
	if !existed || prevData == nil {
		summary.Added = []uint64{a.clientID}
	} else {
		summary.Updated = []uint64{a.clientID}
	}

	a.fireUpdate(summary, originLocal)

	contentChanged := !existed || prevData == nil || !bytes.Equal(prevData, jsonState)
	if contentChanged {
		a.fireChange(summary, originLocal)
	}
}

// SetLocalStateJSON marshals v with encoding/json and forwards to
// SetLocalState. Returns the marshal error if any; the awareness
// state is unchanged on error. Pass nil to remove the local state.
func (a *Awareness) SetLocalStateJSON(v any) error {
	if v == nil {
		a.RemoveLocalState()
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("awareness: marshal local state: %w", err)
	}
	a.SetLocalState(raw)
	return nil
}

// RemoveLocalState marks the local client offline. The wire-format
// representation is an entry with JSON "null"; the local entry is
// kept (with Data = nil) so the clock survives subsequent revival
// races.
//
// No-op when the local client never set a state.
func (a *Awareness) RemoveLocalState() {
	a.mu.Lock()
	prev, existed := a.states[a.clientID]
	if !existed || prev.Data == nil {
		a.mu.Unlock()
		return
	}
	prev.Clock++
	prev.LastUpdated = a.now()
	prev.Data = nil
	a.mu.Unlock()

	summary := Summary{Removed: []uint64{a.clientID}}
	a.fireUpdate(summary, originLocal)
	a.fireChange(summary, originLocal)
}

// States returns a snapshot of all currently-online client states
// as a deep-copy map. Removed/offline entries (Data nil but Clock
// retained) are excluded.
//
// Modifying the returned map or its values does not affect the
// Awareness state.
func (a *Awareness) States() map[uint64][]byte {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make(map[uint64][]byte, len(a.states))
	for id, s := range a.states {
		if s.Data == nil {
			continue
		}
		c := make([]byte, len(s.Data))
		copy(c, s.Data)
		out[id] = c
	}
	return out
}

// Meta returns the per-client metadata. The third return is false
// when clientID has never been seen.
func (a *Awareness) Meta(clientID uint64) (clock uint32, lastUpdated time.Time, ok bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	s, present := a.states[clientID]
	if !present {
		return 0, time.Time{}, false
	}
	return s.Clock, s.LastUpdated, true
}

// RemoveState marks an arbitrary clientID offline. Used by
// embedders to evict peers after a disconnect notification or
// manual administrative action. Local client cannot be removed via
// this method — use RemoveLocalState. Removing a never-seen
// clientID is a no-op.
func (a *Awareness) RemoveState(clientID uint64) {
	if clientID == a.clientID {
		a.RemoveLocalState()
		return
	}
	a.mu.Lock()
	prev, existed := a.states[clientID]
	if !existed || prev.Data == nil {
		a.mu.Unlock()
		return
	}
	prev.Clock++
	prev.LastUpdated = a.now()
	prev.Data = nil
	a.mu.Unlock()

	summary := Summary{Removed: []uint64{clientID}}
	a.fireUpdate(summary, originLocal)
	a.fireChange(summary, originLocal)
}

// SweepOutdated removes every non-local client whose LastUpdated
// is older than (now - threshold). Returns the sorted-ascending
// IDs of removed clients. The local client is never swept — its
// heartbeat is the caller's responsibility.
//
// Fires OnUpdate and OnChange with origin = "timeout".
func (a *Awareness) SweepOutdated(threshold time.Duration) []uint64 {
	cutoff := a.now().Add(-threshold)

	a.mu.Lock()
	var removed []uint64
	for id, s := range a.states {
		if id == a.clientID {
			continue
		}
		if s.Data == nil {
			continue
		}
		if s.LastUpdated.Before(cutoff) {
			s.Clock++
			s.LastUpdated = a.now()
			s.Data = nil
			removed = append(removed, id)
		}
	}
	a.mu.Unlock()

	if len(removed) == 0 {
		return nil
	}
	sortAscending(removed)
	summary := Summary{Removed: removed}
	a.fireUpdate(summary, originTimeout)
	a.fireChange(summary, originTimeout)
	return removed
}

// Encode produces V1 awareness wire bytes for the given clientIDs.
// Pass nil or an empty slice to encode every known client (online
// or removed-but-clock-retained). Unknown IDs are silently skipped.
//
// Iteration order is determined by the caller — pass a sorted
// slice for deterministic output. The wire format itself does not
// canonicalize order; round-trip tests should compare structurally.
func (a *Awareness) Encode(clientIDs []uint64) []byte {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var ids []uint64
	if len(clientIDs) == 0 {
		ids = make([]uint64, 0, len(a.states))
		for id := range a.states {
			ids = append(ids, id)
		}
	} else {
		ids = clientIDs
	}

	entries := make([]wireEntry, 0, len(ids))
	for _, id := range ids {
		s, ok := a.states[id]
		if !ok {
			continue
		}
		var payload []byte
		if s.Data == nil {
			payload = []byte(NullStateJSON)
		} else {
			payload = s.Data
		}
		entries = append(entries, wireEntry{
			ClientID: id,
			Clock:    s.Clock,
			JSON:     payload,
		})
	}
	return encodeUpdate(entries)
}

// ErrInvalidUpdate wraps decode failures of an incoming wire blob.
// The Awareness state is unchanged when Apply returns this error.
var ErrInvalidUpdate = errors.New("awareness: invalid update bytes")

// Apply integrates a wire-encoded update into the local state.
// origin is forwarded to OnUpdate / OnChange subscribers — typical
// values are "remote", "timeout", a peer ID, or nil.
//
// Returns the Summary describing every applied entry. On decode
// error the state is unchanged and the returned Summary is empty.
//
// Apply implements the y-protocols LWW algorithm exactly per
// docs/yrs-port-notes/awareness.md §4:
//
//   - Accept iff currClock < incomingClock, OR
//     currClock == incomingClock AND incoming is null AND current is non-null
//     (tied-clock removal wins).
//   - Self-eviction defense: if a remote tries to remove the local
//     client while the local state is still non-null, bump the
//     local clock instead of removing.
//   - Removal preserves the entry locally with Data = nil so the
//     clock survives stale revivals.
func (a *Awareness) Apply(update []byte, origin any) (Summary, error) {
	entries, _, err := decodeUpdate(update)
	if err != nil {
		return Summary{}, fmt.Errorf("%w: %w", ErrInvalidUpdate, err)
	}

	a.mu.Lock()
	var added, updated, removed, changed []uint64
	for _, e := range entries {
		incomingIsNull := bytes.Equal(e.JSON, []byte(NullStateJSON))

		prev, existed := a.states[e.ClientID]
		var prevData []byte
		var prevClock uint32
		if existed {
			prevData = prev.Data
			prevClock = prev.Clock
		}
		prevIsNull := !existed || prevData == nil

		accept := false
		if !existed {
			accept = true
		} else if prevClock < e.Clock {
			accept = true
		} else if prevClock == e.Clock && incomingIsNull && !prevIsNull {
			accept = true
		}
		if !accept {
			continue
		}

		// Self-eviction defense: a remote removing our local entry
		// while we are still online bumps our clock instead, so
		// the next heartbeat supersedes the stale removal.
		if e.ClientID == a.clientID && incomingIsNull && !prevIsNull {
			prev.Clock = e.Clock + 1
			prev.LastUpdated = a.now()
			updated = append(updated, e.ClientID)
			// no change event — our content didn't change
			continue
		}

		now := a.now()
		if !existed {
			a.states[e.ClientID] = &ClientState{
				Clock:       e.Clock,
				LastUpdated: now,
				Data:        nil,
			}
			prev = a.states[e.ClientID]
		} else {
			prev.Clock = e.Clock
			prev.LastUpdated = now
		}

		if incomingIsNull {
			if !prevIsNull {
				prev.Data = nil
				removed = append(removed, e.ClientID)
				changed = append(changed, e.ClientID)
			}
			// nil → nil: nothing to record (already filtered by accept check)
			continue
		}

		dataCopy := make([]byte, len(e.JSON))
		copy(dataCopy, e.JSON)
		prev.Data = dataCopy

		if prevIsNull {
			added = append(added, e.ClientID)
			changed = append(changed, e.ClientID)
		} else {
			updated = append(updated, e.ClientID)
			if !bytes.Equal(prevData, e.JSON) {
				changed = append(changed, e.ClientID)
			}
		}
	}
	a.mu.Unlock()

	summary := Summary{
		Added:   added,
		Updated: updated,
		Removed: removed,
	}
	a.fireUpdate(summary, origin)

	if len(added) > 0 || len(removed) > 0 || len(changed) > 0 {
		changeSummary := Summary{
			Added:   added,
			Updated: changed,
			Removed: removed,
		}
		a.fireChange(changeSummary, origin)
	}

	return summary, nil
}

// OnUpdate registers a callback fired for every applied entry,
// including no-op heartbeat re-broadcasts. Returns an unsubscribe
// closure that removes the callback.
//
// Use OnUpdate for traffic accounting or debug logging. For UI
// redraws prefer OnChange — heartbeats are noise.
func (a *Awareness) OnUpdate(f Sub) (unsub func()) {
	id := a.nextSubID.Add(1)
	a.subMu.Lock()
	a.onUpdateSub[id] = f
	a.subMu.Unlock()
	return func() {
		a.subMu.Lock()
		delete(a.onUpdateSub, id)
		a.subMu.Unlock()
	}
}

// OnChange registers a callback fired only when the JSON state
// content actually differs from the previous value, or on
// add/remove. Returns an unsubscribe closure.
func (a *Awareness) OnChange(f Sub) (unsub func()) {
	id := a.nextSubID.Add(1)
	a.subMu.Lock()
	a.onChangeSub[id] = f
	a.subMu.Unlock()
	return func() {
		a.subMu.Lock()
		delete(a.onChangeSub, id)
		a.subMu.Unlock()
	}
}

const (
	originLocal   = "local"
	originTimeout = "timeout"
)

func (a *Awareness) fireUpdate(s Summary, origin any) {
	if s.IsEmpty() {
		return
	}
	a.subMu.Lock()
	subs := make([]Sub, 0, len(a.onUpdateSub))
	for _, fn := range a.onUpdateSub {
		subs = append(subs, fn)
	}
	a.subMu.Unlock()
	for _, fn := range subs {
		fn(s, origin)
	}
}

func (a *Awareness) fireChange(s Summary, origin any) {
	if s.IsEmpty() {
		return
	}
	a.subMu.Lock()
	subs := make([]Sub, 0, len(a.onChangeSub))
	for _, fn := range a.onChangeSub {
		subs = append(subs, fn)
	}
	a.subMu.Unlock()
	for _, fn := range subs {
		fn(s, origin)
	}
}

// sortAscending is a tiny insertion-sort for the small slices
// returned by SweepOutdated. Avoids a sort.Slice closure-alloc.
func sortAscending(ids []uint64) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
}
