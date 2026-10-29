package scanner

import (
	"errors"
	"io"
	"io/fs"
	"syscall"
)

// Identity failures use stable sentinels; raw operating-system diagnostics stay private.
var errSourceChanged = errors.New("SOURCE_CHANGED")
var errMountChanged = errors.New("MOUNT_CHANGED")

// sourceReason classifies observed metadata failures, never inferring an empty source.
func sourceReason(err error) string {
	var boundary *boundaryError
	switch {
	case errors.As(err, &boundary):
		return boundary.code
	case errors.Is(err, errSourceChanged):
		return "SOURCE_CHANGED"
	case errors.Is(err, errMountChanged):
		return "OUTSIDE_SCOPE_MOUNT"
	case errors.Is(err, ErrTraversalDepth):
		return "DEPTH_LIMIT"
	case errors.Is(err, fs.ErrPermission):
		return "SOURCE_UNREADABLE"
	case errors.Is(err, fs.ErrNotExist):
		return "SOURCE_MISSING"
	case errors.Is(err, syscall.ESTALE), errors.Is(err, syscall.EAGAIN), errors.Is(err, syscall.EINTR), errors.Is(err, syscall.ETIMEDOUT):
		return "SOURCE_TRANSIENT"
	case errors.Is(err, syscall.ENOTDIR), errors.Is(err, syscall.ELOOP):
		return "SOURCE_CHANGED"
	case errors.Is(err, io.ErrNoProgress):
		return "SOURCE_IO_ERROR"
	default:
		return "SOURCE_IO_ERROR"
	}
}

// sourceStateReason preserves the native inventory state separately from syscall observations.
func sourceStateReason(state string) string {
	switch state {
	case "locked":
		return "SOURCE_LOCKED"
	case "unmounted":
		return "SOURCE_NOT_MOUNTED"
	case "unreadable":
		return "SOURCE_UNREADABLE"
	case "missing":
		return "SOURCE_MISSING"
	default:
		return "SOURCE_UNVERIFIED"
	}
}
