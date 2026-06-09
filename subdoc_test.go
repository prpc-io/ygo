package ygo_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Deln0r/ygo"
)

// TestSubdoc_SetGet_RoundTrip nests a subdoc, syncs the parent to a
// fresh replica, and confirms the subdoc reference (its GUID) survives.
func TestSubdoc_SetGet_RoundTrip(t *testing.T) {
	d := ygo.NewDoc()
	m := ygo.NewMap(d, "m")

	txn := d.WriteTxn()
	sub := m.SetDoc(txn, "child")
	txn.Commit()

	if sub.GUID() == "" {
		t.Fatal("subdoc has no GUID")
	}
	// Same key returns the same registered handle.
	got, ok := m.GetDoc(d, "child")
	if !ok || got.GUID() != sub.GUID() {
		t.Fatalf("GetDoc local: ok=%v guid=%v want %v", ok, got.GUID(), sub.GUID())
	}

	// Sync to a fresh replica and read the reference back.
	d2 := ygo.NewDoc()
	if err := ygo.ApplyUpdate(d2, ygo.EncodeStateAsUpdate(d)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	m2 := ygo.NewMap(d2, "m")
	got2, ok := m2.GetDoc(d2, "child")
	if !ok {
		t.Fatal("GetDoc after sync: not found")
	}
	if got2.GUID() != sub.GUID() {
		t.Errorf("synced GUID = %q, want %q", got2.GUID(), sub.GUID())
	}
}

// TestSubdoc_Missing confirms GetDoc on absent or non-doc keys.
func TestSubdoc_Missing(t *testing.T) {
	d := ygo.NewDoc()
	m := ygo.NewMap(d, "m")
	txn := d.WriteTxn()
	m.Set(txn, "scalar", "v")
	txn.Commit()

	if _, ok := m.GetDoc(d, "absent"); ok {
		t.Error("GetDoc on absent key returned ok")
	}
	if _, ok := m.GetDoc(d, "scalar"); ok {
		t.Error("GetDoc on scalar key returned ok")
	}
}

// TestSubdoc_TwoSubdocs nests two distinct subdocs and checks their
// GUIDs are independent and stable across a sync.
func TestSubdoc_TwoSubdocs(t *testing.T) {
	d := ygo.NewDoc()
	m := ygo.NewMap(d, "m")
	txn := d.WriteTxn()
	a := m.SetDoc(txn, "a")
	b := m.SetDoc(txn, "b")
	txn.Commit()
	if a.GUID() == b.GUID() {
		t.Fatal("distinct subdocs share a GUID")
	}

	d2 := ygo.NewDoc()
	if err := ygo.ApplyUpdate(d2, ygo.EncodeStateAsUpdate(d)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	m2 := ygo.NewMap(d2, "m")
	ga, _ := m2.GetDoc(d2, "a")
	gb, _ := m2.GetDoc(d2, "b")
	if ga.GUID() != a.GUID() || gb.GUID() != b.GUID() {
		t.Errorf("synced GUIDs mismatch: a=%v/%v b=%v/%v", ga.GUID(), a.GUID(), gb.GUID(), b.GUID())
	}
}

type subdocFixtureFile struct {
	Generator string          `json:"generator"`
	Scenarios []subdocFixture `json:"scenarios"`
}

type subdocFixture struct {
	Description string            `json:"description"`
	RootName    string            `json:"root_name"`
	UpdateHex   string            `json:"update_hex"`
	ExpectGUID  map[string]string `json:"expect_guids"`
}

// TestSubdoc_CrossLanguage proves ygo's ContentDoc codec is byte-
// compatible with yjs@13.6.31. For each yjs-produced parent update we:
//  1. ApplyUpdate to a fresh doc.
//  2. Read each subdoc GUID back via Map.GetDoc and check it.
//  3. Re-encode the doc and assert byte-identical output to the input.
func TestSubdoc_CrossLanguage(t *testing.T) {
	path := filepath.Join("testdata", "subdoc-fixtures.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("testdata/subdoc-fixtures.json not present; run testdata/gen/gen-subdoc.mjs")
		}
		t.Fatalf("read fixture: %v", err)
	}
	var ff subdocFixtureFile
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

			m := ygo.NewMap(d, sc.RootName)
			for key, guid := range sc.ExpectGUID {
				sub, ok := m.GetDoc(d, key)
				if !ok {
					t.Errorf("GetDoc(%q): not found", key)
					continue
				}
				if sub.GUID() != guid {
					t.Errorf("GetDoc(%q) GUID = %q, want %q", key, sub.GUID(), guid)
				}
			}

			got := ygo.EncodeStateAsUpdate(d)
			if hex.EncodeToString(got) != sc.UpdateHex {
				t.Errorf("re-encode mismatch\n got: %s\nwant: %s",
					hex.EncodeToString(got), sc.UpdateHex)
			}
		})
	}
}
