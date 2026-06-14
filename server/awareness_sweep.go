package server

import "time"

// startAwarenessSweep launches the periodic presence-eviction loop.
// It is always on for a Server: the awareness layer is an exposed
// attack surface, and without a sweep a peer's wedged or churned
// clients accumulate as live entries and tombstones without bound.
//
// Every tick the loop marks silent clients offline across every live
// document (broadcasting the removals) and garbage-collects old
// tombstones. The tick is a tenth of the configured timeout — the
// same cadence as y-protocols' outdatedTimeout/10 check — floored at
// one second so sub-10s timeouts do not spin. Frequent enough that
// evictions land promptly, cheap enough to be negligible (a map scan
// per document).
func (s *Server) startAwarenessSweep() {
	interval := s.opts.AwarenessTimeout / 10
	if interval < time.Second {
		interval = time.Second
	}
	s.awarenessStop = make(chan struct{})
	s.awarenessDone = make(chan struct{})
	go func() {
		defer close(s.awarenessDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.awarenessStop:
				return
			case <-ticker.C:
				s.sweepAwareness()
			}
		}
	}()
}

// stopAwarenessSweep terminates the eviction loop and waits for an
// in-flight sweep to finish. Safe to call when the sweep never
// started.
func (s *Server) stopAwarenessSweep() {
	if s.awarenessStop == nil {
		return
	}
	close(s.awarenessStop)
	<-s.awarenessDone
	s.awarenessStop = nil
}

// sweepAwareness evicts silent presence entries from every live
// document and reclaims old tombstones. Swept removals are broadcast
// to the room so peers drop the departed cursors. Tombstones are
// purged only after twice the timeout, so a live eviction and its
// broadcast settle before the key is freed and a stale low-clock
// revival can no longer resurrect the entry.
func (s *Server) sweepAwareness() {
	timeout := s.opts.AwarenessTimeout

	s.docsMu.Lock()
	states := make([]*docState, 0, len(s.docs))
	for _, st := range s.docs {
		states = append(states, st)
	}
	s.docsMu.Unlock()

	for _, st := range states {
		removed := st.awareness.SweepOutdated(timeout)
		st.broadcastAwarenessRemovals(removed)
		st.awareness.PurgeTombstones(2 * timeout)
	}
}
