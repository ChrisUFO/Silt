//go:build windows

package db

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows network-filesystem names returned by GetVolumeInformationW.
var windowsNetworkFSNames = map[string]bool{
	"NFS":     true,
	"CDFS":    true, // CD-ROM (not network, but read-only)
	"DFS":     true, // Distributed File System
	"WebDAV":  true,
	"CBFS":    true,
}

// Common network filesystem names that contain SMB/CIFS in their identifier.
var windowsNetworkFSPrefixes = []string{"SMB", "CIFS"}

func detectNetworkFilesystem(path string) error {
	if len(path) == 0 {
		return nil
	}
	// GetVolumeInformationW needs a drive root or UNC path with a trailing backslash.
	volumePath := path
	if !strings.HasSuffix(volumePath, "\\") {
		volumePath += "\\"
	}

	pathUTF16, err := windows.UTF16PtrFromString(volumePath)
	if err != nil {
		return nil // If we can't convert, let sql.Open surface the real error
	}

	var fsName [256]uint16
	err = syscall.GetVolumeInformation(
		(*uint16)(unsafe.Pointer(pathUTF16)),
		nil, 0,        // volume name buffer + size
		nil,            // volume serial number
		nil,            // max component length
		nil,            // filesystem flags
		&fsName[0],     // filesystem name buffer
		uint32(len(fsName)),
	)
	if err != nil {
		return nil // If we can't query, let sql.Open surface the real error
	}

	fstype := windows.UTF16ToString(fsName[:])
	upper := strings.ToUpper(fstype)

	if windowsNetworkFSNames[upper] {
		return fmt.Errorf("%w: %s is on a network filesystem (%s). WAL mode requires shared memory, which network mounts do not provide. Move the vault to a local folder",
			ErrNetworkFilesystem, path, fstype)
	}
	for _, prefix := range windowsNetworkFSPrefixes {
		if strings.Contains(upper, prefix) {
			return fmt.Errorf("%w: %s is on a network filesystem (%s). WAL mode requires shared memory, which network mounts do not provide. Move the vault to a local folder",
				ErrNetworkFilesystem, path, fstype)
		}
	}
	return nil
}
