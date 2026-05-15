package block

// Forward-dependency stubs. These types are owned by layers we have not
// yet ported; the block layer references them only through pointers and
// never inspects their internals. Real definitions land with the types,
// store, and protocol layers per docs/yrs-port-notes/README.md.

// Branch is the owning collection for nested shared types (Map, Array,
// Text, Xml). Defined in full when the types layer is ported. See
// yrs/src/branch.rs.
type Branch struct{}

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
