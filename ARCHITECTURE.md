Engineering Architecture: Silt

This document details the low-level system design, state machines, data pipelines, and performance constraints of Silt. It acts as the direct engineering blueprint for developers implementing the Go core and Svelte interface layers.

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

To allow interoperability with external plain-text editors (e.g., VS Code, Obsidian), the Go backend implements an active directory watcher using github.com/fsnotify/fsnotify.

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

var TaskRegex = regexp.MustCompile(`^([\s]*)-\s\[([ x/])\]\s(TODO|DOING|DONE)\sTASK(?:\s\[([^\]]*)\])?(?:\(([^)]*)\))?(?:#(\d+))?\s(.*)$`)

type BlockType string

const (
	BlockTask BlockType = "TASK"
	BlockNote BlockType = "NOTE"
	BlockHeader BlockType = "HEADER"
)

type ParsedBlock struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parent_id"`
	Type      BlockType `json:"type"`
	Depth     int       `json:"depth"`
	RawText   string    `json:"raw_text"`
	CleanText string    `json:"clean_text"`
	Owner     string    `json:"owner,omitempty"`
	StartDate string    `json:"start_date,omitempty"`
	DueDate   string    `json:"due_date,omitempty"`
	Priority  int       `json:"priority,omitempty"`
}


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

SQLite is a persistent on-disk index in WAL mode at `<vault>/.system/index.sqlite` (+ `.sqlite-wal` + `.sqlite-shm`). On restart only files whose `mtime`+`size` differ from the last successful index are re-parsed and re-indexed; a cold start (no index file yet, or the 3 index files deleted by the user) performs a full scan and rebuild. Markdown remains the source of truth — the index is disposable and disposable only; deleting the 3 `.system/index.sqlite*` files is the documented recovery path. This durable, incremental model is what lets Silt scale to dozens of notebooks and thousands of pages without rebuilding the whole index on every launch.

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
CREATE TABLE blocks (
    id TEXT PRIMARY KEY,
    parent_id TEXT,
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
    FOREIGN KEY(block_id) REFERENCES blocks(id) ON DELETE CASCADE
);

