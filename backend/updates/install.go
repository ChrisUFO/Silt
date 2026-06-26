package updates

import "errors"

// ErrSwapOKRelaunchFailed is returned by Install when the AppImage swap
// succeeded but relaunching the new version failed (e.g. exec permission
// denied on a noexec temp mount). The on-disk install is updated; the user is
// still running the old process and must relaunch manually. willQuit is false
// in this case so the app stays alive to surface the message.
var ErrSwapOKRelaunchFailed = errors.New("appimage updated but relaunch failed; restart manually")

// Install launches the verified local asset so it can replace the running
// binary. The bool return reports whether the caller should QUIT the app:
//   - true  (Windows NSIS, Linux AppImage in-place): a self-replacing
//     installer/relaunch was launched, so the app must exit for the upgrade
//     to complete (Windows: to release the locked binary; Linux: to avoid two
//     live instances). The caller quits via the graceful shutdown path so the
//     vault/WAL flush.
//   - false (Linux xdg-open hand-off): the asset was handed to the desktop's
//     default handler because Silt cannot self-replace a package-managed
//     install. The app stays running and the UI guides the user to place the
//     downloaded file manually.
//
// Install does NOT verify the asset itself — the caller (the App binding) must
// run VerifySHA256Sums first. Launching an unverified file is a security
// regression, so this ordering is load-bearing.
func Install(localPath string) (bool, error) {
	return installForCurrentOS(localPath)
}
