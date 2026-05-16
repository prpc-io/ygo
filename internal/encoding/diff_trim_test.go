package encoding

import (
	"testing"

	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/store"
	"github.com/Deln0r/ygo/internal/types"
)

// TestEncodeDiff_SliceTrim_DropsFullyKnownClients verifies that
// EncodeDiff with remoteSV that fully covers a client emits zero
// blocks for that client (slice-trim skip).
//
// Setup: clientA writes 5 ops, snapshot, clientA writes 5 more
// ops. Then encode diff against snapshot SV — only the second 5
// ops should be emitted, NOT all 10.
func TestEncodeDiff_SliceTrim_DropsFullyKnownClients(t *testing.T) {
	src := doc.NewDocWithOptions(doc.Options{ClientID: 1})
	m := types.NewMap(src.Branch("kv"))

	txn := src.WriteTxn()
	for i := 0; i < 5; i++ {
		m.Set(txn, "k"+string(rune('a'+i)), int64(i))
	}
	txn.Commit()

	// Snapshot SV — represents "what remote has".
	rtxn := src.ReadTxn()
	snapshot := rtxn.Store().GetStateVector()
	rtxn.Close()

	// Add 5 more ops.
	txn = src.WriteTxn()
	for i := 5; i < 10; i++ {
		m.Set(txn, "k"+string(rune('a'+i)), int64(i))
	}
	txn.Commit()

	// Encode full state vs incremental diff.
	full := EncodeStateAsUpdate(src)

	rtxn = src.ReadTxn()
	diff := EncodeDiff(src, rtxn, snapshot)
	rtxn.Close()

	if len(diff) >= len(full) {
		t.Errorf("slice-trim ineffective: diff=%d bytes, full=%d bytes", len(diff), len(full))
	}
	// Diff should be roughly half-ish (5 ops vs 10), not the full payload.
	if float64(len(diff)) > 0.6*float64(len(full)) {
		t.Errorf("slice-trim too weak: diff=%d bytes vs full=%d bytes; expected <60%% of full",
			len(diff), len(full))
	}

	// Apply diff to a peer that already has the snapshot state — must converge.
	_ = full
	snapBytes := buildSnapshotUpdate(t, src, snapshot)
	dst := doc.NewDocWithOptions(doc.Options{ClientID: 2})
	dstMap := types.NewMap(dst.Branch("kv"))
	if err := ApplyUpdate(dst, snapBytes); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}
	if err := ApplyUpdate(dst, diff); err != nil {
		t.Fatalf("apply diff: %v", err)
	}
	for i := 0; i < 10; i++ {
		key := "k" + string(rune('a'+i))
		if got := dstMap.Get(key); got != int64(i) {
			t.Errorf("dst[%q] = %v, want %d", key, got, i)
		}
	}
}

// TestEncodeDiff_SliceTrim_EmptyRemoteFullEmit verifies that diff
// against an empty SV (zero remote knowledge) is byte-identical
// to a full state encode — trim must NOT trigger when remoteSV is
// empty.
func TestEncodeDiff_SliceTrim_EmptyRemoteFullEmit(t *testing.T) {
	src := doc.NewDocWithOptions(doc.Options{ClientID: 1})
	m := types.NewMap(src.Branch("kv"))
	txn := src.WriteTxn()
	for i := 0; i < 3; i++ {
		m.Set(txn, "k"+string(rune('a'+i)), int64(i))
	}
	txn.Commit()

	full := EncodeStateAsUpdate(src)

	rtxn := src.ReadTxn()
	diffEmpty := EncodeDiff(src, rtxn, nil)
	rtxn.Close()

	if string(full) != string(diffEmpty) {
		t.Errorf("diff vs nil SV should equal full encode:\nfull=% x\ndiff=% x", full, diffEmpty)
	}
}

// TestEncodeDiffV2_SliceTrim mirrors the V1 test for V2 path.
func TestEncodeDiffV2_SliceTrim(t *testing.T) {
	src := doc.NewDocWithOptions(doc.Options{ClientID: 1})
	m := types.NewMap(src.Branch("kv"))
	txn := src.WriteTxn()
	for i := 0; i < 5; i++ {
		m.Set(txn, "k"+string(rune('a'+i)), int64(i))
	}
	txn.Commit()

	rtxn := src.ReadTxn()
	snapshot := rtxn.Store().GetStateVector()
	rtxn.Close()

	txn = src.WriteTxn()
	for i := 5; i < 10; i++ {
		m.Set(txn, "k"+string(rune('a'+i)), int64(i))
	}
	txn.Commit()

	full := EncodeStateAsUpdateV2(src)

	rtxn = src.ReadTxn()
	diff := EncodeDiffV2(src, rtxn, snapshot)
	rtxn.Close()

	if len(diff) >= len(full) {
		t.Errorf("V2 slice-trim ineffective: diff=%d bytes, full=%d bytes", len(diff), len(full))
	}
}

// buildSnapshotUpdate encodes a doc's state as of the given SV
// (= as if we wanted to emit "everything up to the snapshot
// boundary"). Used by the slice-trim test to set up a dst that
// has only the pre-snapshot state.
//
// Approach: open a fresh Doc, apply same ops as src would up to
// snapshot clock. Since this is a test helper, simplest is to
// re-build the pre-snapshot state by applying a full encode of a
// separate "snapshot-frozen" doc. For this test scenario where
// src wrote 5 then 5 more keys, we cheat: re-do the first 5 ops
// on a stand-alone doc with the same clientID and encode that.
func buildSnapshotUpdate(t *testing.T, _ *doc.Doc, snapshot store.StateVector) []byte {
	t.Helper()
	// Reconstruct: ClientID 1, 5 ops with same keys.
	d := doc.NewDocWithOptions(doc.Options{ClientID: 1})
	m := types.NewMap(d.Branch("kv"))
	txn := d.WriteTxn()
	for i := 0; i < 5; i++ {
		m.Set(txn, "k"+string(rune('a'+i)), int64(i))
	}
	txn.Commit()
	// Sanity: the reconstructed doc's SV must match the snapshot SV
	// (same clientID + same ops = same clocks).
	rtxn := d.ReadTxn()
	reconSV := rtxn.Store().GetStateVector()
	rtxn.Close()
	for c, clk := range snapshot {
		if reconSV[c] != clk {
			t.Fatalf("snapshot reconstruction divergence: client %d snapshot=%d recon=%d",
				c, clk, reconSV[c])
		}
	}
	return EncodeStateAsUpdate(d)
}
