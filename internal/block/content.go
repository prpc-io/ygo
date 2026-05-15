package block

import "fmt"

// ContentKind discriminates the variants of Content. The numeric values
// are the Yjs wire ref numbers (BLOCK_ITEM_*_REF_NUMBER from
// yrs/src/block.rs:28-72) and must not change.
//
// KindGC (0) and KindSkip (10) are not Content variants — they are
// parallel BlockCell kinds — but the constants are reserved here so the
// nibble values stay consistent with the wire.
type ContentKind uint8

const (
	KindGC      ContentKind = 0
	KindDeleted ContentKind = 1
	KindJSON    ContentKind = 2
	KindBinary  ContentKind = 3
	KindString  ContentKind = 4
	KindEmbed   ContentKind = 5
	KindFormat  ContentKind = 6
	KindType    ContentKind = 7
	KindAny     ContentKind = 8
	KindDoc     ContentKind = 9
	KindSkip    ContentKind = 10
	KindMove    ContentKind = 11
)

// Content is the payload of an Item. A single struct (not an interface)
// so it embeds in Item without an extra allocation and reads cleanly
// in tests without a type-switch.
//
// Each ContentKind uses only a subset of fields. The unused fields on
// any given variant must be left at their zero value; tests assert this.
//
// See docs/yrs-port-notes/block.md "ItemContent tagged union" for the
// rationale and the variant-to-field mapping.
type Content struct {
	Kind ContentKind

	// Variant-specific payloads. Set on the indicated kinds; zero
	// elsewhere.

	Anys      []Any    // KindAny, KindEmbed (1 elem), KindFormat (value, 1 elem)
	JSONStrs  []string // KindJSON (legacy stringified JSON, splittable)
	Bytes     []byte   // KindBinary
	Str       string   // KindString — UTF-8 input; internally normalized to UTF-16 for wire
	FormatKey string   // KindFormat (key)

	DeletedLen uint64 // KindDeleted, KindSkip — element count only

	Branch    *Branch // KindType
	Move      *Move   // KindMove
	Doc       *Doc    // KindDoc — child doc
	ParentDoc *Doc    // KindDoc — parent doc reference, set at integrate time
}

// RefNumber returns the Yjs wire ref number for the content's kind.
// This is the low nibble of the Item info byte.
func (c Content) RefNumber() uint8 {
	return uint8(c.Kind) & InfoContentMask
}

// IsCountable reports whether this content's elements contribute to
// the parent's user-facing length.
//
// yrs/src/block.rs ItemContent::is_countable: false for Deleted,
// Format, Move; true for everything else.
func (c Content) IsCountable() bool {
	switch c.Kind {
	case KindDeleted, KindFormat, KindMove, KindSkip, KindGC:
		return false
	default:
		return true
	}
}

// Len returns the number of elements (clock units) this content spans
// under the given offset semantics.
//
// For splittable variants (Any, JSON, String) this is the slice length
// in the appropriate unit; for single-element variants (Binary, Embed,
// Type, Doc) it is 1; for Deleted/Skip it is the stored length.
//
// String length is always UTF-16 code units regardless of kind, per
// yrs/src/block.rs comment on Item::new and the wire-format invariant.
// (See docs/yjs-architecture-notes.md §19 and DESIGN.md.)
//
// Return type is uint64 (yrs uses u32). We accept wire values up to
// 2^64-1; clock and length values produced by yrs and JS Yjs always
// fit in u32 in practice, so this is strictly a defensive widening.
func (c Content) Len(_ OffsetKind) uint64 {
	switch c.Kind {
	case KindAny:
		return uint64(len(c.Anys))
	case KindJSON:
		return uint64(len(c.JSONStrs))
	case KindString:
		// TODO(text): convert UTF-8 c.Str to UTF-16 code unit count.
		// For ASCII-only inputs, len(c.Str) is correct. For non-BMP
		// characters this overcounts; surrogate-pair-aware counting
		// arrives with the Text type implementation. See block.md
		// "Concrete Go translation choices" and open question 2.
		return uint64(len(c.Str))
	case KindBinary, KindEmbed, KindType, KindDoc:
		return 1
	case KindDeleted, KindSkip:
		return c.DeletedLen
	case KindFormat, KindMove:
		return 1
	case KindGC:
		// GC is a parallel BlockCell kind; Len is meaningless on a
		// Content with KindGC and callers should not query it. Return
		// 0 to fail noisily downstream rather than silently behave.
		return 0
	}
	return 0
}

