package undo

import (
	"sync"
	"time"

	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
)

// DefaultCaptureTimeout groups subsequent edits into the same StackItem
// when they land within this window of the previous capture. Matches
// the upstream yjs default of 500 milliseconds.
const DefaultCaptureTimeout = 500 * time.Millisecond

// Options configures an UndoManager. All fields are optional; a zero
// value is equivalent to passing no Options at all.
type Options struct {
	// CaptureTimeout groups bursty edits into a single StackItem.
	// Zero means "use DefaultCaptureTimeout"; a negative value
	// disables grouping (every transaction becomes its own
	// StackItem).
	CaptureTimeout time.Duration

	// TrackedOrigins is the set of TransactionMut.Origin values
	// that qualify as "this UndoManager should record this edit".
	// nil means default-track-local: a single entry of the untyped
	// nil origin (matching the default a local edit produces).
	TrackedOrigins map[any]struct{}

	// IgnoreRemoteMapChanges, when true, suppresses StackItem
	// capture for transactions whose only effect on the scope is
	// a Map operation overwriting a key from a non-tracked origin.
	// Off by default; the implementation hook lands with full
	// nested-type support in a follow-up.
	IgnoreRemoteMapChanges bool
}

// UndoManager records local mutations under a scope of root branches
// and replays them on Undo / Redo. See package doc for semantics.
//
// An UndoManager is safe for concurrent reads of CanUndo / CanRedo;
// Undo, Redo, StopCapturing, and Clear must be serialised by the
// caller (mirroring the Doc write-lock contract).
type UndoManager struct {
	doc *doc.Doc

	// scope is the set of root branches whose mutations qualify.
	// Nested-type ancestry walks up Branch.Item.Parent to find a
	// matching scope entry.
	scope []*block.Branch

	captureTimeout         time.Duration
	trackedOrigins         map[any]struct{}
	ignoreRemoteMapChanges bool //nolint:unused // wired in nested-type follow-up

	mu        sync.Mutex
	undoStack []*StackItem
	redoStack []*StackItem

	// undoing / redoing flip true while Undo or Redo runs an
	// internally-issued transaction; the AfterTransaction handler
	// uses these flags to route the new StackItem to the opposite
	// stack instead of the usual undoStack.
	undoing bool
	redoing bool

	// lastChange is the wall-clock time of the previous captured
	// transaction. Used together with captureTimeout to decide
	// whether the next transaction extends the top StackItem or
	// opens a new one. Zero means "no prior change".
	lastChange time.Time

	// nowFn returns the current wall-clock time. Indirected so tests
	// can drive grouping deterministically without sleeping.
	nowFn func() time.Time

	unsubscribe func()
	closed      bool
}

// NewUndoManager registers an UndoManager on doc, watching the given
// scope of root branches. Returns the manager; call Close when done
// to unregister the handler.
//
// Panics if scope is empty (a no-scope UndoManager would silently
// drop every transaction; almost always a programming error).
func NewUndoManager(d *doc.Doc, scope []*block.Branch, opts ...Options) *UndoManager {
	if len(scope) == 0 {
		panic("undo: NewUndoManager requires at least one scope branch")
	}
	var opt Options
	if len(opts) > 0 {
		opt = opts[0]
	}

	captureTimeout := opt.CaptureTimeout
	if captureTimeout == 0 {
		captureTimeout = DefaultCaptureTimeout
	}

	trackedOrigins := opt.TrackedOrigins
	if trackedOrigins == nil {
		trackedOrigins = map[any]struct{}{nil: {}}
	}

	um := &UndoManager{
		doc:                    d,
		scope:                  append([]*block.Branch(nil), scope...),
		captureTimeout:         captureTimeout,
		trackedOrigins:         trackedOrigins,
		ignoreRemoteMapChanges: opt.IgnoreRemoteMapChanges,
		nowFn:                  time.Now,
	}
	um.unsubscribe = d.OnAfterTransaction(um.onAfterTransaction)
	return um
}

// onAfterTransaction is the Doc-level hook. Called under the Doc
// write lock; mutations to UndoManager state happen here. We take
// the local mu inside this callback so external readers of
// CanUndo / CanRedo see consistent state even between transactions.
func (um *UndoManager) onAfterTransaction(mut *doc.TransactionMut) {
	if um.closed {
		return
	}

	// Origin filter: skip transactions whose Origin is not in
	// trackedOrigins. The receiver itself is implicitly tracked so
	// Undo / Redo's own transactions roundtrip back to the opposite
	// stack.
	if !um.isTrackedOrigin(mut.Origin) {
		return
	}

	// Scope filter: at least one changedType must be in scope (or
	// descended from one). If nothing in scope changed, this
	// transaction is invisible to this UndoManager.
	if !um.touchesScope(mut.ChangedTypes()) {
		return
	}

	um.mu.Lock()
	defer um.mu.Unlock()

	// Build the StackItem for this transaction.
	si := newStackItem()

	// Insertions: per-client (afterState - beforeState).
	before := mut.BeforeState()
	after := mut.AfterState()
	for client, end := range after {
		start := before[client]
		if end > start {
			si.Insertions.Insert(client, start, end-start)
		}
	}

	// Deletions: every ID tombstoned during this transaction.
	for _, id := range mut.DeletedIDs() {
		// DeletedIDs reports the head of each deleted item; the
		// Len of the item is not exposed here. The simplest
		// correct thing is to record a singleton range and rely on
		// IdSet.Insert to merge consecutive deletions on the same
		// client. A follow-up will expose item-Len in
		// TransactionMut so we record the full range in one shot.
		si.Deletions.Insert(id.Client, id.Clock, 1)
	}

	now := um.nowFn()
	undoing := um.undoing
	redoing := um.redoing
	target := &um.undoStack
	if undoing {
		target = &um.redoStack
	}

	if !undoing && !redoing {
		// Fresh local edit: throw away any pending redo history.
		um.redoStack = um.redoStack[:0]
	}

	if !undoing && !redoing &&
		len(*target) > 0 &&
		!um.lastChange.IsZero() &&
		um.captureTimeout >= 0 &&
		now.Sub(um.lastChange) < um.captureTimeout {
		// Group with the previous StackItem.
		(*target)[len(*target)-1].merge(si)
	} else {
		*target = append(*target, si)
	}

	if !undoing && !redoing {
		um.lastChange = now
	}
}

