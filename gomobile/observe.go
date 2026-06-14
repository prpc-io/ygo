package gomobile

import (
	"encoding/json"

	"github.com/Deln0r/ygo/internal/types"
)

// TextChangeListener receives text change deltas. Implement it in
// Swift / Kotlin and pass to Text.ObserveChanges. deltaJSON is a
// Quill-style delta array, the same shape Quill / ProseMirror / a JS
// Yjs binding consumes, so a native editor can apply it directly:
//
//	[{"retain":3},{"insert":"hi","attributes":{"bold":true}},{"delete":2}]
//
// Called from a background goroutine while the document lock is held;
// dispatch to the main thread before touching UI and do not call back
// into the document synchronously.
type TextChangeListener interface {
	OnTextChange(deltaJSON []byte)
}

// MapChangeListener receives map change summaries. keysJSON maps each
// changed key to {"action":"add|update|delete","oldValue":...}:
//
//	{"title":{"action":"update","oldValue":"draft"}}
type MapChangeListener interface {
	OnMapChange(keysJSON []byte)
}

// ObserveChanges registers a listener that fires after every
// transaction changing this text, delivering the change as a Quill
// delta. This is the fine-grained signal a native editor needs to
// update only what changed, instead of re-rendering the whole
// document. Replaces any previously registered listener on this text.
func (t *Text) ObserveChanges(l TextChangeListener) {
	if t.unobserve != nil {
		t.unobserve()
		t.unobserve = nil
	}
	if l == nil {
		return
	}
	t.unobserve = t.inner.Observe(func(e *types.TextEvent) {
		l.OnTextChange(marshalTextDelta(e.Delta))
	})
}

// ObserveChanges registers a listener that fires after every
// transaction changing this map, delivering the changed keys.
// Replaces any previously registered listener on this map.
func (m *Map) ObserveChanges(l MapChangeListener) {
	if m.unobserve != nil {
		m.unobserve()
		m.unobserve = nil
	}
	if l == nil {
		return
	}
	m.unobserve = m.inner.Observe(func(e *types.MapEvent) {
		l.OnMapChange(marshalMapKeys(e.Keys))
	})
}

// marshalTextDelta renders a text delta in the standard Quill JSON
// shape.
func marshalTextDelta(delta []types.DeltaOp) []byte {
	ops := make([]map[string]any, 0, len(delta))
	for _, op := range delta {
		m := map[string]any{}
		switch {
		case op.Insert != "":
			m["insert"] = op.Insert
		case op.Embed != nil:
			m["insert"] = op.Embed
		case op.Retain > 0:
			m["retain"] = op.Retain
		case op.Delete > 0:
			m["delete"] = op.Delete
		}
		if len(op.Attributes) > 0 {
			m["attributes"] = op.Attributes
		}
		ops = append(ops, m)
	}
	b, _ := json.Marshal(ops)
	return b
}

// marshalMapKeys renders a map event's changed keys as JSON.
func marshalMapKeys(keys map[string]types.KeyChange) []byte {
	out := make(map[string]map[string]any, len(keys))
	for k, c := range keys {
		entry := map[string]any{"action": c.Action}
		if c.OldValue != nil {
			entry["oldValue"] = c.OldValue
		}
		out[k] = entry
	}
	b, _ := json.Marshal(out)
	return b
}
