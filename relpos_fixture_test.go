package ygo_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Deln0r/ygo"
)

type relposFixtureFile struct {
	Generator string           `json:"generator"`
	Scenarios []relposScenario `json:"scenarios"`
}

type relposScenario struct {
	Description         string       `json:"description"`
	Root                string       `json:"root"`
	RootKind            string       `json:"root_kind"`
	Create              relposCreate `json:"create"`
	RposHex             string       `json:"rpos_hex"`
	UpdateBeforeHex     string       `json:"update_before_hex"`
	ExpectedIndexBefore *uint64      `json:"expected_index_before"`
	UpdateAfterHex      string       `json:"update_after_hex"`
	ExpectedIndexAfter  *uint64      `json:"expected_index_after"`
}

type relposCreate struct {
	Path  []string `json:"path"`
	Index uint64   `json:"index"`
	Assoc int64    `json:"assoc"`
}

// resolveScopeType walks root + path to the shared-type wrapper the
// scenario anchors in.
func resolveScopeType(t *testing.T, d *ygo.Doc, sc relposScenario) ygo.UndoScope {
	t.Helper()
	switch sc.RootKind {
	case "text":
		return ygo.NewText(d, sc.Root)
	case "array":
		return ygo.NewArray(d, sc.Root)
	case "map":
		m := ygo.NewMap(d, sc.Root)
		if len(sc.Create.Path) == 0 {
			return m
		}
		v := m.Get(sc.Create.Path[0])
		txt, ok := v.(*ygo.Text)
		if !ok {
			t.Fatalf("path %v: got %T, want *ygo.Text", sc.Create.Path, v)
		}
		return txt
	default:
		t.Fatalf("unknown root_kind %q", sc.RootKind)
		return nil
	}
}

// TestRelativePosition_Fixtures verifies, against yjs@13.6.31-captured
// fixtures: (1) Go creation from (type, index, assoc) is byte-identical
// to Y.encodeRelativePosition, (2) decode→encode round-trips the JS
// bytes, (3) resolution matches yjs before and after concurrent edits.
func TestRelativePosition_Fixtures(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "relpos-fixtures.json"))
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("testdata/relpos-fixtures.json not present; run testdata/gen/gen-relpos.mjs")
		}
		t.Fatal(err)
	}
	var f relposFixtureFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	for _, sc := range f.Scenarios {
		t.Run(sc.Description, func(t *testing.T) {
			jsBytes, err := hex.DecodeString(sc.RposHex)
			if err != nil {
				t.Fatal(err)
			}

			// Decode -> re-encode round-trips the JS bytes exactly.
			rpos, err := ygo.DecodeRelativePosition(jsBytes)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			got, err := ygo.EncodeRelativePosition(rpos)
			if err != nil {
				t.Fatalf("re-encode: %v", err)
			}
			if !bytes.Equal(got, jsBytes) {
				t.Errorf("re-encode mismatch:\n got %x\nwant %x", got, jsBytes)
			}

			// Build the pre-edit doc, create the same rpos in Go: byte parity.
			before, err := hex.DecodeString(sc.UpdateBeforeHex)
			if err != nil {
				t.Fatal(err)
			}
			d := ygo.NewDoc()
			if err := ygo.ApplyUpdate(d, before); err != nil {
				t.Fatalf("apply before: %v", err)
			}
			scope := resolveScopeType(t, d, sc)
			created, err := ygo.CreateRelativePositionFromTypeIndex(scope, sc.Create.Index, sc.Create.Assoc)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			createdBytes, err := ygo.EncodeRelativePosition(created)
			if err != nil {
				t.Fatalf("encode created: %v", err)
			}
			if !bytes.Equal(createdBytes, jsBytes) {
				t.Errorf("created rpos bytes mismatch:\n got %x\nwant %x", createdBytes, jsBytes)
			}

			// Resolution on the pre-edit doc.
			checkResolve(t, d, rpos, sc.ExpectedIndexBefore, "before")

			// Apply the post-edit state and resolve again.
			after, err := hex.DecodeString(sc.UpdateAfterHex)
			if err != nil {
				t.Fatal(err)
			}
			if err := ygo.ApplyUpdate(d, after); err != nil {
				t.Fatalf("apply after: %v", err)
			}
			checkResolve(t, d, rpos, sc.ExpectedIndexAfter, "after")
		})
	}
}

func checkResolve(t *testing.T, d *ygo.Doc, rpos ygo.RelativePosition, want *uint64, stage string) {
	t.Helper()
	abs, ok := ygo.CreateAbsolutePositionFromRelativePosition(d, rpos)
	if want == nil {
		if ok {
			t.Errorf("%s: resolved to %d, want unresolvable", stage, abs.Index)
		}
		return
	}
	if !ok {
		t.Errorf("%s: unresolvable, want index %d", stage, *want)
		return
	}
	if abs.Index != *want {
		t.Errorf("%s: index = %d, want %d", stage, abs.Index, *want)
	}
}
