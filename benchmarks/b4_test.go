package benchmarks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Deln0r/ygo"
)

// b4Edit is one record from the dmonad/crdt-benchmarks B4 trace:
// `[position, deleteCount, insertContent]`. Per-record semantics:
// at `position` first delete `deleteCount` chars, then insert
// `insertContent` (possibly empty). Total ~260k records build a
// ~105k-char LaTeX paper.
type b4Edit [3]any

// b4TraceFile is the JSON envelope produced by
// testdata/gen/fetch-b4-trace.mjs.
type b4TraceFile struct {
	Source    string   `json:"source"`
	Edits     []b4Edit `json:"edits"`
	FinalText string   `json:"finalText"`
}

// loadB4Trace returns the parsed trace if testdata/b4-trace.json is
// present, or (nil, "") to signal the caller should b.Skip. The
// trace is gitignored (~3 MB); fetch via
// `node testdata/gen/fetch-b4-trace.mjs`.
func loadB4Trace(b *testing.B) (*b4TraceFile, bool) {
	b.Helper()
	path := filepath.Join("..", "testdata", "b4-trace.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			b.Skipf("B4 trace not present at %s — run `node testdata/gen/fetch-b4-trace.mjs` to download", path)
		}
		b.Fatalf("read trace: %v", err)
	}
	var tf b4TraceFile
	if err := json.Unmarshal(data, &tf); err != nil {
		b.Fatalf("unmarshal trace: %v", err)
	}
	return &tf, true
}

// edit fields are interface{} from JSON: position and deleteCount
// come back as float64; insertContent as string.
func unpackEdit(e b4Edit) (pos uint64, delLen uint64, ins string) {
	if f, ok := e[0].(float64); ok {
		pos = uint64(f)
	}
	if f, ok := e[1].(float64); ok {
		delLen = uint64(f)
	}
	if s, ok := e[2].(string); ok {
		ins = s
	}
	return
}

// BenchmarkB4_RealWorldTrace replays all 259,778 LaTeX-paper edit
// operations into a fresh Text. Final document is asserted to
// match finalText (~104k chars) once before the timed loop to
// catch a misapply early — the loop itself only measures the
// op-throughput.
func BenchmarkB4_RealWorldTrace(b *testing.B) {
	tf, _ := loadB4Trace(b)

	build := func() *ygo.Doc {
		d := ygo.NewDocWithOptions(ygo.Options{ClientID: 1})
		t := ygo.NewText(d, "text")
		txn := d.WriteTxn()
		for _, e := range tf.Edits {
			pos, delLen, ins := unpackEdit(e)
			if delLen > 0 {
				_ = t.Delete(txn, pos, delLen)
			}
			if len(ins) > 0 {
				_ = t.Insert(txn, pos, ins)
			}
		}
		txn.Commit()
		return d
	}

	// Sanity-check the final text once before the timed run.
	d := build()
	tCheck := ygo.NewText(d, "text")
	if got := tCheck.String(); got != tf.FinalText {
		b.Fatalf("final text mismatch: got %d chars, want %d chars",
			len(got), len(tf.FinalText))
	}

	b.Run("ops", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := build()
			if i == 0 {
				reportSize(b, d)
			}
		}
	})
	runStandardSuite(b, build)
}
