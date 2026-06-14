Technical Specification: Silt

A Local-First, High-Performance Hybrid Note & Task Management Lifecycle Architecture

1. Executive Summary & Philosophy

1.1 Problem Statement

Modern personal knowledge management (PKM) and task-management tools are fundamentally split. Hierarchical tools (such as Microsoft OneNote) excel at spatial partitioning and structured organization but fail at temporal journaling, lightweight processing, and open formats. On the other hand, outline graph-based systems (such as Logseq or Obsidian) offer friction-free, daily logging but struggle to natively integrate rich task metadata directly into the block-stream. Relying on complex, third-party plugin ecosystems to connect notes and tasks introduces structural instability, speed degradation, and unpredictable data-serialization standards.

1.2 The System Vision: Silt

Silt is an uncompromised, local-first desktop application designed to bridge structured notebooks with daily chronological capture streams. It treats simple, human-readable Markdown text files on your local drive as the immutable database of record. Simultaneously, it uses a lightweight, compiled Go-based backend and an in-memory SQLite indexing cache to serve real-time multi-dimensional productivity views:

The Document View: A seamless, virtualized infinite scrolling page of notes organized by days.

The Agenda Plugin: An automatically rolling list of active schedules.

The Calendar Plugin: A macro spatial tracker for start and due dates.

The Kanban Plugin: An interactive, drag-and-drop workflow status visualizer.

The Sovereign Principle: The local directory structure is the single source of truth. The application runtime acts strictly as a reactive viewport, transforming and writing text mutations safely back to disk without vendor lock-in.

2. Technical Stack & Decoupled Architecture

To hit our strict resource limits and keep the UI completely lag-free, Silt avoids bloated Electron wrappers in favor of a compile-time optimized, system-native desktop wrapper.

+-----------------------------------------------------------------------+
|                             SVELTE FRONTEND                           |
|  - Infinite Scroll Stream       - Dynamic Plugin Rendering Engine     |
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


2.1 Stack Blueprint

Desktop Engine: Go (v1.26+). Handles system-level interactions, high-efficiency disk operations, directory indexing, config management, and AST parsing.

Application Shell Bridge: Wails Framework. Connects Go structures directly to platform-native WebKit engines (WebKit on macOS, WebKit2 on Linux, WebView2 on Windows) without bundled Node/V8 runtimes.

UI Presentation Layer: Svelte 5 + Tailwind CSS. Selected for its compile-time reactive paradigm. Svelte writes direct, targeted DOM updates, preserving rendering cycles during heavy UI interactions like dragging task cards or scrolling long documents.

Analytical Query Layer: SQLite 3 (In-Memory / Local Volatile Cache). Provides lightning-fast multi-dimensional indexing, relational join processing, and instant filtering across notebooks, sections, tags, and date limits.

2.2 Unidirectional State Synchronization

The architecture decouples UI actions from disk manipulations to prevent editing stutters:

Frontend State Shift: User interacts with a visual component (e.g., ticking a task checkbox).

IPC Event Despatch: Svelte transmits a structured JSON envelope across the Wails IPC bridge containing the targeted block's UUID and requested modification.

Backend Atomic Mutation: The Go backend locates the precise line in the target Markdown file, stages a temporary write, performs an atomic overwrite, and notifies the internal cache.

Index Optimization: The in-memory SQLite database processes the block shift.

Reactive Feedback Loop: The backend broadcasts a UI state event to ensure other views (e.g., Kanban columns or Calendars) update in perfect lockstep.

3. File Directory Structure & Storage Engine

3.1 The Virtualized Infinite Scroll Stream

While the user experiences each Section as a single, endless, scrollable timeline, storing an entire section in one massive file is a technical anti-pattern. Parsing multiple megabytes of plaintext on every keystroke introduces severe performance drops and increases the scale of potential data loss during a write failure.

Solution: Daily files are serialized discretely to disk inside the structured notebook folders. The Go engine reads these small daily files on demand, streaming them to a virtualized list container in Svelte. The files are dynamically stitched together at the viewport boundaries, creating the illusion of a single continuous document.

3.2 Physical Directory Layout

Silt uses a OneNote-style three-level hierarchy — **Notebook > Section > Page** — mapped directly onto folders on disk, where the **Section layer is optional**:

