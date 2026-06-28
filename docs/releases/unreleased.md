<!-- Next release notes go here -->

## Editor: advanced blocks & interactions

- **Mermaid diagrams** — a fenced ```` ```mermaid ```` code block now renders a live, theme-aware diagram. Invalid source shows a readable inline error; toggle to edit the raw source; copy works as before.
- **LaTeX math (KaTeX)** — inline `$…$` and block `$$…$$` equations render via KaTeX. `/math` inserts a centered block equation; click any equation to edit its LaTeX. Currency like `$5` / `5$` stays literal. Per-vault `ui.formatting.math_enabled` toggle (default on).
- **@-mentions** — typing `@` opens a typeahead of known task owners (the distinct-owner set from your index); selecting one inserts an `@[name]` chip that round-trips through markdown.
- **Drag-to-reorder** — a drag handle floats over each block to reorder by direct manipulation; `Alt+↑`/`Alt+↓` moves the active block by keyboard.
- **Scroll preserved across Edit↔Source** — toggling a tab to Source and back no longer jumps you to the top of the page; the Edit scroll offset is restored.

## Notes

- **Linked notebooks on Linux/macOS:** the linked-root trust fingerprint no longer includes the folder's modification time (it mutated on every page add/remove, breaking the link repeatedly). The fingerprint is now the folder's device + inode only. Existing linked notebooks on Linux/macOS will prompt once to re-link on first open after this update.
