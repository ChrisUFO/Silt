# Notes# (Notes Sharp)

A lightweight, local-first hybrid note-taking and task-lifecycle engine built for speed, durability, and stream-of-consciousness collection.

[![Engine Architecture](https://img.shields.io/badge/Architecture-Go%20%2B%20Wails%20%2B%20Svelte%205-blueviolet)](#-architecture-and-stack)
[![Storage Schema](https://img.shields.io/badge/Storage-Plaintext%20Markdown%20%2B%20SQLite%20Cache-blue)](#-storage-architecture)
[![License](https://img.shields.io/badge/License-MIT-green)](#)

**notes#** bridges the gap between structured namespace notebooks (like OneNote) and chronological block-based daily streams (like Logseq). It treats human-readable plaintext files as the absolute database of record, while utilizing a native desktop runtime cache to project your logs into fully interactive **Agenda**, **Calendar**, and **Kanban** board interfaces.

---

## ⚡ Key Highlights

* **No File Lock-In:** Your data is stored entirely in flat, transparent directories of basic Markdown `.md` files.
* **Zero-Bloat Performance:** Bypasses bulky Electron runtimes entirely. Idle allocation sits below **65MB RAM** with sub-**16ms** input rendering loops.
* **Inline Task Machine:** Turn arbitrary block bullets into state-managed tasks using a dense, human-writable inline shorthand syntax.
* **Composable Views:** Seamlessly flip your stream logs between standard document scroll mode, an auto-forwarding Agenda, a month/week Calendar grid, or a drag-and-drop Kanban board.
* **Fail-Safe Design:** Includes hard crash safety via an atomic staging protocol, preventing file corruption or truncation if system power drops mid-keystroke.

---

## 🛠 Architecture and Stack

The system decouples user interaction from filesystem mutations through an active, single-directional update loop:

```
+-----------------------------------------------------------------------+
|                             SVELTE FRONTEND                           |
|  - Infinite Scroll Stream       - Interleaved Kanban / Calendar Views  |
|  - Rich Interactive AST Tokens  - Fast Keyboard Command Palette       |
+-----------------------------------------------------------------------+
                                  ▲  ▼
                        Wails IPC Bridge (JSON)
                                  ▲  ▼
+-----------------------------------------------------------------------+
|                           GO BACKEND CORE                             |
|                                                                       |
|   +-------------------+    Event    +----------------------------+    |
|   |   File Watcher    |  Triggered  |        AST Parser          |    |
|   |    (fsnotify)     | ---------- Pinpoint Block Extraction    |    |
|   +-------------------+             +----------------------------+    |
|             |                                     |                   |
|       Disk Changes                            Map Blocks              |
|             ▼                                     ▼                   |
|   +-------------------+             +----------------------------+    |
|   | Markdown Files on |             |     SQLite Cache Index     |    |
|   | Local Storage     | ◄---------- | - Tasks, Tags, Blocks      |    |
|   | (Atomic Writes)   |  Sync Write | - Hierarchical Links       |    |
|   +-------------------+             +----------------------------+    |
+-----------------------------------------------------------------------+
```

* **Core Logic Engine:** Go (v1.26+) handles native OS threads, file system IO, and optimized line-by-line Markdown AST parsing.
* **Window Bridge:** Wails bindings expose Go methods directly to the OS native WebKit engine, bypassing massive V8 framework compilation duplication.
* **UI/UX Frame:** Svelte 5 + Tailwind CSS manages compile-time surgical DOM manipulation, delivering liquid-smooth drag-and-drop transformations without a Virtual DOM overhead layer.
* **Query Indexer:** An in-memory/volatile SQLite cache maps tags, blocks, and tasks on application boot to serve complex dashboard cross-joins instantly.

---

## 📂 Storage Architecture

To provide an infinite page visualization without parsing massive, fragile monolithic files, notebooks are serialized to disk into individual, discrete daily increments. The frontend virtualizes these components on scroll, stitching them together cleanly.

```text
Notebooks/
├── Engineering/
│   ├── Architecture/
│   │   ├── 2026-06-11.md
│   │   ├── 2026-06-12.md
│   │   └── 2026-06-13.md
│   └── SOGAV_SaaS/
│       └── 2026-06-01.md
└── Personal/
    └── Journal/
        └── 2026-06-13.md
```

### File Specification Layout
Every tracking file utilizes standard YAML-compliant frontmatter metadata to identify spatial coordinates:

```markdown
---
notebook: Engineering
section: Architecture
date: 2026-06-13
tags: [work/sogav, systems/specs]
---
# Saturday, June 13, 2026

## Stream Logging
- [ ] TODO TASK [Chris](2026-06-13, 2026-08-03)#1 Draft README definition file <!-- id: 8fa72c3b -->
```

---

## 📝 AST Syntax Grammar

Tasks are injected directly inline using an unambiguous string format scanned by the Go regex lexer:

```text
^([ ]|[/]|[x])\s(TODO|DOING|DONE)\sTASK\s(?:\[([^\]]*)\])?(?:\(([^)]*)\))?(?:#(\d+))?\s(.*)$
```

### Visual Token Breakdown

```markdown
[/] DOING TASK [Chris](2026-06-13, 2026-08-03)#1 Refine core AST engine parser. <!-- id: 8fa72c3b -->
```
* `[/] DOING TASK` ➔ **Interactive Checkbox State:** Cyclable component mapping to an active, half-filled state widget.
* `[Chris]` ➔ **Assignment Target:** An explicit user/owner assignment token pill.
* `(2026-06-13, 2026-08-03)` ➔ **Temporal Window:** Concrete `(Start Date, Due Date)` maps. Single entries default to the absolute deadline.
* `#1` ➔ **Priority Index:** High-contrast color assignments designating item urgency hierarchy.
* `<!-- id: 8fa72c3b -->` ➔ **Block Identity:** A hidden unique tracking hash used to link blocks for references or dashboard mutations.

### Relationship Inheritance
Nesting hierarchies use native tab indents (`Tab` to indent, `Shift+Tab` to un-indent). Deeply nested tasks are captured during AST analysis and linked to parents via foreign key relationships in SQLite:

```markdown
- [ ] TODO TASK [Chris]#1 Top-Level Structural Parent
    - [ ] TODO TASK [Jenny] Dependent Child Sub-Task Node
```

---

## 🧩 Advanced Features

### 1. Hierarchical Smart Tag Namespaces
Avoid flat tag aggregation mess. Leverage slash-delimited hierarchies to index nested structural collections recursively:
```markdown
- Review marketing specs for deployment timeline #work/sogav/milestone-one
```
Filtering workflows by `#work` rolls up all cascading child metrics (`#sogav`, `#milestone-one`) automatically into your timeline perspective.

### 2. Global Block References & Live Embeds
* **References `((uuid))`:** Injects a strict visual hyperlinked anchor tracing straight back to source definitions across notebooks.
* **Embeds `{{embed:uuid}}`:** Generates an inline, dual-binding interactive mirroring block interface. Mutating data within an embed line rewrites the source file on disk instantly.

### 3. Visual Breadcrumb Paths
When diving deep into nested list nodes, vertical guides map indentation lines. Selecting an active block dynamically highlights its exact ancestral path up to the root block root using vivid contrast indicators.

### 4. Keyboard Command Palette
Typing `/` activates an on-cursor prompt overlay for fast formatting injection without removing hands from the keyboard layout:

| Shortcut | Action Behavior |
| :--- | :--- |
| `/todo` | Appends a standardized inline task signature block `- [ ] TODO TASK []()#3 `. |
| `/today` | Injects current local timestamp formatted as `YYYY-MM-DD`. |
| `/kanban` | Flips your current working interface area directly into column lane view modes. |
| `/embed` | Overlays global block indexing search modals for linking embeddings. |

---

## 🔒 Reliability Matrix & Safety

### Atomic File Write Protocol
To prevent data loss from spontaneous system halts, battery drain, or unexpected edge case crash events, the Go backend writes using isolation staging primitives:

1. Modded layout text is written to a temporary block mirror: `.2026-06-13.md.tmp`.
2. Hardware flushes are systematically completed using lower-level disk controls (`file.Sync()`).
3. The platform executes an atomic directory replacement using native `os.Rename()` primitives. 

If execution drops anywhere along steps 1 or 2, your original active file remains completely uncorrupted and intact.

---

## 🚀 Getting Started

### Development Prerequisites
Ensure your device has **Go (v1.26+)**, **Node.js (v20+)**, and the **Wails CLI** globally configured.

```bash
# Clone the repository source
git clone https://github.com/ChrisUFO/Notes-Sharp.git
cd Notes-Sharp

# Fire up live compilation development mode
wails dev
```

### Production Bundling
Compile down to an optimized native binary matching your current OS distribution architecture:
```bash
wails build -clean
```
