# Fixes

- **Added "Open DevTools on startup" toggle** in Settings > Editor. Requires a build with the Wails `-devtools` flag (now default in both build scripts). When enabled, the Chromium inspector opens on next launch. `Ctrl+Shift+F12` also opens DevTools on any `-devtools` build. `SILT_DEBUG=1` env var works as a fallback.
