// Cross-impl fixture harness: applies Yan's JS-Yjs reference fixtures to
// reearth/ygo and checks whether the resulting state matches what JS Yjs
// recorded as expected.
package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/reearth/ygo/crdt"
)

type fixtureFile struct {
	Generator string     `json:"generator"`
	Scenarios []scenario `json:"scenarios"`
}

type scenario struct {
	Description    string                 `json:"description"`
	JsClientID     uint64                 `json:"js_client_id"`
	GoClientID     uint64                 `json:"go_client_id"`
	RootKind       string                 `json:"root_kind"`
	RootName       string                 `json:"root_name"`
	UpdateHex      string                 `json:"update_hex"`
	ExpectedMap    map[string]interface{} `json:"expected_map,omitempty"`
	ExpectedArray  []interface{}          `json:"expected_array,omitempty"`
	ExpectedText   string                 `json:"expected_text,omitempty"`
	ExpectedXML    string                 `json:"expected_xml,omitempty"`
	ExpectedLength uint64                 `json:"expected_length,omitempty"`
}

type result struct {
	scenario string
	kind     string
	wireVer  string
	pass     bool
	reason   string
}

func main() {
	base := "/Users/ianchechin/Documents/Calm/ygo/testdata"
	v1Path := flag.String("v1", base+"/yjs-updates.json", "V1 fixture file (yjs-encoded)")
	v2Path := flag.String("v2", base+"/yjs-update-v2-fixtures.json", "V2 fixture file (yjs-encoded)")
	xmlPath := flag.String("xml", base+"/yjs-xml-fixtures.json", "XML fixture file (yjs-encoded)")
	revV1Path := flag.String("rv1", base+"/go-updates.json", "Reverse V1 fixture (Yan's ygo encoded)")
	revV2Path := flag.String("rv2", base+"/go-update-v2-fixtures.json", "Reverse V2 fixture (Yan's ygo encoded)")
	verbose := flag.Bool("v", false, "print every scenario")
	flag.Parse()

	results := []result{}
	results = append(results, runWrappedFile(*v1Path, "V1-yjs")...)
	results = append(results, runWrappedFile(*v2Path, "V2-yjs")...)
	results = append(results, runXMLFile(*xmlPath, "V1-xml")...)
	results = append(results, runWrappedFile(*revV1Path, "V1-rev")...)
	results = append(results, runWrappedFile(*revV2Path, "V2-rev")...)

	// Group + print summary
	type bucket struct{ pass, fail int }
	groups := map[string]*bucket{}
	for _, r := range results {
		key := r.wireVer + "/" + r.kind
		if groups[key] == nil {
			groups[key] = &bucket{}
		}
		if r.pass {
			groups[key].pass++
		} else {
			groups[key].fail++
		}
	}

	if *verbose {
		fmt.Println("=== Per-scenario ===")
		for _, r := range results {
			status := "PASS"
			if !r.pass {
				status = "FAIL"
			}
			fmt.Printf("  [%s/%s/%-5s] %s\n", r.wireVer, r.kind, status, r.scenario)
			if !r.pass && r.reason != "" {
				fmt.Printf("                  reason: %s\n", r.reason)
			}
		}
	}

	fmt.Println("=== Summary by wire/kind ===")
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	totalPass, totalFail := 0, 0
	for _, k := range keys {
		b := groups[k]
		fmt.Printf("  %-12s pass=%-3d fail=%-3d (total %d)\n", k, b.pass, b.fail, b.pass+b.fail)
		totalPass += b.pass
		totalFail += b.fail
	}
	fmt.Println("=== TOTAL ===")
	fmt.Printf("  PASS %d / FAIL %d / TOTAL %d\n", totalPass, totalFail, totalPass+totalFail)

	if !*verbose && totalFail > 0 {
		fmt.Println("\n=== First 10 failures (use -v for full list) ===")
		shown := 0
		for _, r := range results {
			if r.pass {
				continue
			}
			fmt.Printf("  [%s/%s] %s\n      %s\n", r.wireVer, r.kind, r.scenario, r.reason)
			shown++
			if shown >= 10 {
				break
			}
		}
	}
}

func runWrappedFile(path, wireVer string) []result {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", path, err)
		return nil
	}
	var ff fixtureFile
	if err := json.Unmarshal(data, &ff); err != nil {
		fmt.Fprintf(os.Stderr, "cannot parse %s: %v\n", path, err)
		return nil
	}
	out := make([]result, 0, len(ff.Scenarios))
	for _, sc := range ff.Scenarios {
		out = append(out, runScenario(sc, wireVer))
	}
	return out
}

