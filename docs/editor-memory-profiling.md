# Editor Memory: N×TipTap Profiling & Optimization (#178)

Follow-up to PR #174 (Page Tabs). The multi-editor surface mounts one TipTap
editor per open tab (hidden via `display:none`), so editor memory scales with
the open-tab count. This doc records the cost model, the profiling
methodology, the chosen optimization (now shipped), and the levers left for
future work.

## Cost model (code-level)

Each open tab holds, for its lifetime, a **full live editor** even when the
tab is inactive or in Source view:

- A ProseMirror `Editor` instance + its `EditorState` (doc model, selection,
  plugin state) and the `EditorView` (DOM + decorations).
- Every Silt block extension with a NodeView (`EmbedPortal`,
  `BlockReferenceChip`, `TaskBlockView`, `NoteBlockView`, …) instantiates a
  Svelte component per occurrence via the embed-chain Svelte context. NodeViews
  rely on the host element being in the live DOM (see
  `nodeview-test-harness.ts`), which is why the `display:none` architecture
  keeps them mounted rather than snapshotting.
- All editor extensions (StarterKit, marks, tables, details, callouts,
  CharacterCount, Focus, TrailingNode, TaskMetaSuggest, …) per instance.
- Per-tab autosave timer + focus-lease/heartbeat bookkeeping.

At the `max_open_tabs` cap (default **8**, hard cap **32**, normalized in
`backend/config/config.go`), the worst case is 32× the above.

## Shipped optimization: editor teardown in Source view

A tab held in **Source view** is a read-only markdown projection — there is no
reason to keep its full TipTap editor alive. As of this sprint the
Edit/Source switch lives in `VirtualScrollContainer`: Source mode renders only
`MarkdownSourceViewer` and does **not** mount `TipTapEditor`. Svelte destroys
the editor (ProseMirror doc + NodeViews + listeners) on the switch and
rebuilds it from `blocks` on return to Edit (content is on disk via auto-save).

This is the highest-leverage, lowest-risk lever because Source mode is
read-only: no in-flight edits can be lost, and the content round-trips through
the on-disk file. The trade-off is a **scroll/cursor reset** on an
Edit→Source→Edit round-trip (the remounted editor starts at the top), which is
acceptable for a deliberate view switch and is the sanctioned default from the
sprint plan (snapshot/restore was deferred as higher-complexity).

Lifecycle correctness relied on by this change:
- `TipTapEditor.onDestroy` flushes the pending save, then releases the focus
  lease — so unmounting in Source view never drops an edit or leaks a lease.
- `VirtualScrollContainer.hasFirstEdit` is bound to the container (not the
  editor), so an edit-to-pin promotion cannot fire twice across a remount.
- `blocks` is held by `VirtualScrollContainer` and passed down, so the
  remounted editor rebuilds from current content.

## Profiling methodology (run before changing the cap or adding keepalive)

Interactive measurement requires the GUI webview (`wails dev`) — the headless
CI model can't drive it (see `AGENTS.md` — no Playwright). To gather data:

1. `wails dev`, open DevTools → Performance / Memory.
2. Raise `ui.max_open_tabs` to `32` in `.system/config.yaml`; restart.
3. Take a baseline heap snapshot with **1** tab open in Edit mode.
4. Open tabs to the cap (32), all in **Edit** mode; snapshot. The delta ÷ 31 is
   the per-editor cost.
5. Switch every tab to **Source** mode; snapshot. The drop vs. step 4 is the
   Source-teardown win (this optimization).
6. Repeat with a representative large page (many embeds/references) to stress
   the NodeView path.

`performance.measureUserAgentSpecificMemory` (Chromium) can script this for a
repeatable number if exposed by the webview.

## Levers evaluated

| Lever | Memory win | Risk | Status |
|---|---|---|---|
| **Editor teardown in Source view** (shipped) | Per-source-tab editor freed | Low (read-only, content on disk) | **Done** |
| Tighter default cap (8 → 4/6) | Linear | Low, but changes UX | Deferred — gather profiling data first; 8 may be fine |
| Snapshot-on-deactivate (unmount inactive editors) | Per-inactive-tab editor freed | High — touches the focus-lease lifecycle + NodeView-DOM dependency | Deferred until profiling shows 8-tab all-edit memory is a real problem |
| Lazy-mount-with-keepalive | Lower initial cost only | Low | Rejected — doesn't bound memory, only delays it |

## Recommendation

Ship the Source-view teardown (done). Do **not** lower the cap or add
inactive-tab snapshotting until the profiling methodology above produces data
showing the 8-tab default (or the 32-tab worst case) is a real memory problem
on target hardware. The default of 8 was chosen deliberately and is likely
fine; 32 is the stress case the cap exists to bound.
