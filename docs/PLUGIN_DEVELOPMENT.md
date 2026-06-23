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

> **UI rendering (third-party):** Third-party plugins render UI through a sandboxed `<iframe srcdoc>` + postMessage bridge (#117). Use `ctx.registerSurface(...)` (requires the `ui-surface` capability grant) to mount HTML into sidebar-panel, modal, status-bar, or settings-panel slots. The iframe carries a restrictive CSP (`connect-src 'none'`); all network traffic routes through `ctx.fetch`. See §8 for the full rendered-UI surface guide.

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

### 7.1 Trust boundary and binding identity (#151, #152)

**The SDK (`PluginContext`) is the contract.** Plugin authors MUST use the SDK
methods provided through `ctx.*`. Importing `wailsjs/go/main/App.js` directly
and calling the raw Wails bindings is a **violation of the trust model** and
will break when per-plugin isolated webviews land (#152 long-term).

**Session tokens (#151, #236).** When a plugin loads, the host calls
`RegisterPluginSession(pluginID)` to mint a session token. Every privileged SDK
closure captures this token and passes it to the Go binding alongside the
`pluginID`. The Go side validates `token ↔ pluginID` before checking grants, so
a plugin cannot impersonate another by passing a different `pluginID` to a raw
binding — it doesn't have the target plugin's token.

**Enforced on every `Plugin*` binding (#236).** The session-token check is the
first gate on every privileged `Plugin*` binding — file I/O, OS integration,
network/fetch, page/section/notebook CRUD, block CRUD, raw query, file pickers,
clipboard, notifications, and surface registration. A malicious plugin that
bypasses the SDK (imports `wailsjs/go/main/App.js` directly) and calls any
binding with a different `pluginID` is rejected at the session boundary before
`requireGrant` runs. Spoofing a first-party pluginID (which is implicitly
granted every capability) does not help the attacker: it does not have that
plugin's session token.

This is a **stepping stone**, not a full isolation boundary. Because plugin JS
runs in the main webview, a determined attacker could read another plugin's
token from closure scope. The full fix requires per-plugin isolated webviews
(#152 long-term), which is tracked as a separate future issue.

**Runtime integrity (#161).** The installer computes `sha256(index.js)` and
writes it into `plugin.json` as `contentSha256`. On every load, the frontend
verifies the hash before importing the module. A tampered `index.js` (modified
post-install by another process, a sync conflict, or malware) is refused with a
clear error.

### 7.2 Content-mutate capability (#156)

Block CRUD operations (`createBlock`, `deleteBlock`, `moveBlock`,
`applyBlocks`) are gated by the `content-mutate` capability. A plugin that
mutates blocks must declare it in the manifest:

```json
{
  "capabilities": { "content-mutate": true }
}
```

First-party plugins inherit this capability implicitly. Third-party plugins
that previously mutated blocks without declaring it will need to update their
manifest.

---

## 8. v2 SDK — Capabilities, Lifecycle, and Extended APIs

The v2 SDK (milestone #11) expands the plugin surface with capabilities,
lifecycle hooks, a typed event bus, content CRUD, file I/O, OS integration,
network/fetch, editor extension points, rendered UI surfaces, and a
declarative settings schema. Every privileged binding is gated by the
capability model (#113).

### 8.1 Capabilities & permissions (#113)

Plugins that need privileged access declare `capabilities` in the manifest:

```json
{
  "id": "my-sync",
  "name": "Sync",
  "version": "1.0.0",
  "main": "index.js",
  "capabilities": {
    "network": true,
    "write-files": "notebook",
    "os-open": true
  }
}
```

Recognized capabilities: `read-files`, `write-files`, `network`, `os-open`,
`os-clipboard`, `os-notify`, `ui-surface`, `editor-schema`. (`exec` is
intentionally deferred.)

Grants are per-vault, stored in `config.yaml` under `plugins.grants`. The user
is prompted on first use (contextual, low-fatigue) and can review/revoke in
**Settings → Plugins**. Enforcement is server-side — the Go backend checks
every privileged call and returns a structured `CapabilityDeniedError`.

### 8.2 Lifecycle hooks & event bus (#106)

```ts
export default {
  manifest,
  onVaultOpen(ctx) { /* vault is open, ctx is fully usable */ },
  onVaultClose()   { /* release watchers/timers */ },
  onShutdown()     { /* app is exiting */ }
}
```

Typed event subscription:

```ts
const off = ctx.on('block:changed', (e) => {
  console.log(e.id, e.notebook, e.section, e.page)
})
// Events: 'block:changed', 'config:changed', 'active-notebook:changed', 'selection:changed'
// Returns an unsubscribe; auto-cleaned on disable/uninstall/vault-close.
```

### 8.3 Content API (#104)

Beyond `sqliteQuery` + the base mutators:

```ts
// Typed query helpers
ctx.queryByTag('work/sprint-4')
ctx.queryByDateRange('2026-06-01', '2026-06-30')
ctx.fullTextSearch('meeting notes')
ctx.getBacklinks(uuid)
ctx.getEmbeds(uuid)

// Block CRUD (same atomic-write path as mutateBlock)
const newId = await ctx.createBlock({ type: 'TASK', text: 'New task', after: parentUuid })
await ctx.deleteBlock(uuid)
await ctx.moveBlock(uuid, { after: targetUuid })

// Page / section / notebook CRUD
await ctx.createPage('Work', 'Projects', 'NewPage', '2026-06-19')
await ctx.createSection('Work', 'NewSection')
await ctx.createNotebook('Personal')
await ctx.deletePage('Work', 'Projects', 'OldPage')
await ctx.renamePage('Work', 'Projects', 'OldName', 'NewName')
```

### 8.4 File I/O (#108)

Read/write non-markdown files within a notebook (attachments, assets, caches).
Gated by `read-files` / `write-files`.

```ts
const bytes = await ctx.readFile('Work', 'attachments/report.pdf')
await ctx.writeFile('Work', 'attachments/export.json', new Uint8Array([...]))
await ctx.deleteFile('Work', 'attachments/old.pdf')
const entries = await ctx.listDir('Work', 'attachments')
const root = await ctx.notebookRoot('Work')
const scratch = await ctx.scratchDir('Work')  // <notebook>/.system/plugins/<id>/data/
```

Writes are restricted to `attachments/` + the plugin's own scratch dir.

### 8.5 OS integration (#114)

```ts
await ctx.openInNativeHandler('Work', 'attachments/report.pdf')  // gated: os-open
await ctx.openUrl('https://example.com')                         // gated: os-open
const path = await ctx.pickOpenFile('*.pdf')                      // user-driven
const savePath = await ctx.pickSaveFile('export.json')
const text = await ctx.clipboardRead()                            // gated: os-clipboard
await ctx.clipboardWrite('copied text')                           // gated: os-clipboard
await ctx.notify({ title: 'Sync', body: 'Done!' })               // gated: os-notify
```

### 8.6 Network / fetch (#115)

```ts
const res = await ctx.fetch('https://api.example.com/data', {
  method: 'POST',
  headers: { Authorization: 'Bearer ...' },
  body: JSON.stringify({ query: '...' }),
  timeoutMs: 10000
})
// res = { status, headers, body, ok }
```

CORS-free (Go-side proxy), with timeout/size/redirect caps. Host + status are
audit-logged in Settings → Plugins (never the body).

**Rate limiting (#153).** Each plugin's fetch calls are throttled by a
per-plugin token-bucket rate limiter (default: 1 request/sec, burst of 10).
To request a higher limit, declare it in the manifest:

```json
{
  "ratelimit": { "rps": 5, "burst": 20 }
}
```

`rps` must be > 0 and <= 10; `burst` must be > 0 and <= 100. Values outside
this range are rejected at install time. The response body cap is 2 MB per
request.

**Redirect header hygiene (#160, #247).** On a cross-host redirect, custom
auth headers (`X-Api-Key`, etc.) are stripped and `User-Agent` is reset to
`Go-http-client/1.1` — do NOT embed credentials in the `User-Agent` header
(anti-pattern; will leak across cross-host redirects). Same-host redirects
preserve both custom headers and the plugin-supplied `User-Agent` (legitimate
use case: API versioning via UA).

### 8.7 Editor extension points (#110)

Register slash commands:

```ts
const off = ctx.registerSlashCommand({
  id: 'my-cmd',
  label: 'Do Something',
  description: 'Inserts a special block',
  icon: 'bolt',
  onSelect: (editor, pos) => {
    editor.commands.insertContent('Hello!')
  }
})
```

Custom embed blocks via the generic `embedBlock` node (round-trips through
`<!-- silt-embed: {json} -->` markers).

### 8.8 Rendered UI surfaces (#117)

Third-party plugins render UI in a sandboxed iframe:

```ts
const off = ctx.registerSurface({
  id: 'my-panel',
  kind: 'sidebar-panel',
  label: 'My Panel',
  html: '<div id="app">Loading...</div>'
})
```

The iframe's `window.__siltCtx` is a postMessage proxy for the full
PluginContext. Theme tokens are injected as CSS custom properties.

### 8.9 Settings schema (#103)

```json
{
  "id": "my-plugin",
  "settings": [
    { "key": "apiKey", "label": "API Key", "type": "string" },
    { "key": "theme", "label": "Color", "type": "select", "options": ["dark", "light"], "default": "dark" },
    { "key": "autosync", "label": "Auto-sync", "type": "bool", "default": false }
  ]
}
```

Settings → Plugins renders the form generically. Read merged values:

```ts
const key = await ctx.getSetting('apiKey')  // schema-default-aware
```
