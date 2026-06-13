package gomobile_test

import (
	"testing"

	"github.com/Deln0r/ygo/gomobile"
)

// TestText_InsertDelete covers the editable text surface including the
// previously-untested DeleteAt and Length.
func TestText_InsertDelete(t *testing.T) {
	d := gomobile.NewDoc()
	txt := d.Text("note")
	if err := txt.InsertAt(0, "hello world"); err != nil {
		t.Fatal(err)
	}
	if got := txt.Length(); got != 11 {
		t.Errorf("Length = %d, want 11", got)
	}
	if err := txt.DeleteAt(5, 6); err != nil { // drop " world"
		t.Fatal(err)
	}
	if got := txt.String(); got != "hello" {
		t.Errorf("after delete = %q, want hello", got)
	}
	if got := txt.Length(); got != 5 {
		t.Errorf("Length = %d, want 5", got)
	}
	// Negative arguments are rejected, not panicked.
	if err := txt.InsertAt(-1, "x"); err == nil {
		t.Error("InsertAt(-1) did not error")
	}
	if err := txt.DeleteAt(0, -1); err == nil {
		t.Error("DeleteAt(_, -1) did not error")
	}
}

// TestMap_StringOps covers SetString/GetString plus the previously
// untested Has, DeleteKey, and Len.
func TestMap_StringOps(t *testing.T) {
	d := gomobile.NewDoc()
	m := d.Map("meta")
	if m.Len() != 0 || m.Has("k") {
		t.Fatal("fresh map is not empty")
	}
	m.SetString("title", "shopping")
	m.SetString("owner", "ian")
	if m.Len() != 2 {
		t.Errorf("Len = %d, want 2", m.Len())
	}
	if !m.Has("title") {
		t.Error("Has(title) = false")
	}
	if got := m.GetString("title"); got != "shopping" {
		t.Errorf("GetString = %q, want shopping", got)
	}
	m.DeleteKey("title")
	if m.Has("title") {
		t.Error("Has(title) = true after delete")
	}
	if m.Len() != 1 {
		t.Errorf("Len = %d after delete, want 1", m.Len())
	}
	if got := m.GetString("missing"); got != "" {
		t.Errorf("GetString(missing) = %q, want empty", got)
	}
}

// TestTextUndoManager covers Undo plus the previously untested
// CanRedo / Redo / StopCapturing.
func TestTextUndoManager(t *testing.T) {
	d := gomobile.NewDoc()
	txt := d.Text("note")
	um := d.NewTextUndoManager("note")
	defer um.Close()

	if err := txt.InsertAt(0, "abc"); err != nil {
		t.Fatal(err)
	}
	um.StopCapturing() // close the group so the next edit is separate
	if err := txt.InsertAt(3, "def"); err != nil {
		t.Fatal(err)
	}

	if !um.CanUndo() {
		t.Fatal("CanUndo = false after edits")
	}
	if um.CanRedo() {
		t.Error("CanRedo = true before any undo")
	}
	if !um.Undo() { // undo "def"
		t.Fatal("Undo failed")
	}
	if got := txt.String(); got != "abc" {
		t.Errorf("after undo = %q, want abc (StopCapturing kept groups separate)", got)
	}
	if !um.CanRedo() {
		t.Fatal("CanRedo = false after undo")
	}
	if !um.Redo() {
		t.Fatal("Redo failed")
	}
	if got := txt.String(); got != "abcdef" {
		t.Errorf("after redo = %q, want abcdef", got)
	}
}

// TestMapUndoManager covers the NewMapUndoManager constructor path.
func TestMapUndoManager(t *testing.T) {
	d := gomobile.NewDoc()
	m := d.Map("meta")
	um := d.NewMapUndoManager("meta")
	defer um.Close()

	m.SetString("k", "v1")
	if !um.CanUndo() {
		t.Fatal("CanUndo = false after a map set")
	}
	if !um.Undo() {
		t.Fatal("Undo failed")
	}
	if m.Has("k") {
		t.Errorf("key still present after undo: %q", m.GetString("k"))
	}
}

// TestCursor_RoundTrip covers EncodeCursor / ResolveCursor locally.
func TestCursor_RoundTrip(t *testing.T) {
	d := gomobile.NewDoc()
	txt := d.Text("note")
	if err := txt.InsertAt(0, "abcdef"); err != nil {
		t.Fatal(err)
	}
	cur, err := txt.EncodeCursor(3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := txt.ResolveCursor(cur); got != 3 {
		t.Errorf("ResolveCursor = %d, want 3", got)
	}
	// Insert before the anchor shifts it.
	if err := txt.InsertAt(0, "XY"); err != nil {
		t.Fatal(err)
	}
	if got := txt.ResolveCursor(cur); got != 5 {
		t.Errorf("ResolveCursor after prepend = %d, want 5", got)
	}
	// Garbage / foreign bytes resolve to -1, not a panic.
	if got := txt.ResolveCursor([]byte{0xff, 0xff}); got != -1 {
		t.Errorf("ResolveCursor(garbage) = %d, want -1", got)
	}
}
