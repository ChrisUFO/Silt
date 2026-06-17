# Page Templates

Silt ships a full **page template system**: pick from a built-in library of first-class templates, browse any custom templates you have authored, and insert a new page (or a section into the current page) from any template. Custom templates live alongside your data and can be added without touching the binary.

> **Engineering docs.** This is the *end-user* guide. For the internal pipeline (Go loader → Wails IPC → Svelte store → picker), see [`ARCHITECTURE.md` §4.5](../ARCHITECTURE.md). For the product spec, see [`SPECS.md` §6.5](../SPECS.md). This document is the authoritative authoring reference; the frontmatter schema table below mirrors the Go validator (`backend/templates/validate.go`) and is kept in sync by hand.

---

## 1. Concepts

### What is a template?

A **template** is *parameterized Markdown*: a title, a category, an icon, an optional list of user-declared placeholders, and a Markdown body. When you insert a template, the placeholders are resolved (the built-in date/time placeholders auto-fill, user placeholders are filled from the picker form) and the resulting Markdown becomes real Silt blocks — tasks, notes, headings, embeds — indistinguishable from anything you would have typed by hand.

### First-class templates

| Template | Category | Description |
| :--- | :--- | :--- |
| **Notes** | notes | A clean, minimal blank-note baseline. |
| **Meeting Notes** | meetings | Structured meetings — agenda, discussion, decisions, indexed action items. |
| **Project Standup Notes** | meetings | Daily standup / sync — yesterday, today, blockers. |
| **Daily Note** | daily | A date-stamped daily log — the single most-used template in PKM tools. |
| **Project Brief** | projects | Owner, scope, goals, milestones, stakeholders. |
| **1-on-1 Meeting** | meetings | Structured recurring 1:1s — check-in, agendas, action items. |
| **Weekly Review** | weekly | Wins, blockers, lessons, priorities for the week ahead. |
| **Decision Log / ADR** | decisions | An Architecture Decision Record — context, decision, consequences. |
| **Reading Notes** | reading | Book or article capture — key ideas, quotes, synthesis. |
| **Retrospective** | retrospectives | What went well, what didn't, what we'll change. |

> First-class templates are bundled (embedded in the binary) and always selectable. They are **read-only** — you cannot overwrite or delete them. To customize one, copy its content into a new user template with a different id.

### Two ways to insert

1. **New Page → From Template…** — creates a new page pre-filled with the rendered template. Click the `content_copy` icon in the sidebar (next to New Page) or press **Ctrl+Shift+T**.
2. **Insert at cursor** — type `/template` in the editor's slash menu to insert the rendered template's blocks at the cursor position in the current page.

---

## 2. Placeholder syntax

Placeholders use double-curly-brace tokens: `{{name}}`. When a template is rendered, the engine substitutes recognized names and leaves everything else untouched.

### Built-in (default) placeholders

These auto-fill from the current date/time and are always available in every template:

| Placeholder | Resolves to | Format |
| :--- | :--- | :--- |
| `{{date}}` | Today's date | `YYYY-MM-DD` |
| `{{time}}` | Current time | `HH:MM` |
| `{{iso_date}}` | ISO 8601 timestamp | `2026-06-15T09:30:00Z` |
| `{{weekday}}` | Full weekday name | `Monday`, `Tuesday`, … |

### User-declared placeholders

Declare your own placeholders in the frontmatter. Each becomes a text field in the picker form. For example, declaring `meeting_title` makes `{{meeting_title}}` a recognized token that the user fills in before inserting:

```yaml
placeholders:
  - name: meeting_title
    description: The title or topic of the meeting.
    required: true
```

### The passthrough rule (Smart Graph compatibility)

Silt's Smart Graph uses `{{embed:uuid}}` for live block embeds and `((uuid))` for block references (see [`SPECS.md` §5.2](../SPECS.md)). These are **never** treated as template placeholders — they pass through the renderer **byte-for-byte**, so a template can contain embeds and references that work normally when the page is loaded. This is a structural guarantee: placeholder names must match `^[a-z][a-z0-9_]*$` (lowercase letters, digits, underscores), which excludes the colon in `{{embed:uuid}}` and the parentheses in `((uuid))`.

