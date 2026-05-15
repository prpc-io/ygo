# testdata/gen — fixture generator

Node.js scripts that load the official `lib0` and `yjs` npm packages and emit
fixtures consumed by Go tests.

## Setup

```bash
# from this directory
npm install
```

## Run

```bash
node gen-lib0.mjs
# (planned) node gen-yjs-map.mjs
# (planned) node gen-yjs-array.mjs
# (planned) node gen-yjs-text.mjs
```

Each script writes to `testdata/<name>.json` (or `.bin`) at the repo root.

## Why Node, not Go-only

The JS reference is the source of truth for wire format. Generating fixtures
from the JS side catches divergence we would otherwise miss. CI runs these
scripts on every PR and fails if `testdata/` would change.

## Adding a new generator

1. Create `gen-<topic>.mjs`.
2. Output JSON or binary to `../<topic>.json` or `../<topic>.bin`.
3. Document the schema at the top of the file.
4. Add a Go test in the corresponding package that loads and asserts against it.
5. Update `../README.md`.
