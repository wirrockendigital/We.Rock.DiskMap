package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestCheckpointWaitsForBlockedMetadata proves no next inspection starts after pause acknowledgement.
func TestCheckpointWaitsForBlockedMetadata(t *testing.T) {
	source, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "file"), []byte("metadata only"), 0600); err != nil {
		t.Fatal(err)
	}
	root, binding, err := OpenBoundRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	blocked := make(chan struct{})
	releaseIO := make(chan struct{})
	paused := make(chan struct{})
	resume := make(chan struct{})
	done := make(chan error, 1)
	var request atomic.Bool
	var inspections atomic.Int32
	var acknowledged atomic.Bool
	ctx := WithCheckpoint(context.Background(), func() error {
		if request.Load() {
			if acknowledged.CompareAndSwap(false, true) {
				close(paused)
			}
			<-resume
		}
		return nil
	})
	var first atomic.Bool
	ops := walkOperations{inspect: func(f *os.File, name, mount string) (*os.File, os.FileInfo, error) {
		inspections.Add(1)
		return inspectEntry(f, name, mount)
	}, directory: openDirectory, readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) {
		if first.CompareAndSwap(false, true) {
			close(blocked)
			<-releaseIO
		}
		return f.ReadDir(n)
	}}
	go func() {
		done <- walkMetadata(ctx, directory, ".", binding.MountID, func(_ string, _ os.FileInfo, e error) error { return e }, ops)
	}()
	<-blocked
	request.Store(true)
	select {
	case <-paused:
		t.Fatal("claimed pause while syscall blocked")
	default:
	}
	close(releaseIO)
	select {
	case <-paused:
	case <-time.After(3 * time.Second):
		t.Fatal("pause not acknowledged")
	}
	if inspections.Load() != 0 {
		t.Fatal("metadata inspection crossed reached pause")
	}
	request.Store(false)
	close(resume)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if inspections.Load() != 1 {
		t.Fatal(inspections.Load())
	}
}

// TestCancellationDuringBlockedMetadata waits for the outstanding call, then starts no new inspection.
func TestCancellationDuringBlockedMetadata(t *testing.T) {
	source, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(source, "file"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	root, binding, err := OpenBoundRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	blocked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	ops := walkOperations{inspect: func(f *os.File, name, mount string) (*os.File, os.FileInfo, error) {
		calls.Add(1)
		return inspectEntry(f, name, mount)
	}, directory: openDirectory, readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) { close(blocked); <-release; return f.ReadDir(n) }}
	go func() {
		done <- walkMetadata(ctx, directory, ".", binding.MountID, func(_ string, _ os.FileInfo, e error) error { return e }, ops)
	}()
	<-blocked
	cancel()
	select {
	case <-done:
		t.Fatal("pretended to interrupt blocked syscall")
	default:
	}
	close(release)
	if err = <-done; err != context.Canceled {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("inspection started after abort")
	}
}
