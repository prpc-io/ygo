package types_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/Deln0r/ygo/internal/doc"
	"github.com/Deln0r/ygo/internal/encoding"
	"github.com/Deln0r/ygo/internal/types"
)

// xmlFixture mirrors testdata/yjs-xml-fixtures.json (gen-xml.mjs).
type xmlFixture struct {
	Description string `json:"description"`
	JSClientID  uint64 `json:"js_client_id"`
	RootName    string `json:"root_name"`
	UpdateHex   string `json:"update_hex"`
	ExpectedXML string `json:"expected_xml"`
}

func loadXMLFixtures(t *testing.T) []xmlFixture {
	t.Helper()
	candidates := []string{
		"../../testdata/yjs-xml-fixtures.json",
		"../../../testdata/yjs-xml-fixtures.json",
	}
	var raw []byte
	var err error
	for _, p := range candidates {
		raw, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("read yjs-xml-fixtures.json: %v", err)
	}
	var fixtures []xmlFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatalf("parse yjs-xml-fixtures.json: %v", err)
	}
	return fixtures
}

// TestFixtures_DecodeApplyJSXmlUpdates is the cross-language proof:
// bytes produced by Y.encodeStateAsUpdate over a Y.XmlFragment must
// decode + apply in Go and yield a ToString() output matching the
// JS-side render.
func TestFixtures_DecodeApplyJSXmlUpdates(t *testing.T) {
	fixtures := loadXMLFixtures(t)
	if len(fixtures) == 0 {
		t.Fatal("no XML fixtures loaded")
	}
	for _, fix := range fixtures {
		fix := fix
		t.Run(fix.Description, func(t *testing.T) {
			bytes, err := hex.DecodeString(fix.UpdateHex)
			if err != nil {
				t.Fatalf("hex: %v", err)
			}
			d := doc.NewDoc()
			if err := encoding.ApplyUpdate(d, bytes); err != nil {
				t.Fatalf("ApplyUpdate: %v", err)
			}
			frag := types.NewXmlFragment(d.Branch(fix.RootName))
			if got := frag.ToString(); got != fix.ExpectedXML {
				t.Errorf("ToString mismatch:\n got  %q\n want %q", got, fix.ExpectedXML)
			}
		})
	}
}
