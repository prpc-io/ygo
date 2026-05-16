package types

import (
	"fmt"

	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/utf16"
)

// Attrs is the format-attribute map carried by KindFormat markers
// and exposed in Range / ToDelta output. Per
// docs/yrs-port-notes/types-text-rich.md §9.
//
// Nil values inside Attrs mean "clear this attribute" — they encode
// to the Any-Null tag on the wire and are interpreted by
// currentAttributes as removing the key. To distinguish "key not
// present" from "key present with nil value", check via the second
// return of a map lookup.
type Attrs = map[string]any

// DeltaOp is a single op from a Quill-style delta. Exactly one of
// Insert / Retain / Delete is non-zero for any given op. Used by
// ToDelta to expose the doc and by ApplyDelta to ingest one (the
// ApplyDelta API ships in a follow-up commit; see tech-debt.md).
type DeltaOp struct {
	// Insert is the text to insert at the cursor. Mutually exclusive
	// with Embed, Retain, Delete.
	Insert string

	// Embed is a single embedded value (image, mention, etc.). Mutually
	// exclusive with Insert, Retain, Delete. The Embed value's runtime
	// type matches whatever Y.encodeStateAsUpdate-produced JS clients
	// would round-trip through Any — typically a map[string]any.
	Embed any

	// Retain advances the cursor by N UTF-16 units, optionally
	// applying Attributes to the retained range. Mutually exclusive
	// with Insert, Embed, Delete.
	Retain uint64

	// Delete removes N UTF-16 units from the cursor. Mutually exclusive
	// with Insert, Embed, Retain.
	Delete uint64

	// Attributes applies to Insert, Embed, or Retain. Nil-valued keys
	// inside Attributes mean "clear this attribute" on the affected
	// range; absent keys preserve the current value.
	Attributes Attrs
}

// InsertWithAttributes inserts str at idx, opening format markers
// before the text for any attribute whose value differs from the
// current formatting, and closing them afterward to restore the
// prior state.
//
// nil attrs is equivalent to Insert(idx, str). An empty map (zero
// keys) is also equivalent.
//
// Per docs/yrs-port-notes/types-text-rich.md §5, the algorithm
// mirrors yrs Text::insert_with_attributes (text.rs:280-295) +
// JS Y.Text._insertWithAttributes (ytype.js:330-350).
func (t *Text) InsertWithAttributes(txn *doc.TransactionMut, idx uint64, str string, attrs Attrs) error {
	if str == "" {
		return nil
	}
	if len(attrs) == 0 {
		return t.Insert(txn, idx, str)
	}

	length := t.Length()
	if idx > length {
		return fmt.Errorf("text: insert index %d out of range [0, %d]", idx, length)
	}

	left, right, err := findTextPosition(t.branch, txn, idx)
	if err != nil {
		return err
	}
	// Skip past tombstones AND existing format markers at the cursor
	// so the new content lands at the natural end of the position
	// (after any close markers that would otherwise leave the new
	// text inside the prior formatting range).
	for right != nil && (right.IsDeleted() || right.Content.Kind == block.KindFormat) {
		left = right
		right = right.Right
	}

	currentAttrs := currentAttributesAt(t.branch, right)

	// Emit opening markers for every attr whose target value differs
	// from current. Skip keys that already match.
	for key, value := range attrs {
		if attrValuesEqual(currentAttrs[key], value) {
			continue
		}
		marker := buildFormatMarker(txn, left, right, t.branch, key, value)
		// The new marker is now the left neighbour for subsequent
		// inserts at this cursor.
		left = marker
		// right stays — markers sit between left and the next item.
	}

	// Insert the text itself.
	if err := insertStringBetween(txn, t.branch, left, right, str); err != nil {
		return err
	}

	// Compute the post-insert cursor for closing markers: walk to
	// just past the string item we just inserted.
	postLeft := postInsertLeftOf(t.branch, idx+utf16.Length(str))
	postRight := neighbourRightOf(postLeft)

	// Emit closing markers to restore the previous formatting where
	// our applied attrs differ. For each key we opened, emit a
	// marker carrying its previous value (which may be nil = clear).
	for key, value := range attrs {
		prev := currentAttrs[key]
		if attrValuesEqual(prev, value) {
			continue
		}
		closeMarker := buildFormatMarker(txn, postLeft, postRight, t.branch, key, prev)
		postLeft = closeMarker
	}

	return nil
}

