package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Deln0r/ygo/persist/sqlite"
)

// TestOpen_FilePath_PersistsAcrossReopens proves that bytes written
// to an on-disk database survive Close+Open of the same path. This
// is the property that distinguishes the file-backed mode from
// ":memory:" and the only sqlite-specific behaviour that needs its
// own test (memory-mode + interface-contract tests live in
// internal/persist/persist_test.go).
func TestOpen_FilePath_PersistsAcrossReopens(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ygo-test.db")
	ctx := context.Background()

	// Round 1: open, write, close.
	{
		s, err := sqlite.Open(path)
		if err != nil {
			t.Fatalf("first Open: %v", err)
		}
		if err := s.StoreUpdate(ctx, "doc", []byte{0x01, 0x02}); err != nil {
			s.Close()
			t.Fatalf("StoreUpdate: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("first Close: %v", err)
		}
	}

	// Round 2: reopen the same path, read, expect the bytes.
	{
		s, err := sqlite.Open(path)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		defer s.Close()

		got, err := s.GetUpdates(ctx, "doc")
		if err != nil {
			t.Fatalf("GetUpdates: %v", err)
		}
		if len(got) != 1 || len(got[0]) != 2 || got[0][0] != 0x01 || got[0][1] != 0x02 {
			t.Errorf("got %v, want one blob [0x01 0x02]", got)
		}
	}
}

// TestOpen_InvalidPath surfaces sqlite's path validation. A blank
// path produces an empty-name database which modernc.org/sqlite
// accepts (creates an unnamed temp database), so we cannot test
// that as an error. A directory path, however, fails — sqlite
// rejects it because it cannot acquire a file lock on a directory.
func TestOpen_InvalidPath_Directory(t *testing.T) {
	dir := t.TempDir()
	// Pass the directory itself as a "database path" — sqlite will
	// either fail to open or fail on first write.
	s, err := sqlite.Open(dir)
	if err != nil {
		// Acceptable: Open caught the error eagerly via the schema
		// exec. No further assertion needed.
		return
	}
	// Some sqlite builds defer the error to first write. Verify
	// either path leads to a meaningful failure.
	defer s.Close()
	if err := s.StoreUpdate(context.Background(), "doc", []byte{0x01}); err == nil {
		t.Error("Open + StoreUpdate against a directory both succeeded")
	}
}
