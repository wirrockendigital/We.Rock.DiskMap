package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// SchedulerTemplate is an inert, versioned invocation; it does not create or discover DSM tasks.
type SchedulerTemplate struct {
	TemplateVersion     int     `json:"templateVersion"`
	ProfileID           string  `json:"profileId"`
	Command             string  `json:"command"`
	Mode                string  `json:"mode"`
	ExitContractVersion int     `json:"exitContractVersion"`
	DSMBuild            *string `json:"dsmBuild"`
	Qualification       string  `json:"qualification"`
	ExecutionAccount    *string `json:"executionAccount"`
}

// SchedulerTemplate binds a fixed package command to an existing active stable profile ID.
func (m *Manager) SchedulerTemplate(ctx context.Context, id string) (SchedulerTemplate, error) {
	out := SchedulerTemplate{}
	if !security.ValidID(id) {
		return out, ErrInvalid
	}
	work, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var raw string
	if err := m.db.QueryRowContext(work, `SELECT profile_json FROM profiles WHERE id=? AND length(CAST(profile_json AS BLOB))<=65536`, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrProfileMissing
		}
		return out, ErrUnavailable
	}
	var p contracts.Profile
	if json.Unmarshal([]byte(raw), &p) != nil || p.ID != id {
		return out, ErrUnavailable
	}
	if !p.Active {
		return out, ErrInvalid
	}
	args := []string{"/var/packages/we-rock-diskmap/target/bin/diskmapctl", "--socket=/var/packages/we-rock-diskmap/var/run/control.sock", "--protocol-version=1", "--exit-contract-version=1", "scan", "--profile-id=" + id, "--mode=reconcile"}
	for i, arg := range args {
		args[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return SchedulerTemplate{TemplateVersion: 1, ProfileID: id, Command: strings.Join(args, " "), Mode: "reconcile", ExitContractVersion: 1, Qualification: "unknown"}, nil
}
