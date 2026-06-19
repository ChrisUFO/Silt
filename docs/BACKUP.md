# Backing up and migrating your vault

Your Silt vault is plain text on disk, so you can always back it up by copying
the folder directly. Silt also ships a **portable `.silt-vault` archive** format
(`Settings → General → workspace ⋮ menu → Export vault…` / `Import vault…`)
that bundles the whole vault into a single checksummed file — convenient for
backups, external drives, and moving between machines.

## What is and isn't in a `.silt-vault` archive

A `.silt-vault` archive is a single self-contained file containing:

- All your notebooks, sections, and pages (your notes + tasks).
- The `.system/` folder: your config, themes, templates, and plugins.

It deliberately **excludes** two things:

- **The search index** (`.system/index.sqlite*`) — it is a cache that Silt
  rebuilds automatically from your notes when you import. Nothing is lost.
- **Linked notebooks** — those are external folders (e.g. a synced SharePoint
  or Dropbox mount) that live outside the vault; Silt never moves or copies
  them. If you want them on the destination machine, sync them there
  separately and re-link them (`Link External Folder…` in the sidebar).

Every file in the archive is checksummed (per-file SHA-256 + a whole-archive
root digest), so corruption or tampering is detected **before** anything is
written on import.

## Export (back up / migrate out)

1. `Settings → General` → click the **⋮** next to your vault path →
   **Export vault…**.
2. Choose where to save the `.silt-vault` file (e.g. an external drive or
   cloud-synced folder).
3. A progress bar streams until the archive is complete. The vault you're
   exporting stays open and untouched — export is read-only.

## Import (restore / migrate in)

1. `Settings → General` → **⋮** → **Import vault…**.
2. Pick the `.silt-vault` archive, then pick an **empty folder** to extract
   into (must be on a local drive).
3. Silt validates the archive (manifest + checksums; rejects a corrupt or
   tampered archive before writing anything), extracts it, and opens the
   restored vault.

The original archive file is never modified or deleted by import — keep it as
your backup.

## Move vs Copy vs Export — when to use which

| Action | Use when |
| :--- | :--- |
| **Move vault…** | You're relocating your active vault to a new folder on this machine and want to keep working in it there. |
| **Copy vault…** | You want a duplicate vault on this machine as a separate workspace you can switch into later. |
| **Export vault…** | You want a single portable backup file, or to carry the vault to another machine. |
| **Import vault…** | You're restoring from a `.silt-vault` archive (backup or migration). |

Move/Copy produce a live vault folder; Export produces a portable archive file.
Both exclude the reproducible search index (rebuilt on first open), and neither
touches linked external notebooks.

See `SPECS.md` §3.4 for the full `.silt-vault` format specification.
