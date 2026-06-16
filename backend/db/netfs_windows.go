//go:build windows

package db

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// Windows network-filesystem names returned by GetVolumeInformationW.
var windowsNetworkFSNames = map[string]bool{
	"NFS":    true,
	"DFS":    true, // Distributed File System
	"WebDAV": true,
	"CBFS":   true,
}

// Common network filesystem names that contain SMB/CIFS in their identifier.
var windowsNetworkFSPrefixes = []string{"SMB", "CIFS"}

func detectNetworkFilesystem(path string) error {
	if len(path) == 0 {
		return nil
	}

	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil
	}

	// GetVolumeInformationW requires a volume root path (e.g. "C:\"), not an
	// arbitrary subdirectory. Resolve the volume root first via
	// GetVolumePathName (#79 review fix).
	var volumeRoot [260]uint16
	err = windows.GetVolumePathName(pathUTF16, &volumeRoot[0], uint32(len(volumeRoot)))
	if err != nil {
		return nil
	}

	var fsName [256]uint16
	err = windows.GetVolumeInformation(
		&volumeRoot[0],
		nil, 0,
		nil,
		nil,
		nil,
		&fsName[0],
		uint32(len(fsName)),
	)
	if err != nil {
		return nil
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
