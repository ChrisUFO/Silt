Engineering Architecture: Silt

This document details the low-level system design, state machines, data pipelines, and performance constraints of Silt. It acts as the direct engineering blueprint for developers implementing the Go core and Svelte interface layers.

## Storage-of-Truth Tiers (Read First)

Silt's persistent storage is layered into four tiers with **deliberate,
non-overlapping responsibilities**. Every new feature MUST be designed
against this map before writing code. Violating these tiers is a
correctness regression, not a style choice.

| Tier | Format | Location | Holds | Example |
|---|---|---|---|---|
| **Content** | Markdown (`.md`) | Vault root + per-page files | Block bodies, task markers, per-task metadata, block identity (`<!-- id: uuid @ YYYY-MM-DD -->`) | `[/] DOING TASK [Alice] (2026-06-15) #2 !pin [p:50] Implement search <!-- id: 7c2a… @ 2026-06-15 -->` |
| **Per-vault UI preferences** | YAML | `<vault>/.system/config.yaml` | Per-vault, per-plugin settings: active/disabled plugin list, Kanban columns, Kanban filter state, hotkey bindings, editor font sizes, theme typography overrides | `plugins.plugin_settings.silt-kanban.columns: [Backlog, In Progress, Review, Done]` |
| **Per-linked-notebook overrides** | YAML | `<linkedRoot>/.system/config.yaml` | Per-notebook plugin setting overrides for a linked (external) notebook (#133). Read-only to Silt (user-authored); deep-merged over the vault defaults (linked wins per-key). See §3.1. | `plugins.plugin_settings.silt-kanban.columns: [Backlog, Done]` |
| **User-global, pre-vault** | JSON | `<config>/silt/settings.json` | Settings that must be known before any vault is open: active theme id, dark/light/system mode, non-vault font preferences | `{"active_theme": "silt-graphite", "mode": "dark"}` |
| **Working memory** | SQLite (WAL) | `<vault>/.system/index.sqlite*` | Re-derivable caches: block↔location projection, FTS5 search index, denormalized per-task caches (comments/links counts, pin, progress — all re-derived from markdown on re-index), file mtime/size for incremental re-index | The `blocks` table, `blocks_fts` virtual table, `files` mtime cache |

**The cardinal rules:**

1. **Markdown is the source of truth for content.** Every per-block
   metadata field (status, owner, priority, dates, pin, progress) MUST
   round-trip through the markdown inline task syntax. The block
   identity comment is the only identifier stored in the file; the file
   position and the inline syntax are the source for everything else.
   Deleting the entire `<vault>` should be recoverable by re-creating
   the YAML config — the markdown files are the *product*.

2. **YAML holds per-vault, per-user, per-plugin UI preferences** that
   don't belong in any individual block. If two plugins want different
   values, they live in YAML, not in markdown.

3. **JSON holds user-global, pre-vault settings.** A user can have a
   theme picked before they ever open a vault. The active theme id
   cannot wait for a vault to be loaded; it must live in user-global
   JSON.

4. **SQLite is working memory, not a system of record.** Every row in
   the index MUST be reproducible from the markdown + YAML above. The
   recovery path for any SQLite corruption is *delete the index file
   and relaunch* — that is the documented, supported operation. SQLite
   is allowed to hold the block↔location projection, FTS5, file
   mtime/size caches, and re-derived per-task caches (comments/links
   counts, pin, progress — re-derived from markdown `[pin:: true]` /
   `[progress:: N]` tokens on every re-index, exactly like the counts).
   It is **forbidden** to hold user intent *as the source of truth*:
   pin state, progress, custom column names, filter state, theme id,
   hotkey bindings must round-trip through the markdown inline task
   syntax (per-block) or YAML/JSON (per-vault/per-user). The cached
   pin/progress columns in the `tasks` table are projections for query
   speed, not authoritative — delete the index and they rebuild from
   markdown.

5. **Settings can be stored in JSON** (the pre-vault / user-global
   tier), but only when the data must be available before a vault is
   open. Everything else that is per-vault goes in YAML.

This is the local-first contract: the user's files on disk *are* the
product. The Svelte UI, the Go backend, and the SQLite index are all
projections of those files, not the other way around.

---

1. System Topology & Process Boundaries

The Silt system runs as a single local process. The operating system boundary separates the low-level compiled disk-access layer from the lightweight front-end view frame using native platform Webview IPC handles:

+-----------------------------------------------------------------------------------+
|                           FRONTEND PROCESS BOUNDARY (Webkit)                      |
|                                                                                   |
|   [Svelte Rendering Framework] <───> [Svelte Store / Reactive State Matrix]       |
|                │                                              ▲                   |
|                ▼ (UI Event)                                   │ (Events/Data)     |
|   +───────────────────────────────────────────────────────────┼───────────────+   |
|   │ Wails JS Runtime (IPC Bridge)                             │               │   |
|   +───────────────────────────────────────────────────────────┼───────────────+   |
+────────────────┼──────────────────────────────────────────────┼────────────────---+
                 │                                              │
                 │ JSON RPC (WebKit MessagePorts)               │ IPC Event Dispatch
                 ▼                                              │
+────────────────┼──────────────────────────────────────────────┼────────────────---+
|                │          BACKEND PROCESS BOUNDARY (Go Core)  │                   |
|   +────────────▼───────────────+                              │                   |
|   │ Wails Binding Router       │                              │                   |
|   +────────────┬───────────────+                              │                   |
|                │ (Internal Calls)                             │                   |
|                ▼                                              │                   |
|   +────────────────────────────+                              │                   |
|   │ Mutex-Locked Disk Writer   │                              │                   |
|   +────────────┬───────────────+                              │                   |
|                │                                              │                   |
|                ├─► [Tmp Write] ──► [Atomic Rename] ──► [Disk] │                   |
|                │                                       │      │                   |
|                ▼                                       ▼      │                   |
|   +────────────────────────────+                +─────────────┴───────────────+   |
|   │ In-Memory SQLite Indexer   │ ◄───────────── | Directory File Monitor      │   |
|   +────────────────────────────+  (Parse Cache) | (fsnotify Engine)           │   |
|                                                 +─────────────────────────────+   |
+-----------------------------------------------------------------------------------+


2. Go Backend Core

The Go runtime orchestrates system access, monitors local storage directories, parses block arrays into structural tokens, and updates the SQLite analytics cache.

2.1 File System Monitor (fsnotify Pipeline)

To allow interoperability with external plain-text editors (e.g. an external editor), the Go backend implements an active directory watcher using github.com/fsnotify/fsnotify.

The Feedback Loop Prevention Strategy

When Silt writes to disk, the fsnotify engine intercepts the write event, which could trigger an accidental infinite parsing loop. To prevent this, the file writer implements an execution flag cache:

package monitor

import (
	"sync"
	"time"
)

type WriteTracker struct {
	mu           sync.Mutex
	activeWrites map[string]time.Time
}

func (wt *WriteTracker) RegisterWrite(filepath string) {
	wt.mu.Lock()
	defer wt.mu.Unlock()
	wt.activeWrites[filepath] = time.Now()
}

func (wt *WriteTracker) IsSelfGenerated(filepath string) bool {
	wt.mu.Lock()
	defer wt.mu.Unlock()
	t, exists := wt.activeWrites[filepath]
	if !exists {
		return false
	}
	// If write event fired within 300ms of our atomic write, ignore it
	if time.Since(t) < 300*time.Millisecond {
		delete(wt.activeWrites, filepath)
		return true
	}
	return false
}


2.2 Custom AST Parser Engine

Every file ingested is scanned line-by-line using a customized Markdown AST engine built on top of yuin/goldmark.

Line Parser Interface

package parser

import "regexp"

// TaskCheckboxRegex matches a GFM checkbox item (`- [ ]`, `- [/]`, `- [x]`)
// plus the remainder of the line. Any checkbox item is a task — the legacy
// TASK keyword was dropped in favour of the Dataview inline-metadata
// standard (SPECS.md §4.1).
var TaskCheckboxRegex = regexp.MustCompile(`^([\s]*)-\s\[([ x/])\]\s+(.*)$`)

// TaskTokenRegex scans the checkbox remainder for `[key:: value]` Dataview
// tokens (due, start, owner, priority, pin, progress). Order-independent
// and extensible via a one-line addition to the scanTaskTokens dispatch.
var TaskTokenRegex = regexp.MustCompile(`\[([\w]+)::\s*([^\]]*)\]`)

type BlockType string

const (
	BlockTask   BlockType = "TASK"
	BlockNote   BlockType = "NOTE"
	BlockHeader BlockType = "HEADER"
	BlockCode   BlockType = "CODE"     // managed fenced code (#189)
	BlockTable  BlockType = "TABLE"    // managed GFM table (#310)
	BlockDetails BlockType = "DETAILS" // managed <details> HTML (#310)
	BlockCallout BlockType = "CALLOUT" // managed Obsidian callout (#308)
)

type ParsedBlock struct {
	ID         string    `json:"id"`
	ParentID   string    `json:"parent_id"`
	Type       BlockType `json:"type"`
	Depth      int       `json:"depth"`
	RawText    string    `json:"raw_text"`
	CleanText  string    `json:"clean_text"`
	Owner      string    `json:"owner,omitempty"`
	StartDate  string    `json:"start_date,omitempty"`
	DueDate    string    `json:"due_date,omitempty"`
	Priority   int       `json:"priority,omitempty"`
	Pinned     *bool     `json:"pinned,omitempty"`
	Progress   int       `json:"progress,omitempty"`
	ExtraTokens []string  `json:"extra_tokens,omitempty"`
	Language   string    `json:"language,omitempty"`
	LineNumber int       `json:"line_number"`
	FileDate   string    `json:"file_date,omitempty"`
}


Unified Region Accumulator (#189/#310/#308)

The parser's `accumulateRegion` detects four multi-line region shapes and
collapses each into ONE managed `ParsedBlock`: fenced code (``` fence),
GFM table runs (header + separator), `<details>` HTML (depth-counted), and
Obsidian callouts (`> [!variant]` + consecutive `>` lines). Each becomes
one `blocks`-table row, one UUID, one FTS5 document. The block identity
comment lives on its own dedicated trailing line after the region content
so the on-disk format stays strictly GFM/HTML/Obsidian syntax. The
`detectRegionKind` / `findRegionCloser` / `skipManagedRegion` helpers are
shared by `ParseFileContent` and `RenderFileContent` so both paths agree
on region boundaries. Old-format files with inline id comments on each
line are detected (id comments stripped before matching), migrated to the
trailing-id-line format on first parse, and `((uuid))` references to
vanished per-line ids are remapped to the typed block's id.


Unique Block ID Injection (UUIDv4)

If a parsed line block does not terminate with an identifier HTML comment (<!-- id: <uuid> -->), the Go backend dynamically assigns a UUID, updates the text line, and flags the file for atomic rewrite:

func EnsureBlockID(line string) (string, string, bool) {
	idRegex := regexp.MustCompile(`<!-- id: ([a-f0-9\-]{36}) -->$`)
	matches := idRegex.FindStringSubmatch(line)
	if len(matches) > 1 {
		return matches[1], line, false
	}
	
	newID := generateUUIDv4()
	cleanLine := strings.TrimRight(line, "\r\n")
	newLine := fmt.Sprintf("%s <!-- id: %s -->", cleanLine, newID)
	return newID, newLine, true
}


3. SQLite Schema & Query Optimization Layer

**Storage-of-truth principle.** Silt's persistent storage is layered with
deliberate, non-overlapping responsibilities:

- **Markdown files** (`.md` in the vault) are the **source of truth for
  content**. Every block, every task marker, every per-task flag (status,
  owner, priority, dates, pin, progress) round-trips through the markdown
  inline syntax. The block identity comment (`<!-- id: uuid @ YYYY-MM-DD -->`)
  is the only identifier stored in the file; everything else is derived
  from the line's position in the file.
- **YAML** (`<vault>/.system/config.yaml`) is the **source of truth for
  per-user, per-vault, per-plugin UI preferences**: active plugin list,
  disabled plugin list, per-plugin settings (e.g. Kanban column list,
  Kanban filter state, theme typography overrides, hotkey bindings,
  editor font sizes).
- **JSON** is the **source of truth for user-global, pre-vault settings**:
  `settings.json` (the active theme + mode) lives at
  `<config>/silt/settings.json` and is the only disk write in the theme
  pipeline, because the active theme must be known *before* a vault is
  open. It is also where non-vault, non-theme user preferences go (font
  pickers, etc.).
- **SQLite** (`<vault>/.system/index.sqlite*`) is **working memory only**:
  a derived, re-derivable cache. It is not a system of record. Any data
  in SQLite must be reproducible from the markdown + YAML above; deleting
  the index file is the documented recovery path. SQLite is *allowed* to
  hold:
  - The block ↔ file-location projection (notebook/section/page/line/file_date),
    so the editor can jump-to-source by block id in O(1).
  - Denormalized per-task caches that are expensive to recompute on
    every query (e.g. `comments_count` = number of child NOTE blocks,
    `links_count` = number of `((uuid))` references in the block body,
    `pinned`/`progress` projected from the `[pin:: true]` /
    `[progress:: N]` markdown tokens). These are **derived** from
    markdown structure (parent_id, raw_content, inline metadata) and
    re-derived on every re-index; they live in SQLite for query speed,
    not because they are user state.
  - The FTS5 full-text index over `clean_content` for `SearchBlocks`.
  - The `files` table (path → mtime/size) that powers incremental re-index.

**It is not allowed to store user intent as the source of truth in SQLite.**
Pin, progress, custom column names, filter state, theme id, hotkey
bindings — these are all user intent and must live in the markdown inline
syntax (for per-block metadata) or in YAML/JSON (for per-vault / per-user
preferences). The `pinned`/`progress` columns in the `tasks` table are the
exception that proves the rule: they are re-derived projections cached for
the Kanban query, rebuilt from markdown on every re-index, and never
written to by user action directly. `pinned` is a **tri-state cache
(`NULL`/`0`/`1` for `[pin:: absent|false|true]`, #135)** so the projection
preserves the explicit-unpinned state the parser/renderer round-trip
(#123); `progress` is a plain `0-100` projection. New features that need
persistent per-task state must extend the markdown inline task syntax
(`[pin:: true]`, `[progress:: N]`, etc.) and round-trip through the parser
+ renderer; if the data is per-user/per-vault, it goes in YAML config.
This is what "local-first" means: the user's files on disk *are* the
product.

The on-disk SQLite lives in WAL mode at `<vault>/.system/index.sqlite`
(+ `.sqlite-wal` + `.sqlite-shm`). On restart only files whose
`mtime`+`size` differ from the last successful index are re-parsed and
re-indexed; a cold start (no index file yet, or the 3 index files
deleted by the user) performs a full scan and rebuild. The recovery
path is documented and intentional: deleting the 3 `.system/index.sqlite*`
files is safe because every row in them is re-derivable from the
markdown + YAML on the next launch. This durable, incremental model is
what lets Silt scale to dozens of notebooks and thousands of pages
without rebuilding the whole index on every launch.

The connection is opened by `db.NewDatabaseManager(dbPath)` (pass `""` for an ephemeral in-memory shared-cache DB, used in tests and before a vault is open). The pragmas below run on every open. `journal_mode=WAL` is persistent in the file header, so once the first on-disk open creates a WAL-mode file, every later connection — including the plugin SDK's read-only handle — inherits it without re-running the pragma.

```sql
-- WAL: persistent in the DB file header (set once, inherited by all connections).
-- On an in-memory DB SQLite silently keeps "memory" — the call is a safe no-op.
PRAGMA journal_mode = WAL;
-- Per-connection (re-applied on every open):
PRAGMA synchronous  = NORMAL;   -- safe under WAL; the WAL itself survives app crashes
PRAGMA temp_store   = MEMORY;
PRAGMA mmap_size    = 268435456; -- 256 MiB mmap threshold for faster reads on large indexes
PRAGMA cache_size   = -64000;    -- 64 MiB per-connection page cache (negative = KB)
PRAGMA busy_timeout = 5000;      -- contended writes wait rather than failing instantly
PRAGMA foreign_keys = ON;
```

Concurrency: WAL allows unlimited readers alongside a single writer; readers never block writers and the writer never blocks readers. The Go-level `core.ExecutionCoordinator` still serializes all access (`SetMaxOpenConns(1)` retained) so the locking story stays simple; relaxing reads to a second pool is an optional follow-up, not required for correctness. Clean shutdown runs `PRAGMA wal_checkpoint(TRUNCATE)` (in `DatabaseManager.Close` and after each startup re-index pass) so the WAL does not grow unbounded across sessions; on a crash, SQLite auto-recovery replays the WAL on the next open.

Caveat: WAL relies on shared memory and therefore does **not** work on network filesystems (NFS/SMB). Local-first single-user desktop is the supported deployment; a vault on a network mount will fail to open the index with a clear error rather than silently corrupt.

-- Blocks Table
-- file_date is per-block (stored inline in the trailing comment
-- <!-- id: uuid @ YYYY-MM-DD -->), not file-level. A page is a single .md
-- file; blocks from different dates coexist in the same page file.
CREATE TABLE blocks (
    id TEXT PRIMARY KEY,
    parent_id TEXT,
    source TEXT NOT NULL DEFAULT 'vault',  -- 'vault' | 'linked:<id>' (#100)
    notebook TEXT NOT NULL,
    section TEXT NOT NULL,
    page TEXT NOT NULL,       -- Page (streaming unit) inside the Section
    file_date TEXT NOT NULL,  -- YYYY-MM-DD
    depth INTEGER DEFAULT 0,
    type TEXT NOT NULL,      -- 'TASK', 'NOTE', 'HEADER'
    raw_content TEXT NOT NULL,
    clean_content TEXT NOT NULL,
    line_number INTEGER NOT NULL,
    FOREIGN KEY(parent_id) REFERENCES blocks(id) ON DELETE SET NULL
);

-- Tasks Metadata Projection Table (Mapped from blocks)
CREATE TABLE tasks (
    block_id TEXT PRIMARY KEY,
    status TEXT NOT NULL,    -- 'TODO', 'DOING', 'DONE'
    owner TEXT,
    start_date TEXT,         -- YYYY-MM-DD or NULL
    due_date TEXT,           -- YYYY-MM-DD or NULL
    priority INTEGER,        -- 1, 2, 3
    pinned INTEGER DEFAULT 0,         -- NULL/0/1 tri-state cache (#135): NULL=no [pin::] token, 0=[pin:: false], 1=[pin:: true]; reproducible from markdown
    progress INTEGER DEFAULT 0,       -- 0-100; cached from [progress:: N] markdown token
    comments_count INTEGER DEFAULT 0, -- derived: child NOTE blocks
    links_count INTEGER DEFAULT 0,    -- derived: ((uuid)) references in body
    FOREIGN KEY(block_id) REFERENCES blocks(id) ON DELETE CASCADE
);

-- Namespace Hierarchical Tags
CREATE TABLE tags (
    block_id TEXT NOT NULL,
    raw_path TEXT NOT NULL,  -- 'work/project/milestone-one'
    level_0 TEXT NOT NULL,   -- 'work'
    level_1 TEXT,            -- 'project'
    level_2 TEXT,            -- 'milestone-one'
    PRIMARY KEY(block_id, raw_path),
    FOREIGN KEY(block_id) REFERENCES blocks(id) ON DELETE CASCADE
);

-- File-stats cache (incremental re-indexing, #29). Keyed by absolute path;
-- a renamed file is a new path, with the stale old row pruned by the next
-- startup scan. Persists in the same on-disk DB so a warm restart skips
-- re-parsing any file whose mtime+size match the last successful index.
CREATE TABLE files (
    path       TEXT PRIMARY KEY,
    mtime      INTEGER NOT NULL, -- Unix nanoseconds
    size       INTEGER NOT NULL,
    indexed_at INTEGER NOT NULL  -- Unix nanoseconds
);

-- Create covered indexes for dynamic query performance
-- idx_blocks_src_file is source-aware (#100): source leads so same-named
-- notebooks across roots don't collide; replaces the pre-source idx_blocks_file.
CREATE INDEX idx_blocks_src_file ON blocks(source, notebook, section, page, file_date);
CREATE INDEX idx_tasks_dates ON tasks(start_date, due_date) WHERE start_date IS NOT NULL OR due_date IS NOT NULL;
CREATE INDEX idx_tags_lookup ON tags(level_0, level_1, level_2);


3.1 External / Linked Notebooks (#100)

A vault is the default home for notebooks, but it is not the only one. A user
can LINK an external folder (e.g. a synced SharePoint/OneDrive/Dropbox mount)
as a notebook and edit it IN PLACE — it is never copied into the vault, so its
existing source of truth and sync/conflict semantics are preserved. The
local-first contract is unchanged: markdown is the product; the SQLite index is
reproducible working memory.

Identity model. `blocks.source` discriminates the root a block belongs to:
`'vault'` for an in-vault notebook, or `'linked:<id>'` for a linked notebook.
This disambiguates same-named notebooks across roots (a vault "Work" and a
linked "Work" never collide on `(notebook, section, page)`). Notebook DISPLAY
NAMES are globally unique — `LinkNotebook` rejects a name that collides with a
vault notebook or an existing link — so the frontend resolves a notebook's
source from its name alone via a name→source map kept in sync on each nav load.
The index stays LOCAL (`<vault>/.system/index.sqlite*`); only the markdown
content (and any co-located `<root>/.system/`) lives on the remote mount.

Link registry. The vault-scoped `config.yaml` carries a `linked_notebooks:`
list (`{id, root_path, display_name}`), persisted atomically by the existing
`config.Save` (self-write suppressed). The registry is vault state (same bucket
as the active plugin list), NOT user-global.

Path resolution. `App.resolveNotebookDir(notebook, source)` returns a
notebook's content directory: `<vault>/<notebook>` for `'vault'`, or the linked
root itself for `'linked:<id>'` (sections/pages live directly under it). Source
is resolved **server-side** by `resolveSourceByName(name)` — notebook display
names are globally unique (link collision rejection), so the name alone maps to
`'vault'` or `'linked:<id>'`. Every notebook-scoped operation (the blockID write
paths via `GetBlockLocation().Source`; CreatePage / CreatePageFromTemplate /
DeletePage / RenamePage / CreateSection / DeleteSection / RenameSection; the
editor focus-lease) routes through it, so linked notebooks get full page CRUD +
focus protection with no parallel frontend source-flow. The traversal guard
generalizes to `isPathWithinRoot(target, root)`.

Multi-root watcher. `DirectoryWatcher` observes the vault root PLUS any number
of linked roots on one process-wide fsnotify watcher, sharing the coordinator,
WriteTracker, and focus-lease maps (all path-keyed, root-agnostic). `AddWatchRoot`
/ `RemoveWatchRoot` register/deregister; `resolveFileMetadata` does a longest-
prefix root lookup and attributes each event: for the vault root the notebook
is the first path component (a vault holds many notebooks); for a linked root
the notebook is the registered display name (the root IS one notebook).

Lifecycle bindings. `LinkNotebook(folderPath)` validates, assigns a stable id,
rejects collisions, folders already inside the vault, **and ancestors of the
vault** (which would double-index the vault), persists the registry, watches +
indexes the tree in a SINGLE batched transaction (forcing `notebook =
DisplayName` so an external file's frontmatter can't drift it out of the nav).
The batched path threads `source` through `IndexScanResults` (the same
function the vault startup scan uses) and does the `files`-table
(`MarkFileIndexed`) pass after the index commit, so a large synced mount
indexes without per-file WAL-checkpoint thrash (#134). `UnlinkNotebook(id)` stops watching,
drops the source's index rows (`ClearSourceBlocks`), and leaves the external
files COMPLETELY UNTOUCHED (safe default). `PickLinkedNotebook()` drives the
native folder picker. Deleting a linked notebook from the sidebar UNLINKS it
(vs. trashing a vault notebook). Page/section delete inside a linked notebook
removes the file IN PLACE (the external folder is the source of truth — Silt
never copies linked content into the vault trash). `RenameNotebook` refuses a
linked notebook (rename = unlink + re-link); page/section rename works in place.

Failure modes. An offline mount degrades gracefully: `ListNavigation` marks the
notebook `Disconnected` (the badge flips to cloud_off) but its last-synced index
rows remain queryable; writes to a disconnected root return a clear error (no
crash). Reconnect re-indexes on the next fsnotify event (the watch survives a
temporary mount drop). Linking a folder that is offline at link time registers
the link and indexes best-effort (logged); the user can re-link once it's back.

Sync-conflict caveat. Silt's atomic write is temp-file + `os.Rename`. On a
network filesystem (SMB/WebDAV) `os.Rename` may not be atomic the way it is on a
local FS, but the `WriteTracker` self-write suppression and the editor focus
lock still hold, so external (non-Silt) edits to the same file are reconciled
by the existing diff/lease machinery once both sides land. A vault index must
stay on a local disk regardless (WAL does not work on NFS/SMB — §3), so only
the markdown crosses the mount.

Co-located per-notebook config (#133). Per the storage-of-truth model, data
attached to a notebook travels with the notebook. For a linked (external)
notebook, per-notebook plugin overrides live at
`<linkedRoot>/.system/config.yaml`, so an external notebook on SharePoint
carries its own config with it — not in the vault. The co-located file is
READ-ONLY to Silt (user-authored); plugin settings continue to persist to the
vault-scoped `config.yaml` via the atomic `UpdatePluginSetting` path. The
co-located file is purely an override layer.

Merge precedence: vault-scoped config.yaml is the baseline; a linked
notebook's co-located file overlays it per-key (linked wins). Nested maps
merge recursively; scalars and arrays from the co-located file replace the
vault's. The merge is computed on every call from the live, mtime-cached
co-located config (see `App.linkedConfigs`), so an external edit is reflected
on the next call. The multi-root watcher observes `<linkedRoot>/.system/
config.yaml` and emits `linked-config:changed` on external edit, driving
reactive refreshes (e.g. Kanban columns/filters re-resolve on the switch).

Resolution surface: `App.GetPluginSettingsForNotebook(pluginID, notebookName)`
is the IPC binding that resolves a plugin's settings for the active notebook
(vault → vault settings verbatim; linked → deep-merge). The SDK
`PluginContext.getPluginSettings()` wraps it with the live `activeNotebook`
reactive getter, so a plugin that calls it at render time always sees the
merged settings for the current notebook.


4. Wails Bridge & IPC API Contract

Communication between Svelte and Go occurs over a typed JSON bridge. The following API commands are registered with the Wails framework.

4.1 Block Mutation Envelope

type MutateBlockPayload struct {
	ID        string `json:"id"`
	FilePath  string `json:"file_path"`
	NewText   string `json:"new_text"`
}


4.2 Query Filter Envelope (Agenda / Calendar)

type TaskQueryFilter struct {
	Owner     string   `json:"owner"`
	Priority  int      `json:"priority"`
	Tags      []string `json:"tags"`
	StartDate string   `json:"start_date"`
	EndDate   string   `json:"end_date"`
}


4.3 Exposed Go Services Methods (App.go)

type App struct {
	ctx context.Context
	db  *sql.DB
}

// FetchPageBlocks returns a flat list of all blocks for a page (single file),
// ordered by line_number. Each block carries its own file_date.
func (a *App) FetchPageBlocks(notebook, section, page string) ([]ParsedBlock, error)

// UpdateBlockState transitions task checkbox and updates raw plaintext files atomically
func (a *App) UpdateBlockState(blockID string, newState string) error

// QueryTasks retrieves indexed items matching the active dashboard layout filters
func (a *App) QueryTasks(filter TaskQueryFilter) ([]TaskResult, error)

// Notebook/Section/Page lifecycle. Silt starts blank; the user opens an
// existing notebook folder or creates one from the sidebar selector. The
// Section layer is optional — a page may live directly under a notebook.
func (a *App) CreateNotebook(name string) error
func (a *App) OpenNotebook(folderPath string) (string, error)
func (a *App) PickNotebookFolder() (string, error)
func (a *App) CreateSection(notebook, section string) error
func (a *App) CreatePage(notebook, section, page, dateStr string) (string, error) // section may be ""
func (a *App) MovePage(notebook, fromSection, toSection, page string) error     // #177 cross-section / root move

// Vault relocation (#141). The active vault can be moved or duplicated to a
// new folder from Settings → General. CopyVaultTree + verifyCopy (in
// backend/vault/mover.go) are the shared core; the SQLite index is NEVER
// copied — it is reproducible working memory (§0 rule 4) and is rebuilt from
// markdown when the destination is first opened, which sidesteps every stale
// absolute-path concern a move would otherwise raise.
//   - CopyVault duplicates the tree; the active vault stays live (no settings
//     change, no event).
//   - MoveVault = copy + verify → cutover: teardownVaultServices → patch the
//     dest config.yaml notebooks.path → persist settings.json (theme/mode
//     preserved) → initializeVaultServices at the new path, with a verbatim
//     rollback to the original path if reinit fails. removeOld deletes the
//     original folder AFTER a successful cutover (non-fatal on failure).
//   - SwitchVault points Silt at an existing vault folder with no picker or
//     scaffold (the Copy flow's "Switch to this vault" affordance).
// Both MoveVault and SwitchVault emit `vault:moved` ({from, to}) so the
// frontend resets navigation and reloads its stores. Linked notebooks are
// external folders and are never moved by any of these.
func (a *App) PickVaultDestination() (string, error)
func (a *App) CopyVault(destPath string) (vault.CopyResult, error)
func (a *App) MoveVault(destPath string, removeOld bool) (vault.MoveVaultResult, error)
func (a *App) SwitchVault(path string) error

// Portable vault archive / backup (#143). Export bundles the active vault
// tree (notes + .system/, EXCLUDING the reproducible .system/index.sqlite*)
// into a single .silt-vault archive (ZIP + custom extension) carrying a
// manifest.json with per-entry + whole-archive-root SHA-256 digests. Import
// validates-before-extract (mirrors the .silt-plugin installer, SPECS §8.4:
// manifest self-consistency + per-entry checksum verification + zip-slip /
// absolute / size-cap guards), streams into a sibling temp dir, atomically
// renames into the empty destination, then calls SwitchVault to open it
// (rebuilding the index from markdown). Both stream determinate progress via
// the vault:archive:progress event ({phase: "export"|"extract", current,
// total}) so the UI renders a progress bar for large vaults. Format detail:
// SPECS §3.4.
func (a *App) PickVaultExportPath(defaultFilename string) (string, error)   // *.silt-vault save dialog
func (a *App) ExportVault(destPath string) (vault.ExportResult, error)      // active vault read-only
func (a *App) PickVaultArchive() (string, error)                            // *.silt-vault open dialog
func (a *App) ImportVault(archivePath, destPath string) (vault.ImportResult, error) // validate → extract → SwitchVault


// ListNavigation returns the Notebook > Section > Page tree for the sidebar,
// enumerated from the on-disk folder structure (source of truth) with block
// counts merged from the index. Section-less pages group under section "".
func (a *App) ListNavigation() (NavigationTree, error)

// System configuration (see §8). GetSystemConfig returns the parsed
// .system/config.yaml; SaveSystemConfig validates + atomically persists +
// applies live knobs (editor.tab_indent_spaces drives parsing) and emits
// config:changed. GetAppVersion returns the embedded VERSION.
func (a *App) GetSystemConfig() (config.SystemConfig, error)
func (a *App) SaveSystemConfig(cfg config.SystemConfig) error

// Per-plugin settings — UpdatePluginSetting is the atomic read-modify-write
// for a single key in the vault-scoped config.yaml (#120).
// GetPluginSettingsForNotebook resolves a plugin's settings for the ACTIVE
// notebook, applying the co-located per-notebook override layer (#133): vault
// notebooks return the vault-scoped entry; linked notebooks return the deep-
// merge of vault defaults with the co-located <root>/.system/config.yaml
// (linked wins per-key). Emits linked-config:changed on external edit of a
// co-located file so reactive consumers (e.g. Kanban) refresh.
func (a *App) UpdatePluginSetting(pluginID, key string, value any) error
func (a *App) GetPluginSettingsForNotebook(pluginID, notebookName string) (map[string]any, error)
func (a *App) GetAppVersion() string


// In-app update check + self-upgrade (#312). backend/updates owns the HTTP,
// semver, download, and SHA256 logic; these are thin Wails-bound wrappers.
// CheckForUpdates issues one unauthenticated GET to GitHub's
// /releases/latest (no token embedded — AC7), semver-compares (the leading
// `v` is stripped; backend/semver is the single source of truth shared with
// plugin min-version enforcement), and stamps settings.json LastUpdateCheck.
// DownloadUpdate re-fetches the release, downloads the platform asset
// (streaming update:download:progress events), and verifies it against the
// published SHA256SUMS before returning the local path — it NEVER returns a
// path for an unverified asset, and a URL not in the release's asset list is
// rejected (defense against a stale/coerced frontend). InstallUpdate launches
// the verified installer and returns WillQuit so the frontend quits via the
// graceful JS runtime.Quit() (OnShutdown flushes the vault + WAL). Get/SetUpdateSettings
// persist the auto-check toggle in user-global settings.json (AutoCheckUpdates
// *bool, default-on; LastUpdateCheck RFC3339) — NOT SQLite (not reproducible)
// and NOT vault config.yaml (must be known before a vault opens).
func (a *App) CheckForUpdates() (updates.UpdateInfo, error)
func (a *App) DownloadUpdate(assetURL string) (string, error)
// InstallUpdate launches the verified installer/relaunch (Windows NSIS
// detached; Linux AppImage $APPIMAGE in-place swap + relaunch) and returns
// InstallUpdateResult{WillQuit}. WillQuit is true when a self-replacing
// installer was launched: the FRONTEND then calls the JS runtime.Quit() so the
// app exits via the graceful OnShutdown path (vault + WAL flush) and the
// installer can replace the locked binary. WillQuit is false for the Linux
// xdg-open hand-off (package-managed install), where the app stays running.
func (a *App) InstallUpdate(localPath string) (InstallUpdateResult, error)
func (a *App) GetUpdateSettings() (UpdateSettingsResult, error)
func (a *App) SetUpdateSettings(autoCheck bool) error


// Open-tab persistence (#142). GetOpenTabs returns the persisted pinned-tab
// set + active tab, pruned against ListNavigation (stale tabs for deleted
// pages are silently dropped). SetOpenTabs atomically persists the pinned set
// + active via configMu + RegisterSelfWrite + config.Save. Only pinned tabs
// are persisted (preview tabs are ephemeral — industry-standard parity).
func (a *App) GetOpenTabs() (OpenTabsResult, error)
func (a *App) SetOpenTabs(openTabs []config.TabRef, activeTab *config.TabRef) error

// Nav-order persistence (#68, #177). GetNavOrder returns the explicit
// ordering for notebooks/sections/pages; SetNavOrder persists it atomically.
// MovePage updates NavOrder.Pages for both the source and target
// sectionKeys on a cross-section move (RenamePage omits this step).
func (a *App) GetNavOrder() (config.NavOrder, error)
func (a *App) SetNavOrder(order config.NavOrder) error
func (a *App) GetSidebarWidth() (int, error)
func (a *App) SetSidebarWidth(width int) error


4.4 Theme Engine IPC & Pipeline

The theme engine is a four-stage pipeline (DESIGN.md §7 / SPECS.md §6.4): canonical schema -> settings persistence -> loader -> runtime injection. It lives in backend/themes and frontend/src/theme and reuses the existing App-binding -> JSON RPC -> Svelte store IPC topology; it does NOT touch SQLite or the file write lock (the only disk write is AppSettings, via the atomic settings.json writer).

Pipeline (single source of truth shared with DESIGN.md §7 / SPECS.md §6.4):

```
  <vault>/.system/themes/*.json          (on-disk user themes)
          │  +  embed.FS cyber_forest.json (guaranteed fallback)
          ▼
  +----------------------------------------------------------+
  | Go: backend/themes                                       |
  |   validate.go  ParseAndValidate (schema sandbox)         |
  |   loader.go    ListThemes / ResolveActive / LoadByID      |
  |   importer.go  ImportThemeFromPath / ExportThemeToPath    |
  |   cache.go     CachedThemeByID (mtime-aware, launch path) |
  |   default.go   embedded canonical default                |
  +----------------------------------------------------------+
          │  Wails JSON RPC (single Bind: { app })
          │   ListThemes / GetActiveTheme / ApplyTheme
          │   ImportTheme / ExportActiveTheme / PickThemeFile
          │   events: theme:changed | themes:changed
          ▼
  +----------------------------------------------------------+
  | Svelte store (frontend/src/theme/store.svelte.ts)        |
  |   themeState   active id/name/mode + dark/light maps     |
  |   themesState  listing + flat tokens (picker previews)   |
  |   resolves "system" locally via prefers-color-scheme     |
  +----------------------------------------------------------+
          │  injectTokens(tokens)
          ▼
  ONE <style id="silt-theme">:root{ ... }</style>   (one DOM write
                                                    -> one recalc
                                                    -> same-tick repaint;
                                                       index.css :root is
                                                       startup fallback only)

  AppSettings (user-global settings.json): { active_theme, theme_mode }
          ▲  atomic write via vault.SaveSettings
          │  ApplyTheme persists here (the only disk write in the engine)
```

Storage layout: theme files live in `<vault>/.system/themes/*.json` (see SPECS §3.2). The **first-class set** (the canonical default `cyber_forest.json` plus Terra Noir, Linen, Stark, and Graphite) is embedded in the binary via `//go:embed themes/*.json` (default.go, an `embed.FS`) and is also what `ScaffoldVault` writes when bootstrapping a vault, so there is a single source of truth for each first-class theme's content. Since Sprint 8, `ListThemes` appends every embedded first-class theme (deduped — an on-disk copy of the same id wins), so the full first-party roster is always selectable even on an empty/wiped vault or an existing vault scaffolded before a theme shipped; `ResolveActive` / `CachedThemeByID` resolve a first-class id from the embed when it is not on disk (removing the non-default first-paint-flashes-default regression). Settings persistence (the active id + mode) is the user-global `settings.json`, not the vault config — it must be known before any vault is open, and it is the only disk write in the entire theme pipeline.

backend/themes package:
- theme.go — Go structs mirroring the canonical modes-based schema; Theme.Flatten(mode) -> map[string]string of CSS custom-property names (--color-void, --color-accent-primary-start, ...) for one mode. The --color-* keys are the SINGLE namespace: Tailwind v4's @theme block declares them (generating utilities like text-accent-primary-start), and the runtime injector overrides them on :root so the whole shell repaints on theme switch (#146 — no bridge/alias layer). HexToRGB converts bg.void to a native RGBA for the webview background. An optional theme-level Typography struct (font_family, mono_font_family, headline_font) emits --font-body, --font-mono, --font-headline when present — these override the config-provided --editor-* variables via CSS fallback chains in index.css.
- validate.go — Validate(*Theme) returns structured per-field ValidationErrors (missing tokens, malformed colors). schema_version is informational (forward compatible). isValidColor narrows the accepted color grammar to #hex / rgb() / rgba() — anything else (named colors, hsl(), url(), <script>, expression()) is rejected at validation time, which is the sandbox for user-imported themes. isValidFontFamily validates optional typography fields by rejecting CSS-breaking characters (;, {, }, <, >) — the same sandbox-by-validation approach used for colors.
- loader.go — LoadTheme, ListThemes (on-disk *.json + the embedded default, deduped by id, per-file load errors collected), ResolveActive (active id -> theme, falling back to the embedded default). LoadByID is a single os.ReadDir + parse that ApplyTheme uses to drop the previous double-scan (#76). ListThemesResult additionally carries a `flat_tokens` map keyed by ThemeInfo.ID so the picker can render hover previews without a second IPC call.
- importer.go (Sprint 6) — ImportThemeFromPath validates a user-supplied JSON, namespaces its id to avoid collisions with built-ins (`user-` prefix on a built-in id; counter suffix on repeat), and writes the on-disk file atomically via parser.WriteFileAtomic. ExportThemeToPath resolves the active id (or the embedded default) and writes the canonical JSON for round-trip editing. Both functions share the loader's ParseAndValidate, so an imported theme is exactly the same object ListThemes enumerates.
- cache.go (Sprint 6) — process-local, mtime-aware cache of parsed themes; CachedThemeByID serves the embedded default directly, on-disk themes from the cache (refreshing on mtime change). InvalidateThemeCache is called by App.ImportTheme so a freshly imported theme is picked up on the next launch-resolution call. The cache replaces Sprint 5's embedded-only path so a non-default active theme no longer shows the default's bg.void as a pre-CSS flash (#73).
- default.go — the embedded first-class theme set (`//go:embed themes/*.json`, an embed.FS): the canonical default (cyber_forest.json) plus Terra Noir, Linen, Stark, and Graphite. `DefaultThemeJSON()` / `ParseDefault()` serve the primary default; `EmbeddedThemes()` enumerates the parsed set (used by ListThemes), `ParseEmbeddedByID(id)` resolves a single first-class id (used by ResolveActive / CachedThemeByID for the off-disk case), and `EmbeddedThemeFiles()` returns filename→raw bytes (used by ScaffoldVault). The app always has a guaranteed-correct fallback (works before a vault exists / when the themes dir is wiped / when the active id is invalid). ScaffoldVault writes every embedded first-class theme idempotently.

Wails-bound App methods (all auto-exposed via the single `Bind: { app }`):
- ListThemes() -> ListThemesResult { themes: []ThemeInfo, errors: []ThemeLoadError, flat_tokens: {id: {dark, light}} }. Used by the picker for the listing + live previews.
- GetActiveTheme() -> ActiveThemeResult { id, name, mode, tokens, dark_tokens, light_tokens, bg_void }. Reads AppSettings, resolves the theme (default fallback), returns BOTH dark+light maps so the frontend resolves "system" locally without a second round-trip. Works before a vault is open (serves the embedded default).
- ApplyTheme(id, mode) -> ActiveThemeResult. Validates id+mode, persists atomically to settings.json, emits a `theme:changed` Wails event, returns the new maps. Unknown id / invalid mode -> structured error, not persisted. Uses themes.LoadByID for a single directory scan (the previous implementation called ListThemes + ResolveActive, doubling the I/O for every switch).
- PickThemeFile() -> string. Native *.json file picker; empty string on cancel.
- ImportTheme(srcPath) -> ImportResult { info, renamed, renamed_from_id, warnings }. Validates, namespaces, writes atomically, emits `themes:changed` (NOT `theme:changed` — that event is for active-theme changes; the picker subscribes to the listing event so the new theme appears immediately without an IPC round-trip from the frontend). ValidationErrors propagate verbatim so the UI can name the offending token and the expected format.
- ExportActiveTheme(dstPath) -> error. Resolves the active id from AppSettings and writes the canonical JSON to the chosen path.

Frontend (frontend/src/theme):
- store.svelte.ts — two reactive stores: `themeState` (active id/name/mode + dark/light token maps) and `themesState` (listing + flat tokens, populated by ListThemes). Subscribes to the `theme:changed` event for the active theme and to the `themes:changed` event for the listing. Exposes applyTheme, pickAndImportTheme, importThemeFromPath, exportActiveTheme, setStatus/clearStatus. The `themeStatus` proxy is rendered in a `role="status" aria-live="polite"` region by the picker (role="alert" on errors).
- inject.ts — injectTokens rewrites a single generated `<style id="silt-theme">:root{...}</style>` element (one DOM write -> one recalc -> same-tick repaint, no flicker/reload/remount). index.css :root values are startup fallbacks only.
- AppearanceTab.svelte (Sprint 6) — live, accessible picker + Dark/Light/System toggle + import button + drop zone (Wails OnFileDrop) + export button. Data-driven from themesState; roving tabindex with Arrow/Home/End, Enter/Space to commit, Esc to cancel preview. Zero per-theme code branches — every row is built from ThemeInfo + ThemeInfo.Swatches.

Launch background: main.go resolves BackgroundColour from the in-process theme cache (CachedThemeByID) so a non-default active theme's bg.void is used for the pre-CSS paint, removing the first-paint flash that matched no token (#73). The cache falls back to the embedded default when no settings exist or the active id is invalid.


4.5 Template Engine IPC & Pipeline

The template engine mirrors the theme engine's two-tier design (§4.4) but is strictly simpler: there is no "active" template (you insert one, you don't wear one), so there is no settings.json persistence and no SQLite/file-write-lock involvement. Templates are vault-scoped Markdown, read-mostly. The only disk writes are user-template save/delete (atomic, self-write-tracked) and the new-page-from-template write (reuses the CreatePage atomic-write path).

Pipeline (single source of truth shared with SPECS.md §6.5 / docs/TEMPLATES.md):

```
  <vault>/.system/templates/*.md      (on-disk user templates)
          │  +  embed.FS builtin/*.md  (10 first-class defaults, read-only)
          ▼
  +----------------------------------------------------------+
  | Go: backend/templates                                    |
  |   template.go   Template/Placeholder/TemplateSummary     |
  |   render.go     Render (substitution; smart-graph        |
  |                  passthrough; unknown→warn)              |
  |   validate.go   Validate (structured ValidationErrors)   |
  |   default.go    //go:embed builtin/*.md                  |
  |   loader.go     ListTemplates / GetTemplate              |
  |   store.go      SaveTemplate / DeleteTemplate            |
  |   cache.go      mtime-aware CachedGetTemplate            |
  |   watcher.go    fsnotify on .system/templates/           |
  +----------------------------------------------------------+
          │  Wails JSON RPC (single Bind: { app })
          │   ListTemplates / GetTemplate / RenderTemplate
          │   RenderTemplateBlocks / SaveUserTemplate
          │   DeleteUserTemplate / ReloadTemplates
          │   RegisterPluginTemplates / UnregisterPluginTemplates (#96)
          │   CreatePageFromTemplate
          │   events: templates:changed
          ▼
  +----------------------------------------------------------+
  | Svelte store (frontend/src/templates/store.svelte.ts)    |
  |   templatesState  listing (TemplateSummary[])            |
  |   initTemplates   load + templates:changed subscription  |
  +----------------------------------------------------------+
          │
          ▼
  TemplatePicker.svelte (modal: search, category groups,
                         live preview, placeholder form,
                         new-page | insert-at-cursor)
```

backend/templates package:
- template.go — Template struct (SchemaVersion, ID, Title, Description, Category, Icon, Placeholders, Body, Source, PluginID). Source is "builtin" (embedded, read-only), "disk" (user-authored, writable), or "plugin" (runtime-registered by a plugin, #96). PluginID is non-empty only when Source == "plugin"; both Source and PluginID are `yaml:"-"` so disk frontmatter can never claim a plugin provenance. SupportedSchemaVersion = "1.0.0" (informational/forward-compatible).
- render.go — Render(t, vars, opts) → (string, warnings). A ~30-line substitution renderer (NOT Go text/template). The placeholder grammar `{{[a-z][a-z0-9_]*}}` structurally excludes Smart Graph syntax: `{{embed:uuid}}` (colon) and `((uuid))` (parentheses) never match the regex, so they pass through byte-for-byte. Built-in defaults (date/time/iso_date/weekday) resolve from RenderOptions.Now in RenderOptions.Timezone. Declared-but-unprovided placeholders stay literal (no warning); truly-unknown tokens stay literal + warn (forward-compat).
- validate.go — Validate(*Template) returns structured ValidationErrors (id grammar `^[a-z0-9_-]+$`, non-empty body/title/schema_version/category, placeholder-name grammar `^[a-z][a-z0-9_]*$`, no duplicate placeholder names, semver schema_version). Categories are additive: an unknown-but-non-empty category is valid (the loader emits a forward-compat warning); only an empty category is rejected.
- default.go — `//go:embed builtin/*.md` (embed.FS). EmbeddedTemplates() / ParseEmbeddedByID(id) / BuiltinIDs() (filename==id convention) / IsBuiltinID(id) (the write-path read-only guard). Fail-loud: an invalid embedded template is a release-blocking authoring bug.
- loader.go — ListTemplates(templatesDir) → on-disk `*.md` (dedup by id, on-disk wins) + every embedded built-in not already on disk; sorted by (Category, Title); per-file load errors + forward-compat warnings collected (never abort). GetTemplate(templatesDir, id) → on-disk then embedded; ErrTemplateNotFound sentinel. ParseTemplateBytes splits YAML frontmatter from body, defaults omitted metadata, validates.
- store.go — SaveTemplate (validate → builtin guard → parser.WriteFileAtomic → canonical re-serialize). DeleteTemplate (builtin guard → os.Remove, idempotent). SerializeTemplate re-emits canonical frontmatter + body (yaml:"-" on Body/Source).
- cache.go — process-local, mtime-aware cache (CachedGetTemplate / InvalidateTemplateCache / ResetCacheForTests). Embedded builtins served from embed (never cached); on-disk templates cached with mtime + TTL freshness.
- watcher.go — TemplateWatcher (fsnotify on .system/templates/). Self-write suppression window (RegisterSelfWrite, called by App before SaveTemplate). Debounced onChange callback (App → InvalidateTemplateCache + templates:changed event). Watches the .system parent when templates/ doesn't exist yet (detects creation).

Wails-bound App methods (all auto-exposed via the single Bind: { app }):
- ListTemplates() → ListTemplatesResult { templates: []TemplateSummary, errors: []TemplateLoadError, warnings: []TemplateLoadError }. Works pre-vault (returns just the embedded set).
- GetTemplate(id) → Template (full, incl. Body). Via the mtime cache.
- RenderTemplate(id, vars) → string. Defaults + vars substituted; warnings logged.
- RenderTemplateBlocks(id, vars) → []ParsedBlock. Rendered + parsed for the insert-at-cursor flow (fresh UUIDs each call).
- SaveUserTemplate(t) → void. Validate + builtin guard + atomic write + RegisterSelfWrite + cache invalidate + templates:changed.
- DeleteUserTemplate(id) → void. Builtin guard + idempotent remove + cache invalidate + templates:changed.
- ReloadTemplates() → void. Cache flush + templates:changed.
- CreatePageFromTemplate(notebook, section, page, dateStr, templateID, vars) → string (the resolved date). Renders the template, prepends the standard frontmatter (§3.3), writes atomically under LockFileWrite + tracker.RegisterWrite (§7.1), indexes via ParseFileContent so tasks/embeds/tags are picked up immediately. Composes with the existing CreatePage path.

Frontend (frontend/src/templates):
- store.svelte.ts — templatesState (listing: TemplateSummary[], loading, loadError) + templateStatus (live-region). loadTemplates() calls ListTemplates. initTemplates() wires the initial load + the templates:changed event subscription (debounced 100ms). _resetForTests() for vitest.
- TemplatePicker.svelte — modal overlay: search box, category-grouped list (role="listbox"/"option", roving tabindex, ↑/↓/Home/End/Enter/Esc), live preview pane (lazy GetTemplate + RenderTemplate), dynamic placeholder form (from Placeholders[]), page-name field (new-page mode). Two entry points: New Page → From Template (sidebar content_copy button + Ctrl+Shift+T hotkey) and /template slash command (TipTapEditor). Cyber-Ink tokens only.


5. Svelte 5 Frontend Architecture

The frontend uses Svelte 5's fine-grained compiler. The editor surface is built on TipTap v3 (ProseMirror engine) via the `svelte-tiptap` adapter, replacing the former per-block contenteditable. The TipTap editor provides native cross-block selection (the core capability the per-block approach could not support), eliminates the text-duplication bug, and delegates IME/selection edge cases to the framework.

5.1 TipTap Editor Surface (one editor per open tab, #142)

Each **open tab** renders a single TipTap editor instance
(`TipTapEditor.svelte`) containing all of that page's blocks. The tab strip
(`TabStrip.svelte`, directly above the editor in the content area) manages the
standard preview-vs-pinned model: a single-click opens a transient
**preview tab** (reusable slot); a double-click, middle-click, or first edit
promotes it to a dedicated **pinned tab**. Multiple editors coexist (one per
open tab, hidden via `display:none` to preserve per-tab scroll, cursor, and
selection); only the active tab is visible and holds the focus lease. The tab
set + active tab persist across restarts via `ui.open_tabs` / `ui.active_tab`
in `config.yaml` (pinned-only; preview tabs are ephemeral).

**Per-notebook tab scoping.** Tabs are scoped per-notebook: the tab strip and
editor surface display only tabs whose `notebook` matches `activeNotebook`
(the `displayedTabs` derived in `App.svelte`). The full `openTabs` array
(tabs from ALL notebooks) persists to config.yaml, so switching notebooks
preserves each notebook's tab set — the sidebar notebook selector activates
the MRU tab for the newly-selected notebook (or shows the blank state if no
tabs exist for it). Cross-notebook navigation (block references, search jumps)
switches `activeNotebook` via `syncActiveFromTab()`, which in turn updates the
displayed tab set.

The editor's transaction lifecycle is wired to the Go backend:
- **Load:** `FetchPageBlocks(notebook, section, page)` returns a flat `[]ParsedBlock`; `blocksToDoc(blocks)` converts to ProseMirror doc JSON; `editor.commands.setContent(doc)` populates the editor.
- **Save:** `editor.on('update')` (debounced via `editor.auto_save_delay_ms`) → `docToBlocks(editor.getJSON())` → `SaveFileBlocks(notebook, section, page, blocks)`. Go's `RenderFileContent` remains the single on-disk serializer.
- **Focus lock (#38):** the editor's `onFocus`/`onBlur` events drive `Acquire/ReleaseFocusLock`; a 20s heartbeat (`RefreshFocusLock`) keeps the lease alive while focused.
- **Per-tab save-state (#167):** `TipTapEditor` exposes `onSaveStateChange({ dirty, error })` on dirty/error/clean transitions. The callback threads through `VirtualScrollContainer` → `App.svelte`, which writes `TabEntry.dirty` / `TabEntry.saveError`. The tab strip renders a dirty glyph (`circle` icon in `--color-text-muted`) or error glyph (`error` icon in `--color-status-danger`) before the page name, visible from any tab — not just the active one. Controlled by `ui.show_tab_dirty_indicators` (default true). The in-editor footer indicator remains the authoritative surface; the tab glyph is a secondary always-visible hint.

The ProseMirror schema defines block node types that map to `parser.ParsedBlock`:
the three prose types (`taskBlock`, `noteBlock`, `headerBlock`) map 1:1, plus
the Sprint 14 block primitives — `calloutBlock` (Obsidian `> [!variant]`),
`codeBlock` (managed multi-line fenced code), the TipTap `details`/`detailsSummary`/
`detailsContent` family (foldable sections), and the TipTap `table`/`tableRow`/
`tableCell`/`tableHeader` family (GFM tables). `noteBlock` additionally carries a
`quote` attr (a `> ` blockquote marker, parallel to `bullet`). Each carries a
UUID `id` attr and a per-block `file_date`. A `UniqueBlockIds` extension
(`appendTransaction`) mints fresh UUIDs for pasted/duplicated blocks to prevent
`blocks`-table PK collisions. `calloutBlock` and `detailsContent` use
`content: 'block+'` so a callout or foldable section can nest task lists, code
blocks, tables, and other callouts; this is safe (no silent drop) because the
converter serializer has an explicit branch for every allowed block type — a
plain `>` body line parses back to a paragraph so legacy multi-paragraph
callouts round-trip byte-for-byte.

**Multi-line block model (unified, #310/#308).** The Go parser reads files
line-by-line and `renderBlock` collapses `\n`→space for the prose types
(TASK/NOTE/HEADER). All multi-line block types use ONE unified strategy: the
parser's `accumulateRegion` detects region openers — fenced code (```),
GFM table runs (header + separator), `<details>` HTML, and Obsidian callouts
(`> [!variant]` + consecutive `>` lines) — and accumulates each into ONE
`ParsedBlock` (type CODE/TABLE/DETAILS/CALLOUT) whose `clean_text` retains
internal newlines. `renderBlock` emits them verbatim (no `\n`→space collapse)
with the block identity comment on its own dedicated trailing line, so the
on-disk format stays strictly GFM/HTML/Obsidian syntax (byte-exact interop
with Obsidian / GitHub / VS Code). The frontend converter (`blocksToDoc`) is
a clean 1:1 map (`blocks.map(blockToNode)`) — the multi-block regrouping layer
that previously faked single-entity semantics for tables/details/callouts is
deleted. Each multi-line block is one `blocks`-table row, one UUID, one
searchable FTS5 document, and one SDK mutation target.

NodeView components (`TaskBlockView`, `NoteBlockView`, `HeaderBlockView`) render the Svelte UI for each block type — checkbox cycle for tasks, drag handles, meta badges. The slash menu (`/` at block start) surfaces commands to change block types.

**Smart Graph NodeViews (#85).** Two additional schema nodes render Smart Graph syntax as live, interactive elements inside the editor. The converter layer (`frontend/src/lib/editor/converters.ts`) tokenizes `clean_text` and emits the corresponding node types inline within the parent `noteBlock`; on save, the textual tokens are reconstructed byte-for-byte so the on-disk file is round-trip identical.

- `embedNode` (block-level, atomic) — `{{embed:uuid}}` becomes a live `EmbedPortal` NodeView. The portal fetches the referenced block via `ResolveBlockReference` and renders it as a nested live view.
- `blockReferenceNode` (inline, atomic) — `((uuid))` becomes a clickable `BlockReferenceChip` NodeView that navigates to the referenced block via the `navigate-to-block` DOM event.

The NodeView wrappers (`frontend/src/components/editor/EmbedNodeView.svelte`, `BlockReferenceNodeView.svelte`) re-use the existing read-mode `EmbedPortal.svelte` and `BlockReferenceChip.svelte` components — the same rendering pipeline serves both the read-mode (search snippets, standalone embeds) and the NodeView contexts.

5.2 Drag-and-Drop Kanban Board

The Kanban board is a first-party plugin (`silt-kanban`, `frontend/src/plugins/first-party/silt-kanban/Kanban.svelte`) that uses the identical `PluginContext` SDK as Agenda and Calendar — no direct `window.go.*` access. It queries tasks via `ctx.sqliteQuery` and shifts status via `ctx.updateBlockState`, preserving the "core feature decoupling" contract (SPECS §8.3).

Cards are rendered as `role="button"` elements with `aria-grabbed`/`aria-label` and animated with Svelte's native `svelte/animate/flip` (200ms cubic-out, per DESIGN.md §6). HTML5 drag-and-drop drives the data; the FLIP animation repositions remaining cards in the same paint frame. Keyboard users change status with ArrowLeft/ArrowRight directly; Enter/click navigates to the source block. The board supports multi-level scope (vault / notebook / section / page) via a segmented control, with the SQL `WHERE` clause built per scope level.


6. Race Conditions, Locking, & Cooldowns

Running a local-first system that allows concurrent UI actions and external filesystem editing requires robust concurrency protections.

6.1 Multi-Thread Access Locking (Go Mutex Pools)

Because SQLite runs in memory, concurrent reads/writes from the Svelte UI and the fsnotify file monitor must be strictly controlled to prevent database locked exceptions. The engine routes all file writing and database tasks through an app-wide synchronization coordinator:

package core

import (
	"database/sql"
	"sync"
)

type ExecutionCoordinator struct {
	dbMu sync.RWMutex
	ioMu sync.Map // Map of filepath -> *sync.Mutex
	DB   *sql.DB
}

func (ec *ExecutionCoordinator) GetFileMutex(filepath string) *sync.Mutex {
	mu, _ := ec.ioMu.LoadOrStore(filepath, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

func (ec *ExecutionCoordinator) LockFileWrite(filepath string, task func()) {
	fMu := ec.GetFileMutex(filepath)
	fMu.Lock()
	defer fMu.Unlock()
	
	task()
}


6.2 Viewport Sync Conflict Mitigation

If you edit a markdown file in an external editor while the Silt dashboard is open, the file-watcher triggers a rebuild of the SQLite cache. If Svelte is actively editing the same line, the changes could conflict.

Mitigation Plan:

Focus Locking (TTL leases): While the TipTap editor has focus on a page, the backend monitor holds a time-limited lease on that page's file and pauses external sync operations for it. The editor acquires the lease on focus (`AcquireFocusLock`), refreshes it on a 20s heartbeat while focused (`RefreshFocusLock`), and releases on blur (`ReleaseFocusLock`). One editor per page = one lease per file. The Go side runs a background sweeper (`monitor.DirectoryWatcher.startLeaseSweeper`) that drops expired leases every `TTL/2` (default TTL 60s), so if a component unmounts without releasing — route change, crash, hot-reload — fsnotify suppression self-heals within a minute instead of leaking forever (#38). `RefreshFocusLock` is a no-op on an already-expired lease (the editor must re-acquire), so a stale heartbeat can't resurrect suppression. On shutdown / `CloseVault`, `ReleaseAllFocus` clears every outstanding lease so a clean exit can't strand a file. The `WriteTracker` self-write cooldown is unaffected.

Deterministic Diff Verification: Instead of overwriting entire files when external changes occur, Go computes a diff patch based on block IDs to preserve uncommitted cursor inputs.


7. Plugin Subsystem & Smart Graph Events

7.1 Plugin Loader Pipeline (Frontend)

The Svelte shell discovers and renders plugins at boot:

config.yaml (optional active whitelist)
        │
        ▼
ListPlugins() → .system/plugins/<id>/ folders (skip .disabled sentinel)
        │
        ▼
resolve each id:
   first-party registry (bundled Svelte component)  ──► always available
   on-disk → ReadPluginSource(id) → Blob URL → import(/* @vite-ignore */)
        │
        ▼
plugin.init(ctx: PluginContext)   ←   sqliteQuery (SELECT/WITH-only),
                                      mutateBlock, updateBlockState,
                                      updateTaskMeta, ctx.on (typed event bus)
plugin.onVaultOpen(ctx)             ←   v2 lifecycle hook (#106)
         │
         ▼
App view router renders plugin:<id> via PluginView (or Agenda/Calendar slots)

Per-plugin load failures are collected and surfaced (PluginView shows a load-error notice) without aborting boot. The `plugins:changed` Wails event (emitted after install/uninstall/enable/disable) re-runs discovery.

7.2 v2 SDK Capability & Permission Model (#113)

Every privileged v2 SDK binding (file I/O, network, OS integration, editor
schema, rendered UI, content mutation) is gated server-side by
`App.requireGrant(pluginID, capability)`. Grants are per-vault, stored in
`config.yaml` under `plugins.grants` (pluginID → capability → qualifier).
First-use prompts the user (contextual, low-fatigue); Settings → Plugins shows
requested vs. granted with revoke. First-party plugins are implicitly granted.
`exec` is deferred until the trust/signing model matures.

Capabilities: `read-files`, `write-files`, `network`, `os-open`,
`os-clipboard`, `os-notify`, `ui-surface`, `editor-schema`,
`content-mutate` (#156 — gates block CRUD).

**Binding identity (#151).** High-risk bindings (fetch, file write/delete,
block CRUD) also validate a session token: the loader calls
`RegisterPluginSession(pluginID)` at load time and the SDK closures capture the
token. The Go side verifies `token ↔ pluginID` before `requireGrant` so a
plugin cannot impersonate another by calling a raw binding with a different
pluginID. This is a stepping stone; the full fix requires per-plugin isolated
webviews (#152, deferred).

**Registry-internal gates (#158).** The three frontend registries
(slash-registry, surfaces, decorations) check `isGranted(pluginID, cap)` from a
Go-provided grant cache — NOT from the plugin's self-declared manifest. A
plugin importing `registerSlashCommand` directly still hits the gate.

**Iframe CSP (#149).** Plugin UI surfaces (iframe srcdoc) carry a restrictive
CSP: `connect-src 'none'` blocks direct fetch/XHR/WebSocket from inside the
iframe. All network traffic routes through the postMessage bridge → `ctx.fetch`
(SSRF-defended + audit-logged).

**Rate limiting (#153).** `PluginFetch` is throttled by a per-plugin token-
bucket rate limiter (default 1 rps, burst 10; manifest `ratelimit` override).
Buckets are evicted on uninstall.

**Network audit log (#157).** `auditNetwork` appends to the in-memory log
(capped 500 entries) under `networkAuditMu`, then enqueues a disk-write op
onto a buffered channel. A single background goroutine (`startNetworkAuditWriter`,
started in `initializeVaultServices`, stopped first in `teardownVaultServices`)
drains the channel and writes to the per-plugin `network.log` WITHOUT holding
the lock, so concurrent `PluginFetch` calls don't serialize on file I/O
(#235). On vault open, `seedNetworkAuditFromDisk` reads the on-disk logs to
seed the in-memory log (before the writer starts). No SQLite table (audit data
is not reproducible from markdown; ARCHITECTURE §0 rule 4).

**Runtime integrity (#161).** `Install` computes `sha256(index.js)` and writes
it into `plugin.json` as `contentSha256`. The frontend loader verifies the hash
via `crypto.subtle.digest` before Blob import. A tampered `index.js` is refused.

7.3 v2 SDK Extended APIs

- Content API (#104): query helpers (queryByTag/queryByDateRange/
  fullTextSearch/getBacklinks/getEmbeds) + block CRUD (createBlock/
  deleteBlock/moveBlock) + page/section/notebook CRUD + bulk ops.
- File I/O (#108): readFile/writeFile/deleteFile/listDir (notebook-scoped,
  traversal-guarded, atomic-write path); scratch space at
  <notebook>/.system/plugins/<id>/data/.
- OS integration (#114): openInNativeHandler, openUrl (scheme-restricted),
  pickers, clipboard, notify — all capability-gated.
- Network/fetch (#115): ctx.fetch via Go net/http proxy (timeout/size/
  redirect caps); SSRF defense at URL validation, redirect re-validation, AND
  dial-time — the custom `DialContext` re-runs `isInternalIP` on every resolved
  IP (DNS-rebinding guard) and fails closed on lookup error so the OS resolver
  cannot bypass the check (#234); audit-logged.
- Editor extension points (#110): slash-command registry; generic embedBlock
  node (round-trips through <!-- silt-embed: {json} --> markers).
- Rendered UI surfaces (#117): sandboxed <iframe srcdoc> + postMessage bridge;
  sidebar panel / modal / status-bar surfaces; theme tokens injected.
- Settings schema (#103): declarative SettingSchema[] on the manifest;
  generic form renderer replaces bespoke panels.

See `frontend/src/plugins/sdk.ts` for the full typed contract and
`docs/PLUGIN_DEVELOPMENT.md` §8 for the author guide.

7.2 PluginContext → Go Bindings

PluginContext is a thin frontend wrapper over four Wails bindings on App:

- PluginRawQuery(sql, params) — read-only; rejects anything not starting with SELECT/WITH; routed through ExecutionCoordinator.WithDBRead; returns row maps.
- PluginMutateBlock(id, text) / PluginUpdateBlockState(id, status) — wrap MutateBlock / UpdateBlockState (same atomic-write + re-index + lock path as the core editor).
- GetPluginRegistry() / ListPlugins() / ReadPluginSource(id) — discovery.
- ValidatePluginArchive / PickPluginArchive / InstallPlugin / UninstallPlugin / EnablePlugin / DisablePlugin — `.silt-plugin` distribution (see backend/plugins package; zip-slip + traversal guarded, atomic extract).

7.3 Smart Graph Events

Block mutations broadcast a `block:changed` Wails event (BlockChangedEvent {ID, Notebook, Section, Page, FileDate}) so live embeds (`{{embed:uuid}}`) and references (`((uuid))`) refresh in real time. Emitted from MutateBlock, UpdateBlockState, and the post-write path of SaveFileBlocks; emission no-ops when ctx is nil (tests). The frontend EmbedPortal subscribes via EventsOn and re-fetches its source block when the event matches its uuid (a module-scoped render-stack guard stops recursive embed loops).
8. System Configuration Engine (config.yaml)

Global settings — editor defaults, parsing rules, hotkeys, and the plugin registry — live in <vault>/.system/config.yaml, the single source of truth for everything except the vault path (which stays in OS-config settings.json because it must be known before any vault can be opened).

8.1 Parser (backend/config)

config.SystemConfig mirrors the SPECS §9.1 schema (notebooks / editor / parsing / hotkeys / plugins / ui). The `ui.*` block holds per-vault UI preferences: `sidebar_width`, `nav_order` (explicit section/page ordering for drag-to-reorder, #68/#177), `open_tabs` / `active_tab` (pinned-tab persistence, #142 — preview tabs are ephemeral), `enable_preview_tabs`, `max_open_tabs`, `show_format_toolbar` (#168), `show_tab_dirty_indicators` (#167, default true), `dismissed_tips`, and `formatting.*` toggles. Load(vaultPath) decodes over config.Defaults() so omitted sections keep their default values rather than being zero-valued; a missing file returns defaults (non-fatal), but a file that exists and fails to parse returns an error (fail-loud — never silently fall through). Save(vaultPath, cfg) is atomic (temp file + fsync + rename), matching the durability guarantee of note writes. The App holds the parsed config under configMu and replaces it wholesale on reload (never mutated in place), so a struct read under RLock is a safe snapshot.

8.2 Hot-Reload (backend/config.ConfigWatcher)

A dedicated fsnotify watcher observes the .system parent directory (not the file alone) so a delete+recreate of config.yaml is still observed. Self-loop prevention is a local time-window in ConfigWatcher: SaveSystemConfig calls RegisterSelfWrite() before the atomic write, and the watcher ignores every config.yaml event until a 500ms window elapses — a single logical save can emit several fsnotify events (atomic temp+rename, or truncate+write), so the window suppresses all of them, not just the first. External edits re-parse and invoke onChange → App.applyConfig (updates live knobs + emits config:changed); a parse failure invokes onError → config:error (last-good config retained). This implements SPECS §9.2 without an application restart.

8.3 Settings Menu (frontend)

The settings store (settings/store.svelte.ts) is a $state object exposing loadConfig/saveConfig, dirty tracking, and a config:changed / config:error subscription. The SettingsShell is a full-screen frosted overlay with a left tab rail (General / Appearance / Plugins / About), roving keyboard navigation (Arrow/Home/End, Esc to close), and ARIA tablist semantics. GeneralTab edits a local draft (Save/Revert) so an external hot-reload cannot fight a half-edited form; if an external change lands while the draft is dirty, the draft is preserved and a non-blocking "reload" notice is shown (never a silent clobber). The Plugins tab (#65) is the single plugin UI: rich cards (first-party bundled vs. third-party installed), enable/disable (all plugins — first-party via config.yaml `plugins.disabled` list, third-party via `.disabled` sentinel), uninstall (third-party only), inline load errors, an expandable detail panel with per-plugin settings, and the .silt-plugin install flow. The standalone PluginManagerModal was removed in favour of this tab; the titlebar extension icon opens Settings → Plugins.

8.4 Editor Config Consumer (frontend)

The editor-token pipeline (settings/editor-tokens.svelte.ts) mirrors the theme injector pattern (§4.4): editor.* config values (font_family, mono_font_family, font_size_px, line_height) are injected as CSS custom properties (--editor-font-family, --editor-mono-font-family, --editor-font-size, --editor-line-height) on :root via a dedicated <style id="silt-editor"> element, separate from the theme injector's <style id="silt-theme">. initEditorTokens() uses $effect.root to watch the reactive settings store, so config changes apply live (one DOM write → one recalculation → same-tick repaint) without a reload or remount. The index.css :root values are startup fallbacks only.

TipTapEditor (the live block editor, frontend/src/components/TipTapEditor.svelte)
consumes the full editor.* config surface: typography flows through the CSS
variables (font-family, font-size, line-height on the contenteditable);
auto_save_delay_ms drives the triggerAutoSave debounce; focus_highlight_ancestors
gates the guide-rail active highlight; show_word_count toggles a subtle
CharacterCount display; focus_mode dims non-active paragraphs; and
indent_block / unindent_block hotkeys are matched via matchHotkey
(settings/hotkeys.ts). The cycle_view_layout hotkey is wired in App.svelte's
global keydown handler alongside open_search, toggle_sidebar, and
toggle_view_mode. Inline formatting marks (#168), block alignment (#173),
text/background color (#170), and the source/edit view toggle (#171) are all
additive to clean_text — the Go parser sees formatted text as opaque and
requires zero parser changes.


9. Performance Budgets & System Tray

9.1 Boot-Scanner Budget (Hard Regression Gate)

TestScanWorkspace_BudgetRegression (backend/parser/parser_test.go) seeds 1,000 small page files and asserts ScanWorkspace completes in under 450ms (baseline ~280ms on Ryzen AI MAX+ / Go 1.25 / Windows). The test runs in the normal `go test -race ./...` CI gate (skipped under `-short`) so a regression is caught immediately, not only when someone runs `-bench`.

9.2 Atomic-Write Safety (Kill-Mid-Write WAL Recovery)

TestAtomicWrite_KillMidWriteRecoversViaWAL (backend/db/db_test.go) simulates a destructive exit (SIGKILL / power loss) by closing the raw `*sql.DB` handle WITHOUT the `PRAGMA wal_checkpoint(TRUNCATE)` that `DatabaseManager.Close` performs. A subsequent `NewDatabaseManager` (the "next launch") auto-replays the WAL, recovering every committed block. The test also asserts zero stray `*.tmp` files in the vault directory. TestWriteFileAtomic_NoTruncatedFilesOnKill verifies 100 concurrent atomic writes to different files leave no truncated content.

9.3 UI Frame-Budget Probe

frontend/src/lib/perf/frame-budget.ts provides `measureFrameBudget(label, fn)` — a dev-only probe (gated on `?perf=1` in the URL; zero-cost pass-through otherwise) that wraps a callback in `performance.mark`/`measure` + `requestAnimationFrame` and logs the elapsed time against the 16ms frame budget. Instrumented on the three highest-stress paths: Kanban drag-drop settle, TipTap editor transaction (docToBlocks), and theme-token injection.

9.4 System Tray (Deferred)

Wails v2.12 has an internal `menu.TrayMenu` struct but does NOT expose a public runtime API to register tray menus from application code. The system tray icon + minimize-to-tray feature is blocked by this API gap and will be revisited when Wails v3 (which has full tray support) is adopted. The production build pipeline (`wails build --clean`), memory budget (<65MB idle), and cross-platform artifacts (Windows NSIS + portable zip, Linux AppImage + .deb) are the deliverables for issue #23.
