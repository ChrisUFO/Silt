# Fixes

- **Added "Open DevTools on startup" toggle** in Settings > Editor. Requires `-devtools` build flag (now default). `Ctrl+Shift+F12` shortcut also available.
- **WebView2 cache cleared on version upgrade.** A `.silt-version` marker inside `%APPDATA%/Silt/webview2/` prevents stale `EBWebView` data from carrying over between releases. Same-version restarts keep their cache; version bumps get a clean slate. This fixes the blank-page rendering that affects some machines due to corrupted browser cache (MicrosoftEdge/WebView2Feedback#2979).
- **Diagnostic console logging** (`[Silt]` / `[VSC]` prefixes) in the browser console to surface page-rendering state.