### Unknown placeholders

A `{{token}}` that is not a built-in default, not a declared placeholder, and not a caller-supplied variable is **left untouched** and reported as a warning (never an error). This is forward-compatible: a template that uses a placeholder a future version of Silt will understand still loads today.

### Error handling

When a template write fails (disk full, focus-lock contention, IPC timeout, missing parent directory, etc.), the picker surfaces the error in two places:

1. The **in-modal status region** (`role="status"`, `aria-live="polite"`) at the bottom of the picker, so the error is visible while the picker is open.
2. A **global toast** (`role="alert"`, `aria-live="assertive"`) in the bottom-right corner, so the error is visible after the picker closes. The toast is the same channel `SaveFileBlocks` errors use (#86), giving the user one consistent error surface for all persistence failures.

This applies to both `New Page from Template` and `Insert at cursor` flows.

---

## 3. Frontmatter schema reference

Every template is a Markdown file with an optional YAML frontmatter block at the top. The table below lists every field the validator checks.

> This table mirrors `Validate` in `backend/templates/validate.go`. The two are kept in sync by hand.

| Field | Required | Meaning |
| :--- | :--- | :--- |
| `schema_version` | yes | Schema version string. Currently `"1.0.0"`. Informational / forward-compatible — a higher version whose shape still matches v1 keeps loading. |
| `id` | yes | Unique identifier, lowercase `[a-z0-9_-]`. Used as the filename on disk. Must not collide with a built-in id. |
| `title` | yes | Human-readable display name, shown in the picker. |
| `description` | optional | One-line description shown in the picker. |
| `category` | yes | One of the known categories (see below). Unknown categories are accepted with a warning (forward-compat). |
| `icon` | optional | A Material Symbols icon name (e.g. `today`, `group`). Shown in the picker list. |
| `placeholders` | optional | A list of user-declared placeholders (see below). |

### Placeholder fields

| Field | Required | Meaning |
| :--- | :--- | :--- |
| `name` | yes | The placeholder identifier, `^[a-z][a-z0-9_]*$`. Used as `{{name}}` in the body. |
| `description` | optional | Help text shown in the picker form. |
| `required` | optional | If `true`, the picker highlights the field (visual only — rendering never errors on a missing value). |
| `default` | optional | A fallback value used when the caller does not supply one. |

### Known categories

`notes`, `meetings`, `daily`, `projects`, `weekly`, `decisions`, `reading`, `retrospectives`.

Unknown categories are **accepted with a warning** (forward-compatible) so you can organize templates your way. The picker groups templates by category.

### Validation rules

- **id**: non-empty, matches `^[a-z0-9_-]+$`, must not collide with a built-in id (built-ins are read-only).
- **title**: non-empty.
- **schema_version**: non-empty, dotted-numeric (e.g. `1.0.0`).
- **body**: non-empty (a template must contain Markdown).
- **category**: non-empty.
- **placeholder names**: each matches `^[a-z][a-z0-9_]*$`, no duplicates within a template.

---

## 4. Authoring a template

### A minimal valid template

The smallest template that passes validation — a frontmatter block with the required fields plus a Markdown body:

```markdown
---
schema_version: "1.0.0"
id: my-template
title: My Template
category: notes
---
# {{title}}

_Created: {{date}}_

## Notes

```

### A full annotated template (with placeholders)

```markdown
---
schema_version: "1.0.0"                    # required; currently "1.0.0"
id: sprint-review                          # required; lowercase [a-z0-9_-]; becomes the filename
title: Sprint Review                       # required; shown in the picker
description: End-of-sprint review template. # optional
category: retrospectives                    # required; drives picker grouping
icon: history                               # optional; Material Symbols name
placeholders:                               # optional; drives the picker form
  - name: sprint_name
    description: The sprint name or number.
    required: true
  - name: team_name
    description: The team name.
    default: Platform
---
# {{sprint_name}} Review — {{date}}

**Team:** {{team_name}} ({{weekday}})

## What went well

-

## What didn't go well

-

## What we'll change

- [ ] TODO TASK #3 

## Stats

- Stories completed:
- Bugs resolved:
```

> **Task shorthand:** lines using `- [ ] TODO TASK …` are recognized as indexed tasks and flow into the Kanban, Agenda, and Calendar views automatically. Plain `- [ ]` checkboxes are just notes — they don't appear in task views. See [`SPECS.md` §4](../SPECS.md) for the full task grammar.

---

## 5. Inserting a template

### New Page from Template

1. Click the **content_copy** icon in the sidebar (next to **New Page**), or press **Ctrl+Shift+T**.
2. Search or browse the template list (grouped by category).
3. Select a template — the right pane shows a live preview with today's date/time.
4. Fill in any placeholders (user-declared fields appear below the preview).
5. The **page name** field is pre-filled with `Page YYYY-MM-DD` and focused — you can edit it before confirming, or just press **Enter** to use the default.
6. Click **Create Page**. The new page is created with the rendered Markdown, opens in the editor, and the inline title at the top of the page is focused and selected so you can immediately overwrite the name (the file is renamed on debounce via `RenamePage`, the OneNote model).

### Insert at cursor

1. Type `/template` on an empty block in the editor.
2. Select a template from the slash menu.
3. Fill in any placeholders.
4. Click **Insert**. The rendered blocks are inserted at the cursor position.

> Inserted blocks get **fresh UUIDs** automatically (the editor's `UniqueBlockIds` extension), so inserting the same template twice never creates duplicate block IDs.

### Plugin-provided templates (preview)

> **Status:** template-side data path is shipped (#96). The rendered plugin UI surface (a plugin's own template-listing widget) is tracked in #60 and ships separately.

Third-party plugins can ship their own page templates. At runtime, a plugin registers its templates via the `RegisterPluginTemplates(pluginID, templates)` IPC; the picker then shows them under a `Plugins / <plugin-id>` group header, sorted with the rest of the library. `GetTemplate` resolves the canonical `plugin://<plugin-id>/<template-id>` URI; the templates are an in-memory tier — they never write to `<vault>/.system/templates/`, and a user-authored .md file claiming `plugin_id:` in its frontmatter is rejected by the validator as a corruption indicator.

---

## 6. Managing custom templates

### Adding a custom template

Copy a `.md` file directly into `<your-vault>/.system/templates/`. It appears in the picker immediately (the watcher hot-reloads on file change — no restart needed). The filename should match the id: `<id>.md`.

### Deleting a custom template

Remove the `.md` file from `<your-vault>/.system/templates/`. The picker updates on the next `templates:changed` event.

### Overriding a built-in

You **cannot** overwrite or delete a built-in template (they are embedded in the binary and read-only). To customize a built-in, copy its content into a new `.md` file with a **different id** and place it in `.system/templates/`.

### Where files live

| Platform | Path |
| :--- | :--- |
| All | `<vault>/.system/templates/<id>.md` |

---

## 7. Troubleshooting

| Symptom | Cause & fix |
| :--- | :--- |
| **"template id already exists"** | A template with the same `id` is already on disk. Change the `id` in your frontmatter. |
| **"cannot overwrite built-in template"** | Your `id` matches a first-class built-in (e.g. `daily-note`). Choose a different `id`. |
| **Template not appearing** | (a) The file isn't a `.md` in `<vault>/.system/templates/`. (b) It failed validation — check the load errors. (c) The watcher hasn't fired — reopen the picker. |
| **Unknown placeholder warning** | A `{{token}}` in the body isn't a built-in, declared, or supplied placeholder. Either declare it in the frontmatter `placeholders` list, supply it as a var, or remove the token. The template still loads (warnings are non-fatal). |
| **`{{embed:uuid}}` not resolving** | This is correct — embed syntax passes through the renderer untouched (Smart Graph compatibility). The embed resolves when the page is loaded in the editor. |
| **Placeholder field shows `{{name}}`** | The placeholder was declared but not filled. Type a value in the picker form, or set a `default` in the frontmatter. |

---

## Developer section

### IPC surface (Wails-bound methods on `App`)

| Method | Returns | Description |
| :--- | :--- | :--- |
| `ListTemplates()` | `ListTemplatesResult` | All templates (on-disk + embedded, deduped; on-disk wins). Works pre-vault. |
| `GetTemplate(id)` | `Template` | Full template (incl. body). Not-found → error. |
| `RenderTemplate(id, vars)` | `string` | Rendered Markdown (defaults + vars substituted). |
| `RenderTemplateBlocks(id, vars)` | `[]ParsedBlock` | Rendered + parsed into blocks (for insert-at-cursor). |
| `SaveUserTemplate(t)` | `void` | Validate + atomic write. Builtin ids rejected. Emits `templates:changed`. |
| `DeleteUserTemplate(id)` | `void` | Remove file. Builtin ids rejected. Emits `templates:changed`. |
| `ReloadTemplates()` | `void` | Cache flush + `templates:changed`. |
| `CreatePageFromTemplate(...)` | `string` | Render + write new page (frontmatter + body) + index. Returns the date. |

### The `builtin://` namespace

Built-in templates are embedded via `//go:embed builtin/*.md` and are read-only. The `Source` field on each template distinguishes `builtin` (embedded) from `disk` (user-authored). A `plugin` source is reserved for future plugin-provided templates — the loader and picker are shaped so adding it is an additive change.

### Events

- **`templates:changed`** — emitted after SaveUserTemplate / DeleteUserTemplate / ReloadTemplates / watcher external-edit. The frontend store re-lists on this event.

### On-disk layout

```
<vault>/.system/templates/
  ├── my-meeting-template.md
  └── sprint-review.md
```

---

## Design section

The template picker reuses the same modal chrome and Refined Cyber-Ink token system as the theme picker (`Settings → Appearance`). Iconography follows the Material Symbols convention; the `icon` frontmatter field is a Material Symbols name rendered at 18–20px. No emojis are used in first-class template icons — they are abstract, CSS-friendly glyphs per `DESIGN.md` iconography rules.

---

## Testing

Manual verification (under 2 minutes with `wails dev`):

1. **Ctrl+Shift+T** opens the template picker; all 10 built-ins are listed, grouped by category.
2. Select **Daily Note** — the preview shows today's date and weekday.
3. Enter a page name → **Create Page** → the new page opens with the rendered template.
4. Type `/template` in the editor → select **Meeting Notes** → fill `meeting_title` → **Insert** → the blocks appear at the cursor.
5. Verify action items (TODO TASK lines) appear in the Kanban view.
6. Drop a custom `.md` into `<vault>/.system/templates/` → it appears in the picker without a restart.

---

## FAQ

**Can I delete a built-in template?**
No — built-ins are embedded in the binary and read-only. Copy the content into a new user template with a different id to customize.

**What happens to `{{embed:uuid}}` and `((uuid))` in a template body?**
They pass through the renderer untouched (Smart Graph compatibility). The embed/reference resolves when the page is loaded in the editor.

**What happens on invalid frontmatter?**
The template is rejected with a structured error naming the offending field. It does not appear in the picker and nothing is written to disk.

**Can I use Go template syntax?**
No — the renderer is a simple substitution, not Go's `text/template`. Write `{{date}}`, not `{{ .Date }}`.

---

## Appendix: blank template

Copy-paste this and fill in the placeholders:

```markdown
---
schema_version: "1.0.0"
id: your-template-id
title: Your Template Name
description: A short description.
category: notes
icon: note
placeholders:
  - name: title
    description: The note title.
    required: true
---
# {{title}}

_Created: {{date}} {{time}}_

## Section

- 
```