// Format applies attrs to the range [idx, idx+length). For each
// attribute whose target value differs from the current formatting:
// open a marker at idx, walk through the range rewriting any
// intermediate markers for the same key (to ensure they don't
// re-establish the old value mid-range), and close with a marker
// at idx+length restoring the prior state.
//
// length == 0 is a no-op. nil attrs is a no-op. Per
// docs/yrs-port-notes/types-text-rich.md §4.
func (t *Text) Format(txn *doc.TransactionMut, idx, length uint64, attrs Attrs) error {
	if length == 0 || len(attrs) == 0 {
		return nil
	}
	total := t.Length()
	if idx+length > total {
		return fmt.Errorf("text: format range [%d, %d) exceeds length %d", idx, idx+length, total)
	}

	// Split at the start boundary so we can insert a marker exactly there.
	startLeft, startRight, err := findTextPosition(t.branch, txn, idx)
	if err != nil {
		return err
	}
	// Skip tombstones and format markers so cursor lands at the
	// natural end of the position — any pre-existing markers there
	// have already taken effect.
	for startRight != nil && (startRight.IsDeleted() || startRight.Content.Kind == block.KindFormat) {
		startLeft = startRight
		startRight = startRight.Right
	}

	// Snapshot startAttrs BEFORE emitting any new markers so the
	// "what was here originally" view is stable.
	startAttrs := currentAttributesAt(t.branch, startRight)

	// Split + locate end position. endLeft / endRight are pointers
	// to existing Items; emitting open markers at the start position
	// does not invalidate them because the new markers land strictly
	// before endRight in the linked list.
	endLeft, endRight, err := findTextPosition(t.branch, txn, idx+length)
	if err != nil {
		return err
	}
	for endRight != nil && (endRight.IsDeleted() || endRight.Content.Kind == block.KindFormat) {
		endLeft = endRight
		endRight = endRight.Right
	}

	// Snapshot endAttrs BEFORE emitting any markers — this is the
	// "what was originally in effect at idx+length" view we will
	// restore via closing markers.
	endAttrs := currentAttributesAt(t.branch, endRight)

	// Emit opening markers — only for keys whose value changes.
	for key, value := range attrs {
		if attrValuesEqual(startAttrs[key], value) {
			continue
		}
		marker := buildFormatMarker(txn, startLeft, startRight, t.branch, key, value)
		startLeft = marker
	}

	// Emit closing markers to restore whatever was in effect at the
	// end position before our format applied.
	for key, value := range attrs {
		if attrValuesEqual(startAttrs[key], value) {
			continue
		}
		prevAtEnd := endAttrs[key]
		// If our applied value is already what's in effect at the
		// end (i.e. the original formatting at idx+length was the
		// same as what we're applying), no close needed.
		if attrValuesEqual(prevAtEnd, value) {
			continue
		}
		closeMarker := buildFormatMarker(txn, endLeft, endRight, t.branch, key, prevAtEnd)
		endLeft = closeMarker
	}

	return nil
}