- A **Notebook** is a top-level folder directly under the vault root. Users open existing notebook folders or create new ones from the notebook selector. Multiple notebooks can be open at once.
- A **Section** is an optional grouping folder within a Notebook. Sections are shown even when empty, so a freshly created section appears immediately.
- A **Page** is a folder that directly contains `.md` files and is the **streaming unit**: the daily note files inside it are stitched into a single infinite-scroll timeline in the editor. A page may live directly under a Notebook (no section) or nested within a Section.

```
VaultRoot/
├── .system/
│   ├── config.yaml
│   ├── plugins/
│   │   ├── agenda/
│   │   ├── calendar/
│   │   └── kanban/
│   └── themes/
│       └── cyber_forest.json
├── Work/                          ← Notebook
│   ├── Inbox/                     ← Page directly under the Notebook (no section)
│   │   └── 2026-06-13.md
│   └── Projects/                  ← Section
│       ├── WebsiteRedesign/       ← Page (streams the .md files below)
│       │   ├── 2026-06-11.md
│       │   └── 2026-06-13.md
│       └── MobileApp/
│           └── 2026-06-13.md
└── Personal/                      ← another Notebook
    └── Journal/
        └── Daily/
            └── 2026-06-13.md
```

Path resolution: the **notebook** is the top folder under the vault; the **page** is the folder directly containing the `.md` file; the **section** is the path between them (`""` when the page sits directly under the notebook). Frontmatter values override path-derived defaults. Files at shallower depths (e.g. a stray `.md` directly in a Notebook folder) are skipped with a warning at startup (fail-loudly).

Silt starts blank — no default notebook or section is created. The user creates or opens their first notebook from the sidebar's notebook selector.


3.3 File Boundary Specification & Frontmatter Standard

Every daily file contains a strict YAML metadata block bounded by triple dashes (---). This block allows indexers to map orphaned or moved files without reading the entire file directory tree:

```
---
notebook: Work
section: Projects        # optional; omit (or leave empty) for a section-less page
page: WebsiteRedesign
date: 2026-06-13
tags: [systems/specs, wails/go]
---
# Saturday, June 13, 2026

## Daily Standup Logging
- [ ] TODO TASK [Chris](2026-06-13, 2026-06-20)#1 Implement parser tests <!-- id: f1b2c3d4-e5f6-7a8b-9c0d-1e2f3a4b5c6d -->
```


4. Custom AST Parser & Task Shorthand Grammar

The Go backend uses a custom tokenizer layered onto a Markdown syntax tree engine (such as yuin/goldmark) to parse, match, and modify inline task properties.

4.1 Parser Shorthand Specification

An active task block is declared anywhere within a bulleted list hierarchy via the explicit keyword TASK immediately following a checkbox token, followed by optional contextual tokens:

