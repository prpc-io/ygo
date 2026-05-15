package block

// Item is the atomic CRDT unit of a Yjs document. Every insertion the
// user makes produces an Item; deletions tombstone an Item but never
// remove it.
//
// The struct shape mirrors yrs/src/block.rs Item one-for-one. See
// docs/yrs-port-notes/block.md for field-by-field semantics and the
// 13 invariants that integrate() and try_squash() depend on.
//
// Concurrency: *Item must never escape the transaction that produced
// it. The Doc's RWMutex is the only thing that makes pointer access
// to Left/Right/Origin/RightOrigin sound.
type Item struct {
	// ID of the FIRST element in this block. The block covers
	// IDs (Client, Clock) ... (Client, Clock+Len-1).
	ID ID

	// Number of elements (clock units) packed into this block.
	// Always counted in UTF-16 code units for String content, per
	// yrs/src/block.rs Item::new (block.rs:1307).
	//
	// uint64 matches our ID.Clock width. yrs uses u32; we widen
	// defensively since lib0 varuints can carry values up to u64.
	Len uint64

	// Doubly-linked list neighbours in document order. Mutable —
	// repointed by integrate(), splice(), and squash. nil at the
	// edges of the parent collection.
	Left  *Item
	Right *Item

	// Insertion-time neighbour IDs. Immutable — part of the wire
	// identity and used by YATA conflict resolution. nil when the
	// item was inserted at the start/end of the parent.
	Origin      *ID
	RightOrigin *ID

	// User payload.
	Content Content

	// Owning collection. Stored items always have Parent.IsResolved.
	Parent Parent

	// Map key when Parent is map-like (Y.Map, XML attributes).
	// nil for sequence-positional items (Y.Array, Y.Text).
	ParentSub *string

	// UndoManager bookkeeping: ID of the item that revived this one
	// via redo. nil for items that have not been redone.
	Redone *ID

	// Active Move operation controlling this item, if any.
	Moved *Move

	// Internal flag bits: FlagKeep, FlagCountable, FlagDeleted,
	// FlagMarked, FlagLinked. NOT serialized.
	Flags uint16
}

// LastID returns the ID of the last element this block covers, i.e.
// (Client, Clock+Len-1). Mirrors yrs Item::last_id (block.rs).
//
// Caller must ensure Len > 0; an empty Item is rejected at construction.
func (it *Item) LastID() ID {
	return ID{Client: it.ID.Client, Clock: it.ID.Clock + it.Len - 1}
}

// Info returns the Yjs wire info byte for this item: low nibble carries
// the content ref number; bits 5-7 carry presence flags for parent_sub,
// right_origin, origin.
//
// yrs/src/block.rs Item::info():
//
//	(origin.is_some()       ? HAS_ORIGIN       : 0)
//	| (right_origin.is_some() ? HAS_RIGHT_ORIGIN : 0)
//	| (parent_sub.is_some()   ? HAS_PARENT_SUB   : 0)
//	| (content.get_ref_number() & 0b1111)
func (it *Item) Info() uint8 {
	b := it.Content.RefNumber()
	if it.Origin != nil {
		b |= InfoHasOrigin
	}
	if it.RightOrigin != nil {
		b |= InfoHasRightOrigin
	}
	if it.ParentSub != nil {
		b |= InfoHasParentSub
	}
	return b
}

// flag accessors
//
// IsDeleted, IsCountable, IsKeep, IsLinked report individual bits.
// SetDeleted, SetCountable, SetKeep, SetLinked toggle them.
//
// Countable is initialized from Content.IsCountable() at construction
// and cleared when GC replaces the content with KindDeleted. The flag
// is the source of truth thereafter; do not re-derive from Content.

func (it *Item) IsDeleted() bool   { return it.Flags&FlagDeleted != 0 }
func (it *Item) IsCountable() bool { return it.Flags&FlagCountable != 0 }
func (it *Item) IsKeep() bool      { return it.Flags&FlagKeep != 0 }
func (it *Item) IsLinked() bool    { return it.Flags&FlagLinked != 0 }

func (it *Item) SetDeleted(v bool)   { setFlag(&it.Flags, FlagDeleted, v) }
func (it *Item) SetCountable(v bool) { setFlag(&it.Flags, FlagCountable, v) }
func (it *Item) SetKeep(v bool)      { setFlag(&it.Flags, FlagKeep, v) }
func (it *Item) SetLinked(v bool)    { setFlag(&it.Flags, FlagLinked, v) }

func setFlag(bits *uint16, mask uint16, on bool) {
	if on {
		*bits |= mask
	} else {
		*bits &^= mask
	}
}

// EqualByID reports whether two items name the same insertion (same ID).
// Yjs's Item::PartialEq derives equality through ItemPtr which compares
// by id only; this is the same semantics in Go.
//
// Use this for set/map keys and tests of structural identity. Use
// content/flag/neighbour comparison directly when checking whether two
// items are byte-identical (e.g. encode/decode roundtrip tests).
func (it *Item) EqualByID(other *Item) bool {
	if it == nil || other == nil {
		return it == other
	}
	return it.ID.Equal(other.ID)
}
