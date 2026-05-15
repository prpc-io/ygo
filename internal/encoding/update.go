package encoding

import (
	"errors"
	"fmt"
	"sort"

	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/lib0"
	"github.com/Deln0r/ygo/internal/store"
)

// BlockKind discriminates the three on-the-wire block variants:
// regular Item, GC tombstone range, Skip range.
type BlockKind uint8

const (
	WireBlockItem BlockKind = 0
	WireBlockGC   BlockKind = 1
	WireBlockSkip BlockKind = 2
)

// Block is one block-shaped record inside an Update. For WireBlockItem
// the Item field carries the parsed *block.Item with Origin /
// RightOrigin set but Left / Right nil (Repair fixes those at apply
// time). For WireBlockGC and WireBlockSkip only ID and Len matter.
type Block struct {
	Kind BlockKind
	Item *block.Item // WireBlockItem
	ID   block.ID    // WireBlockGC, WireBlockSkip
	Len  uint64      // WireBlockGC, WireBlockSkip
}

// Update is the parsed-but-not-yet-applied form of a V1 wire update.
// Blocks groups records by client; per-client lists are stored in
// emission order (clock-ascending within a client).
type Update struct {
	Blocks    map[uint64][]Block
	DeleteSet *IdSet
}

// NewUpdate returns an empty Update.
func NewUpdate() *Update {
	return &Update{
		Blocks:    map[uint64][]Block{},
		DeleteSet: NewIdSet(),
	}
}

// EncodeStateAsUpdate returns the V1 wire bytes for the doc's full
// state — equivalent to encoding against an empty remote state vector.
// The caller may pass an existing transaction for read access; if nil
// a fresh ReadTxn is acquired and released.
//
// Mirrors yrs Doc::encode_state_as_update_v1, simplified to omit the
// pending-update merge path (we have no pending buffer yet).
func EncodeStateAsUpdate(d *doc.Doc) []byte {
	txn := d.ReadTxn()
	defer txn.Close()
	return EncodeDiff(d, txn, nil)
}

// EncodeDiff returns the V1 wire bytes for the blocks the local doc
// has that the remote doc (per remoteSV) does not. A nil remoteSV is
// treated as the empty SV — emit everything.
//
// In this first port pass EncodeDiff emits whole blocks (no slicing
// at the SV boundary). yrs's Store::write_blocks_from clamps the
// first block of each client run via find_pivot + slice-trim; we
// emit the entire client list. The wire format is identical when
// the remote SV either knows the client fully (we skip them) or
// not at all (we emit from clock 0). Partial-knowledge clients
// (remote knows clocks [0, k) but not [k, end)) still get all
// blocks for that client emitted, so the receiver may see redundant
// blocks for clocks it already has. Those are silently rejected by
// integrate (state-vector check) so the result is correct, just
// chattier than yrs. Tracked in tech-debt.md.
//
// Block runs are emitted in DESCENDING clientID order per
// docs/yrs-port-notes/update-v1.md gotcha 1.
func EncodeDiff(d *doc.Doc, txn *doc.Transaction, remoteSV store.StateVector) []byte {
	bs := txn.Store()
	localSV := bs.GetStateVector()

	// diff = clients to emit. For each client present in localSV with
	// localClock > remoteClock, include with the remote clock as the
	// run start. Clients absent from remoteSV start at 0.
	type clientRun struct {
		client     uint64
		startClock uint64
	}
	var diff []clientRun
	for c, localClock := range localSV {
		remoteClock := uint64(0)
		if remoteSV != nil {
			remoteClock = remoteSV[c]
		}
		if localClock > remoteClock {
			diff = append(diff, clientRun{client: c, startClock: remoteClock})
		}
	}
	// Descending by clientID.
	sort.Slice(diff, func(i, j int) bool { return diff[i].client > diff[j].client })

	buf := lib0.WriteVarUint(nil, uint64(len(diff)))
	for _, run := range diff {
		clientList := bs.GetClient(run.client)
		// Emit all cells from index 0 (full-client fallback per
		// docstring above); the SV-clamped form will land with the
		// slice-aware EncodeDiff in a follow-up commit.
		_ = run.startClock // intentionally unused in this pass
		buf = lib0.WriteVarUint(buf, uint64(clientList.Len()))
		buf = lib0.WriteVarUint(buf, run.client)
		first, _ := clientList.Get(0)
		buf = lib0.WriteVarUint(buf, first.ClockStart())
		for i := 0; i < clientList.Len(); i++ {
			cell, _ := clientList.Get(i)
			buf = encodeCell(buf, cell)
		}
	}

	// Emit delete set: scan all cells, collect deleted ranges.
	ds := buildDeleteSetFromStore(bs, localSV)
	buf = ds.Encode(buf)

	return buf
}

