package jobs

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"path"
	"strings"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/diagnostics"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// ScopeObservationRequest reads stable share-relative identities independently of the selected view.
type ScopeObservationRequest struct {
	ContractVersion int             `json:"contractVersion"`
	Scopes          []ObservedScope `json:"scopes"`
}
type ObservedScope struct {
	ShareID string `json:"shareId"`
	Path    string `json:"path"`
}
type ScopeObservation struct {
	// Explicit JSON fields keep strict response validation unambiguous.
	ShareID      string                   `json:"shareId"`
	Path         string                   `json:"path"`
	RecycleBytes *contracts.Bytes         `json:"recycleBytes"`
	Bytes        *contracts.Bytes         `json:"bytes"`
	EndedAt      *string                  `json:"endedAt"`
	State        string                   `json:"state"`
	SnapshotID   string                   `json:"snapshotId"`
	Reasons      []contracts.ScanBoundary `json:"reasons"`
	// Contained gaps can be replaced by a newer subtree; ancestor gaps cannot.
	containedReasonCount            int
	containedUnreadableRecycleCount int
	containedUnreadableSystemCount  int
	coverageUnknown                 bool
	unreadableRecycleCount          int
	unreadableSystemCount           int
	UnreadableRecycle               bool `json:"unreadableRecycle"`
	UnreadableSystem                bool `json:"unreadableSystem"`
	ReasonCount                     int  `json:"reasonCount"`
}

// Validate bounds both the request and all path traversals before opening a generation.
func (r ScopeObservationRequest) Validate() error {
	if r.ContractVersion != 1 || len(r.Scopes) < 1 || len(r.Scopes) > 64 {
		return ErrInvalid
	}
	for _, s := range r.Scopes {
		if !security.ValidID(s.ShareID) || !contracts.ValidFolderPath(s.Path, true) {
			return ErrInvalid
		}
	}
	return nil
}

// ScopeObservations selects the latest completed observation, not the currently displayed profile.
func (m *Manager) ScopeObservations(ctx context.Context, in ScopeObservationRequest) ([]ScopeObservation, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	finishHistory := diagnostics.Measure(ctx, "scope_history", uint64(len(in.Scopes)))
	snapshots, err := retryControlRead(ctx, func() ([]string, error) {
		return m.scopeHistory(ctx)
	})
	finishHistory(err)
	if err != nil {
		return nil, err
	}
	fragments := make([][]scopeFragment, len(in.Scopes))
	out := make([]ScopeObservation, len(in.Scopes))
	for i, s := range in.Scopes {
		out[i] = ScopeObservation{ShareID: s.ShareID, Path: s.Path, State: "unscanned", Reasons: []contracts.ScanBoundary{}}
	}
	for _, snapshot := range snapshots {
		db, close, err := m.generationDB(snapshot)
		if err != nil {
			continue
		}
		h, e := index.ReadGenerationHeader(ctx, db, snapshot)
		if e == nil {
			// Unrelated generations cannot contribute metadata or replacement fragments.
			relevant := false
			for i, s := range in.Scopes {
				if out[i].SnapshotID != "" {
					continue
				}
				for _, root := range h.Manifest.Provenance.Roots {
					if root.SourceID != nil && *root.SourceID == s.ShareID {
						relevant = true
					}
					for _, area := range root.Areas {
						if area.SourceID != nil && *area.SourceID == s.ShareID && area.Qualification == "qualified" {
							relevant = true
						}
					}
				}
			}
			if !relevant {
				close()
				continue
			}
			var recycling []recycleObservation
			recycling, e = m.cachedRecycleObservations(ctx, db, h.SnapshotID)
			if e != nil {
				close()
				return nil, e
			}
			for i, s := range in.Scopes {
				if out[i].SnapshotID != "" {
					continue
				}
				fragments[i] = append(fragments[i], capturedSubscopes(h, s)...)
				v, found, e := observeScope(ctx, db, h, s, recycling)
				if e != nil {
					close()
					return nil, e
				}
				if found {
					v, e = m.composeObservation(ctx, s, v, fragments[i])
					if e != nil {
						close()
						return nil, e
					}
					out[i] = v
				}
			}
		}
		close()
		if e != nil {
			return nil, e
		}
		// Stop once the entire batch is qualified; older generations cannot change these answers.
		complete := true
		for _, v := range out {
			if v.SnapshotID == "" {
				complete = false
				break
			}
		}
		if complete {
			break
		}
	}
	// Without an encompassing scan, expose only known disjoint portions and keep coverage partial.
	for i, s := range in.Scopes {
		if out[i].SnapshotID == "" && len(fragments[i]) > 0 {
			zero := contracts.Bytes("0")
			base := out[i]
			base.Bytes = &zero
			base.State = "partial"
			base.coverageUnknown = true
			v, e := m.composeObservation(ctx, s, base, fragments[i])
			if e != nil {
				return nil, e
			}
			out[i] = v
		}
	}
	return out, nil
}

