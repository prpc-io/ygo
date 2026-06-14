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

// TestClient_ReconnectsAfterServerDrop exercises the run() backoff
// loop: a client syncs, its transport is force-dropped, the read pump
// errors, Synced flips false, and the backoff loop redials the still-
// running server and re-syncs. The single most load-bearing path in
// the package and was previously uncovered.
//
// The server stays up the whole test (only the connection is dropped),
// so this is deterministic: no port reuse, no shared on-disk store
// across instances, just a transport-level drop and recovery.
func TestClient_ReconnectsAfterServerDrop(t *testing.T) {
	ln := mustListen(t)
	url := "ws://" + ln.Addr().String()

	srv := server.New(server.Options{OriginPatterns: []string{"*"}})
	hs := &http.Server{Handler: srv.Handler()}
	go hs.Serve(ln)
	t.Cleanup(func() {
		_ = hs.Close()
		_ = srv.Close(context.Background())
	})

	c, err := client.New(client.Options{
		URL:        url,
		DocName:    "room",
		Doc:        doc.NewDocWithOptions(doc.Options{ClientID: 91}),
		MinBackoff: 100 * time.Millisecond,
		MaxBackoff: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	waitFor(t, "first sync", c.Synced)

	// Hard transport drop: force-close every accepted connection so the
	// client's read pump errors and run() enters its backoff loop. The
	// server keeps listening.
	ln.dropAll()
	waitFor(t, "client notices drop", func() bool { return !c.Synced() })

	// The backoff loop redials the still-running server and re-handshakes.
	waitFor(t, "client reconnects and re-syncs", c.Synced)

	// And the reconnected client is functional: an edit it makes now
	// round-trips through the server to a second client.
	other := dial(t, url, "room", 93)
	cm := mapOf(c)
	txn := c.Doc().WriteTxn()
	cm.Set(txn, "after", "reconnect")
	txn.Commit()
	waitFor(t, "edit after reconnect reaches peer", func() bool {
		return getKey(other, "after") == "reconnect"
	})
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
