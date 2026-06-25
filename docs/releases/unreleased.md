# In-App Update Check & Upgrade

- Silt now checks for new releases on startup (once per 24h) and from **Settings → About → Check for updates**, so you no longer have to watch the GitHub Releases page manually.
- When a new version is available, a non-blocking toast links to the release notes, and **About** shows the version, a changelog excerpt, and a one-click **Install update**.
- The installer downloads the correct asset for your platform, verifies it against the published `SHA256SUMS` before running it, and relaunches Silt on the new version. A checksum mismatch aborts the upgrade with a clear error.
- Prefer to check manually? Turn off **Automatically check for updates** on the About page; the choice persists across launches.
- No account or token is required — update checks use unauthenticated reads of the public GitHub Releases API.

# Security

- Every release now publishes a `SHA256SUMS` file covering its binary assets; the in-app updater verifies each download against it before executing, complementing the existing cosign (Linux) and SignPath (Windows) signatures.
