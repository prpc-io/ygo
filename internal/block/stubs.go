package block

// Forward-dependency stubs. These types are owned by layers we have not
// yet ported; the block layer references them only through pointers and
// never inspects their internals. Real definitions land with the types,
// store, and protocol layers per docs/yrs-port-notes/README.md.

// TypeRef discriminates the kind of shared type a Branch represents.
// Wire constants match yjs/src/structs/ContentType.js Y*RefID family
// (Item.js:1382-1388) and yrs/src/types/mod.rs TypeRef enum.
//
// The TypeRef byte is emitted as a varuint at the start of a
// ContentType payload — see docs/yrs-port-notes/nested-types.md §2.
// Root branches do NOT emit TypeRef on the wire (they are referenced
// by name), but their in-memory Branch.TypeRef field is still set so
// extractValue can return the right wrapper type to user code.
//
// TypeRefArray is intentionally 0 to match the JS/Rust wire value.
// TypeRefUndefined (15) is the sentinel for "not specified" — used
// for Branch values constructed before their final type is known
// (e.g. a freshly-decoded ContentType payload whose type-refs byte
// hasn't been read yet). A zero Branch{} would silently read as Array
// without an explicit sentinel, so constructors must set TypeRef
// explicitly.
type TypeRef uint8

const (
	TypeRefArray       TypeRef = 0
	TypeRefMap         TypeRef = 1
	TypeRefText        TypeRef = 2
	TypeRefXmlElement  TypeRef = 3
	TypeRefXmlFragment TypeRef = 4
	TypeRefXmlHook     TypeRef = 5
	TypeRefXmlText     TypeRef = 6
	TypeRefDoc         TypeRef = 9
	TypeRefUndefined   TypeRef = 15
)

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

	// TypeRef discriminates the kind of shared type this Branch
	// represents (Map, Array, Text, Xml*, etc.). Set on construction
	// by the types layer; consumed by the ContentType encoder and by
	// extractValue when wrapping a nested type for user code.
	//
	// The zero value (TypeRefArray) is intentional for the wire
	// format — Array's type-refs byte is 0. Constructors that build
	// non-Array branches MUST set this explicitly.
	TypeRef TypeRef

	// Markers is the per-branch search-marker cache used by Array /
	// Text position resolution to skip O(N) linked-list walks on
	// hot edit paths. Allocated lazily (nil until the first marker
	// goes in) so branches that never see large positional ops pay
	// no overhead. See search_marker.go for the LRU semantics and
	// docs/yrs-port-notes/types-array.md finding 1 + BENCHMARKS.md
	// B4 entry for the workload that motivated this.
	Markers *MarkerList
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
