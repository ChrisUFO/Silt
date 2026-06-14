# Testing & Verification — Sprint 1 (Foundation)

> See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for the contribution workflow,
> pre-push hook setup (`git config core.hooksPath .githooks`), and the
> `npm run generate` Wails-binding regeneration step.

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
| `backend/vault` | Settings round-trip + new theme fields (active_theme/theme_mode), backward-compat (legacy settings.json → defaults), first-run defaults, theme-mode normalization, corrupt JSON error path | Settings durability & theme persistence |
| `backend/themes` | Validate (valid theme, missing token, bad color, missing identity, unparseable JSON), isValidColor forms, HexToRGB, ParseDefault (embedded default valid), Flatten (dark/light differ + pixel-identity), BGVoid (dark/light/system→dark), ListThemes (empty dir, missing dir, on-disk+malformed), ResolveActive (known/unknown/empty id → default) | Canonical schema, embed fallback, loader, validator |

### Benchmark

Run with: `go test -bench=. -count=3 ./backend/parser/` (cold scan) and
`go test -bench=. -count=3 ./backend/db/` (warm-restart diff).

**Phase 3 startup budget:** < 450ms for 1,000 daily-note files.

Baseline (Ryzen AI MAX+, Go 1.25, Windows): **~280ms** — within budget.

```
BenchmarkScanWorkspace_1000Files    1    ~252–334ms/op
```

