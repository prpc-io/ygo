package doc_test

import (
	"sync/atomic"
	"testing"

	"github.com/Deln0r/ygo/internal/doc"
)

// TestAfterTransaction_FiresOnce verifies a registered handler fires
// exactly once per Commit, in the order registered.
func TestAfterTransaction_FiresOnce(t *testing.T) {
	d := doc.NewDoc()
	var seen int32
	unsub := d.OnAfterTransaction(func(*doc.TransactionMut) {
		atomic.AddInt32(&seen, 1)
	})
	defer unsub()

	for i := 0; i < 3; i++ {
		txn := d.WriteTxn()
		txn.Commit()
	}
	if got := atomic.LoadInt32(&seen); got != 3 {
		t.Fatalf("handler fired %d times, want 3", got)
	}
}

// TestAfterTransaction_Order verifies handlers run in registration
// order even when multiple are registered.
func TestAfterTransaction_Order(t *testing.T) {
	d := doc.NewDoc()
	var order []int
	unsubA := d.OnAfterTransaction(func(*doc.TransactionMut) {
		order = append(order, 1)
	})
	defer unsubA()
	unsubB := d.OnAfterTransaction(func(*doc.TransactionMut) {
		order = append(order, 2)
	})
	defer unsubB()
	unsubC := d.OnAfterTransaction(func(*doc.TransactionMut) {
		order = append(order, 3)
	})
	defer unsubC()

	txn := d.WriteTxn()
	txn.Commit()

	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("handler order = %v, want [1 2 3]", order)
	}
}

// TestAfterTransaction_Unsubscribe verifies unsubscribe removes the
// handler so it no longer fires on subsequent commits.
func TestAfterTransaction_Unsubscribe(t *testing.T) {
	d := doc.NewDoc()
	var fired int32
	unsub := d.OnAfterTransaction(func(*doc.TransactionMut) {
		atomic.AddInt32(&fired, 1)
	})

	txn := d.WriteTxn()
	txn.Commit()
	unsub()
	txn2 := d.WriteTxn()
	txn2.Commit()

	if got := atomic.LoadInt32(&fired); got != 1 {
		t.Fatalf("handler fired %d times after unsubscribe; want 1", got)
	}

	// Double-unsubscribe is a no-op.
	unsub()
}

// TestAfterTransaction_SeesTxn verifies the handler receives the
// committed *TransactionMut and can read Origin from it.
func TestAfterTransaction_SeesTxn(t *testing.T) {
	d := doc.NewDoc()
	var gotOrigin any
	unsub := d.OnAfterTransaction(func(mut *doc.TransactionMut) {
		gotOrigin = mut.Origin
	})
	defer unsub()

	txn := d.WriteTxn()
	txn.Origin = "test-origin"
	txn.Commit()

	if gotOrigin != "test-origin" {
		t.Fatalf("handler saw origin %v, want test-origin", gotOrigin)
	}
}
