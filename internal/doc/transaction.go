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

	// deletedIDs records items tombstoned during this transaction
	// via Delete. Used at Commit time (not yet) to build the wire
	// DeleteSet and drive squash of adjacent deleted runs. Read by
	// DeletedIDs accessor for tests and future observer dispatch.
	deletedIDs []block.ID

	// changedTypes records branches whose user-observable state
	// changed during this transaction via AddChangedType. Drives
	// observer dispatch at Commit (not yet implemented). Read by
	// ChangedTypes accessor.
	changedTypes map[*block.Branch]struct{}

	// mergeBlocks would accumulate item IDs that should be
	// considered for try_squash at Commit. Will be added back when
	// Item.Integrate gains a MarkForMerge call site and Commit
	// gains a squash pass; both deferred (see tech-debt.md).
	// Field intentionally absent for now to keep the type lint-clean.
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

// IntegrateContext implementation. These methods make TransactionMut
// satisfy block.IntegrateContext so Item.Integrate can route store
// access and change-tracking through the active transaction.

// GetItem looks up the Item containing id in the doc's BlockStore.
// Returns nil for GC cells or unknown IDs.
func (t *TransactionMut) GetItem(id block.ID) *block.Item {
	return t.doc.store.GetItem(id)
}

// MaterializeCleanStart returns an Item starting exactly at id.Clock,
// splitting the underlying block in the store if id lands mid-block.
// Returns nil if id is in a GC cell or unknown.
func (t *TransactionMut) MaterializeCleanStart(id block.ID) *block.Item {
	slc, ok := t.doc.store.GetItemCleanStart(id)
	if !ok {
		return nil
	}
	return t.doc.store.Materialize(slc)
}

// MaterializeCleanEnd returns an Item ending exactly at id.Clock
// (inclusive), splitting if needed.
func (t *TransactionMut) MaterializeCleanEnd(id block.ID) *block.Item {
	slc, ok := t.doc.store.GetItemCleanEnd(id)
	if !ok {
		return nil
	}
	return t.doc.store.Materialize(slc)
}

// Delete tombstones an Item and records its ID for inclusion in the
// transaction's eventual delete-set emission. The Item must already
// be in the store.
//
// Note: the recursive-delete-of-Type-children path is not yet
// implemented (tracked in tech-debt.md). This implementation handles
// the simple case integrate uses for map-key LWW tombstoning.
func (t *TransactionMut) Delete(item *block.Item) {
	if item == nil || item.IsDeleted() {
		return
	}
	item.SetDeleted(true)
	t.deletedIDs = append(t.deletedIDs, item.ID)
	if item.Parent.IsResolved() {
		// Tombstoning subtracts from the parent's countable totals,
		// mirroring yrs's branch.block_len -= len adjustment.
		if item.IsCountable() && item.ParentSub == nil {
			parent := item.Parent.Branch
			if item.Len <= parent.BlockLen {
				parent.BlockLen -= item.Len
			}
			if item.Len <= parent.ContentLen {
				parent.ContentLen -= item.Len
			}
		}
	}
}

// AddChangedType records that a Branch (with optional map-key
// discriminator) saw user-observable changes during this
// transaction. Drives observer dispatch at Commit.
//
// Currently records only the Branch pointer; the map-key dimension
// is dropped because the observer subsystem does not yet consume it.
// See tech-debt.md.
func (t *TransactionMut) AddChangedType(parent *block.Branch, parentSub *string) {
	if parent == nil {
		return
	}
	if t.changedTypes == nil {
		t.changedTypes = map[*block.Branch]struct{}{}
	}
	t.changedTypes[parent] = struct{}{}
	_ = parentSub // intentionally dropped until observer subsystem lands
}

// GetOrCreateBranch returns the root branch with the given name from
// the doc's root-branch registry. Used by block.Repair to resolve
// ParentNamed references arriving from wire updates.
//
// We do not call Doc.Branch here because Doc.Branch acquires the
// write lock, which we already hold inside this transaction. Touch
// the registry directly under the existing lock instead.
func (t *TransactionMut) GetOrCreateBranch(name string) *block.Branch {
	if b, ok := t.doc.rootBranches[name]; ok {
		return b
	}
	b := &block.Branch{Name: name, Map: map[string]*block.Item{}}
	t.doc.rootBranches[name] = b
	return b
}

// DeletedIDs returns the IDs of items tombstoned during this
// transaction so far. Returned slice aliases internal state; do not
// mutate. Primarily for tests and the future delete-set emitter.
func (t *TransactionMut) DeletedIDs() []block.ID { return t.deletedIDs }

// ChangedTypes returns the branches with recorded changes in this
// transaction. Order is non-deterministic (map iteration). Primarily
// for tests and the future observer dispatcher.
func (t *TransactionMut) ChangedTypes() []*block.Branch {
	out := make([]*block.Branch, 0, len(t.changedTypes))
	for b := range t.changedTypes {
		out = append(out, b)
	}
	return out
}

// Compile-time check that TransactionMut satisfies the
// block.IntegrateContext interface. If this line stops compiling,
// the integrate-context contract has shifted.
var _ block.IntegrateContext = (*TransactionMut)(nil)
