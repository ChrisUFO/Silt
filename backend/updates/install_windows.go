//go:build windows

package updates

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// installForCurrentOS launches the verified NSIS installer so it can replace
// the running binary. The Silt installer is per-user-only — RequestExecutionLevel
// user → an asInvoker manifest (build/windows/installer/project.nsi) — so it runs
// with the launching user's token and never needs elevation.
//
// ShellExecute with a nil verb launches the installer exactly as Explorer would
// on a double-click and is deliberately kept here even though asInvoker would
// also let os/exec's CreateProcess succeed: ShellExecute honours whatever
// execution level the installer ships, so this code never needs to change if the
// manifest decision is revisited (CreateProcess cannot satisfy a higher manifest
// and would fail silently again).
//
// Returns willQuit=true: the caller must quit Silt (via the graceful shutdown
// path) so the installer's file replacement does not collide with open handles.
// ShellExecute launches the installer via the Application Information Service
// rather than as a direct child, so it is already decoupled from Silt's
// lifetime — no CREATE_NEW_PROCESS_GROUP / Process.Release is needed.
func installForCurrentOS(localPath string) (bool, error) {
	file, err := windows.UTF16PtrFromString(localPath)
	if err != nil {
		return false, fmt.Errorf("launch installer: %w", err)
	}
	if err := windows.ShellExecute(
		0,                     // hwnd: no owner window
		nil,                   // verb: nil = default ("open"); manifest drives elevation
		file,                  // file: the verified installer
		nil,                   // args
		nil,                   // cwd
		windows.SW_SHOWNORMAL, // show the installer window
	); err != nil {
		// Any non-nil error means the installer did not launch — the user
		// declined the UAC prompt, the file vanished, or AIS rejected the
		// launch. The installer UI is the real feedback channel once it runs,
		// so we do not try to distinguish decline from other failures here.
		return false, fmt.Errorf("launch installer: %w", err)
	}
	return true, nil
}
