# Developing Plugins for Silt

Silt plugins extend the app with new views and capabilities. There are two kinds:

- **First-party plugins** (Agenda, Calendar, Kanban) are bundled with the app and ship as compiled Svelte components.
- **Third-party plugins** are authored by anyone, packaged as a **`.silt-plugin`** archive, and installed via the in-app **Plugin Manager**.

Both kinds use the **exact same PluginContext SDK** — the built-in Agenda/Calendar/Kanban plugins are reference implementations of the same contract a third-party plugin uses.

> The PluginContext contract lives in `frontend/src/plugins/sdk.ts`; the on-disk loader is `frontend/src/plugins/loader.ts`.

---

## 1. The PluginContext SDK

Every plugin receives a `PluginContext` when it loads and (for first-party) as a prop to its view component:

```ts
interface PluginContext {
  activeNotebook: string
  activeSection: string
  activePage: string

  // Read-only SQL against the in-memory index. Only SELECT / WITH are allowed.
  sqliteQuery: (sql: string, params?: unknown[]) => Promise<{ rows: Record<string, unknown>[]; truncated: boolean }>

  // Rewrite a block's body text by UUID (preserves task syntax + the UUID comment).
  mutateBlock: (id: string, text: string) => Promise<boolean>

  // Transition a task block's status: 'TODO' | 'DOING' | 'DONE'.
  updateBlockState: (id: string, status: 'TODO' | 'DOING' | 'DONE') => Promise<boolean>

  // Update per-task metadata (pin, progress). Both fields are optional;
  // pass undefined to skip. Pin and progress are file-resident user
  // intent — the call round-trips through the markdown file, writing
  // [pin:: true] / [progress:: N] tokens via the parser + renderer.
  updateTaskMeta: (id: string, meta: { pinned?: boolean; progress?: number }) => Promise<boolean>
}
```

### Querying the index

`sqliteQuery` runs **read-only** SQL against the in-memory SQLite index. Anything other than `SELECT`/`WITH` is rejected. The schema (see `ARCHITECTURE.md` §3):

- `blocks(id, parent_id, notebook, section, page, file_date, depth, type, raw_content, clean_content, line_number)`
- `tasks(block_id, status, owner, start_date, due_date, priority, pinned, progress, comments_count, links_count)`
- `tags(block_id, raw_path, level_0, level_1, level_2)`

Example — all active tasks with a due date:

```ts
const { rows, truncated } = await ctx.sqliteQuery(
  `SELECT b.id, b.clean_content, t.due_date
   FROM blocks b JOIN tasks t ON b.id = t.block_id
   WHERE t.status != 'DONE' AND t.due_date != ''
   ORDER BY t.due_date ASC`
)
// truncated === true when the result hit the 5000-row cap;
// surface a "narrow your scope" hint to the user when set.
```

### Mutating blocks

`mutateBlock(id, text)` rewrites a block's text in its source file and re-indexes it. `updateBlockState(id, status)` cycles a task's checkbox. `updateTaskMeta(id, {pinned?, progress?})` updates per-task metadata (pin/progress) by round-tripping through the markdown file's `[key:: value]` inline tokens. All three emit a `block:changed` event so live embeds/references update.

### Navigating to a block

To open a block (e.g. when a user clicks a result), dispatch a window event — no SDK call needed:

```ts
window.dispatchEvent(new CustomEvent('navigate-to-block', {
  detail: { notebook, section, page, date: file_date, blockId: id }
}))
```

---

## 2. Anatomy of a third-party plugin

A plugin is a folder containing, at minimum:

```
my-plugin/
├── plugin.json     # manifest (required)
└── index.js        # ESM entry (required; "main" in the manifest, defaults to index.js)
```

### `plugin.json`

```json
{
  "id": "my-plugin",
  "name": "My Plugin",
  "version": "1.0.0",
  "author": "Your Name",
  "description": "What it does.",
  "main": "index.js",
  "minSiltVersion": "1.0.0"
}
```

Rules:

- **`id`** is required and must match `^[a-z0-9-]+$` (lowercase letters, digits, hyphens). It becomes the folder name and the view slot.
- **`name`** and **`version`** are required.
- **`main`** defaults to `index.js`.

### `index.js` (native ESM)

Export a default object describing the plugin. At minimum, `init(ctx)` is called with the `PluginContext`:

```js
// index.js
export default {
  manifest: { id: 'my-plugin', name: 'My Plugin', version: '1.0.0' },
  init(ctx) {
    // Called once on load with the PluginContext.
    console.log('My Plugin loaded for', ctx.activeNotebook)
  }
}
```

> **UI rendering (third-party):** Today, third-party plugins get full SDK access (`init(ctx)` + `sqliteQuery`/`mutateBlock`/`updateBlockState`) and appear in the Plugin Manager, but a dedicated UI surface for arbitrary third-party components ships in a follow-up (Silt cannot compile Svelte at runtime). First-party plugins provide the rendered-view examples. Headless/data plugins (queries, exports, automations via the SDK) are fully supported today.