**Hardening sprint (#29) warm-restart diff budget:** the on-disk WAL index
makes a warm restart skip unchanged files. The new `BenchmarkWarmStart_5000Files`
measures just the `IsFileUnchanged` diff loop (the new hot path) against a
5,000-row `files` table:

```
BenchmarkWarmStart_5000Files-32    ~48ms/op    ~3.6MB/op    ~120k allocs/op
```

That is the DB-diff portion of a warm restart of a 5k-page vault — well under
the budget, on top of which only the `os.Stat` cost of `ScanWorkspace` is
unavoidable (the markdown stays the source of truth).

## Hardening Sprint — Coverage Added (#29 #30 #31 #32 #33 #38 #39 #40)

| Package | New tests | What is covered |
|---|---|---|
| `silt` (main) | `CloseVault_TearsDownServices`, `CloseVault_Idempotent`, `CloseVault_ReopenUsesWarmRestart` | #33 reverse-order teardown, idempotency, close→reopen warm path |
| `backend/core` | `ReleaseFileMutex_EntryDeleted`, `..._NextAcquireGetsFreshEntry`, `..._NoDeadlockWithInFlightHolder`, `..._ConcurrentCallersSerialize` (all `-race`) | #30 generation-based io-mutex eviction |
| `backend/db` | `FilesTable_ColdStartPopulatesAndWarmStartSkips`, `PruneStaleFiles_DropsRenamedAndDeletedPaths`, `PruneStaleFiles_EmptyScanClearsAll`, `OnDiskWAL_CreatesWALFiles`, `OnDiskWAL_CheckpointOnCloseCollapsesWAL`, `OnDiskWAL_DeleteIndexForcesCleanRebuild`, `PluginRODB_ReadsOnDiskIndex`, `BenchmarkWarmStart_5000Files` | #29 persistent WAL index, files table, incremental diff, recovery, plugin RO visibility |
| `backend/db` | `Search_FTS5SmokeAndSync`, `..._RankingPutsMostRelevantFirst`, `..._SnippetContainsHighlightMarkers`, `..._MultiTermIsImplicitAND`, `..._PerPageGroupingCapsResultsPerPage`, `..._PaginationAndHasMore`, `..._EmptyQueryReturnsEmpty`, `..._TagHydrationSurvivesFTS`, `..._RebuildFTSIndexRepairs`, `..._UpdateReplacesOldFTSContent` | #39 FTS5 ranking/snippets/grouping/pagination/migration |
| `backend/monitor` | `FocusLease_AcquireThenLocked`, `..._ExpiryRecoversSuppression`, `..._RefreshKeepsItAlive`, `..._RefreshNoOpWhenExpired`, `..._ReleaseAllClearsEverything`, `..._ConcurrentAccessIsRaceClean` | #38 TTL focus leases + sweeper + shutdown release |
| `backend/parser` | `RenderFileContent_RoundTripIdentity` (task/note/header, nested, code-fence + body preservation), `RenderFileContent_DeletedBlockDropped`, `RenderFileContent_ScaffoldSnapshot`, `WalkMarkdown_SelfReferencingSymlinkDoesNotLoop`, `..._MutualSymlinkCycleIsSkipped`, `..._OneHopSymlinkIsSkippedWithWarning`, `ScanWorkspace_NoCrashOnSymlinkLoop` | #40 single-serializer round-trip; #32 symlink loop handling |

Frontend: `npm run check` reports **0 errors**. The TipTap editor (`TipTapEditor.svelte`) replaces the former per-block contenteditable with a single ProseMirror editor per page. `npm test` runs Vitest (20 tests: 3 TipTap smoke + 17 converter/schema/uniqueId round-trip identity). SearchModal.svelte renders sanitized FTS5 snippets (`<mark>` highlights), scroll-to-load-more pagination, and a result-count footer. Sidebar.svelte adds the "Change Vault" affordance (#33).

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

- No Wails integration test (requires `wails dev` runtime)
- No watcher e2e test against real fsnotify events
- ~~No symlink-loop detection in `ScanWorkspace` (see #32 follow-up)~~ — **Resolved in the hardening sprint**: `parser.WalkMarkdown` skips symlinks explicitly with a warning (#32)
- ~~No `ClearVault` / switch-workspace path (see #33)~~ — **Resolved in the hardening sprint**: `App.CloseVault` + the sidebar "Change Vault" affordance (#33)
- ~~Index rebuilt from scratch on every startup~~ — **Resolved**: persistent on-disk WAL index + incremental `files`-table diff (#29)

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

Frontend: `npm run check` reports **0 errors** across the smart-graph components (RichText, BlockReferenceChip, EmbedPortal, BlockPickerModal, TagsExplorer, TagTreeNode), the plugin SDK (`frontend/src/plugins/*`), first-party Agenda/Calendar plugins, the titlebar/sidebar/App shell, and the theme engine (`frontend/src/theme/*`).

## Manual Verification Matrix (`wails dev`)

1. **Onboarding (blank start):** `wails dev` → Initialize Workspace → `.system/` only (no Work/Journal). Onboarding empty state prompts to create a notebook.
2. **3-level navigation:** Create a Notebook → Section → Page via the sidebar tree; the page timeline loads; breadcrumb shows Notebook › Section › Page.
3. **Sidebar collapse:** Collapse button (sidebar) hides the navigator; floating reopen button + Ctrl+B restore it; content reflows.
4. **Custom titlebar (#41):** frameless window; drag the empty header to move; min/max-restore/close work; double-click header toggles maximize.
5. **Smart Graph:** add `#work/sogav/milestone-one` to a block (renders as a pill, appears in Tags view); type `((uuid))` (renders as a link with hover preview, click scrolls to source); use `/embed` → picker → `{{embed:uuid}}` renders a live portal; edit the source block elsewhere and watch the embed update.
6. **Agenda (#17):** Agenda view shows overdue/today/tomorrow/upcoming; mark-done works; click jumps to source.
7. **Calendar (#18):** month + week grids with due-date tasks; prev/next/today navigation; click a task jumps to source.
8. **Plugin install:** Plugin Manager → install a sample `.silt-plugin` → it appears + loads; enable/disable + uninstall work.
9. **Theme engine (Sprint 5):**
   - On launch the shell paints the embedded default theme with no pre-CSS flash (webview background = `bg.void`).
   - `GetActiveTheme` returns dark+light token maps; the injector overrides `index.css :root` same-tick (inspect `:root` → computed `--bg-void` is the theme's value, not the fallback).
   - `ApplyTheme(id, "light")` switches to light mode in one paint frame (no reload/remount); `document.documentElement` computed `--bg-void` changes to the light value.
   - `ThemeMode = "system"` follows the OS dark/light preference live (toggle OS theme → shell re-paints with no IPC round-trip).
   - Theme + mode persist across restart (settings.json).
   - Missing/empty themes dir or a deleted `cyber_forest.json` → the embedded default still loads.
   - A malformed `*.json` dropped in `.system/themes/` is rejected with a structured error (surfaced in `ListThemes.errors`) and never crashes the app.

## Sprint 3 Known Gaps

- Third-party plugins get full SDK access but a dedicated rendered-UI surface for arbitrary third-party components ships in a follow-up (Silt cannot compile Svelte at runtime); first-party plugins are the rendered-view references.
- Drag-to-reorder in the navigator/kanban deferred to a future sprint.
- ~~Real-time theme-swap reactivity of the titlebar depends on the not-yet-built theme-injector pipeline (DESIGN.md §7)~~ — **Resolved in Sprint 5**: the theme injector (frontend/src/theme) rewrites `:root` tokens same-tick, so every token-bound surface (including the titlebar) now re-themes live.

---

# Sprint 10 — Settings Menu & Plugin Manager

## Automated Tests

Run with: `go test -race -count=1 ./...` (Go) and `npm run check` (frontend, svelte-check).

### Go coverage added this sprint

| Package | Tests | What is covered |
|---|---|---|
| `backend/config` (new) | Defaults populated; Load happy-path overrides defaults; missing file → defaults (non-fatal); malformed YAML → error; Save round-trip; Save atomic (no leftover temp); normalize never nil | config.yaml parser + persistence |
| `backend/config` (watcher) | external write triggers reload; self-write ignored (multi-event window); malformed write → onError; Close idempotent; missing .system dir constructs without error; external atomic rename triggers reload | Hot-reload watcher + self-loop prevention |
| `silt` (main) | GetSystemConfig returns scaffolded defaults; SaveSystemConfig persists + applies (spacesPerTab) + round-trips through Get; SaveSystemConfig rejects invalid (tab_indent_spaces/font_size_px/line_height/auto_save_delay_ms); GetPluginRegistry now reads the in-memory config; GetAppVersion; ListPlugins manifest fields | Config + plugin-manager IPC surface |

### Frontend

`npm run check` reports **0 errors** across the new `settings/` store and components (`SettingsShell`, `GeneralTab`, `AppearanceTab`, `AboutTab`, `PluginsTab`), the updated `TitleBar`/`Sidebar`/`App` wiring, and the removed `PluginManagerModal`. New components use the canonical semantic accent tokens (`--accent-primary-*` / `--accent-secondary-*`, #43).

## Manual Verification Matrix (`wails dev`)

1. **Settings shell (#66/#35/#36):** TitleBar gear icon and Sidebar footer Settings button both open the overlay; tabs switch (General/Appearance/Plugins/About); Arrow keys move between tabs; Esc closes.
2. **General tab round-trip (#20):** edit tab width / font / a hotkey → Save → reopen the panel (and inspect `<vault>/.system/config.yaml`) → values persisted.
3. **Hot-reload:** with the panel closed, edit `.system/config.yaml` externally → reopen Settings → new values reflected without restart. With the panel open and unsaved edits, an external edit shows a non-blocking "reload" notice and preserves the draft.
4. **Plugin manager (#65):** Plugins tab lists Agenda + Calendar (Bundled) and any installed third-party plugins; Enable/Disable toggles reload via `plugins:changed`; Uninstall asks for confirmation and is hidden for bundled plugins; a broken plugin shows an inline error; "Install from .silt-plugin…" → validation preview → install works; first-party plugins have an "Open view" link.
5. **About:** shows the version from VERSION (0.1.0).

## Cross-Platform Build Verification

- **Linux:** `./build-linux.sh --no-bump` produces a working AppImage (`Silt-<v>-x86_64.AppImage`) and `.deb` (`silt_<v>_amd64.deb`) into `distributions/v<version>/`. The build auto-detects webkit2gtk-4.1 (Ubuntu 24.04+) and applies the `-tags webkit2_41` tag.
- **Windows:** `./build.sh --no-bump` produces the portable zip + NSIS installer (run on a Windows host).
- **CI:** merging a PR to `main` triggers `.github/workflows/release.yml`, which bumps VERSION, tags `v<version>`, builds both platforms from that one version, and publishes a GitHub Release with all artifacts.

## Known Gaps (deferred)

- The Appearance tab is a placeholder; wiring the theme picker + dark/light mode toggle into Settings is tracked in #47 (Sprint 6). The theme engine core landed in Sprint 5 (#43–#46), and Sprint 10 code uses its canonical semantic accent tokens.
- Per-plugin settings in the detail panel are read-only; an editing UI is future work.
- Community plugin marketplace/registry browsing is out of scope (separate future issue).

---

# #72 — Apply editor.* config + editor-internal hotkeys to the live editor

## Automated Tests

Run with: `npm run check` (frontend, svelte-check) and `go test -race -count=1 ./...` (Go, regression only — this is a frontend-only change).

### Frontend

`npm run check` reports **0 errors**. New module `settings/editor-tokens.svelte.ts` type-checks against the generated `config.EditorConfig` model. Updated components (`BlockRenderer.svelte`, `App.svelte`) pass with no new errors or warnings.

### Go

All existing Go tests pass unchanged (`go test -race -count=1 ./...`) — no backend changes in this PR.

## Manual Verification Matrix (`wails dev`)

1. **Editor typography (font_family / mono_font_family / font_size_px / line_height):** Open Settings → General → change font family to `Inter`, font size to `16`, line height to `1.8` → Save → editor text and shell body text immediately re-render at the new proportional font/size/line-height (no restart). `mono_font_family` drives `.font-label-sm` labels and badges. Inspect `document.documentElement` computed `--editor-font-size` → `16px`.
2. **Auto-save delay:** Change `auto_save_delay_ms` to `2000` → type in a block → verify the save fires ~2s after the last keystroke (check backend log / file mtime). Set to `0` → saves are debounced at a 50ms floor (prevents per-keystroke disk thrashing while remaining effectively immediate).
3. **Indent / unindent hotkeys:** Defaults (Tab / Shift+Tab) indent/outdent as before. Remap `indent_block` to `Ctrl+]` and `unindent_block` to `Ctrl+[` → Save → Tab no longer indents but Ctrl+] does.
4. **cycle_view_layout:** Press `Alt+Tab` (default) → the main view cycles notes → tags → agenda → calendar → kanban → notes.
5. **focus_highlight_ancestors:** Uncheck the checkbox → Save → focus a nested block → guide rails still render (showing indentation) but never light up with the active highlight gradient.
6. **Hot-reload:** With Settings closed, edit `.system/config.yaml` externally (change `font_size_px`) → editor re-renders at the new size without restart (the `$effect.root` in `initEditorTokens` re-injects CSS variables from the updated store).
7. **Disabled hotkeys:** Set `indent_block` to `""` (empty) in config.yaml → Tab falls through to the browser default (moves focus, does not indent).

---

# Sprint 6 — Theme Engine: UX & Extensibility (#47, #48, #73, #74, #76)

The theme engine shipped complete in Sprint 5 (settings persistence, loader, runtime injection, picker-as-`themes:changed` re-fetch) and is now extended with the user-facing surface in `Settings → Appearance`: live picker, mode toggle, custom theme import + export, plus a perf-pass that drops the previous `ApplyTheme` double-directory-scan (#76) and a launch-time cache that eliminates the first-paint flash for non-default themes (#73).

## Automated Tests

Run with: `go test -race -count=1 ./...` (Go) and `npm run check` + `npm test` (frontend, vitest).

### Go coverage added/updated this sprint

| Package | Tests | What is covered |
|---|---|---|
| `silt` (main) | `ImportTheme_IPCHappyPath` (file written + listing refreshes), `ImportTheme_IPCValidationFailure` (per-field ValidationErrors, no file written), `ImportTheme_IPCBeforeVault`, `ImportTheme_IPCNamespaceBuiltIn` (id renamed to `user-cyber_forest`), `ImportTheme_IPCRejectsDuplicate` (ErrImportDuplicate), `ImportTheme_IPCMissingSource`, `PickThemeFile_NoCtx`, `ExportActiveTheme_IPCRoundTrip` (exported file re-parses with the canonical validator), `ExportActiveTheme_IPCBeforeVault`, `ExportActiveTheme_IPCEmptyPath`, `ApplyTheme_ReadsListOnce` (#76 regression guard under -race) | Wails IPC surface for the import/export flow + the perf refactor |
| `backend/themes` | `importer_test.go` (24 tests: happy path, validation, sandbox rejection of non-color values, built-in namespacing, duplicate rejection, id sanitization with `_` preserved, atomic-write cleanliness, export round-trip, embedded default export, `LoadByID` semantics); `cache_test.go` (11 tests: embedded-default fallbacks, disk load, cache hit, invalid-file fallback, mtime reload, invalidate-one/all); `loader_test.go` regressions for the `flat_tokens` extension | Canonical validator reuse, atomic import, in-process theme cache |
| `silt` (main) | `launchBackgroundColour_TracksActiveCustom` (custom dark.bg.void propagates to the webview), `launchBackgroundColour_DefaultWhenNoSettings` (embedded default used pre-vault), `launchBackgroundColour_InvalidActiveIDFallsBack` (stale id → default rather than failing the launch) | main.go pre-CSS paint color |

### Frontend coverage added this sprint (#74)

`npm test` runs **17 vitest tests** across three files (jsdom environment, all stubs via `vi.mock` + `vi.hoisted` for the Wails-bound functions):

| File | Tests | What is covered |
|---|---|---|
| `frontend/src/theme/inject.test.ts` (7 tests) | single `<style id="silt-theme">` creation, element reuse on subsequent calls, CSS custom-property emission, empty/null/undefined skip, exactly-one textContent assignment per call (same-tick repaint contract), round-trip through `readToken`, no dangling `--empty:;` | Injector DOM-write contract |
| `frontend/src/theme/store.test.ts` (5 tests) | `initTheme` loads + injects, idempotency guard, `applyTheme` round-trip, `applyTheme` error path (returns false, surfaces error), subscription to the `theme:changed` event | Active-theme store |
| `frontend/src/theme/listing.test.ts` (5 tests) | `loadThemes` populates `themesState.items`, error surfacing, `initThemes` idempotency, subscription to `themes:changed` event, event handler re-fetches `ListThemes` | Listing store |

`npm run check` reports **0 errors** across the new theme code (inject, store, AppearanceTab, three test files) and the App.svelte wiring.

## Manual Verification Matrix (`wails dev`)

1. **Onboarding → first theme:** Initialize a fresh vault → Settings → Appearance → "Cyber Forest" row is the active row with a check icon; the swatches render the teal+indigo pair. Toggle Dark → Light → System. The same theme renders in each mode (System is OS-dependent).
2. **Mode change does not change the active theme:** Switch to Light; the row that was active is still active (only the segmented control's highlight changed).
3. **Keyboard navigation in the picker:** Tab into the tab; ArrowDown/ArrowUp moves focus between rows; Home/End jumps to first/last; Enter/Space selects the focused row; Esc cancels any live preview.
4. **Live preview on hover:** Hover a non-active theme row → the shell repaints in the same paint frame with the hovered theme's tokens; mouse leave restores the active theme. Esc also restores.
5. **focus ring:** Tab into the picker; the focused row has a `--accent-primary-start` focus ring visible.
6. **prefers-reduced-motion:** With `prefers-reduced-motion: reduce` set in the OS, no swatch / selection animations play. (The picker uses a single style block rewrite; the only "transition" is the same-tick repaint, which is unaffected.)
7. **Import — happy path:** Click "Import .json" → native file picker → select a valid theme JSON (e.g. the one in `app_themes_test.go`) → the new theme appears in the list immediately (no restart). Status region shows "Imported as <id>".
8. **Import — validation failure:** Import a JSON with `modes.dark.accent.primary.start: "not-a-color"`. The error message names the token (`modes.dark.accent.primary.start`) and the expected format (`#hex or rgb()/rgba()`). No file is written under `<vault>/.system/themes/`.
9. **Import — built-in id collision:** Import a JSON whose `id` is `cyber_forest`. The status shows "Imported as user-cyber_forest (renamed from cyber_forest)". The on-disk `cyber_forest.json` is untouched.
10. **Import — drag-drop:** Drag a `.json` file from the OS file manager and drop it onto the tab. The import fires through the same code path as the picker button.
11. **Import — duplicate:** Import a theme, then import the same JSON again. The second call rejects with "theme id X already exists". The first theme is not overwritten.
12. **Export — round-trip:** Click "Export active" → native save dialog (default filename = `<themeId>.json`) → save → open the file in a text editor → confirm the JSON parses with the canonical validator (Drop into the import picker to verify the round trip).
13. **Persistence:** Switch to a custom theme and dark mode → close the app → reopen → the same theme and mode are restored (Sprint 5 regression: `vault.SaveSettings` is the source of truth).
14. **First-paint cache (#73):** Apply a custom theme whose `bg.void` is a distinctive color (e.g. `#102030`) → restart the app → the window background color is `#102030` from the very first paint (no flash of the default's bg.void).
15. **ApplyTheme perf (#76):** With many on-disk themes (≥ 5), switching between them repeatedly under -race shows no concurrency hazard. (The single-scan refactor removes the second `os.ReadDir` that was inside `ResolveActive`.)

## Known Gaps (deferred to future sprints)

- A visual palette editor (in-app) for custom themes — covered by `Sprint 8 — First-Class Themes` (#42 follow-up).
- A user-facing authoring guide for custom themes — covered by the Sprint 7 docs work (#49).
- Theme marketplace / online sharing (out of scope per #48).
- Per-note theming (out of scope per #47).
