package encoding

import (
	"fmt"

	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/lib0"
)

// EncodeContent appends the wire-format payload of c to buf. Caller
// has already emitted the info byte; this writes only the content
// field that follows.
//
// Mirrors yrs/src/block.rs:1844-1872 ItemContent::encode.
//
// Supported in this commit: KindAny, KindString, KindBinary,
// KindDeleted, KindType, KindFormat, KindEmbed.
// Skipped: KindDoc, KindMove, KindJSON.
// All deferred kinds panic on encode rather than emit silently-wrong
// bytes.
func EncodeContent(buf []byte, c block.Content) []byte {
	switch c.Kind {
	case block.KindAny:
		buf = lib0.WriteVarUint(buf, uint64(len(c.Anys)))
		for _, v := range c.Anys {
			buf = EncodeAny(buf, v)
		}
		return buf
	case block.KindString:
		return lib0.WriteVarString(buf, c.Str)
	case block.KindBinary:
		return lib0.WriteVarUint8Array(buf, c.Bytes)
	case block.KindDeleted:
		return lib0.WriteVarUint(buf, c.DeletedLen)
	case block.KindEmbed:
		// Single Any payload — yjs/src/structs/ContentEmbed.js write.
		// We carry Embed's payload in Anys[0]; if missing, treat as nil
		// (encoded as Any-Null tag).
		var v block.Any
		if len(c.Anys) > 0 {
			v = c.Anys[0]
		}
		return EncodeAny(buf, v)
	case block.KindFormat:
		// varstring(key) + Any(value) — yjs ContentFormat.js write.
		// Anys[0] is the value (may be nil to signal clear-attribute,
		// which encodes as the Any-Null tag — receiver interprets the
		// null in updateCurrentAttributes per types-text-rich.md gotcha 8).
		buf = lib0.WriteVarString(buf, c.FormatKey)
		var v block.Any
		if len(c.Anys) > 0 {
			v = c.Anys[0]
		}
		return EncodeAny(buf, v)
	case block.KindType:
		// ContentType payload: varuint(typeRef) + optional
		// varstring(name) for XmlElement (refID 3) and XmlHook
		// (refID 5). Per docs/yrs-port-notes/nested-types.md §2,
		// citing yjs/src/structs/ContentType.js:1507-1510 and
		// yrs equivalent.
		if c.Branch == nil {
			panic("encoding.EncodeContent: KindType with nil Branch")
		}
		buf = lib0.WriteVarUint(buf, uint64(c.Branch.TypeRef))
		switch c.Branch.TypeRef {
		case block.TypeRefXmlElement, block.TypeRefXmlHook:
			buf = lib0.WriteVarString(buf, c.Branch.Name)
		}
		return buf
	default:
		panic(fmt.Sprintf("encoding.EncodeContent: unsupported kind %d (supported: Any, String, Binary, Deleted, Type)", c.Kind))
	}
}

// DecodeContent reads a Content payload from buf given the content
// ref-number (the low nibble of the info byte). Returns the parsed
// Content plus the unconsumed tail.
//
// Mirrors yrs/src/block.rs ItemContent::decode dispatch.
func DecodeContent(buf []byte, refNum uint8) (block.Content, []byte, error) {
	switch block.ContentKind(refNum) {
	case block.KindAny:
		count, n, err := lib0.ReadVarUint(buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		buf = buf[n:]
		anys := make([]block.Any, count)
		for i := uint64(0); i < count; i++ {
			v, tail, err := DecodeAny(buf)
			if err != nil {
				return block.Content{}, buf, err
			}
			anys[i] = v
			buf = tail
		}
		return block.Content{Kind: block.KindAny, Anys: anys}, buf, nil
	case block.KindString:
		s, n, err := lib0.ReadVarString(buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		return block.Content{Kind: block.KindString, Str: s}, buf[n:], nil
	case block.KindBinary:
		b, n, err := lib0.ReadVarUint8Array(buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		// Copy the slice — DecodeContent's return must not alias the
		// input buffer, otherwise mutation of the buffer corrupts
		// stored content.
		out := make([]byte, len(b))
		copy(out, b)
		return block.Content{Kind: block.KindBinary, Bytes: out}, buf[n:], nil
	case block.KindDeleted:
		v, n, err := lib0.ReadVarUint(buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		return block.Content{Kind: block.KindDeleted, DeletedLen: v}, buf[n:], nil
	case block.KindEmbed:
		v, tail, err := DecodeAny(buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		return block.Content{Kind: block.KindEmbed, Anys: []block.Any{v}}, tail, nil
	case block.KindFormat:
		key, n, err := lib0.ReadVarString(buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		buf = buf[n:]
		v, tail, err := DecodeAny(buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		return block.Content{Kind: block.KindFormat, FormatKey: key, Anys: []block.Any{v}}, tail, nil
	case block.KindType:
		// Mirror of EncodeContent.KindType. Build an empty Branch
		// with the wire-supplied TypeRef. The Branch.Item back-
		// reference is set by the Repair / Integrate path once the
		// containing Item is fully constructed (gotcha 2 in
		// nested-types.md). Map field is lazily allocated when the
		// types layer first writes a map-key.
		typeRefU, n, err := lib0.ReadVarUint(buf)
		if err != nil {
			return block.Content{}, buf, err
		}
		buf = buf[n:]
		typeRef := block.TypeRef(typeRefU)
		br := &block.Branch{TypeRef: typeRef}
		switch typeRef {
		case block.TypeRefXmlElement, block.TypeRefXmlHook:
			name, n, err := lib0.ReadVarString(buf)
			if err != nil {
				return block.Content{}, buf, err
			}
			br.Name = name
			buf = buf[n:]
		}
		return block.Content{Kind: block.KindType, Branch: br}, buf, nil
	default:
		return block.Content{}, buf, fmt.Errorf("encoding.DecodeContent: unsupported content kind %d (tech-debt.md)", refNum)
	}
}
