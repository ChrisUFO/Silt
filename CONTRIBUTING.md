# Contributing to Silt

Silt is a local-first hybrid journal and task manager — plain-text Markdown on
disk, a real-time SQLite index in memory (persisted on disk in WAL mode), and a
Svelte 5 frontend over a Wails Go core. This guide covers the workflow that
keeps the Go, frontend, and IPC-binding layers in sync.

## Quick start

```sh
# Install hooks so every push is gated (tests, build, binding freshness):
git config core.hooksPath .githooks

# Run the app:
wails dev

# Run the Go test suite with the race detector:
go test -race -count=1 ./...

# Type-check + build the frontend:
cd frontend && npm run check && npm run build
```

## Branching & commits

- Work on feature branches off `main`. Open a PR to merge back.
- **Before opening a PR, rebase onto `main`** so `frontend/package-lock.json`
  is current and reviewers see only your real changes.
- Use [Conventional Commits](https://www.conventionalcommits.org/) prefixes:
  `feat:`, `fix:`, `refactor:`, `docs:`, `chore:`.
- Keep commits focused and reviewable; one logical change per commit.

## Release notes

User-visible changes ship with a fresh entry in
[`docs/releases/unreleased.md`](./docs/releases/unreleased.md) — the staging
file for the *next* release.

- **Overwrite the file in full — do not append.** Each PR that lands a
  user-visible change replaces `unreleased.md` completely with that PR's own
  notes. Whatever is already in the file belongs to a release that has already
  shipped and is stale by the time your PR lands; never layer your entry on top
  of it.
- Write customer-facing prose (what changed and why it matters to the person
  using Silt), not implementation detail. Match the existing voice:
  keep-a-changelog-style sections (`# Fixes`, `# Improvements`, `# Notes`, …)
  with `- **Lead sentence.** body` bullets.
- **The published version history lives in the GitHub Releases**, not in the
  repo — there are no per-version changelog files. `unreleased.md` is only the
  staging area for the upcoming release; once a version ships, its notes move
  to that version's GitHub Release and the next PR overwrites the file fresh.

PRs with no user-visible surface (refactors, test-only changes, internal
plumbing) can leave `unreleased.md` untouched.

## Lockfile conflicts

`frontend/package-lock.json` is large and ordering-sensitive, so hand-merging
the conflict markers produces an inconsistent lockfile. `.gitattributes`
configures it with `merge=union`, so most lockfile conflicts resolve
automatically (both sides' added packages are kept). When a manual conflict
remains:

1. Take either side: `git checkout --theirs frontend/package-lock.json`
2. Regenerate from the merged `package.json`:
   ```sh
   cd frontend && npm install
   ```
3. `git add frontend/package-lock.json` and commit.

Do **not** attempt to resolve conflict markers in the lockfile by hand.

## Wails bindings — auto-regenerated on `npm install`

The Go→JS IPC layer is **generated**: every method exported on `App` in
`app.go` is reflected into `frontend/wailsjs/go/main/App.{js,d.ts}` and the
types into `frontend/wailsjs/go/models.ts`. The frontend imports those
generated files; they must match the live Go signatures or the frontend calls a
function that does not exist (or with the wrong arg shape).

`frontend/wailsjs/` is **gitignored** — it is a build artifact, never
committed. Binding regeneration is now automatic:

- `npm install` runs the `prepare` script (`scripts/regenerate-bindings.mjs`),
  which calls `wails generate module` from the project root. A fresh clone
  produces a working `frontend/wailsjs/` without a manual step, so a newly-
  added Go method can never silently drift from the frontend imports a user
  has.

If the `wails` CLI is not on `PATH` (e.g. a brand-new machine that hasn't run
`go install github.com/wailsapp/wails/v2/cmd/wails@latest` yet), the script
prints a one-line pointer and exits 0 — `npm install` is never blocked by an
unrelated dev-tool install.

You can also run `npm run generate` explicitly at any time to force a refresh
(it calls the same script with the same skip-tolerant behavior).

CI (`.github/workflows/ci.yml`) regenerates the bindings fresh on every run as
part of the build, then runs `svelte-check` + `vite build` — that is the real
Go↔binding consistency guarantee (if a signature changed and the frontend
import went stale, the type-check fails the build).

## Pre-push hook

`git config core.hooksPath .githooks` enables a fast local Go gate on every
push:

- **`go test -race -count=1 ./...`** when any `.go` file changed.

This is intentionally a *fast local gate* — it catches Go regressions in
seconds before you push, so you're not waiting on CI for a broken build.
**CI (`.github/workflows/ci.yml`) is the authoritative gate** and runs the
full pipeline on Linux (go test -race, npm build, svelte-check, binding
regeneration), including the cross-platform signal the local Windows hook
can't give (symlink + fsnotify tests that skip on Windows). Frontend
validation is left to CI: your IDE + `wails dev` cover live editing, and CI
re-validates authoritatively on push.

Documentation-only / asset-only pushes are exempt automatically.

## Testing

See [`TESTING.md`](./TESTING.md) for the full test matrix (per-package
coverage, the startup benchmark budget, and the manual verification checklist
for `wails dev`).

## Architecture

See [`ARCHITECTURE.md`](./ARCHITECTURE.md) for the system topology, the
on-disk WAL SQLite index + incremental re-indexing model, the execution
coordinator's locking, and the TTL-lease focus-lock model. Read it before
changing the persistence or concurrency layers.
