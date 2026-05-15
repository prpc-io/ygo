package doc

import (
	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/store"
)

// Transaction is a read-only transaction holding the doc's read lock
// for its lifetime. Created by Doc.ReadTxn; released by Close.
//
// Mirrors yrs Transaction<'doc>. The lock is held until Close runs;
// Go has no Drop trait, so Close must be called explicitly. Use a
// `defer txn.Close()` immediately after acquisition.
//
// A Transaction value must not be retained past its Close call. yrs
// enforces this with a 'doc lifetime parameter; we document the
// contract and trust callers (see tech-debt.md).
type Transaction struct {
	doc    *Doc
	closed bool
}

// ReadTxn acquires the doc's read lock and returns a Transaction. The
// caller MUST call Close (typically via defer) to release the lock.
//
// Multiple read transactions may coexist; they block any concurrent
// WriteTxn until all read transactions close.
func (d *Doc) ReadTxn() *Transaction {
	d.mu.RLock()
	return &Transaction{doc: d}
}

// Close releases the read lock. Safe to call more than once; only the
// first call unlocks.
func (t *Transaction) Close() {
	if t.closed {
		return
	}
	t.closed = true
	t.doc.mu.RUnlock()
}

// Doc returns the Doc this transaction was created from.
func (t *Transaction) Doc() *Doc { return t.doc }

// Store returns the doc's BlockStore for read access. Mutations
// through this pointer would race with WriteTxn writers; do not
// mutate from within a read transaction.
func (t *Transaction) Store() *store.BlockStore { return t.doc.store }

// TransactionMut is a write transaction holding the doc's write lock
// for its lifetime. Created by Doc.WriteTxn; released by Commit.
//
// Mirrors yrs TransactionMut<'doc>. Accumulates change-tracking state
// during the transaction; consumes it at Commit time to run the
// post-commit lifecycle (squash, GC, observers, update emission).
//
// Most lifecycle steps are not yet implemented — see tech-debt.md.
// Commit currently only releases the lock and marks the txn closed.
type TransactionMut struct {
	doc    *Doc
	closed bool

	// Origin is an opaque caller-supplied value attached to this
	// transaction. Mirrors yrs Origin (transaction.rs:1210-1288).
	// Visible in observer events to distinguish e.g. local edits
	// from updates applied via ApplyUpdate.
	Origin any

	// mergeBlocks accumulates IDs of items that should be considered
	// for try_squash with their left neighbour at Commit time.
	// Populated by Item.Integrate as items land; consumed by squash
	// in Commit. Currently unused (no Integrate, no squash).
	mergeBlocks []block.ID

	// deletedIDs records items tombstoned during this transaction.
	// Used to build the DeleteSet emitted with the update event,
	// and to drive squash of adjacent deleted runs.
	// Currently unused (no DeleteSet, no update emit).
	deletedIDs []block.ID

	// changedTypes records branches whose user-observable state
	// changed during this transaction. Drives observer dispatch at
	// Commit. Currently unused (no observers, Branch is a stub).
	changedTypes map[*block.Branch]struct{}
}

// WriteTxn acquires the doc's write lock and returns a TransactionMut.
// The caller MUST call Commit (typically via defer) to release the
// lock and run the commit lifecycle.
//
// WriteTxn blocks until all concurrent ReadTxn / WriteTxn close.
// Calling WriteTxn while already holding a write lock on this Doc
// from the same goroutine deadlocks (Go RWMutex is not re-entrant),
// matching yrs's transact_mut behaviour (transact.rs:255 explicit
// "this will hang forever" comment).
func (d *Doc) WriteTxn() *TransactionMut {
	d.mu.Lock()
	return &TransactionMut{doc: d}
}

// Commit runs the post-commit lifecycle and releases the write lock.
// Safe to call more than once; subsequent calls are no-ops.
//
// Lifecycle steps (mostly stubbed today; tech-debt.md tracks each):
//  1. Squash mergeBlocks against their left neighbours.
//  2. GC fully-observed deleted items if Doc.GC is true.
//  3. Fire pre-emit observers on changedTypes.
//  4. Emit the update event with V1 (or V2) bytes for the diff.
//  5. Emit subdoc events.
//  6. Fire after-commit observers.
//
// Today: only step 0 (release the lock) runs.
func (t *TransactionMut) Commit() {
	if t.closed {
		return
	}
	t.closed = true
	// TODO: lifecycle steps 1-6.
	t.doc.mu.Unlock()
}

// Doc returns the Doc this transaction was created from.
func (t *TransactionMut) Doc() *Doc { return t.doc }

// Store returns the doc's BlockStore. Safe to mutate from within
// this transaction; the write lock prevents concurrent access.
func (t *TransactionMut) Store() *store.BlockStore { return t.doc.store }