// observeScope resolves through recorded native source authority; no live file paths are opened.
func observeScope(ctx context.Context, db *sql.DB, h index.GenerationHeader, s ObservedScope, recycling []recycleObservation) (ScopeObservation, bool, error) {
	out := ScopeObservation{ShareID: s.ShareID, Path: s.Path, State: "unscanned", Reasons: []contracts.ScanBoundary{}}
	for _, root := range h.Manifest.Provenance.Roots {
		target, relative, matched := root.TargetID, s.Path, false
		prefix, areaPrefix := ".", ""
		if root.SourceID != nil && *root.SourceID == s.ShareID && root.Kind == "share" {
			prefix = "."
			for _, r := range h.Manifest.Roots {
				if r.TargetID == target && r.RelativePath != "" {
					prefix = r.RelativePath
				}
			}
			if prefix == "." {
				matched = true
			} else if s.Path == prefix {
				relative = "."
				matched = true
			} else if strings.HasPrefix(s.Path, prefix+"/") {
				relative = strings.TrimPrefix(s.Path, prefix+"/")
				matched = true
			}
		}
		for _, a := range root.Areas {
			if a.SourceID != nil && *a.SourceID == s.ShareID && a.Qualification == "qualified" {
				areaPrefix = string(a.RelativePath)
				relative = areaPrefix
				if s.Path != "." {
					relative += "/" + s.Path
				}
				matched = true
			}
		}
		if !matched {
			continue
		}
		n, err := index.FindNode(ctx, db, target, []byte(relative))
		var a contracts.DirectoryAggregate
		if errors.Is(err, sql.ErrNoRows) {
			// Missing recorded roots still carry their boundary reasons; do not borrow older names.
			a.Subtree.Logical.KnownBytes = "0"
			out.coverageUnknown = true
		} else {
			if err != nil {
				return out, false, err
			}
			a, err = index.ReadDirectoryAggregate(ctx, db, n.Node.ID)
			if err != nil {
				return out, false, err
			}
		}

		b := a.Subtree.Logical.KnownBytes
		// Keep recycle totals separate while system metadata remains part of normal usage.
		recycled := contracts.Bytes(recycleBytes(recycling, target, relative, s.Path, string(b)))
		normal, _ := new(big.Int).SetString(string(b), 10)
		r, _ := new(big.Int).SetString(string(recycled), 10)
		// Inside a recycle tree, its own contents are the selected scope, not an amount to subtract.
		if !insideRecycleScope(s.Path) {
			normal.Sub(normal, r)
		}
		if normal.Sign() < 0 {
			return out, false, index.ErrGeneration
		}
		b = contracts.Bytes(normal.String())
		out.RecycleBytes = &recycled
		out.Bytes = &b
		out.EndedAt = &h.EndedAt
		out.SnapshotID = h.SnapshotID
		out.State = "complete"
		if out.coverageUnknown {
			out.State = "partial"
			out.Bytes = nil
		}
		rows, err := db.QueryContext(ctx, `SELECT path_bytes,reason_code FROM scan_boundaries WHERE target_id=?`, target)
		if err != nil {
			return out, false, err
		}
		// Translate recorded-root paths into stable share-relative boundary identities.
		boundaryPath := func(p string) string {
			if areaPrefix != "" {
				if p == areaPrefix {
					return "."
				}
				if strings.HasPrefix(p, areaPrefix+"/") {
					return strings.TrimPrefix(p, areaPrefix+"/")
				}
				return "."
			}
			return path.Join(prefix, p)
		}
		reset := false
		for rows.Next() {
			var raw []byte
			var code string
			if err = rows.Scan(&raw, &code); err != nil {
				break
			}
			p := string(raw)
			// Preserve unknown sizes even when an intentional exclusion is not a coverage error.
			intersects := relative == "." || p == "." || p == relative || strings.HasPrefix(p, relative+"/") || strings.HasPrefix(relative, p+"/")
			if intersects {
				// Aggregate category failures before the bounded detail list can truncate them.
				if code == "SOURCE_UNREADABLE" {
					markUnreadableCategory(&out, p+"/"+s.Path)
					if containsScope(relative, p) {
						var categories ScopeObservation
						markUnreadableCategory(&categories, p+"/"+s.Path)
						out.containedUnreadableRecycleCount += categories.unreadableRecycleCount
						out.containedUnreadableSystemCount += categories.unreadableSystemCount
					}
				}
				if a.Subtree.Logical.KnownBytes == "0" {
					out.Bytes = nil
				}
				for _, part := range strings.Split(p, "/") {
					if strings.EqualFold(part, "#recycle") {
						out.RecycleBytes = nil
						break
					}
				}
			}
			// A gap affects ancestors and descendants, never unrelated siblings.
			if index.BoundaryIsGap(code) && (relative == "." || p == "." || p == relative || strings.HasPrefix(p, relative+"/") || strings.HasPrefix(relative, p+"/")) {
				out.State = "partial"
				if code == "INDEX_RESET" && (p == "." || p == relative || strings.HasPrefix(relative, p+"/")) {
					out.State = "unscanned"
					reset = true
					out.Bytes = nil
					out.RecycleBytes = nil
					out.EndedAt = nil
				}
				out.ReasonCount++
				if containsScope(relative, p) {
					out.containedReasonCount++
				}
				if len(out.Reasons) < 16 {
					out.Reasons = append(out.Reasons, contracts.ScanBoundary{TargetID: target, RelativePath: boundaryPath(p), ReasonCode: code})
				}
			}
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return out, false, err
		}
		for _, gap := range h.EndValidation {
			if gap.TargetID == target {
				if containsScope(s.Path, prefix) {
					out.containedReasonCount++
				}
				out.State = "partial"
				out.ReasonCount++
				if len(out.Reasons) < 16 {
					out.Reasons = append(out.Reasons, contracts.ScanBoundary{TargetID: target, RelativePath: prefix, ReasonCode: gap.Code})
				}
			}
		}
		if out.ReasonCount > 0 {
			out.coverageUnknown = false
		}
		if reset {
			out.State = "unscanned"
		}
		return out, true, nil
	}
	return out, false, nil
}

