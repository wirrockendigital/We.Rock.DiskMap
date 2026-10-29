package diagnostics

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixture uses only an isolated private directory and a deterministic clock.
func fixture(t *testing.T) *Logger {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	l, e := New(dir)
	if e != nil {
		t.Fatal(e)
	}
	l.clock = func() time.Time { return time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { l.Close() })
	return l
}
func sample() Event {
	return Event{Code: "SERVICE_STATUS", Phase: "scanning", Count: math.MaxUint64, Warnings: math.MaxUint64, ElapsedMillis: math.MaxUint64, Schema: 7, Scanner: 5, Aggregate: 1}
}

// Upgrades must retain valid historical query records without accepting new retired events.
func TestReopenRetainedMapPage(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	event := Event{Code: "QUERY_COMPLETED", Phase: "map_page"}
	data, err := json.Marshal(record{Time: time.Now().UTC().Add(-time.Minute), Event: event})
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := filepath.Join(dir, names[0])
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	l, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Record(event); err != ErrInvalid {
		t.Fatalf("retired write: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, data) {
		t.Fatal("retained evidence changed", err)
	}
	if err := l.Record(Event{Code: "SERVICE_READY", Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
}

// Typed high counters survive JSON without floating-point conversion or private text.
func TestRecordBoundsAndVocabulary(t *testing.T) {
	l := fixture(t)
	if e := l.Record(sample()); e != nil {
		t.Fatal(e)
	}
	raw, e := l.root.ReadFile(names[0])
	if e != nil {
		t.Fatal(e)
	}
	if len(raw) > EventBytes {
		t.Fatal(len(raw))
	}
	var r record
	if json.Unmarshal(raw, &r) != nil || r.Count != math.MaxUint64 {
		t.Fatal(string(raw))
	}
	before := append([]byte(nil), raw...)
	for _, e := range []Event{{Code: "secret/path"}, {Code: "SERVICE_READY", Phase: "private-name"}} {
		if got := l.Record(e); got != ErrInvalid || strings.Contains(got.Error(), "private") {
			t.Fatal(got)
		}
	}
	after, _ := l.root.ReadFile(names[0])
	if !bytes.Equal(before, after) {
		t.Fatal("invalid input changed storage")
	}
}

// Full slots rotate without growing the total payload beyond four fixed byte budgets.
func TestFullRotationAndExpiry(t *testing.T) {
	l := fixture(t)
	row, _ := json.Marshal(record{Time: l.clock(), Event: sample()})
	row = append(row, '\n')
	n := SlotBytes / len(row)
	for _, name := range names {
		if e := l.root.WriteFile(name, bytes.Repeat(row, n), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if e := l.Record(sample()); e != nil {
		t.Fatal(e)
	}
	var total int
	for _, name := range names {
		b, _ := l.root.ReadFile(name)
		total += len(b)
		if len(b) > SlotBytes {
			t.Fatal(len(b))
		}
	}
	if total > 4*SlotBytes {
		t.Fatal(total)
	}
	now := l.clock()
	l.clock = func() time.Time { return now.Add(Retention) }
	if e := l.Maintain(); e != nil {
		t.Fatal(e)
	}
	for _, name := range names {
		b, _ := l.root.ReadFile(name)
		if len(b) != 0 {
			t.Fatal("expired slot retained")
		}
	}
}

// Unsafe files remain untouched; all reported errors are fixed redacted sentinels.
func TestUnsafeSlotsAndDirectory(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "permission", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			os.Chmod(dir, 0700)
			outside := filepath.Join(t.TempDir(), "sentinel")
			os.WriteFile(outside, []byte("private-source"), 0600)
			slot := filepath.Join(dir, names[0])
			switch kind {
			case "symlink":
				os.Symlink(outside, slot)
			case "hardlink":
				os.Link(outside, slot)
			case "permission":
				os.WriteFile(slot, nil, 0644)
			case "oversize":
				os.WriteFile(slot, make([]byte, SlotBytes+1), 0600)
			}
			l, e := New(dir)
			if l != nil {
				l.Close()
			}
			if e != ErrUnavailable {
				t.Fatal(e)
			}
			b, _ := os.ReadFile(outside)
			if string(b) != "private-source" {
				t.Fatal("sentinel changed")
			}
		})
	}
	dir := t.TempDir()
	os.Chmod(dir, 0755)
	if l, e := New(dir); e != ErrUnavailable {
		if l != nil {
			l.Close()
		}
		t.Fatal(e)
	}
}

// A closed logger never reopens its previously pinned directory.
func TestClosed(t *testing.T) {
	l := fixture(t)
	l.Close()
	if l.Record(sample()) != ErrUnavailable || l.Maintain() != ErrUnavailable {
		t.Fatal("closed logger accepted work")
	}
}

// Corrupt or injected persisted records are never silently accepted as legitimate events.
func TestMalformedRecords(t *testing.T) {
	for _, kind := range []string{"torn", "unknown", "future", "large", "trailing-json", "trailing-garbage"} {
		t.Run(kind, func(t *testing.T) {
			l := fixture(t)
			r := record{Time: l.clock(), Event: sample()}
			if kind == "future" {
				r.Time = r.Time.Add(time.Hour)
			}
			raw, _ := json.Marshal(r)
			switch kind {
			case "unknown":
				raw = append(raw[:len(raw)-1], []byte(`,"message":"private-text"}`)...)
			case "trailing-json":
				raw = append(raw, []byte(` {"private":"secret"}`)...)
			case "trailing-garbage":
				raw = append(raw, []byte(` garbage`)...)
			case "large":
				raw = []byte(strings.Repeat("x", EventBytes+1))
			}
			if kind != "torn" {
				raw = append(raw, '\n')
			}
			if e := l.root.WriteFile(names[0], raw, 0600); e != nil {
				t.Fatal(e)
			}
			if e := l.Maintain(); e != ErrUnavailable {
				t.Fatal(e)
			}
		})
	}
}

// An independent logger cannot write the same directory until its previous owner closes.
func TestExclusiveDirectoryLock(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	first, e := New(dir)
	if e != nil {
		t.Fatal(e)
	}
	second, e := New(dir)
	if e != ErrUnavailable || second != nil {
		t.Fatal("second writer admitted", e)
	}
	if e = first.Close(); e != nil {
		t.Fatal(e)
	}
	third, e := New(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer third.Close()
	if e = third.Record(sample()); e != nil {
		t.Fatal(e)
	}
}

// Record and maintenance share one mutex while other contenders fail without modifying slots.
func TestConcurrentRecordsMaintenanceAndLockContenders(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	l, e := New(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				switch i % 3 {
				case 0:
					if e := l.Record(sample()); e != nil {
						t.Error(e)
					}
				case 1:
					if e := l.Maintain(); e != nil {
						t.Error(e)
					}
				case 2:
					other, e := New(dir)
					if other != nil {
						other.Close()
						t.Error("concurrent writer admitted")
					}
					if e != ErrUnavailable {
						t.Error(e)
					}
				}
			}
		}(i)
	}
	wg.Wait()
	raw, e := l.root.ReadFile(names[0])
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Count(raw, []byte("\n")) != 40 {
		t.Fatal("lost or interleaved records")
	}
}
