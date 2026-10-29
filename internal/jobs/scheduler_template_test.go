package jobs

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// Names never become shell input, and quoted arguments survive exact POSIX parsing.
func TestSchedulerTemplateStableQuotedInvocation(t *testing.T) {
	m := diagnosticFixture(t)
	m.db.Exec(`CREATE TABLE profiles(id TEXT PRIMARY KEY,profile_json TEXT)`)
	p := contracts.Profile{ID: "stable-ID_1", Name: "Grüße ' \" $(touch /tmp/forbidden) ;", Active: true}
	raw, _ := json.Marshal(p)
	m.db.Exec(`INSERT INTO profiles VALUES(?,?)`, p.ID, string(raw))
	first, e := m.SchedulerTemplate(context.Background(), p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(first.Command, p.Name) || first.ExecutionAccount != nil || first.Qualification != "unknown" {
		t.Fatal(first)
	}
	// Use set and printf to validate tokenization without invoking any package command.
	script := "set -- " + first.Command + "; printf '%s\\n' \"$@\""
	parsed, e := exec.Command("/bin/sh", "-c", script).Output()
	if e != nil {
		t.Fatal(e)
	}
	expected := []string{"/var/packages/we-rock-diskmap/target/bin/diskmapctl", "--socket=/var/packages/we-rock-diskmap/var/run/control.sock", "--protocol-version=1", "--exit-contract-version=1", "scan", "--profile-id=stable-ID_1", "--mode=reconcile"}
	if string(parsed) != strings.Join(expected, "\n")+"\n" {
		t.Fatal(string(parsed))
	}
	p.Name = "Renamed"
	raw, _ = json.Marshal(p)
	m.db.Exec(`UPDATE profiles SET profile_json=?`, string(raw))
	second, e := m.SchedulerTemplate(context.Background(), p.ID)
	if e != nil || second.Command != first.Command {
		t.Fatal(second, e)
	}
	p.Active = false
	raw, _ = json.Marshal(p)
	m.db.Exec(`UPDATE profiles SET profile_json=?`, string(raw))
	if _, e = m.SchedulerTemplate(context.Background(), p.ID); e != ErrInvalid {
		t.Fatal(e)
	}
	m.db.Exec(`DELETE FROM profiles`)
	if _, e = m.SchedulerTemplate(context.Background(), p.ID); e != ErrProfileMissing {
		t.Fatal(e)
	}
	for _, id := range []string{"x';echo hacked", "../x", "x.y", "name space", "Ünicode"} {
		if _, e = m.SchedulerTemplate(context.Background(), id); e != ErrInvalid {
			t.Fatal(id, e)
		}
	}
}
