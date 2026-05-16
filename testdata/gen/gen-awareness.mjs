// Generates testdata/awareness-fixtures.json — wire-byte captures
// from y-protocols/awareness (the JS reference for the Awareness
// CRDT). Drives internal/awareness/fixtures_test.go's
// cross-language proof.
//
// Each scenario captures:
//   description       — human-readable name
//   js_client_id      — the clientID we pinned (deterministic bytes)
//   update_hex        — encodeAwarenessUpdate(awareness, [clientID])
//                       as lowercase hex
//   expected_states   — { clientID: stateObj | null } describing
//                       the awareness state map after applying
//                       the update to a fresh Awareness instance.
//                       null = removed entry (clock retained but
//                       Data nil); object = live presence.
//
// Run: node gen-awareness.mjs (after npm install in this directory).

import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import * as Y from "yjs";
import * as awarenessProtocol from "y-protocols/awareness";

const here = dirname(fileURLToPath(import.meta.url));
const outPath = resolve(here, "..", "awareness-fixtures.json");

// capture builds an Awareness against a Y.Doc with pinned
// clientID, runs mutate(), then encodes the full snapshot.
function capture(description, clientID, mutate) {
  const doc = new Y.Doc();
  doc.clientID = clientID;
  const awareness = new awarenessProtocol.Awareness(doc);
  mutate(awareness, doc);
  // Use meta keys (not states keys) to capture removed entries —
  // y-protocols retains clock metadata for removed clients after
  // setLocalState(null) deletes them from the states map. Iterating
  // states.keys() alone would silently drop "I'm offline" wire entries
  // from the fixture.
  const ids = Array.from(awareness.meta.keys());
  const bytes = awarenessProtocol.encodeAwarenessUpdate(awareness, ids);
  // expected_states reflects the live snapshot after the local
  // mutate. Removed clients (state set to null then cleared) do
  // not appear in getStates() — y-protocols deletes them from
  // the states Map while retaining clock metadata. To capture
  // removal-on-wire fixtures we explicitly add a null entry
  // for any client whose meta exists but state does not.
  const expected = {};
  for (const [cid, state] of awareness.getStates().entries()) {
    expected[String(cid)] = state;
  }
  // For removed clients, the wire entry still carries them with
  // JSON "null". We capture that by checking meta-without-state.
  for (const [cid, meta] of awareness.meta.entries()) {
    if (!awareness.getStates().has(cid)) {
      expected[String(cid)] = null;
    }
  }
  return {
    description,
    js_client_id: doc.clientID,
    update_hex: Buffer.from(bytes).toString("hex"),
    expected_states: expected,
  };
}

const fixtures = [
  capture(
    "single client with name+color",
    100,
    (awareness) => {
      awareness.setLocalState({ name: "Alice", color: "#FF0000" });
    },
  ),
  capture(
    "single client with cursor position",
    101,
    (awareness) => {
      awareness.setLocalState({ cursor: { x: 42, y: 7 }, focus: true });
    },
  ),
  capture(
    "two clients (local + simulated remote)",
    102,
    (awareness, doc) => {
      awareness.setLocalState({ name: "local" });
      // Inject a remote via applyAwarenessUpdate using a different
      // pre-encoded fake update. The cleanest way: build a sibling
      // Y.Doc + Awareness and copy state across.
      const remoteDoc = new Y.Doc();
      remoteDoc.clientID = 202;
      const remoteAw = new awarenessProtocol.Awareness(remoteDoc);
      remoteAw.setLocalState({ name: "remote" });
      const remoteBytes = awarenessProtocol.encodeAwarenessUpdate(
        remoteAw,
        [202],
      );
      awarenessProtocol.applyAwarenessUpdate(awareness, remoteBytes, "test");
    },
  ),
  capture(
    "client with empty state object",
    103,
    (awareness) => {
      awareness.setLocalState({});
    },
  ),
  capture(
    "unicode in state value",
    104,
    (awareness) => {
      awareness.setLocalState({ name: "Иван", emoji: "🚀" });
    },
  ),
  capture(
    "client set then removed (wire still carries entry)",
    105,
    (awareness) => {
      awareness.setLocalState({ initial: true });
      awareness.setLocalState(null);
    },
  ),
];

writeFileSync(outPath, JSON.stringify(fixtures, null, 2) + "\n");
console.log(`wrote ${fixtures.length} awareness fixtures to ${outPath}`);
// y-protocols' Awareness installs a setInterval-driven outdated-sweep
// timer (awareness.js:75-82) that keeps the Node event loop alive
// indefinitely. We're done with the captured Awareness instances —
// explicitly exit so CI doesn't time out.
process.exit(0);
