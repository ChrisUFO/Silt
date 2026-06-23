// Preloaded font registry (#82) — the single source of truth for the fonts
// the font picker (Settings → General) offers and for the theme-typography
// override indicator (Settings → Appearance).
//
// Fonts are bundled offline via @fontsource (woff2 self-hosted by Vite — no
// Google Fonts CDN at runtime, preserving Silt's local-first guarantee). We
// import a bounded weight set (400/500/600/700, the weights the UI actually
// uses) per family, restricted to the `latin` subset, to keep the bundle
// small. Two families ship fewer weights by the type designer's intent:
// Atkinson Hyperlegible (Regular + Bold) and Space Mono (Regular + Bold).
//
// The full set is bundled (no online/downloadable "extension" pack): at
// ~4.5 MB of woff2 across 26 families the size is negligible for a local-
// first desktop app, and bundling everything keeps the app 100% offline and
// the architecture simple (no network/download feature). It includes every
// family the five first-class themes reference so every theme renders
// correctly out of box, plus popular UI/developer/editorial alternatives.
//
// Each entry's `cssFamily` is the value stored in `config.editor.font_family`
// / `mono_font_family` and the value injected as `--editor-font-family`. It is
// the bare family name (e.g. "Plus Jakarta Sans"), matching the config.yaml
// default convention; CSS accepts an unquoted multi-word family name as a
// sequence of identifiers. The picker renders each option in its own font by
// applying `style="font-family: <cssFamily>"`.

// --- Body / sans-serif ----------------------------------------------------
import '@fontsource/plus-jakarta-sans/latin-400.css'
import '@fontsource/plus-jakarta-sans/latin-500.css'
import '@fontsource/plus-jakarta-sans/latin-600.css'
import '@fontsource/plus-jakarta-sans/latin-700.css'
import '@fontsource/inter/latin-400.css'
import '@fontsource/inter/latin-500.css'
import '@fontsource/inter/latin-600.css'
import '@fontsource/inter/latin-700.css'
import '@fontsource/work-sans/latin-400.css'
import '@fontsource/work-sans/latin-500.css'
import '@fontsource/work-sans/latin-600.css'
import '@fontsource/work-sans/latin-700.css'
import '@fontsource/manrope/latin-400.css'
import '@fontsource/manrope/latin-500.css'
import '@fontsource/manrope/latin-600.css'
import '@fontsource/manrope/latin-700.css'
import '@fontsource/mulish/latin-400.css'
import '@fontsource/mulish/latin-500.css'
import '@fontsource/mulish/latin-600.css'
import '@fontsource/mulish/latin-700.css'
import '@fontsource/outfit/latin-400.css'
import '@fontsource/outfit/latin-500.css'
import '@fontsource/outfit/latin-600.css'
import '@fontsource/outfit/latin-700.css'
import '@fontsource/atkinson-hyperlegible/latin-400.css'
import '@fontsource/atkinson-hyperlegible/latin-700.css'
import '@fontsource/geist/latin-400.css'
import '@fontsource/geist/latin-500.css'
import '@fontsource/geist/latin-600.css'
import '@fontsource/geist/latin-700.css'
import '@fontsource/dm-sans/latin-400.css'
import '@fontsource/dm-sans/latin-500.css'
import '@fontsource/dm-sans/latin-600.css'
import '@fontsource/dm-sans/latin-700.css'
import '@fontsource/figtree/latin-400.css'
import '@fontsource/figtree/latin-500.css'
import '@fontsource/figtree/latin-600.css'
import '@fontsource/figtree/latin-700.css'
import '@fontsource/public-sans/latin-400.css'
import '@fontsource/public-sans/latin-500.css'
import '@fontsource/public-sans/latin-600.css'
import '@fontsource/public-sans/latin-700.css'
import '@fontsource/lexend/latin-400.css'
import '@fontsource/lexend/latin-500.css'
import '@fontsource/lexend/latin-600.css'
import '@fontsource/lexend/latin-700.css'

