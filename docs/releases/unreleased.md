# Fixes

- **Right-click "Rename" on a page now opens a modal dialog** (like sections and notebooks do) instead of trying to focus an inline title that may not be mounted yet. Renaming a page through the dialog refreshes the sidebar tree and updates any open tabs pointing to the old page name.
- **Fixed blank page regression.** A closure-variable deduplication guard in the page-loading effect could prevent the editor from loading page content. Reverted to the simpler, proven pattern.
