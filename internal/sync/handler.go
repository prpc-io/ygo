package sync

import (
	"errors"
	"fmt"
	"sync"

	"github.com/Deln0r/ygo/internal/awareness"
	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/encoding"
)

// Sender writes one outer-envelope frame back to a single
// connection. The transport layer implements this — for WebSocket
// it serializes via the conn's write mutex; for in-memory tests it
// appends to a slice.
type Sender func(envelope []byte) error

// Broadcaster fans one outer-envelope frame to every connection on
// the same doc, including the originator. Per port-note gotcha 6,
// V1 updates are idempotent so skip-self is unnecessary at the
// network layer. The transport layer implements this against a
// per-doc connection set.
type Broadcaster func(envelope []byte)

// Conn is the per-connection sync state machine. It owns no
// transport — Send and Broadcast are caller-supplied. The package
// tests exercise it with in-memory channels; the server/ package
// wires it to coder/websocket.
//
// Conn is safe for sequential use from a single read goroutine.
// HandleFrame must not be called concurrently with itself on the
// same Conn; the transport's read loop serializes naturally.
// Send and Broadcast may be called from other goroutines (e.g. an
// awareness OnChange callback running on a different conn's read
// goroutine).
type Conn struct {
	// Doc is the document this connection edits. All Sync messages
	// route here. Multiple Conns sharing the same Doc form one
	// collaborative session.
	Doc *doc.Doc

	// Awareness is the presence layer multiplexed on the same WS.
	// Multiple Conns share one *Awareness per doc.
	Awareness *awareness.Awareness

	// ID identifies this connection in diagnostics and as the
	// origin tag passed to awareness.Apply. The transport layer
	// generates this (typically a random string or remote addr).
	ID string

	// Send writes one envelope to this connection only.
	Send Sender

	// Broadcast writes one envelope to every connection on this
	// doc, including self.
	Broadcast Broadcaster

	// controlledClients tracks the awareness clientIDs this
	// connection has authoritatively introduced. On disconnect
	// the transport iterates these and calls Awareness.RemoveState
	// so other peers learn the presence is gone.
	muClients         sync.Mutex
	controlledClients map[uint64]struct{}
}

// New returns a Conn ready to handle frames. The transport must set
// Send and Broadcast before any HandleFrame / SendInitialSync call;
// New does not check because the transport may wire those after
// fully constructing the Conn (typical pattern: build Conn, register
// in the broadcaster registry, then start the read loop).
func New(d *doc.Doc, aw *awareness.Awareness, id string) *Conn {
	return &Conn{
		Doc:               d,
		Awareness:         aw,
		ID:                id,
		controlledClients: map[uint64]struct{}{},
	}
}

// SendInitialSync emits the server-side opening of the sync
// handshake: a SyncStep1 carrying the current local state vector,
// followed by an Awareness frame carrying the current full
// awareness snapshot (the implicit response to a future
// QueryAwareness).
//
// Per port-note state machine §1.5, the server opens by sending
// SyncStep1 immediately on connect. Clients open with their own
// SyncStep1 in parallel; the two cross on the wire and both peers
// reply with SyncStep2.
//
// The awareness send is technically optional under the bare
// y-websocket protocol (clients learn about peers via the broadcast
// stream as they update). We send it eagerly so a fresh client
// gets a complete snapshot without waiting for the next change.
func (c *Conn) SendInitialSync() error {
	rtxn := c.Doc.ReadTxn()
	sv := encoding.EncodeStateVector(rtxn.Store().GetStateVector(), nil)
	rtxn.Close()
	if err := c.Send(EncodeSyncStep1(sv)); err != nil {
		return fmt.Errorf("send initial SyncStep1: %w", err)
	}
	if states := c.Awareness.States(); len(states) > 0 {
		ids := make([]uint64, 0, len(states))
		for id := range states {
			ids = append(ids, id)
		}
		if err := c.Send(EncodeAwareness(c.Awareness.Encode(ids))); err != nil {
			return fmt.Errorf("send initial awareness snapshot: %w", err)
		}
	}
	return nil
}

// HandleFrame routes one decoded envelope through the state machine
// per docs/yrs-port-notes/protocol-sync.md. Returns an error only
// for genuine protocol violations (malformed payload bytes);
// unknown message types are silently ignored to preserve forward
// compatibility with Hocuspocus extensions.
func (c *Conn) HandleFrame(frame *Frame) error {
	switch frame.Type {
	case MessageSync, MessageSyncReply:
		return c.handleSync(frame)
	case MessageAwareness:
		return c.handleAwareness(frame)
	case MessageQueryAwareness:
		return c.handleQueryAwareness()
	case MessagePing:
		return c.Send(EncodePong())
	case MessagePong:
		// Server doesn't send pings yet; an inbound Pong is a no-op.
		return nil
	default:
		// Stateless / BroadcastStateless / Close / SyncStatus / Auth
		// — not implemented in v0.1, silently drop.
		return nil
	}
}