// InsertEmbed inserts a single embedded value at idx. The value
// must be a type representable through lib0 Any encoding —
// typically map[string]any or one of the scalar Any types.
//
// Embeds count as one UTF-16 position in Text.Length. They are
// emitted by Range / ToDelta as an Embed op rather than a string
// chunk.
func (t *Text) InsertEmbed(txn *doc.TransactionMut, idx uint64, value any) error {
	total := t.Length()
	if idx > total {
		return fmt.Errorf("text: embed index %d out of range [0, %d]", idx, total)
	}
	left, right, err := findTextPosition(t.branch, txn, idx)
	if err != nil {
		return err
	}
	for right != nil && right.IsDeleted() {
		left = right
		right = right.Right
	}

	var origin, rightOrigin *block.ID
	if left != nil {
		lid := left.LastID()
		origin = &lid
	}
	if right != nil {
		rid := right.ID
		rightOrigin = &rid
	}

	clientID := txn.Doc().ClientID()
	clock := txn.Store().GetClock(clientID)

	item := &block.Item{
		ID:          block.ID{Client: clientID, Clock: clock},
		Len:         1,
		Origin:      origin,
		Left:        left,
		RightOrigin: rightOrigin,
		Right:       right,
		Content:     block.Content{Kind: block.KindEmbed, Anys: []block.Any{value}},
		Parent:      block.Parent{Kind: block.ParentBranch, Branch: t.branch},
		Flags:       block.FlagCountable,
	}
	txn.Store().PushBlock(item)
	if dropped := item.Integrate(txn, 0); dropped {
		txn.Delete(item)
	}
	return nil
}

// ChunkKind discriminates the variants emitted by Text.Range.
type ChunkKind uint8

const (
	// ChunkString is a contiguous run of UTF-8 text from a KindString
	// item; the value passed to fn is a string.
	ChunkString ChunkKind = iota
	// ChunkEmbed is a single embedded value from a KindEmbed item;
	// the value passed to fn is whatever the embed payload's runtime
	// type is (typically map[string]any or a primitive Any).
	ChunkEmbed
)

// Range walks the document and invokes fn for each (kind, value, attrs)
// chunk. attrs reflects the current formatting at this chunk's start.
//
// Stops early when fn returns false. Returns immediately for an
// empty doc.
//
// Per docs/yrs-port-notes/types-text-rich.md §8 — companion to ToDelta.
// Explicit ChunkKind discriminator means a string-typed embed payload
// is not misidentified as a text chunk by callers that switch on
// the runtime type of the value.
func (t *Text) Range(fn func(kind ChunkKind, value any, attrs Attrs) bool) {
	current := Attrs{}
	for cur := t.branch.Start; cur != nil; cur = cur.Right {
		if cur.IsDeleted() {
			continue
		}
		switch cur.Content.Kind {
		case block.KindFormat:
			var value any
			if len(cur.Content.Anys) > 0 {
				value = cur.Content.Anys[0]
			}
			updateCurrentAttrs(current, cur.Content.FormatKey, value)
		case block.KindString:
			if cur.Content.Str == "" {
				continue
			}
			if !fn(ChunkString, cur.Content.Str, copyAttrs(current)) {
				return
			}
		case block.KindEmbed:
			var v any
			if len(cur.Content.Anys) > 0 {
				v = cur.Content.Anys[0]
			}
			if !fn(ChunkEmbed, v, copyAttrs(current)) {
				return
			}
		}
	}
}

// ApplyDelta walks ops in order and dispatches each to the
// appropriate primitive (InsertWithAttributes / InsertEmbed /
// Format / Delete) while maintaining a cursor through the text.
//
// Op semantics (mirrors JS Y.Text.applyDelta from
// testdata/gen/node_modules/yjs/src/types/YText.js):
//
//   - Insert string with optional Attributes — insert text at
//     cursor, advance cursor by len(text)
//   - Insert non-string (Embed) — insert single embed at cursor,
//     advance cursor by 1
//   - Retain N with optional Attributes — if Attributes is non-nil,
//     apply Format(cursor, N, Attributes); advance cursor by N
//   - Delete N — delete N units at cursor; cursor unchanged
//     (the text shifts left under the cursor)
//
// All ops execute inside the supplied txn so the entire delta is
// atomic from peers' perspective. Errors abort mid-delta — the
// txn caller must decide whether to commit the partial result or
// roll back (the doc layer's TransactionMut has no Rollback, so
// the partial mutation persists; this mirrors yrs's behaviour).
//
// Per docs/yrs-port-notes/types-text-rich.md §7. Empty ops slice
// is a no-op; nil ops also a no-op.
func (t *Text) ApplyDelta(txn *doc.TransactionMut, ops []DeltaOp) error {
	var cursor uint64
	for i, op := range ops {
		switch {
		case op.Insert != "":
			if err := t.InsertWithAttributes(txn, cursor, op.Insert, op.Attributes); err != nil {
				return fmt.Errorf("ApplyDelta op[%d] insert: %w", i, err)
			}
			cursor += utf16.Length(op.Insert)
		case op.Embed != nil:
			if err := t.InsertEmbed(txn, cursor, op.Embed); err != nil {
				return fmt.Errorf("ApplyDelta op[%d] embed: %w", i, err)
			}
			cursor++
		case op.Retain > 0:
			if op.Attributes != nil {
				if err := t.Format(txn, cursor, op.Retain, op.Attributes); err != nil {
					return fmt.Errorf("ApplyDelta op[%d] retain+format: %w", i, err)
				}
			}
			cursor += op.Retain
		case op.Delete > 0:
			if err := t.Delete(txn, cursor, op.Delete); err != nil {
				return fmt.Errorf("ApplyDelta op[%d] delete: %w", i, err)
			}
			// cursor unchanged — text shifts left under cursor
		default:
			// All-zero op (or a Retain == 0 with nil attrs) is a no-op.
		}
	}
	return nil
}