---

## 3. Packaging a `.silt-plugin`

A `.silt-plugin` file is a **ZIP archive with a custom extension**. It must contain `plugin.json` and your entry module **at the archive root** (no wrapping folder, no absolute paths, no `..` entries — these are rejected on install for safety).

Build it from your plugin folder:

```bash
# From the folder containing plugin.json and index.js:
cd my-plugin
# zip the contents (note the `.` and no `-r ..`), then rename:
zip -r ../my-plugin.silt-plugin .
```

or, on Windows PowerShell:

```powershell
Compress-Archive -Path my-plugin\* -DestinationPath my-plugin.zip
Rename-Item my-plugin.zip my-plugin.silt-plugin
```

The resulting `my-plugin.silt-plugin` is what you distribute.

---

## 4. Installing

1. Open the **Plugin Manager** from the puzzle/extension icon in the titlebar.
2. Click **Install from .silt-plugin…** and pick your `.silt-plugin` file.
3. The manager validates the archive (manifest schema, entry module present, no unsafe paths) and shows a preview (name, version, id, description).
4. Confirm to install. The plugin is extracted to `.system/plugins/<id>/` and loaded immediately.

Installed plugins can be **enabled/disabled** (toggle) or **uninstalled** from the same manager. First-party (bundled) plugins can also be enabled/disabled but cannot be uninstalled.

### Manual install (advanced)

You can also drop a plugin folder directly into `.system/plugins/<id>/` (with `plugin.json` + `index.js`) and restart Silt — the loader discovers plugins by folder.

---

## 5. Enabling/disabling plugins

Every plugin — first-party or third-party — can be **enabled or disabled** from the Plugin Manager (Settings → Plugins). A disabled plugin is not loaded at boot; its view slot shows a "not registered" empty state.

The mechanism differs by source:

| Source | Disable mechanism | Uninstall |
|---|---|---|
| **First-party** (bundled) | `config.yaml` → `plugins.disabled` list | Not available |
| **Third-party** (installed) | `.disabled` sentinel file in `.system/plugins/<id>/` | Removes the folder |

### First-party disable (config.yaml)

When you toggle a bundled plugin off in the Plugin Manager, its id is added to the `disabled` list in `.system/config.yaml`. The loader reads this list at boot and skips any first-party plugin whose id appears in it:

```yaml
plugins:
  active:          # informational only; not a whitelist
    - silt-agenda
    - silt-calendar
    - silt-kanban
  disabled:        # first-party plugins the user has toggled off
    - silt-calendar
```

Re-enabling from the Plugin Manager removes the id from the list. You can also edit `config.yaml` directly (the hot-reload watcher picks up the change without a restart).

### Third-party disable (.disabled sentinel)

Third-party plugins use a `.disabled` sentinel file inside the plugin folder. The loader skips any folder containing `.disabled`. Re-enable by removing the sentinel (or toggling in the Plugin Manager).

> **Note:** `plugins.active` is informational only — it is **not** a whitelist. Plugin discovery is folder-based (third-party) + registry-based (first-party). The `disabled` list is the sole mechanism for disabling first-party plugins.

---

## 6. First-party reference implementations

Read these to see the SDK used end-to-end:

- `frontend/src/plugins/first-party/silt-agenda/Agenda.svelte` — queries tasks, groups Overdue/Today/Tomorrow/Upcoming, marks done via `updateBlockState`, jumps to source.
- `frontend/src/plugins/first-party/silt-calendar/Calendar.svelte` — month/week grids over a windowed due-date query, with navigation.
- `frontend/src/plugins/first-party/silt-kanban/Kanban.svelte` — multi-level scope (vault/notebook/section/page) drag-and-drop board with FLIP animations, filter bar (owner/priority/due/tags), custom columns (add/rename/remove/reorder), card detail panel (pin/progress/comments/links), `updateBlockState` on drop, `updateTaskMeta` for pin/progress, keyboard a11y, and a config-driven column list.

These components receive `{ ctx, manifest }` as props — the same `PluginContext` documented above.

---

## 7. Security model

- `sqliteQuery` is restricted to `SELECT`/`WITH` — plugins cannot mutate the index or schema through it.
- All block mutations (`mutateBlock`, `updateBlockState`, `updateTaskMeta`) route through the Go backend's atomic-write + concurrency-coordinator path; plugins never write files directly.
- `.silt-plugin` archives are validated against zip-slip, absolute paths, and `..` traversal before extraction, and the install path is checked to stay within `.system/plugins/`.
- Plugins run in the same webview as the app (no sandbox). Install only plugins you trust, as with any browser-extension-style model.
