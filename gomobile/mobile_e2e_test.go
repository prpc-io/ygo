package gomobile_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Deln0r/ygo/gomobile"
	"github.com/Deln0r/ygo/server"
)

func startServer(t *testing.T) string {
	t.Helper()
	srv := server.New(server.Options{OriginPatterns: []string{"*"}})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		_ = srv.Close(context.Background())
	})
	return "ws://" + strings.TrimPrefix(ts.URL, "http://")
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// recorder implements gomobile.Listener.
type recorder struct {
	synced  atomic.Bool
	changes atomic.Int64
}

func (r *recorder) OnSynced(v bool)    { r.synced.Store(v) }
func (r *recorder) OnDocChanged()      { r.changes.Add(1) }
func (r *recorder) OnError(msg string) {}

// TestMobile_EndToEnd drives the full mobile API shape: two
// gomobile-bound clients sync text edits, cursors, and undo through a
// real server, exactly as a Swift / Kotlin app would.
func TestMobile_EndToEnd(t *testing.T) {
	url := startServer(t)

	da := gomobile.NewDocWithClientID(71)
	ca := gomobile.NewClient(url, "notes", da)
	recA := &recorder{}
	ca.SetListener(recA)
	if err := ca.Connect(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ca.Close() })

	db := gomobile.NewDocWithClientID(72)
	cb := gomobile.NewClient(url, "notes", db)
	if err := cb.Connect(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cb.Close() })

	waitFor(t, "a synced", ca.Synced)
	waitFor(t, "b synced", cb.Synced)
	if !recA.synced.Load() {
		t.Error("listener OnSynced(true) not delivered")
	}

	// A types; B sees it.
	ta := da.Text("note")
	if err := ta.InsertAt(0, "hello"); err != nil {
		t.Fatal(err)
	}
	tb := db.Text("note")
	waitFor(t, "b sees hello", func() bool { return tb.String() == "hello" })
	if recA.changes.Load() == 0 {
		t.Error("listener OnDocChanged never fired")
	}

	// A's cursor anchor resolves on B, and survives a concurrent
	// prepend on B.
	cursor, err := ta.EncodeCursor(5, 0) // end of "hello"
	if err != nil {
		t.Fatal(err)
	}
	if got := tb.ResolveCursor(cursor); got != 5 {
		t.Errorf("b resolves cursor to %d, want 5", got)
	}
	if err := tb.InsertAt(0, ">> "); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a sees prepend", func() bool { return ta.String() == ">> hello" })
	if got := tb.ResolveCursor(cursor); got != 8 {
		t.Errorf("cursor after prepend = %d, want 8", got)
	}

	// Undo on A rolls back A's last edit and propagates.
	ua := da.NewTextUndoManager("note")
	defer ua.Close()
	if err := ta.InsertAt(8, "!!!"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "b sees !!!", func() bool { return tb.String() == ">> hello!!!" })
	if !ua.CanUndo() {
		t.Fatal("CanUndo = false after a captured edit")
	}
	if !ua.Undo() {
		t.Fatal("Undo failed")
	}
	waitFor(t, "b sees undo", func() bool { return tb.String() == ">> hello" })

	// Map round-trip.
	da.Map("meta").SetString("title", "shopping")
	mb := db.Map("meta")
	waitFor(t, "b sees title", func() bool { return mb.GetString("title") == "shopping" })
}
