//go:build !windows && !linux

package updates

// installForCurrentOS returns ErrPlatformNotSupported on any OS without an
// install path (e.g. darwin, which has no build leg). Build-tagged siblings
// override this on windows/linux. willQuit is false because nothing launched.
func installForCurrentOS(localPath string) (bool, error) {
	return false, ErrPlatformNotSupported
}
