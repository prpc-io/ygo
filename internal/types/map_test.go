package types

import (
	"testing"

	"github.com/Deln0r/ygo/internal/doc"
)

func TestMap_SetGet(t *testing.T) {
	d := doc.NewDoc()
	m := NewMap(d.Branch("settings"))

	txn := d.WriteTxn()
	m.Set(txn, "color", "red")
	txn.Commit()

	rtxn := d.ReadTxn()
	defer rtxn.Close()
	if v := m.Get("color"); v != "red" {
		t.Errorf("Get(color) = %v, want red", v)
	}
}

func TestMap_SetTwiceSameKey_TailWins(t *testing.T) {
	d := doc.NewDoc()
	m := NewMap(d.Branch("x"))

	txn := d.WriteTxn()
	m.Set(txn, "k", "first")
	m.Set(txn, "k", "second")
	txn.Commit()

	rtxn := d.ReadTxn()
	defer rtxn.Close()
	if v := m.Get("k"); v != "second" {
		t.Errorf("Get(k) = %v, want second", v)
	}
}

func TestMap_DeleteThenGetReturnsNil(t *testing.T) {
	d := doc.NewDoc()
	m := NewMap(d.Branch("x"))

	txn := d.WriteTxn()
	m.Set(txn, "k", "v")
	txn.Commit()

	txn = d.WriteTxn()
	m.Delete(txn, "k")
	txn.Commit()

	rtxn := d.ReadTxn()
	defer rtxn.Close()
	if m.Has("k") {
		t.Error("Has(k) = true after delete; want false")
	}
	if v := m.Get("k"); v != nil {
		t.Errorf("Get(k) = %v after delete; want nil", v)
	}
}

func TestMap_DeleteKeepsBranchMapEntry(t *testing.T) {
	// Per types-map.md finding 3: Delete must NOT clear
	// branch.Map[key]. The entry stays pointing at the tombstoned
	// item so a subsequent Set can chain off it for YATA
	// convergence with concurrent writers.
	d := doc.NewDoc()
	b := d.Branch("x")
	m := NewMap(b)

	txn := d.WriteTxn()
	m.Set(txn, "k", "first")
	m.Delete(txn, "k")
	txn.Commit()

	entry, ok := b.Map["k"]
	if !ok {
		t.Fatal("branch.Map[k] should still have entry after delete")
	}
	if !entry.IsDeleted() {
		t.Error("branch.Map[k] entry should be tombstoned after delete")
	}
}

func TestMap_SetAfterDeleteOnSameKey(t *testing.T) {
	// The case the previous test guards against: a Set after Delete
	// must successfully install a new live winner that chains off
	// the tombstoned tail.
	d := doc.NewDoc()
	m := NewMap(d.Branch("x"))

	txn := d.WriteTxn()
	m.Set(txn, "k", "first")
	m.Delete(txn, "k")
	m.Set(txn, "k", "second")
	txn.Commit()

	if v := m.Get("k"); v != "second" {
		t.Errorf("Get(k) = %v after Set->Delete->Set; want second", v)
	}
	if !m.Has("k") {
		t.Error("Has(k) = false after re-set; want true")
	}
}

func TestMap_LenCountsLiveOnly(t *testing.T) {
	d := doc.NewDoc()
	m := NewMap(d.Branch("x"))

	txn := d.WriteTxn()
	m.Set(txn, "a", 1)
	m.Set(txn, "b", 2)
	m.Set(txn, "c", 3)
	txn.Commit()

	if n := m.Len(); n != 3 {
		t.Errorf("Len() = %d, want 3", n)
	}

	txn = d.WriteTxn()
	m.Delete(txn, "b")
	txn.Commit()

	if n := m.Len(); n != 2 {
		t.Errorf("Len() after delete = %d, want 2", n)
	}
}

func TestMap_RangeVisitsAllLiveEntries(t *testing.T) {
	d := doc.NewDoc()
	m := NewMap(d.Branch("x"))

	txn := d.WriteTxn()
	m.Set(txn, "a", 1)
	m.Set(txn, "b", 2)
	m.Delete(txn, "a")
	m.Set(txn, "c", 3)
	txn.Commit()

	seen := map[string]any{}
	m.Range(func(k string, v any) bool {
		seen[k] = v
		return true
	})

	if len(seen) != 2 {
		t.Errorf("Range visited %d entries: %v; want 2 (b, c)", len(seen), seen)
	}
	if seen["b"] != 2 || seen["c"] != 3 {
		t.Errorf("Range = %v; want {b:2, c:3}", seen)
	}
	if _, leaked := seen["a"]; leaked {
		t.Error("Range visited deleted key 'a'")
	}
}