// Classify permission failures using the same category names as the map and navigation.
func markUnreadableCategory(out *ScopeObservation, path string) {
	recycle, system := false, false
	for _, part := range strings.Split(strings.ToLower(path), "/") {
		recycle = recycle || part == "#recycle"
		system = system || part == "@eadir" || part == ".ds_store" || part == ".appledouble" || part == ".spotlight-v100" || part == ".trashes" || part == ".fseventsd" || strings.HasPrefix(part, ".smb") || strings.HasPrefix(part, "._")
	}
	if recycle {
		out.unreadableRecycleCount++
		out.UnreadableRecycle = true
	}
	if system {
		out.unreadableSystemCount++
		out.UnreadableSystem = true
	}
}

// scopeHistory releases every row before a bounded retry of transient control-database contention.
func (m *Manager) scopeHistory(ctx context.Context) ([]string, error) {
	rows, err := m.readDB.QueryContext(ctx, `SELECT json_extract(j.outcome_json,'$.job.resultSnapshotId') FROM jobs j JOIN profiles p ON p.id=json_extract(j.outcome_json,'$.job.profileId') WHERE j.state='completed' AND json_extract(j.outcome_json,'$.snapshotState') IN ('complete','partial') ORDER BY json_extract(j.outcome_json,'$.snapshotEndedAt') DESC`)
	if err != nil {
		return nil, err
	}
	// Historical subtree generations remain eligible; project only the identity actually consumed.
	var snapshots []string
	for rows.Next() {
		var snapshot sql.NullString
		if err = rows.Scan(&snapshot); err != nil {
			break
		}
		if snapshot.Valid {
			snapshots = append(snapshots, snapshot.String)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	err = errors.Join(err, rows.Close())
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}
