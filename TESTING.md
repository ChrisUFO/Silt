# Testing & Verification — Sprint 1 (Foundation)

> See [`CONTRIBUTING.md`](./CONTRIBUTING.md) for the contribution workflow,
> pre-push hook setup (`git config core.hooksPath .githooks`), and the
> auto-regenerating `npm install` (via the `prepare` script —
> `npm run generate` is now an explicit-refresh alias).

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
   - Edit a `.md` file externally (e.g., in an external editor) while `wails dev` is running.
   - Confirm the change is indexed (DB query visible in logs) and no infinite write-loop occurs.

## Known Gaps (deferred to future sprints)

- No Wails integration test (requires `wails dev` runtime)
- No watcher e2e test against real fsnotify events
- ~~No symlink-loop detection in `ScanWorkspace` (see #32 follow-up)~~ — **Resolved in the hardening sprint**: `parser.WalkMarkdown` skips symlinks explicitly with a warning (#32)
- ~~No `ClearVault` / switch-workspace path (see #33)~~ — **Resolved in the hardening sprint**: `App.CloseVault` + the sidebar "Change Vault" affordance (#33)
- ~~Index rebuilt from scratch on every startup~~ — **Resolved**: persistent on-disk WAL index + incremental `files`-table diff (#29)

---

# Sprint 3 — Smart Graph, Plugin SDK & 3-Level Hierarchy

## Automated Tests

Run with: `go test -race -count=1 ./...` (Go) and `npm run check` (frontend, svelte-check).

### Go coverage added/updated this sprint

| Package | Tests | What is covered |
|---|---|---|
| `silt` (main) | 3-level model migration of all existing tests + `ResolveBlockReference` (found/dangling), `MutateBlock` (preserves UUID + task syntax, unknown id), `PluginRawQuery` (SELECT allowed, non-SELECT rejected), `PluginUpdateBlockState`, `GetPluginRegistry`, `ReadPluginSource` (+ traversal), `QueryBlocksByTag` (prefix semantics), `CreatePage` scaffolding, `CreateNotebook`/`OpenNotebook`/`PickNotebookFolder`/`CreateSection`, `FetchPageBlocks` | Wails API surface for the 3-level model + smart graph + plugin SDK |
| `backend/plugins` (new) | `Validate`/`Install` happy path, bad-archive rejections (missing manifest, bad id, missing main, zip-slip, absolute path), duplicate-install refusal, `Uninstall` (+ traversal rejection), `Enable`/`Disable` sentinel toggle, `sanitizeID` | `.silt-plugin` packaging/install lifecycle |
| `backend/db` | `QueryTagHierarchy` (prefix-count aggregation), `QueryBlocksByTag`, 3-level `IndexFileBlocks`/`ClearFileBlocks`/`ListNavigation`, `ExtractTags` now supports hyphenated tags | DatabaseManager + hierarchical tags |
| `backend/parser` | `BlockRefRegex`/`EmbedRegex` detectors, `page` dimension in `ParseFileContent` + scanner 3-level resolution + depth warn/skip | AST parser + scanner |
| `backend/monitor` | watcher 3-level `resolveFileMetadata` + reindex/focus-lock updated to the page model | DirectoryWatcher |
| `backend/vault` | blank-start scaffolding (no default Work/Journal), plugins README written | Settings durability |

Frontend: `npm run check` reports **0 errors** across the smart-graph components (RichText, BlockReferenceChip, EmbedPortal, BlockPickerModal, TagsExplorer, TagTreeNode), the plugin SDK (`frontend/src/plugins/*`), first-party Agenda/Calendar plugins, the titlebar/sidebar/App shell, and the theme engine (`frontend/src/theme/*`).

## Manual Verification Matrix (`wails dev`)

1. **Onboarding (blank start):** `wails dev` → Initialize Workspace → `.system/` only (no Work/Journal). Onboarding empty state prompts to create a notebook.
2. **3-level navigation:** Create a Notebook → Section → Page via the sidebar tree; the page editor loads; breadcrumb shows Notebook › Section › Page.
3. **Sidebar collapse:** Collapse button (sidebar) hides the navigator; floating reopen button + Ctrl+B restore it; content reflows.
4. **Custom titlebar (#41):** frameless window; drag the empty header to move; min/max-restore/close work; double-click header toggles maximize.
5. **Smart Graph:** add `#work/project/milestone-one` to a block (renders as a pill, appears in Tags view); type `((uuid))` (renders as a link with hover preview, click scrolls to source); use `/embed` → picker → `{{embed:uuid}}` renders a live portal; edit the source block elsewhere and watch the embed update.
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

- ~~Third-party plugins get full SDK access but a dedicated rendered-UI surface for arbitrary third-party components ships in a follow-up~~ — **Resolved in the v2 SDK (PR #148, #117)**: third-party plugins render UI through a sandboxed `<iframe srcdoc>` + postMessage bridge (`frontend/src/plugins/surfaces.ts`). Sidebar-panel, modal, status-bar, and settings surfaces are all supported.
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

- ~~The Appearance tab is a placeholder; wiring the theme picker + dark/light mode toggle into Settings is tracked in #47 (Sprint 6).~~ — **Resolved in Sprint 6**: Settings → Appearance is a fully accessible theme picker + mode toggle + import/export.
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
- ~~A user-facing authoring guide for custom themes~~ — **Resolved in Sprint 7** (#49): `docs/THEMING.md` is the authoritative authoring/import reference.
- Theme marketplace / online sharing (out of scope per #48).
- Per-note theming (out of scope per #47).

---

# Sprint 7 — Theme System: Docs & Tests (#49, #50)

The engine shipped complete in Sprints 5–6; Sprint 7 consolidates the
documentation (contributor architecture docs + the end-user authoring
guide, #49) and hardens the test coverage into one auditable plan (#50),
including a WCAG contrast harness that caught and fixed a real a11y bug
in the shipped default.

## Automated Tests

Run with: `go test -race -count=1 ./...` (Go) and `npm run check` +
`npm test` (frontend, vitest).

### Go coverage added this sprint

| Package | Tests | What is covered |
|---|---|---|
| `backend/themes` (`contrast.go` + `contrast_test.go`) | `TestContrastRatio_ReferencePairs` (black/white = 21:1; WCAG sample), `TestContrastRatio_AcceptedColorForms` (#hex / rgb / rgba / percent / alpha-dropped / rejected forms), `TestWCAG_DefaultTheme_PrimaryTextAAA` (≥7:1 both modes), `TestWCAG_DefaultTheme_AccentsNonTextAA` (≥3:1), `TestWCAG_DefaultTheme_MutedTextAA` (≥4.5:1), `TestWCAG_DefaultTheme_ReportsAllRatios` (logs every pair) | Reusable WCAG 2.x contrast harness + assertions for the shipped default |
| `backend/themes` (`snapshot_test.go`) | `TestDefaultTheme_GoldenSnapshot` | Pins the ENTIRE dark+light flattened token map of the embedded `cyber_forest.json`; any token drift fails with a precise diff |
| `backend/themes` (`themes_test.go`) | `TestValidate_SchemaVersionForwardCompat` (higher version still validates — informational), `TestValidate_UnknownSchemaVersionStillRequiresField`, `TestValidate_MissingLightMode` (dark-only theme flags every required light token) | schema_version handling + missing-modes edge case (#50) |
| `silt` (main) | `TestMigrationInvariant_NoOldHueTokens` | CI-grade guard: walks tracked text files via `git ls-files`, fails loudly if any old hue-named token (`color-teal-*` / `--accent-teal-*` / `--accent-indigo-*`) reappears. Runs in the existing `go test` step + pre-push hook |

### Frontend coverage added this sprint

| File | Tests | What is covered |
|---|---|---|
| `frontend/src/theme/inject.test.ts` (+1) | `applying a new theme changes --bg-void WITHOUT remounting the style element` | The #50 no-remount contract: two injects → one reused `<style id="silt-theme">` node + `readToken('--bg-void')` reflects the latest value |
| `frontend/src/components/settings/AppearanceTab.test.ts` (new, 7) | listbox/`aria-selected`, radiogroup/`aria-checked`, ArrowDown/Home/End roving tabindex, Enter/Space commit, mode-radio click | The #50 picker keyboard-navigable + correct-ARIA contract |

`npm test` now runs **25 vitest tests** across 4 files (was 17 across 3). `npm run check` reports **0 errors**.

### Pre-existing theme coverage (Sprint 5/6, re-verified intact)

| Package | Tests | What is covered |
|---|---|---|
| `backend/themes` (`themes_test.go`) | Validate (valid/missing token/bad color/missing identity/unparseable), `isValidColor` forms, `HexToRGB`, `ParseDefault`, `Flatten` (dark/light differ + pixel-identity), `BGVoid`, `ListThemes` (empty/missing/on-disk+malformed), `ResolveActive` (known/unknown/empty), Typography (optional/valid/rejects-CSS-injection/partial/flatten-emit) | Canonical schema, embed fallback, loader, validator |
| `backend/themes` (`importer_test.go`) | Import happy path / validation / sandbox rejection / built-in namespacing / duplicate rejection / id sanitization / atomic-write cleanliness / export round-trip / `LoadByID` / `ExistingOnDiskIDs` / `namespaceThemeID` | Import + export + namespace logic |
| `backend/themes` (`cache_test.go`) | embedded-default fallbacks, disk load, cache hit (pointer identity), invalid-file fallback, mtime reload, invalidate-one/all | In-process launch cache |
| `silt` (main) (`app_themes_test.go`) | `GetActiveTheme` (default/round-trip/pre-vault), `ListThemes` (scaffolded/malformed), `ApplyTheme` (switch/persist/invalid-mode/unknown-id/system/bad-mode-not-persisted), `ImportTheme` IPC (happy/validation/before-vault/namespace/duplicate/missing), `ExportActiveTheme` IPC (round-trip/before-vault/empty), `effectiveMode`, `buildThemeResult`, `ApplyTheme_ReadsListOnce` (#76) | Wails IPC surface |
| `silt` (main) (`main_themes_test.go`) | `launchBackgroundColour` (tracks active custom / default when no settings / invalid-id fallback) (#73) | Pre-CSS paint color |
| `backend/vault` | Settings round-trip (`ActiveTheme`/`ThemeMode`), legacy backward-compat, first-run defaults, `ThemeMode` normalization, corrupt-JSON error path | Settings durability + theme persistence |
| `frontend/src/theme/{store,listing,inject}.test.ts` | store init/apply/error/event, listing load/error/idempotency/`themes:changed` re-fetch, inject single-style/emission/skip/same-tick contract | Frontend data pipeline |

## WCAG Contrast — finding & fix

The contrast harness (`backend/themes/contrast.go`) surfaced a real
accessibility bug in the shipped default: `text.muted` rendered below
the **AA 4.5:1** target that `DESIGN.md §8` already documents ("above
4.5:1 for secondary tags"):

| Token | Before | After | Min ratio across all 5 backgrounds (both modes) |
|---|---|---|---|
| `modes.dark.text.muted` | `#71717a` (3.74–4.04:1) | `#8b8b94` | 4.69:1 (`bg.active`, the lightest dark bg) |
| `modes.light.text.muted` | `#64748b` (4.03:1 on `bg.active`) | `#4d5667` | 4.98:1 (`bg.active`) |

The assertion matrix covers **all five** backgrounds (`void`/`surface`/
`panel`/`hover`/`active`) for both primary (≥7:1 AAA) and muted (≥4.5:1
AA) text — the earlier 3-background version passed while light-muted
actually failed on `bg.active`; the full matrix closes that gap.
Primary text (13–18:1, AAA) and accents (≥3:1, AA non-text) already
passed and are unchanged. The doc examples (`SPECS.md` §6.4, `DESIGN.md`
§2.1, `docs/THEMING.md`) were updated to match. **WCAG matrix
extensibility:** the harness asserts the shipped Default (cyber_forest)
now; Sprint 8's Terra Noir / Linen (#42) plug into the same table when
they land — no harness change required.

## Manual Verification Matrix (`wails dev`) — theme deltas

The Sprint 5 (§"Theme engine (Sprint 5)") and Sprint 6 (§"Sprint 6 —
Theme Engine: UX & Extensibility") manual matrices remain authoritative
for the engine + picker UX. Sprint 7 adds:

1. **Authoring round-trip:** follow `docs/THEMING.md` §9 (blank
   template) → fill tokens → import → export → re-import; the file
   parses with the canonical validator at each step.
2. **Muted-text contrast (post-fix):** in dark mode, sidebar metadata
   and note tag labels (muted text) are visibly legible against panels;
   a    contrast-tool spot-check on any muted label reports ≥ 4.5:1.
3. **Default-theme snapshot stability:** the shipped default looks
   unchanged except the muted gray is one notch lighter (dark) / darker
   (light); no other token moved.

# Sprint 8 — First-Class Themes (#42, #51, #82, #90)

Sprint 8 ships four new first-class theme palettes (Terra Noir, Linen, Stark,
Graphite), extends the embed from a single default to the full first-class set
so every shipped palette is always selectable, and adds the preloaded font
picker that makes theme typography actually render. The theme engine itself is
unchanged (IPC contract, on-disk-wins dedup, `:root` injection runtime) —
Sprint 8 is content + one bounded engine extension + one frontend feature.

## Go coverage added this sprint

| File | Tests | What is covered |
|---|---|---|
| `backend/themes/embed_test.go` (new) | `EmbeddedThemes_RosterAndValid` (5 ids + typography + both modes), `ParseEmbeddedByID` (each first-class id resolves; unknown/empty → false), `EmbeddedThemeFiles_UsedByScaffold` (one `<id>.json` per theme), `ListThemes_OnDiskDefaultWinsDedup` (on-disk cyber_forest source=disk + FlatTokens reflect disk), `ResolveActive_FirstClassEmbeddedOffDisk` (non-default id resolves from embed on empty dir; unknown → default), `CachedThemeByID_FirstClassEmbeddedOffDisk` (off-disk + pre-vault resolve; unknown → default), `EmbeddedThemes_DeterministicOrder` | The multi-theme embed extension: first-class set is always selectable + resolvable even on a wiped/existing vault |
| `backend/themes/contrast_test.go` (+3) | `WCAG_FirstClassThemes_AllMeetsTargets` (primary ≥7:1, muted ≥4.5:1, accents ≥3:1 across all 5 backgrounds × both modes for the 4 new themes), `WCAG_Stark_FocusStatesUnmistakable` (border.focus ≥3:1 on all backgrounds, both modes — WCAG 2.4.11/1.4.11), `AccentDistinctness_AllFirstClassThemes` (primary vs secondary sRGB distance ≥30 so go/done and in-progress never blur) | WCAG matrix + Stark focus contract + accent distinctness for the first-class set |
| `backend/themes/snapshot_test.go` (+1) | `FirstClassThemes_FlattenShape` (both modes flatten to exactly the 23-key canonical set incl. typography; the WCAG-tuned tokens pinned: terra-noir dark muted `#a89478`, linen dark muted `#afb3bb`) | Structural drift guard for the new themes (the default retains its full value-level golden snapshot) |
| `backend/themes/themes_test.go` (updated) | `ListThemes_EmptyDir`/`_MissingDir`/`_EmptyPath` (now assert the full 5-theme embedded set with correct source labels), `ListThemes_OnDiskPlusMalformed` (custom + 5 embedded) | Updated for the multi-theme listing |
| `backend/vault/vault_test.go` (+2) | `ScaffoldVault_WritesAllFirstClassThemes` (every embedded theme written), `ScaffoldVault_ThemesIdempotent` (a hand-edited sentinel survives a re-scaffold) | Scaffold writes the full first-class set without clobbering user edits |
| `silt` (main, `app_themes_test.go` updated) | `GetActiveTheme_BeforeVaultOpen` (all 5 first-class ids present pre-vault), `ImportTheme_IPCValidationFailure` (rejected import adds nothing beyond the scaffolded set) | App-level listing/import accounting for the 5-theme set |

WCAG tuning applied this sprint (same lesson as the default's light-muted):
`modes.dark.text.muted` raised on the two themes whose lighter dark surfaces
broke AA — Terra Noir `#8a7860 → #a89478`, Linen `#82868d → #afb3bb`. The
default's full golden snapshot, the migration-invariant guard, and all
Sprint 5/6/7 theme tests remain green and unchanged.

## Frontend coverage added this sprint

| File | Tests | What is covered |
|---|---|---|
| `frontend/src/theme/fonts.test.ts` (new, 6) | curated sans/mono/display set present, the three defaults are bundled, unique ids + non-empty cssFamily, `bundledByCategory`/`systemFonts` selectors, `displayNameForCssFamily` unknown-family fallback | The font-picker registry (single source of truth) |
| `frontend/src/components/settings/AppearanceTab.test.ts` (+2 → 10) | theme-typography indicator renders when the active theme overrides fonts; hidden when it doesn't | The #82 override indicator |

`npm test` now runs **34 vitest tests** across 5 files (was 26 across 4).
`npm run check` reports **0 errors**. `npm run build` bundles the curated
`@fontsource` families as self-hosted woff2 assets (offline; no runtime CDN).

## Manual Verification Matrix (`wails dev`) — Sprint 8 deltas

1. **First-class roster:** Settings → Appearance lists **five** themes
   (Cyber Forest, Graphite, Linen, Stark, Terra Noir — alphabetical by name)
   on a freshly scaffolded vault, each with its swatch pair.
2. **Whole-shell restyle, no remounts:** cycle Default → Terra Noir → Linen
   → Stark → Graphite (click or Arrow + Enter). Each repaints the titlebar,
   check/guide-rail components, and focus rings in one paint frame — no
   reload, no component remount, no console errors.
3. **Dark/light in each theme:** toggle Dark → Light → System in every
   theme; the canvas, text, and accents switch coherently. Graphite is
   visibly calmer/lower-chroma than Cyber Forest; Stark is unmistakably
   high-contrast with vivid gold focus rings.
4. **Stark focus clarity:** tab through the sidebar/picker; focus rings are
   unmistakable in both dark (gold on black) and light (blue on white).
5. **Wiped-dir resilience:** delete `<vault>/.system/themes/*.json`; the
   picker still lists all five (embedded fallback) and an active non-default
   theme first-paints in its own `bg.void`, not the default's.
6. **Font picker (Settings → General):** Font family + Monospace are
   `<select>` dropdowns grouped Sans-serif / Display / Monospace / System;
   each option renders in its own font. Selecting a font updates the editor
   live (Save persists via the existing path). Bundled fonts render offline
   (disable network, relaunch — Plus Jakarta Sans / JetBrains Mono still
   apply, proving the @fontsource self-hosting).
7. **Reset to theme default:** with Cyber Forest active (it defines
   typography), each font field shows a "Reset to theme default" button;
   clicking it clears the field and the dropdown shows "Theme default
   (…)". Switch to a theme without a typography section (none shipped
   currently — verify via a custom import) and the reset button is hidden.
 8. **Theme typography indicator (Settings → Appearance):** with an active
    theme that defines fonts, a "Theme typography" section lists the
    overridden Body/Mono/Headline families.

---

# Sprint 9 — Page Templates (#53–#58)

## Automated Tests

Run with: `go test -race -count=1 ./...` (Go) and `npm run check` + `npm test`
(frontend, vitest).

### Go coverage added this sprint

| Package | Tests | What is covered |
|---|---|---|
| `backend/templates` (new) | Loader (empty/builtin-only/user-only/mixed dedup/missing/malformed/sort), Validator (dup id/empty body/bad placeholder/bad schema_version/builtin collision), Renderer (defaults/unknown→warn/user vars/missing required/empty vars/smart-graph passthrough/timezone), Watcher (add/modify/delete → list + callback), Cache (mtime/TTL/invalidate), Store (save/delete/builtin guard/round-trip), Snapshot (every built-in rendered output pinned with frozen time), Roster (exactly 10 ids + round-trip ParseFileContent + action-items-as-tasks) | The full template engine + spec-compat regression guards |
| `silt` (main, `app_templates_test.go`) | ListTemplates (10 built-ins pre-vault), GetTemplate (happy/not-found), SaveUserTemplate/DeleteUserTemplate round-trip + overwrite, RenderTemplate with vars, RenderTemplateBlocks, CreatePageFromTemplate (writes + indexes), builtin:// write rejected + disk unchanged | Wails IPC surface |

### Frontend

| File | Tests | What is covered |
|---|---|---|
| `frontend/src/templates/store.test.ts` | loadTemplates populates items, error surfacing, initTemplates idempotency, templates:changed re-list | Listing store |
| `frontend/src/templates/TemplatePicker.test.ts` | dialog renders with options, insert vs. new-page mode labels, search filters, placeholder form renders on focus, empty state | Picker component (mock IPC) |

## Manual Verification Matrix (`wails dev`)

1. **Ctrl+Shift+T** opens the template picker; all 10 built-ins are listed, grouped by category.
2. Select **Daily Note** — the preview shows today's date and weekday.
3. Enter a page name → **Create Page** → the new page opens with the rendered template body.
4. Type `/template` in the editor → select **Meeting Notes** → fill `meeting_title` → **Insert** → the blocks appear at the cursor.
5. Verify action items (TODO TASK lines) appear in the Kanban view.
6. Drop a custom `.md` into `<vault>/.system/templates/` → it appears in the picker without a restart (watcher hot-reload).
7. Smart-graph passthrough: author a template body containing `{{embed:abc-123}}` → insert → the embed token survives rendering intact.


---
# Sprint 4 — Kanban Board, Performance, Tests & Polish (#19, #21, #22, #23)

## Automated Tests

Run with: `go test -race -count=1 ./...` (Go) and `npm run check` + `npm test` (frontend, Vitest).

### Go coverage added this sprint

| Package | Tests | What is covered |
|---|---|---|
| `backend/parser` | `TestParseLine_EdgeCases` (minimal task, DOING/DONE states, partial metadata, priority-without-owner), `TestScanWorkspace_BudgetRegression` (hard <450ms/1k files gate), `TestWriteFileAtomic_NoTruncatedFilesOnKill` (100 concurrent writes, zero truncated, zero stray temp) | AST edge cases + boot-scanner budget regression + atomic-write durability |
| `backend/db` | `TestAtomicWrite_KillMidWriteRecoversViaWAL` (destructive-exit WAL replay + zero stray temp files), `TestIndexer_KanbanQueryPath` (exact Kanban SQL ordering + section scoping) | WAL crash recovery + Kanban query regression guard |

### Frontend coverage added this sprint

| File | Tests | What is covered |
|---|---|---|
| `frontend/src/plugins/first-party/silt-kanban/Kanban.test.ts` (36 tests) + `query.test.ts` (15 tests) + `KanbanSidebar.test.ts` (17 tests) + `kanbanSharedState.test.ts` (12 tests) | Kanban.test: 3-lane render, task bucketing, default page-scope, scope-change re-query, click → detail panel, "Open in editor" → navigate-to-block, ArrowRight/Enter/Space keyboard, error revert, empty state, scope-button enable/disable, truncation banner, race guard, owner/priority filter SQL clauses, Add/Remove column persistence, pin-pending disable, board reload after meta toggle, custom-column drop rejection, scope radiogroup arrow-key nav, shared-state reactivity (#323); query.test: all 4 scope branches + all 4 filter branches (owners, priorities, dueDate overdue/today/week/none, tags subquery), ORDER BY invariant; KanbanSidebar.test: SAVED BOARDS render + apply, + Save current… capture, scope radio click + keyboard nav, Enter activates via event target (not cursor), Enter on disabled scope does NOT activate, priority/due filter toggles, bidirectional sync, Clear all, empty state, malformed-entry validation, delete-board confirmation dialog (cancel + proceed), `applySavedBoard` sets `aria-pressed="true"`; kanbanSharedState.test: direct unit tests pinning setScope, narrowScopeTo, clearScopeOverride, setFilters, clearFilters, applySavedBoard, initFromConfig | Kanban plugin IPC boundary + the extracted pure SQL builder + the primary sidebar's saved-boards UX + shared-state semantics + #325 hardening pass (keyboard-correct radios, delete confirm, dead-write removal) |
| `frontend/src/plugins/first-party/silt-agenda/Agenda.test.ts` (4 tests) + `AgendaList.test.ts` (3 tests) | Date-bucket loading, mark-done → ctx.updateBlockState, click → navigate-to-block, empty state, markDoneError banner render + dismiss button, success clears the banner | Agenda plugin IPC boundary (plugin remains registered for third-party compat) + the extracted AgendaList subcomponent (#322) used inside Calendar.svelte's agenda mode |
| `frontend/src/plugins/first-party/silt-calendar/Calendar.test.ts` (10 tests) + `CalendarSidebar.test.ts` (13 tests) + `focusState.test.ts` (8 tests) | Calendar: month-grid rendering, Today button, click → navigate-to-block, 3-button mode toggle (Month / Week / Agenda), agenda-mode group render, navigate-to-block preserved, in-view Clear-filter banner, view_mode failure UI (positive path), dismisses the modeError banner via the X button; CalendarSidebar: smart-list rendering, live counts from SQL aggregate, click → set filter, mini-calendar dots, click → focusDate + dispatch event, arrow-key nav, Enter activates, clear-filter visibility, mini-cal "Today" chip clears focusDate, refresh-navigation resets focusDate + activeFilter (vault switch reset), Today smart-list count excludes overdue tasks, unmount releases refresh-navigation listener + calendar:clear-filter listener + nowInterval + block:changed subscription (regression coverage for #325 cleanup leak); focusState.test: direct unit tests pinning setActiveFilter, clearActiveFilter, setFocusDate, clearFocusDate, `resetFocusState` production export, window-event dispatch | Calendar plugin IPC boundary + the unified Calendar/Agenda view (#322) + the smart-list sidebar + shared-state semantics + #325 hardening pass (vault switch reset, Today ≠ Overdue, agenda-mode read-only sidebar) + #325 regression pass (cleanup-on-unmount) |
| `frontend/src/components/PluginView.test.ts` (3 tests) | Happy-path render, load-error path, not-registered empty state | Plugin host view |
| `frontend/src/components/Sidebar.test.ts` (9 tests) | Collapse render, no-accent notebook label (#138), MovePage mock + collision rejection, onPageMoved callback, tags-view TagSidebarPanel regression, plugin-sidebarComponent fallback (#321), notes-view page-tree fallback, registered sidebarComponent render + ctx+sessionToken wiring | Sidebar interactions + the plugin-provided sidebar routing layer (#321) |

`npm test` now runs **891 vitest tests** across 86 files (was 887 across 86 before the senior-review round-3 follow-ups; +4 for the Upcoming-filter parity test, the AgendaList midnight re-bucket test, the CalendarSidebar mount-reload-count test, and the round-trip stability test). `npm run check` reports **0 errors** introduced by this round (12 pre-existing Wails-binding + TabRef + DistinctOwners + math_enabled errors unchanged from `main`).

### Dead-code cleanup

- Removed the stale page-timeline surface (`FetchPageTimeline`, `FetchTimelineDays`, `DayGroup`, their tests, and the `maxTimelineLimit`/`defaultTimelineLimit` constants). The live editor uses `FetchPageBlocks`; the timeline API was dead code left over from the per-day file model removal.

## Manual Verification Matrix (`wails dev`)

1. **Kanban board (#19):** Navigate to a section with mixed TODO/DOING/DONE tasks → switch to the Kanban view → 3 columns render with correct counts.
2. **Kanban scope selector:** Switch between Vault / Notebook / Section / Page → the board re-queries and the card set narrows/widens; the breadcrumb shows the active scope. Default scope follows the active navigation (navigate to a page → board defaults to page scope).
3. **Kanban drag-and-drop:** Drag a card TODO → DOING → file on disk now reads `[/] description [key:: value]...`. Drag DOING → DONE → file reads `[x] description [key:: value]...`. Reload → persisted state reflects the markdown.
4. **Kanban keyboard:** Focus a card → ArrowRight moves it to the next lane; ArrowLeft moves back. Enter/Space opens the card detail panel.
5. **Kanban filter bar:** Click Owner chip → multi-select owners → board narrows. Stack Owner + Priority → both filter clauses active. "Clear all" restores the full set. Filters persist across reload (stored in config.yaml plugin_settings.silt-kanban.filters).
6. **Kanban custom columns:** Click "+ Add Column" → type name → new column appears and persists across reload. Click more_horiz → Rename → inline edit → persists. Click more_horiz → Remove → confirm → column drops (cards keep their status). Drag column headers to reorder → persists.
7. **Kanban card detail panel:** Click a card → right-side panel slides in. Toggle pin → file updates with `[pin:: true]`. Adjust progress slider → file updates with `[progress:: N]`. Comments/links counts display correctly. "Open in editor" jumps to the source block. Esc closes.
8. **Kanban card visuals:** Pinned cards show push_pin icon. Cards with progress > 0 show a progress bar. Comment/link counts appear at the bottom. DOING cards have a left-edge indigo border.
9. **Task metadata autocomplete (%):** In the editor, type a task line (`- [ ] some task`). Type `%` → popup shows all 6 metadata keys. Type `%d` → filters to "due". Select "due" → `[due:: ]` inserted with cursor positioned. Type a date → `[due:: 2026-08-03]` stored in file. Select "pin" → `[pin:: true]` auto-filled.
10. **Plugin disable:** Settings → Plugins → toggle off Kanban → the Kanban view shows the "not registered" empty state. Toggle back on → it reappears. Works for both first-party and third-party plugins.
11. **Frame-budget probe (#21):** Open `wails dev` with `?perf=1` appended to the URL. Perform Kanban drag-drop, editor typing, and theme switching → console logs each measurement with ✓ (<16ms) or ⚠️ (>16ms).
12. **Production build (#23):** `./build.sh --no-bump` (Windows) or `./build-linux.sh --no-bump` (Linux) produces the platform artifacts. Launch the binary, open a vault with ≥10 pages, idle 60s → peak RSS < 65MB (Task Manager on Windows, `ps -o rss=` on Linux).

## Known Gaps (deferred)

- **System tray (#23):** Wails v2.12 has an internal `TrayMenu` struct but no public runtime API to register tray menus. The tray icon + minimize-to-tray feature is blocked by this API gap; deferred to Wails v3 adoption.
- **Sidebar tree-render test:** The Sidebar's `loadNavigation` runs in `onMount`, which does not fire reliably under Svelte 5 + testing-library/jsdom (unlike `$effect`, which Kanban/Agenda/Calendar use). Tree rendering is covered by manual verification; the Sidebar test covers collapse + Change Vault.


# Sprint Follow-Ups — Issues #61, #62, #63, #64, #68, #69, #75, #79, #83

## Sprint: Template follow-ups + dep pull-ins (#85, #86, #88, #89, #93–#98)

Closes the six template follow-up issues (PRs #99, #121) plus the four open dependencies that gated them (#85 Smart Graph NodeViews, #86 save-failure toast, #88 ListNavigation deep-nesting, #89 sanitizePathSegment). All work in one branch (`feat/template-followups-93-98`) so the tests run as real assertions — no `t.Skip` workarounds.

### Automated Tests

Run with: `go test -race -count=1 ./...` (Go) and `npm run check` + `npm test` (frontend, vitest).

### Go coverage added

| File | Tests | What is covered |
|---|---|---|
| `app.go` (#89) | `TestSanitizePathSegment` (table updated) | `2.0..2.1` and `a..b..c` preserved; `..foo` / `....foo` / `../etc/passwd` still stripped (boundary-only `..` strip) |
| `app.go` (Phase 7) | `sanitizeSectionPath` helper + `CreatePageFromTemplate` integration | Multi-segment section paths (`Projects/Active`) survive the sanitize pass — closes the latent bug where `Projects/Active` was being mangled to `ProjectsActive` by the single-segment sanitizer |
| `app_nav_test.go` (#88) | `TestListNavigation_DeepNesting` | Multi-level section tree (Work > Projects > Active > Site.md) appears in the navigation at the correct depth |
| `app_templates_test.go` (#93, #97, #98, #96) | `TestCreatePageFromTemplate_EmbedAndRefPreservedInFile`, `TestCreatePageFromTemplate_DeepSection_AppearsInNavigation`, `TestCreatePageFromTemplate_SanitizesEdgeCaseNames` (6 sub-cases), `TestRegisterPluginTemplates_IPC` | Smart-graph passthrough through the file-write IPC; deep-section page visibility; sanitization edge cases (internal `..` preserved, leading `..` stripped, exact `..` rejected); plugin-template registry round-trip |
| `backend/templates/loader_test.go` (#96) | `TestRegisterPluginTemplates_HappyPath`, `..._RejectsEmptyPluginID`, `..._RejectsNilSlice`, `..._RejectsMismatchedSource`, `..._RejectsMismatchedPluginID`, `TestUnregisterPluginTemplates_Idempotent`, `TestGetPluginTemplate_ResolvesURI`, `..._NotFound`, `..._InvalidURI` (3 sub-cases), `TestListTemplates_IncludesPluginTemplates`, `TestGetTemplate_PluginURI`, `TestRejectPluginIDInFrontmatter` | Plugin-template registry round-trip: register / unregister / list / get / invalid URI / dedup with on-disk / on-disk frontmatter `plugin_id:` rejection |

### Frontend coverage added

| File | Tests | What is covered |
|---|---|---|
| `frontend/src/notifications/store.test.ts` (new, #86) | 9 tests | `pushNotification` / `dismissNotification` / `clearAllNotifications`; auto-dismiss timing; action callback; test reset |
| `frontend/src/components/ToastContainer.test.ts` (new, #86) | 7 tests | role=alert / role=status, aria-live=assertive / polite, action button wires to handler + dismiss, dismiss button, multi-stack rendering |
| `frontend/src/templates/TemplatePicker.test.ts` (#95, #94, #96) | +5 tests | Pre-filled page-name field in new-page mode; `focus-page-title` event dispatch on success; `onCreatedPage` callback regression; toast pushed on `CreatePageFromTemplate` / `RenderTemplateBlocks` failure; plugin templates group under `Plugins / <plugin_id>` |
| `frontend/src/components/SidebarSection.test.ts` (new, #88) | 7 tests | Renders pages when expanded; hidden when collapsed; recursive children; click + keyboard (Enter/Space) toggle; `aria-level` per depth; page click invokes `onSelectPage` |
| `frontend/src/lib/editor/converters.test.ts` (#85) | +4 tests | `{{embed:uuid}}` tokenized as embedNode; `((uuid))` tokenized as blockReferenceNode; full-text round-trip with both; top-level embedNode round-trip |

Run with: `go test -race -count=1 ./...` (Go) and `npm run check` + `npm test` (frontend, vitest). All ten Go packages pass; `npm run check` reports 0 errors and 0 warnings; `npm test` runs **162 vitest tests** across 23 files.

## Automated Tests

Run with: `go test -race -count=1 ./...` (Go) and `npm run check` + `npm test` (frontend).

### Go coverage added

| File | Tests | What is covered |
|---|---|---|
| `app_nav_test.go` (new) | `TestGetSetSidebarWidth_RoundTrip`, `TestSetSidebarWidth_Clamps`, `TestGetSetNavOrder_RoundTrip`, `TestRenamePage_UpdatesFrontmatterAndFile`, `TestRenamePage_NameCollision`, `TestRenamePage_PathTraversal`, `TestRenameSection_UpdatesAllFiles`, `TestDeletePage_MovesToTrash`, `TestDeleteSection_DeletesAllPages`, `TestLockBlocksWrite_NoDeadlock` | Config ui: block persistence, rename frontmatter + index, delete trash path, per-block lock deadlock-safety (#63, #68, #62, #83, #64) |
| `backend/db/db.go` (#79) | WAL mode assert in `initSchema` | Belt-and-suspenders: rejects mounts that silently downgrade from WAL |
| `backend/db/netfs_*.go` (#79) | Platform-specific network filesystem detection | NFS/SMB/CIFS denylist on Linux, macOS, Windows |

### Frontend

`npm run check` reports **0 errors and 0 warnings** (the #75 a11y pass target — all `svelte-check` a11y warnings fixed). `npm test` runs **123 vitest tests** across 20 files.

### Key a11y fixes (#75)

| Component | Fix |
|---|---|
| SearchModal, BlockPickerModal | Scrim-as-button sibling + `role="dialog"` + `aria-modal` (SettingsShell pattern) |
| Sidebar create/rename modal, context menu, delete dialog | Same pattern; `role="menu"` + `tabindex="-1"` on context menu |
| Sidebar treeitems | `aria-selected` added on section + page items |
| VirtualScrollContainer title | `role="textbox"` removed from contenteditable `<h1>` |
| Agenda task row | `onkeydown` (Enter/Space) keyboard activation |

## Manual Verification Matrix (`wails dev`)

1. **Resizable sidebar (#63):** drag divider → width changes live → restart → persists. Double-click → 256. Arrow keys → ±8px. Ctrl+B → collapses → restores last width. Shrink window → auto-collapse.
2. **Inline title (#83):** New Page → "Untitled" created + title auto-focused → type a name → renames file + sidebar updates.
3. **Context menu (#62):** right-click a page/section → Rename/Delete. Delete → confirmation dialog → moves to `.system/trash/`.
4. **macOS titlebar (#61):** on macOS, no duplicate window controls; ~80px left inset for traffic lights. Windows/Linux unchanged.
5. **Drag-to-reorder (#68):** drag a section up/down → order persists across restart.
6. **Plugin reactivity (#69):** navigate between pages → Agenda/Calendar/Kanban reflect the new active location without a plugin reload.
7. **Embed/editor race (#64):** edit an embed → source block updates; type in the editor while embed auto-saves → no clobbering.
8. **A11y (#75):** tab through every view → logical order, visible focus rings, all actions reachable via keyboard. `npm run check` = 0 warnings.


## Multi-Issue Hardening + External Notebooks — Coverage Added (#100 #118 #120 #122 #123 #124)

### Go coverage added/updated

| Package | New / updated tests | What is covered |
|---|---|---|
| `silt` (main) | `TestReleaseBlockMutex_*`, `TestPluginUpdateTaskMeta` (tri-state), `TestUpdatePluginSetting_*`, `TestResolveNotebookDir`, `TestLinkNotebook_IndexesAndUnlinkLeavesFiles`, `TestLinkNotebook_RejectsCollisions`, `TestListNavigation_LinkedDisconnectedWhenRootMissing`, `TestLinkedNotebook_PageCRUD_RoutesToLinkedRoot`, `TestRenameNotebook_RefusesLinked` | #122 per-block mutex eviction; #123 pin tri-state + clear sentinel; #120 atomic per-plugin setting (RMW under configMu, preserves other fields); #100 resolveNotebookDir, link/unlink lifecycle, collision + ancestor rejection, offline `Disconnected`, **linked page CRUD routes to the linked root (delete-in-place, no vault trash)**, RenameNotebook refuses linked |
| `backend/core` | `TestReleaseBlockMutex_*` (entry-deleted, fresh-entry, no-deadlock, serialize, batch), `TestLockBlocksWrite_*` | #122 generation-based block-mutex eviction (-race) |
| `backend/db` | `TestBlocksSource_DefaultsVault`, `TestIndexFileBlocks_PinnedProjection` | #100 `source` column default + source-aware index; #123 `tasks.pinned` projection |
| `backend/monitor` | `TestDirectoryWatcher_GoverningRoot_LongestPrefix`, `TestDirectoryWatcher_ResolveFileMetadata_LinkedRoot`, `TestDirectoryWatcher_AddRemoveWatchRoot` | #100 multi-root watcher: longest-prefix resolution, per-root (vault vs linked) metadata attribution, AddWatchRoot/RemoveWatchRoot registry |
| `backend/parser` | `TestParseLine_PinAndProgress` (tri-state + toggle-safety), `TestBlocksSource_*` callers | #123 Pinned `*bool` round-trip + on→off→on no-drift |

### Frontend coverage added/updated

| File | Tests | What is covered |
|---|---|---|
| `plugins/sdk.test.ts` | `localToday`, `plusDaysISO` | #118 local-day anchor (local-midnight behavior, month/year/leap bounds) |
| `plugins/context.test.ts` | `updateTaskMeta` sentinel translation | #123 pin `boolean|null` → `-2/-1/0/1` |
| `plugins/first-party/silt-kanban/Kanban.test.ts` | due-date `today`/`this week` SQL param tests; scope auto-narrow (follows nav / sticky / reset) | #118 (bound `?` not `date('now')`); #124 scope auto-narrow |

### Manual Verification Matrix (`wails dev`)

1. **#122 (mutex eviction):** create + delete many pages in a long session → memory stays flat (no per-block-mutex leak).
2. **#123 (pin tri-state):** type `[pin:: false]` → round-trips; pin on→off→on via the Kanban card → never silently reverts.
3. **#118 (local dates):** near local midnight, the Kanban Today/Overdue chips match the local day (not UTC).
4. **#124 (scope):** open Kanban, navigate to a page → scope narrows to page; pick Vault → sticks; click Follow → tracks nav again.
5. **#120 (atomic setting):** edit Kanban columns while externally editing `config.yaml` in an external editor → no clobber.
6. **#100 (linked notebooks):** "Link External Folder…" → a synced folder appears as a notebook (badge + root tooltip); edit/create/rename/delete pages in place (vault untouched); delete a linked notebook → UNLINKS (files untouched); take the linked root offline → badge flips to cloud_off, last-sync rows still show.

## Known Gaps (deferred)

- Co-located per-notebook config (`<linkedRoot>/.system/config.yaml` read-through) — deferred (follow-up issue).
- Frontend sidebar badge render test — blocked by the documented jsdom `onMount` limitation in `Sidebar.test.ts` (tree does not populate); covered by manual verification.

## Page Tabs + Smart Graph NodeView Tests (#142, #127)

### Go coverage

| Package | Tests | What is covered |
|---|---|---|
| `backend/config` | `TestDefaults_TabsConfig`, `TestOpenTabs_RoundTrip`, `TestLoad_LegacyConfigMissingTabFields` (backward-compat), `TestLoad_MalformedOpenTabsEntryNotFatal`, `TestNormalize_MaxOpenTabsClamp` (incl. upper-bound 32 clamp), `TestNormalize_EnablePreviewTabsNilBecomesTrue` | #142 UIConfig schema: TabRef, OpenTabs, ActiveTab, EnablePreviewTabs (*bool), MaxOpenTabs; defaults (preview=true, max=8); YAML round-trip; legacy config backward-compat; malformed entry handling; normalization clamps [1, 32] |
| `silt` (main) | `TestGetSetOpenTabs_RoundTrip`, `TestGetOpenTabs_PruneStaleTabs` (delete page → tab dropped), `TestGetOpenTabs_PruneMalformedEntries` (empty Page dropped), `TestSetOpenTabs_AtomicWrite` (no leftover .tmp), `TestSetOpenTabs_NilBecomesEmptySlice`, `TestGetOpenTabs_EmptyVault`, `TestSetOpenTabs_SelfWriteSuppressed` (RegisterSelfWrite end-to-end via real ConfigWatcher) | #142 IPC: GetOpenTabs/SetOpenTabs (OpenTabsResult struct), stale-tab pruning against ListNavigation, atomic write, self-write suppression |

### Frontend coverage

| File | Tests | What is covered |
|---|---|---|
| `frontend/src/lib/tabs.test.ts` (new, 35) | openPage all 7 rules (pinned-exists→activate, preview-same-page→activate-or-PROMOTE-on-pin, pin→new, activate-only, enable_preview_tabs=false, LRU eviction preview-first/pinned), closeTab MRU neighbor, promotePreview, cycleTab MRU, mruOrder, pickEvictionVictim, tabMatches/findTab, generateTabId | #142 pure state machine — every transition + edge case, incl. dblclick-pin promotion fix |
| `frontend/src/components/TabStrip.test.ts` (new, 16) | role=tablist/tab/tabpanel ARIA, aria-selected, aria-controls, roving tabindex, Arrow/Home/End/Enter/Space/Delete keyboard nav, click→onSelectTab, × →onCloseTab, dblclick→onPromoteTab, middle-click→onCloseTab, preview-italic class, pinned/preview distinction | #142 TabStrip component — full ARIA + keyboard contract |
| `frontend/src/components/SidebarSection.test.ts` (+2) | dblclick→onPinPage, middle-click→onPinPage | #142 sidebar click modes (preview vs pin) |
| `frontend/src/lib/editor/nodeview-test-harness.test.ts` (new, 5) | boots editor with NodeViews in jsdom, NoteBlock data-node-view-wrapper, embedNode NodeView, blockReferenceNode NodeView, mkBlock defaults | #127 reusable NodeView test harness + Smart Graph rendering smoke gate |
| `frontend/src/components/EmbedPortal.test.ts` (new, 5) | loading state, happy-path content render, not-found state, block:changed subscription, debounced PluginMutateBlock persistence | #127 EmbedPortal component: resolve/render/not-found/live-sync/edit-persist |
| `frontend/src/components/BlockReferenceChip.test.ts` (new, 6) | loading state, happy-path text render, navigate-to-block on click, navigate-to-block on Enter, unresolved state, breadcrumb tooltip | #127 BlockReferenceChip component: resolve/navigate/unresolved/tooltip |
| `frontend/src/components/TipTapEditor.test.ts` (new, 5) | embedNode NodeView for sole {{embed:uuid}}, blockReferenceNode for inline ((uuid)), both in same block, data-node-view-wrapper presence, multiple NoteBlock NodeViews | #127 TipTapEditor smart-graph content via the NodeView pipeline |

`npm test` now runs **336 vitest tests** across **43 files**. `npm run check` reports **0 errors, 0 warnings**.

## Sprint 15 — Source / Rendered View Toggle follow-ups (#194, #195, #178)

Closes the three open follow-ups to the base view toggle (#171, shipped in PR #193) in one branch (`feat/sprint15-view-toggle-followups`): Shiki highlighting in the Source view (#194), per-tab view-mode persistence on `TabEntry` / `TabRef` (#195), and a profiling pass + the editor-teardown memory optimization (#178).

### Automated Tests

Run with: `go test -race -count=1 ./...` (Go) and `npm run check` + `npm test` (frontend, vitest). No Playwright (per `AGENTS.md` — the Wails webview can't run headless in CI).

### Go coverage added

| Package | Tests | What is covered |
|---|---|---|
| `backend/config` | `TestTabRef_ViewMode_RoundTrip` (source persists, edit omits), `TestNormalize_TabRefViewModeSanitize` (source survives; ""/edit/garbage → ""), `TestLoad_LegacyOpenTabsMissingViewMode` (pre-#195 config backward-compat) | #195 `TabRef.ViewMode` YAML round-trip + `normalize()` sanitization + legacy backward-compat |

### Frontend coverage added

| File | Tests | What is covered |
|---|---|---|
| `frontend/src/lib/tabs.test.ts` (+8, #195) | new-tab seeds `defaultViewMode`; default→edit; existing-tab mode preserved on activate; preview-slot reuse resets to default; promotePreview preserves; cycleTab preserves; `setTabViewMode` flip + same/missing no-op; LRU eviction doesn't leak the evicted tab's mode | #195 `TabEntry.viewMode` state machine |
| `frontend/src/lib/editor/useMarkdownHighlighter.test.ts` (new, 5, #194) | dark/light type threading; bg/fg/editor.background from tokens; dark vs light accent distinct; typographic fontStyle for bold/italic/strike/heading/quote; fallback defaults for missing tokens | #194 Silt-token → Shiki-theme mapper |
| `frontend/src/components/editor/MarkdownSourceViewer.svelte.test.ts` (new, 8, #194) | plain-text fallback before resolve; highlighted HTML after resolve; plain-text fallback on error; mode→theme on mount; re-highlight on content change; line-number gutter; Copy as Markdown; read-only `role="document"` landmark | #194 Source viewer (Shiki mocked; integration verified separately) |
| `frontend/src/components/VirtualScrollContainer.test.ts` (+2, #195/#178) | toggle button `aria-pressed`/`aria-keyshortcuts`; #178 teardown — TipTapEditor mounted in edit, torn down in source (only MarkdownSourceViewer present) | #195 a11y + #178 editor teardown |

### Manual Verification Matrix (`wails dev`)

| # | Scenario | Expected |
|---|---|---|
| 1 | Open a page, type + format (bold/italic/link), press `Ctrl+Shift+V` (while editing) | Hotkey flips to Source (it now works editor-focused — was dead before #195); Source shows raw markdown with the `**`/`*`/`[]()` marks visible and **syntax-highlighted** in the active theme's colors |
| 2 | In Source view, flip dark↔light (Appearance tab or OS) | Source re-highlights to the new mode's token colors |
| 3 | Set a tab to Source, navigate to another page, come back | The tab is still in Source (per-tab sticky within the session) |
| 4 | Quit + relaunch | Persisted tabs restore their view mode (a tab stuck in Source reopens in Source) |
| 5 | Set `editor.default_view_mode: "source"`; open a new page | The new tab opens in Source |
| 6 | Open the `.md` in VS Code, edit it externally, return to Silt Source view | The Source view reflects the external edit (fsnotify) |
| 7 | Source view: byte-identity check — `==highlight==`, `<u>`, `<!-- silt-align -->`, `<span style>` color, GFM table pipes, `<!-- id: uuid @ date -->`, `[key:: value]` all visible verbatim | Nothing hidden, added, or re-ordered |
| 8 | Toggle Edit→Source→Edit on a long page | The Edit scroll offset is restored on return to Edit (#319); content intact (auto-save flushed on unmount) |
| 9 | Open 8 tabs, set several to Source | Per `docs/editor-memory-profiling.md`, the Source tabs hold no TipTap editor (DevTools Memory confirms the drop vs. all-edit) |

# Sprint 16 — Advanced Blocks & Editor Interactions (#190 #191 #181 #184 #319)

## Automated Tests

| File | Coverage |
|---|---|
| `backend/db/db_test.go` (+1, #184) | `TestDistinctOwners` — dedup, empty-owner skip, non-task exclusion, sorted |
| `frontend/src/lib/editor/mentionSuggest.test.ts` (new, 11, #184) | owner filter (empty/substring/no-match); context detection (`@` trigger, multi-word query, email guard, code-block guard, no-`@`); insertion replaces `@query` with a `mentionNode` + caret after; no-op when no context |
| `frontend/src/lib/editor/converters.test.ts` (+7, #184/#191) | `@[name]` tokenize + serialize; email non-tokenize; inline `$...$` round-trip; currency `$5` non-tokenize; `$$...$$` block round-trip via blocksToDoc/docToBlocks |
| `frontend/src/lib/editor/useMermaid.test.ts` (new, 5, #190) | valid → SVG; initialize once per theme (`securityLevel: strict`); invalid source → readable error (no throw); memoise by (theme, source); empty source renders nothing |
| `frontend/src/lib/editor/keymaps.test.ts` (+4, #181) | `moveActiveBlock` up/down swaps; no-op at top/bottom |
| `frontend/src/components/VirtualScrollContainer.test.ts` (+3, #319) | scroll restored after Edit→Source→Edit; stale offset clamped to scrollHeight; no force-scroll on cold open |

## Manual Verification Matrix (`wails dev`)

| # | Scenario | Expected |
|---|---|---|
| 1 | Type `@` in a note whose vault has task owners | Mention typeahead lists distinct owners; ↑/↓ + Enter inserts an `@[name]` chip; backspace deletes it whole; the `@[name]` survives a save/reload round-trip |
| 2 | Type `$E=mc^2$` in a note; on reload it renders via KaTeX; click opens an edit prompt | Inline math renders; round-trips verbatim; `5$ cash` / `$5` stay literal |
| 3 | `/math` → enter `\int_0^1 x\,dx` | Centered block equation renders; round-trips as a sole `$$...$$` line |
| 4 | A ```mermaid fence with `graph TD; A-->B` | Renders an SVG diagram; theme flip (dark↔light) re-renders; invalid source shows a readable error; Edit-source toggle reveals raw; Copy works |
| 5 | Drag a block's grip to reorder; also `Alt+ArrowUp/Down` | Block moves; on-disk order updates on save; `Alt+↑/↓` moves the active block |
| 6 | Long page, Edit→Source→Edit | Edit scroll offset restored (#319); cursor restore is a tracked follow-up |
| 7 | Set `ui.formatting.math_enabled: false`; reload | The `/math` slash command is gone (existing math still renders) |

## Known Gaps (deferred)

- **Cursor-position restore (#331)** across the Edit↔Source round-trip — scroll-offset restore (#319) ships; cursor restore is deferred to its own follow-up PR because it needs an async editor-readiness path the jsdom layer can't drive (no webview e2e per AGENTS.md).
- ~~**#181 indent-on-drop** (Notion-style depth change on horizontal drop)~~ — **Resolved** (#330): dropping a block now sets its indent from the horizontal drop position; covered by the manual matrix in the Editor & Sidebar Follow-Ups section below.
- ~~**Math entry UX** uses `window.prompt` for `/math` and click-to-edit~~ — **Resolved** (#328): the in-app LaTeX popover (live preview, `Ctrl/Cmd+Enter` commit, `Esc` cancel) replaces the native prompt; covered by the manual matrix below.

---

# Editor & Sidebar Follow-Ups — Manual Matrix Additions (#326–#330, #332)

These PRs ship a mix of pure-helper unit tests (the indent-depth resolver,
the mention owner write-back planner, the math InputRule finder) and
interactive paths the jsdom layer cannot drive (HTML5 drag/drop with layout,
the LaTeX popover, the `@` typeahead against live IPC, vault-switch races).
The jsdom-untestable paths are pinned here.

## Manual Verification Matrix (`wails dev`)

| # | Scenario | Expected |
|---|---|---|
| 1 | Drag a block's grip and drop it indented to the right | The block becomes a child (deeper `depth`); drop in line keeps the same depth; the drop-zone indicator shows the target depth; on-disk order + depth persist after save; `Alt+Up/Down` still moves blocks; no mis-indent at block boundaries; `Esc` cancels an in-flight drag (#330). |
| 2 | `/math`, then type `\int_0^1 x\,dx`; also click an existing equation | The LaTeX popover opens with a live KaTeX preview; multi-line editing works; `Ctrl/Cmd+Enter` commits, `Esc` cancels; a parse error shows in the preview; committing an empty equation is blocked (#328). |
| 3 | In a task line, type `@alice` and confirm; confirm again as `@bob` | The task's owner becomes `alice`, then `bob` (the `[owner:: …]` token is written/updated, not duplicated); mentioning `@alice` in a regular paragraph inserts the chip with no owner write-back (#329). |
| 4 | Switch vaults while a Kanban/Calendar sidebar is mounted | No "missing session token" error; the new vault opens with default scope/filters/focusDate (not the previous vault's) (#326 items 1, 5). |
| 5 | Press `Ctrl+Shift+B` while editing, and again with the sidebar collapsed | Focus jumps into the active sidebar's first control; if collapsed it expands first (#326 item 8). |
