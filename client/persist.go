package client

import (
	"context"
	"log"
	"time"

	"github.com/Deln0r/ygo/internal/encoding"
)

// flushEvery compacts the local update log after this many persisted
// updates, so a long-lived offline client does not accumulate an
// unbounded log (each EncodeDiff carries the full delete set). The
// store's Flush merges the log into a single snapshot.
const flushEvery = 64

// persistRetryDelay paces re-tries after a failed store write so a
// persistent failure (disk full) does not spin the persist loop.
const persistRetryDelay = time.Second

// minUpdateLen is the byte length of an empty V1 update (zero clients,
// empty delete set). A diff this short carries no new content and is
// not worth storing.
const minUpdateLen = 2

// loadLocal replays the document's persisted update log into the live
// doc before the client connects, giving the caller offline state
// immediately. Applied under this client's Origin so the not-yet-
// installed observer would skip them anyway; the persisted watermark
// is set to the loaded state so the persist loop only writes new
// changes from here on.
func (c *Client) loadLocal(ctx context.Context) {
	updates, err := c.opts.LocalStore.GetUpdates(ctx, c.opts.DocName)
	if err != nil {
		if c.opts.OnError != nil {
			c.opts.OnError(err)
		}
		return
	}
	if len(updates) > 0 {
		txn := c.doc.WriteTxn()
		txn.Origin = c
		for _, raw := range updates {
			upd, _, derr := encoding.DecodeUpdate(raw)
			if derr != nil {
				continue // skip a corrupt blob rather than abort the load
			}
			_ = upd.Apply(txn)
		}
		txn.Commit()
	}
	rtxn := c.doc.ReadTxn()
	sv := cloneSV(rtxn.Store().GetStateVector())
	rtxn.Close()
	c.mu.Lock()
	c.lastPersisted = sv
	c.mu.Unlock()
}

// persistLoop writes document changes to the local store until the
// context is cancelled, then performs a final persist and compaction.
// Signals coalesce through the cap-1 dirtyPersist channel, so a burst
// of edits collapses into one diff write.
func (c *Client) persistLoop(ctx context.Context) {
	defer close(c.persistDone)
	for {
		select {
		case <-ctx.Done():
			c.persistNow(true) // final attempt; capture pre-cancel edits
			c.flushStore()
			return
		case <-c.dirtyPersist:
			c.persistNow(false)
		}
	}
}

// persistNow writes the diff between the document's current state and
// the persisted watermark to the store, then advances the watermark.
// EncodeDiff always emits the delete set, so a delete-only change is
// captured even though it adds no new clocks; replaying the redundant
// delete set is idempotent and compaction collapses it.
//
// final marks the shutdown flush: it does not schedule a retry (the
// loop is exiting) but still reports any failure.
func (c *Client) persistNow(final bool) {
	c.mu.Lock()
	since := c.lastPersisted
	c.mu.Unlock()

	rtxn := c.doc.ReadTxn()
	diff := encoding.EncodeDiff(c.doc, rtxn, since)
	sv := cloneSV(rtxn.Store().GetStateVector())
	rtxn.Close()

	// An empty-shaped diff (no new structs, no deletes) carries nothing;
	// skip the write to avoid bloating the log with no-op blobs that a
	// coalesced double-signal would otherwise produce.
	if len(diff) <= minUpdateLen {
		return
	}

	if err := c.opts.LocalStore.StoreUpdate(context.Background(), c.opts.DocName, diff); err != nil {
		// The watermark stays unmoved, but the consumed dirtyPersist
		// token is gone, so a future edit is not guaranteed. Report the
		// failure (always observable, even without a Listener) and, mid-
		// session, schedule a paced retry so the edit is not silently
		// dropped on a transient error.
		c.reportError(err)
		if !final {
			c.scheduleRetry()
		}
		return
	}
	c.mu.Lock()
	c.lastPersisted = sv
	c.persistCount++
	count := c.persistCount
	c.mu.Unlock()
	if count%flushEvery == 0 {
		c.flushStore()
	}
}

// reportError surfaces a persistence error through OnError when set,
// and always logs it so a write failure is never completely silent
// (the gomobile binding wires OnError only when a Listener is set).
func (c *Client) reportError(err error) {
	if c.opts.OnError != nil {
		c.opts.OnError(err)
	} else {
		log.Printf("client: local persistence: %v", err)
	}
}

// scheduleRetry re-signals the persist loop after a delay so a
// transient store failure (and the last edit before an idle period) is
// retried without spinning. A non-blocking send drops the extra token
// if one is already pending; if the loop has since exited the buffered
// send is harmless (the channel is never closed).
func (c *Client) scheduleRetry() {
	time.AfterFunc(persistRetryDelay, func() {
		select {
		case c.dirtyPersist <- struct{}{}:
		default:
		}
	})
}

// flushStore compacts the local update log into a single snapshot.
func (c *Client) flushStore() {
	if err := c.opts.LocalStore.Flush(context.Background(), c.opts.DocName); err != nil {
		if c.opts.OnError != nil {
			c.opts.OnError(err)
		}
	}
}