-- Namespace Hierarchical Tags
CREATE TABLE tags (
    block_id TEXT NOT NULL,
    raw_path TEXT NOT NULL,  -- 'work/sogav/milestone-one'
    level_0 TEXT NOT NULL,   -- 'work'
    level_1 TEXT,            -- 'sogav'
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
CREATE INDEX idx_blocks_file ON blocks(notebook, section, page, file_date);
CREATE INDEX idx_tasks_dates ON tasks(start_date, due_date) WHERE start_date IS NOT NULL OR due_date IS NOT NULL;
CREATE INDEX idx_tags_lookup ON tags(level_0, level_1, level_2);


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

// FetchPageTimeline returns day-grouped blocks for the streaming Page
// (notebook/section/page), paged for infinite virtualization scroll.
func (a *App) FetchPageTimeline(notebook, section, page string, offset int, limit int) ([]DayGroup, error)

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
func (a *App) GetAppVersion() string


4.4 Theme Engine IPC & Pipeline

The theme engine is a four-stage pipeline (DESIGN.md §7 / SPECS.md §6.4): canonical schema -> settings persistence -> loader -> runtime injection. It lives in backend/themes and frontend/src/theme and reuses the existing App-binding -> JSON RPC -> Svelte store IPC topology; it does NOT touch SQLite or the file write lock (the only disk write is AppSettings, via the atomic settings.json writer).

backend/themes package:
- theme.go — Go structs mirroring the canonical modes-based schema; Theme.Flatten(mode) -> map[string]string of CSS custom-property names (--bg-void, --accent-primary-start, ...) for one mode. HexToRGB converts bg.void to a native RGBA for the webview background.
- validate.go — Validate(*Theme) returns structured per-field ValidationErrors (missing tokens, malformed colors). schema_version is informational (forward compatible).
- loader.go — LoadTheme, ListThemes (on-disk *.json + the embedded default, deduped by id, per-file load errors collected), ResolveActive (active id -> theme, falling back to the embedded default).
- default.go — the canonical default theme (cyber_forest.json) embedded via embed.FS so the app always has a guaranteed-correct fallback (works before a vault exists / when the themes dir is wiped / when the active id is invalid). ScaffoldVault writes this same embedded JSON.

Wails-bound App methods (all three auto-exposed via the single `Bind: { app }`):
- ListThemes() -> ListThemesResult { themes: []ThemeInfo, errors: []ThemeLoadError }
- GetActiveTheme() -> ActiveThemeResult { id, name, mode, tokens, dark_tokens, light_tokens, bg_void }. Reads AppSettings, resolves the theme (default fallback), returns BOTH dark+light maps so the frontend resolves "system" locally without a second round-trip. Works before a vault is open (serves the embedded default).
- ApplyTheme(id, mode) -> ActiveThemeResult. Validates id+mode, persists atomically to settings.json, emits a `theme:changed` Wails event, returns the new maps. Unknown id / invalid mode -> structured error, not persisted.

Frontend (frontend/src/theme):
- store.svelte.ts — $state store holding the active id/mode + dark/light maps; subscribes to GetActiveTheme/ApplyTheme; resolves "system" via prefers-color-scheme and re-resolves live on OS-theme change; re-paints on the `theme:changed` event.
- inject.ts — injectTokens rewrites a single generated `<style id="silt-theme">:root{...}</style>` element (one DOM write -> one recalc -> same-tick repaint, no flicker/reload/remount). index.css :root values are startup fallbacks only.

Launch background: main.go resolves BackgroundColour from the embedded default theme's bg.void (sync LoadSettings + embed.FS, before wails.Run) so the pre-CSS flash tracks the active theme mode.


5. Svelte 5 Frontend Architecture

The frontend uses Svelte 5’s fine-grained compiler to handle rapid content editing and real-time drag-and-drop operations without triggering bulk UI re-renders.

5.1 Infinite Timeline Virtualizer

To render sections containing years of logs without degrading memory, the Svelte layer virtualizes scrolling lists.

<!-- VirtualScrollContainer.svelte -->
<script lang="ts">
  import { onMount } from 'svelte';
  
  let { notebook, section, page } = $props();
  
  let visibleGroups = $state<DayGroup[]>([]);
  let listContainer: HTMLDivElement;
  let offset = $state(0);
  let loading = $state(false);

  async function loadMoreDays(direction: 'top' | 'bottom') {
    if (loading) return;
    loading = true;
    
    const newDays = await window.go.main.App.FetchPageTimeline(
      notebook, 
      section,
      page,
      offset, 
      15
    );
    
    if (direction === 'bottom') {
      visibleGroups = [...visibleGroups, ...newDays];
      offset += 15;
    } else {
      visibleGroups = [...newDays, ...visibleGroups];
    }
    
    loading = false;
  }

  function handleScroll() {
    const { scrollTop, scrollHeight, clientHeight } = listContainer;
    // Load next block when scrolled within 200px of container bottom
    if (scrollHeight - scrollTop - clientHeight < 200) {
      loadMoreDays('bottom');
    }
  }
</script>

<div bind:this={listContainer} onscroll={handleScroll} class="overflow-y-auto h-screen pr-2">
  {#each visibleGroups as group (group.date)}
    <section class="mb-8 border-l border-zinc-800 pl-4 relative group">
      <h2 class="text-sky-400 font-bold mb-4 sticky top-0 bg-[#121214] py-2 z-10">{group.formattedDate}</h2>
      
      {#each group.blocks as block (block.id)}
        <BlockRenderer {block} />
      {/each}
    </section>
  {/each}
</div>


5.2 Responsive Drag-and-Drop Kanban Store

Svelte stores track active task states across dashboard Kanban lanes. Moving a card updates the store and sends mutations directly across the IPC bridge to ensure immediate sync back to disk.

import { writable } from 'svelte/store';

export interface KanbanCard {
    id: string;
    title: string;
    status: 'TODO' | 'DOING' | 'DONE';
    owner?: string;
    dueDate?: string;
    priority: number;
}

function createKanbanStore() {
    const { subscribe, set, update } = writable<Record<string, KanbanCard[]>>({
        TODO: [],
        DOING: [],
        DONE: []
    });

    return {
        subscribe,
        loadTasks: (tasks: KanbanCard[]) => {
            const cols = { TODO: [], DOING: [], DONE: [] };
            tasks.forEach(t => cols[t.status].push(t));
            set(cols);
        },
        moveCard: async (cardId: string, fromCol: string, toCol: string, targetIndex: number) => {
            update(cols => {
                const card = cols[fromCol].find(c => c.id === cardId);
                if (!card) return cols;

                // Mutate state locally for instant visual feedback
                cols[fromCol] = cols[fromCol].filter(c => c.id !== cardId);
                card.status = toCol as 'TODO' | 'DOING' | 'DONE';
                cols[toCol].splice(targetIndex, 0, card);

                return cols;
            });

            // Write change back to filesystem and SQLite Cache
            await window.go.parser.App.UpdateBlockState(cardId, toCol);
        }
    };
}

export const kanbanStore = createKanbanStore();


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

If you edit a markdown file in VS Code while the Silt dashboard is open, the file-watcher triggers a rebuild of the SQLite cache. If Svelte is actively editing the same line, the changes could conflict.

Mitigation Plan:

Focus Locking (TTL leases): While Svelte has focus on an active text field, the backend monitor holds a time-limited lease on that file and pauses external sync operations for it. The Svelte editor acquires the lease on focus, refreshes it on a 20s heartbeat while focused (and on every save), and releases on blur. The Go side runs a background sweeper (`monitor.DirectoryWatcher.startLeaseSweeper`) that drops expired leases every `TTL/2` (default TTL 60s), so if a component unmounts without releasing — route change, crash, hot-reload — fsnotify suppression self-heals within a minute instead of leaking forever (#38). `RefreshFocusLock` is a no-op on an already-expired lease (the editor must re-acquire), so a stale heartbeat can't resurrect suppression. On shutdown / `CloseVault`, `ReleaseAllFocus` clears every outstanding lease so a clean exit can't strand a file. The `WriteTracker` self-write cooldown is unaffected.

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
                                      mutateBlock, updateBlockState
        │
        ▼
App view router renders plugin:<id> via PluginView (or Agenda/Calendar slots)

Per-plugin load failures are collected and surfaced (PluginView shows a load-error notice) without aborting boot. The `plugins:changed` Wails event (emitted after install/uninstall/enable/disable) re-runs discovery.

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

config.SystemConfig mirrors the SPECS §9.1 schema (notebooks / editor / parsing / hotkeys / plugins). Load(vaultPath) decodes over config.Defaults() so omitted sections keep their default values rather than being zero-valued; a missing file returns defaults (non-fatal), but a file that exists and fails to parse returns an error (fail-loud — never silently fall through). Save(vaultPath, cfg) is atomic (temp file + fsync + rename), matching the durability guarantee of note writes. The App holds the parsed config under configMu and replaces it wholesale on reload (never mutated in place), so a struct read under RLock is a safe snapshot.

8.2 Hot-Reload (backend/config.ConfigWatcher)

A dedicated fsnotify watcher observes the .system parent directory (not the file alone) so a delete+recreate of config.yaml is still observed. Self-loop prevention is a local time-window in ConfigWatcher: SaveSystemConfig calls RegisterSelfWrite() before the atomic write, and the watcher ignores every config.yaml event until a 500ms window elapses — a single logical save can emit several fsnotify events (atomic temp+rename, or truncate+write), so the window suppresses all of them, not just the first. External edits re-parse and invoke onChange → App.applyConfig (updates live knobs + emits config:changed); a parse failure invokes onError → config:error (last-good config retained). This implements SPECS §9.2 without an application restart.

8.3 Settings Menu (frontend)

The settings store (settings/store.svelte.ts) is a $state object exposing loadConfig/saveConfig, dirty tracking, and a config:changed / config:error subscription. The SettingsShell is a full-screen frosted overlay with a left tab rail (General / Appearance / Plugins / About), roving keyboard navigation (Arrow/Home/End, Esc to close), and ARIA tablist semantics. GeneralTab edits a local draft (Save/Revert) so an external hot-reload cannot fight a half-edited form; if an external change lands while the draft is dirty, the draft is preserved and a non-blocking "reload" notice is shown (never a silent clobber). The Plugins tab (#65) is the single plugin UI: rich cards (first-party bundled vs. third-party installed), enable/disable, uninstall (first-party protected), inline load errors, an expandable detail panel with per-plugin settings, and the .silt-plugin install flow. The standalone PluginManagerModal was removed in favour of this tab; the titlebar extension icon opens Settings → Plugins.

8.4 Editor Config Consumer (frontend)

The editor-token pipeline (settings/editor-tokens.svelte.ts) mirrors the theme injector pattern (§4.4): editor.* config values (font_family, mono_font_family, font_size_px, line_height) are injected as CSS custom properties (--editor-font-family, --editor-mono-font-family, --editor-font-size, --editor-line-height) on :root via a dedicated <style id="silt-editor"> element, separate from the theme injector's <style id="silt-theme">. initEditorTokens() uses $effect.root to watch the reactive settings store, so config changes apply live (one DOM write → one recalculation → same-tick repaint) without a reload or remount. The index.css :root values are startup fallbacks only.

BlockRenderer (the live block editor) consumes the full editor.* config surface: typography flows through the CSS variables (font-family, font-size, line-height on the contenteditable and read-mode divs); auto_save_delay_ms drives the triggerAutoSave debounce (0 = immediate, no timer); focus_highlight_ancestors gates the guide-rail active highlight; and indent_block / unindent_block hotkeys are matched via matchHotkey (settings/hotkeys.ts) so users can remap or disable them from Settings → General. The cycle_view_layout hotkey is wired in App.svelte's global keydown handler alongside open_search and toggle_sidebar, cycling through the main views (notes → tags → agenda → calendar → kanban).
