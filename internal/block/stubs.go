package block

// Forward-dependency stubs. These types are owned by layers we have not
// yet ported; the block layer references them only through pointers and
// never inspects their internals. Real definitions land with the types,
// store, and protocol layers per docs/yrs-port-notes/README.md.

// Branch is the owning collection for nested shared types (Map, Array,
// Text, Xml). The full type lives in the types layer when it lands;
// the fields below are the minimum subset that Item.Integrate and
// Item.Splice need to manipulate.
//
// Concurrency: a Branch is mutated only under a TransactionMut write
// lock. Reading from a non-current transaction is undefined.
//
// See yrs/src/branch.rs Branch.
type Branch struct {
	// Start is the head of the positional linked list (the first Item
	// in document order). nil for empty or map-only branches.
	Start *Item

	// Map associates a map-key with the rightmost Item that wrote
	// that key (the "winner" of the YATA tail). nil for non-map-like
	// branches; populated lazily.
	Map map[string]*Item

	// Item is the back-reference to the Item whose Content owns this
	// branch (for nested shared types). nil for root branches.
	// Integrate consults Item.IsDeleted to decide whether to
	// auto-delete the inserted item (parent_deleted path).
	Item *Item

	// BlockLen is the sum of Len for non-deleted, countable, positional
	// items in this branch. Integrate updates it as items land.
	BlockLen uint64

	// ContentLen is the sum of content-len for the same items. For
	// non-string content this matches BlockLen; for strings it may
	// differ once UTF-16 encoding semantics are wired (see
	// tech-debt.md surrogate-pair entry).
	ContentLen uint64

	// Name identifies a root branch. Empty string for non-root.
	// Used by encoders that need to emit the parent type name.
	Name string
}

// Move records a move operation for movable list items. Defined when
// Y.Array move support is added (post-MVP). See yrs/src/moving.rs.
type Move struct{}

// Doc is the document container. Stubbed here so ContentDoc compiles
// without pulling in the doc package.
type Doc struct{}

// Any is the JSON-like value type used as the payload for ContentAny,
// ContentEmbed, ContentFormat. yrs's Any is a proper tagged union
// (Bool, Number, BigInt, String, Buffer, Array, Map, Null, Undefined).
//
// We use Go's any here as a placeholder. The proper tagged-union form
// will land with the encoding/decoding work since wire compatibility
// requires deterministic encoding of each variant.
type Any = any

// OffsetKind selects between Yjs's UTF-16 (default, matches JS) and
// UTF-8 (alternative) text-position semantics. Internal Item lengths
// are always UTF-16 regardless of this setting; OffsetKind only
// affects what user-facing index parameters mean.
//
// See yrs/src/doc.rs OffsetKind enum and DESIGN.md "Wire-format-driven
// storage decisions".
type OffsetKind uint8

const (
	// OffsetUtf16 is the default. ytext.Insert(idx int, ...) accepts
	// idx in UTF-16 code units, matching JS Y.Text.length semantics.
	OffsetUtf16 OffsetKind = iota
	// OffsetBytes is an alternative where idx is in UTF-8 bytes.
	OffsetBytes
)