// --- Monospace ------------------------------------------------------------
import '@fontsource/jetbrains-mono/latin-400.css'
import '@fontsource/jetbrains-mono/latin-500.css'
import '@fontsource/jetbrains-mono/latin-600.css'
import '@fontsource/jetbrains-mono/latin-700.css'
import '@fontsource/fira-code/latin-400.css'
import '@fontsource/fira-code/latin-500.css'
import '@fontsource/fira-code/latin-600.css'
import '@fontsource/fira-code/latin-700.css'
import '@fontsource/ibm-plex-mono/latin-400.css'
import '@fontsource/ibm-plex-mono/latin-500.css'
import '@fontsource/ibm-plex-mono/latin-600.css'
import '@fontsource/ibm-plex-mono/latin-700.css'
import '@fontsource/space-mono/latin-400.css'
import '@fontsource/space-mono/latin-700.css'
import '@fontsource/geist-mono/latin-400.css'
import '@fontsource/geist-mono/latin-500.css'
import '@fontsource/geist-mono/latin-600.css'
import '@fontsource/geist-mono/latin-700.css'
import '@fontsource/martian-mono/latin-400.css'
import '@fontsource/martian-mono/latin-500.css'
import '@fontsource/martian-mono/latin-600.css'
import '@fontsource/martian-mono/latin-700.css'

// --- Display / headline ---------------------------------------------------
import '@fontsource/hanken-grotesk/latin-400.css'
import '@fontsource/hanken-grotesk/latin-500.css'
import '@fontsource/hanken-grotesk/latin-600.css'
import '@fontsource/hanken-grotesk/latin-700.css'
import '@fontsource/sora/latin-400.css'
import '@fontsource/sora/latin-500.css'
import '@fontsource/sora/latin-600.css'
import '@fontsource/sora/latin-700.css'
import '@fontsource/schibsted-grotesk/latin-400.css'
import '@fontsource/schibsted-grotesk/latin-500.css'
import '@fontsource/schibsted-grotesk/latin-600.css'
import '@fontsource/schibsted-grotesk/latin-700.css'
import '@fontsource/bricolage-grotesque/latin-400.css'
import '@fontsource/bricolage-grotesque/latin-500.css'
import '@fontsource/bricolage-grotesque/latin-600.css'
import '@fontsource/bricolage-grotesque/latin-700.css'

// --- Serif (warm / editorial) ---------------------------------------------
import '@fontsource/source-serif-4/latin-400.css'
import '@fontsource/source-serif-4/latin-500.css'
import '@fontsource/source-serif-4/latin-600.css'
import '@fontsource/source-serif-4/latin-700.css'
import '@fontsource/newsreader/latin-400.css'
import '@fontsource/newsreader/latin-500.css'
import '@fontsource/newsreader/latin-600.css'
import '@fontsource/newsreader/latin-700.css'
import '@fontsource/lora/latin-400.css'
import '@fontsource/lora/latin-500.css'
import '@fontsource/lora/latin-600.css'
import '@fontsource/lora/latin-700.css'
import '@fontsource/crimson-pro/latin-400.css'
import '@fontsource/crimson-pro/latin-500.css'
import '@fontsource/crimson-pro/latin-600.css'
import '@fontsource/crimson-pro/latin-700.css'

// Material Symbols Outlined — the icon font used throughout the UI. Bundled
// via @fontsource (woff2 self-hosted by Vite) so the host webview's CSP can
// ship with `font-src 'self'` and no Google Fonts CDN allowlist (#237, F2).
// The previous setup loaded it from fonts.googleapis.com; bundling keeps
// Silt 100% offline and consistent with the local-first font philosophy
// documented at the top of this file. The 400 weight is the default
// Material Symbols Outlined rendering; the codebase does not override the
// weight via CSS, so a single weight file matches the prior rendering.
import '@fontsource/material-symbols-outlined/400.css'

export type FontCategory = 'sans' | 'mono' | 'display' | 'serif'
export type FontSource = 'bundled' | 'system'

