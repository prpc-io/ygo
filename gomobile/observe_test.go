package gomobile_test

import (
	"encoding/json"
	"testing"

	"github.com/Deln0r/ygo/gomobile"
)

type textRecorder struct{ deltas [][]byte }

func (r *textRecorder) OnTextChange(d []byte) { r.deltas = append(r.deltas, d) }

type mapRecorder struct{ keys [][]byte }

func (r *mapRecorder) OnMapChange(d []byte) { r.keys = append(r.keys, d) }

// TestText_ObserveChanges confirms a mobile text listener receives a
// Quill-style delta as JSON that a native editor can apply directly.
func TestText_ObserveChanges(t *testing.T) {
	d := gomobile.NewDoc()
	txt := d.Text("note")
	rec := &textRecorder{}
	txt.ObserveChanges(rec)

	if err := txt.InsertAt(0, "hello"); err != nil {
		t.Fatal(err)
	}
	if len(rec.deltas) != 1 {
		t.Fatalf("got %d deltas, want 1", len(rec.deltas))
	}
	var ops []map[string]any
	if err := json.Unmarshal(rec.deltas[0], &ops); err != nil {
		t.Fatalf("delta is not valid JSON: %v", err)
	}
	if len(ops) != 1 || ops[0]["insert"] != "hello" {
		t.Errorf("delta = %v, want [{insert:hello}]", ops)
	}

	// A formatted retain carries attributes.
	rec.deltas = nil
	cur, _ := txt.EncodeCursor(0, 0) // touch to ensure no interference
	_ = cur
	// Re-register replaces the listener (no double fire).
	rec2 := &textRecorder{}
	txt.ObserveChanges(rec2)
	if err := txt.InsertAt(5, "!"); err != nil {
		t.Fatal(err)
	}
	if len(rec.deltas) != 0 {
		t.Errorf("old listener fired %d times after replace, want 0", len(rec.deltas))
	}
	if len(rec2.deltas) != 1 {
		t.Fatalf("new listener got %d, want 1", len(rec2.deltas))
	}
	var ops2 []map[string]any
	_ = json.Unmarshal(rec2.deltas[0], &ops2)
	if len(ops2) != 2 || ops2[0]["retain"] == nil || ops2[1]["insert"] != "!" {
		t.Errorf("delta = %v, want [{retain:5},{insert:!}]", ops2)
	}
}

// TestMap_ObserveChanges confirms a mobile map listener receives the
// changed keys as JSON.
func TestMap_ObserveChanges(t *testing.T) {
	d := gomobile.NewDoc()
	m := d.Map("meta")
	rec := &mapRecorder{}
	m.ObserveChanges(rec)

	m.SetString("title", "draft")
	if len(rec.keys) != 1 {
		t.Fatalf("got %d events, want 1", len(rec.keys))
	}
	var got map[string]map[string]any
	if err := json.Unmarshal(rec.keys[0], &got); err != nil {
		t.Fatalf("keys not valid JSON: %v", err)
	}
	if got["title"]["action"] != "add" {
		t.Errorf("title action = %v, want add", got["title"]["action"])
	}

	m.SetString("title", "final")
	var upd map[string]map[string]any
	_ = json.Unmarshal(rec.keys[1], &upd)
	if upd["title"]["action"] != "update" || upd["title"]["oldValue"] != "draft" {
		t.Errorf("update entry = %v, want {update, draft}", upd["title"])
	}
}
