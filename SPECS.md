Technical Specification: Silt

A Local-First, High-Performance Hybrid Note & Task Management Lifecycle Architecture

1. Executive Summary & Philosophy

1.1 Problem Statement

Modern personal knowledge management (PKM) and task-management tools are fundamentally split. Hierarchical tools excel at spatial partitioning and structured organization but fail at temporal journaling, lightweight processing, and open formats. On the other hand, outline graph-based systems offer friction-free, daily logging but struggle to natively integrate rich task metadata directly into the block-stream. Relying on complex, third-party plugin ecosystems to connect notes and tasks introduces structural instability, speed degradation, and unpredictable data-serialization standards.

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

3.1 The Single-File Page Model

While the user experiences each Page as a single, endless, scrollable document, storing an entire page in one file is practical because a page is a focused topic (not an unbounded daily journal). Each block within the page carries its own `file_date` in the trailing `<!-- id: uuid @ YYYY-MM-DD -->` comment, preserving the temporal dimension the agenda and calendar views rely on.

The Go engine parses the single `.md` file on load and streams the blocks to the TipTap editor in Svelte. Writes are debounced and serialized atomically (temp file + fsync + rename) so a crash never corrupts the file. Blocks from different dates coexist in the same page file — the date is per-block, not per-file.

3.2 Physical Directory Layout

Silt uses a three-level hierarchy — **Notebook > Section > Page** — mapped directly onto folders on disk, where the **Section layer is optional**:

- A **Notebook** is a top-level folder directly under the vault root. Users open existing notebook folders or create new ones from the notebook selector. Multiple notebooks can be open at once.
- A **Section** is an optional grouping folder within a Notebook. Sections are shown even when empty, so a freshly created section appears immediately.
- A **Page** is a single `.md` file and is the **streaming unit**: the editor renders one TipTap instance per page. Each block within the page carries its own `file_date` in the trailing comment, so blocks from different dates coexist in one file. A page may live directly under a Notebook (no section) or nested within a Section.

```
VaultRoot/
├── .system/
│   ├── config.yaml
│   ├── plugins/
│   │   ├── agenda/
│   │   ├── calendar/
│   │   └── kanban/
│   ├── themes/                     ← first-class themes (embedded + scaffolded)
│   │   ├── cyber_forest.json       ← the default / primary ("Refined Cyber-Ink")
│   │   ├── silt-terra-noir.json    ← warm dark earth
│   │   ├── silt-linen.json         ← clean paper
│   │   ├── silt-stark.json         ← WCAG AAA high-contrast
│   │   └── silt-graphite.json      ← calm monochrome dark
│   └── templates/                  ← user-authored page templates (built-ins are embedded)
│       ├── my-meeting-template.md
│       └── sprint-review.md
├── Work/                          ← Notebook
│   ├── Inbox.md                   ← Page directly under the Notebook (no section)
│   └── Projects/                  ← Section
│       ├── WebsiteRedesign.md     ← Page (single file; blocks carry per-block dates)
│       └── MobileApp.md
└── Personal/                      ← another Notebook
    └── Journal/
        └── Daily.md
```

Path resolution: the **notebook** is the top folder under the vault; the **page** is the folder directly containing the `.md` file; the **section** is the path between them (`""` when the page sits directly under the notebook). Frontmatter values override path-derived defaults. Files at shallower depths (e.g. a stray `.md` directly in a Notebook folder) are skipped with a warning at startup (fail-loudly).

Silt starts blank — no default notebook or section is created. The user creates or opens their first notebook from the sidebar's notebook selector.

