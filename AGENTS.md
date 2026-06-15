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

## Conventions

- Follow `ARCHITECTURE.md` (topology, SQLite schema, IPC contract) and
  `SPECS.md` (file format, plugin SDK, config schema) as the sources of truth.
- Frontend tests mock `../../wailsjs/go/main/App.js` via `vi.mock` +
  `vi.hoisted` (see `frontend/src/components/settings/AppearanceTab.test.ts`
  for the canonical pattern). Never hit real IPC in a test.
- Keep `PLAN.md` as a temporary planning artifact — never stage, commit, or
  push it (it is `.gitignore`d).
