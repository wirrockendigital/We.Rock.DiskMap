package nativefixture

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/jobs"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/service"
)

// registrationFixture initializes real Control state and transfers a small independently sealed fixture.
func registrationFixture(t *testing.T) registrationOptions {
	t.Helper()
	scratch, e := os.MkdirTemp("/tmp", "nfr-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(scratch) })
	base, e := filepath.EvalSymlinks(scratch)
	if e != nil {
		t.Fatal(e)
	}
	state := filepath.Join(base, "state")
	m, e := jobs.Open(context.Background(), state)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	db, e := index.Open(filepath.Join(state, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	// An unrelated pointer is deliberately retained byte-for-byte by registration and cleanup.
	if _, e = db.Exec(`INSERT INTO active_generations VALUES('existing-profile','existing-snapshot')`); e != nil {
		t.Fatal(e)
	}
	db.Close()
	o := smallOptions(t)
	o.Output = filepath.Join(state, "generations", "fixture")
	r, e := transfer(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	return registrationOptions{State: state, Backup: filepath.Join(base, "control-before.db"), Snapshot: r.SnapshotID, Profile: r.ProfileID, Job: r.JobID, SHA: r.OutputSHA}
}

// TestRegistrationReaderLeaseChain uses real Manager authorization, hierarchy and navigation, then exact reversal.
func TestRegistrationReaderLeaseChain(t *testing.T) {
	o := registrationFixture(t)
	ctx := context.Background()
	r, e := registerFixture(ctx, o)
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Baseline) < 16 || r.BackupSHA == "" {
		t.Fatal("missing complete baseline", r)
	}
	m, e := jobs.Open(ctx, o.State)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = registerFixture(ctx, o); e == nil {
		t.Fatal("live daemon lock ignored")
	}
	view, e := m.OpenView(ctx, "admin", o.Profile, "view", "", false)
	if e != nil || view.Lease == nil || view.View.CapturedProfileName == nil || *view.View.CapturedProfileName == "" {
		t.Fatal(view, e)
	}
	q := jobs.HierarchyRequest{ContractVersion: 1, ProfileID: o.Profile, ViewID: "view", Query: query.Request{SnapshotID: o.Snapshot, RootID: "nas", Metric: "logical", Depth: 3, ElementBudget: 100, Filter: query.Filter{Categories: []string{}}}}
	if _, e = m.ReadHierarchy(ctx, "admin", q, false); e != nil {
		t.Fatal(e)
	}
	if _, e = m.ReadHierarchy(ctx, "foreign", q, false); e == nil {
		t.Fatal("foreign lease accepted")
	}
	if _, e = m.ReadNavigation(ctx, "admin", jobs.NavigationRequest{ContractVersion: 1, ProfileID: o.Profile, ViewID: "view", SnapshotID: o.Snapshot, RootID: "nas"}); e != nil {
		t.Fatal(e)
	}
	if _, e = m.Start(o.Profile, "full"); e == nil {
		t.Fatal("disabled fixture scan accepted")
	}
	if e = m.ReleaseView("admin", o.Profile, "view"); e != nil {
		t.Fatal(e)
	}
	if _, e = m.ReadHierarchy(ctx, "admin", q, false); e == nil {
		t.Fatal("released lease accepted")
	}
	if e = m.Close(); e != nil {
		t.Fatal(e)
	}
	if e = unregisterFixture(ctx, r); e != nil {
		t.Fatal(e)
	}
	// Reversal preserves the immutable generation for the caller's explicit cleanup and hash proof.
	if _, e = os.Stat(filepath.Join(o.State, "generations", o.Snapshot, "index.db")); e != nil {
		t.Fatal(e)
	}
}

// TestRegistrationServiceUnixChain exercises the production service's kernel peer and principal boundaries.
// The installed DSM CGI/auth layer remains a separate native gate; no substitute CGI is implemented here.
func TestRegistrationServiceUnixChain(t *testing.T) {
	o := registrationFixture(t)
	if _, e := registerFixture(context.Background(), o); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	socket := filepath.Join(o.State, "run", "control.sock")
	go func() { done <- service.Run(ctx, socket) }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(5 * time.Second):
			t.Error("service shutdown timeout")
		}
	}()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, e := client.Get("http://unix/api/v1/health")
		if e == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("service did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	call := func(route, principal string, in any) (int, []byte) {
		t.Helper()
		raw, _ := json.Marshal(in)
		req, e := http.NewRequest("POST", "http://unix"+route, bytes.NewReader(raw))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("X-DiskMap-Principal", principal)
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if e != nil {
			t.Fatal(e)
		}
		return resp.StatusCode, b
	}
	status, b := call("/api/v1/views", "admin", contracts.ViewRequest{ContractVersion: 1, Operation: "open", ProfileID: o.Profile, ViewID: "service-view"})
	if status != 200 {
		t.Fatal(status, string(b))
	}
	q := jobs.HierarchyRequest{ContractVersion: 1, ProfileID: o.Profile, ViewID: "service-view", Query: query.Request{SnapshotID: o.Snapshot, RootID: "nas", Metric: "logical", Depth: 3, ElementBudget: 100, Filter: query.Filter{Categories: []string{}}}}
	if status, b = call("/api/v1/hierarchy", "admin", q); status != 200 {
		t.Fatal(status, string(b))
	}
	if status, _ = call("/api/v1/hierarchy", "other", q); status == 200 {
		t.Fatal("foreign principal accepted")
	}
	nav := jobs.NavigationRequest{ContractVersion: 1, ProfileID: o.Profile, ViewID: "service-view", SnapshotID: o.Snapshot, RootID: "nas"}
	if status, b = call("/api/v1/views/navigation", "admin", nav); status != 200 {
		t.Fatal(status, string(b))
	}
	nav.SnapshotID = "wrong"
	if status, _ = call("/api/v1/views/navigation", "admin", nav); status == 200 {
		t.Fatal("wrong snapshot accepted")
	}
}

// TestRegistrationRejectsForeignOrChangedBinding checks fail-closed admission and cleanup.
func TestRegistrationRejectsForeignOrChangedBinding(t *testing.T) {
	for _, kind := range []string{"profile", "job", "sha", "collision"} {
		t.Run(kind, func(t *testing.T) {
			o := registrationFixture(t)
			switch kind {
			case "profile":
				o.Profile = "other"
			case "job":
				o.Job = "other"
			case "sha":
				o.SHA = "0000000000000000000000000000000000000000000000000000000000000000"
			case "collision":
				if _, e := registerFixture(context.Background(), o); e != nil {
					t.Fatal(e)
				}
				o.Backup += "2"
			}
			if _, e := registerFixture(context.Background(), o); e == nil {
				t.Fatal("invalid registration accepted")
			}
		})
	}
	t.Run("cleanup changed outcome", func(t *testing.T) {
		o := registrationFixture(t)
		r, e := registerFixture(context.Background(), o)
		if e != nil {
			t.Fatal(e)
		}
		db, e := index.Open(filepath.Join(o.State, "control.db"))
		if e != nil {
			t.Fatal(e)
		}
		_, e = db.Exec("UPDATE jobs SET outcome_json='{}' WHERE id=?", o.Job)
		db.Close()
		if e != nil {
			t.Fatal(e)
		}
		if e = unregisterFixture(context.Background(), r); e == nil {
			t.Fatal("changed outcome deleted")
		}
	})
}

// TestRegistrationAtomicFailure proves late write failure cannot expose a partial imported profile or pointer.
func TestRegistrationAtomicFailure(t *testing.T) {
	o := registrationFixture(t)
	db, e := index.Open(filepath.Join(o.State, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`CREATE TRIGGER registration_fault BEFORE INSERT ON integrity_generations BEGIN SELECT RAISE(ABORT,'fixture fault'); END`); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if _, e = registerFixture(context.Background(), o); e == nil {
		t.Fatal("late failure accepted")
	}
	db, e = index.Open(filepath.Join(o.State, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for table, p := range recordPredicates(registrationReceipt{Profile: o.Profile, Job: o.Job, Snapshot: o.Snapshot}) {
		var n int
		if e = db.QueryRow("SELECT count(*) FROM "+quoted(table)+" WHERE "+quoted(p.field)+"=?", p.value).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
	var preserved string
	if e = db.QueryRow(`SELECT snapshot_id FROM active_generations WHERE profile_id='existing-profile'`).Scan(&preserved); e != nil || preserved != "existing-snapshot" {
		t.Fatal(preserved, e)
	}
}

// TestRegistrationCleanupGuards rejects a changed backup or integrity record before deleting any fixture records.
func TestRegistrationCleanupGuards(t *testing.T) {
	for _, kind := range []string{"backup", "integrity"} {
		t.Run(kind, func(t *testing.T) {
			o := registrationFixture(t)
			r, e := registerFixture(context.Background(), o)
			if e != nil {
				t.Fatal(e)
			}
			if kind == "backup" {
				if e = os.WriteFile(o.Backup, []byte("changed"), 0600); e != nil {
					t.Fatal(e)
				}
			} else {
				db, e := index.Open(filepath.Join(o.State, "control.db"))
				if e != nil {
					t.Fatal(e)
				}
				_, e = db.Exec(`UPDATE integrity_generations SET quality='corrupt' WHERE snapshot_id=?`, o.Snapshot)
				db.Close()
				if e != nil {
					t.Fatal(e)
				}
			}
			if e = unregisterFixture(context.Background(), r); e == nil {
				t.Fatal("changed evidence accepted")
			}
			db, e := index.Open(filepath.Join(o.State, "control.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var n int
			if e = db.QueryRow(`SELECT count(*) FROM profiles WHERE id=?`, o.Profile).Scan(&n); e != nil || n != 1 {
				t.Fatal(n, e)
			}
		})
	}
}

// TestRegistrationForeignTriggerRollback checks foreign mutations are rejected before either commit.
func TestRegistrationForeignTriggerRollback(t *testing.T) {
	for _, operation := range []string{"INSERT", "DELETE"} {
		t.Run(operation, func(t *testing.T) {
			o := registrationFixture(t)
			ctx := context.Background()
			var receipt registrationReceipt
			var e error
			if operation == "DELETE" {
				receipt, e = registerFixture(ctx, o)
				if e != nil {
					t.Fatal(e)
				}
			}
			db, e := index.Open(filepath.Join(o.State, "control.db"))
			if e != nil {
				t.Fatal(e)
			}
			_, e = db.Exec("CREATE TRIGGER foreign_effect AFTER " + operation + " ON jobs BEGIN UPDATE active_generations SET snapshot_id='wrong' WHERE profile_id='existing-profile'; END")
			db.Close()
			if e != nil {
				t.Fatal(e)
			}
			if operation == "INSERT" {
				_, e = registerFixture(ctx, o)
			} else {
				e = unregisterFixture(ctx, receipt)
			}
			if e == nil {
				t.Fatal("foreign mutation committed")
			}
			db, e = index.Open(filepath.Join(o.State, "control.db"))
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			var value string
			if e = db.QueryRow("SELECT snapshot_id FROM active_generations WHERE profile_id='existing-profile'").Scan(&value); e != nil || value != "existing-snapshot" {
				t.Fatal(value, e)
			}
			want := 0
			if operation == "DELETE" {
				want = 1
			}
			for table, p := range recordPredicates(registrationReceipt{Profile: o.Profile, Job: o.Job, Snapshot: o.Snapshot}) {
				var n int
				if e = db.QueryRow("SELECT count(*) FROM "+quoted(table)+" WHERE "+quoted(p.field)+"=?", p.value).Scan(&n); e != nil || n != want {
					t.Fatal(table, n, e)
				}
			}
		})
	}
}

// TestRegistrationReceiptFile exercises the exact hash-qualified opt-in cleanup input.
func TestRegistrationReceiptFile(t *testing.T) {
	o := registrationFixture(t)
	r, e := registerFixture(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(filepath.Dir(o.State), "receipt.json")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	decoded, e := readRegistrationReceipt(path, hash)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = readRegistrationReceipt(path, "0000000000000000000000000000000000000000000000000000000000000000"); e == nil {
		t.Fatal("wrong hash accepted")
	}
	if e = unregisterFixture(context.Background(), decoded); e != nil {
		t.Fatal(e)
	}
}

// TestRegistrationOwnTriggerRollback prevents an excluded own row from falsifying the success receipt.
func TestRegistrationOwnTriggerRollback(t *testing.T) {
	o := registrationFixture(t)
	db, e := index.Open(filepath.Join(o.State, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(`CREATE TRIGGER own_effect AFTER INSERT ON jobs BEGIN UPDATE jobs SET outcome_json='{}' WHERE id=NEW.id; END`)
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = registerFixture(context.Background(), o); e == nil {
		t.Fatal("mutated receipt accepted")
	}
	db, e = index.Open(filepath.Join(o.State, "control.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for table, p := range recordPredicates(registrationReceipt{Profile: o.Profile, Job: o.Job, Snapshot: o.Snapshot}) {
		var n int
		if e = db.QueryRow("SELECT count(*) FROM "+quoted(table)+" WHERE "+quoted(p.field)+"=?", p.value).Scan(&n); e != nil || n != 0 {
			t.Fatal(table, n, e)
		}
	}
}
