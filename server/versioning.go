package server

import (
	"context"
	"log"
	"time"

	"github.com/Deln0r/ygo/persist"
)

// startVersioning launches the periodic auto-versioning loop when the
// configuration enables it: VersionInterval > 0 and the Store also
// implements persist.VersionStore. Returns immediately otherwise.
//
// The loop only versions documents that received updates since the
// previous sweep (the dirty set, marked on every persisted update), so
// idle documents do not accumulate identical versions. Documents
// written by other processes sharing the database are not observed;
// auto-versioning assumes this server is the writer.
func (s *Server) startVersioning() {
	if s.opts.VersionInterval <= 0 || s.opts.Store == nil {
		return
	}
	vs, ok := s.opts.Store.(persist.VersionedStore)
	if !ok {
		log.Printf("server: VersionInterval set but Store does not implement persist.VersionStore; auto-versioning disabled")
		return
	}
	s.versionStop = make(chan struct{})
	s.versionDone = make(chan struct{})
	go func() {
		defer close(s.versionDone)
		ticker := time.NewTicker(s.opts.VersionInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.versionStop:
				return
			case <-ticker.C:
				s.sweepVersions(context.Background(), vs)
			}
		}
	}()
}

// stopVersioning terminates the auto-versioning loop and waits for an
// in-flight sweep to finish. Safe to call when versioning never
// started.
func (s *Server) stopVersioning() {
	if s.versionStop == nil {
		return
	}
	close(s.versionStop)
	<-s.versionDone
	s.versionStop = nil
}

// markVersionDirty records that docName changed since the last sweep.
func (s *Server) markVersionDirty(docName string) {
	if s.opts.VersionInterval <= 0 {
		return // versioning disabled: skip the lock entirely
	}
	s.versionMu.Lock()
	if s.versionDirty == nil {
		s.versionDirty = map[string]struct{}{}
	}
	s.versionDirty[docName] = struct{}{}
	s.versionMu.Unlock()
}

// sweepVersions captures one auto-version for every document marked
// dirty since the previous sweep, then prunes each to the newest
// KeepVersions. Failures are logged and the document stays dirty so
// the next sweep retries.
func (s *Server) sweepVersions(ctx context.Context, vs persist.VersionedStore) {
	s.versionMu.Lock()
	dirty := s.versionDirty
	s.versionDirty = nil
	s.versionMu.Unlock()

	for name := range dirty {
		if _, err := persist.SaveVersion(ctx, vs, name, "auto"); err != nil {
			log.Printf("server: auto-version %q: %v", name, err)
			s.markVersionDirty(name) // retry on the next sweep
			continue
		}
		if s.opts.KeepVersions > 0 {
			if _, err := persist.PruneVersions(ctx, vs, name, s.opts.KeepVersions); err != nil {
				log.Printf("server: prune versions %q: %v", name, err)
			}
		}
	}
}