// encodeCell writes one cell's wire record (info byte + conditional
// fields + content).
//
// Mirrors yrs ItemSlice::encode (slice.rs:181-233) for the
// adjacent-on-both-sides case. We do not yet support partial slices
// (start > 0 or end < len-1); first-block trimming is deferred per
// EncodeDiff's docstring.
func encodeCell(buf []byte, cell store.BlockCell) []byte {
	switch cell.Kind {
	case store.CellKindGC:
		buf = lib0.WriteVarUint(buf, uint64(0)) // BLOCK_GC_REF_NUMBER
		return lib0.WriteVarUint(buf, cell.GC.Len())
	case store.CellKindItem:
		return encodeItem(buf, cell.Item)
	default:
		panic(fmt.Sprintf("encoding.encodeCell: unknown cell kind %d", cell.Kind))
	}
}

// encodeItem writes a full Item record (no slicing).
func encodeItem(buf []byte, it *block.Item) []byte {
	info := it.Info()
	cantCopyParentInfo := info&(block.InfoHasOrigin|block.InfoHasRightOrigin) == 0
	buf = append(buf, info)
	if it.Origin != nil {
		buf = encodeID(buf, *it.Origin)
	}
	if it.RightOrigin != nil {
		buf = encodeID(buf, *it.RightOrigin)
	}
	if cantCopyParentInfo {
		switch it.Parent.Kind {
		case block.ParentBranch:
			b := it.Parent.Branch
			if b == nil {
				panic("encoding.encodeItem: ParentBranch with nil Branch")
			}
			if b.Item != nil {
				// Nested branch — emit as parent-by-ID.
				buf = append(buf, 0x00) // parent_info: false
				buf = encodeID(buf, b.Item.ID)
			} else if b.Name != "" {
				// Root branch — emit by name.
				buf = append(buf, 0x01) // parent_info: true
				buf = lib0.WriteVarString(buf, b.Name)
			} else {
				panic("encoding.encodeItem: ParentBranch with neither nested item nor root name")
			}
		case block.ParentNamed:
			buf = append(buf, 0x01)
			buf = lib0.WriteVarString(buf, it.Parent.Named)
		case block.ParentID:
			buf = append(buf, 0x00)
			buf = encodeID(buf, it.Parent.ID)
		case block.ParentUnknown:
			panic("encoding.encodeItem: ParentUnknown — cannot emit")
		}
		if it.ParentSub != nil {
			buf = lib0.WriteVarString(buf, *it.ParentSub)
		}
	}
	return EncodeContent(buf, it.Content)
}

func encodeID(buf []byte, id block.ID) []byte {
	buf = lib0.WriteVarUint(buf, id.Client)
	return lib0.WriteVarUint(buf, id.Clock)
}

// buildDeleteSetFromStore produces the wire DeleteSet by scanning
// every cell (Items and GC alike) and recording deleted ID ranges.
//
// Mirrors yrs IdSet::from_store (id_set.rs:432-448), simplified.
func buildDeleteSetFromStore(bs *store.BlockStore, sv store.StateVector) *IdSet {
	ds := NewIdSet()
	// Iterate clients in deterministic ascending order; IdSet.Insert
	// keeps per-client ranges sorted regardless.
	clients := make([]uint64, 0, len(sv))
	for c := range sv {
		clients = append(clients, c)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i] < clients[j] })
	for _, c := range clients {
		l := bs.GetClient(c)
		if l == nil {
			continue
		}
		for i := 0; i < l.Len(); i++ {
			cell, _ := l.Get(i)
			if cell.IsDeleted() {
				ds.Insert(c, cell.ClockStart(), cell.Len())
			}
		}
	}
	return ds
}