export interface FontEntry {
  /** Stable registry key (matches the @fontsource package suffix). */
  id: string
  /** Human-readable name shown in the dropdown. */
  displayName: string
  /** Value stored in config and injected as the CSS font-family. */
  cssFamily: string
  category: FontCategory
  source: FontSource
}

/**
 * The curated, bundled font set. The three defaults (Plus Jakarta Sans /
 * JetBrains Mono / Hanken Grotesk) match the canonical default theme's
 * typography block and the config.yaml defaults, so a fresh install renders
 * in those families without any user action. The other entries cover the
 * first-class themes' pairings plus popular UI/developer/editorial
 * alternatives the user can select.
 */
export const FONT_REGISTRY: FontEntry[] = [
  // Sans-serif body fonts
  {
    id: 'plus-jakarta-sans',
    displayName: 'Plus Jakarta Sans',
    cssFamily: 'Plus Jakarta Sans',
    category: 'sans',
    source: 'bundled'
  },
  {
    id: 'inter',
    displayName: 'Inter',
    cssFamily: 'Inter',
    category: 'sans',
    source: 'bundled'
  },
  {
    id: 'work-sans',
    displayName: 'Work Sans',
    cssFamily: 'Work Sans',
    category: 'sans',
    source: 'bundled'
  },
  {
    id: 'manrope',
    displayName: 'Manrope',
    cssFamily: 'Manrope',
    category: 'sans',
    source: 'bundled'
  },
  {
    id: 'mulish',
    displayName: 'Mulish',
    cssFamily: 'Mulish',
    category: 'sans',
    source: 'bundled'
  },
  {
    id: 'outfit',
    displayName: 'Outfit',
    cssFamily: 'Outfit',
    category: 'sans',
    source: 'bundled'
  },
  {
    id: 'atkinson-hyperlegible',
    displayName: 'Atkinson Hyperlegible',
    cssFamily: 'Atkinson Hyperlegible',
    category: 'sans',
    source: 'bundled'
  },
  {
    id: 'geist',
    displayName: 'Geist',
    cssFamily: 'Geist',
    category: 'sans',
    source: 'bundled'
  },
  {
    id: 'dm-sans',
    displayName: 'DM Sans',
    cssFamily: 'DM Sans',
    category: 'sans',
    source: 'bundled'
  },
  {
    id: 'figtree',
    displayName: 'Figtree',
    cssFamily: 'Figtree',
    category: 'sans',
    source: 'bundled'
  },
  {
    id: 'public-sans',
    displayName: 'Public Sans',
    cssFamily: 'Public Sans',
    category: 'sans',
    source: 'bundled'
  },
  {
    id: 'lexend',
    displayName: 'Lexend',
    cssFamily: 'Lexend',
    category: 'sans',
    source: 'bundled'
  },
  // Monospace fonts
  {
    id: 'jetbrains-mono',
    displayName: 'JetBrains Mono',
    cssFamily: 'JetBrains Mono',
    category: 'mono',
    source: 'bundled'
  },
  {
    id: 'fira-code',
    displayName: 'Fira Code',
    cssFamily: 'Fira Code',
    category: 'mono',
    source: 'bundled'
  },
  {
    id: 'ibm-plex-mono',
    displayName: 'IBM Plex Mono',
    cssFamily: 'IBM Plex Mono',
    category: 'mono',
    source: 'bundled'
  },
  {
    id: 'space-mono',
    displayName: 'Space Mono',
    cssFamily: 'Space Mono',
    category: 'mono',
    source: 'bundled'
  },
  {
    id: 'geist-mono',
    displayName: 'Geist Mono',
    cssFamily: 'Geist Mono',
    category: 'mono',
    source: 'bundled'
  },
  {
    id: 'martian-mono',
    displayName: 'Martian Mono',
    cssFamily: 'Martian Mono',
    category: 'mono',
    source: 'bundled'
  },
  // Display / headline fonts
  {
    id: 'hanken-grotesk',
    displayName: 'Hanken Grotesk',
    cssFamily: 'Hanken Grotesk',
    category: 'display',
    source: 'bundled'
  },
  {
    id: 'sora',
    displayName: 'Sora',
    cssFamily: 'Sora',
    category: 'display',
    source: 'bundled'
  },
  {
    id: 'schibsted-grotesk',
    displayName: 'Schibsted Grotesk',
    cssFamily: 'Schibsted Grotesk',
    category: 'display',
    source: 'bundled'
  },
  {
    id: 'bricolage-grotesque',
    displayName: 'Bricolage Grotesque',
    cssFamily: 'Bricolage Grotesque',
    category: 'display',
    source: 'bundled'
  },
  // Serif fonts (warm / editorial)
  {
    id: 'source-serif-4',
    displayName: 'Source Serif 4',
    cssFamily: 'Source Serif 4',
    category: 'serif',
    source: 'bundled'
  },
  {
    id: 'newsreader',
    displayName: 'Newsreader',
    cssFamily: 'Newsreader',
    category: 'serif',
    source: 'bundled'
  },
  {
    id: 'lora',
    displayName: 'Lora',
    cssFamily: 'Lora',
    category: 'serif',
    source: 'bundled'
  },
  {
    id: 'crimson-pro',
    displayName: 'Crimson Pro',
    cssFamily: 'Crimson Pro',
    category: 'serif',
    source: 'bundled'
  },
  // System fallbacks (always available offline; no bundled files)
  {
    id: 'system-ui',
    displayName: 'System UI',
    cssFamily: 'system-ui',
    category: 'sans',
    source: 'system'
  },
  {
    id: 'sans-serif',
    displayName: 'Sans Serif (generic)',
    cssFamily: 'sans-serif',
    category: 'sans',
    source: 'system'
  },
  {
    id: 'monospace',
    displayName: 'Monospace (generic)',
    cssFamily: 'monospace',
    category: 'mono',
    source: 'system'
  }
]

