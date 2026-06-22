# Silt

> Capture the flow. Map the connections. Let your thoughts settle.

*A [Chelydra Labs](https://chelydra.dev) project.*

Silt is a simple, super-fast, and lightweight note-taking app designed to capture your stream-of-consciousness thoughts and map them to one another. Like silt carried by water, your daily notes are fleeting deposits that slowly settle, accumulate, and connect—ultimately building a fertile, structured foundation of knowledge.


[![Engine Architecture](https://img.shields.io/badge/Architecture-Go%20%2B%20Wails%20%2B%20Svelte%205-blueviolet)](ARCHITECTURE.md)
[![Storage Schema](https://img.shields.io/badge/Storage-Plaintext%20Markdown%20%2B%20SQLite%20Cache-blue)](SPECS.md)
[![License](https://img.shields.io/badge/License-MIT-green)](./LICENSE)

**Silt** bridges the gap between structured namespace notebooks and chronological block-based daily streams. It treats human-readable plaintext files as the absolute database of record, while utilizing a native desktop runtime cache to project your logs into fully interactive **Agenda**, **Calendar**, and **Kanban** board interfaces.

Notes are organized **Notebook › Section › Page**: each Page is a single Markdown file whose blocks carry their own dates. The Section layer is optional — pages can live directly under a notebook. Silt starts blank — you create or open notebooks and build your own hierarchy.

---

## Key Highlights

- **Notebook › Section › Page hierarchy** — folders on disk; each Page is a single `.md` file with per-block dates.
- **Smart Graph** — slash-delimited hierarchical tags (`#work/project/milestone-one`), global block references (`((uuid))`) with hover previews, and live dual-bound embeds (`{{embed:uuid}}`).
- **Themeable** — the entire shell is driven by a single JSON theme (colors + optional fonts). Five first-class themes ship built-in (Cyber Forest default, Terra Noir, Linen, Stark, Graphite). Ship your own palette by dropping a `.json` into `<vault>/.system/themes/` or importing it from **Settings → Appearance**. See the [authoring guide](./docs/THEMING.md).
- **Plugin SDK + first-party plugins** — Agenda (rolling task timeline), Calendar (month/week grids), and Kanban (drag-and-drop task board with multi-level scope) are built on the same `PluginContext` SDK as third-party plugins. All plugins can be enabled/disabled from the Plugin Manager; install community plugins from `.silt-plugin` archives.
- **No File Lock-In** — Your data lives in flat directories of basic Markdown `.md` files.
- **Zero-Bloat Performance** — No Electron. Idle allocation sits below 65MB RAM with sub-16ms input rendering.
- **Inline Task Machine** — Turn any block bullet into a state-managed task using dense, human-writable shorthand.
- **Fail-Safe Design** — Atomic staging protocol prevents file corruption on power loss or crash.

---

## Documentation

Each concern has a single source of truth. Refer to the file that owns the topic rather than duplicating here.

| Document | Scope |
| :--- | :--- |
| [**SPECS.md**](SPECS.md) | Product specification: philosophy, file format, AST grammar, plugin architecture, system configuration. |
| [**ARCHITECTURE.md**](ARCHITECTURE.md) | Engineering blueprint: process topology, Go backend internals, SQLite schema, IPC API contract, concurrency model, plugin loader. |
| [**DESIGN.md**](DESIGN.md) | Design system: Refined Cyber-Ink vision, color tokens, typography, component specs, motion, accessibility. |
| [**docs/PLUGIN_DEVELOPMENT.md**](docs/PLUGIN_DEVELOPMENT.md) | How to author, package (`.silt-plugin`), and install Silt plugins — with the full PluginContext SDK reference. |
| [**docs/THEMING.md**](docs/THEMING.md) | How to author, import, and select Silt themes — the canonical token schema reference and a copy-pasteable blank template. |
| [**docs/BACKUP.md**](docs/BACKUP.md) | How to back up, restore, and migrate a vault via the portable `.silt-vault` archive. |
| [**TESTING.md**](TESTING.md) | Test coverage matrix, benchmarks, manual verification checklist, known gaps. |
| [**CODE_SIGNING.md**](CODE_SIGNING.md) | Windows Authenticode signing policy (SignPath Foundation), verification, and one-time setup. |

---

## Platform Support

Silt targets **Windows** and **Linux** as first-class platforms — both are built, tested, and shipped. **macOS** is not excluded (Wails and Go are cross-platform), but it is not specifically built or tested against. The release pipeline ([`build.sh`](build.sh)) currently produces Windows NSIS installers and portable zips; Linux AppImage/Flatpak packaging is planned for a future sprint.

---

## Installation

### Windows
1. Download the latest installer (`silt-v<version>-windows-installer.exe`) or the portable ZIP (`silt-v<version>-windows-portable.zip`) from GitHub Releases.
2. Run the installer or extract the portable ZIP.

> [!NOTE]
> **Windows Defender SmartScreen Bypass:** Since Silt is unsigned, Windows SmartScreen may show an "Unknown publisher" warning. To bypass this:
> 1. Right-click the installer `.exe` (or the `.zip` file *before* extracting it).
> 2. Select **Properties**.
> 3. Under the **General** tab, check the **Unblock** checkbox in the Security section at the bottom.
> 4. Click **Apply** or **OK**, then run/extract the file.

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
