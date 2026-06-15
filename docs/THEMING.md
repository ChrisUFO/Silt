# Theming Silt

Silt's entire visual surface — backgrounds, borders, text, accents, status colors, and optionally fonts — is driven by a single **theme**. Themes are plain JSON files. You can write one in any text editor, drop it into your vault, and select it from **Settings → Appearance**. No restart, no recompile.

> **Engineering docs.** This is the *end-user* guide. For the internal pipeline (Go loader → Wails IPC → Svelte store → `:root` injection), see [`ARCHITECTURE.md` §4.4](../ARCHITECTURE.md). For the product spec, see [`SPECS.md` §6.4](../SPECS.md). For the design-system token vision, see [`DESIGN.md` §7](../DESIGN.md). This document is the authoritative authoring reference; the schema table below mirrors the Go validator (`backend/themes/validate.go`) and is kept in sync by hand (see the note under the table).

---

## 1. Concepts

### Theme vs. mode

- A **theme** is a palette *family* — a complete set of colors (and optionally fonts) with a name like *Cyber Forest*. Each theme file defines **two modes** in one file:
  - **`dark`** — the colors used in dark appearance.
  - **`light`** — the colors used in light appearance.
- A **mode** is which of the two palettes is currently active: **Dark**, **Light**, or **System** (follows your OS preference). Switching mode **never** changes the active theme; it only selects which of the theme's two palettes is rendered.

Both modes are **required** in every theme file. A theme with only a dark mode is invalid and will be rejected on import.

### Semantic accents (the key idea)

Silt components never reference a concrete hue like "teal" or "indigo". Instead they reference two **semantic** accent slots:

| Semantic slot | Meaning | Used for |
| :--- | :--- | :--- |
| `accent.primary` | **"go / done"** | Active selection, completed tasks, primary buttons, focus rings, guide-rail highlights. |
| `accent.secondary` | **"in progress"** | In-progress states, the "doing" lane, secondary highlights. |

Each theme decides which concrete colors map onto `primary` and `secondary`. Cyber Forest maps teal → primary and indigo → secondary. Your theme can map *any* two hues onto them. This is what lets every theme restyle the whole app without per-theme code.

Each accent is a **triple**: `start` / `end` (a gradient pair) plus `glow` (a translucent version used for soft halos).

### First-class themes

| Theme | Status | Description |
| :--- | :--- | :--- |
| **Cyber Forest** *(the default, "Refined Cyber-Ink")* | Shipped | Ink-rich dark slate canvas, surgical teal primary, indigo secondary. Embedded in the app as the guaranteed fallback. |
| Terra Noir | Planned (Sprint 8) | Dark earth palette. |
| Linen | Planned (Sprint 8) | Clean light palette. |

> First-class themes are bundled and always selectable. The schema and everything in this guide applies equally to first-class and user-authored themes.

---

## 2. Token schema reference

Every theme is a JSON object. The table below lists **every token the validator requires** (both `modes.dark` and `modes.light` must define the full set), plus the optional typography fields. The "JSON path" is relative to a `mode` object (e.g. `modes.dark.bg.void`).

> This table mirrors `requiredTokens` in `backend/themes/validate.go`. The two are kept in sync by hand: when you add a token to the schema, add a row here **and** an entry there. (There is no automated coupling today — the doc is the author-facing reference, the Go slice is the enforcement.)

### Identity (top-level, not per-mode)

| Field | Required | Meaning |
| :--- | :--- | :--- |
| `schema_version` | yes | Schema version string. Currently `"1.0.0"`. Informational / forward-compatible — a higher version whose token set still matches v1 keeps working. |
| `id` | yes | Unique identifier, lowercase `[a-z0-9_-]`. Used as the filename on disk and the picker key. |
| `name` | yes | Human-readable display name. |
| `author` | optional | Author credit. |
| `description` | optional | One-line description shown in the picker. |

### `bg` — canvas / background scale