// DecodeUpdate parses V1 wire bytes into an Update. The returned
// Update is ready for Apply but its blocks have nil Left/Right —
// Repair fills those at apply time.
//
// Mirrors yrs Update::decode (update.rs:497-516).
func DecodeUpdate(buf []byte) (*Update, []byte, error) {
	clientCount, n, err := lib0.ReadVarUint(buf)
	if err != nil {
		return nil, buf, err
	}
	buf = buf[n:]

	u := NewUpdate()
	for i := uint64(0); i < clientCount; i++ {
		blockCount, n, err := lib0.ReadVarUint(buf)
		if err != nil {
			return nil, buf, err
		}
		buf = buf[n:]
		client, n, err := lib0.ReadVarUint(buf)
		if err != nil {
			return nil, buf, err
		}
		buf = buf[n:]
		clock, n, err := lib0.ReadVarUint(buf)
		if err != nil {
			return nil, buf, err
		}
		buf = buf[n:]

		blocks := make([]Block, 0, blockCount)
		for j := uint64(0); j < blockCount; j++ {
			b, tail, err := decodeBlock(buf, block.ID{Client: client, Clock: clock})
			if err != nil {
				return nil, buf, err
			}
			blocks = append(blocks, b)
			clock += b.length()
			buf = tail
		}
		u.Blocks[client] = blocks
	}

	ds, tail, err := DecodeIdSet(buf)
	if err != nil {
		return nil, buf, err
	}
	u.DeleteSet = ds
	return u, tail, nil
}

func (b Block) length() uint64 {
	if b.Kind == WireBlockItem {
		return b.Item.Len
	}
	return b.Len
}

// decodeBlock parses one block record at the given starting ID.
//
// Mirrors yrs Update::decode_block (update.rs:364-393).
func decodeBlock(buf []byte, id block.ID) (Block, []byte, error) {
	if len(buf) < 1 {
		return Block{}, buf, lib0.ErrTruncated
	}
	info := buf[0]
	buf = buf[1:]

	switch info {
	case 0: // BLOCK_GC_REF_NUMBER
		l, n, err := lib0.ReadVarUint(buf)
		if err != nil {
			return Block{}, buf, err
		}
		return Block{Kind: WireBlockGC, ID: id, Len: l}, buf[n:], nil
	case 10: // BLOCK_SKIP_REF_NUMBER
		l, n, err := lib0.ReadVarUint(buf)
		if err != nil {
			return Block{}, buf, err
		}
		return Block{Kind: WireBlockSkip, ID: id, Len: l}, buf[n:], nil
	}

	cantCopyParentInfo := info&(block.InfoHasOrigin|block.InfoHasRightOrigin) == 0
	var origin, rightOrigin *block.ID
	if info&block.InfoHasOrigin != 0 {
		idVal, tail, err := decodeID(buf)
		if err != nil {
			return Block{}, buf, err
		}
		origin = &idVal
		buf = tail
	}
	if info&block.InfoHasRightOrigin != 0 {
		idVal, tail, err := decodeID(buf)
		if err != nil {
			return Block{}, buf, err
		}
		rightOrigin = &idVal
		buf = tail
	}

	parent := block.Parent{Kind: block.ParentUnknown}
	if cantCopyParentInfo {
		if len(buf) < 1 {
			return Block{}, buf, lib0.ErrTruncated
		}
		parentInfo := buf[0]
		buf = buf[1:]
		if parentInfo == 0x01 {
			name, n, err := lib0.ReadVarString(buf)
			if err != nil {
				return Block{}, buf, err
			}
			parent = block.Parent{Kind: block.ParentNamed, Named: name}
			buf = buf[n:]
		} else {
			pid, tail, err := decodeID(buf)
			if err != nil {
				return Block{}, buf, err
			}
			parent = block.Parent{Kind: block.ParentID, ID: pid}
			buf = tail
		}
	}

	var parentSub *string
	if cantCopyParentInfo && info&block.InfoHasParentSub != 0 {
		s, n, err := lib0.ReadVarString(buf)
		if err != nil {
			return Block{}, buf, err
		}
		parentSub = &s
		buf = buf[n:]
	}

	content, tail, err := DecodeContent(buf, info&block.InfoContentMask)
	if err != nil {
		return Block{}, buf, err
	}
	buf = tail

	it := &block.Item{
		ID:          id,
		Len:         content.Len(block.OffsetUtf16),
		Origin:      origin,
		RightOrigin: rightOrigin,
		Content:     content,
		Parent:      parent,
		ParentSub:   parentSub,
	}
	if content.IsCountable() {
		it.SetCountable(true)
	}
	return Block{Kind: WireBlockItem, Item: it}, buf, nil
}

