package jobs

import "testing"

// TestGenerationReaderPolicy verifies the actual held-file reader used by leased production queries.
func TestGenerationReaderPolicy(t *testing.T) {
	m, in, _ := fixture(t)
	if err := m.ImportProfile(in); err != nil {
		t.Fatal(err)
	}
	job, err := m.Start("probe", "full")
	if err != nil {
		t.Fatal(err)
	}
	done := terminal(t, m, job.Job.ID)
	if done.Job.ResultSnapshotID == nil {
		t.Fatal("missing generation")
	}
	db, release, err := m.generationDB(*done.Job.ResultSnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, tc := range []struct {
		pragma string
		want   int
	}{{"query_only", 1}, {"cache_size", -8192}, {"temp_store", 1}, {"temp.cache_size", -2048}, {"mmap_size", 0}} {
		var actual int
		if err = db.QueryRow("PRAGMA " + tc.pragma).Scan(&actual); err != nil || actual != tc.want {
			t.Fatalf("%s=%d want%d err=%v", tc.pragma, actual, tc.want, err)
		}
	}
}