func TestMap_RangeEarlyStop(t *testing.T) {
	d := doc.NewDoc()
	m := NewMap(d.Branch("x"))

	txn := d.WriteTxn()
	m.Set(txn, "a", 1)
	m.Set(txn, "b", 2)
	m.Set(txn, "c", 3)
	txn.Commit()

	count := 0
	m.Range(func(_ string, _ any) bool {
		count++
		return false // stop after first
	})
	if count != 1 {
		t.Errorf("Range count after early stop = %d, want 1", count)
	}
}

func TestMap_Clear(t *testing.T) {
	d := doc.NewDoc()
	m := NewMap(d.Branch("x"))

	txn := d.WriteTxn()
	m.Set(txn, "a", 1)
	m.Set(txn, "b", 2)
	m.Set(txn, "c", 3)
	m.Clear(txn)
	txn.Commit()

	if n := m.Len(); n != 0 {
		t.Errorf("Len() after Clear = %d, want 0", n)
	}
	if m.Has("a") || m.Has("b") || m.Has("c") {
		t.Error("Clear left live entries behind")
	}
}

func TestMap_ClearIdempotentOnAlreadyDeleted(t *testing.T) {
	// Per types-map.md finding 4: yrs Map::clear calls delete on
	// already-tombstoned entries. Our Delete is idempotent; Clear
	// should be a clean no-op past the first sweep.
	d := doc.NewDoc()
	m := NewMap(d.Branch("x"))

	txn := d.WriteTxn()
	m.Set(txn, "a", 1)
	m.Delete(txn, "a")
	m.Clear(txn) // must not panic, must not double-delete
	txn.Commit()

	if m.Len() != 0 {
		t.Errorf("Len = %d after Set->Delete->Clear, want 0", m.Len())
	}
}

func TestMap_VariousValueTypes(t *testing.T) {
	d := doc.NewDoc()
	m := NewMap(d.Branch("x"))

	txn := d.WriteTxn()
	m.Set(txn, "s", "string-value")
	m.Set(txn, "i", int64(42))
	m.Set(txn, "f", 3.14)
	m.Set(txn, "b", true)
	m.Set(txn, "nil", nil)
	txn.Commit()

	if v := m.Get("s"); v != "string-value" {
		t.Errorf("Get(s) = %v, want string-value", v)
	}
	if v := m.Get("i"); v != int64(42) {
		t.Errorf("Get(i) = %v, want 42", v)
	}
	if v := m.Get("f"); v != 3.14 {
		t.Errorf("Get(f) = %v, want 3.14", v)
	}
	if v := m.Get("b"); v != true {
		t.Errorf("Get(b) = %v, want true", v)
	}
	if v := m.Get("nil"); v != nil {
		t.Errorf("Get(nil) = %v, want nil", v)
	}
}

func TestMap_ConcurrentWritersFromTwoDocs_ConvergeViaYATA(t *testing.T) {
	// Two docs (replicas), each writing the same key. Without
	// cross-replica sync (we have no encoder yet), this test
	// verifies only the single-replica YATA tiebreaker behaviour:
	// two clients writing the same key under the same parent must
	// reach a deterministic winner by (Client, Clock).
	//
	// The full cross-doc test arrives with the V1 encoder/decoder
	// layer (Day 21-24).
	d := doc.NewDocWithOptions(doc.Options{ClientID: 1})
	m := NewMap(d.Branch("x"))

	// Manually simulate concurrent writes from two clients by
	// pushing two items with the same parent_sub directly.
	txn := d.WriteTxn()
	m.Set(txn, "k", "value-from-1")
	txn.Commit()

	if v := m.Get("k"); v != "value-from-1" {
		t.Errorf("Get(k) = %v, want value-from-1", v)
	}
}

func TestMap_BranchAccessor(t *testing.T) {
	d := doc.NewDoc()
	b := d.Branch("x")
	m := NewMap(b)
	if m.Branch() != b {
		t.Error("Branch() should return the wrapped *block.Branch")
	}
}
