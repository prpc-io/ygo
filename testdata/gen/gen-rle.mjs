// Generates testdata/rle-fixtures.json — wire-byte captures from
// lib0's stateful RLE encoders (RleEncoder, UintOptRleEncoder,
// IntDiffOptRleEncoder, StringEncoder). Drives
// internal/lib0/rle_fixtures_test.go's cross-language proof.
//
// Each scenario captures:
//   primitive    — which encoder (e.g. "uint-opt-rle")
//   description  — human-readable name
//   values       — the input values fed to the encoder
//   bytes_hex    — encoder output as lowercase hex
//
// Run: node gen-rle.mjs (after npm install in this directory).

import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import {
  RleEncoder,
  UintOptRleEncoder,
  IntDiffOptRleEncoder,
  StringEncoder,
  writeUint8,
  toUint8Array,
} from "lib0/encoding";

const here = dirname(fileURLToPath(import.meta.url));
const outPath = resolve(here, "..", "rle-fixtures.json");

function hex(bytes) {
  return Buffer.from(bytes).toString("hex");
}

function captureRle(description, values) {
  const enc = new RleEncoder(writeUint8);
  for (const v of values) {
    enc.write(v);
  }
  return {
    primitive: "rle",
    description,
    values,
    bytes_hex: hex(toUint8Array(enc)),
  };
}

function captureUintOptRle(description, values) {
  const enc = new UintOptRleEncoder();
  for (const v of values) {
    enc.write(v);
  }
  // Non-Encoder-extending classes expose their own toUint8Array().
  return {
    primitive: "uint-opt-rle",
    description,
    values,
    bytes_hex: hex(enc.toUint8Array()),
  };
}

function captureIntDiffOptRle(description, values) {
  const enc = new IntDiffOptRleEncoder();
  for (const v of values) {
    enc.write(v);
  }
  return {
    primitive: "int-diff-opt-rle",
    description,
    values,
    bytes_hex: hex(enc.toUint8Array()),
  };
}

function captureStringEncoder(description, values) {
  const enc = new StringEncoder();
  for (const s of values) {
    enc.write(s);
  }
  return {
    primitive: "string",
    description,
    values,
    bytes_hex: hex(enc.toUint8Array()),
  };
}

const fixtures = [
  captureRle("single value", [42]),
  captureRle("long run of 100 sevens", Array(100).fill(7)),
  captureRle("alternating 1 and 2", [1, 2, 1, 2, 1, 2]),
  captureRle("zeros only", [0, 0, 0]),

  captureUintOptRle("single value 42", [42]),
  captureUintOptRle("run of three fives", [5, 5, 5]),
  captureUintOptRle("singles then run [1,2,3,3,3]", [1, 2, 3, 3, 3]),
  captureUintOptRle("zeros only (negative-zero edge case)", [0, 0, 0, 0, 0]),
  captureUintOptRle("large value (1<<20)", [1 << 20, 1 << 20]),

  captureIntDiffOptRle("monotonic increment +1", [10, 11, 12, 13, 14]),
  captureIntDiffOptRle("mixed deltas", [3, 1100, 1101, 1050, 0]),
  captureIntDiffOptRle("negative deltas (decrement -10)", [100, 90, 80, 70, 60]),
  captureIntDiffOptRle("zero delta run (same value)", [42, 42, 42, 42, 42]),

  captureStringEncoder("five short strings", ["hello", "world", "foo", "bar", "baz"]),
  captureStringEncoder("with empty strings", ["", "a", "", "bc"]),
  captureStringEncoder("long string triggers staging", [
    "abcdefghijklmnopqrstuvwxyz",
    "short",
    "another moderately long string here",
  ]),
];

writeFileSync(outPath, JSON.stringify(fixtures, null, 2) + "\n");
console.log(`wrote ${fixtures.length} RLE fixtures to ${outPath}`);