// ToDelta returns the Quill-style delta representation of the doc.
// Adjacent same-attribute string chunks are coalesced into single
// insert ops. Embeds appear as separate ops.
//
// The returned slice has no Retain / Delete ops — those exist only
// in deltas representing CHANGES, not snapshots.
func (t *Text) ToDelta() []DeltaOp {
	var out []DeltaOp
	var pendingStr string
	var pendingAttrs Attrs

	flush := func() {
		if pendingStr == "" {
			return
		}
		out = append(out, DeltaOp{
			Insert:     pendingStr,
			Attributes: pendingAttrs,
		})
		pendingStr = ""
		pendingAttrs = nil
	}

	t.Range(func(kind ChunkKind, v any, attrs Attrs) bool {
		switch kind {
		case ChunkString:
			s, _ := v.(string)
			if pendingStr != "" && !attrsEqual(pendingAttrs, attrs) {
				flush()
			}
			pendingStr += s
			if pendingAttrs == nil {
				pendingAttrs = attrs
			}
		case ChunkEmbed:
			flush()
			out = append(out, DeltaOp{
				Embed:      v,
				Attributes: copyNonEmptyAttrs(attrs),
			})
		}
		return true
	})
	flush()
	return out
}

// buildFormatMarker constructs and integrates a single KindFormat
// Item between left and right neighbours. Returns the new Item so
// the caller can chain subsequent inserts off it.
func buildFormatMarker(txn *doc.TransactionMut, left, right *block.Item, parentBranch *block.Branch, key string, value any) *block.Item {
	var origin, rightOrigin *block.ID
	if left != nil {
		lid := left.LastID()
		origin = &lid
	}
	if right != nil {
		rid := right.ID
		rightOrigin = &rid
	}
	clientID := txn.Doc().ClientID()
	clock := txn.Store().GetClock(clientID)
	marker := &block.Item{
		ID:          block.ID{Client: clientID, Clock: clock},
		Len:         1,
		Origin:      origin,
		Left:        left,
		RightOrigin: rightOrigin,
		Right:       right,
		Content:     block.Content{Kind: block.KindFormat, FormatKey: key, Anys: []block.Any{value}},
		Parent:      block.Parent{Kind: block.ParentBranch, Branch: parentBranch},
		// FlagCountable left off — format markers do not contribute
		// to branch.BlockLen (per Content.IsCountable() returning
		// false for KindFormat).
	}
	txn.Store().PushBlock(marker)
	if dropped := marker.Integrate(txn, 0); dropped {
		txn.Delete(marker)
	}
	return marker
}

