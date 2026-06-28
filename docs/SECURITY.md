# Silt Security Model

This document describes Silt's security posture: the trust boundaries, the
threat models, and the controls that protect the user's data against the
realistic adversaries of a local-first desktop application.

## 1. Threat models

Silt is a local-first desktop app. The realistic adversaries are:

- **M2 — Synced-vault adversary:** an attacker who can edit files in a vault
  that is synced between hosts via OneDrive, Dropbox, Syncthing, etc. They
  cannot run code on the victim's machine, but they can inject/modify
  markdown, config.yaml, and plugin files.
- **M3 — Co-tenant / malware with user credentials:** an attacker who can
  write to the user's OS-config dir (e.g. a shared workstation, or malware
  running with the user's privileges). The hard boundary here is the
  filesystem permissions on the home directory; Silt's controls are
  defense-in-depth tripwires.

## 2. Storage tiers and trust scope

Silt's persistent storage is layered (see `ARCHITECTURE.md §0`):

| Tier | Location | Trust scope |
|---|---|---|
| Content (markdown) | `<vault>/*.md` | Travels with the vault (by design — the user's notes ARE the product) |
| Vault config | `<vault>/.system/config.yaml` | Vault-scoped; editable by M2 |
| Linked-notebook config | `<linkedRoot>/.system/config.yaml` | Notebook-scoped; editable by M2 |
| User-global settings | `<configDir>/silt/settings.json` | Host-scoped; editable by M3 |
| Host-scoped grants | `<configDir>/silt/grants.json` | Host-scoped; editable by M3 |
| SQLite index | `<vault>/.system/index.sqlite*` | Reproducible working memory (delete + rebuild) |

The cardinal rule: **trust anchors that the user's safety depends on must be
host-scoped (verified on this machine), not vault-scoped (editable by a sync
counterpart).**

## 3. Vault trust anchors

### 3.1 Linked-notebook RootPath fingerprinting (F3)

A linked notebook's `root_path` is stored in vault-scoped `config.yaml`, so
it travels with the vault — that's correct (the user wants their link list on
every host). But without a host-side anchor, a synced edit to `root_path`
could redirect the link to an attacker-chosen folder, and every downstream
containment check (`isPathWithinRoot`) would be satisfied against the new
root.

**Control:** `LinkNotebook` captures a `RootFingerprint` (inode + device on
POSIX; volume serial + file index on Windows) at link time. On every access,
`resolveNotebookDir` recomputes the fingerprint and compares. On mismatch, the
link is **quarantined** (excluded from indexing, reads, and writes) and the
user sees a re-link prompt. A config reload (fsnotify) that changes `root_path`
is detected and quarantined without adopting the new path. The POSIX
fingerprint is `dev:ino` only — directory `mtime` is intentionally excluded
because adding/removing a page inside the linked root mutates the directory's
mtime (which would invalidate the fingerprint on every CRUD op), and `touch -r`
defeats mtime anyway. This is an inode-identity anchor, not a tamper-evidence
seal: a co-tenant with write access to the linked root can still clone+recreate
the folder.

**Migration:** existing linked notebooks (pre-F3) get a fingerprint assigned
silently on the next vault open (the user linked the folder on THIS host, so
it's trusted). A subsequent format change that drops a field (e.g. the POSIX
`mtime` removal) triggers a one-time re-link prompt for existing links on the
affected platform — the fingerprint no longer matches, so the user re-confirms
the link once.

### 3.2 settings.json integrity tripwire (F20)

The user-global `settings.json` holds `vault_path` and `trusted_publishers`.
A co-tenant (M3) who can write to the OS-config dir could redirect the vault
path or poison the publisher trust list.

