//go:build darwin

package db

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

// macOS network-filesystem type names reported by statfs.
var darwinNetworkFSTypes = map[string]bool{
	"nfs":   true,
	"nfs4":  true,
	"smbfs": true,
	"smb2":  true,
	"cifs":  true,
	"afpfs": true,
	"9p":    true,
	"webdav": true,
}

func detectNetworkFilesystem(path string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return nil // If we can't stat, let sql.Open surface the real error
	}
	fstype := strings.TrimRight(string(bytesFromInt8(stat.Fstypename[:])), "\x00")
	if darwinNetworkFSTypes[strings.ToLower(fstype)] {
		return fmt.Errorf("%w: %s is on a network filesystem (%s). WAL mode requires shared memory, which network mounts do not provide. Move the vault to a local folder",
			ErrNetworkFilesystem, path, fstype)
	}
	return nil
}

func bytesFromInt8(b []int8) []byte {
	out := make([]byte, len(b))
	for i, v := range b {
		out[i] = byte(v)
	}
	return out
}