// insertStringBetween emits a KindString Item between left and
// right neighbours. Used by InsertWithAttributes after format
// markers have been placed.
func insertStringBetween(txn *doc.TransactionMut, parentBranch *block.Branch, left, right *block.Item, str string) error {
	var origin, rightOrigin *block.ID
	if left != nil {
		lid := left.LastID()
		origin = &lid
	}
	if right != nil {
		rid := right.ID
		rightOrigin = &rid
	}
	clientID := txn.Doc().ClientID()
	clock := txn.Store().GetClock(clientID)
	item := &block.Item{
		ID:          block.ID{Client: clientID, Clock: clock},
		Len:         utf16.Length(str),
		Origin:      origin,
		Left:        left,
		RightOrigin: rightOrigin,
		Right:       right,
		Content:     block.Content{Kind: block.KindString, Str: str},
		Parent:      block.Parent{Kind: block.ParentBranch, Branch: parentBranch},
		Flags:       block.FlagCountable,
	}
	txn.Store().PushBlock(item)
	if dropped := item.Integrate(txn, 0); dropped {
		txn.Delete(item)
	}
	return nil
}

// currentAttributesAt is the branch-aware accumulator used by
// InsertWithAttributes / Format. Walks branch.Start forward until
// it reaches the supplied right neighbour (or the end), accumulating
// format markers along the way. The last occurrence per key wins.
//
// Tombstoned items are skipped. nil-valued format markers clear
// the key (deleted from the running map).
func currentAttributesAt(branch *block.Branch, right *block.Item) Attrs {
	out := Attrs{}
	for cur := branch.Start; cur != nil; cur = cur.Right {
		if cur == right {
			break
		}
		if cur.IsDeleted() {
			continue
		}
		if cur.Content.Kind != block.KindFormat {
			continue
		}
		var value any
		if len(cur.Content.Anys) > 0 {
			value = cur.Content.Anys[0]
		}
		updateCurrentAttrs(out, cur.Content.FormatKey, value)
	}
	return out
}

// postInsertLeftOf finds the leftmost Item at or after position pos
// in the branch — essentially the same as findTextPosition's left
// return but without triggering a split. Used by InsertWithAttributes
// to position closing markers after the just-inserted string.
//
// Walks from branch.Start counting UTF-16 units in live String items.
// Returns nil if pos is at the very start (no item to anchor left of).
func postInsertLeftOf(branch *block.Branch, pos uint64) *block.Item {
	var counted uint64
	var lastSeen *block.Item
	for cur := branch.Start; cur != nil; cur = cur.Right {
		if cur.IsDeleted() {
			continue
		}
		if cur.Content.Kind == block.KindString {
			contentLen := cur.Content.Len(block.OffsetUtf16)
			counted += contentLen
			lastSeen = cur
			if counted >= pos {
				return cur
			}
		}
	}
	return lastSeen
}

// neighbourRightOf returns the Item immediately after item in the
// branch linked list, or nil if item is at the tail. Used to compute
// the right-neighbour for marker insertion.
func neighbourRightOf(item *block.Item) *block.Item {
	if item == nil {
		return nil
	}
	return item.Right
}

// updateCurrentAttrs applies one KindFormat marker to the running
// attrs map: a nil value clears the key, a non-nil value sets it.
func updateCurrentAttrs(attrs Attrs, key string, value any) {
	if value == nil {
		delete(attrs, key)
		return
	}
	attrs[key] = value
}

// copyAttrs returns a shallow copy of attrs so callers cannot
// mutate the walker's running map.
func copyAttrs(attrs Attrs) Attrs {
	if len(attrs) == 0 {
		return nil
	}
	out := make(Attrs, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

// copyNonEmptyAttrs returns nil for empty attrs (cleaner serialization)
// or a deep copy otherwise.
func copyNonEmptyAttrs(attrs Attrs) Attrs {
	if len(attrs) == 0 {
		return nil
	}
	return copyAttrs(attrs)
}

// attrValuesEqual compares two Any values for structural equality.
// Both nil are equal; nil-vs-non-nil is unequal; otherwise direct
// comparison via Go == works for the primitive Any variants we
// support (bool, int, int64, float64, string).
func attrValuesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a == b
}

// attrsEqual reports whether two Attrs maps contain identical
// (key, value) sets. Order-independent.
func attrsEqual(a, b Attrs) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || !attrValuesEqual(va, vb) {
			return false
		}
	}
	return true
}
