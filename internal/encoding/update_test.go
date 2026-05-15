package encoding

import (
	"testing"

	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/types"
)

func TestEncodeStateAsUpdate_EmptyDoc(t *testing.T) {
	d := doc.NewDoc()
	buf := EncodeStateAsUpdate(d)
	// Empty doc → 0 client runs + 0 IdSet entries = two zero bytes.
	want := []byte{0x00, 0x00}
	if string(buf) != string(want) {
		t.Errorf("got % x, want % x", buf, want)
	}
}

func TestUpdate_RoundTrip_Bytes(t *testing.T) {
	src := doc.NewDocWithOptions(doc.Options{ClientID: 42})
	m := types.NewMap(src.Branch("settings"))

	txn := src.WriteTxn()
	m.Set(txn, "color", "red")
	m.Set(txn, "lang", "go")
	txn.Commit()

	bytes1 := EncodeStateAsUpdate(src)
	u, tail, err := DecodeUpdate(bytes1)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(tail) != 0 {
		t.Errorf("trailing bytes after decode: % x", tail)
	}

	// Verify the decoded update has one client (42) with two items.
	if len(u.Blocks) != 1 {
		t.Fatalf("Blocks has %d clients, want 1", len(u.Blocks))
	}
	blocks, ok := u.Blocks[42]
	if !ok {
		t.Fatalf("Blocks[42] missing; have %v", u.Blocks)
	}
	if len(blocks) != 2 {
		t.Errorf("client 42 has %d blocks, want 2", len(blocks))
	}
	for i, b := range blocks {
		if b.Kind != WireBlockItem {
			t.Errorf("block %d kind = %d, want WireBlockItem", i, b.Kind)
		}
	}
}

func TestUpdate_TwoDocConvergence_StringValues(t *testing.T) {
	// The actual milestone of Phase B: src writes Map values, encode
	// to bytes, decode + apply on a fresh dst, dst reads same values.
	src := doc.NewDocWithOptions(doc.Options{ClientID: 7})
	srcMap := types.NewMap(src.Branch("settings"))

	txn := src.WriteTxn()
	srcMap.Set(txn, "color", "red")
	srcMap.Set(txn, "lang", "go")
	srcMap.Set(txn, "version", int64(1))
	srcMap.Set(txn, "stable", true)
	txn.Commit()

	// Encode src → wire bytes.
	bytes := EncodeStateAsUpdate(src)

	// Fresh destination doc.
	dst := doc.NewDocWithOptions(doc.Options{ClientID: 99})
	dstMap := types.NewMap(dst.Branch("settings"))

	// Decode + apply.
	u, _, err := DecodeUpdate(bytes)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	dstTxn := dst.WriteTxn()
	if err := u.Apply(dstTxn); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dstTxn.Commit()

	// Read back from dst — should match src exactly.
	if got := dstMap.Get("color"); got != "red" {
		t.Errorf("dst color = %v, want red", got)
	}
	if got := dstMap.Get("lang"); got != "go" {
		t.Errorf("dst lang = %v, want go", got)
	}
	if got := dstMap.Get("version"); got != int64(1) {
		t.Errorf("dst version = %v, want 1", got)
	}
	if got := dstMap.Get("stable"); got != true {
		t.Errorf("dst stable = %v, want true", got)
	}
}

func TestUpdate_TwoDocConvergence_DeleteSet(t *testing.T) {
	src := doc.NewDocWithOptions(doc.Options{ClientID: 11})
	srcMap := types.NewMap(src.Branch("x"))

	txn := src.WriteTxn()
	srcMap.Set(txn, "a", "first")
	srcMap.Set(txn, "b", "second")
	srcMap.Delete(txn, "a") // tombstones the "a" entry
	txn.Commit()

	bytes := EncodeStateAsUpdate(src)

	dst := doc.NewDocWithOptions(doc.Options{ClientID: 99})
	dstMap := types.NewMap(dst.Branch("x"))

	u, _, err := DecodeUpdate(bytes)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	dstTxn := dst.WriteTxn()
	if err := u.Apply(dstTxn); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dstTxn.Commit()

	if dstMap.Has("a") {
		t.Error("dst still has 'a' after delete-set apply")
	}
	if got := dstMap.Get("b"); got != "second" {
		t.Errorf("dst b = %v, want second", got)
	}
}

func TestUpdate_RoundTrip_OverwriteSameKey(t *testing.T) {
	src := doc.NewDocWithOptions(doc.Options{ClientID: 5})
	srcMap := types.NewMap(src.Branch("x"))

	txn := src.WriteTxn()
	srcMap.Set(txn, "k", "v1")
	srcMap.Set(txn, "k", "v2") // overwrites; v1 becomes tombstoned predecessor
	srcMap.Set(txn, "k", "v3")
	txn.Commit()

	bytes := EncodeStateAsUpdate(src)
	dst := doc.NewDocWithOptions(doc.Options{ClientID: 99})
	dstMap := types.NewMap(dst.Branch("x"))
	u, _, err := DecodeUpdate(bytes)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	dstTxn := dst.WriteTxn()
	if err := u.Apply(dstTxn); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dstTxn.Commit()

	if got := dstMap.Get("k"); got != "v3" {
		t.Errorf("dst k = %v, want v3 (last write)", got)
	}
}

func TestApply_IdempotentOnAlreadyKnownItems(t *testing.T) {
	// Apply the same update twice: second apply should be a no-op
	// (Contains check skips already-integrated items).
	src := doc.NewDocWithOptions(doc.Options{ClientID: 7})
	srcMap := types.NewMap(src.Branch("x"))
	txn := src.WriteTxn()
	srcMap.Set(txn, "a", "1")
	txn.Commit()

	bytes := EncodeStateAsUpdate(src)

	dst := doc.NewDoc()
	dstMap := types.NewMap(dst.Branch("x"))

	for i := 0; i < 2; i++ {
		u, _, err := DecodeUpdate(bytes)
		if err != nil {
			t.Fatalf("decode iter %d: %v", i, err)
		}
		dstTxn := dst.WriteTxn()
		if err := u.Apply(dstTxn); err != nil {
			t.Fatalf("apply iter %d: %v", i, err)
		}
		dstTxn.Commit()
	}

	if got := dstMap.Get("a"); got != "1" {
		t.Errorf("dst a = %v, want 1 (idempotent apply)", got)
	}
}
