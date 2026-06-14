# Testing & Verification — Sprint 1 (Foundation)

## Automated Tests

Run with: `go test -race -count=1 ./...`

### Coverage by package

| Package | Tests | What is covered |
|---|---|---|
| `silt` (main) | FindLineByBlockID, sanitizePathSegment, isPathWithinVault, UpdateBlockState (transitions, traversal rejection, non-task rejection), FetchSectionTimeline (pagination, empty), QueryTasks (owner, priority, tags, hydration) | Wails API surface |
| `backend/core` | DB write serialization, DB read concurrency, per-file lock isolation, same-file serialization, error propagation | ExecutionCoordinator |
| `backend/db` | Block insertion with cascade, replacement, empty re-index, frontmatter tag attachment (loop-index fix), metadata-change re-index stability, tag deduplication, N+1 fix verification, pagination and empty timeline, filter combinations, tag hydration, IndexScanResults skip-collection | DatabaseManager |
| `backend/monitor` | Tracker immediate check, cooldown timeout, expired entry cleanup, background sweeper, prune expired, stop idempotency, concurrent PruneExpired, reindexFile lock-holding test, reindexFile end-to-end | DirectoryWatcher, WriteTracker |
| `backend/parser` | ID injection, date normalization (4 cases), line parsing (task/header/note), file content parsing (frontmatter metadata, parent-child), code block ID protection (single + multiple fences), YAML frontmatter error surfacing | AST parser |
| `backend/vault` | Settings round-trip (atomic write + load), corrupt JSON error path | Settings durability |

### Benchmark

Run with: `go test -bench=. -count=3 ./backend/parser/`

**Phase 3 startup budget:** < 450ms for 1,000 daily-note files.

Baseline (Ryzen AI MAX+, Go 1.25, Windows): **~280ms** — within budget.

```
BenchmarkScanWorkspace_1000Files    1    ~252–334ms/op
```

## Manual Verification

Per Phase 6 of `PLAN.md`:

1. **`wails dev` onboarding flow**
   - Run `wails dev` from the project root.
   - Confirm the "Initialize Workspace Folder" button opens the native Wails folder selector.
   - Select a folder; confirm the vault scaffolds `Work/Journal/<today>.md`, `.system/config.yaml`, `.system/themes/cyber_forest.json`.
   - Confirm the UI transitions to "Vault Ready".
   - Close and reopen; confirm the vault auto-loads without re-showing the folder picker.

2. **Task state transitions**
   - With the vault loaded, use the browser console to invoke `window.go.main.App.UpdateBlockState("<block-id>", "DOING")` on a known block ID from the welcome note.
   - Verify the file on disk has the updated checkbox state.

3. **Watcher self-loop prevention**
   - Edit a `.md` file externally (e.g., in VS Code) while `wails dev` is running.
   - Confirm the change is indexed (DB query visible in logs) and no infinite write-loop occurs.

## Known Gaps (deferred to future sprints)

- No Wails integration test (requires `wails dev` runtime, see #32)
- No watcher e2e test against real fsnotify events
- No symlink-loop detection in `ScanWorkspace` (see #32 follow-up)
- No `ClearVault` / switch-workspace path (see #33)

---

# Sprint 3 — Smart Graph, Plugin SDK & OneNote-style Hierarchy

## Automated Tests

Run with: `go test -race -count=1 ./...` (Go) and `npm run check` (frontend, svelte-check).

### Go coverage added/updated this sprint

| Package | Tests | What is covered |
|---|---|---|
| `silt` (main) | 3-level model migration of all existing tests + `ResolveBlockReference` (found/dangling), `MutateBlock` (preserves UUID + task syntax, unknown id), `PluginRawQuery` (SELECT allowed, non-SELECT rejected), `PluginUpdateBlockState`, `GetPluginRegistry`, `ReadPluginSource` (+ traversal), `QueryBlocksByTag` (prefix semantics), `CreatePage` scaffolding, `CreateNotebook`/`OpenNotebook`/`PickNotebookFolder`/`CreateSection`, `FetchPageTimeline` | Wails API surface for the 3-level model + smart graph + plugin SDK |
| `backend/plugins` (new) | `Validate`/`Install` happy path, bad-archive rejections (missing manifest, bad id, missing main, zip-slip, absolute path), duplicate-install refusal, `Uninstall` (+ traversal rejection), `Enable`/`Disable` sentinel toggle, `sanitizeID` | `.silt-plugin` packaging/install lifecycle |
| `backend/db` | `QueryTagHierarchy` (prefix-count aggregation), `QueryBlocksByTag`, 3-level `FetchTimelineDays`/`IndexFileBlocks`/`ClearFileBlocks`/`ListNavigation`, `ExtractTags` now supports hyphenated tags | DatabaseManager + hierarchical tags |
| `backend/parser` | `BlockRefRegex`/`EmbedRegex` detectors, `page` dimension in `ParseFileContent` + scanner 3-level resolution + depth warn/skip | AST parser + scanner |
| `backend/monitor` | watcher 3-level `resolveFileMetadata` + reindex/focus-lock updated to the page model | DirectoryWatcher |
| `backend/vault` | blank-start scaffolding (no default Work/Journal), plugins README written | Settings durability |

Frontend: `npm run check` reports **0 errors** across the smart-graph components (RichText, BlockReferenceChip, EmbedPortal, BlockPickerModal, TagsExplorer, TagTreeNode), the plugin SDK (`frontend/src/plugins/*`), first-party Agenda/Calendar plugins, and the new titlebar/sidebar/App shell.

## Manual Verification Matrix (`wails dev`)

1. **Onboarding (blank start):** `wails dev` → Initialize Workspace → `.system/` only (no Work/Journal). Onboarding empty state prompts to create a notebook.
2. **3-level navigation:** Create a Notebook → Section → Page via the sidebar tree; the page timeline loads; breadcrumb shows Notebook › Section › Page.
3. **Sidebar collapse:** Collapse button (sidebar) hides the navigator; floating reopen button + Ctrl+B restore it; content reflows.
4. **Custom titlebar (#41):** frameless window; drag the empty header to move; min/max-restore/close work; double-click header toggles maximize.
5. **Smart Graph:** add `#work/sogav/milestone-one` to a block (renders as a pill, appears in Tags view); type `((uuid))` (renders as a link with hover preview, click scrolls to source); use `/embed` → picker → `{{embed:uuid}}` renders a live portal; edit the source block elsewhere and watch the embed update.
6. **Agenda (#17):** Agenda view shows overdue/today/tomorrow/upcoming; mark-done works; click jumps to source.
7. **Calendar (#18):** month + week grids with due-date tasks; prev/next/today navigation; click a task jumps to source.
8. **Plugin install:** Plugin Manager → install a sample `.silt-plugin` → it appears + loads; enable/disable + uninstall work.

## Sprint 3 Known Gaps

- Third-party plugins get full SDK access but a dedicated rendered-UI surface for arbitrary third-party components ships in a follow-up (Silt cannot compile Svelte at runtime); first-party plugins are the rendered-view references.
- Drag-to-reorder in the navigator/kanban deferred to a future sprint.
- Real-time theme-swap reactivity of the titlebar depends on the not-yet-built theme-injector pipeline (DESIGN.md §7); the titlebar binds to the existing CSS tokens so it inherits themes automatically once that lands.
