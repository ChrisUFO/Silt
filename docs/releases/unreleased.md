# Security

- All known dependency vulnerabilities resolved (Go stdlib, transitive Go modules, and frontend build tooling).
- Linux downloads are now cryptographically signed. An SBOM (software bill of materials) is attached to every release. See `CODE_SIGNING.md` for how to verify a download.

# Improvements

- A new audit log (Settings → Diagnostics) records every plugin install, capability grant, and linked-notebook change, so trust decisions leave a durable host-side trail.
- Plugin desktop notifications now cap title and body length to prevent oversized payloads from reaching the OS notifier.

# Fixes

- Removed the one-release backward-compatibility parser for the old space-delimited network audit log format. Logs written by the current JSON-only format are unaffected.
