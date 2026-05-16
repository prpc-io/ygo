// Command ygo-server is the stand-alone WebSocket sync server for
// ygo documents. It speaks the bare y-websocket subset of the
// Hocuspocus envelope (Sync + Awareness + QueryAwareness), which
// covers the universal interop subset shared by every JS Yjs
// adopter.
//
// Usage:
//
//	ygo-server [-addr :8080] [-store path/to/ygo.db]
//
// Without -store the server runs purely in-memory; documents are
// lost when their last connection disconnects. With -store the
// server persists every applied update to a SQLite database and
// loads the document history on first connect of a fresh server
// process.
//
// Mount point: documents are addressed by the URL path. A client
// connecting to ws://host:8080/my-doc operates on docName
// "my-doc". The leading slash is stripped; query strings are
// ignored (matching y-websocket's convention).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Deln0r/ygo/persist"
	"github.com/Deln0r/ygo/persist/sqlite"
	"github.com/Deln0r/ygo/server"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	storePath := flag.String("store", "", "SQLite database path for persistence (empty = in-memory)")
	flag.Parse()

	var store persist.Store
	if *storePath != "" {
		s, err := sqlite.Open(*storePath)
		if err != nil {
			log.Fatalf("ygo-server: open store %q: %v", *storePath, err)
		}
		defer s.Close()
		store = s
		log.Printf("ygo-server: persistence enabled (sqlite at %s)", *storePath)
	} else {
		log.Printf("ygo-server: in-memory only (pass -store to persist)")
	}

	srv := server.New(server.Options{
		Store:          store,
		OriginPatterns: []string{"*"}, // dev-friendly; tighten in prod
	})

	httpSrv := &http.Server{
		Addr:    *addr,
		Handler: srv.Handler(),
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	idleConnsClosed := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Printf("ygo-server: shutting down")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			log.Printf("ygo-server: HTTP shutdown: %v", err)
		}
		if err := srv.Close(ctx); err != nil {
			log.Printf("ygo-server: store flush: %v", err)
		}
		close(idleConnsClosed)
	}()

	log.Printf("ygo-server: listening on %s", *addr)
	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("ygo-server: %v", err)
	}
	<-idleConnsClosed
	log.Printf("ygo-server: stopped")
	fmt.Fprintln(os.Stderr, "")
}
