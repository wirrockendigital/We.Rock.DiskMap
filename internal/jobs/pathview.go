package jobs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

// PathText explicitly distinguishes original Unicode from a printable original-byte representation.
type PathText struct {
	Text     string `json:"text"`
	Encoding string `json:"encoding"`
}

// NodePaths separates immutable scan provenance from a currently verified File Station address.
type NodePaths struct {
	SnapshotID      string   `json:"snapshotId"`
	NodeID          string   `json:"nodeId"`
	Name            PathText `json:"name"`
	NASPath         PathText `json:"nasPath"`
	FileStationPath *string  `json:"fileStationPath"`
}

// pathText never replaces invalid bytes with a different apparent Unicode filename.
func pathText(value string) PathText {
	if utf8.ValidString(value) {
		return PathText{value, "utf8"}
	}
	return PathText{strconv.QuoteToASCII(value), "escaped-bytes"}
}

// Paths keeps historical metadata and current existence claims separate, using identifiers only.
func (m *Manager) Paths(ctx context.Context, profileID, snapshotID, nodeID string, shares map[string]string) (NodePaths, error) {
	out, err := m.snapshotPaths(ctx, profileID, snapshotID, nodeID)
	if err != nil {
		return out, err
	}
	target, err := m.FileStation(ctx, profileID, snapshotID, nodeID, shares)
	if err == nil && target.NoticeCode == "" {
		out.FileStationPath = &target.FileStationPath
	}
	if err != nil && !errors.Is(err, ErrFileStationUnavailable) && !errors.Is(err, ErrPathEncoding) && !errors.Is(err, ErrNotFound) {
		return NodePaths{}, err
	}
	return out, nil
}

// snapshotPaths requires the sealed start manifest; mutable current root paths are never substituted.
func (m *Manager) snapshotPaths(ctx context.Context, profileID, snapshotID, nodeID string) (NodePaths, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out NodePaths
	if m.closed {
		return out, ErrUnavailable
	}
	if !identifier.MatchString(nodeID) {
		return out, ErrNotFound
	}
	active, err := m.activeGeneration(ctx, profileID)
	if err != nil {
		return out, err
	}
	if active == "" || active != snapshotID {
		return out, ErrStaleSnapshot
	}
	db, close, err := m.generationDB(snapshotID)
	if err != nil {
		return out, err
	}
	defer close()
	header, err := index.ReadGenerationHeader(ctx, db, snapshotID)
	if err != nil {
		return out, ErrGenerationRefresh
	}
	var revision int
	if err = m.db.QueryRowContext(ctx, `SELECT revision FROM profiles WHERE id=?`, profileID).Scan(&revision); err != nil {
		return out, ErrUnavailable
	}
	if header.Profile.ID != profileID || header.Profile.Revision != revision {
		return out, ErrStaleSnapshot
	}
	// Resolve original bytes from the sealed numeric tree before exposing a path.
	record, err := index.ReadNode(ctx, db, nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, ErrUnavailable
	}
	node, target, name, path := record.Node, record.TargetID, record.Name, record.Path
	matched := 0
	for _, root := range header.Manifest.Roots {
		if root.TargetID != target {
			continue
		}
		matched++
		if root.Binding == nil || root.PhysicalPath != root.Binding.Path || !filepath.IsAbs(root.Binding.Path) || filepath.Clean(root.Binding.Path) != root.Binding.Path || security.ValidateNode(node, snapshotID, nodeID, target, root.VolumeID, string(path)) != nil {
			return out, ErrNotFound
		}
		out = NodePaths{SnapshotID: snapshotID, NodeID: nodeID, Name: pathText(string(name)), NASPath: pathText(filepath.Join(root.Binding.Path, string(path)))}
	}
	if matched != 1 {
		return NodePaths{}, ErrNotFound
	}
	return out, nil
}
