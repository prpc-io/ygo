package types

import (
	"testing"

	"github.com/Deln0r/ygo/internal/block"
	"github.com/Deln0r/ygo/internal/doc"
)

func TestText_Empty(t *testing.T) {
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))
	if got := tx.Length(); got != 0 {
		t.Errorf("empty Length = %d, want 0", got)
	}
	if got := tx.String(); got != "" {
		t.Errorf("empty String = %q, want \"\"", got)
	}
}

func TestText_InsertAtZero(t *testing.T) {
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	if err := tx.Insert(wtxn, 0, "hello"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	wtxn.Commit()

	if got := tx.String(); got != "hello" {
		t.Errorf("String = %q, want hello", got)
	}
	if got := tx.Length(); got != 5 {
		t.Errorf("Length = %d, want 5", got)
	}
}

func TestText_AppendAtEnd(t *testing.T) {
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	if err := tx.Insert(wtxn, 0, "hello"); err != nil {
		t.Fatalf("insert hello: %v", err)
	}
	if err := tx.Insert(wtxn, 5, " world"); err != nil {
		t.Fatalf("insert world: %v", err)
	}
	wtxn.Commit()

	if got := tx.String(); got != "hello world" {
		t.Errorf("String = %q, want %q", got, "hello world")
	}
	if got := tx.Length(); got != 11 {
		t.Errorf("Length = %d, want 11", got)
	}
}

func TestText_InsertInMiddle_SplitsExistingItem(t *testing.T) {
	// Insert "helloworld" as one big Item (Push-style — single
	// Insert with one string), then Insert " " at idx=5. Should
	// split the original Item at byte/utf16 5, then integrate the
	// new " " between the two halves.
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	if err := tx.Insert(wtxn, 0, "helloworld"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := tx.Insert(wtxn, 5, " "); err != nil {
		t.Fatalf("middle insert: %v", err)
	}
	wtxn.Commit()

	if got := tx.String(); got != "hello world" {
		t.Errorf("String = %q, want %q", got, "hello world")
	}
	if got := tx.Length(); got != 11 {
		t.Errorf("Length = %d, want 11", got)
	}
}

func TestText_InsertOutOfRangeReturnsError(t *testing.T) {
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	defer wtxn.Commit()
	if err := tx.Insert(wtxn, 5, "x"); err == nil {
		t.Error("Insert(5) on empty text should error")
	}
}

func TestText_DeleteSingleChar(t *testing.T) {
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	tx.Insert(wtxn, 0, "hello")
	if err := tx.Delete(wtxn, 2, 1); err != nil {
		t.Fatalf("delete: %v", err)
	}
	wtxn.Commit()

	if got := tx.String(); got != "helo" {
		t.Errorf("String = %q, want helo", got)
	}
	if got := tx.Length(); got != 4 {
		t.Errorf("Length = %d, want 4", got)
	}
}

func TestText_DeleteRange(t *testing.T) {
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	tx.Insert(wtxn, 0, "hello world")
	if err := tx.Delete(wtxn, 5, 1); err != nil {
		t.Fatalf("delete space: %v", err)
	}
	wtxn.Commit()

	if got := tx.String(); got != "helloworld" {
		t.Errorf("String = %q, want helloworld", got)
	}

	wtxn = d.WriteTxn()
	if err := tx.Delete(wtxn, 0, 5); err != nil {
		t.Fatalf("delete hello: %v", err)
	}
	wtxn.Commit()

	if got := tx.String(); got != "world" {
		t.Errorf("String = %q, want world", got)
	}
}

func TestText_DeleteEntire(t *testing.T) {
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	tx.Insert(wtxn, 0, "hello")
	if err := tx.Delete(wtxn, 0, 5); err != nil {
		t.Fatalf("delete all: %v", err)
	}
	wtxn.Commit()

	if got := tx.String(); got != "" {
		t.Errorf("String after delete-all = %q, want \"\"", got)
	}
	if got := tx.Length(); got != 0 {
		t.Errorf("Length after delete-all = %d, want 0", got)
	}
}

func TestText_DeleteOutOfRangeReturnsError(t *testing.T) {
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	defer wtxn.Commit()
	tx.Insert(wtxn, 0, "hi")
	if err := tx.Delete(wtxn, 0, 10); err == nil {
		t.Error("Delete(0, 10) on text of length 2 should error")
	}
}

func TestText_NonBMP_LengthInUTF16(t *testing.T) {
	// "a😀b": a (1 UTF-16) + 😀 (2 UTF-16, surrogate pair) + b (1) = 4.
	// Verifies Content.Len(KindString) was correctly switched to
	// UTF-16 code units (was previously byte count, would have
	// returned 6 for "a😀b" given UTF-8 bytes).
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	if err := tx.Insert(wtxn, 0, "a😀b"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	wtxn.Commit()

	if got := tx.String(); got != "a😀b" {
		t.Errorf("String = %q, want %q", got, "a😀b")
	}
	if got := tx.Length(); got != 4 {
		t.Errorf("Length = %d, want 4 (a=1, 😀=2, b=1 in UTF-16)", got)
	}
}

func TestText_InsertBetweenBMPAndNonBMP(t *testing.T) {
	// Insert "a😀c" then Insert "B" at idx=1 (between a and 😀).
	// Result: "aB😀c", Length 5.
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	tx.Insert(wtxn, 0, "a😀c")
	if err := tx.Insert(wtxn, 1, "B"); err != nil {
		t.Fatalf("middle insert: %v", err)
	}
	wtxn.Commit()

	if got := tx.String(); got != "aB😀c" {
		t.Errorf("String = %q, want %q", got, "aB😀c")
	}
	if got := tx.Length(); got != 5 {
		t.Errorf("Length = %d, want 5", got)
	}
}

func TestText_InsertSurrogateSplit_UsesU_FFFD(t *testing.T) {
	// Insert "a😀c" (Length 4) then Insert "X" at idx=2. Index 2
	// lands inside 😀's surrogate pair. Per JS Yjs semantics
	// (utf16.SplitAt's U+FFFD replacement), the original 😀 splits
	// into "" + U+FFFD on the left half and U+FFFD + "" on the
	// right half. Result string: "a" + "�" + "X" + "�" + "c".
	// Length still 5 (1+1+1+1+1).
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	tx.Insert(wtxn, 0, "a😀c")
	if err := tx.Insert(wtxn, 2, "X"); err != nil {
		t.Fatalf("mid-surrogate insert: %v", err)
	}
	wtxn.Commit()

	want := "a�X�c"
	if got := tx.String(); got != want {
		t.Errorf("String = %q, want %q (mid-surrogate split → U+FFFD per JS Yjs)", got, want)
	}
	if got := tx.Length(); got != 5 {
		t.Errorf("Length = %d, want 5", got)
	}
}

func TestText_InsertWalksPastTombstones(t *testing.T) {
	// Build "hello", delete "ll" (idx 2 len 2), insert "X" at idx 2.
	// Per text.rs:219-225 the insert must walk past the tombstoned
	// "ll" and attach to the live successor "o". Visible text: "heXo".
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	tx.Insert(wtxn, 0, "hello")
	tx.Delete(wtxn, 2, 2)
	if err := tx.Insert(wtxn, 2, "X"); err != nil {
		t.Fatalf("insert after tombstones: %v", err)
	}
	wtxn.Commit()

	if got := tx.String(); got != "heXo" {
		t.Errorf("String = %q, want heXo", got)
	}
	if got := tx.Length(); got != 4 {
		t.Errorf("Length = %d, want 4", got)
	}
}

func TestText_BranchAccessor(t *testing.T) {
	d := doc.NewDoc()
	b := d.Branch("x")
	tx := NewText(b)
	if tx.Branch() != b {
		t.Error("Branch() should return wrapped branch")
	}
}

func TestText_ContentLenAfterInsert(t *testing.T) {
	// Sanity: after a single Insert, the head item's Content.Len
	// returns the UTF-16 length, and Item.Len matches it.
	d := doc.NewDoc()
	tx := NewText(d.Branch("x"))

	wtxn := d.WriteTxn()
	tx.Insert(wtxn, 0, "a😀b")
	wtxn.Commit()

	head := tx.branch.Start
	if head == nil {
		t.Fatal("branch.Start is nil")
	}
	if head.Len != 4 {
		t.Errorf("head.Len = %d, want 4 (UTF-16 units)", head.Len)
	}
	if got := head.Content.Len(block.OffsetUtf16); got != 4 {
		t.Errorf("head.Content.Len = %d, want 4", got)
	}
}
