package client_test

import (
	"context"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Deln0r/ygo/client"
	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/persist/sqlite"
	"github.com/Deln0r/ygo/server"
)

// trackingListener wraps a net.Listener, remembering every accepted
// connection so the test can forcibly drop them. http.Server.Close
// does not close hijacked WebSocket connections, so a test that needs
// the client to observe a hard transport drop closes them here.
type trackingListener struct {
	net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

func (l *trackingListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.conns = append(l.conns, c)
	l.mu.Unlock()
	return c, nil
}

// dropAll force-closes every accepted connection, breaking the
// client's WebSocket read so it enters the reconnect loop.
func (l *trackingListener) dropAll() {
	l.mu.Lock()
	conns := l.conns
	l.conns = nil
	l.mu.Unlock()
	for _, c := range conns {
		_ = c.Close()
	}
}

// mustListen binds a tracking TCP listener on a free port.
func mustListen(t *testing.T) *trackingListener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return &trackingListener{Listener: ln}
}

// mustListenOn binds a tracking TCP listener on a specific address,
// retrying briefly while the previous listener's port frees.
func mustListenOn(t *testing.T, addr string) *trackingListener {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return &trackingListener{Listener: ln}
		}
		if time.Now().After(deadline) {
			t.Fatalf("listen on %s: %v", addr, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestClient_ReconnectsAfterServerDrop exercises the run() backoff
// loop: a client syncs, the server goes away, the client keeps
// retrying, then a fresh server on the SAME address accepts the
// redial and the client re-syncs a pre-existing edit. This is the
// single most load-bearing path in the package and was previously
// uncovered.
func TestClient_ReconnectsAfterServerDrop(t *testing.T) {
	// Bind a listener we control so the same port can be reused across
	// two server instances.
	// A file-backed store so the document survives the server restart
	// (in-memory state is lost when the last connection drops).
	storePath := t.TempDir() + "/recon.db"

	ln := mustListen(t)
	addr := ln.Addr().String()
	url := "ws://" + addr

	srv1, st1 := newPersistentServer(t, storePath)
	hs1 := &http.Server{Handler: srv1.Handler()}
	go hs1.Serve(ln)

	// Seed an edit before the drop via a throwaway client, then let the
	// store retain it.
	seed := dial(t, url, "room", 92)
	sm := mapOf(seed)
	stxn := seed.Doc().WriteTxn()
	sm.Set(stxn, "before", "drop")
	stxn.Commit()

	c, err := client.New(client.Options{
		URL:        url,
		DocName:    "room",
		Doc:        doc.NewDocWithOptions(doc.Options{ClientID: 91}),
		MinBackoff: 20 * time.Millisecond,
		MaxBackoff: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	waitFor(t, "first sync", c.Synced)
	waitFor(t, "c sees seed edit", func() bool { return getKey(c, "before") == "drop" })

	// Hard transport drop: force-close every accepted conn AND shut the
	// server. The client's read pump errors, Synced flips false, and
	// run() enters its backoff loop.
	_ = seed.Close()
	ln.dropAll()
	_ = hs1.Close()
	_ = st1.Close()
	waitFor(t, "client notices drop", func() bool { return !c.Synced() })

	// Bring a fresh server up on the same address with the same store,
	// add an edit the down client has never seen, and let it reconnect.
	ln2 := mustListenOn(t, addr)
	srv2, st2 := newPersistentServer(t, storePath)
	hs2 := &http.Server{Handler: srv2.Handler()}
	go hs2.Serve(ln2)
	t.Cleanup(func() {
		_ = hs2.Close()
		_ = st2.Close()
	})

	other := dial(t, url, "room", 93)
	om := mapOf(other)
	txn := other.Doc().WriteTxn()
	om.Set(txn, "after", "reconnect")
	txn.Commit()

	// The original client reconnects (backoff loop redials srv2) and
	// converges on the edit made while it was down.
	waitFor(t, "client reconnects and syncs", c.Synced)
	waitFor(t, "reconnected client sees new edit", func() bool {
		return getKey(c, "after") == "reconnect"
	})
}

// newPersistentServer starts a server backed by a sqlite file store.
func newPersistentServer(t *testing.T, path string) (*server.Server, *sqlite.Store) {
	t.Helper()
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	srv := server.New(server.Options{OriginPatterns: []string{"*"}, Store: st})
	return srv, st
}

// TestClient_OnErrorOnDialFailure confirms the backoff loop surfaces
// dial failures through OnError rather than silently spinning. The
// client points at a dead address; OnError must fire, and Synced
// stays false.
func TestClient_OnErrorOnDialFailure(t *testing.T) {
	// A bound-then-closed listener yields an address nothing answers.
	ln := mustListen(t)
	addr := ln.Addr().String()
	_ = ln.Close()

	var errs atomic.Int64
	c, err := client.New(client.Options{
		URL:        "ws://" + addr,
		DocName:    "room",
		MinBackoff: 20 * time.Millisecond,
		MaxBackoff: 60 * time.Millisecond,
		OnError:    func(error) { errs.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })

	waitFor(t, "OnError fires on dial failure", func() bool { return errs.Load() > 0 })
	if c.Synced() {
		t.Error("Synced = true while never connected")
	}
}

// TestClient_CloseStopsReconnectLoop confirms Close terminates the
// backoff loop promptly even while it is mid-retry against a dead
// address (no goroutine leak, no hang).
func TestClient_CloseStopsReconnectLoop(t *testing.T) {
	ln := mustListen(t)
	addr := ln.Addr().String()
	_ = ln.Close()

	c, err := client.New(client.Options{
		URL:        "ws://" + addr,
		DocName:    "room",
		MinBackoff: 50 * time.Millisecond,
		MaxBackoff: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() { _ = c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return promptly while reconnecting")
	}
}
