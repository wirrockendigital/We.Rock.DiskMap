// Package diagnostics writes a bounded vocabulary without accepting diagnostic free text.
package diagnostics

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

const SlotBytes = 256 * 1024
const EventBytes = 2048
const Retention = 7 * 24 * time.Hour

var ErrInvalid = errors.New("DIAGNOSTIC_EVENT_INVALID")
var ErrUnavailable = errors.New("DIAGNOSTIC_STORAGE_UNAVAILABLE")

// Event contains only classified states and unsigned measurements; it accepts no names or identifiers.
type Event struct {
	Code          string `json:"code"`
	Phase         string `json:"phase"`
	Count         uint64 `json:"count"`
	Warnings      uint64 `json:"warnings"`
	ElapsedMillis uint64 `json:"elapsedMillis"`
	Schema        uint64 `json:"schema"`
	Scanner       uint64 `json:"scanner"`
	Aggregate     uint64 `json:"aggregate"`
}
type record struct {
	Time time.Time `json:"time"`
	Event
}

// Logger pins its private directory and serializes all bounded file operations.
type Logger struct {
	mu    sync.Mutex
	root  *os.Root
	lock  *os.File
	clock func() time.Time
}

// New requires a pre-created private directory and rejects aliases or permissive inodes.
func New(dir string) (*Logger, error) {
	before, e := os.Lstat(dir)
	if e != nil || !private(before, true) {
		return nil, ErrUnavailable
	}
	root, e := os.OpenRoot(dir)
	if e != nil {
		return nil, ErrUnavailable
	}
	f, e := root.Open(".")
	if e != nil {
		root.Close()
		return nil, ErrUnavailable
	}
	after, e := f.Stat()
	if e != nil || !os.SameFile(before, after) || !private(after, true) {
		f.Close()
		root.Close()
		return nil, ErrUnavailable
	}
	// A distinct held directory descriptor owns the process-independent writer lock.
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		root.Close()
		return nil, ErrUnavailable
	}
	l := &Logger{root: root, lock: f, clock: time.Now}
	if e = l.Maintain(); e != nil {
		l.Close()
		return nil, e
	}
	return l, nil
}

// private validates ownership, exact permissions, object type and single-link files.
func private(info os.FileInfo, dir bool) bool {
	if info == nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return false
	}
	if dir {
		return info.IsDir() && info.Mode().Perm() == 0700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && st.Nlink == 1
}

// valid restricts persisted text to the public diagnostic vocabulary.
func valid(e Event) bool {
	switch e.Code {
	case "SERVICE_STARTING", "SERVICE_READY", "SERVICE_STOPPED", "SERVICE_FAILED", "SERVICE_STATUS", "QUERY_COMPLETED", "QUERY_FAILED", "QUERY_TIMEOUT":
	default:
		return false
	}
	switch e.Phase {
	case "idle", "unavailable", "queued", "preflight", "scanning", "aggregating", "validating", "publishing", "paused", "completed", "failed", "cancelled", "interrupted", "scope_batch", "scope_history", "scope_recycle", "map_catalog", "map_manifest", "map_chunks", "map_index", "hierarchy", "reader", "inventory":
		return true
	}
	return false
}

var names = [4]string{"slot-0.jsonl", "slot-1.jsonl", "slot-2.jsonl", "slot-3.jsonl"}

// open checks the held file before any truncation or append, refusing symlinks and special files.
func (l *Logger) open(name string) (*os.File, error) {
	f, e := l.root.OpenFile(name, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, ErrUnavailable
	}
	st, e := f.Stat()
	if e != nil || !private(st, false) || st.Size() > SlotBytes {
		f.Close()
		return nil, ErrUnavailable
	}
	return f, nil
}

