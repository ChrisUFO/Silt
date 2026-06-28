# Unified Calendar + Agenda, Plugin Sidebars (Hardening Pass)

- **The Calendar view now includes the agenda.** Switching to Calendar opens a unified view with three layouts selectable from the header: Month, Week, and Agenda. The Agenda list preserves the previous Overdue / Today / Tomorrow / Upcoming grouping and supports marking tasks done in place.
- **Calendar's sidebar now drives the view.** Pick Today, Upcoming (next 7 days), Overdue, Completed, or All Tasks to dim non-matching tasks and focus the list. A mini month-calendar with dot indicators lets you jump the main view to any day by clicking the date.
- **Kanban's sidebar now drives the board.** Save the current scope plus filter combination as a named board and recall it with one click. The sidebar's scope selector and filter toggles stay in sync with the header and FilterBar — toggle a chip in either place and the other updates instantly.
- **Plugins can now own the sidebar.** First-party plugins (Calendar, Kanban) ship a compiled sidebar component that takes over the sidebar slot for their view. Third-party plugins continue to render sidebar content through the iframe-based sidebar-panel surface.
- **Agenda mode gets a clear-filter banner inside the view.** When a sidebar filter is active, an in-view banner appears with a one-click Clear so the filter can be dismissed without reaching for the sidebar.
- **Mini calendar gains a Today chip.** Click it to jump the main view back to the current day from anywhere in the sidebar.
- **Saved boards validate on load.** Malformed entries in your saved-boards list (from a hand-edited config) are dropped silently and re-persisted on next save, so the file self-heals.

# Hardening (Post-Merge Review Fixes)

- **Today smart-list excludes overdue tasks.** The "Today" badge now counts only tasks due exactly today; overdue tasks are counted under their own "Overdue" badge. Previously the SQL used `due_date <= today` and conflated the two.
- **Mark-done failures show a dismissable banner with auto-clear.** When `updateBlockState` rejects, an in-view banner appears with the error message and an explicit close (×) button; the banner also clears itself after 8 seconds or as soon as a subsequent mark-done succeeds.
- **Today and Upcoming smart lists are mutually exclusive.** Clicking the Upcoming badge dims only future tasks; today-due tasks live in the Today badge alone. Previously the filter and the badge disagreed by one task.
- **Saved-board delete prompts for confirmation.** Deleting a saved board now shows a confirmation dialog matching the existing remove-column affordance, so a stray click can't wipe a named board.
- **Calendar's sidebar state resets on vault switch.** The `refresh-navigation` event (fired after every vault switch) now clears `focusDate` and `activeFilter` so a new vault opens on Today / All Tasks instead of inheriting state from the previous vault.
- **Kanban scope radios are keyboard-correct per WAI-ARIA APG.** The radio group reads the chosen scope from the focused element's own `data-scope-radio` attribute, so Enter on a focused radio activates it even when the cursor index hasn't been updated yet. Disabled scopes are skipped by arrow-key navigation and ignore Enter/Space activation.
- **Agenda mode is read-only on the sidebar.** Cursor movement and reload triggers from the sidebar's mini-calendar are now skipped when the Calendar view is in Agenda mode, so clicking a day cell doesn't snap the agenda list away from the user's current selection.
- **Calendar sidebar cleans up on view switch.** When you switch away from the Calendar view, the sidebar's `refresh-navigation` listener, `block:changed` subscription, and minute-tick `nowInterval` are all released. Switching back to Calendar starts fresh instead of inheriting stale state from the previous mount.

# Improvements

- A separate "Agenda" entry is no longer needed in the activity bar; the Calendar entry covers it.
- `today` re-buckets on midnight while the agenda is open. The agenda's "Today" / "Tomorrow" / "Overdue" / "Upcoming" buckets all recompute when the local date changes, so tasks don't sit in the wrong bucket overnight. Previously only the "Today" bucket followed the local clock.

# Notes

- No breaking changes to third-party plugins. The new `sidebarComponent?` field on `RegisteredPlugin` is optional; existing plugins continue to render sidebar content via the iframe-based `registerSurface({ kind: 'sidebar-panel' })` surface as before.
- The Kanban scope radio group persists the last-used scope per-session. A future change will move this to user config (so scope survives restarts); for now it tracks per-session so opening the same Kanban board in two sessions is deterministic.