**Control:** a SHA-256 fingerprint of the trust-anchor fields
(`vault_path` + `trusted_publishers`) is stored alongside `settings.json`
as `settings.json.fingerprint`. On every launch, the fingerprint is
recomputed and compared. On mismatch, Silt surfaces a confirmation dialog
rather than silently loading the new values. The fingerprint is a tripwire,
not a hard boundary — an attacker who can write `settings.json` can also
write the fingerprint. The hard boundary is filesystem permissions on the
home dir. The host-scoped config directory is created with `0o700` perms
(owner-only on POSIX; no-op on Windows which ignores group/other bits), and
the files within it are written `0o600` (owner-only read/write). Unknown
top-level keys in `settings.json` are rejected (defense in depth against
future field-injection).

## 4. Plugin trust boundary

### 4.1 Session tokens (F1)

Every privileged `Plugin*` Wails binding validates a session token
(`validatePluginSession`) so a plugin cannot impersonate another by calling
a raw binding with a different `pluginID`. The token is captured at load time
by the frontend loader and threaded through the SDK closures.

### 4.2 Content Security Policy (F2)

The main webview carries a strict CSP `<meta>`: `default-src 'self'` with
tightly-scoped per-directive relaxations (`blob:` for plugin ESM imports,
`'wasm-unsafe-eval'` for Vite/Tailwind wasm, `'unsafe-inline'` for the theme
+ editor-token injectors). Google Fonts are bundled via `@fontsource/*`; no
external CDN is loaded.

### 4.3 Capability model (#113)

Every privileged SDK binding is gated server-side by `requireGrant(pluginID,
capability)`. Grants are per-host (see §4.7). The capability set is fixed:
`read-files`, `write-files`, `network`, `os-open`, `os-clipboard`,
`os-notify`, `ui-surface`, `editor-schema`, `content-mutate`. The `exec`
capability is deferred until the signing/trust model matures.

### 4.4 First-party ID reservation (F5)

The ids `silt-agenda`, `silt-calendar`, `silt-kanban`, `silt-attachments` are
reserved for the bundled first-party plugins. A third-party archive claiming
one is rejected at install time (`plugins.Validate`) with a clear error, and
re-checked in `plugins.Install` as defense in depth. The match is exact —
near-collision ids like `silt-kanban2` are accepted.

### 4.5 Network safety (F13)

Plugin `fetch` routes through a Go-side proxy with SSRF defense (internal-IP
rejection at URL-validation, redirect-revalidation, AND dial-time to guard
against DNS rebinding), timeout/size caps, and an audit log. Cross-host
redirects reset the `User-Agent` to `Go-http-client/1.1` so a plugin cannot
embed credentials in the UA and leak them across a redirect.

### 4.6 Plugin surface isolation (F14)

Plugin UI surfaces run in sandboxed `<iframe srcdoc>` with a restrictive
CSP (`connect-src 'none'`). The postMessage bridge uses `'null'` as the
targetOrigin (correct for a sandboxed iframe without `allow-same-origin`).

### 4.7 Grant provenance (F4)

Plugin capability grants live in **per-host storage**
(`<configDir>/silt/grants.json`), NOT in the synced vault's `config.yaml`.
A vault opened on a new host never inherits third-party grants — the user is
prompted on first use. First-party plugins remain implicitly granted (seeded
in-memory at vault open, no prompt).

On the first launch after upgrading to the F4 build, a vault that still
carries a legacy `plugins.grants:` block in `config.yaml` triggers a one-time
migration dialog. The user confirms, and the grants are moved to the host
store + the block is stripped from `config.yaml`. If the user denies, every
third-party plugin re-prompts on first use (the safe default).

The grants file is written `0o600` (atomic temp-file + rename), matching the
F7 perm-pairing protocol used by `settings.json` and the F20 fingerprint.

## 5. CI security scanning (F8)

Every push to `main` and every PR runs a `security` CI job:
- **govulncheck** — fails on any reachable Go vulnerability (stdlib + deps).
- **npm audit --omit=dev --audit-level=high** — fails on any high/critical
  in the production npm tree.
- **gitleaks** — fails on any secret detected in the full commit history.

Dependabot opens weekly PRs for GitHub Actions versions and npm dependency
updates so new advisories surface before they hit the gate.
