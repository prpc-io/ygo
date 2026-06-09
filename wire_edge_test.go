package ygo_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Deln0r/ygo"
)

type wireEdgeFile struct {
	Generator string         `json:"generator"`
	Scenarios []wireEdgeCase `json:"scenarios"`
}

type wireEdgeCase struct {
	Description string         `json:"description"`
	RootName    string         `json:"root_name"`
	RootKind    string         `json:"root_kind"`
	UpdateHex   string         `json:"update_hex"`
	Expected    []any          `json:"expected"`
	ExpectMap   map[string]any `json:"expected_map"`
}

// TestWireEdge_CrossLanguage locks the two byte-exactness edges that
// single-client fixtures never covered: descending delete-set /
// client-struct order in multi-client updates, and wide (> 2^32) client
// IDs. Each yjs-produced update is applied to a fresh doc, its state is
// checked, and it is re-encoded byte-identically.
func TestWireEdge_CrossLanguage(t *testing.T) {
	path := filepath.Join("testdata", "wire-edge-fixtures.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("testdata/wire-edge-fixtures.json not present; run testdata/gen/gen-wire-edge.mjs")
		}
		t.Fatalf("read fixture: %v", err)
	}
	var ff wireEdgeFile
	if err := json.Unmarshal(data, &ff); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ff.Scenarios) == 0 {
		t.Fatal("no scenarios")
	}

	for _, sc := range ff.Scenarios {
		t.Run(sc.Description, func(t *testing.T) {
			want, err := hex.DecodeString(sc.UpdateHex)
			if err != nil {
				t.Fatalf("bad hex: %v", err)
			}

			d := ygo.NewDoc()
			if err := ygo.ApplyUpdate(d, want); err != nil {
				t.Fatalf("ApplyUpdate: %v", err)
			}

			switch sc.RootKind {
			case "array":
				got := ygo.NewArray(d, sc.RootName).ToSlice()
				if len(got) != len(sc.Expected) {
					t.Errorf("array len %d, want %d (%v)", len(got), len(sc.Expected), got)
				} else {
					for i := range got {
						if !valEq(got[i], sc.Expected[i]) {
							t.Errorf("array[%d] = %v, want %v", i, got[i], sc.Expected[i])
						}
					}
				}
			case "map":
				m := ygo.NewMap(d, sc.RootName)
				for k, ev := range sc.ExpectMap {
					if gv := m.Get(k); !valEq(gv, ev) {
						t.Errorf("map[%q] = %v, want %v", k, gv, ev)
					}
				}
			}

			got := ygo.EncodeStateAsUpdate(d)
			if hex.EncodeToString(got) != sc.UpdateHex {
				t.Errorf("re-encode mismatch (client/DS ordering or wide-id)\n got: %s\nwant: %s",
					hex.EncodeToString(got), sc.UpdateHex)
			}
		})
	}
}

// valEq compares a decoded ygo value against a JSON-parsed expected
// value, normalizing the float64-vs-int number representation.
func valEq(g, e any) bool {
	if ef, ok := e.(float64); ok {
		switch gv := g.(type) {
		case int64:
			return float64(gv) == ef
		case float64:
			return gv == ef
		case int:
			return float64(gv) == ef
		}
		return false
	}
	return g == e
}