| JSON path | CSS variable | Meaning | Format |
| :--- | :--- | :--- | :--- |
| `bg.void` | `--bg-void` | The deepest canvas. Also seeds the native window background (the pre-CSS paint color). | color |
| `bg.surface` | `--bg-surface` | Raised surface (cards, inputs). | color |
| `bg.panel` | `--bg-panel` | Panels and sidebars. | color |
| `bg.hover` | `--bg-hover` | Hovered-row background. | color |
| `bg.active` | `--bg-active` | Pressed / active-row background. | color |

### `border` — hairline isolation

| JSON path | CSS variable | Meaning | Format |
| :--- | :--- | :--- | :--- |
| `border.muted` | `--border-muted` | Faintest divider. | color |
| `border.zinc` | `--border-zinc` | Standard hairline. | color |
| `border.active` | `--border-active` | Emphasized border (hovered). | color |
| `border.focus` | `--border-focus` | Focus-trace border. | color |

### `text` — foreground type scale

| JSON path | CSS variable | Meaning | Format |
| :--- | :--- | :--- | :--- |
| `text.primary` | `--text-primary` | Body copy; highest-contrast text. | color |
| `text.muted` | `--text-muted` | Metadata, labels, secondary text. | color |
| `text.disabled` | `--text-disabled` | Disabled / struck-through text. | color |

### `accent` — semantic accents (×2 triples)

| JSON path | CSS variable | Meaning | Format |
| :--- | :--- | :--- | :--- |
| `accent.primary.start` | `--accent-primary-start` | "go/done" gradient start. | color |
| `accent.primary.end` | `--accent-primary-end` | "go/done" gradient end. | color |
| `accent.primary.glow` | `--accent-primary-glow` | "go/done" soft halo (usually `rgba(...)`). | color |
| `accent.secondary.start` | `--accent-secondary-start` | "in-progress" gradient start. | color |
| `accent.secondary.end` | `--accent-secondary-end` | "in-progress" gradient end. | color |
| `accent.secondary.glow` | `--accent-secondary-glow` | "in-progress" soft halo. | color |

### `status` — warn / danger

| JSON path | CSS variable | Meaning | Format |
| :--- | :--- | :--- | :--- |
| `status.warn` | `--status-warn` | Warnings. | color |
| `status.danger` | `--status-danger` | Errors / destructive. | color |

### `typography` (optional, theme-level — not per-mode)

When absent, the theme inherits fonts from your [`config.yaml`](../SPECS.md) `editor.*` settings. When present, each non-empty field overrides the corresponding config font via a CSS fallback chain.

| JSON path | CSS variable | Meaning | Format |
| :--- | :--- | :--- | :--- |
| `typography.font_family` | `--font-body` | Body / proportional font stack. | font-family |
| `typography.mono_font_family` | `--font-mono` | Monospace font stack. | font-family |
| `typography.headline_font` | `--font-headline` | Heading font stack. | font-family |

### Accepted value formats

