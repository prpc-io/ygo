package sync_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	syncpkg "github.com/Deln0r/ygo/internal/sync"
)

// syncFixture mirrors testdata/sync-fixtures.json (gen-sync.mjs).
type syncFixture struct {
	Description string `json:"description"`
	JSClientID  uint64 `json:"js_client_id"`
	EnvelopeHex string `json:"envelope_hex"`
	MessageType string `json:"message_type"`
	PayloadHex  string `json:"payload_hex"`
}

func loadSyncFixtures(t *testing.T) []syncFixture {
	t.Helper()
	candidates := []string{
		"../../testdata/sync-fixtures.json",
		"../../../testdata/sync-fixtures.json",
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
		t.Fatalf("read sync-fixtures.json: %v", err)
	}
	var fixtures []syncFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatalf("parse sync-fixtures.json: %v", err)
	}
	return fixtures
}

// TestFixtures_DecodeJSEnvelopes is the cross-language proof: every
// envelope produced by y-protocols + Hocuspocus framing in
// gen-sync.mjs must decode on the Go side into a Frame whose Type
// + SyncSub + Payload match the expected values.
func TestFixtures_DecodeJSEnvelopes(t *testing.T) {
	fixtures := loadSyncFixtures(t)
	if len(fixtures) == 0 {
		t.Fatal("no fixtures loaded")
	}

	for _, fix := range fixtures {
		fix := fix
		t.Run(fix.Description, func(t *testing.T) {
			envelope, err := hex.DecodeString(fix.EnvelopeHex)
			if err != nil {
				t.Fatalf("hex envelope: %v", err)
			}
			payload, err := hex.DecodeString(fix.PayloadHex)
			if err != nil {
				t.Fatalf("hex payload: %v", err)
			}

			frame, tail, err := syncpkg.DecodeEnvelope(envelope)
			if err != nil {
				t.Fatalf("DecodeEnvelope: %v", err)
			}
			if len(tail) != 0 {
				t.Errorf("tail = %x, want empty", tail)
			}

			wantType, wantSub, expectsPayload := parseMessageType(fix.MessageType)
			if frame.Type != wantType {
				t.Errorf("Type = %d, want %d (%s)", frame.Type, wantType, fix.MessageType)
			}
			if wantType == syncpkg.MessageSync && frame.SyncSub != wantSub {
				t.Errorf("SyncSub = %d, want %d", frame.SyncSub, wantSub)
			}
			if expectsPayload && !bytes.Equal(frame.Payload, payload) {
				t.Errorf("Payload mismatch:\n got  %x\n want %x", frame.Payload, payload)
			}
			if !expectsPayload && len(frame.Payload) != 0 {
				t.Errorf("Payload = %x, want empty (message type carries no payload)", frame.Payload)
			}
		})
	}
}

// TestFixtures_EncodeMatchesJSBytesForFixedTypes verifies the
// reverse direction for envelope types that do NOT depend on
// non-deterministic state — QueryAwareness (empty payload) is the
// safe case. Sync messages embed state-vector / update bytes whose
// reproduction would require a parallel JS-doc setup; the decode
// test above already proves the wire byte layout matches.
func TestFixtures_EncodeMatchesJSBytesForFixedTypes(t *testing.T) {
	fixtures := loadSyncFixtures(t)
	for _, fix := range fixtures {
		if fix.MessageType != "query-awareness" {
			continue
		}
		jsBytes, _ := hex.DecodeString(fix.EnvelopeHex)
		ourBytes := syncpkg.EncodeQueryAwareness()
		if !bytes.Equal(ourBytes, jsBytes) {
			t.Errorf("QueryAwareness encode mismatch:\n ours  %x\n JS    %x", ourBytes, jsBytes)
		}
	}
}

// parseMessageType maps the fixture's string label to the Go
// MessageType + SyncSubType discriminators.
func parseMessageType(label string) (syncpkg.MessageType, syncpkg.SyncSubType, bool) {
	switch label {
	case "sync.step1":
		return syncpkg.MessageSync, syncpkg.SyncStep1, true
	case "sync.step2":
		return syncpkg.MessageSync, syncpkg.SyncStep2, true
	case "sync.update":
		return syncpkg.MessageSync, syncpkg.SyncUpdate, true
	case "awareness":
		return syncpkg.MessageAwareness, 0, true
	case "query-awareness":
		return syncpkg.MessageQueryAwareness, 0, false
	}
	return 0, 0, false
}