// XML fixture file is a bare JSON array (not wrapped in scenarios).
func runXMLFile(path, wireVer string) []result {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot read %s: %v\n", path, err)
		return nil
	}
	var arr []scenario
	if err := json.Unmarshal(data, &arr); err != nil {
		fmt.Fprintf(os.Stderr, "cannot parse %s: %v\n", path, err)
		return nil
	}
	out := make([]result, 0, len(arr))
	for _, sc := range arr {
		sc.RootKind = "xml"
		out = append(out, runScenario(sc, wireVer))
	}
	return out
}

func runScenario(sc scenario, wireVer string) (r result) {
	r.scenario = sc.Description
	r.kind = sc.RootKind
	r.wireVer = wireVer
	defer func() {
		if p := recover(); p != nil {
			r.pass = false
			r.reason = fmt.Sprintf("PANIC: %v", p)
		}
	}()

	updateBytes, err := hex.DecodeString(sc.UpdateHex)
	if err != nil {
		r.reason = fmt.Sprintf("invalid hex: %v", err)
		return
	}

	doc := crdt.New()

	// Wire version is encoded in the prefix of wireVer (V1-yjs, V1-rev, V1-xml, V2-yjs, V2-rev)
	wirePrefix := wireVer
	if len(wireVer) >= 2 {
		wirePrefix = wireVer[:2]
	}
	switch wirePrefix {
	case "V1":
		if err := crdt.ApplyUpdateV1(doc, updateBytes, nil); err != nil {
			r.reason = fmt.Sprintf("ApplyUpdateV1: %v", err)
			return
		}
	case "V2":
		if err := crdt.ApplyUpdateV2(doc, updateBytes, nil); err != nil {
			r.reason = fmt.Sprintf("ApplyUpdateV2: %v", err)
			return
		}
	}

	switch sc.RootKind {
	case "map":
		m := doc.GetMap(sc.RootName)
		got := m.Entries()
		// normalize: JSON numbers parsed as float64, JSON null → nil
		if !compareMaps(got, sc.ExpectedMap) {
			r.reason = fmt.Sprintf("map mismatch: got=%v expected=%v", got, sc.ExpectedMap)
			return
		}
	case "array":
		a := doc.GetArray(sc.RootName)
		got := a.ToSlice()
		if !compareSlices(got, sc.ExpectedArray) {
			r.reason = fmt.Sprintf("array mismatch: got=%v expected=%v", got, sc.ExpectedArray)
			return
		}
	case "text":
		t := doc.GetText(sc.RootName)
		got := t.ToString()
		if got != sc.ExpectedText {
			r.reason = fmt.Sprintf("text mismatch: got=%q expected=%q", got, sc.ExpectedText)
			return
		}
	case "xml":
		f := doc.GetXmlFragment(sc.RootName)
		got := f.ToXML()
		if got != sc.ExpectedXML {
			r.reason = fmt.Sprintf("xml mismatch: got=%q expected=%q", got, sc.ExpectedXML)
			return
		}
	default:
		r.reason = fmt.Sprintf("unknown root_kind: %s", sc.RootKind)
		return
	}

	r.pass = true
	return
}

func compareMaps(got map[string]any, expected map[string]interface{}) bool {
	if len(got) != len(expected) {
		return false
	}
	for k, ev := range expected {
		gv, ok := got[k]
		if !ok {
			return false
		}
		if !valuesEqual(gv, ev) {
			return false
		}
	}
	return true
}

func compareSlices(got []any, expected []interface{}) bool {
	if len(got) != len(expected) {
		return false
	}
	for i := range got {
		if !valuesEqual(got[i], expected[i]) {
			return false
		}
	}
	return true
}

// valuesEqual normalizes for JSON quirks:
//   - JSON numbers come in as float64; Go side may give int/int64
//   - Booleans / strings / nil straightforward
//   - Nested maps and slices recurse
func valuesEqual(g, e any) bool {
	if g == nil && e == nil {
		return true
	}
	if g == nil || e == nil {
		return false
	}
	switch ev := e.(type) {
	case float64:
		switch gv := g.(type) {
		case float64:
			return gv == ev
		case float32:
			return float64(gv) == ev
		case int:
			return float64(gv) == ev
		case int32:
			return float64(gv) == ev
		case int64:
			return float64(gv) == ev
		case uint64:
			return float64(gv) == ev
		}
		return false
	case string:
		gv, ok := g.(string)
		return ok && gv == ev
	case bool:
		gv, ok := g.(bool)
		return ok && gv == ev
	case []interface{}:
		gv, ok := g.([]any)
		if !ok {
			return false
		}
		return compareSlices(gv, ev)
	case map[string]interface{}:
		gv, ok := g.(map[string]any)
		if !ok {
			return false
		}
		return compareMaps(gv, ev)
	}
	// Fallback: try strict equality
	if reflect.DeepEqual(g, e) {
		return true
	}
	// Last-resort string compare
	return strings.TrimSpace(fmt.Sprint(g)) == strings.TrimSpace(fmt.Sprint(e))
}
