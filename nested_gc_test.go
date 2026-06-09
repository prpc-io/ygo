package ygo_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Deln0r/ygo"
)

// TestNestedTypeGC_CrossLanguage verifies that deleting a populated
// nested shared type produces byte-identical V1 update bytes to yjs: the
// reference item becomes a ContentDeleted marker and every child collapses
// into a garbage-collected (ref 0) struct run. Op sequences here mirror
// testdata/gen/gen-nested-gc.mjs, keyed by scenario name.
func TestNestedTypeGC_CrossLanguage(t *testing.T) {
	path := filepath.Join("testdata", "nested-gc-fixtures.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("testdata/nested-gc-fixtures.json not present; run testdata/gen/gen-nested-gc.mjs")
		}
		t.Fatal(err)
	}
	var fx struct {
		Scenarios []struct {
			Name      string `json:"name"`
			ClientID  uint64 `json:"client_id"`
			UpdateHex string `json:"update_hex"`
		} `json:"scenarios"`
	}
	if err := json.Unmarshal(data, &fx); err != nil {
		t.Fatal(err)
	}

	// builders replay the same logical operations as the JS generator.
	builders := map[string]func(d *ygo.Doc){
		"map_in_map": func(d *ygo.Doc) {
			root := ygo.NewMap(d, "root")
			txn := d.WriteTxn()
			child := root.SetMap(txn, "child")
			child.Set(txn, "a", "x")
			child.Set(txn, "b", "y")
			child.Set(txn, "c", "z")
			txn.Commit()
			txn = d.WriteTxn()
			root.Delete(txn, "child")
			txn.Commit()
		},
		"array_in_map": func(d *ygo.Doc) {
			root := ygo.NewMap(d, "root")
			txn := d.WriteTxn()
			child := root.SetArray(txn, "list")
			child.Insert(txn, 0, "p")
			child.Insert(txn, 1, "q")
			child.Insert(txn, 2, "r")
			txn.Commit()
			txn = d.WriteTxn()
			root.Delete(txn, "list")
			txn.Commit()
		},
		"map_in_map_2level": func(d *ygo.Doc) {
			root := ygo.NewMap(d, "root")
			txn := d.WriteTxn()
			outer := root.SetMap(txn, "outer")
			inner := outer.SetMap(txn, "inner")
			inner.Set(txn, "a", "x")
			inner.Set(txn, "b", "y")
			outer.Set(txn, "k", "v")
			txn.Commit()
			txn = d.WriteTxn()
			root.Delete(txn, "outer")
			txn.Commit()
		},
		"map_in_array": func(d *ygo.Doc) {
			root := ygo.NewArray(d, "arr")
			txn := d.WriteTxn()
			m := root.InsertMap(txn, 0)
			m.Set(txn, "a", "x")
			m.Set(txn, "b", "y")
			txn.Commit()
			txn = d.WriteTxn()
			root.Delete(txn, 0, 1)
			txn.Commit()
		},
	}

	for _, sc := range fx.Scenarios {
		sc := sc
		t.Run(sc.Name, func(t *testing.T) {
			build, ok := builders[sc.Name]
			if !ok {
				t.Fatalf("no Go builder for scenario %q", sc.Name)
			}
			d := ygo.NewDocWithOptions(ygo.Options{ClientID: sc.ClientID})
			build(d)
			got := hex.EncodeToString(ygo.EncodeStateAsUpdate(d))
			if got != sc.UpdateHex {
				t.Errorf("scenario %s mismatch:\n go = %s\n js = %s", sc.Name, got, sc.UpdateHex)
			}
		})
	}
}