- **Colors** (every `bg`/`border`/`text`/`accent`/`status` slot): `#hex` (`#rgb`, `#rrggbb`, `#rrggbbaa`), `rgb(r, g, b)`, or `rgba(r, g, b, a)`. Anything else — named colors (`red`), `hsl()`, `url()`, `expression()`, `<script>` — is **rejected at validation time**. This is the security sandbox: a user-imported theme can never smuggle executable CSS.
- **Font-family** (`typography.*`): any CSS font-family string (e.g. `'Inter', sans-serif`). Rejected if it contains `;`, `{`, `}`, `<`, `>`, or `\` (prevents breaking out of the `:root{--name:value;}` injection context).

---

## 3. Authoring a theme

### A minimal valid theme

The smallest theme that passes validation — both modes, every required token. Copy this, change the `id`/`name`, and swap colors:

```json
{
  "schema_version": "1.0.0",
  "id": "my-theme",
  "name": "My Theme",
  "modes": {
    "dark": {
      "bg":      { "void": "#0c0c0e", "surface": "#121215", "panel": "#161619", "hover": "#1c1c21", "active": "#222226" },
      "border":  { "muted": "#1e1e23", "zinc": "#27272a", "active": "#3f3f46", "focus": "#52525b" },
      "text":    { "primary": "#dee3e6", "muted": "#8b8b94", "disabled": "#4b5563" },
      "accent":  {
        "primary":   { "start": "#2dd4bf", "end": "#0d9488", "glow": "rgba(20,184,166,0.15)" },
        "secondary": { "start": "#6366f1", "end": "#a855f7", "glow": "rgba(168,85,247,0.12)" }
      },
      "status":  { "warn": "#fbbf24", "danger": "#f43f5e" }
    },
    "light": {
      "bg":      { "void": "#f8fafc", "surface": "#ffffff", "panel": "#f1f5f9", "hover": "#e2e8f0", "active": "#cbd5e1" },
      "border":  { "muted": "#e2e8f0", "zinc": "#cbd5e1", "active": "#94a3b8", "focus": "#64748b" },
      "text":    { "primary": "#0f172a", "muted": "#4d5667", "disabled": "#94a3b8" },
      "accent":  {
        "primary":   { "start": "#0d9488", "end": "#115e59", "glow": "rgba(13,148,136,0.10)" },
        "secondary": { "start": "#4f46e5", "end": "#7c3aed", "glow": "rgba(79,70,229,0.08)" }
      },
      "status":  { "warn": "#d97706", "danger": "#e11d48" }
    }
  }
}
```

### A full annotated theme (with typography)

```json
{
  "schema_version": "1.0.0",            // required; currently "1.0.0"
  "id": "ocean-dusk",                   // required; lowercase [a-z0-9_-]; becomes the filename
  "name": "Ocean Dusk",                 // required; shown in the picker
  "author": "Your Name",                // optional
  "description": "Deep blue with a coral primary.",  // optional
  "typography": {                       // optional; theme-level (not per-mode)
    "font_family": "'Inter', sans-serif",
    "mono_font_family": "'JetBrains Mono', monospace",
    "headline_font": "'Hanken Grotesk', sans-serif"
  },
  "modes": {
    "dark": {
      "bg":      { "void": "#0a0f1a", "surface": "#0f1626", "panel": "#141d30", "hover": "#1a2438", "active": "#222d44" },
      "border":  { "muted": "#161f30", "zinc": "#1f2b40", "active": "#2f3f5a", "focus": "#3f5275" },
      "text":    { "primary": "#e6ecf5", "muted": "#7e8aa0", "disabled": "#4a5468" },
      "accent":  {
        "primary":   { "start": "#fb7185", "end": "#e11d48", "glow": "rgba(244,63,94,0.15)" },
        "secondary": { "start": "#38bdf8", "end": "#0ea5e9", "glow": "rgba(56,189,248,0.12)" }
      },
      "status":  { "warn": "#fbbf24", "danger": "#f43f5e" }
    },
    "light": {
      "bg":      { "void": "#f1f5f9", "surface": "#ffffff", "panel": "#e8eef5", "hover": "#dde6f0", "active": "#cbd5e1" },
      "border":  { "muted": "#dde6f0", "zinc": "#cbd5e1", "active": "#94a3b8", "focus": "#64748b" },
      "text":    { "primary": "#0f172a", "muted": "#475569", "disabled": "#94a3b8" },
      "accent":  {
        "primary":   { "start": "#e11d48", "end": "#9f1239", "glow": "rgba(225,29,72,0.10)" },
        "secondary": { "start": "#0284c7", "end": "#075985", "glow": "rgba(2,132,199,0.08)" }
      },
      "status":  { "warn": "#d97706", "danger": "#be123c" }
    }
  }
}
```

---

## 4. Choosing & mapping accents

1. **Pick two hues.** One for the "go/done" primary, one for the "in-progress" secondary. They should be visually distinct so the two states never blur together.
2. **Give each a gradient pair (`start` → `end`).** `start` is the brighter/lighter end; `end` is the deeper end. Components draw `linear-gradient(to bottom right, start, end)`.
3. **Make a matching `glow`.** The glow is the same hue at low alpha (≈0.08–0.15), used for soft halos behind active elements. Use `rgba(...)` so you can control transparency.
4. **Mind the mode.** In light mode, accent `start`s are usually *deeper* (so they stay readable on white); in dark mode, *brighter* (so they glow on dark). Compare Cyber Forest's dark `#2dd4bf` vs light `#0d9488` for the same primary.
5. **`primary` should pass AAA against `bg.void`.** See accessibility below.

