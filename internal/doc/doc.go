// Package doc owns the Doc and Transaction types — the document
// container plus its mutation-lifecycle wrapper.
//
// A Doc is the smallest self-contained CRDT replica: it holds a
// client identifier, a block store of every Item this replica has
// produced or received, and a single RWMutex that gates concurrent
// access. Mutations occur inside a Transaction (write) or are
// observed inside a Transaction (read).
//
// See docs/yrs-port-notes/transaction.md for the per-method contract
// and the 11-step commit lifecycle yrs runs at TransactionMut.Commit.
// Most of that lifecycle is not yet implemented; see tech-debt.md.
package doc

import (
	"crypto/rand"
	"encoding/binary"
	"sync"

	"github.com/Deln0r/ygo/internal/store"
)

// MaxClientID is the upper bound on Doc.ClientID values. Set to
// 2^53-1 to match the JS-safe-integer range that JS Yjs uses for its
// clientID; all values produced by NewDoc fit in [1, MaxClientID].
const MaxClientID uint64 = (1 << 53) - 1

// Options bundles per-Doc settings. The zero value (Options{}) gives
// the safe defaults that JS Yjs and yrs use: GC enabled, random
// ClientID. Fields are inverted from their natural English where
// needed so that Go's zero-value default matches the recommended
// configuration.
type Options struct {
	// DisableGC turns OFF garbage collection of fully-observed
	// deleted items at transaction commit. Default (false) means GC
	// is enabled, matching JS Yjs and yrs. Set to true only if you
	// intend to use snapshots; otherwise GC may drop content the
	// snapshot needs to materialize.
	DisableGC bool

	// ClientID overrides the random client identifier. When zero
	// (the default), NewDocWithOptions generates one via crypto/rand.
	// Pass a fixed value only for deterministic tests.
	ClientID uint64
}

// Doc is a single CRDT replica. Construct with NewDoc.
//
// Doc is safe for concurrent access through ReadTxn / WriteTxn — those
// methods acquire the internal RWMutex appropriately. Direct field
// access from outside the doc package is not safe.
type Doc struct {
	clientID uint64
	store    *store.BlockStore
	gc       bool
	mu       sync.RWMutex
}

// NewDoc returns a fresh Doc with default options and a random
// client identifier in [1, MaxClientID].
func NewDoc() *Doc {
	return NewDocWithOptions(Options{})
}

// NewDocWithOptions returns a fresh Doc with the given options.
// A zero Options{} is equivalent to NewDoc().
func NewDocWithOptions(opts Options) *Doc {
	cid := opts.ClientID
	if cid == 0 {
		cid = newClientID()
	}
	return &Doc{
		clientID: cid,
		store:    store.NewBlockStore(),
		gc:       !opts.DisableGC,
	}
}

// ClientID returns the random per-replica identifier. Stable for the
// life of the Doc; used as the Client field in every ID this replica
// produces.
func (d *Doc) ClientID() uint64 { return d.clientID }

// GC reports whether garbage collection of fully-observed deleted
// items will run at transaction commit.
func (d *Doc) GC() bool { return d.gc }

// store accessor used internally by Transaction methods. Unexported
// so external packages route through Transaction (which holds the
// appropriate lock).
func (d *Doc) blockStore() *store.BlockStore { return d.store }

// newClientID returns a random non-zero uint64 in [1, MaxClientID].
// Uses crypto/rand for collision resistance across replicas. Retry
// loop handles the (probability 2^-53) case where the masked draw is
// zero, which our convention treats as reserved (yrs uses NonZeroU64).
func newClientID() uint64 {
	var b [8]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			panic("ygo: crypto/rand failed: " + err.Error())
		}
		id := binary.BigEndian.Uint64(b[:]) & MaxClientID
		if id != 0 {
			return id
		}
	}
}
