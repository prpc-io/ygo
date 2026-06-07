package doc_test

import (
	"testing"

	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/store"
)

// TestTransactionMut_BeforeAfterState_EmptyDoc verifies that on a
// fresh doc both snapshots are empty.
func TestTransactionMut_BeforeAfterState_EmptyDoc(t *testing.T) {
	d := doc.NewDoc()
	txn := d.WriteTxn()
	before := txn.BeforeState()
	if len(before) != 0 {
		t.Errorf("BeforeState on empty doc has %d entries, want 0", len(before))
	}
	txn.Commit()
	after := txn.AfterState()
	if len(after) != 0 {
		t.Errorf("AfterState on no-op commit has %d entries, want 0", len(after))
	}
}

// TestTransactionMut_AfterState_ReflectsInsertions verifies that
// AfterState picks up clock advances for items inserted during the
// transaction.
func TestTransactionMut_AfterState_ReflectsInsertions(t *testing.T) {
	d := doc.NewDoc()
	clientID := d.ClientID()

	txn := d.WriteTxn()
	// Push three items via Store; this is the closest doc-package
	// black-box equivalent of "the transaction inserted three blocks
	// from this client". Higher layers (Map.Set, etc.) drive the
	// same store under the hood.
	for i := uint64(0); i < 3; i++ {
		it := &block.Item{
			ID:  block.ID{Client: clientID, Clock: i},
			Len: 1,
		}
		txn.Store().PushBlock(it)
	}
	txn.Commit()

	after := txn.AfterState()
	if after[clientID] != 3 {
		t.Errorf("AfterState[client] = %d, want 3", after[clientID])
	}
	before := txn.BeforeState()
	if before[clientID] != 0 {
		t.Errorf("BeforeState[client] = %d, want 0", before[clientID])
	}
}

// TestTransactionMut_BeforeState_PicksUpPriorTxn verifies that two
// sequential WriteTxns see the first one's writes reflected in the
// second's BeforeState.
func TestTransactionMut_BeforeState_PicksUpPriorTxn(t *testing.T) {
	d := doc.NewDoc()
	clientID := d.ClientID()

	txn1 := d.WriteTxn()
	txn1.Store().PushBlock(&block.Item{
		ID:  block.ID{Client: clientID, Clock: 0},
		Len: 5,
	})
	txn1.Commit()

	txn2 := d.WriteTxn()
	defer txn2.Commit()
	if got := txn2.BeforeState()[clientID]; got != 5 {
		t.Errorf("BeforeState on second txn = %d, want 5", got)
	}
}

// TestTransactionMut_AfterStateSeenInHandler verifies a registered
// AfterTransaction handler observes the post-mutation afterState.
func TestTransactionMut_AfterStateSeenInHandler(t *testing.T) {
	d := doc.NewDoc()
	clientID := d.ClientID()
	var observed store.StateVector
	unsub := d.OnAfterTransaction(func(mut *doc.TransactionMut) {
		observed = mut.AfterState()
	})
	defer unsub()

	txn := d.WriteTxn()
	txn.Store().PushBlock(&block.Item{
		ID:  block.ID{Client: clientID, Clock: 0},
		Len: 2,
	})
	txn.Commit()

	if observed[clientID] != 2 {
		t.Errorf("handler saw AfterState[client] = %d, want 2", observed[clientID])
	}
}
