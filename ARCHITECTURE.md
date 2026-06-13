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

SQLite acts strictly as an volatile, in-memory index. If the application is restarted, the index is entirely rebuilt from the Markdown directories in < 450ms.

-- Disable disk synchandles for maximum in-memory speed
PRAGMA journal_mode = MEMORY;
PRAGMA synchronous = OFF;

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
// existing notebook folder or creates one from the sidebar selector.
func (a *App) CreateNotebook(name string) error
func (a *App) OpenNotebook(folderPath string) (string, error)
func (a *App) CreateSection(notebook, section string) error
func (a *App) CreatePage(notebook, section, page, dateStr string) (string, error)

// ListNavigation returns the Notebook > Section > Page tree for the sidebar.
func (a *App) ListNavigation() (NavigationTree, error)


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

Focus Locking: While Svelte has focus on an active text field, the backend monitor pauses external sync operations for that specific block file.

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