func decodeID(buf []byte) (block.ID, []byte, error) {
	client, n, err := lib0.ReadVarUint(buf)
	if err != nil {
		return block.ID{}, buf, err
	}
	buf = buf[n:]
	clock, n, err := lib0.ReadVarUint(buf)
	if err != nil {
		return block.ID{}, buf, err
	}
	return block.ID{Client: client, Clock: clock}, buf[n:], nil
}

// ErrMissingDependency means an item's Origin / RightOrigin / parent
// references an ID the local store has not yet seen. Returned by
// Apply for items that would, in yrs, get queued onto the pending
// buffer for retry. Until we ship the pending buffer the caller must
// either retry the entire update later or accept that the
// just-failed item never lands.
var ErrMissingDependency = errors.New("encoding: update item depends on unseen ID (no pending buffer yet)")

// Apply integrates every block in the update into the doc represented
// by txn. Items are integrated in ascending clientID order (the
// reverse of emission for diagnosability; integrate semantics are
// order-independent within a single update once dependencies resolve).
//
// Mirrors yrs Update::integrate (update.rs:234-301), without the
// pending-buffer / retry logic: a missing dependency aborts.
func (u *Update) Apply(txn *doc.TransactionMut) error {
	clients := make([]uint64, 0, len(u.Blocks))
	for c := range u.Blocks {
		clients = append(clients, c)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i] < clients[j] })
	bs := txn.Store()

	for _, c := range clients {
		blocks := u.Blocks[c]
		for _, b := range blocks {
			switch b.Kind {
			case WireBlockGC:
				if !bs.Contains(b.ID) {
					bs.PushGC(c, b.ID.Clock, b.ID.Clock+b.Len-1)
				}
			case WireBlockSkip:
				// Skip blocks reserve clock space without semantics; we
				// drop them (they'll be re-emitted by the next encode
				// scan if relevant).
			case WireBlockItem:
				it := b.Item
				if bs.Contains(it.ID) {
					// Already integrated locally; skip without error.
					continue
				}
				if err := block.Repair(it, txn); err != nil {
					return fmt.Errorf("repair %v: %w", it.ID, err)
				}
				bs.PushBlock(it)
				if dropped := it.Integrate(txn, 0); dropped {
					txn.Delete(it)
				}
			}
		}
	}

	// Apply delete set: tombstone every covered ID.
	u.DeleteSet.Iterate(func(client uint64, ranges []Range) {
		for _, r := range ranges {
			clock := r.Start
			end := r.End()
			for clock < end {
				cell, ok := bs.GetBlock(block.ID{Client: client, Clock: clock})
				if !ok {
					clock++
					continue
				}
				if it := cell.AsItem(); it != nil && !it.IsDeleted() {
					txn.Delete(it)
				}
				clock = cell.ClockEnd() + 1
			}
		}
	})

	return nil
}
