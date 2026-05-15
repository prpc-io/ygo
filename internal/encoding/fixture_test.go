package encoding

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/types"
)

// fixtureFile mirrors the JSON shape emitted by testdata/gen/gen-yjs-update.mjs.
type fixtureFile struct {
	Generator string            `json:"generator"`
	Scenarios []fixtureScenario `json:"scenarios"`
}

type fixtureScenario struct {
	Description string                 `json:"description"`
	JsClientID  uint64                 `json:"js_client_id"`
	RootName    string                 `json:"root_name"`
	UpdateHex   string                 `json:"update_hex"`
	ExpectedMap map[string]interface{} `json:"expected_map"`
}

// TestFixtures_DecodeApplyJSYjsUpdates is the binary-protocol-compat
// proof. For each scenario captured by gen-yjs-update.mjs we:
//  1. Decode the JS-emitted update bytes via our DecodeUpdate.
//  2. Apply to a fresh Doc + Map via our Update.Apply.
//  3. Read every expected key back via our Map.Get.
//
// If everything matches we have proven that bytes JS Yjs produces
// are bytes our pipeline correctly consumes — direction one of the
// prime directive. Direction two (Go encodes, JS decodes) is the
// follow-on test below if/when we wire a Node subprocess into the
// Go test runner.
//
// Skipped if the fixture file is absent (fresh checkout without Node);
// CI regenerates via gen-yjs-update.mjs before running tests.
func TestFixtures_DecodeApplyJSYjsUpdates(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "yjs-updates.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("testdata/yjs-updates.json not present; run testdata/gen/gen-yjs-update.mjs to regenerate")
		}
		t.Fatalf("read fixture: %v", err)
	}

	var ff fixtureFile
	if err := json.Unmarshal(data, &ff); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if len(ff.Scenarios) == 0 {
		t.Fatal("fixture file has no scenarios")
	}

	for _, sc := range ff.Scenarios {
		t.Run(sc.Description, func(t *testing.T) {
			updateBytes, err := hex.DecodeString(sc.UpdateHex)
			if err != nil {
				t.Fatalf("invalid hex: %v", err)
			}

			// Decode the JS-emitted update.
			u, tail, err := DecodeUpdate(updateBytes)
			if err != nil {
				t.Fatalf("DecodeUpdate failed: %v", err)
			}
			if len(tail) != 0 {
				t.Errorf("DecodeUpdate left %d trailing bytes: % x", len(tail), tail)
			}

			// Apply to a fresh Doc.
			d := doc.NewDoc()
			m := types.NewMap(d.Branch(sc.RootName))

			txn := d.WriteTxn()
			if err := u.Apply(txn); err != nil {
				t.Fatalf("Apply failed: %v", err)
			}
			txn.Commit()

			// Verify every expected key matches.
			for key, expected := range sc.ExpectedMap {
				got := m.Get(key)
				if !valueEqual(got, expected) {
					t.Errorf("key %q: got %v (%T), want %v (%T)", key, got, got, expected, expected)
				}
			}

			// Verify no unexpected live keys exist.
			gotKeys := map[string]struct{}{}
			m.Range(func(k string, _ any) bool {
				gotKeys[k] = struct{}{}
				return true
			})
			if len(gotKeys) != len(sc.ExpectedMap) {
				t.Errorf("Map.Range visited %d live keys; want %d. seen=%v expected=%v",
					len(gotKeys), len(sc.ExpectedMap), keysOf(gotKeys), keysOfStr(sc.ExpectedMap))
			}
			for k := range sc.ExpectedMap {
				if _, ok := gotKeys[k]; !ok {
					t.Errorf("expected key %q not visible in Map.Range", k)
				}
			}
		})
	}
}

// valueEqual compares two values across the JSON / Go-Any boundary.
// JSON unmarshals integers as float64 by default; our DecodeAny
// produces int64 for tag 125 and float64 for tag 123. Compare
// numerically rather than by type when one side came from JSON.
func valueEqual(got, expected any) bool {
	if got == nil && expected == nil {
		return true
	}
	if got == nil || expected == nil {
		return false
	}
	// Numeric cross-type comparison.
	gotF, gotIsNum := toFloat(got)
	expF, expIsNum := toFloat(expected)
	if gotIsNum && expIsNum {
		return gotF == expF
	}
	return got == expected
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func keysOfStr(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
