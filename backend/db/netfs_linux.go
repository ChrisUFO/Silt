//go:build linux

package db

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// Linux network-filesystem magic numbers (from linux/magic.h). WAL requires
// shared memory which network mounts do not provide.
var linuxNetworkFSMagic = map[int64]bool{
	0x6969:     true, // NFS_SUPER_MAGIC
	0xFF534D42: true, // CIFS_MAGIC_NUMBER (SMB/Samba)
	0xFE534D42: true, // SMB2_MAGIC_NUMBER
	0x517B:     true, // SMB_SUPER_MAGIC (legacy)
	0x564C:     true, // NCP_SUPER_MAGIC (NetWare)
}

func detectNetworkFilesystem(path string) error {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return nil // If we can't stat, let sql.Open surface the real error
	}
	if linuxNetworkFSMagic[int64(stat.Type)] {
		return fmt.Errorf("%w: %s is on a network filesystem (type 0x%x). WAL mode requires shared memory, which network mounts do not provide. Move the vault to a local folder",
			ErrNetworkFilesystem, path, stat.Type)
	}
	return nil
}
