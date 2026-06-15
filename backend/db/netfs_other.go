//go:build !linux && !darwin && !windows

package db

// detectNetworkFilesystem is a no-op on unsupported platforms. The WAL
// journal_mode assert in initSchema is the belt-and-suspenders fallback.
func detectNetworkFilesystem(_ string) error {
	return nil
}
