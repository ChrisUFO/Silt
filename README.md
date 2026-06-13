# Silt

A lightweight, local-first hybrid note-taking and task-lifecycle engine built for speed, durability, and stream-of-consciousness collection.

[![Engine Architecture](https://img.shields.io/badge/Architecture-Go%20%2B%20Wails%20%2B%20Svelte%205-blueviolet)](ARCHITECTURE.md)
[![Storage Schema](https://img.shields.io/badge/Storage-Plaintext%20Markdown%20%2B%20SQLite%20Cache-blue)](SPECS.md)
[![License](https://img.shields.io/badge/License-MIT-green)](#)

**Silt** bridges the gap between structured namespace notebooks and chronological block-based daily streams. It treats human-readable plaintext files as the absolute database of record, while utilizing a native desktop runtime cache to project your logs into fully interactive **Agenda**, **Calendar**, and **Kanban** board interfaces.

---

## Key Highlights

- **No File Lock-In** — Your data lives in flat directories of basic Markdown `.md` files.
- **Zero-Bloat Performance** — No Electron. Idle allocation sits below 65MB RAM with sub-16ms input rendering.
- **Inline Task Machine** — Turn any block bullet into a state-managed task using dense, human-writable shorthand.
- **Composable Views** — Flip between document scroll, Agenda, Calendar, and Kanban board.
- **Fail-Safe Design** — Atomic staging protocol prevents file corruption on power loss or crash.

---

## Documentation

Each concern has a single source of truth. Refer to the file that owns the topic rather than duplicating here.

| Document | Scope |
| :--- | :--- |
| [**SPECS.md**](SPECS.md) | Product specification: philosophy, file format, AST grammar, plugin architecture, system configuration. |
| [**ARCHITECTURE.md**](ARCHITECTURE.md) | Engineering blueprint: process topology, Go backend internals, SQLite schema, IPC API contract, concurrency model. |
| [**DESIGN.md**](DESIGN.md) | Design system: Refined Cyber-Ink vision, color tokens, typography, component specs, motion, accessibility. |
| [**TESTING.md**](TESTING.md) | Test coverage matrix, benchmarks, manual verification checklist, known gaps. |

---

## Getting Started

### Development Prerequisites

Ensure your device has **Go (v1.26+)**, **Node.js (v20+)**, and the **Wails CLI** globally configured.

```bash
git clone https://github.com/ChrisUFO/Silt.git
cd Silt
wails dev
```

### Production Bundling

```bash
wails build -clean
```

For the full release pipeline (icon generation, NSIS installer, portable zip), see [`build.sh`](build.sh).
