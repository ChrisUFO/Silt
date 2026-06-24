# New Editor Block Types

- Mark any block as a quote with `/quote` or `Ctrl+Shift+9`; nested quotes render with a deeper border.
- Insert callout boxes — note, info, tip, warning, danger, success, or quote — that stand out with an icon and a colored accent.
- Add fenced code blocks with syntax highlighting, a per-block language picker, and a one-click copy button. Multi-line code is preserved exactly.
- Collapse long sections with foldable regions; click the summary or press `Ctrl+.` to expand and collapse.
- Insert editable tables with standard pipe-table formatting, keyboard cell navigation, drag-to-resize columns, and a row/column toolbar (insert, delete, merge).
- Every new block type is reachable from the `/` slash menu, the formatting toolbar, and (where standard) a keyboard shortcut, and all of them round-trip through the file as standard markdown that opens cleanly in any other editor.

# Security

- All known dependency vulnerabilities resolved (Go stdlib, transitive Go modules, and frontend build tooling).
- Linux downloads are now cryptographically signed. An SBOM (software bill of materials) is attached to every release. See `CODE_SIGNING.md` for how to verify a download.

# Improvements

- A new audit log (Settings → Diagnostics) records every plugin install, capability grant, and linked-notebook change, so trust decisions leave a durable host-side trail.
- Plugin desktop notifications now cap title and body length to prevent oversized payloads from reaching the OS notifier.

# Fixes

- Removed the one-release backward-compatibility parser for the old space-delimited network audit log format. Logs written by the current JSON-only format are unaffected.