// Split cuts the content at offset and returns the right half, mutating
// the receiver to hold the left half. Returns an error if the content
// kind is not splittable or offset is out of range.
//
// Splittable kinds: String (currently byte offsets — UTF-16 awareness
// arrives with the Text shared type, see tech-debt.md), Any, JSON,
// Deleted. All other kinds are single-element or parallel cell kinds
// and reject Split.
//
// See yrs/src/block.rs ItemContent::splice.
func (c *Content) Split(offset uint64) (Content, error) {
	switch c.Kind {
	case KindString:
		return c.splitString(offset)
	case KindAny:
		return c.splitAny(offset)
	case KindJSON:
		return c.splitJSON(offset)
	case KindDeleted:
		return c.splitDeleted(offset)
	default:
		return Content{}, fmt.Errorf("block: content kind %d is not splittable", c.Kind)
	}
}

func (c *Content) splitString(offset uint64) (Content, error) {
	if offset == 0 || offset >= uint64(len(c.Str)) {
		return Content{}, fmt.Errorf("block: split offset %d out of range for string length %d", offset, len(c.Str))
	}
	right := Content{Kind: KindString, Str: c.Str[offset:]}
	c.Str = c.Str[:offset]
	return right, nil
}

func (c *Content) splitAny(offset uint64) (Content, error) {
	if offset == 0 || offset >= uint64(len(c.Anys)) {
		return Content{}, fmt.Errorf("block: split offset %d out of range for any-slice length %d", offset, len(c.Anys))
	}
	right := Content{Kind: KindAny, Anys: c.Anys[offset:]}
	c.Anys = c.Anys[:offset]
	return right, nil
}

func (c *Content) splitJSON(offset uint64) (Content, error) {
	if offset == 0 || offset >= uint64(len(c.JSONStrs)) {
		return Content{}, fmt.Errorf("block: split offset %d out of range for json-slice length %d", offset, len(c.JSONStrs))
	}
	right := Content{Kind: KindJSON, JSONStrs: c.JSONStrs[offset:]}
	c.JSONStrs = c.JSONStrs[:offset]
	return right, nil
}

func (c *Content) splitDeleted(offset uint64) (Content, error) {
	if offset == 0 || offset >= c.DeletedLen {
		return Content{}, fmt.Errorf("block: split offset %d out of range for deleted length %d", offset, c.DeletedLen)
	}
	right := Content{Kind: KindDeleted, DeletedLen: c.DeletedLen - offset}
	c.DeletedLen = offset
	return right, nil
}

// TrySquash extends the receiver to absorb other's payload, returning
// true on success. Squashable kinds: Any (slice append), Deleted
// (length sum), JSON (slice append), String (concat). Non-matching
// kinds and other variants return false without mutation.
//
// Mirrors yrs/src/block.rs:1969-1993 ItemContent::try_squash.
func (c *Content) TrySquash(other *Content) bool {
	if c.Kind != other.Kind {
		return false
	}
	switch c.Kind {
	case KindAny:
		c.Anys = append(c.Anys, other.Anys...)
		return true
	case KindDeleted:
		c.DeletedLen += other.DeletedLen
		return true
	case KindJSON:
		c.JSONStrs = append(c.JSONStrs, other.JSONStrs...)
		return true
	case KindString:
		c.Str += other.Str
		return true
	default:
		return false
	}
}
