//go:build windows

package updates

import (
	"fmt"
	"os/exec"
	"syscall"
)

// installForCurrentOS launches the verified NSIS installer detached from the
// Silt process so it survives the app's exit and can replace the locked
// binary files. The installer is a GUI process that prompts the user through
// the upgrade. Returns willQuit=true: the caller must quit Silt (via the
// graceful shutdown path) so the installer's file replacement does not collide
// with open handles.
//
// CREATE_NEW_PROCESS_GROUP (0x00000200) decouples the child from the parent's
// process group, so a Ctrl-C / window-close on Silt does not propagate to the
// installer mid-upgrade.
func installForCurrentOS(localPath string) (bool, error) {
	cmd := exec.Command(localPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000200}
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("launch installer: %w", err)
	}
	// Release the child so it is not reaped when Silt exits; the installer
	// runs to completion independently.
	_ = cmd.Process.Release()
	return true, nil
}
