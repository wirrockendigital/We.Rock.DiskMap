package query

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"strconv"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// SourceAvailability contains historical observations that have no summable indexed subtree.
type SourceAvailability struct {
	Total     contracts.Bytes            `json:"total"`
	Truncated bool                       `json:"truncated"`
	Records   []SourceAvailabilityRecord `json:"records"`
}

// SourceAvailabilityRecord carries no private path, live diagnosis, or invented zero measurement.
type SourceAvailabilityRecord struct {
	Name       string           `json:"name"`
	Provenance SourceProvenance `json:"provenance"`
	Reason     string           `json:"reason"`
}

// sourceAvailability bounds complete labels and keeps omitted observations visible in the total.
func (e *engine) sourceAvailability(id string) (SourceAvailability, error) {
	out := SourceAvailability{Total: "0", Records: []SourceAvailabilityRecord{}}
	if err := e.topology(); err != nil {
		return out, err
	}
	all := id == "nas" || id == virtualID(e.h.SnapshotID, "nas", "")
	volume, target := "", ""
	prefix := []byte(".")
	if !all {
		for _, root := range e.h.Manifest.Provenance.Roots {
			if id == virtualID(e.h.SnapshotID, "volume", root.VolumeID) {
				volume = root.VolumeID
				break
			}
		}
		if volume == "" {
			if real, ok := e.containers[id]; ok {
				id = real
			}
			stored, err := index.ReadNode(e.ctx, e.db, id)
			if err != nil {
				return out, err
			}
			target, prefix = stored.TargetID, stored.Path
		}
	}
	total := 0
	add := func(name string, p SourceProvenance, reason string) error {
		if !ValidSourceProvenance(&p) {
			return ErrSnapshot
		}
		total++
		if len(out.Records) == 32 {
			return nil
		}
		candidate := SourceAvailabilityRecord{Name: name, Provenance: p, Reason: reason}
		trial := append(append([]SourceAvailabilityRecord{}, out.Records...), candidate)
		raw, err := json.Marshal(SourceAvailability{Total: "3072", Truncated: true, Records: trial})
		if err != nil {
			return err
		}
		if len(raw) <= 16<<10 {
			out.Records = trial
		}
		return nil
	}
	for _, root := range e.h.Manifest.Provenance.Roots {
		if !all && (volume != "" && volume != root.VolumeID || target != "" && target != root.TargetID) {
			continue
		}
		rootNode, findErr := index.FindNode(e.ctx, e.db, root.TargetID, []byte("."))
		if findErr != nil && !errors.Is(findErr, sql.ErrNoRows) {
			return out, findErr
		}
		rootID := rootNode.Node.ID
		if _, present := e.parents[rootID]; !present && bytes.Equal(prefix, []byte(".")) {
			state := "unverified"
			for _, issue := range e.h.Manifest.Issues {
				if issue.TargetID == root.TargetID {
					state = issue.State
					break
				}
			}
			qualification := "unavailable"
			if e.h.Manifest.Provenance.Authority != "native-inventory" {
				qualification = "explicit"
			}
			if err := add(root.Name, SourceProvenance{1, e.h.Manifest.Provenance.Authority, root.SourceID, qualification, state}, "content-not-recorded"); err != nil {
				return out, err
			}
		}
		for _, area := range root.Areas {
			if !bytes.Equal(prefix, []byte(".")) && !bytes.Equal(area.RelativePath, prefix) && !bytes.HasPrefix(area.RelativePath, append(append([]byte{}, prefix...), '/')) {
				continue
			}
			areaNode, findErr := index.FindNode(e.ctx, e.db, root.TargetID, area.RelativePath)
			if findErr != nil && !errors.Is(findErr, sql.ErrNoRows) {
				return out, findErr
			}
			areaID := areaNode.Node.ID
			_, present := e.parents[areaID]
			if area.Qualification == "qualified" && present {
				continue
			}
			reason := "content-not-recorded"
			if area.Qualification == "unavailable" {
				reason = "source-unavailable"
			}
			if err := add(area.Name, SourceProvenance{1, e.h.Manifest.Provenance.Authority, area.SourceID, area.Qualification, area.State}, reason); err != nil {
				return out, err
			}
		}
	}
	out.Total = contracts.Bytes(strconv.Itoa(total))
	out.Truncated = total > len(out.Records)
	raw, err := json.Marshal(out)
	if err != nil || len(raw) > 16<<10 {
		return SourceAvailability{}, ErrBudget
	}
	return out, nil
}
