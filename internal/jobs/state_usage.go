package jobs

import (
	"context"
	"io"
	"math/big"
	"os"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"golang.org/x/sys/unix"
)

// packageUsage measures regular package files only; directories, links and sockets are not payloads.
// The result is a timed metadata observation, not a consistent filesystem snapshot.
func packageUsage(ctx context.Context, state string) contracts.PackageUsage {
	return measurePackageUsage(ctx, state, 50000, 500*time.Millisecond)
}

// measurePackageUsage holds no-follow directory descriptors and bounds work independently of source scans.
func measurePackageUsage(ctx context.Context, state string, limit int, budget time.Duration) contracts.PackageUsage {
	out := contracts.PackageUsage{State: "unavailable", Method: "package-state-stat"}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	root, binding, err := scanner.OpenBoundRoot(state)
	if err != nil {
		out.MeasuredAt = time.Now().UTC().Format(time.RFC3339Nano)
		return out
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		out.MeasuredAt = time.Now().UTC().Format(time.RFC3339Nano)
		return out
	}
	type frame struct {
		file    *os.File
		entries []os.DirEntry
		next    int
		end     error
	}
	stack := []frame{{file: dir}}
	defer func() {
		for _, f := range stack {
			f.file.Close()
		}
	}()
	logical, allocated := new(big.Int), new(big.Int)
	complete, seen := true, 0
	for len(stack) > 0 {
		if ctx.Err() != nil || seen >= limit {
			complete = false
			break
		}
		f := &stack[len(stack)-1]
		if f.next == len(f.entries) {
			f.entries = nil
			f.next = 0
			if f.end != nil {
				if f.end != io.EOF {
					complete = false
				}
				f.file.Close()
				stack = stack[:len(stack)-1]
				continue
			}
			f.entries, f.end = f.file.ReadDir(128)
			if len(f.entries) == 0 {
				if f.end == nil {
					complete = false
					break
				}
				continue
			}
		}
		entry := f.entries[f.next]
		f.entries[f.next] = nil
		f.next++
		seen++
		var stat unix.Stat_t
		// fstatat never opens a regular file, FIFO, socket, device or symlink for reading.
		if unix.Fstatat(int(f.file.Fd()), entry.Name(), &stat, unix.AT_SYMLINK_NOFOLLOW) != nil {
			complete = false
			continue
		}
		switch stat.Mode & unix.S_IFMT {
		case unix.S_IFREG:
			if stat.Size < 0 || stat.Blocks < 0 {
				complete = false
				continue
			}
			logical.Add(logical, big.NewInt(stat.Size))
			allocated.Add(allocated, new(big.Int).Mul(big.NewInt(stat.Blocks), big.NewInt(512)))
		case unix.S_IFDIR:
			if len(stack) >= 64 {
				complete = false
				continue
			}
			fd, e := unix.Openat(int(f.file.Fd()), entry.Name(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if e != nil {
				complete = false
				continue
			}
			child := os.NewFile(uintptr(fd), "package-directory")
			var observed unix.Stat_t
			mount, me := scanner.MountIdentity(child)
			if unix.Fstat(fd, &observed) != nil || observed.Dev != stat.Dev || observed.Ino != stat.Ino || me != nil || mount != binding.MountID {
				child.Close()
				complete = false
				continue
			}
			stack = append(stack, frame{file: child})
		}
	}
	out.MeasuredAt = time.Now().UTC().Format(time.RFC3339Nano)
	out.State = "partial"
	if complete {
		l, a := contracts.Bytes(logical.String()), contracts.Bytes(allocated.String())
		out.LogicalBytes, out.AllocatedBytes, out.State = &l, &a, "complete"
	}
	return out
}
