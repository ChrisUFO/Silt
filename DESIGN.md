Design Specification: Silt

Core Design System, Component Tokens, & Interaction Specification

1. Design Vision: Refined Cyber-Ink

Most digital workspace applications fall into one of two visual extremes: flat, sterile minimalism that feels clinical (e.g., default note-taking apps) or over-saturated, high-contrast neon layouts that induce cognitive fatigue during multi-hour reading/writing sessions.

Silt implements "Refined Cyber-Ink"—a design framework engineered for deep, distraction-free focus:

Ink-Rich Canvas: The interface relies on an ultra-dark slate base (#0c0c0e) and dark charcoal panels (#121215). This mimics high-grade dark paper, absorbing light emission to protect eyes on OLED, mini-LED, and high-brightness displays.

Surgical Accents: Highly saturated color gradients are constrained to less than 3% of the active viewport area. They act as glowing signposts (for checkboxes, keyboard navigation path markers, and active selection guides). The teal accent sits in the teal-400 → teal-600 range (rather than a fully-saturated sky/cyan) so it stays readable across long sessions without inducing visual fatigue; the indigo "in-progress" gradient remains one notch more vivid so the active state still draws the eye.

Hairline Isolation: Visual boundaries use absolute $1\text{px}$ lines with dark metallic borders instead of heavy box-shadow offsets, maintaining a clean, structured appearance.

2. Design System Tokens (Semantic & Raw)

This token set maps directly to our Go configuration runtime and Svelte theme-injection components. These variables translate to dark/light-mode variables dynamically.

2.1 Color Tokens Schema (Cyber Forest — the default / primary theme)

The canonical theme schema is modes-based (`modes.dark` / `modes.light`) with hue-agnostic **semantic accent tokens**. Components reference only the semantic accents (`--accent-primary-*` = the "go / done" hue, `--accent-secondary-*` = the "in progress" hue); each theme maps its concrete hues onto them. This is the single source of truth shared by the Go theme loader (`backend/themes`), the runtime CSS injector, and `cyber_forest.json`. **Cyber Forest is the default and primary theme** (embedded as the guaranteed fallback); the additional first-class palettes in §2.2 are alternates.

{
  "schema_version": "1.0.0",
  "id": "cyber_forest",
  "name": "Cyber Forest",
  "author": "System Designer",
  "description": "...",
  "modes": {
    "dark": {
      "bg": {
        "void": "#0c0c0e",
        "surface": "#121215",
        "panel": "#161619",
        "hover": "#1c1c21",
        "active": "#222226"
      },
      "border": {
        "muted": "#1e1e23",
        "zinc": "#27272a",
        "active": "#3f3f46",
        "focus": "#52525b"
      },
      "text": {
        "primary": "#dee3e6",
        "muted": "#8b8b94",
        "disabled": "#4b5563"
      },
      "accent": {
        "primary": {
          "start": "#2dd4bf",
          "end": "#0d9488",
          "glow": "rgba(20, 184, 166, 0.15)"
        },
        "secondary": {
          "start": "#6366f1",
          "end": "#a855f7",
          "glow": "rgba(168, 85, 247, 0.12)"
        }
      },
      "status": {
        "warn": "#fbbf24",
        "danger": "#f43f5e"
      }
    },
    "light": {
      "bg": {
        "void": "#f8fafc",
        "surface": "#ffffff",
        "panel": "#f1f5f9",
        "hover": "#e2e8f0",
        "active": "#cbd5e1"
      },
      "border": {
        "muted": "#e2e8f0",
        "zinc": "#cbd5e1",
        "active": "#94a3b8",
        "focus": "#64748b"
      },
      "text": {
        "primary": "#0f172a",
        "muted": "#4d5667",
        "disabled": "#94a3b8"
      },
      "accent": {
        "primary": {
          "start": "#0d9488",
          "end": "#115e59",
          "glow": "rgba(13, 148, 136, 0.10)"
        },
        "secondary": {
          "start": "#4f46e5",
          "end": "#7c3aed",
          "glow": "rgba(79, 70, 229, 0.08)"
        }
      },
      "status": {
        "warn": "#d97706",
        "danger": "#e11d48"
      }
    }
  }
}


**Token usage convention (when to reach for `--color-text-primary` vs `--accent-primary-*`).**
The "Surgical Accents" doctrine above (accents are *signposts*, < 3% of the
viewport) decides which token a given element binds to:

- **`--color-text-primary`** → plain text chrome: wordmarks, headings, static labels,
  and body copy. These must follow each theme's body-text hue so a theme switch
  is visibly perceptible everywhere text appears.
- **`--accent-primary-*`** → signposts and interactive/selected state only:
  focusable icons, active-tab indicators, selected listbox rows, CTAs, links,
  focus rings, and breadcrumb "you are here" markers.

The split matters because three first-class themes share **cool** accents
(Cyber Forest teal, Graphite blue, Linen slate-blue) — if a prominent label
like the wordmark or the active-notebook header is bound to the accent token,
switching between those themes barely shifts its hue and the theme change reads
as inert even though the palette swapped correctly (#138). Binding such
elements to `--color-text-primary` surfaces each theme's distinct body-text color
(neutral white / warm oatmeal / cool blue-gray) in the most eye-catching chrome.
The brand `<img>` logo (not the wordmark text) carries the brand identity; the
accent token is never used as a decorative text color.


2.2 First-Class Theme Palettes

Silt ships a curated set of first-class themes alongside the default. Each is a plain JSON file embedded in the binary (so it is always selectable, even before a vault exists or when the themes directory is wiped) and written to `<vault>/.system/themes/` by `ScaffoldVault` so it is editable on disk. All consume the same canonical schema and semantic accents from §2.1 — **no per-theme component code exists**; switching themes only changes the injected CSS custom-property values.

| Theme | `id` | Character |
| :--- | :--- | :--- |
| Cyber Forest *(default / primary)* | `cyber_forest` | Ink-rich dark slate, surgical teal primary, indigo secondary. |
| Terra Noir | `silt-terra-noir` | Warm dark earth: clay primary, moss secondary. |
| Linen | `silt-linen` | Woven linen paper: warm grey-taupe canvas + woven-grain texture, slate-blue + muted lilac. |
| Stark | `silt-stark` | High-contrast / accessibility (WCAG AAA): pure black/white extremes, gold + cyan. |
| Graphite | `silt-graphite` | Calm true-neutral monochrome: pure gray canvas, single restrained blue accent, neutral-steel secondary. |

Every first-class theme ships both dark and light variants and its own `typography` pairing: Cyber Forest (Plus Jakarta Sans / JetBrains Mono / Hanken Grotesk — the default), Terra Noir (Source Serif 4 / IBM Plex Mono / Newsreader — warm editorial), Linen (Mulish / Fira Code / Sora — soft clean), Stark (Atkinson Hyperlegible / Geist Mono — the Braille Institute low-vision font, for the AAA theme), Graphite (Geist / Geist Mono / Schibsted Grotesk — developer aesthetic). The palettes below document the color design intent; the authoritative source for each value is the JSON in `backend/themes/themes/` (the contrast harness in `backend/themes/contrast_test.go` guards WCAG for every mode variant).

2.2.1 Terra Noir — warm dark earth

A dark earth palette: warm near-black canvas with **clay/terracotta** primary (selection guides, active focus, completed checks) and **moss** secondary (in-progress / DOING indicator, metadata chips). Intent: a warmer, organic counterpart to Cyber Forest's cool slate, for users who prefer earth tones over cyber neons.

- Dark: `bg.void #100b07` (warm near-black); `text.primary #ece3d5` (warm white); `accent.primary #e07a3c → #b4421a` (clay); `accent.secondary #84a04a → #5e7d2f` (moss).
- Light: `bg.void #f6efe4` (warm paper); `text.primary #2a2014`; `accent.primary #c2511f → #9a3a14`; `accent.secondary #5a7d2a → #44611d`.
- Tuning: dark `text.muted #8a7860 → #a89478` to clear WCAG AA (4.5:1) on `bg.active` — the binding constraint in dark mode is muted text on the lightest dark surface.

2.2.2 Linen — woven linen paper

A soft, low-chroma palette modeled on natural linen: a warm grey-taupe canvas in dark mode (the authentic flax/oatmeal tone — grey-dominant with a whisper of warmth, never brown) and warm paper in light, both carrying a subtle **woven-thread + paper-grain texture** overlay (Linen is the only first-class theme that declares a `texture` block; see §2.1). `primary` = muted **slate-blue** (reads as faded fountain-pen ink on paper), `secondary` = muted **lilac**. Intent: long-session comfort — a calm, tactile "paper" surface distinct from Cyber Forest's cool slate and Graphite's flat monochrome.

- Dark: `bg.void #242220` (warm grey-taupe); `text.primary #e8e3d8` (oatmeal-white); `accent.primary #7fb3c4 → #5d97ab`; `accent.secondary #a8a3d4 → #847cb0`; `texture` overlay = light-thread linen weave + grayscale grain, `overlay` blend, opacity 0.08.
- Light: `bg.void #faf6ef` (warm paper, not pure white); `text.primary #2b2a27`; `accent.primary #4a8a9c → #3a7383`; `accent.secondary #686da3 → #565b8e`; `texture` overlay = dark-thread weave + grain, `multiply` blend, opacity 0.10.
- Tuning: dark `text.muted → #b9b0a1` (warm grey) to clear AA on Linen's surfaces.

2.2.3 Stark — high-contrast / accessibility (WCAG AAA)

A first-class accessibility theme targeting **WCAG 2.2 AAA** (≥7:1 body text). Pure black/white extremes (21:1), **border-led structure** (because the near-uniform background can't separate panels by fill alone), and maximum-visibility accents: **gold/amber** primary and **cyan** secondary. Intent: an out-of-box option for low-vision and bright-environment users, rather than relying on them authoring a custom theme.

- Dark: `bg.void #000000`; `text.primary #ffffff` (21:1); `border.active #ffffff` / `border.focus #ffd400` (vivid gold focus rings); `accent.primary #ffd400 → #ffb800`; `accent.secondary #00e5ff → #00b8d4`.
- Light: `bg.void #ffffff`; `text.primary #000000` (21:1); `border.active #000000` / `border.focus #0000cc`; `accent.primary #8a5a00 → #6f4800`; `accent.secondary #005f70 → #00475a`.
- Exempt / decorative tokens (not WCAG-essential): the `*-glow` halos and `text.disabled`. Focus states are unmistakable in both modes (≥3:1 against adjacent colors per WCAG 2.4.11 / 1.4.11), asserted in the contrast harness.

2.2.4 Graphite — calm monochrome / true-dark

For users who find Cyber Forest *too colorful*. Graphite is a **true neutral monochrome**: pure neutral-gray surfaces (zero blue tint, unlike Cyber Forest's blue slate) with a **single restrained blue** accent as the only color and a **neutral steel** secondary. Neutral-white text (`#ebebeb`) reads distinctly cleaner/warmer than Cyber Forest's cool `#dee3e6`. Intent: the "developer dark" / "dimmed" aesthetic — a calm, flat, low-chroma surface. Comfortable AAA contrast, **not** the extreme contrast of Stark.

- Dark: `bg.void #0a0a0a` (true near-black, neutral); `text.primary #ebebeb`; `accent.primary #6f9ad8 → #4d72a0` (restrained blue); `accent.secondary #9aa3ad → #6f7882` (neutral steel).
- Light: `bg.void #f8f8f8`; `text.primary #1a1a1a`; `accent.primary #4a6fa0 → #374f78`; `accent.secondary #6a737d → #525a63`.
- Distinctness: primary (blue) and secondary (neutral steel) differ in both hue and chroma so go/done and in-progress never blur, while the overall surface stays a calm flat monochrome.


3. Typography & Spacing Rhythm

3.1 Proportional Scaling & Hierarchy

To preserve natural visual hierarchy across deeply indented outliner structures, text elements use the following proportional sizes:

Primary Body Copy: 14px (0.875rem) — optimized for code and technical note-taking readability.

Heading 1 (#): 24px (1.5rem) | Line-Height: 1.3 | Weight: Bold (700)

Heading 2 (##): 18px (1.125rem) | Line-Height: 1.4 | Weight: Semi-Bold (600)

Default Bullet Block: 14px (0.875rem) | Line-Height: 1.6 | Weight: Regular (400)

Monospace Metadata / Shortcuts: 12px (0.75rem) | Line-Height: 1.0 | Weight: Regular (400)

3.2 Indentation Grid Scales

The indent spacing scale matches the indentation depths of the hierarchy blocks:

$$\text{Padding-Left} = L \times 24\text{px}$$

Where $L$ represents the absolute nesting depth level (e.g., Level 0 = 0px, Level 1 = 24px, Level 2 = 48px, Level 3 = 72px).

Line Height Constraint: Every block features a native py-1 ($4\text{px}$ top/bottom) padding window, giving a total block-to-block baseline vertical distance of $28\text{px}$ at $14\text{px}$ text sizes.

4. UI Component Specifications

4.1 The Task Checkpoint Component

Custom checkbox rendering mimics the structural rounded-corner boundaries (rx="16") of the Silt logo.

       [ ] TODO                    [/] DOING                   [x] DONE
   ┌───────────────┐           ┌───────────────┐           ┌───────────────┐
   │               │           │    ┌─────┐    │           │    \     /    │
   │               │           │    │  /  │    │           │     \   /     │
   │               │           │    └─────┘    │           │      \ /      │
   └───────────────┘           └───────────────┘           └───────────────┘
       Border: zinc-400            Border: indigo-500          Border: teal-500
   BG: --color-surface            BG: --color-surface            BG: --color-accent-primary-glow
                               Inside: secondary-grad      Inside: primary-check SVG


Token Rules

Inactive State (TODO):

Border: var(--color-border-zinc)

Background: var(--color-surface)

Hover Transition: border-color 150ms ease, box-shadow 150ms ease

Hover Style: Border: var(--color-accent-primary-start), Glow: 0 0 8px var(--color-accent-primary-glow)

In-Progress State (DOING):

Border: linear-gradient(to bottom right, var(--color-accent-secondary-start), var(--color-accent-secondary-end))

Content: Inner indicator square rotated $12^\circ$ to match the logo slant (M 28,14 L 20,50).

Completed State (DONE):

Border: var(--color-accent-primary-end)

Background: var(--color-accent-primary-glow)

Content: SVG checkmark colored in var(--color-accent-primary-start). Text within the block is struck through and shifted to color var(--color-text-disabled).

4.2 Dynamic Guideline Guide Rails

To prevent visual disorientation in deeply nested lists, the vertical guidelines highlight active parent-child hierarchies.

 - [ ] Root Task Element
 |   - [ ] Sub-Task Level 1
 |   |   - [/ ] Active Focused Block Node  <-- Highlight active guide rails
 |   - [ ] Unfocused Block Node            <-- Fallback guide rail


Standard Guide Rail: Width: $1\text{px}$ solid, offset by $-12\text{px}$ to the left of the child text node. Color: var(--color-border-muted).

Active Ancestral Path Guide Rail: Width: $1.5\text{px}$ solid. Undergoes color-blend shift to linear-gradient(to bottom, var(--color-accent-primary-start), var(--color-accent-primary-end)) when a child node receives active keyboard or mouse focus.

Path-Trace Duration: 250ms cubic-bezier(0.16, 1, 0.3, 1).

4.3 Inline Tag & Metadata Chips

Metadata tags are styled as low-contrast, highly readable pills to prevent cluttering block logs:

Owner Chip ([Chris]):

Typography: Monospace font stack, 0.75rem.

Style: Background: rgba(99, 102, 241, 0.08), Border: 1px solid rgba(99, 102, 241, 0.20), Color: #a5b4fc.

Priority Chip (Critical / #1):

Style: Background: rgba(244, 63, 94, 0.08), Border: 1px solid rgba(244, 63, 94, 0.30), Color: #fca5a5, Font-Weight: 700.

Priority Chip (Low / #3):

Style: Background: var(--color-panel), Border: 1px solid var(--color-border-zinc), Color: var(--color-text-muted).

4.4 Glassmorphism Contextual Menu

The slash command menu uses clear, frosted glass visual styling, maintaining background spatial context when triggered inline:

.command-palette {
  background-color: rgba(22, 22, 25, 0.75);
  border: 1px solid var(--color-border-active);
  border-radius: 8px;
  backdrop-filter: blur(12px) saturate(140%);
  -webkit-backdrop-filter: blur(12px) saturate(140%);
  box-shadow: 
    0 10px 25px -5px rgba(0, 0, 0, 0.50),
    0 0 15px rgba(99, 102, 241, 0.04);
}


5. Interaction States & Dynamic Feedback

Every component in Silt implements distinct states to provide clear feedback during mouse, keyboard, or touch-screen interaction:

Component

Default State

Hover State

Focus State

Active / Clicked State

Document Block Line

Transparent background, standard guide rails

Light background highlight (var(--color-hover)), shows line grab icon

var(--color-surface), guideline color transitions to the primary accent

N/A

Checklist Toggle

var(--color-border-zinc) border

var(--color-accent-primary-start) border, subtle glow

Standard glow ring

Transitions status to next cycle

Kanban Task Card

var(--color-panel) base, no offset

var(--color-hover) base, $1\text{px}$ upward translate

Highlighted outer border

Rotate $2^\circ$, add shadow layer on drag

6. Motion Specification & Micro-Animations

The UI avoids heavy or slow animations, keeping all transitions under $220\text{ms}$ to ensure the app feels fast and highly responsive.

Transitions Easing Curve: cubic-bezier(0.16, 1, 0.3, 1) (Ultra-smooth Exponential Out).

Hover Interaction Transitions: Duration: 120ms for color changes and layout shifts.

Command Menu Initialization: Scale transition from 0.97 to 1.0 combined with opacity fade-in. Duration: 100ms.

Kanban Card Drag-Reorder: Uses compile-time svelte/animate (using Svelte's native flip transition mechanics). Duration: 200ms with linear-out motion.

7. Dynamic Theme Injection Runtime

To support user-defined styling, Silt implements a runtime CSS Custom Property injector. The active theme and dark/light mode are persisted in AppSettings (user-global settings.json) and resolved over the Wails IPC bridge; a Svelte theme store injects the resolved token map onto :root in a single paint frame. The token map includes both color tokens (--bg-*, --border-*, --text-*, --accent-*, --status-*) and, when the theme defines them, typography tokens (--font-headline, --font-body, --font-mono). Typography is theme-level (not per-mode): each theme can optionally declare font-family choices that override the config-driven editor.* defaults via CSS fallback chains in index.css (e.g. `var(--font-body, var(--editor-font-family), 'Plus Jakarta Sans', sans-serif)`). Themes without a typography section are fully backward compatible — the config values remain in effect.

```
                +------------------------------------------+
                | Go: backend/themes (embed.FS default +    |
                |     on-disk *.json loader + validator)    |
                +------------------------------------------+
                                   │  ListThemes / GetActiveTheme / ApplyTheme
                                   ▼  [ActiveThemeResult: dark+light token maps]
                +------------------------------------------+
                |       Wails IPC Transport Layer           |
                +------------------------------------------+
                                   │
                                   ▼
+-----------------------------------------------------------------------+
| Svelte theme store (frontend/src/theme)                               |
|  - resolves "system" via prefers-color-scheme (both maps in hand)     |
|  - injectTokens: rewrites ONE <style id="silt-theme">:root{...} block |
|    (one DOM write -> one recalc -> same-tick repaint, no flicker)     |
|  - index.css :root values are startup fallbacks only                  |
+-----------------------------------------------------------------------+
```

A canonical default theme (cyber_forest) is embedded in the Go binary so the app always renders correctly before a vault exists or when the themes directory is wiped. The native webview BackgroundColour is resolved at launch from that embedded theme's `bg.void`, eliminating any pre-CSS flash that matches no token.

Custom Theme Import (Sprint 6, #48): users can import a theme JSON via the Settings → Appearance "Import" button or by dragging a `.json` onto the tab (Wails `OnFileDrop`). The backend validates against the canonical schema using the same `themes.ParseAndValidate` the loader uses, namespaces the id (`user-` prefix on a built-in id, counter suffix on repeat), and writes the file atomically. `themes.ValidationErrors` propagate over IPC so the UI names the offending token and the expected format. On success the backend emits the Wails event `themes:changed` (distinct from the active-theme event `theme:changed`); the frontend `themesState` listing re-fetches and the new theme appears in the picker without a restart. Sandbox by schema: the canonical schema accepts only color values (hex / rgb / rgba) at every token slot, so embedded `<script>`, `url()`, `expression()`, and named colors are rejected structurally before they reach disk.

User Theme Engine UX (Sprint 6, #47): Settings → Appearance is the single surface for theme selection. Mode is a `role="radiogroup"` of Dark / Light / System (changing mode never changes the active theme). Themes are a `role="listbox"` of `role="option"` rows with roving tabindex, Arrow/Home/End navigation, Enter/Space commit, and Esc to cancel any live preview. Swatches are data-driven from `ThemeInfo.Swatches` (no per-theme code branches). The picker renders a live preview on hover/focus by injecting the preview theme's tokens via the existing `injectTokens` path — restoring the active theme on `mouseleave`/`blur`/`Esc`. Errors and status updates flow through a `role="status" aria-live="polite"` region (escalating to `role="alert" aria-live="assertive"` for errors). The active id and mode persist across restarts via `AppSettings` (Sprint 5).

Page Template Picker (Sprint 9, #55): the template picker reuses the same modal chrome, Refined Cyber-Ink token system, and iconography rules as the theme picker. Iconography follows the Material Symbols convention; the `icon` frontmatter field is a Material Symbols name rendered at 18–20px. No emojis are used in first-class template icons — they are abstract, CSS-friendly glyphs. The picker is a centered overlay (`role="dialog"`, `aria-modal="true"`) with a category-grouped `role="listbox"`, roving tabindex (Arrow/Home/End/Enter), a live preview pane, a dynamic placeholder form, and a Tab focus trap. Entry points: the sidebar `content_copy` button + `Ctrl+Shift+T` (new page mode) and the `/template` slash command (insert mode).


8. Accessibility (A11Y) & Keyboard Navigation Compliance

Silt is built for complete hands-on-keyboard efficiency, complying directly with WCAG 2.2 AAA guidelines:

Contrast Ratios: Text-to-background contrast ratios are strictly maintained above 7:1 for primary elements, and above 4.5:1 for secondary tags.

Focus States: Every interactive element features an explicit :focus-visible outline ring of $2\text{px}$ var(--color-border-focus) offset by $1\text{px}$ to prevent overlapping with components.

Keyboard Navigation Paths: Users can navigate the entire interface using standard shortcut triggers:

Tab and Shift+Tab to shift indentation levels.

Up / Down Arrow keys to navigate blocks.

Enter to create a new parallel block.

/ to trigger the contextual palette list, with keyboard arrows used to select options and Enter to confirm.

ARIA Label Mapping: Task check elements feature explicit ARIA attributes updating in real-time based on state values:

TODO state features: aria-role="checkbox" aria-checked="false" aria-label="Task Toggle: Not Started".

DOING state features: aria-role="checkbox" aria-checked="mixed" aria-label="Task Toggle: In Progress".

DONE state features: aria-role="checkbox" aria-checked="true" aria-label="Task Toggle: Completed".