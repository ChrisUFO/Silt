package main

import (
	"fmt"
	"os"
	"os/exec"
	goruntime "runtime"
	"silt/backend/plugins"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// =========================================================================
// OS integration (#114)
// =========================================================================

// PluginOpenInNativeHandler opens a file within a notebook in the OS default
// handler for its type. Traversal-guarded. Gated by os-open.
// Session-token verified (#236).
func (a *App) PluginOpenInNativeHandler(pluginID, sessionToken, notebook, relPath string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	if err := a.requireGrant(pluginID, plugins.CapOSOpen); err != nil {
		return err
	}
	abs, err := a.resolvePluginNotebookPath(notebook, relPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("file not found: %w", err)
	}
	return openNative(abs)
}

// PluginOpenUrl opens a URL in the system browser. Scheme-restricted to http,
// https, mailto (file/javascript/custom schemes blocked). Gated by os-open.
// Session-token verified (#236).
func (a *App) PluginOpenUrl(pluginID, sessionToken, url string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	if err := a.requireGrant(pluginID, plugins.CapOSOpen); err != nil {
		return err
	}
	if !isSafeUrl(url) {
		return fmt.Errorf("URL scheme is not allowed (only http, https, mailto)")
	}
	if a.ctx == nil {
		return fmt.Errorf("application context not ready")
	}
	wruntime.BrowserOpenURL(a.ctx, url)
	return nil
}

// PluginPickOpenFile opens a native file picker and returns the chosen path
// (empty on cancel). Not capability-gated (a picker is user-driven; the chosen
// path only becomes useful through a gated binding like AddAttachment).
// Session-token verified (#236) — verifies the caller's identity so a
// malicious main-webview plugin cannot drive the OS file picker by spoofing
// a first-party pluginID.
func (a *App) PluginPickOpenFile(pluginID, sessionToken, filterPattern string) (string, error) {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return "", err
	}
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	return wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Select a file",
		Filters: []wruntime.FileFilter{
			{DisplayName: "All files", Pattern: filterPattern},
		},
	})
}

// PluginPickSaveFile opens a native save-file picker and returns the chosen
// path (empty on cancel). Session-token verified (#236).
func (a *App) PluginPickSaveFile(pluginID, sessionToken, defaultFilename string) (string, error) {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return "", err
	}
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	return wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
		Title:           "Save file",
		DefaultFilename: defaultFilename,
	})
}

// PluginClipboardReadText reads the system clipboard. Gated by os-clipboard.
// Session-token verified (#236).
func (a *App) PluginClipboardReadText(pluginID, sessionToken string) (string, error) {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return "", err
	}
	if err := a.requireGrant(pluginID, plugins.CapOSClipboard); err != nil {
		return "", err
	}
	if a.ctx == nil {
		return "", fmt.Errorf("application context not ready")
	}
	return wruntime.ClipboardGetText(a.ctx)
}

// PluginClipboardWriteText writes text to the system clipboard. Gated by
// os-clipboard. Session-token verified (#236).
func (a *App) PluginClipboardWriteText(pluginID, sessionToken, text string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	if err := a.requireGrant(pluginID, plugins.CapOSClipboard); err != nil {
		return err
	}
	if a.ctx == nil {
		return fmt.Errorf("application context not ready")
	}
	wruntime.ClipboardSetText(a.ctx, text)
	return nil
}

// PluginNotify shows a desktop notification. Wails v2 has no native
// notification runtime API, so this falls back to a cross-platform OS command
// (osascript on macOS, notify-send on Linux, msg/PowerShell on Windows). Gated
// by os-notify. A failure to spawn the notifier is non-fatal (logged) — a
// notification is best-effort UX, not a correctness path.
// Session-token verified (#236).
func (a *App) PluginNotify(pluginID, sessionToken, title, body string) error {
	if err := a.validatePluginSession(pluginID, sessionToken); err != nil {
		return err
	}
	if err := a.requireGrant(pluginID, plugins.CapOSNotify); err != nil {
		return err
	}
	return notifyDesktop(title, body)
}

var openNative = func(path string) error {
	switch goruntime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", "", path).Start()
	default: // linux + others
		return exec.Command("xdg-open", path).Start()
	}
}

func notifyDesktop(title, body string) error {
	switch goruntime.GOOS {
	case "darwin":
		return exec.Command("osascript",
			"-e", "on run argv",
			"-e", "display notification (item 2 of argv) with title (item 1 of argv)",
			"-e", "end run",
			title, body,
		).Start()
	case "windows":
		// PowerShell toast — universally available on Win10+. Title/body are
		// passed via environment variables, never as interpolated source.
		cmd := exec.Command("powershell", "-NoProfile", "-Command",
			"[reflection.assembly]::loadwithpartialname('System.Windows.Forms') > $null; "+
				"$t = New-Object System.Windows.Forms.NotifyIcon; "+
				"$t.Icon = [System.Drawing.SystemIcons]::Information; "+
				"$t.BalloonTipTitle = $env:SILT_NOTIFY_TITLE; "+
				"$t.BalloonTipText = $env:SILT_NOTIFY_BODY; "+
				"$t.Visible = $true; $t.ShowBalloonTip(5000);")
		cmd.Env = append(os.Environ(),
			"SILT_NOTIFY_TITLE="+title,
			"SILT_NOTIFY_BODY="+body,
		)
		return cmd.Start()
	default: // linux
		return exec.Command("notify-send", title, body).Start()
	}
}
