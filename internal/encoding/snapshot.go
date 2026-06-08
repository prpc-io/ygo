package encoding

import (
	"sort"

	"github.com/Deln0r/ygo/internal/lib0"
	"github.com/Deln0r/ygo/internal/store"
)

// Snapshot is a point-in-time marker of a document's history: the set
// of deleted ID ranges (DS) plus the per-client clock heads (SV) as of
// the snapshot moment. Replaying the document up to SV, with DS applied
// as the deletions-so-far, reconstructs exactly the state the document
// had when the snapshot was taken.
//
// Mirrors yjs Snapshot (src/utils/Snapshot.js): a {ds, sv} pair. The
// wire form is writeDeleteSet(ds) followed by writeStateVector(sv).
type Snapshot struct {
	DS *IdSet
	SV store.StateVector
}

// CreateSnapshot captures the current delete set and state vector of
// the store. Mirrors yjs's `snapshot(doc)`.
//
// For snapshots to be useful for time-travel the doc should be created
// with GC disabled, otherwise deleted content the snapshot references
// may have been collected. The capture itself does not require it.
func CreateSnapshot(bs *store.BlockStore) Snapshot {
	sv := bs.GetStateVector()
	ds := buildDeleteSetFromStore(bs, sv)
	return Snapshot{DS: ds, SV: sv}
}

// EqualSnapshots reports whether two snapshots carry the same delete
// set and state vector. Mirrors yjs equalSnapshots.
func EqualSnapshots(a, b Snapshot) bool {
	if len(a.SV) != len(b.SV) {
		return false
	}
	for c, clk := range a.SV {
		if b.SV[c] != clk {
			return false
		}
	}
	ac, bc := a.DS.clients, b.DS.clients
	if len(ac) != len(bc) {
		return false
	}
	for c, ar := range ac {
		br, ok := bc[c]
		if !ok || len(ar) != len(br) {
			return false
		}
		for i := range ar {
			if ar[i] != br[i] {
				return false
			}
		}
	}
	return true
}

// EncodeSnapshot returns the V1 wire encoding of s: the delete set
// followed by the state vector. Byte-compatible with yjs
// `encodeSnapshot` (DSEncoderV1).
func EncodeSnapshot(s Snapshot) []byte {
	var buf []byte
	buf = encodeDeleteSetV1(s.DS, buf)
	buf = EncodeStateVector(s.SV, buf)
	return buf
}

// DecodeSnapshot parses a V1 snapshot produced by EncodeSnapshot or
// yjs `encodeSnapshot`.
func DecodeSnapshot(buf []byte) (Snapshot, error) {
	ds, rest, err := DecodeIdSet(buf)
	if err != nil {
		return Snapshot{}, err
	}
	sv, _, err := DecodeStateVector(rest)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{DS: ds, SV: sv}, nil
}

// encodeDeleteSetV1 writes the V1 delete-set wire form with clients in
// DESCENDING order, matching yjs writeDeleteSet ("sort((a,b) => b[0] -
// a[0])") and ygo's own writeDeleteSetV2. The general-purpose
// IdSet.Encode currently emits ascending order, which is only safe for
// single-client sets; snapshots can be multi-client, so we sort
// correctly here. See FEATURE_RACE housekeeping note about unifying
// the V1 delete-set writers.
func encodeDeleteSetV1(ds *IdSet, buf []byte) []byte {
	clients := make([]uint64, 0, len(ds.clients))
	for c, ranges := range ds.clients {
		if len(ranges) > 0 {
			clients = append(clients, c)
		}
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i] > clients[j] })

	buf = lib0.WriteVarUint(buf, uint64(len(clients)))
	for _, c := range clients {
		ranges := ds.clients[c]
		buf = lib0.WriteVarUint(buf, c)
		buf = lib0.WriteVarUint(buf, uint64(len(ranges)))
		for _, r := range ranges {
			buf = lib0.WriteVarUint(buf, r.Start)
			buf = lib0.WriteVarUint(buf, r.Length)
		}
	}
	return buf
}
