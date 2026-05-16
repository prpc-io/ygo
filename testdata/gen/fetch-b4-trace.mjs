// fetch-b4-trace.mjs downloads the dmonad/crdt-benchmarks B4
// real-world editing trace (LaTeX paper edit history — 259,778
// character-level operations, final document ~104k chars) and
// converts it to a compact JSON shape ygo's benchmark consumes.
//
// Output: testdata/b4-trace.json — gitignored (~3 MB compact JSON).
// Re-run on demand; the trace is upstream-stable so re-download is
// only needed on a fresh checkout. The benchmark
// (BenchmarkB4_RealWorldTrace) skips when the file is absent.
//
// Run: node fetch-b4-trace.mjs

import { writeFileSync, mkdirSync, existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";

const here = dirname(fileURLToPath(import.meta.url));
const outPath = resolve(here, "..", "b4-trace.json");

const url =
  "https://raw.githubusercontent.com/dmonad/crdt-benchmarks/master/js-lib/b4-editing-trace.js";

console.error(`fetching ${url} ...`);
const res = await fetch(url);
if (!res.ok) {
  console.error(`fetch failed: ${res.status} ${res.statusText}`);
  process.exit(1);
}
const src = await res.text();

// The upstream file is a small ES module with two named exports:
//   export const edits = [[position, deleteCount, insertContent], ...]
//   export const finalText = "..."
// We can data-uri import it, then re-serialize as JSON. The
// data-URI trick avoids saving a temp .js file.
const dataUri = "data:text/javascript;base64," + Buffer.from(src).toString("base64");
const mod = await import(dataUri);
const { edits, finalText } = mod;

if (!Array.isArray(edits) || typeof finalText !== "string") {
  console.error("trace shape unexpected: edits=", typeof edits, "finalText=", typeof finalText);
  process.exit(1);
}

if (!existsSync(dirname(outPath))) mkdirSync(dirname(outPath), { recursive: true });
writeFileSync(
  outPath,
  JSON.stringify({
    source: url,
    edits,
    finalText,
  }),
);
console.error(`wrote ${edits.length} edits, finalText length ${finalText.length} chars to ${outPath}`);