// CanUndo reports whether there is anything on the undo stack.
func (um *UndoManager) CanUndo() bool {
	um.mu.Lock()
	defer um.mu.Unlock()
	return len(um.undoStack) > 0
}

// CanRedo reports whether there is anything on the redo stack.
func (um *UndoManager) CanRedo() bool {
	um.mu.Lock()
	defer um.mu.Unlock()
	return len(um.redoStack) > 0
}

// StopCapturing prevents the next change from grouping with the
// current top StackItem. Useful before a logical "boundary" event
// such as a save, a programmatic batch, or a user-visible step end.
func (um *UndoManager) StopCapturing() {
	um.mu.Lock()
	defer um.mu.Unlock()
	um.lastChange = time.Time{}
}

// Clear empties both stacks. After Clear, CanUndo and CanRedo both
// return false. Existing keep flags on items previously held against
// GC are released by the GC pass when the items become reachable
// again (not yet wired in this skeleton; tracked in tech-debt).
func (um *UndoManager) Clear() {
	um.mu.Lock()
	defer um.mu.Unlock()
	um.undoStack = um.undoStack[:0]
	um.redoStack = um.redoStack[:0]
	um.lastChange = time.Time{}
}

// Close unregisters this UndoManager from the doc and releases its
// stacks. After Close, all methods become no-ops (in particular Undo
// and Redo return false even if they would otherwise have anything
// to do).
func (um *UndoManager) Close() {
	um.mu.Lock()
	defer um.mu.Unlock()
	if um.closed {
		return
	}
	um.closed = true
	um.undoStack = nil
	um.redoStack = nil
	if um.unsubscribe != nil {
		um.unsubscribe()
		um.unsubscribe = nil
	}
}

// Undo pops the top of the undo stack and replays it against the
// doc. Returns true if a StackItem was applied. In this skeleton
// commit the actual replay is unimplemented; the call drains the top
// of the stack and returns false, signalling "nothing happened".
// The replay logic lands with Item.Redone in the next commit.
func (um *UndoManager) Undo() bool {
	um.mu.Lock()
	defer um.mu.Unlock()
	if um.closed || len(um.undoStack) == 0 {
		return false
	}
	// TODO(undo-execute): pop, run a WriteTxn with Origin=um and
	// undoing=true, walk Insertions to delete, walk Deletions to
	// redoItem-resurrect. Falls through to the AfterTransaction
	// handler which pushes the resulting StackItem to redoStack.
	return false
}

// Redo is the mirror of Undo. See the Undo note about the skeleton
// state.
func (um *UndoManager) Redo() bool {
	um.mu.Lock()
	defer um.mu.Unlock()
	if um.closed || len(um.redoStack) == 0 {
		return false
	}
	// TODO(undo-execute): mirror of Undo.
	return false
}

// isTrackedOrigin reports whether origin is in trackedOrigins. The
// UndoManager itself is always tracked so its own Undo / Redo
// transactions cycle back into the opposite stack.
func (um *UndoManager) isTrackedOrigin(origin any) bool {
	if origin == um {
		return true
	}
	_, ok := um.trackedOrigins[origin]
	return ok
}

// touchesScope reports whether any of the changedTypes lies under
// the configured scope. A direct match (one of the root branches in
// scope) is the common case; nested types walk up Branch.Item.Parent
// looking for a scope match.
func (um *UndoManager) touchesScope(changedTypes []*block.Branch) bool {
	for _, b := range changedTypes {
		if um.isInScope(b) {
			return true
		}
	}
	return false
}

// isInScope walks up the parent chain of b. The chain terminates at
// a root branch (Branch.Item == nil) or at a non-branch parent.
func (um *UndoManager) isInScope(b *block.Branch) bool {
	for cur := b; cur != nil; {
		for _, s := range um.scope {
			if cur == s {
				return true
			}
		}
		if cur.Item == nil {
			return false
		}
		if cur.Item.Parent.Kind != block.ParentBranch || cur.Item.Parent.Branch == nil {
			return false
		}
		cur = cur.Item.Parent.Branch
	}
	return false
}
