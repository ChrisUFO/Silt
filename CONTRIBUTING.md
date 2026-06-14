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

**Any time you add, remove, rename, or change the signature of a Wails-bound
method on `App`**, regenerate the bindings from the `frontend/` directory:

```sh
cd frontend
npm run generate      # runs `wails generate module`
```

Commit the regenerated `frontend/wailsjs/` files in the same commit as the Go
change. Both the pre-push hook (`.githooks/pre-push`) and the CI workflow
(`.github/workflows/ci.yml`) **fail** if the committed bindings are out of date
— if you see *"Wails JS bindings are out of date"*, run `npm run generate` and
commit the result.

## Pre-push hook

`git config core.hooksPath .githooks` enables three gates on every push:

1. **Go tests** (`go test -race -count=1 ./...`) when any `.go` file changed.
2. **Frontend build** (`npm run build` in `frontend/`) when any `frontend/`
   file changed.
3. **Binding-drift check** when any `.go` file changed — runs
   `npm run generate` and fails if it would change `frontend/wailsjs/`.

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