// inspect validates every retained record; expiration drops a whole slot including any younger tail.
func (l *Logger) inspect(f *os.File, now time.Time) (int64, time.Time, error) {
	if _, e := f.Seek(0, 0); e != nil {
		return 0, time.Time{}, ErrUnavailable
	}
	scan := bufio.NewScanner(io.LimitReader(f, SlotBytes+1))
	scan.Buffer(make([]byte, EventBytes), EventBytes)
	var first time.Time
	for scan.Scan() {
		var r record
		decoder := json.NewDecoder(bytes.NewReader(scan.Bytes()))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&r) != nil || decoder.Decode(new(any)) != io.EOF || !validRetained(r.Event) || r.Time.IsZero() || r.Time.After(now) {
			return 0, first, ErrUnavailable
		}
		if first.IsZero() {
			first = r.Time
		}
		if now.Sub(r.Time) >= Retention {
			if f.Truncate(0) != nil {
				return 0, first, ErrUnavailable
			}
			return 0, time.Time{}, nil
		}
	}
	if scan.Err() != nil {
		return 0, first, ErrUnavailable
	}
	st, e := f.Stat()
	if e != nil {
		return 0, first, ErrUnavailable
	}
	// Reject a torn final record instead of concatenating another JSON object onto it.
	if st.Size() > 0 {
		var tail [1]byte
		if _, e := f.ReadAt(tail[:], st.Size()-1); e != nil || tail[0] != '\n' {
			return 0, first, ErrUnavailable
		}
	}
	return st.Size(), first, nil
}

// validRetained preserves the bounded historical vocabulary until retained slots expire.
// New writes still use valid, so retired query routes cannot produce new events.
func validRetained(e Event) bool {
	if e.Phase == "map_page" {
		e.Phase = "map_manifest"
	}
	return valid(e)
}

// operate performs bounded maintenance and optionally appends one complete event.
func (l *Logger) operate(data []byte) error {
	if l.root == nil {
		return ErrUnavailable
	}
	now := l.clock().UTC()
	var files [4]*os.File
	defer func() {
		for _, f := range files {
			if f != nil {
				f.Close()
			}
		}
	}()
	chosen := -1
	oldest := 0
	var oldestTime time.Time
	for i, name := range names {
		f, e := l.open(name)
		if e != nil {
			return e
		}
		files[i] = f
		size, first, e := l.inspect(f, now)
		if e != nil {
			return e
		}
		if chosen < 0 && size+int64(len(data)) <= SlotBytes {
			chosen = i
		}
		if i == 0 || first.Before(oldestTime) {
			oldest = i
			oldestTime = first
		}
	}
	if data == nil {
		return nil
	}
	if chosen < 0 {
		chosen = oldest
		if files[chosen].Truncate(0) != nil {
			return ErrUnavailable
		}
	}
	f := files[chosen]
	if _, e := f.Seek(0, io.SeekEnd); e != nil {
		return ErrUnavailable
	}
	if n, e := f.Write(data); e != nil || n != len(data) {
		return ErrUnavailable
	}
	if f.Sync() != nil {
		return ErrUnavailable
	}
	return nil
}

// Record validates before touching storage and never returns input-derived error text.
func (l *Logger) Record(e Event) error {
	if !valid(e) {
		return ErrInvalid
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := json.Marshal(record{Time: l.clock().UTC(), Event: e})
	if err != nil || len(data)+1 > EventBytes {
		return ErrInvalid
	}
	return l.operate(append(data, '\n'))
}

// Maintain expires old slots even when no event is recorded.
func (l *Logger) Maintain() error { l.mu.Lock(); defer l.mu.Unlock(); return l.operate(nil) }

// Close releases the directory handle; subsequent operations fail without reopening a path.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.root == nil {
		return nil
	}
	e := l.root.Close()
	l.root = nil
	// Close releases flock only after all accesses through the pinned root have ended.
	lockErr := l.lock.Close()
	l.lock = nil
	if lockErr != nil {
		return ErrUnavailable
	}
	if e != nil {
		return ErrUnavailable
	}
	return nil
}
