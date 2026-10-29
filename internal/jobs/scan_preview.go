package jobs

import (
	"context"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// ScanPreview is disposable scan feedback, never a generation or an action locator.
type ScanPreview struct {
	JobID     string           `json:"jobId"`
	ProfileID string           `json:"profileId"`
	Preview   *scanner.Preview `json:"preview"`
}
type scanPreviewSlot struct {
	job, profile, principal string
	revision                int
	value                   scanner.Preview
}

// rememberPreviewOwner retains only pending browser submissions, without persisting identity.
// The caller holds the manager lock and invokes this only after a committed new submission.
func (m *Manager) rememberPreviewOwner(o Outcome) {
	if m.previewOwners == nil {
		m.previewOwners = map[string]string{}
	}
	for id := range m.previewOwners {
		var state string
		if m.db.QueryRow(`SELECT state FROM jobs WHERE id=?`, id).Scan(&state) != nil || state != "queued" {
			delete(m.previewOwners, id)
		}
	}
	if o.PreviewPrincipal != "" && len(m.previewOwners) < QueueCapacity+1 {
		m.previewOwners[o.Job.ID] = o.PreviewPrincipal
	}
}

// capturePreview copies one bounded observer value; no SQL or filesystem operation occurs.
func (m *Manager) capturePreview(o Outcome, p scanner.Preview) {
	if o.PreviewPrincipal == "" || p.Sequence == 0 || contracts.Bytes(p.Entries).Validate() != nil || contracts.Bytes(p.KnownLogicalBytes).Validate() != nil || len(p.Segments) > 64 || len(p.CapturedAt) > 64 || len(p.Entries) > 160 || len(p.KnownLogicalBytes) > 160 {
		return
	}
	for _, s := range p.Segments {
		if len(s.ID) == 0 || len(s.ID) > 128 || len(s.Name) > 256 || len(s.Path) > 768 || (s.Kind != "file" && s.Kind != "directory" && s.Kind != "group") || len(s.Bytes) > 160 || contracts.Bytes(s.Bytes).Validate() != nil {
			return
		}
	}
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > 120*1024 {
		return
	}
	p.Segments = append([]scanner.PreviewSegment(nil), p.Segments...)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil || m.active == nil || m.active.Job.ID != o.Job.ID || m.runner != nil && m.runner.id == o.Job.ID && m.runner.ctx.Err() != nil {
		return
	}
	if m.preview != nil && m.preview.job == o.Job.ID && p.Sequence <= m.preview.value.Sequence {
		return
	}
	m.preview = &scanPreviewSlot{job: o.Job.ID, profile: o.Job.ProfileID, revision: o.Job.ProfileRevision, principal: o.PreviewPrincipal, value: p}
}

// ReadScanPreview verifies current ownership and profile revision against control metadata only.
func (m *Manager) ReadScanPreview(ctx context.Context, principal, job, profile string) (ScanPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out := ScanPreview{JobID: job, ProfileID: profile}
	if !validPrincipal(principal) || !identifier.MatchString(job) || !identifier.MatchString(profile) {
		return out, ErrInvalid
	}
	m.mu.Lock()
	slot := m.preview
	if slot == nil {
		m.mu.Unlock()
		return out, nil
	}
	if m.closed || m.ctx.Err() != nil || m.active == nil || m.active.Job.ID != slot.job || m.runner != nil && m.runner.id == slot.job && m.runner.ctx.Err() != nil {
		m.preview = nil
		m.mu.Unlock()
		return out, nil
	}
	if slot.job != job || slot.profile != profile || slot.principal != principal || m.active.Job.ProfileRevision != slot.revision {
		m.mu.Unlock()
		return out, nil
	}
	m.mu.Unlock()
	// Control metadata observation never holds the scanner's RAM publication lock.
	var revision int
	var state string
	err := m.readDB.QueryRowContext(ctx, `SELECT json_extract(p.profile_json,'$.revision'),j.state FROM profiles p JOIN jobs j ON j.id=? WHERE p.id=?`, slot.job, slot.profile).Scan(&revision, &state)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.preview != slot {
		return out, nil
	}
	if err != nil || revision != slot.revision || terminalJob(state) || m.closed || m.ctx.Err() != nil || m.active == nil || m.active.Job.ID != slot.job || m.runner != nil && m.runner.id == slot.job && m.runner.ctx.Err() != nil {
		m.preview = nil
		return out, nil
	}
	value := slot.value
	value.Segments = append([]scanner.PreviewSegment(nil), value.Segments...)
	out.Preview = &value
	return out, nil
}
