# Silt — Agent Guidelines

Repo-specific rules that augment the global `~/.config/opencode/AGENTS.md`.

## Testing

- **Do NOT introduce Playwright.** Playwright/browser-driven e2e tests are
  flaky in this project's CI model (Wails webview cannot run headless in CI).
  Prefer **Vitest** component/unit tests (jsdom) for frontend, and **Go's
  `testing`** package for backend. If an end-to-end interaction needs covering,
  test the contract at the IPC boundary (mock the Wails bindings) rather than
  driving the rendered webview.

## Accessibility

- **Address all reasonable a11y warnings.** We are not aiming for full WCAG
  compliance, but every Svelte `a11y_*` warning or obvious gap should be fixed
  or explicitly justified. Prefer the correct semantic element/role, proper
  `aria-label`/`aria-labelledby`, keyboard operability, and `aria-live` regions
  for dynamic updates. Suppress a warning with `svelte-ignore` only when the
  interaction genuinely cannot be expressed semantically (and leave a comment
  explaining why).

## Comments

- **Comments explain WHY, not WHAT.** A comment that restates the adjacent
  code is dead weight — the code itself already says what happens. Prefer
  short rationale notes (1–2 lines) over multi-paragraph essays; a comment
  longer than 3 lines needs strong justification (real interop target,
  load-bearing architecture decision, or non-obvious gotcha).
- **Avoid issue-number tags once the feature ships.** A `(#168)` next to a
  routine inline mark is archaeology, not documentation. Keep the tag on the
  design-block comment at the top of the section if it adds traceability;
  drop it from per-occurrence comments.
- **Don't name-drop competitors.** Describe the standard or convention being
  followed, not which product it came from. "Single-click opens a preview,
  double-click promotes" is the right level of detail — naming VS Code /
  Word / Google Docs / etc. is rarely load-bearing.
- **Real interop references stay.** Mentions of SharePoint, OneDrive,
  Dropbox, Obsidian, Dataview, etc. that describe actual sync targets or
  format-compat features are not name-dropping — they document what the
  code is talking to. Keep those.

## Conventions

- Follow `ARCHITECTURE.md` (topology, SQLite schema, IPC contract) and
  `SPECS.md` (file format, plugin SDK, config schema) as the sources of truth.
- Plugin JS runs in the main webview; the SDK (`PluginContext`) is the
  contract — direct Wails binding access (`wailsjs/go/main/App.js`) is
  deprecated and will break when per-plugin webviews land (#151/#152).
- Frontend tests mock `../../wailsjs/go/main/App.js` via `vi.mock` +
  `vi.hoisted` (see `frontend/src/components/settings/AppearanceTab.test.ts`
  for the canonical pattern). Never hit real IPC in a test.
- Keep `PLAN.md` as a temporary planning artifact — never stage, commit, or
  push it (it is `.gitignore`d).
