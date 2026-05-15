// Generates testdata/yjs-updates.json — V1 Update wire-byte fixtures
// captured from JS Yjs (yjs@13.6.20). Drives the binary-protocol-compat
// proof in internal/encoding/fixture_test.go.
//
// For each scenario we capture:
//   description    — human-readable name
//   js_client_id   — the clientID Y.Doc chose (debug info; not used by tests)
//   root_name      — the root Y.Map name we set
//   update_hex     — Y.encodeStateAsUpdate(doc) as lowercase hex
//   expected_map   — final live map state as a JSON-serializable object
//
// Run: node gen-yjs-update.mjs (after npm install in this directory).

import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import * as Y from "yjs";

const here = dirname(fileURLToPath(import.meta.url));
const outPath = resolve(here, "..", "yjs-updates.json");

// captureScenario builds a Y.Doc with a deterministic clientID
// (so the wire bytes are reproducible across runs and CI's
// `git diff --exit-code testdata/` catches real format drift, not
// JS Yjs's random clientID rolls). Y.Doc has no constructor option
// for clientID, so we override the field immediately after
// construction, before any mutation that would mint an ID.
function captureScenario(description, rootName, clientID, mutate) {
  const doc = new Y.Doc();
  doc.clientID = clientID;
  const map = doc.getMap(rootName);
  mutate(map, doc);
  const bytes = Y.encodeStateAsUpdate(doc);
  const expected = {};
  for (const key of map.keys()) {
    const v = map.get(key);
    if (v === undefined) continue;
    expected[key] = v;
  }
  return {
    description,
    js_client_id: doc.clientID,
    root_name: rootName,
    update_hex: Buffer.from(bytes).toString("hex"),
    expected_map: expected,
  };
}

// Scenario clientIDs are arbitrary but stable. Picking distinct
// values per scenario avoids any cross-scenario state leakage in
// case the generator ever shares a doc.
const scenarios = [
  captureScenario("empty doc, no ops", "x", 100, () => {}),

  captureScenario("single Map.set string", "settings", 101, (m) => {
    m.set("color", "red");
  }),

  captureScenario("two Map.set on different keys", "settings", 102, (m) => {
    m.set("color", "red");
    m.set("lang", "go");
  }),

  captureScenario("Map.set across multiple value types", "x", 103, (m) => {
    m.set("s", "hello");
    m.set("i", 42);
    m.set("f", 3.14);
    m.set("b_true", true);
    m.set("b_false", false);
    m.set("nullval", null);
  }),

  captureScenario("Map.set + Map.set same key (LWW)", "x", 104, (m) => {
    m.set("k", "first");
    m.set("k", "second");
    m.set("k", "third");
  }),

  captureScenario("Map.set + Map.delete", "x", 105, (m) => {
    m.set("a", "alpha");
    m.set("b", "beta");
    m.delete("a");
  }),

  captureScenario("Map.set then delete then set again", "x", 106, (m) => {
    m.set("k", "v1");
    m.delete("k");
    m.set("k", "v2");
  }),

  captureScenario("Map with unicode keys and values", "x", 107, (m) => {
    m.set("ключ", "значение");
    m.set("emoji", "ok");
  }),
];

const out = {
  generator: "yjs@13.6.20",
  scenarios,
};

writeFileSync(outPath, JSON.stringify(out, null, 2) + "\n");
console.log(`wrote ${scenarios.length} scenarios to ${outPath}`);
