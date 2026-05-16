// Generates testdata/sync-fixtures.json — y-protocols/sync envelope
// captures plus Hocuspocus outer-tag envelope wrappers. Drives
// internal/sync/fixtures_test.go's cross-language proof.
//
// Each scenario captures:
//   description     — human-readable name
//   js_client_id    — pinned clientID (deterministic bytes)
//   envelope_hex    — outer envelope: varuint(MessageType) [+nested]
//   message_type    — string label: "sync.step1" | "sync.step2" |
//                     "sync.update" | "awareness" | "query-awareness"
//   payload_hex     — inner payload bytes (for sync messages: the
//                     V1 update or state vector AFTER the varbuffer
//                     length-prefix is stripped; for awareness: the
//                     awareness update bytes; for query-awareness:
//                     empty string)
//
// Run: node gen-sync.mjs (after npm install in this directory).

import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import * as Y from "yjs";
import * as syncProtocol from "y-protocols/sync";
import * as awarenessProtocol from "y-protocols/awareness";
import * as encoding from "lib0/encoding";
import * as decoding from "lib0/decoding";

const here = dirname(fileURLToPath(import.meta.url));
const outPath = resolve(here, "..", "sync-fixtures.json");

// Hocuspocus outer-envelope message type tags
// (matches @hocuspocus/server packages/server/src/types.ts:56-67).
const MessageSync = 0;
const MessageAwareness = 1;
const MessageQueryAwareness = 3;

function buildSyncStep1(doc) {
  const encoder = encoding.createEncoder();
  encoding.writeVarUint(encoder, MessageSync);
  syncProtocol.writeSyncStep1(encoder, doc);
  return encoding.toUint8Array(encoder);
}

function buildSyncStep2(doc) {
  const encoder = encoding.createEncoder();
  encoding.writeVarUint(encoder, MessageSync);
  syncProtocol.writeSyncStep2(encoder, doc);
  return encoding.toUint8Array(encoder);
}

function buildSyncUpdate(updateBytes) {
  const encoder = encoding.createEncoder();
  encoding.writeVarUint(encoder, MessageSync);
  syncProtocol.writeUpdate(encoder, updateBytes);
  return encoding.toUint8Array(encoder);
}

function buildAwarenessFrame(awareness, clients) {
  const encoder = encoding.createEncoder();
  encoding.writeVarUint(encoder, MessageAwareness);
  encoding.writeVarUint8Array(
    encoder,
    awarenessProtocol.encodeAwarenessUpdate(awareness, clients),
  );
  return encoding.toUint8Array(encoder);
}

function buildQueryAwarenessFrame() {
  const encoder = encoding.createEncoder();
  encoding.writeVarUint(encoder, MessageQueryAwareness);
  return encoding.toUint8Array(encoder);
}

// stripVarbufferPrefix extracts the inner bytes from a varbuffer-
// wrapped payload. Used to capture the "what's inside the sync
// envelope" view tests assert against.
function stripVarbufferPrefix(envelope, skipBytes) {
  // envelope starts with varuint(MessageSync)=1 byte (since tag<128)
  // + varuint(syncSubType)=1 byte + varbuffer(payload).
  // skipBytes = 1 (sync type) + 1 (sub-type) for sync messages.
  // For awareness: skipBytes = 1 (just the outer tag).
  const decoder = decoding.createDecoder(envelope);
  // Skip the leading fixed-position fields.
  for (let i = 0; i < skipBytes; i++) {
    decoding.readVarUint(decoder);
  }
  return decoding.readVarUint8Array(decoder);
}

function hex(bytes) {
  return Buffer.from(bytes).toString("hex");
}

const fixtures = [];

// --- SyncStep1 from empty doc ---
{
  const doc = new Y.Doc();
  doc.clientID = 300;
  const envelope = buildSyncStep1(doc);
  fixtures.push({
    description: "SyncStep1 from empty doc",
    js_client_id: 300,
    envelope_hex: hex(envelope),
    message_type: "sync.step1",
    payload_hex: hex(stripVarbufferPrefix(envelope, 2)),
  });
}

// --- SyncStep1 from doc with content (state vector has clientID:clock) ---
{
  const doc = new Y.Doc();
  doc.clientID = 301;
  const m = doc.getMap("settings");
  m.set("color", "red");
  m.set("size", "large");
  const envelope = buildSyncStep1(doc);
  fixtures.push({
    description: "SyncStep1 from non-empty doc",
    js_client_id: 301,
    envelope_hex: hex(envelope),
    message_type: "sync.step1",
    payload_hex: hex(stripVarbufferPrefix(envelope, 2)),
  });
}

// --- SyncStep2 carrying full state ---
{
  const doc = new Y.Doc();
  doc.clientID = 302;
  const arr = doc.getArray("items");
  arr.push(["first", "second"]);
  const envelope = buildSyncStep2(doc);
  fixtures.push({
    description: "SyncStep2 with array state",
    js_client_id: 302,
    envelope_hex: hex(envelope),
    message_type: "sync.step2",
    payload_hex: hex(stripVarbufferPrefix(envelope, 2)),
  });
}

// --- Update (incremental — capture from a second mutation) ---
{
  const doc = new Y.Doc();
  doc.clientID = 303;
  const text = doc.getText("body");
  text.insert(0, "Hello ");
  const sv = Y.encodeStateVector(doc);
  text.insert(6, "world");
  const updateBytes = Y.encodeStateAsUpdate(doc, sv);
  const envelope = buildSyncUpdate(updateBytes);
  fixtures.push({
    description: "SyncUpdate incremental text insert",
    js_client_id: 303,
    envelope_hex: hex(envelope),
    message_type: "sync.update",
    payload_hex: hex(stripVarbufferPrefix(envelope, 2)),
  });
}

// --- Awareness frame ---
{
  const doc = new Y.Doc();
  doc.clientID = 304;
  const awareness = new awarenessProtocol.Awareness(doc);
  awareness.setLocalState({ name: "Alice" });
  const envelope = buildAwarenessFrame(
    awareness,
    Array.from(awareness.meta.keys()),
  );
  fixtures.push({
    description: "Awareness frame with one client state",
    js_client_id: 304,
    envelope_hex: hex(envelope),
    message_type: "awareness",
    payload_hex: hex(stripVarbufferPrefix(envelope, 1)),
  });
}

// --- QueryAwareness (empty payload) ---
{
  const envelope = buildQueryAwarenessFrame();
  fixtures.push({
    description: "QueryAwareness empty handshake",
    js_client_id: 0,
    envelope_hex: hex(envelope),
    message_type: "query-awareness",
    payload_hex: "",
  });
}

writeFileSync(outPath, JSON.stringify(fixtures, null, 2) + "\n");
console.log(`wrote ${fixtures.length} sync envelope fixtures to ${outPath}`);
// y-protocols' Awareness keeps a setInterval-driven timer alive
// (see gen-awareness.mjs).
process.exit(0);