^([ ]|[/]|[x])\s(TODO|DOING|DONE)\sTASK\s(?:\[([^\]]*)\])?(?:\(([^)]*)\))?(?:#(\d+))?\s(.*)$


4.2 AST Syntax Mappings

[/] DOING TASK [Chris](2026-06-13, 2026-06-20)#1 Implement parser tests <!-- id: f1b2c3d4-e5f6-7a8b-9c0d-1e2f3a4b5c6d -->


Checkbox State Marker: Maps directly to standard markdown checkbox lists.

[ ] = TODO

[/] = DOING (In-Progress)

[x] = DONE

Owner Token: Indicated by bracketed text immediately following TASK. E.g., [Chris].

Temporal Boundaries: Bounded by parentheses containing either a single date token (assigned as the Due Date), or a comma-separated date pair (representing Start Date, Due Date). Supported formats: M/D/YY, MM/DD/YYYY, or standard ISO-8601 YYYY-MM-DD.

Priority Token: Indicated by a trailing hashtag and digit (e.g., #1 representing highest priority, down to #3 representing lowest).

Persistent Identifier comment: A hidden HTML comment <!-- id: UUIDv4 --> automatically generated and appended to the block by the parser if one is missing.

4.3 Task Token State Matrix

File Plaintext State

UI Checkbox Representation

Kanban Column

Calendar/Agenda Placement

[ ] TODO TASK ...

Unchecked box [ ]

"To Do" Column

Assigned to Due Date

[/] DOING TASK ...

Half-filled box [/]

"In Progress" Column

Spans Start to Due Dates

[x] DONE TASK ...

Checked box [x]

"Done" Column

Stays on original timeline date

4.4 Indentation and Nested Hierarchies

Indentation depths are defined by hard tabs ($T_{level}$).
If a block with nesting depth $T_{n}$ resides under a block at depth $T_{n-1}$, the parser evaluates the relationship and indexes a parent-child dependency map inside the SQLite database:

- [ ] TODO TASK [Chris]#1 Implement AST backend core <!-- id: parent-uuid -->
    - [ ] TODO TASK [Jenny]#2 Write lexer token rules <!-- id: child-uuid-1 -->
    - [ ] TODO TASK [Jenny]#2 Write file synchronization loop <!-- id: child-uuid-2 -->


SQLite mapping schema:
INSERT INTO tasks (id, parent_id, owner, start_date, due_date, priority, body) VALUES ('child-uuid-1', 'parent-uuid', 'Jenny', NULL, NULL, 2, 'Write lexer token rules');

5. Smart Graph Features: Namespaces & Block Links

5.1 Hierarchical Smart Tag Namespaces

Tags in Silt leverage a slash-delimited taxonomy (#work/sogav/milestone-one) to allow for structured, recursive querying without rigid metadata forms.

When a tag is processed, the parser splits it by depth levels and indexes it into a hierarchical table:

CREATE TABLE tags (
    block_id TEXT NOT NULL,
    raw_tag TEXT NOT NULL,       -- "work/sogav/milestone-one"
    root_node TEXT NOT NULL,     -- "work"
    sub_node TEXT,               -- "sogav"
    leaf_node TEXT               -- "milestone-one"
);


This design allows you to view an aggregated chronological timeline of all activities tagged under #work at a high level, or drill down specifically to items tagged with #milestone-one.

5.2 Global Block-References & Embeds

Every line block is given a unique identifier appended as a comment suffix: <!-- id: UUID -->. This allows you to easily link and reuse blocks across different notebooks.

Block Reference ((uuid)): Inline placeholder text that renders as an interactive, clickable link. Hovering over the link reveals the original block content. Clicking it centers the view directly on the source file location.

Block Embed {{embed:uuid}}: Renders a live, interactive portal displaying the source block inline. Svelte coordinates a dual-binding listener on the embedded portal: any text changes made in the embed are piped back to update the source file, and any changes in the source file are immediately updated in the embed.

6. User Interface Specification

6.1 Color Palette & Dark-Mode Aesthetics

To minimize eye strain and maintain professional focus, Silt utilizes a high-contrast dark aesthetic with deep visual depth:

Base Canvas: #121214 (Deep slate black)

Sidebar & Workspace Panels: #161619 (Solid dark charcoal)

Borders, Guidelines, & Rules: #27272a (Crisp zinc gray)

Primary Text: #e4e4e7 (Light warm gray)

Muted Text & Metadata: #71717a (Medium cool gray)

Active Highlights & Guideline Markers: #2dd4bf (Refined teal, 400-shade)

6.2 Visual Guideline Path Highlights

For nested lists, Svelte tracks the active cursor focus and dynamically highlights the current hierarchy path. Vertical guide rules align to the indentation columns. Selecting a nested bullet changes the color of its ancestral parent guidelines from #27272a to #2dd4bf, providing instant visual context within deeply nested structures.

  - Root Node Focus
  |   - Sub-Level Node
  |   |   - Active Cursor Selection Bullet Point  <-- Guideline columns are colored teal
  |   - Unfocused Parallel Bullet Point           <-- Guideline column is colored dark gray


6.3 Contextual Keyboard Command Palette (Slash Menu)

Typing the / trigger key on an empty block opens a contextual command menu directly beneath your cursor. You can search, filter, and apply commands using only your keyboard:

Action Trigger

Action Result

/todo

Automatically appends - [ ] TODO TASK []()#3  and focuses the owner field.

/today

Injects today's date formatted as YYYY-MM-DD.

/kanban

Instantly swaps the active workspace pane into the Kanban board columns.

/embed

Displays a search modal of indexed blocks to select and embed.

/h1

Transforms the active block into a first-level markdown header (# ).

6.4 Theme Customization Engine

To prevent styling stagnation, Silt provides a built-in user theme engine mapping to CSS Custom Properties.

Theme Files: Parsed dynamically from JSON files inside Notebooks/.system/themes/.

Mechanism: Upon initialization, the Go backend reads the configured active theme file, serializes key-value mappings to a Svelte configuration state, and CSS variables are dynamically generated and injected into a global :root style block.

Schema Example (cyber_forest.json):

{
  "name": "Cyber Forest",
  "author": "System Designer",
  "colors": {
    "bg-void": "#080b09",
    "bg-surface": "#0d1310",
    "bg-panel": "#121b16",
    "bg-hover": "#1a2620",
    "border-zinc": "#22332a",
    "border-active": "#3d5c4b",
    "text-primary": "#e2ebd5",
    "text-muted": "#6a8274",
    "color-teal-start": "#2dd4bf",
    "color-teal-end": "#0d9488",
    "color-indigo-start": "#4ade80",
    "color-indigo-end": "#22c55e"
  }
}


7. Reliability, Protection, & Performance Targets

7.1 Atomic Staging & Overwrite Protocol

Because your notes are stored directly on disk, Silt must guarantee data safety during unexpected app exits, power failures, or system crashes. The Go file-writing engine never directly modifies an active file. Instead, it follows a strict atomic update sequence:

[InMemory Modified Block Buffer]
              │
              ▼
1. Create Scratch File: ".2026-06-13.md.tmp"
              │
              ▼
2. Flush Buffer to Disk: Call OS file.Sync()
              │
              ▼
3. Atomic Overwrite: Call OS os.Rename(".2026-06-13.md.tmp", "2026-06-13.md")


If a system crash occurs mid-write during steps 1 or 2, the current file on disk remains completely untouched and uncorrupted.

7.2 Non-Functional Performance Thresholds

Startup Ingestion: The parser must boot, scan, token-analyze, and index a directory containing 1,000 markdown files into the SQLite database in under 450ms.

UI Frame Budget: To keep typing smooth, Svelte must complete inline shorthand processing and DOM updates within a 16ms render window (maintaining a locked 60 FPS).

Memory Footprint: The application must maintain an idle memory footprint of less than 65MB RAM, ensuring Silt remains a lightweight utility running in your system tray.

8. Local-First Plugin Architecture

To support core system extension while retaining a lightweight base engine, Silt abstracts all dynamic dashboards—including the Agenda, Calendar, and Kanban viewports—into explicit plugins. The host application acts strictly as a raw block editor, tree compiler, and IPC router.

                  +--------------------------------+
                  |      Silt Core Editor       |
                  +--------------------------------+
                                  │
          ┌───────────────────────┼───────────────────────┐
          ▼                       ▼                       ▼
+───────────────────+   +───────────────────+   +───────────────────+
|   Agenda Plugin   |   |  Calendar Plugin  |   |   Kanban Plugin   |
|   (First-Party)   |   |   (First-Party)   |   |   (First-Party)   |
+───────────────────+   +───────────────────+   +───────────────────+


8.1 Runtime Sandboxing and Lifecycle

Frontend Modules: Svelte dynamically imports plugins at boot time. Plugins are written as independent ESM (ECMAScript Modules) and reside in Notebooks/.system/plugins/{plugin-name}/index.js.

Backend Hooking Structure: Plugins communicate with the Go backend via standard JSON-RPC bridges using Wails events. They obtain read/write query privileges targeting the local SQLite database.

8.2 Host-Plugin API Specification (Frontend)

Plugins run as native ES modules. First-party plugins ship as compiled Svelte components bundled with the app; third-party plugins live in `.system/plugins/<id>/index.js` and are loaded at boot (native ESM via a blob URL so Vite does not resolve them at build time). Both kinds receive the same PluginContext:

```ts
export interface PluginContext {
  activeNotebook: string;
  activeSection: string;
  activePage: string;
  // Read-only SQL against the in-memory index (SELECT / WITH only).
  sqliteQuery: (sql: string, params?: unknown[]) => Promise<Record<string, unknown>[]>;
  mutateBlock: (id: string, text: string) => Promise<boolean>;
  updateBlockState: (id: string, status: 'TODO' | 'DOING' | 'DONE') => Promise<boolean>;
}

export interface SiltPlugin {
  manifest: { id: string; name: string; version: string; icon?: string };
  init?: (ctx: PluginContext) => void;
}
```

The active `notebook/section/page` from the navigator is bound into the context; `sqliteQuery` is read-only (anything other than SELECT/WITH is rejected). See `docs/PLUGIN_DEVELOPMENT.md` for the full author guide.

8.3 Core Feature Decoupling

To enforce architectural parity, the user interface contains no custom code for the default Calendar, Kanban, or Agenda dashboards. They use the exact same SDK constraints as any third-party developer plugin:

Kanban Plugin: Uses the sqliteQuery context hook to pull records: SELECT * FROM tasks INNER JOIN blocks ON tasks.block_id = blocks.id WHERE blocks.section = ? and utilizes updateBlockState to modify card indices.

Calendar Plugin: Pulls dates using range constraints and exposes interactive timeline components.

Agenda Plugin: Filters overdue, current-day, and upcoming milestones, rolling unfinished tasks into the active day view dynamically.

8.4 Plugin Packaging & Distribution (.silt-plugin)

Third-party plugins are distributed as `.silt-plugin` archives — a **ZIP with a custom extension** containing `plugin.json` + the entry module (`index.js`) + optional assets, all at the archive root:

```
plugin.json   { "id": "my-plugin", "name": "My Plugin", "version": "1.0.0", "main": "index.js" }
index.js      native ESM exporting { manifest, init(ctx) }
```

- **Validation:** on install, the manifest schema is checked (`id` must match `^[a-z0-9-]+$`, required name/version, entry module present); absolute paths, `..`, and zip-slip entries are rejected.
- **Install:** atomic extract into `.system/plugins/<id>/` (staged in a temp sibling dir, then renamed); refuses to overwrite an existing id. Emits `plugins:changed` so the loader re-runs.
- **Enable/Disable:** a `.disabled` sentinel file inside the plugin folder (the loader skips disabled plugins) — avoids fragile config.yaml edits. Discovery is folder-based, so install "just works" without editing config.
- **Uninstall:** removes the plugin folder (id sanitized + within-vault check).
- The in-app **Plugin Manager** (titlebar extension icon) drives validate → preview → install, plus per-plugin enable/disable and uninstall.
- First-party plugins (Agenda, Calendar) are always available (bundled) regardless of `.system/plugins/` contents.

9. System Configuration Engine

Global settings are managed locally in a human-readable file located at Notebooks/.system/config.yaml. The schema defines global application defaults, plugin configurations, hotkeys, and parsing logic.

9.1 Configuration Schema (config.yaml)

# Silt Global System Settings Configuration

# Spatial Mapping
notebooks:
  path: "~/Notebooks"
  default_active: "Work"

# Editor Tuning
editor:
  font_family: "Plus Jakarta Sans"
  mono_font_family: "JetBrains Mono"
  font_size_px: 14
  line_height: 1.6
  tab_indent_spaces: 4
  auto_save_delay_ms: 500
  focus_highlight_ancestors: true

# Task Parse Rules
parsing:
  auto_inject_uuid: true
  shorthand_regex: "^([ ]|[/]|[x])\\s(TODO|DOING|DONE)\\sTASK\\s(?:\\[([^\\]]*)\\])?(?:\\(([^)]*)\\))?(?:#(\\d+))?\\s(.*)$"
  default_task_priority: 3

# Key-Binding Map
hotkeys:
  open_search: "Ctrl+P"
  open_command_palette: "Ctrl+Slash"
  cycle_view_layout: "Alt+Tab"
  indent_block: "Tab"
  unindent_block: "Shift+Tab"

# Plugin Registry
plugins:
  active:
    - "silt-agenda"
    - "silt-calendar"
    - "silt-kanban"
  disabled: []
  plugin_settings:
    silt-kanban:
      default_col: "TODO"
      columns: ["TODO", "DOING", "DONE"]


9.2 Hot Reloading Logic

The Go file-system monitor sets a high-priority watch handler on .system/config.yaml. If settings are modified internally or externally, Go parses the file, updates the system memory state instantly, and triggers a style or action event over the Wails IPC bridge, bypassing the need for a full application reboot.