/** Registry ids of the three Cyber Forest (default) families. */
export const DEFAULT_BODY_ID = 'plus-jakarta-sans'
export const DEFAULT_MONO_ID = 'jetbrains-mono'
export const DEFAULT_HEADLINE_ID = 'hanken-grotesk'

/** The bundled (non-system) entries of a given category, in registry order. */
export function bundledByCategory(category: FontCategory): FontEntry[] {
  return FONT_REGISTRY.filter(
    (f) => f.source === 'bundled' && f.category === category
  )
}

/** The system-fallback entries (rendered in their own optgroup). */
export function systemFonts(): FontEntry[] {
  return FONT_REGISTRY.filter((f) => f.source === 'system')
}

/** Look up an entry by its cssFamily (the value stored in config). */
export function findByCssFamily(cssFamily: string): FontEntry | undefined {
  return FONT_REGISTRY.find((f) => f.cssFamily === cssFamily)
}

/**
 * Resolve a config font-family value to a human-readable display name. Falls
 * back to the raw value when it isn't in the registry (e.g. a hand-edited
 * config.yaml) so the picker never shows a blank.
 */
export function displayNameForCssFamily(cssFamily: string): string {
  return findByCssFamily(cssFamily)?.displayName ?? cssFamily
}

/**
 * Resolve a font-family value to a clean display name, handling both bare
 * registry names ("Plus Jakarta Sans") and full CSS stacks as written by
 * theme typography blocks ("'Plus Jakarta Sans', sans-serif"). Used by the
 * font picker trigger and the Appearance-tab typography indicator so a theme
 * override is shown as "Plus Jakarta Sans" rather than the verbose stack.
 */
export function displayFamilyName(cssFamily: string): string {
  if (!cssFamily) return ''
  const found = findByCssFamily(cssFamily)
  if (found) return found.displayName
  // CSS stack: pull the first family (quoted or bare) before the first comma.
  const m = cssFamily.match(/^'([^']+)'|^([^,]+)/)
  if (m) return (m[1] ?? m[2] ?? cssFamily).trim()
  return cssFamily
}
