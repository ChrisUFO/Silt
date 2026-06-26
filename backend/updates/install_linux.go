//go:build linux

package updates

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// installForCurrentOS replaces the running AppImage in place and relaunches
// it. Linux permits renaming over a running file: the live process keeps its
// mapped inode, so the swap is atomic from the user's perspective. After the
// rename, the new AppImage is relaunched and Silt returns willQuit=true: the
// caller must quit so the old instance exits and only the new version runs.
//
// If $APPIMAGE is unset (the user runs the .deb build, a bare binary, etc.),
// the verified asset is opened with xdg-open so the user's file manager /
// software center handles placement — Silt cannot self-replace a package-managed
// install safely, so it returns willQuit=false and the UI guides the user.
func installForCurrentOS(localPath string) (bool, error) {
	appImage := os.Getenv("APPIMAGE")
	if appImage == "" {
		return false, openWithXdg(localPath)
	}

	// The new AppImage must be executable to relaunch.
	if err := os.Chmod(localPath, 0o755); err != nil {
		return false, fmt.Errorf("chmod new AppImage: %w", err)
	}
	// Atomically swap the running AppImage for the new one. replaceFile writes
	// to a sibling temp on the SAME filesystem as $APPIMAGE then renames, so an
	// interruption (crash, disk-full, kill) never leaves a corrupt, un-runnable
	// AppImage at the install path — the live file is replaced only by a
	// complete, verified copy.
	if err := replaceFile(appImage, localPath); err != nil {
		return false, fmt.Errorf("replace AppImage: %w", err)
	}

	// Relaunch the new version detached, then let the caller quit. If the
	// relaunch fails AFTER the swap succeeded, the on-disk AppImage is already
	// the new version but the user is still running the old one (its inode is
	// mapped). Returning willQuit=false + ErrSwapOKRelaunchFailed keeps the
	// app alive so the UI can tell the user to relaunch manually — quitting
	// here would leave them with no running instance and a confusing error.
	cmd := exec.Command(appImage)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return false, fmt.Errorf("%w: relaunch AppImage: %v", ErrSwapOKRelaunchFailed, err)
	}
	_ = cmd.Process.Release()
	return true, nil
}

// openWithXdg hands the downloaded asset to the desktop's default handler
// (file manager / software center). This is the non-AppImage fallback.
func openWithXdg(localPath string) error {
	abs, err := filepath.Abs(localPath)
	if err != nil {
		return fmt.Errorf("resolve asset path: %w", err)
	}
	if err := exec.Command("xdg-open", abs).Start(); err != nil {
		return fmt.Errorf("xdg-open asset: %w", err)
	}
	return nil
}

// replaceFile atomically installs src at dst. It first tries an in-place
// os.Rename (atomic when src and dst share a filesystem). If that fails —
// typically EXDEV because the OS temp dir is on a different mount than the
// AppImage install path — it copies src into a sibling temp file IN dst's
// directory (same filesystem) and renames that over dst. The sibling-temp
// step keeps the swap atomic even cross-device: dst is never observed in a
// half-written state, so a crash mid-copy cannot corrupt the running AppImage.
func replaceFile(dst, src string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	dir := filepath.Dir(dst)
	sibling, err := os.CreateTemp(dir, ".silt-update-*.AppImage")
	if err != nil {
		return fmt.Errorf("create sibling temp: %w", err)
	}
	siblingPath := sibling.Name()
	// If the copy or the final rename fails, remove the sibling temp so no
	// partial file litters the install directory.
	defer os.Remove(siblingPath)

	in, err := os.Open(src)
	if err != nil {
		sibling.Close()
		return err
	}
	defer in.Close()
	if _, err := io.Copy(sibling, in); err != nil {
		sibling.Close()
		return fmt.Errorf("copy to sibling temp: %w", err)
	}
	if err := sibling.Chmod(0o755); err != nil {
		sibling.Close()
		return fmt.Errorf("chmod sibling temp: %w", err)
	}
	if err := sibling.Close(); err != nil {
		return fmt.Errorf("close sibling temp: %w", err)
	}
	if err := os.Rename(siblingPath, dst); err != nil {
		return fmt.Errorf("rename sibling over AppImage: %w", err)
	}
	// The cross-device copy left the original downloaded temp (src) in place;
	// remove it so repeated updates don't litter the OS temp dir. Best-effort:
	// a failure here doesn't undo the successful swap.
	_ = os.Remove(src)
	return nil
}