**Linked / external notebooks (#100).** A notebook root does not have to live
inside the vault. The user can LINK an external folder (e.g. a synced
SharePoint/OneDrive mount) as a notebook from the sidebar ("Link External
Folder…"); it is browsed/searched/edited in place and is NEVER copied into the
vault. The linked root IS one notebook: its sections/pages live directly under
it (there is no leading notebook-name component the way there is under the
vault). Each linked notebook carries a `source` of `'linked:<id>'` (vs.
`'vault'`) so two notebooks that happen to share a name across roots cannot
collide; display names are globally unique. The link registry lives in
vault-scoped `config.yaml` (`linked_notebooks:`). Unlinking a notebook stops
indexing it and leaves its files completely untouched (vs. deleting a vault
notebook, which trashes it). See ARCHITECTURE.md §3.1 for the full model
(identity, path resolution, multi-root watcher, failure modes).


3.3 File Boundary Specification & Frontmatter Standard

Every page file contains a strict YAML metadata block bounded by triple dashes (---). This block allows indexers to map orphaned or moved files without reading the entire file directory tree:

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
- [ ] Implement parser tests [owner:: Chris] [start:: 2026-06-13] [due:: 2026-06-20] [priority:: 1] <!-- id: f1b2c3d4-e5f6-7a8b-9c0d-1e2f3a4b5c6d -->
```


4. Custom AST Parser & Task Shorthand Grammar

The Go backend uses a custom tokenizer layered onto a Markdown syntax tree engine to parse, match, and modify inline task properties.

4.1 Task Syntax — Dataview Inline Metadata

Silt tasks are GFM checkbox items enriched with Dataview-style inline
metadata tokens (`[key:: value]`). The `TASK` keyword is dropped — any
GFM checkbox (`- [ ]`, `- [/]`, `- [x]`) is a task. Metadata is
order-independent and extensible.

```
- [/] Critical workstream [priority:: 1] [due:: 2026-08-03] [owner:: Bob] [pin:: true] [progress:: 50] #work/sprint-4
```

Checkbox State Marker (GFM convention):

[ ] = TODO

[/] = DOING (In-Progress)

[x] = DONE

Metadata Tokens (Dataview `[key:: value]` format):

| Key | Shorthand | Format | Example |
|---|---|---|---|
| `due` | — | `[due:: YYYY-MM-DD]` | `[due:: 2026-08-03]` |
| `start` | — | `[start:: YYYY-MM-DD]` | `[start:: 2026-06-13]` |
| `owner` | `[o:: name]` | `[owner:: name]` | `[owner:: Bob]` |
| `priority` | `[p:: N]` | `[priority:: N]` (1=critical, 2=normal, 3=low) | `[priority:: 1]` |
| `pin` | `[pinned:: true]` | `[pin:: true]` (boolean) | `[pin:: true]` |
| `progress` | `[prog:: N]` | `[progress:: N]` (0-100) | `[progress:: 50]` |

Tags: Standard markdown hashtags (`#work/project/milestone-one`) —
unaffected by the metadata token system.

Persistent Identifier comment: A hidden HTML comment
`<!-- id: UUIDv4 @ YYYY-MM-DD -->` automatically generated and appended
to the block by the parser if one is missing.

4.2 Editor Input Paths

Three input paths produce the same Dataview `[key:: value]` storage
format:

1. **`%` prefix autocomplete**: User types `%` → instant popup showing
   all available metadata keys (scoped to task metadata only, unlike
   the general `/` command palette). Typing filters; selecting inserts
   `[key:: ]` with cursor positioned for value entry.
2. **`/` slash commands**: TipTap's command palette. Richer UI: date
   pickers for `/due`, priority selector for `/priority`, toggle for
   `/pin`.
3. **Direct typing**: Power users type `[key:: value]` directly — what
   you type is what's stored (WYSIWYG).

4.3 Task Token State Matrix

| File Plaintext State | UI Checkbox | Kanban Column | Calendar/Agenda |
|---|---|---|---|
| `- [ ] ...` | Unchecked `[ ]` | "To Do" | Assigned to Due Date |
| `- [/] ...` | Half-filled `[/]` | "In Progress" | Spans Start to Due |
| `- [x] ...` | Checked `[x]` | "Done" | Stays on original date |

4.4 Indentation and Nested Hierarchies

Indentation depths are defined by hard tabs ($T_{level}$).
If a block with nesting depth $T_{n}$ resides under a block at depth $T_{n-1}$, the parser evaluates the relationship and indexes a parent-child dependency map inside the SQLite database:

- [ ] Implement AST backend core [priority:: 1] [owner:: Chris] <!-- id: parent-uuid -->
    - [ ] Write lexer token rules [priority:: 2] [owner:: Jenny] <!-- id: child-uuid-1 -->
    - [ ] Write file synchronization loop [priority:: 2] [owner:: Jenny] <!-- id: child-uuid-2 -->


SQLite mapping schema:
INSERT INTO tasks (block_id, status, owner, priority) VALUES ('child-uuid-1', 'TODO', 'Jenny', 2);

4.5 Storage-of-Truth Tiers

See ARCHITECTURE.md §0 for the full storage-of-truth contract. Summary:
task metadata (`[key:: value]`) is **file-resident user intent** — the
markdown file is the source of truth. SQLite caches derived values
(comments count, links count) and the parsed projection for query speed,
but every SQLite row is re-derivable from the markdown.

5. Smart Graph Features: Namespaces & Block Links

5.1 Hierarchical Smart Tag Namespaces

Tags in Silt leverage a slash-delimited taxonomy (#work/project/milestone-one) to allow for structured, recursive querying without rigid metadata forms.

When a tag is processed, the parser splits it by depth levels and indexes it into a hierarchical table:

CREATE TABLE tags (
    block_id TEXT NOT NULL,
    raw_tag TEXT NOT NULL,       -- "work/project/milestone-one"
    root_node TEXT NOT NULL,     -- "work"
    sub_node TEXT,               -- "project"
    leaf_node TEXT               -- "milestone-one"
);


This design allows you to view an aggregated chronological timeline of all activities tagged under #work at a high level, or drill down specifically to items tagged with #milestone-one.

5.2 Global Block-References & Embeds

Every line block is given a unique identifier appended as a comment suffix: <!-- id: UUID -->. This allows you to easily link and reuse blocks across different notebooks.

Block Reference ((uuid)): Inline placeholder text that renders as an interactive, clickable link. Hovering over the link reveals the original block content. Clicking it centers the view directly on the source file location.

Block Embed {{embed:uuid}}: Renders a live, interactive portal displaying the source block inline. Svelte coordinates a dual-binding listener on the embedded portal: any text changes made in the embed are piped back to update the source file, and any changes in the source file are immediately updated in the embed. In the TipTap editor surface (#85), both `((uuid))` and `{{embed:uuid}}` render as live ProseMirror NodeViews (`BlockReferenceNode` inline atom and `EmbedNode` block atom respectively) via `SvelteNodeViewRenderer`, reusing the same read-mode components (`BlockReferenceChip.svelte`, `EmbedPortal.svelte`). The editor's converters tokenize `clean_text` to emit these node types and reconstruct the textual tokens on save, so the on-disk file is round-trip identical.

6. User Interface Specification

6.1 Color Palette & Dark-Mode Aesthetics

To minimize eye strain and maintain professional focus, Silt utilizes a high-contrast dark aesthetic with deep visual depth:

Base Canvas: #121214 (Deep slate black)

Sidebar & Workspace Panels: #161619 (Solid dark charcoal)

Borders, Guidelines, & Rules: #27272a (Crisp zinc gray)

Primary Text: #e4e4e7 (Light warm gray)

Muted Text & Metadata: #8b8b94 (Medium cool gray)

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

Automatically appends `- [ ] ` (empty GFM checkbox) and triggers the `%` metadata autocomplete so the user can add owner, due date, priority, etc.

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

Theme Files: Parsed dynamically from canonical modes-based JSON files inside `<vault>/.system/themes/`. Each theme carries a `schema_version`, `id`, `name`, an optional `typography` section, and a `modes.dark` / `modes.light` token set (bg, border, text, accent.primary / accent.secondary × start/end/glow, status). Accent tokens are hue-agnostic and semantic: components reference only `--accent-primary-*` / `--accent-secondary-*`, and each theme maps its concrete hues onto them. The optional `typography` section (theme-level, not per-mode) defines font-family choices (`font_family`, `mono_font_family`, `headline_font`) that are injected as `--font-body`, `--font-mono`, `--font-headline` CSS custom properties; the CSS classes use fallback chains (`var(--font-body, var(--editor-font-family), <hardcoded>)`) so themes without typography inherit the config-driven fonts. Typography values are validated via `isValidFontFamily` which rejects CSS-breaking characters as a sandbox defense.

Default Theme: A canonical default theme (`cyber_forest`) is embedded in the Go binary (`backend/themes`, via `embed.FS`) so the app always has a guaranteed-correct fallback — it works before a vault exists, when the themes directory is empty/wiped, and when the active theme id is missing or invalid. Since Sprint 8, the full **first-class set** is embedded (`themes/*.json`: Cyber Forest, Terra Noir, Linen, Stark, Graphite); `ListThemes` appends every embedded first-class theme (deduped — on-disk wins), and `ScaffoldVault` writes editable on-disk copies of all of them. `ResolveActive` / `CachedThemeByID` resolve a first-class id from the embed even when it is not on disk, so a non-default active theme no longer flashes the default palette on a wiped/existing vault.

Mechanism: On startup the Go backend reads the active theme + mode from `AppSettings`, resolves the theme file (falling back to the embedded default), and exposes it over the Wails IPC bridge (`ListThemes` / `GetActiveTheme` / `ApplyTheme` / `ImportTheme` / `ExportActiveTheme` / `PickThemeFile`). A Svelte theme store receives the flattened token map and injects every token as a CSS custom property on `document.documentElement` by rewriting a single generated `:root { … }` style block — one DOM write, one recalc, same-tick repaint, no flicker. The `index.css :root` values are retained as startup fallbacks only, overridden once the IPC round-trip completes. The native webview `BackgroundColour` is resolved at launch from a process-local, mtime-aware theme cache (`themes.CachedThemeByID` in `backend/themes/cache.go`) so a non-default active theme's `bg.void` is used for the pre-CSS paint (#73); the cache falls back to the embedded default when no settings exist or the active id is invalid.

Import + Validation (Sprint 6, #48): `App.ImportTheme` calls `themes.ImportThemeFromPath` which reuses `ParseAndValidate` (the same call the loader uses, so a successfully imported theme is exactly the kind of object `ListThemes` enumerates). The id is sanitized to `[a-z0-9_-]` (preserving underscores so the existing `cyber_forest` built-in stays canonical) and namespaced (`user-` prefix on a built-in id, counter suffix on repeat). Atomic write via `parser.WriteFileAtomic`. Sandbox by schema: `isValidColor` accepts only hex / rgb() / rgba() values at every token slot, so embedded `<script>`, `url()`, `expression()`, and named colors are rejected structurally before reaching disk. `themes.ValidationErrors` propagate over IPC so the UI can name the offending token and the expected format. On success the Wails event `themes:changed` is emitted so the picker re-fetches `ListThemes` and the new theme appears without a restart. Export is the inverse: `App.ExportActiveTheme` writes the active theme verbatim to a user-chosen path for round-trip editing.

Settings → Appearance (Sprint 6, #47): a fully accessible picker + mode toggle + import button + drop zone + export button. Zero per-theme code branches — every row is built from `ThemeInfo` + `ThemeInfo.Swatches`. Mode is a `role="radiogroup"` of Dark / Light / System; themes are a `role="listbox"` of `role="option"` rows with roving tabindex, Arrow/Home/End navigation, Enter/Space commit, Esc to cancel any live preview. Status and errors render in a `role="status" aria-live="polite"` region (escalating to `role="alert"` for errors). The `Settings → Appearance` tab is the single surface for theme selection; the same theme engine is also what the custom titlebar (Sprint 3, #41) and the rest of the shell inherit through the same CSS custom-property pipeline.

Schema Example (cyber_forest.json, dark mode shown):

{
  "schema_version": "1.0.0",
  "id": "cyber_forest",
  "name": "Cyber Forest",
  "author": "System Designer",
  "description": "...",
  "typography": {
    "font_family": "'Plus Jakarta Sans', sans-serif",
    "mono_font_family": "'JetBrains Mono', monospace",
    "headline_font": "'Hanken Grotesk', sans-serif"
  },
  "modes": {
    "dark": {
      "bg": { "void": "#0c0c0e", "surface": "#121215", "panel": "#161619", "hover": "#1c1c21", "active": "#222226" },
      "border": { "muted": "#1e1e23", "zinc": "#27272a", "active": "#3f3f46", "focus": "#52525b" },
      "text": { "primary": "#dee3e6", "muted": "#8b8b94", "disabled": "#4b5563" },
      "accent": {
        "primary": { "start": "#2dd4bf", "end": "#0d9488", "glow": "rgba(20, 184, 166, 0.15)" },
        "secondary": { "start": "#6366f1", "end": "#a855f7", "glow": "rgba(168, 85, 247, 0.12)" }
      },
      "status": { "warn": "#fbbf24", "danger": "#f43f5e" }
    },
    "light": { "..." : "..." }
  }
}


6.5 Page Template Engine

Silt provides a full page template system: a built-in library of first-class templates (Notes, Meeting Notes, Standup, Daily Note, Project Brief, 1-on-1, Weekly Review, Decision Log/ADR, Reading Notes, Retrospective), user-extensible custom templates, and the UI/IPC surface to insert them as a new page or into the current page at the cursor. Templates are parameterized Markdown — a title, category, icon, optional placeholder list, and a Markdown body using `{{name}}` placeholder tokens.

Template Files: Parsed dynamically from Markdown files inside `<vault>/.system/templates/`. Each carries a `schema_version`, `id`, `title`, `category`, optional `icon`, optional `placeholders` list, and a Markdown body. The placeholder syntax is `{{name}}` (not Go template syntax) — a small substitution renderer resolves built-in defaults (`date`=YYYY-MM-DD, `time`=HH:MM, `iso_date`=ISO 8601, `weekday`=full weekday name) and user-declared/caller-supplied variables. Unknown placeholders warn (forward-compat), never error.

Smart Graph Compatibility: the placeholder grammar (`^[a-z][a-z0-9_]*$`) structurally excludes Smart Graph syntax — `{{embed:uuid}}` (colon) and `((uuid))` (parentheses) pass through the renderer byte-for-byte, so templates can contain embeds and references that resolve normally on load (§5.2).

Default Library: the full first-class set is embedded in the Go binary (`backend/templates`, via `embed.FS`) so templates are always available — they work before a vault exists, when the templates directory is empty, and on existing vaults. Built-ins are read-only (`builtin://` namespace); user templates are writable (`<vault>/.system/templates/<id>.md`). On-disk templates win the dedup if they share an id with a built-in.

Mechanism: the Go backend resolves templates (on-disk + embedded, deduped, sorted by Category then Title) and exposes them over the Wails IPC bridge (`ListTemplates` / `GetTemplate` / `RenderTemplate` / `RenderTemplateBlocks` / `SaveUserTemplate` / `DeleteUserTemplate` / `ReloadTemplates` / `CreatePageFromTemplate`). A Svelte template store receives the listing and drives the picker modal; the backend emits a `templates:changed` event so the picker re-lists on add/edit/delete. A file watcher on `.system/templates/` hot-reloads external changes. Inserted templates produce real Silt blocks — tasks (`- [ ] TODO TASK …`) flow into Kanban/Agenda/Calendar, embeds/references resolve, and blocks get fresh UUIDs via the standard pipeline (§4, §5.2). No SQLite schema change, no file-write-lock change, no settings.json change — templates are vault-scoped Markdown, read-mostly, on the existing atomic-write path.

Forward Compatibility: `schema_version` is informational (a forward-versioned template keeps loading); the `Source` field supports three tiers — `builtin` (embedded, read-only), `disk` (user-authored, writable), and `plugin` (runtime-registered by a plugin, #96); categories are additive (unknown categories warn, never reject); and new built-ins land as a single `.md` file + embed with no engine change.

Plugin Templates (#96): plugins register templates at runtime via the `RegisterPluginTemplates(pluginID, []*Template)` IPC method. Plugin templates carry `Source = "plugin"` and a `PluginID` field (both `yaml:"-"` so disk frontmatter can never claim a plugin provenance). The canonical URI for a plugin template is `plugin://<plugin-id>/<template-id>`, resolved by `GetTemplate` directly to the in-memory registry. The picker groups plugin templates under a `Plugins / <plugin-id>` header. Plugin templates are deduped last (on-disk > embedded > plugin) so a plugin can't shadow a first-class or user template. The registry is capped at 100 templates per plugin.


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

Kanban Plugin: Uses the sqliteQuery context hook to pull records scoped to the active navigation level (vault / notebook / section / page). The user selects the scope via a segmented control in the board header. The WHERE clause is built per scope:

```sql
-- vault scope (all notebooks)
SELECT * FROM tasks INNER JOIN blocks ON tasks.block_id = blocks.id WHERE 1=1

-- section scope
SELECT * FROM tasks INNER JOIN blocks ON tasks.block_id = blocks.id
WHERE blocks.notebook = ? AND blocks.section = ?
```

Status changes are committed via updateBlockState, which writes the new checkbox state to the source markdown file and re-indexes the block.

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
  checkbox_regex: "^([\\s]*)-\\s\\[([ x/])\\]\\s+(.*)$"
  metadata_token_regex: "\\[([\\w]+)::\\s*([^\\]]*)\\]"
  default_task_priority: 3

# Key-Binding Map
hotkeys:
  open_search: "Ctrl+P"
  open_command_palette: "Ctrl+Slash"
  cycle_view_layout: "Alt+Tab"
  indent_block: "Tab"
  unindent_block: "Shift+Tab"

# UI Preferences (per-vault)
ui:
  sidebar_width: 256

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