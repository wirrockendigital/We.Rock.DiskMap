package scanner

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// Traversal limits bound retained directory descriptors and batches, not the total node count.
const (
	TraversalBatchSize = 128
	TraversalMaxDepth  = 256
)

// ErrTraversalDepth marks an explicit coverage gap instead of a silently complete truncation.
var ErrTraversalDepth = errors.New("DEPTH_LIMIT")

// walkOperations exposes only metadata inspection and directory enumeration for focused auditing.
type walkOperations struct {
	// Profile entrypoints always supply a qualified value; standalone walker tests use the fixed ceiling.
	directoryBatch int
	inspect        func(*os.File, string, string) (*os.File, os.FileInfo, error)
	directory      func(*os.File, string, os.FileInfo, string) (*os.File, error)
	readDirectory  func(*os.File, int) ([]os.DirEntry, error)
	observe        func(openDirectories, retainedEntries int)
	guardDirectory func(string, *os.File) error
}

// walkNoFollow enumerates held directories without resolving mutable ancestor names again.
func walkNoFollow(ctx context.Context, directory *os.File, relative, mountID string, visit func(string, os.FileInfo, error) error) error {
	return walkMetadata(ctx, directory, relative, mountID, visit, walkOperations{inspect: inspectEntry, directory: openDirectory, readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) { return f.ReadDir(n) }})
}

// walkFrame contains one held ancestor and at most one bounded directory batch.
type walkFrame struct {
	file     *os.File
	relative string
	info     os.FileInfo
	entries  []os.DirEntry
	next     int
	readErr  error
	depth    int
}

// walkMetadata is the production iterative traversal, also used by syscall-instrumented tests.
// Only the caller-owned root remains open on return; all acquired children close on every exit.
func walkMetadata(ctx context.Context, directory *os.File, relative, mountID string, visit func(string, os.FileInfo, error) error, ops walkOperations) error {
	batch := ops.directoryBatch
	if batch == 0 {
		batch = TraversalBatchSize
	}
	if batch < 1 || batch > TraversalBatchSize {
		return errors.New("DIRECTORY_BATCH_LIMIT")
	}
	if err := Checkpoint(ctx); err != nil {
		return err
	}
	if ops.guardDirectory != nil {
		if e := ops.guardDirectory(relative, directory); e != nil {
			return visit(relative, nil, e)
		}
	}
	info, err := directory.Stat()
	if err != nil {
		return visit(relative, nil, err)
	}
	if err = visit(relative, info, nil); err != nil {
		if err == fs.SkipDir {
			return nil
		}
		return err
	}
	depth := 0
	if relative != "." {
		depth = strings.Count(relative, "/") + 1
	}
	if depth >= TraversalMaxDepth {
		return visit(relative, info, ErrTraversalDepth)
	}
	stack := []walkFrame{{file: directory, relative: relative, info: info, depth: depth}}
	defer func() {
		for i := 1; i < len(stack); i++ {
			stack[i].file.Close()
		}
	}()
	observe := func() {
		if ops.observe != nil {
			retained := 0
			for i := range stack {
				retained += len(stack[i].entries) - stack[i].next
			}
			ops.observe(len(stack), retained)
		}
	}
	pop := func() {
		last := len(stack) - 1
		if last > 0 {
			stack[last].file.Close()
		}
		stack[last] = walkFrame{}
		stack = stack[:last]
	}
	for len(stack) > 0 {
		if err = Checkpoint(ctx); err != nil {
			return err
		}
		frame := &stack[len(stack)-1]
		if frame.next == len(frame.entries) {
			// Drop the previous batch before acquiring another, including its consumed backing references.
			frame.entries = nil
			frame.next = 0
			if frame.readErr != nil {
				if frame.readErr != io.EOF {
					if err = visit(frame.relative, frame.info, frame.readErr); err != nil && err != fs.SkipDir {
						return err
					}
				}
				pop()
				continue
			}
			frame.entries, frame.readErr = ops.readDirectory(frame.file, batch)
			if len(frame.entries) > batch {
				return errors.New("DIRECTORY_BATCH_LIMIT")
			}
			observe()
			if len(frame.entries) == 0 {
				if frame.readErr == nil {
					if err = visit(frame.relative, frame.info, io.ErrNoProgress); err != nil && err != fs.SkipDir {
						return err
					}
					pop()
					continue
				}
				continue
			}
		}
		entry := frame.entries[frame.next]
		frame.entries[frame.next] = nil
		frame.next++
		rel := path.Join(frame.relative, entry.Name())
		if err = Checkpoint(ctx); err != nil {
			return err
		}
		handle, childInfo, e := ops.inspect(frame.file, entry.Name(), mountID)
		// The package account determines metadata visibility through the filesystem.
		if handle != nil {
			handle.Close()
		}
		if e != nil {
			// Only a proven mount mismatch carries metadata for exclusion matching.
			// Continue unconditionally afterwards: visitor success never grants traversal.
			if !errors.Is(e, errMountChanged) {
				childInfo = nil
			}
			if err = visit(rel, childInfo, e); err != nil && err != fs.SkipDir {
				return err
			}
			continue
		}
		// The same visitor decides exclusions before any readable child-directory open.
		if err = visit(rel, childInfo, nil); err != nil {
			if err == fs.SkipDir {
				continue
			}
			return err
		}
		if !childInfo.IsDir() {
			continue
		}
		childDepth := frame.depth + 1
		if childDepth >= TraversalMaxDepth {
			if err = visit(rel, childInfo, ErrTraversalDepth); err != nil && err != fs.SkipDir {
				return err
			}
			continue
		}
		if err = Checkpoint(ctx); err != nil {
			return err
		}
		child, e := ops.directory(frame.file, entry.Name(), childInfo, mountID)
		if e != nil {
			if err = visit(rel, childInfo, e); err != nil && err != fs.SkipDir {
				return err
			}
			continue
		}
		if ops.guardDirectory != nil {
			if e := ops.guardDirectory(rel, child); e != nil {
				child.Close()
				if err = visit(rel, childInfo, e); err != nil && err != fs.SkipDir {
					return err
				}
				continue
			}
		}
		stack = append(stack, walkFrame{file: child, relative: rel, info: childInfo, depth: childDepth})
		observe()
	}
	return nil
}
