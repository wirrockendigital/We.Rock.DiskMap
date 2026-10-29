package scanner

import "io/fs"

// metadataKind classifies observed type bits without opening any object contents.
func metadataKind(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "directory"
	case mode.IsRegular():
		return "file"
	case mode&fs.ModeSymlink != 0:
		return "symlink"
	case mode&fs.ModeNamedPipe != 0:
		return "fifo"
	case mode&fs.ModeSocket != 0:
		return "socket"
	case mode&fs.ModeDevice != 0 && mode&fs.ModeCharDevice != 0:
		return "characterDevice"
	case mode&fs.ModeDevice != 0:
		return "blockDevice"
	default:
		return ""
	}
}