---

## 5. Accessibility & contrast

Silt targets **WCAG 2.2**. Your theme is checked against the shipped palette; aim for:

| Element | Minimum ratio | Level |
| :--- | :--- | :--- |
| `text.primary` on `bg.void` / `bg.surface` / `bg.panel` | **≥ 7:1** | AAA |
| `text.muted` / `text.disabled` on backgrounds | **≥ 4.5:1** | AA |
| `accent.primary.start` / `accent.secondary.start` on `bg.void` (non-text UI) | **≥ 3:1** | AA (non-text) |

### How to verify

- **In-repo harness:** `backend/themes/contrast.go` computes WCAG contrast ratios (`ContrastRatio(a, b)`); `backend/themes/contrast_test.go` asserts the thresholds above for every shipped first-class theme mode. Drop your theme's colors into a quick ratio check using the same formula.
- **Browser devtools:** inspect any text element → computed color → the accessibility panel reports the contrast ratio against its background.
- **Online tools:** [WebAIM Contrast Checker](https://webaim.org/resources/contrastchecker/), [APCA Contrast Calculator](https://www.myndex.com/APCA/).

> A theme that fails contrast will still *import* (validation checks structure/format, not perceptual contrast) but will be hard to read. The contrast tests guard the **shipped** first-class themes; treat the thresholds above as your authoring target.

---

## 6. Importing a theme

You have a valid `my-theme.json`. There are three ways to add it; all end with the theme appearing in **Settings → Appearance** immediately (no restart):

### Option A — the picker Import button
1. Open **Settings → Appearance**.
2. Click **Import .json**.
3. Select your `.json` in the native file dialog.

### Option B — drag & drop
Drag the `.json` file from your file manager onto the **Appearance** tab. It imports through the same validated path.

### Option C — drop it into the vault
Copy the file directly into `<your-vault>/.system/themes/`. It is enumerated the next time the picker loads (the listing re-fetches on the `themes:changed` event).

### What the importer does for you
- **Validates** the file against the canonical schema. If a token is missing or a color is malformed, **nothing is written** and the error names the offending field and the expected format.
- **Sanitizes the `id`** to lowercase `[a-z0-9_-]` so it is filename-safe on every platform (underscores preserved).
- **Namespaces collisions:**
  - If your `id` collides with a built-in (e.g. `cyber_forest`), it is renamed to `user-cyber_forest` so the bundled default is never overwritten.
  - If `user-cyber_forest` already exists too, a counter is appended (`user-cyber_forest-2`, …).
  - If your `id` already exists as a *different* on-disk theme, the import is **rejected** (rename the `id` in your JSON and try again) — Silt never silently overwrites a different theme.
- **Sandbox:** because the schema only accepts `#hex` / `rgb()` / `rgba()` at color slots and font-family strings are stripped of CSS-breaking characters, a hostile theme cannot smuggle `<script>`, `url()`, or `expression()` past validation.

### Exporting (for round-trip editing)
Click **Export active** in **Settings → Appearance** to save the currently-active theme (or the embedded default) to a `.json` you can edit and re-import. This is the fastest way to start a new theme: export the default, tweak the colors, change the `id`, and import.

---

## 7. Selecting a theme & mode

1. Open **Settings → Appearance** (the gear icon in the titlebar, or Settings in the sidebar footer).
2. **Theme list:** click a row (or focus it with the keyboard and press Enter/Space) to make it active. The whole shell repaints in one frame — no reload.
3. **Mode toggle:** Dark / Light / System. System follows your OS appearance preference live. Changing the mode **does not** change the active theme.
4. **Live preview:** hover (or keyboard-focus) a non-active row to preview it without committing; move away or press Esc to restore the active theme.
5. **Persistence:** your selection and mode are saved to your user-global `settings.json` and restored on the next launch.

### Keyboard shortcuts (picker)
- **Tab** into the picker; **Arrow ↑/↓/←/→** move focus between rows.
- **Home / End** jump to the first / last row.
- **Enter / Space** select the focused row.
- **Esc** cancel any live preview.

---

## 8. Troubleshooting

| Symptom | Cause & fix |
| :--- | :--- |
| **"token is missing"** on import | A required token (see §2) is empty or absent in one of the modes. The error names the field, e.g. `modes.light.accent.primary.start`. Fill it in both `dark` and `light`. |
| **"not a valid color: …"** | A color slot holds a value that isn't `#hex`/`rgb()`/`rgba()` — e.g. a named color (`red`), `hsl()`, or a typo. Use hex. |
| **"theme id already exists"** | A theme with the same `id` is already on disk (a *different* theme, not a built-in). Change the `id` in your JSON and re-import. |
| **Imported as `user-<id>` (renamed)** | Your `id` collided with a built-in (`cyber_forest`). The importer namespaced it for you; the status line says "Imported as user-cyber_forest (renamed from cyber_forest)". |
| **"id … is invalid after sanitization"** | Your `id` consisted entirely of invalid characters. Use lowercase letters, digits, hyphens, and underscores. |
| **Theme not appearing in the list** | (a) The file isn't a `.json` in `<vault>/.system/themes/`. (b) It failed validation — check the load errors surfaced in the picker. (c) You're looking before the `themes:changed` event fired — reopen Settings. |
| **Typography fonts not applying** | The `typography` section is optional and theme-level. If you omitted it, the config `editor.*` fonts remain in effect. If you set a field but see no change, confirm the font is installed on your system (themes reference fonts by name; they don't bundle them). |
| **First-paint flash of the wrong color on restart** | The native window background is seeded from the active theme's `bg.void` at launch via an mtime-aware cache. If you hand-edited the on-disk file, touch its mtime or re-import so the cache refreshes. |

---

## 9. Appendix: blank theme template

Copy-paste this and fill in the `…` placeholders. Both modes are required; the `typography` block is optional (delete it to inherit config fonts).

```json
{
  "schema_version": "1.0.0",
  "id": "your-theme-id",
  "name": "Your Theme Name",
  "author": "Your Name",
  "description": "A short description.",
  "typography": {
    "font_family": "'Inter', sans-serif",
    "mono_font_family": "'JetBrains Mono', monospace",
    "headline_font": "'Hanken Grotesk', sans-serif"
  },
  "modes": {
    "dark": {
      "bg":      { "void": "#………", "surface": "#………", "panel": "#………", "hover": "#………", "active": "#………" },
      "border":  { "muted": "#………", "zinc": "#………", "active": "#………", "focus": "#………" },
      "text":    { "primary": "#………", "muted": "#………", "disabled": "#………" },
      "accent":  {
        "primary":   { "start": "#………", "end": "#………", "glow": "rgba(…,…,…,0.15)" },
        "secondary": { "start": "#………", "end": "#………", "glow": "rgba(…,…,…,0.12)" }
      },
      "status":  { "warn": "#………", "danger": "#………" }
    },
    "light": {
      "bg":      { "void": "#………", "surface": "#………", "panel": "#………", "hover": "#………", "active": "#………" },
      "border":  { "muted": "#………", "zinc": "#………", "active": "#………", "focus": "#………" },
      "text":    { "primary": "#………", "muted": "#………", "disabled": "#………" },
      "accent":  {
        "primary":   { "start": "#………", "end": "#………", "glow": "rgba(…,…,…,0.10)" },
        "secondary": { "start": "#………", "end": "#………", "glow": "rgba(…,…,…,0.08)" }
      },
      "status":  { "warn": "#………", "danger": "#………" }
    }
  }
}
```
