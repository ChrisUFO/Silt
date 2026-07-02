# ADR 0001: Per-plugin SQLite storage tier

Date: 2026-07-01
Status: Accepted
Sprint: 19 (#212, #213)

## Context

Cardinal rule #4 (ARCHITECTURE.md §0) states that SQLite is working memory,
not a system of record: every row in the core index must be reproducible from
markdown + YAML, and deleting `<vault>/.system/index.sqlite*` is the
documented recovery path. This keeps the core local-first contract simple and
the recovery story clean.

The incoming AI plugins (Sprints 20–23) need queryable private data the core
contract cannot express: vector indexes for semantic Q&A, content-hash caches
for incremental summarization, agent memory. Forcing these through flat files
or the re-derivable core index is either unusable or a correctness regression
against rule #4 as written.

## Decision

Add a **plugin-owned storage tier**: each plugin MAY carry its own SQLite
file at `<vault>/.system/plugins/<id>/data/plugin.db`, opened lazily on a
**distinct** connection that is never `ATTACH`-able to the core index.

- The **core index** (`<vault>/.system/index.sqlite*`) stays governed by
  rule #4 unchanged — working memory only, re-derivable, delete-to-recover.
- The **plugin DB** is a separate, plugin-owned tier. The plugin owns its
  schema and chooses durability semantics (working memory *or* durable). The
  "re-derivable from markdown" requirement does **not** apply to it.
- **Boundary:** data that must survive uninstall or be portable across vaults
  MUST round-trip through markdown. Plugin-private caches (embeddings, hashes,
  agent memory) may live only in the plugin DB.
- **Lifecycle:** the connection is closed on `teardownPlugin(id)` and on
  vault close; the file is deleted on uninstall (the whole
  `.system/plugins/<id>/` folder is removed).
- **sqlite-vec** is registered on every plugin connection (`vec0` +
  `vec_distance_cosine`) via the pure-Go `modernc.org/sqlite/vec` blank
  import — no CGo, single static binary, WAL-compatible.

## Why plugins are exempt (and core is not)

Plugin isolation is the load-bearing difference. A plugin's data is
plugin-private: it is not user intent, it is not the product, and a bug or
corruption in one plugin's DB cannot affect the core index or another plugin.
The core index, by contrast, is the single projection the whole editor depends
on, so the stricter re-derivable contract is warranted there. The carve-out
narrows the relaxed rule to a sandboxed, per-plugin file rather than weakening
the core contract.

## Consequences

- ARCHITECTURE.md §0 rule #4 is clarified to govern the core index only; the
  plugin tier is documented as a separate row in the storage tiers table and
  a paragraph in the SQLite section.
- SPECS.md §8.6 and PLUGIN_DEVELOPMENT.md §8.11 mirror the contract.
- The core recovery path (`delete index.sqlite*`) is unchanged; plugin DBs are
  deleted separately on uninstall.
- The AI sprints (20–23) build on this substrate.
