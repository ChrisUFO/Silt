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
- Use [Conventional Commits](https://www.conventionalcommits.org/) prefixes:
  `feat:`, `fix:`, `refactor:`, `docs:`, `chore:`.
- Keep commits focused and reviewable; one logical change per commit.

## Wails bindings — keep them fresh

The Go→JS IPC layer is **generated**: every method exported on `App` in
`app.go` is reflected into `frontend/wailsjs/go/main/App.{js,d.ts}` and the
types into `frontend/wailsjs/go/models.ts`. The frontend imports those
generated files; they must match the live Go signatures or the frontend calls a
function that does not exist (or with the wrong arg shape).

`frontend/wailsjs/` is **gitignored** — it is a build artifact, never
committed. So every developer (and CI) regenerates it locally:

```sh
cd frontend
npm run generate      # runs `wails generate module`
```

Run this after you add, remove, rename, or change the signature of a
Wails-bound method on `App`, so your local frontend imports resolve.
**Any time you edit Go bindings, run `npm run generate`** before the frontend
will type-check/build against the new signatures.

CI (`.github/workflows/ci.yml`) regenerates the bindings fresh on every run as
part of the build, then runs `svelte-check` + `vite build` — that is the real
Go↔binding consistency guarantee (if a signature changed and the frontend
import went stale, the type-check fails the build).

## Pre-push hook

`git config core.hooksPath .githooks` enables two gates on every push:

1. **Go tests** (`go test -race -count=1 ./...`) when any `.go` file changed.
2. **Frontend build** (`npm run build` in `frontend/`) when any `frontend/`
   file changed.

Documentation-only / asset-only pushes are exempt automatically. (There is no
binding-drift gate in the hook because `frontend/wailsjs/` is gitignored and
therefore has no committed state to compare against; CI regenerates it instead.)

## Testing

See [`TESTING.md`](./TESTING.md) for the full test matrix (per-package
coverage, the startup benchmark budget, and the manual verification checklist
for `wails dev`).

## Architecture

See [`ARCHITECTURE.md`](./ARCHITECTURE.md) for the system topology, the
on-disk WAL SQLite index + incremental re-indexing model, the execution
coordinator's locking, and the TTL-lease focus-lock model. Read it before
changing the persistence or concurrency layers.
