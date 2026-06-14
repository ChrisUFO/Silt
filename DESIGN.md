Design Specification: Silt

Core Design System, Component Tokens, & Interaction Specification

1. Design Vision: Refined Cyber-Ink

Most digital workspace applications fall into one of two visual extremes: flat, sterile minimalism that feels clinical (e.g., default Logseq or Obsidian) or over-saturated, high-contrast neon layouts that induce cognitive fatigue during multi-hour reading/writing sessions.

Silt implements "Refined Cyber-Ink"—a design framework engineered for deep, distraction-free focus:

Ink-Rich Canvas: The interface relies on an ultra-dark slate base (#0c0c0e) and dark charcoal panels (#121215). This mimics high-grade dark paper, absorbing light emission to protect eyes on OLED, mini-LED, and high-brightness displays.

Surgical Accents: Highly saturated color gradients are constrained to less than 3% of the active viewport area. They act as glowing signposts (for checkboxes, keyboard navigation path markers, and active selection guides). The teal accent sits in the teal-400 → teal-600 range (rather than a fully-saturated sky/cyan) so it stays readable across long sessions without inducing visual fatigue; the indigo "in-progress" gradient remains one notch more vivid so the active state still draws the eye.

Hairline Isolation: Visual boundaries use absolute $1\text{px}$ lines with dark metallic borders instead of heavy box-shadow offsets, maintaining a clean, structured appearance.

2. Design System Tokens (Semantic & Raw)

This token set maps directly to our Go configuration runtime and Svelte theme-injection components. These variables translate to dark/light-mode variables dynamically.

2.1 Color Tokens Schema

The canonical theme schema is modes-based (`modes.dark` / `modes.light`) with hue-agnostic **semantic accent tokens**. Components reference only the semantic accents (`--accent-primary-*` = the "go / done" hue, `--accent-secondary-*` = the "in progress" hue); each theme maps its concrete hues onto them. This is the single source of truth shared by the Go theme loader (`backend/themes`), the runtime CSS injector, and `cyber_forest.json`.

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
        "muted": "#71717a",
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
        "muted": "#64748b",
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
   BG: --bg-surface            BG: --bg-surface            BG: --accent-primary-glow
                               Inside: secondary-grad      Inside: primary-check SVG


Token Rules

Inactive State (TODO):

Border: var(--border-zinc)

Background: var(--bg-surface)

Hover Transition: border-color 150ms ease, box-shadow 150ms ease

Hover Style: Border: var(--accent-primary-start), Glow: 0 0 8px var(--accent-primary-glow)

In-Progress State (DOING):

Border: linear-gradient(to bottom right, var(--accent-secondary-start), var(--accent-secondary-end))

Content: Inner indicator square rotated $12^\circ$ to match the logo slant (M 28,14 L 20,50).

Completed State (DONE):

Border: var(--accent-primary-end)

Background: var(--accent-primary-glow)

Content: SVG checkmark colored in var(--accent-primary-start). Text within the block is struck through and shifted to color var(--text-disabled).

4.2 Dynamic Guideline Guide Rails

To prevent visual disorientation in deeply nested lists, the vertical guidelines highlight active parent-child hierarchies.

 - [ ] Root Task Element
 |   - [ ] Sub-Task Level 1
 |   |   - [/ ] Active Focused Block Node  <-- Highlight active guide rails
 |   - [ ] Unfocused Block Node            <-- Fallback guide rail


Standard Guide Rail: Width: $1\text{px}$ solid, offset by $-12\text{px}$ to the left of the child text node. Color: var(--border-muted).

Active Ancestral Path Guide Rail: Width: $1.5\text{px}$ solid. Undergoes color-blend shift to linear-gradient(to bottom, var(--accent-primary-start), var(--accent-primary-end)) when a child node receives active keyboard or mouse focus.

Path-Trace Duration: 250ms cubic-bezier(0.16, 1, 0.3, 1).

4.3 Inline Tag & Metadata Chips

Metadata tags are styled as low-contrast, highly readable pills to prevent cluttering block logs:

Owner Chip ([Chris]):

Typography: Monospace font stack, 0.75rem.

Style: Background: rgba(99, 102, 241, 0.08), Border: 1px solid rgba(99, 102, 241, 0.20), Color: #a5b4fc.

Priority Chip (Critical / #1):

Style: Background: rgba(244, 63, 94, 0.08), Border: 1px solid rgba(244, 63, 94, 0.30), Color: #fca5a5, Font-Weight: 700.

Priority Chip (Low / #3):

Style: Background: var(--bg-panel), Border: 1px solid var(--border-zinc), Color: var(--text-muted).

4.4 Glassmorphism Contextual Menu

The slash command menu uses clear, frosted glass visual styling, maintaining background spatial context when triggered inline:

.command-palette {
  background-color: rgba(22, 22, 25, 0.75);
  border: 1px solid var(--border-active);
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

Light background highlight (var(--bg-hover)), shows line grab icon

var(--bg-surface), guideline color transitions to the primary accent

N/A

Checklist Toggle

var(--border-zinc) border

var(--accent-primary-start) border, subtle glow

Standard glow ring

Transitions status to next cycle

Kanban Task Card

var(--bg-panel) base, no offset

var(--bg-hover) base, $1\text{px}$ upward translate

Highlighted outer border

Rotate $2^\circ$, add shadow layer on drag

6. Motion Specification & Micro-Animations

The UI avoids heavy or slow animations, keeping all transitions under $220\text{ms}$ to ensure the app feels fast and highly responsive.

Transitions Easing Curve: cubic-bezier(0.16, 1, 0.3, 1) (Ultra-smooth Exponential Out).

Hover Interaction Transitions: Duration: 120ms for color changes and layout shifts.

Command Menu Initialization: Scale transition from 0.97 to 1.0 combined with opacity fade-in. Duration: 100ms.

Kanban Card Drag-Reorder: Uses compile-time svelte/animate (using Svelte's native flip transition mechanics). Duration: 200ms with linear-out motion.

7. Dynamic Theme Injection Runtime

To support user-defined styling, Silt implements a runtime CSS Custom Property injector. The active theme and dark/light mode are persisted in AppSettings (user-global settings.json) and resolved over the Wails IPC bridge; a Svelte theme store injects the resolved token map onto :root in a single paint frame.

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


8. Accessibility (A11Y) & Keyboard Navigation Compliance

Silt is built for complete hands-on-keyboard efficiency, complying directly with WCAG 2.2 AAA guidelines:

Contrast Ratios: Text-to-background contrast ratios are strictly maintained above 7:1 for primary elements, and above 4.5:1 for secondary tags.

Focus States: Every interactive element features an explicit :focus-visible outline ring of $2\text{px}$ var(--border-focus) offset by $1\text{px}$ to prevent overlapping with components.

Keyboard Navigation Paths: Users can navigate the entire interface using standard shortcut triggers:

Tab and Shift+Tab to shift indentation levels.

Up / Down Arrow keys to navigate blocks.

Enter to create a new parallel block.

/ to trigger the contextual palette list, with keyboard arrows used to select options and Enter to confirm.

ARIA Label Mapping: Task check elements feature explicit ARIA attributes updating in real-time based on state values:

TODO state features: aria-role="checkbox" aria-checked="false" aria-label="Task Toggle: Not Started".

DOING state features: aria-role="checkbox" aria-checked="mixed" aria-label="Task Toggle: In Progress".

DONE state features: aria-role="checkbox" aria-checked="true" aria-label="Task Toggle: Completed".