package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A pending check or changed file must not be reported as proven corruption or a usable complete index.
func TestStoredDataIntegrityDimensions(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	id := scanRetention(t, m, in.Profile.ID)
	read := func() StoredDataStatus {
		m.mu.Lock()
		defer m.mu.Unlock()
		got, err := m.storedDataStatus(context.Background(), in.Profile.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for _, quality := range []string{"unverified", "unsupported", "corrupt", "deleting", "deleted"} {
		m.integrityStates.Store(id, integrityGuard{quality: quality})
		got := read()
		want := quality
		if quality == "deleting" || quality == "deleted" {
			want = "missing"
		}
		if got.Quality != want || got.Freshness != "unknown" || got.EndedAt != nil {
			t.Fatal(quality, got)
		}
	}
	// A healthy verdict becomes unusable when the exact sealed file changes.
	file := filepath.Join(m.state, "generations", id, "index.db")
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	m.integrityStates.Store(id, integrityGuard{quality: "healthy", stamp: retainedStamp(info)})
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte("changed")); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	got := read()
	if got.Quality != "unverified" || got.EndedAt != nil {
		t.Fatal(got)
	}
}

// TestStoredFreshnessLargeThreshold never overflows a valid integer configuration into a negative duration.
func TestStoredFreshnessLargeThreshold(t *testing.T) {
	m, in, _ := fixture(t)
	in.Profile.FreshnessSeconds = int(^uint(0) >> 1)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	s, err := m.Start(in.Profile.ID, "full")
	if err != nil {
		t.Fatal(err)
	}
	terminal(t, m, s.Job.ID)
	// Persist the same valid profile threshold explicitly, independent of import defaults.
	raw, err := json.Marshal(in.Profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.db.Exec(`UPDATE profiles SET profile_json=? WHERE id=?`, string(raw), in.Profile.ID); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	got, err := m.storedDataStatus(context.Background(), in.Profile.ID)
	m.mu.Unlock()
	if err != nil || got.Quality != "complete" || got.Freshness != "current" || got.AgeSeconds == nil {
		t.Fatal(got, err)
	}
}