func (c *Conn) handleSync(frame *Frame) error {
	switch frame.SyncSub {
	case SyncStep1:
		// Peer wants our diff against their state vector. Reply
		// with SyncStep2 carrying everything they're missing.
		remoteSV, _, err := encoding.DecodeStateVector(frame.Payload)
		if err != nil {
			return fmt.Errorf("decode SyncStep1 SV: %w", err)
		}
		rtxn := c.Doc.ReadTxn()
		diff := encoding.EncodeDiff(c.Doc, rtxn, remoteSV)
		rtxn.Close()
		return c.Send(EncodeSyncStep2(diff))

	case SyncStep2, SyncUpdate:
		// Peer is delivering content. Apply (which queues missing
		// deps in encoding.Pending) and broadcast as a SyncUpdate
		// to all peers including self. Self-echo is safe because
		// V1 updates are idempotent — see port-note gotcha 6.
		if err := encoding.ApplyUpdate(c.Doc, frame.Payload); err != nil {
			return fmt.Errorf("apply Sync%s update: %w", subTypeName(frame.SyncSub), err)
		}
		// Re-encode as SyncUpdate (regardless of inbound sub-type)
		// because the fan-out semantics on receivers are identical
		// and using a single sub-type simplifies their dispatcher.
		c.Broadcast(EncodeSyncUpdate(frame.Payload))
		return nil

	default:
		return fmt.Errorf("%w: %d", ErrUnknownSyncSubType, frame.SyncSub)
	}
}

func (c *Conn) handleAwareness(frame *Frame) error {
	summary, err := c.Awareness.Apply(frame.Payload, c.ID)
	if err != nil {
		return fmt.Errorf("apply awareness: %w", err)
	}

	// Track ownership for cleanup on disconnect: every clientID
	// this connection has authoritatively introduced or updated is
	// recorded; on disconnect we tombstone them so peers learn the
	// presence is gone. Removed entries from the same connection
	// drop out of the controlled set immediately.
	c.muClients.Lock()
	for _, id := range summary.Added {
		c.controlledClients[id] = struct{}{}
	}
	for _, id := range summary.Updated {
		c.controlledClients[id] = struct{}{}
	}
	for _, id := range summary.Removed {
		delete(c.controlledClients, id)
	}
	c.muClients.Unlock()

	// Broadcast the awareness frame so other connections learn the
	// new state. Including self is harmless — the receiver checks
	// clock and drops stale/equal entries.
	c.Broadcast(EncodeAwareness(frame.Payload))
	return nil
}

func (c *Conn) handleQueryAwareness() error {
	// Reply with the full current snapshot — every known client.
	states := c.Awareness.States()
	ids := make([]uint64, 0, len(states))
	for id := range states {
		ids = append(ids, id)
	}
	return c.Send(EncodeAwareness(c.Awareness.Encode(ids)))
}

// ControlledClients returns a snapshot of the awareness clientIDs
// this connection has authoritatively touched. The transport calls
// this on disconnect to tombstone them on behalf of departing
// clients.
//
// Returned slice is sorted ascending for deterministic disconnect
// behaviour in tests.
func (c *Conn) ControlledClients() []uint64 {
	c.muClients.Lock()
	defer c.muClients.Unlock()
	out := make([]uint64, 0, len(c.controlledClients))
	for id := range c.controlledClients {
		out = append(out, id)
	}
	// Inline insertion-sort — tiny, no closure-alloc.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Disconnect tombstones every controlled awareness clientID. The
// transport calls this when the WS closes. After Disconnect the
// Conn's Send / Broadcast functions MUST NOT be called by the
// caller — the underlying WS is dead.
//
// Returns the IDs that were tombstoned, for diagnostic logging.
func (c *Conn) Disconnect() []uint64 {
	ids := c.ControlledClients()
	for _, id := range ids {
		c.Awareness.RemoveState(id)
	}
	return ids
}

func subTypeName(t SyncSubType) string {
	switch t {
	case SyncStep1:
		return "Step1"
	case SyncStep2:
		return "Step2"
	case SyncUpdate:
		return "Update"
	}
	return "Unknown"
}

// ErrSendFailed is returned by transport implementations of Sender
// when a write to the underlying socket fails. The handler does
// not wrap this; it propagates so the read loop can tear down the
// connection cleanly.
var ErrSendFailed = errors.New("sync: send failed")
