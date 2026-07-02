# Silt

<p align="center">
  <strong>Capture the flow. Map the connections. Let your thoughts settle.</strong>
</p>

<p align="center">
  <a href="ARCHITECTURE.md"><img src="https://img.shields.io/badge/Architecture-Go%20%2B%20Wails%20%2B%20Svelte%205-blueviolet" alt="Engine Architecture"></a>
  <a href="SPECS.md"><img src="https://img.shields.io/badge/Storage-Markdown%20%2B%20SQLite%20Cache%20%2B%20Plugin%20Stores-blue" alt="Storage Schema"></a>
  <a href="./LICENSE"><img src="https://img.shields.io/badge/License-MIT-green" alt="License"></a>
</p>

---

Silt is a simple, ultra-fast, local-first note-taking app designed to capture stream-of-consciousness thoughts and connect them. Like silt carried by water, your daily notes are fleeting deposits that slowly settle, accumulate, and connect—ultimately building a fertile, structured foundation of knowledge.

It bridges the gap between structured namespace notebooks and chronological daily streams. Silt treats human-readable plaintext Markdown files on your local drive as the absolute database of record, while utilizing a native desktop runtime cache to project your logs into fully interactive **Agenda**, **Calendar**, and **Kanban** board interfaces.

---

## 🧭 Core Philosophy: Local-First & Zero Lock-In

Silt is built on the belief that your notes belong to you.
1. **Local Flat-Files as Source of Truth:** Your data lives in plain Markdown (`.md`) files on your computer. If Silt disappears tomorrow, your notes remain fully readable in any text editor.
2. **Speed and Efficiency:** No bloated Electron wrapper. Silt uses system-native WebKit engines via Wails, keeping idle memory allocation below 65MB RAM and input latency within a 16ms frame budget (60 FPS).
3. **Atomic Reliability:** An atomic staging and overwrite protocol prevents file corruption on power loss or crash.
4. **Privacy-First:** Silt is completely local. It does not transfer any information to other networked systems, and contains no telemetry or automatic update checks.

---

## ✨ Key Features

- **Notebook › Section › Page Structure** – Directly mapped to directories on your local drive. Silt starts completely blank; you create, open, or link notebooks.
- **Smart Graph** – Hierarchical smart tag namespaces (`#work/project/milestone`), global block references (`((uuid))`) with hover previews, and live dual-bound embeds (`{{embed:uuid}}`).
- **Inline Task Machine** – Turn any list bullet into a state-managed task. Silt parses standard GFM checkboxes (`- [ ]`, `- [/]`, `- [x]`) and extracts Dataview-style inline metadata:
  ```markdown
  - [/] Critical workstream [priority:: 1] [due:: 2026-08-03] [owner:: Bob] #work/sprint-4
  ```
- **Built-in & Custom Themes** – Styled with a "Cyber-Ink" design language. Ships with 5 premium themes (Cyber Forest, Terra Noir, Linen, Stark, Graphite). Import or export your own palettes as simple JSON files.
- **Flexible Plugin SDK** – Dynamic viewports like Agenda, Calendar, and Kanban are decoupled as plugins. Extend Silt using native ES Modules in a secure runtime sandbox; plugins may carry their own per-plugin data store (including vector search) without touching the core Markdown index.
- **Linked Notebooks** – Mount external folders (e.g., a synced OneDrive/SharePoint or Obsidian directory) directly without importing them. They are watched and edited in place.

---

## 📚 Technical Documentation

Each concern in Silt has a single source of truth. Refer to these documents for detailed architectural and design specifications:

| Document | Scope |
| :--- | :--- |
| [**SPECS.md**](SPECS.md) | Product specification: AST grammar, file formats, plugin SDK architecture, configurations. |
| [**ARCHITECTURE.md**](ARCHITECTURE.md) | Engineering blueprint: process topology, SQLite schema, IPC API contracts, file synchronization. |
| [**DESIGN.md**](DESIGN.md) | Design system: Cyber-Ink aesthetics, color tokens, typography, component specs. |
| [**docs/PLUGIN_DEVELOPMENT.md**](docs/PLUGIN_DEVELOPMENT.md) | How to build, package (`.silt-plugin`), and distribute plugins. |
| [**docs/THEMING.md**](docs/THEMING.md) | Guide to authoring, importing, and selecting themes. |
| [**docs/BACKUP.md**](docs/BACKUP.md) | How to back up, restore, and migrate vaults using `.silt-vault` archives. |
| [**TESTING.md**](TESTING.md) | Test coverage matrix, benchmarks, and verification checklists. |
| [**CODE_SIGNING.md**](CODE_SIGNING.md) | Windows Authenticode signing policy, verification, and SignPath setup. |

---

## 🚀 Installation

### Windows
1. Download the latest installer (`silt-v<version>-windows-installer.exe`) or the portable ZIP (`silt-v<version>-windows-portable.zip`) from [GitHub Releases](https://github.com/Chelydra-Labs/Silt/releases).
2. Run the installer or extract the portable ZIP.

> [!NOTE]
> **Windows Defender SmartScreen Bypass:** Since Silt releases are unsigned, Windows SmartScreen may show an "Unknown publisher" warning. To bypass this:
> 1. Right-click the downloaded `.exe` (or the `.zip` file *before* extracting it).
> 2. Select **Properties**.
> 3. Under the **General** tab, check the **Unblock** checkbox in the Security section at the bottom.
> 4. Click **Apply** or **OK**, then run/extract the file.

### Linux
1. Download the latest AppImage (`Silt-*.AppImage`) or Debian package (`silt_*_debian_amd64.deb`) from [GitHub Releases](https://github.com/Chelydra-Labs/Silt/releases).
2. Choose one of the following installation paths:
   - **AppImage (Universal):**
     Make the file executable and run it:
     ```bash
     chmod +x Silt-*.AppImage
     ./Silt-*.AppImage
     ```
   - **Debian / Ubuntu Package:**
     Install via `apt` (which resolves required webview dependencies like WebKit2GTK):
     ```bash
     sudo apt install ./silt_*_debian_amd64.deb
     ```

---

## 🛠️ Development

### Prerequisites

Ensure you have **Go (v1.26+)**, **Node.js (v20+)**, and the **Wails CLI** globally configured.

### Setting Up a Local Build

```bash
git clone https://github.com/Chelydra-Labs/Silt.git
cd Silt
wails dev
```

### Production Bundling

```bash
wails build -clean
```

For the full release pipeline (including icon generation, NSIS installers, and portable ZIP packages), refer to the release script ([`build.sh`](build.sh)) or ([`build-linux.sh`](build-linux.sh)).

---

## 📄 License

Silt is open-source software licensed under the [MIT License](LICENSE).
