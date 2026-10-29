//go:build linux

package scanner

import (
	"encoding/binary"
	"errors"
	"os"
	"strconv"
	"unsafe"

	"golang.org/x/sys/unix"
)

// NativeSubvolumeID uses the read-only INO_LOOKUP ABI qualified on DSM build 90080.
// Linux UAPI btrfs.h: _IOWR(0x94,18,{u64 treeid,u64 objectid,char name[4080]}).
// treeid=0 and objectid=256 request the containing tree ID without arbitrary inode lookup.
func NativeSubvolumeID(file *os.File) (string, bool, error) {
	var stat unix.Statfs_t
	if err := unix.Fstatfs(int(file.Fd()), &stat); err != nil {
		return "", false, err
	}
	if uint64(stat.Type) != 0x9123683e {
		return "", false, nil
	}
	var buffer [4096]byte
	binary.LittleEndian.PutUint64(buffer[8:16], 256)
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, file.Fd(), uintptr(0xd0009412), uintptr(unsafe.Pointer(&buffer[0])))
	if errno != 0 {
		return "", true, errno
	}
	id := binary.LittleEndian.Uint64(buffer[:8])
	if id == 0 {
		return "", true, errors.New("SUBVOLUME_ID_UNAVAILABLE")
	}
	return strconv.FormatUint(id, 10), true, nil
}
