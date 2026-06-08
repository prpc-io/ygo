package encoding

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type snapshotFixtureFile struct {
	Generator string            `json:"generator"`
	Scenarios []snapshotFixture `json:"scenarios"`
}

type snapshotFixture struct {
	Description string                `json:"description"`
	SnapshotHex string                `json:"snapshot_hex"`
	SV          map[string]uint64     `json:"sv"`
	DS          map[string][][]uint64 `json:"ds"`
}

// TestSnapshotFixtures_CrossLanguage proves ygo's snapshot codec is
// byte-compatible with yjs@13.6.20. For each yjs-produced snapshot we:
//  1. Decode the bytes via DecodeSnapshot.
//  2. Re-encode via EncodeSnapshot.
//  3. Assert the re-encoded bytes are byte-identical to the yjs input.
//
// Step 3 is the strong check: it catches client-ordering differences in
// both the delete set and the state vector (yjs sorts both descending).
// The multi-client scenario exercises that ordering specifically.
func TestSnapshotFixtures_CrossLanguage(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "snapshot-fixtures.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Skip("testdata/snapshot-fixtures.json not present; run testdata/gen/gen-snapshot.mjs")
		}
		t.Fatalf("read fixture: %v", err)
	}
	var ff snapshotFixtureFile
	if err := json.Unmarshal(data, &ff); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(ff.Scenarios) == 0 {
		t.Fatal("no scenarios")
	}

	for _, sc := range ff.Scenarios {
		t.Run(sc.Description, func(t *testing.T) {
			want, err := hex.DecodeString(sc.SnapshotHex)
			if err != nil {
				t.Fatalf("bad hex: %v", err)
			}

			snap, err := DecodeSnapshot(want)
			if err != nil {
				t.Fatalf("DecodeSnapshot: %v", err)
			}

			// Semantic decode check: state vector matches the fixture.
			if len(snap.SV) != len(sc.SV) {
				t.Errorf("SV size: got %d want %d", len(snap.SV), len(sc.SV))
			}
			for cs, clk := range sc.SV {
				var c uint64
				_, _ = scanUint(cs, &c)
				if snap.SV[c] != clk {
					t.Errorf("SV[%s]: got %d want %d", cs, snap.SV[c], clk)
				}
			}

			// Byte-exact re-encode check (catches ordering).
			got := EncodeSnapshot(snap)
			if hex.EncodeToString(got) != sc.SnapshotHex {
				t.Errorf("re-encode mismatch\n got: %s\nwant: %s",
					hex.EncodeToString(got), sc.SnapshotHex)
			}
		})
	}
}

// scanUint parses a base-10 uint64 from s into *out. Tiny local helper
// to avoid importing strconv in the test for one call.
func scanUint(s string, out *uint64) (int, error) {
	var v uint64
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		v = v*10 + uint64(r-'0')
	}
	*out = v
	return len(s), nil
}
