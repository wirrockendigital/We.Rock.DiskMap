package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
)

// ExclusionPreviewRequest previews proposed rules without saving or enumerating any source.
type ExclusionPreviewRequest struct {
	SyntaxOnly         bool                  `json:"syntaxOnly,omitempty"`
	PartialJobID       string                `json:"partialJobId,omitempty"`
	ContractVersion    int                   `json:"contractVersion"`
	ProfileID          string                `json:"profileId"`
	Exclusions         []contracts.Exclusion `json:"exclusions"`
	TemplateExclusions []contracts.Exclusion `json:"templateExclusions"`
}

// Validate checks routing and byte limits; rule syntax is reported as a useful preview result.
func (r ExclusionPreviewRequest) Validate() error {
	if r.ContractVersion != 1 || (r.SyntaxOnly && (r.ProfileID != "" || r.PartialJobID != "")) || (!r.SyntaxOnly && !identifier.MatchString(r.ProfileID)) || (r.PartialJobID != "" && !identifier.MatchString(r.PartialJobID)) {
		return ErrInvalid
	}
	raw, e := json.Marshal(r)
	if e != nil || len(raw) > 65536 {
		return ErrInvalid
	}
	return nil
}

// ExclusionMatch names only an existing index node; private paths are not added to diagnostics.
type ExclusionMatch struct {
	NodeID   string `json:"nodeId"`
	TargetID string `json:"targetId"`
}

// PartialPreviewOption identifies a persisted partial result without silently selecting it.
type PartialPreviewOption struct {
	JobID           string `json:"jobId"`
	ProfileRevision int    `json:"profileRevision"`
	EndedAt         string `json:"endedAt"`
}

// ExclusionPreview reports bounded observations, never a forecast of unindexed source data.
type ExclusionPreview struct {
	AvailablePartial        *PartialPreviewOption          `json:"availablePartial"`
	SyntaxVersion           int                            `json:"syntaxVersion"`
	Valid                   bool                           `json:"valid"`
	SyntaxError             *profiles.ExclusionSyntaxError `json:"syntaxError,omitempty"`
	EffectiveExclusions     []contracts.Exclusion          `json:"effectiveExclusions"`
	Warnings                []string                       `json:"warnings"`
	ProfileRevision         int                            `json:"profileRevision"`
	SnapshotID              *string                        `json:"snapshotId"`
	SnapshotProfileRevision *int                           `json:"snapshotProfileRevision"`
	SnapshotState           string                         `json:"snapshotState"`
	Examined                int                            `json:"examined"`
	Matched                 int                            `json:"matched"`
	Matches                 []ExclusionMatch               `json:"matches"`
	Truncated               bool                           `json:"truncated"`
	MatchesTruncated        bool                           `json:"matchesTruncated"`
}

// PreviewExclusions pins the active index and reads at most 1000 nodes with a three-second budget.
func (m *Manager) PreviewExclusions(ctx context.Context, in ExclusionPreviewRequest) (ExclusionPreview, error) {
	out := ExclusionPreview{SyntaxVersion: profiles.ExclusionSyntaxVersion, EffectiveExclusions: []contracts.Exclusion{}, Warnings: []string{"EXCLUSIONS_WIN", "RULES_APPLY_TO_EACH_NORMALIZED_ROOT", "EXISTING_INDEX_ONLY"}, Matches: []ExclusionMatch{}, SnapshotState: "none"}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if in.Validate() != nil {
		return out, ErrInvalid
	}
	effective, err := profiles.EffectiveExclusions(in.TemplateExclusions, in.Exclusions)
	if err != nil {
		var syntax *profiles.ExclusionSyntaxError
		if errors.As(err, &syntax) {
			out.SyntaxError = syntax
			return out, nil
		}
		return out, ErrInvalid
	}
	out.Valid = true
	out.EffectiveExclusions = effective
	if len(effective) < len(in.TemplateExclusions)+len(in.Exclusions) {
		out.Warnings = append(out.Warnings, "DUPLICATE_RULES_MERGED")
	}
	// Canonical escaping is part of the response budget, even for syntax-only requests.
	raw, _ := json.Marshal(out)
	if len(raw) > 48000 {
		return out, ErrInvalid
	}
	if in.SyntaxOnly {
		out.Warnings = append(out.Warnings, "SYNTAX_ONLY")
		return out, nil
	}
	matcher, _ := profiles.CompileExclusions(effective)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return out, ErrUnavailable
	}
	snapshot, err := m.activeGeneration(ctx, in.ProfileID)
	if err != nil {
		return out, err
	}
	if err = m.db.QueryRowContext(ctx, `SELECT revision FROM profiles WHERE id=?`, in.ProfileID).Scan(&out.ProfileRevision); err != nil {
		return out, err
	}
	// Discovery is bounded to one persisted result; actual selection requalifies its generation below.
	var partialRaw string
	pe := m.db.QueryRowContext(ctx, `SELECT outcome_json FROM jobs WHERE json_extract(outcome_json,'$.job.profileId')=? AND state='completed' AND json_extract(outcome_json,'$.snapshotState')='partial' ORDER BY rowid DESC LIMIT 1`, in.ProfileID).Scan(&partialRaw)
	if pe != nil && !errors.Is(pe, sql.ErrNoRows) {
		return out, pe
	}
	if pe == nil {
		var candidate Outcome
		if len(partialRaw) > 65536 || json.Unmarshal([]byte(partialRaw), &candidate) != nil {
			return out, ErrUnavailable
		}
		if candidate.Job.ProfileID == in.ProfileID && identifier.MatchString(candidate.Job.ID) && candidate.Job.ProfileRevision > 0 && candidate.Job.ResultSnapshotID != nil && candidate.SnapshotEndedAt != nil {
			out.AvailablePartial = &PartialPreviewOption{candidate.Job.ID, candidate.Job.ProfileRevision, *candidate.SnapshotEndedAt}
		}
	}
	var selected *Outcome
	if in.PartialJobID != "" {
		var raw string
		e := m.db.QueryRowContext(ctx, "SELECT outcome_json FROM jobs WHERE id=? AND json_extract(outcome_json,'$.job.profileId')=?", in.PartialJobID, in.ProfileID).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) {
			return out, ErrNotFound
		}
		if e != nil {
			return out, e
		}
		var candidate Outcome
		if json.Unmarshal([]byte(raw), &candidate) != nil || candidate.Job.ID != in.PartialJobID || candidate.Job.ProfileID != in.ProfileID || candidate.Job.State != "completed" || candidate.SnapshotState != "partial" || candidate.Job.ResultSnapshotID == nil || candidate.SnapshotEndedAt == nil {
			return out, ErrScanResultUnavailable
		}
		selected = &candidate
		snapshot = *candidate.Job.ResultSnapshotID
	}
	if snapshot == "" {
		return out, nil
	}
	db, close, err := m.generationDB(snapshot)
	if err != nil {
		return out, err
	}
	defer close()
	header, err := index.ReadGenerationHeader(ctx, db, snapshot)
	if err != nil || header.Profile.ID != in.ProfileID {
		return out, ErrGenerationRefresh
	}
	if selected != nil {
		if header.State != "partial" || header.JobID != selected.Job.ID || header.Profile.Revision != selected.Job.ProfileRevision || header.EndedAt != *selected.SnapshotEndedAt {
			return out, ErrScanResultUnavailable
		}
	} else if header.State != "complete" {
		return out, ErrUnavailable
	}
	out.SnapshotID = &snapshot
	out.SnapshotProfileRevision = &header.Profile.Revision
	out.SnapshotState = header.State
	// Reuse a bounded directory-path cache while examining the preview limit.
	reader, err := index.NewNodeReader(ctx, db)
	if err != nil {
		return out, err
	}
	after := int64(0)
	for out.Examined <= 1000 {
		records, readErr := reader.Page(after, 128)
		if readErr != nil {
			return out, readErr
		}
		if len(records) == 0 {
			break
		}
		for _, record := range records {
			if out.Examined == 1000 {
				out.Truncated = true
				return out, nil
			}
			after = record.RowID
			if len(record.Path) > 65536 {
				return out, ErrUnavailable
			}
			out.Examined++
			matched, matchErr := matcher.MatchContext(ctx, string(record.Path), record.Node.Kind == "directory")
			if matchErr != nil {
				return out, ErrUnavailable
			}
			if matched {
				out.Matched++
				if len(out.Matches) < 50 {
					out.Matches = append(out.Matches, ExclusionMatch{record.Node.ID, record.TargetID})
				} else {
					out.MatchesTruncated = true
				}
			}
		}
	}
	return out, nil
}
