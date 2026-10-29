package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/dsm/filestation"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
)

var ErrNotFound = errors.New("NOT_FOUND")
var ErrStaleSnapshot = errors.New("SNAPSHOT_MISMATCH")
var ErrRootRebind = errors.New("PROFILE_REBIND_REQUIRED")
var ErrGenerationRefresh = errors.New("INDEX_REFRESH_REQUIRED")
var ErrPathEncoding = errors.New("PATH_ENCODING_UNSUPPORTED")

// ErrFileStationUnavailable distinguishes a known node without a safely addressable target.
var ErrFileStationUnavailable = errors.New("FILE_STATION_UNAVAILABLE")

// ProfileView exposes no privileged root mappings or filesystem binding metadata.
type ProfileView = profiles.Summary

// SnapshotView is a deliberately bounded preview, not a claim of full hierarchy delivery.
type SnapshotView struct {
	State                   *string                  `json:"state"`
	StartedAt               *string                  `json:"startedAt"`
	EndedAt                 *string                  `json:"endedAt"`
	ScanBoundaries          []contracts.ScanBoundary `json:"scanBoundaries"`
	ScanBoundaryCount       contracts.Bytes          `json:"scanBoundaryCount"`
	ScanBoundariesTruncated bool                     `json:"scanBoundariesTruncated"`
	PackageUsage            contracts.PackageUsage   `json:"packageUsage"`
	ProfileRevision         *int                     `json:"profileRevision"`
	CurrentProfileRevision  int                      `json:"currentProfileRevision"`
	ProfileChanged          bool                     `json:"profileChanged"`
	CapturedProfileName     *string                  `json:"capturedProfileName"`
	SnapshotID              *string                  `json:"snapshotId"`
	Nodes                   []contracts.Node         `json:"nodes"`
	Truncated               bool                     `json:"truncated"`
}

// FileStationTarget contains a freshly checked share path, not a launcher success claim.
type FileStationTarget = filestation.Target

// Profiles reads persisted profiles and active generation pointers in a single database snapshot.
func (m *Manager) Profiles(ctx context.Context) ([]ProfileView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrUnavailable
	}
	return profiles.List(ctx, m.db)
}

// activeGeneration validates the selected profile before returning its recorded immutable generation.
func (m *Manager) activeGeneration(ctx context.Context, profileID string) (string, error) {
	if !identifier.MatchString(profileID) {
		return "", ErrInvalid
	}
	var snapshot sql.NullString
	err := m.db.QueryRowContext(ctx, `SELECT a.snapshot_id FROM profiles p LEFT JOIN active_generations a ON a.profile_id=p.id WHERE p.id=?`, profileID).Scan(&snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if !snapshot.Valid {
		return "", nil
	}
	if !identifier.MatchString(snapshot.String) {
		return "", ErrUnavailable
	}
	return snapshot.String, nil
}

// generationDB pins a regular index file under a no-symlink generation directory and opens it read-only.
func (m *Manager) generationDB(snapshot string) (*sql.DB, func(), error) {
	if !identifier.MatchString(snapshot) {
		return nil, nil, ErrInvalid
	}
	// Reserve before opening so cleanup cannot remove the generation in the gap.
	// Opening a held descriptor and SQLite must not serialize unrelated readers.
	m.readerMu.Lock()
	if m.generationReaders == nil {
		m.generationReaders = map[string]int{}
	}
	m.generationReaders[snapshot]++
	m.readerMu.Unlock()
	db, close, err := m.openGenerationDB(snapshot)
	if err != nil {
		m.readerMu.Lock()
		m.generationReaders[snapshot]--
		if m.generationReaders[snapshot] == 0 {
			delete(m.generationReaders, snapshot)
		}
		m.readerMu.Unlock()
		return nil, nil, err
	}
	var once sync.Once
	return db, func() {
		once.Do(func() {
			close()
			m.readerMu.Lock()
			defer m.readerMu.Unlock()
			m.generationReaders[snapshot]--
			if m.generationReaders[snapshot] == 0 {
				delete(m.generationReaders, snapshot)
			}
		})
	}, nil
}

// openGenerationDB pins the descriptor; callers own reader/cleanup exclusion.
func (m *Manager) openGenerationDB(snapshot string) (*sql.DB, func(), error) {
	if !identifier.MatchString(snapshot) {
		return nil, nil, ErrInvalid
	}
	// A known unusable generation cannot remain readable through an earlier lease.
	if err := m.generationIntegrityAllowed(snapshot); err != nil {
		return nil, nil, err
	}
	root, _, err := scanner.OpenBoundRoot(filepath.Join(m.state, "generations", snapshot))
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	info, err := root.Lstat("index.db")
	if err != nil || !info.Mode().IsRegular() {
		root.Close()
		return nil, nil, ErrUnavailable
	}
	file, err := root.Open("index.db")
	root.Close()
	if err != nil {
		return nil, nil, ErrUnavailable
	}
	current, err := file.Stat()
	if err != nil || !os.SameFile(info, current) {
		file.Close()
		return nil, nil, ErrUnavailable
	}
	// A prior healthy verdict is valid only for the exact currently held file identity.
	if err = m.generationIntegrityFileAllowed(snapshot, file); err != nil {
		file.Close()
		return nil, nil, err
	}
	prefix := "/proc/self/fd/"
	if runtime.GOOS == "darwin" {
		prefix = "/dev/fd/"
	}
	uri := url.URL{Scheme: "file", Path: prefix + strconv.FormatUint(uint64(file.Fd()), 10)}
	// Every immutable generation reader uses the measured bounded SQLite cache and disk-backed temporary state.
	db, err := sql.Open("sqlite", uri.String()+"?mode=ro&immutable=1&_pragma=query_only(1)&_pragma=cache_size(-8192)&_pragma=temp_store(1)&_pragma=temp.cache_size(-2048)&_pragma=mmap_size(0)")
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	// Keep the actual source handle available independently of SQLite pathname normalization.
	m.generationFiles.Store(db, file)
	cleanup := func() { m.generationFiles.Delete(db); db.Close(); file.Close() }
	if err = db.Ping(); err != nil {
		cleanup()
		return nil, nil, ErrUnavailable
	}
	return db, cleanup, nil
}

// Snapshot previews at most 200 actual records, bounded below the common 64 KiB response cap.
func (m *Manager) Snapshot(ctx context.Context, profileID string) (SnapshotView, error) {
	return m.SnapshotSelection(ctx, profileID, "")
}

// SnapshotSelection opens a partial only through an explicit, profile-bound terminal job.
func (m *Manager) SnapshotSelection(ctx context.Context, profileID, jobID string) (SnapshotView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked(ctx, profileID, jobID, "")
}

// snapshotLocked reads one explicitly chosen generation while its caller owns lifecycle coordination.
func (m *Manager) snapshotLocked(ctx context.Context, profileID, jobID, fixedSnapshot string) (SnapshotView, error) {
	out := SnapshotView{Nodes: []contracts.Node{}, ScanBoundaries: []contracts.ScanBoundary{}, ScanBoundaryCount: "0"}
	if m.closed {
		return out, ErrUnavailable
	}
	snapshot, err := m.activeGeneration(ctx, profileID)
	if err != nil {
		return out, err
	}
	if fixedSnapshot != "" {
		snapshot = fixedSnapshot
	}
	var selected *Outcome
	if jobID != "" {
		if !identifier.MatchString(jobID) {
			return out, ErrInvalid
		}
		var raw string
		err = m.db.QueryRowContext(ctx, `SELECT outcome_json FROM jobs WHERE id=? AND json_extract(outcome_json,'$.job.profileId')=?`, jobID, profileID).Scan(&raw)
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrNotFound
		}
		if err != nil {
			return out, err
		}
		var candidate Outcome
		if json.Unmarshal([]byte(raw), &candidate) != nil || candidate.Job.ID != jobID || candidate.Job.ProfileID != profileID {
			return out, ErrUnavailable
		}
		if candidate.Job.State != "completed" || candidate.SnapshotState != "partial" || candidate.Job.ResultSnapshotID == nil || candidate.SnapshotEndedAt == nil {
			return out, ErrScanResultUnavailable
		}
		selected = &candidate
		snapshot = *candidate.Job.ResultSnapshotID
	}
	if err = m.db.QueryRowContext(ctx, `SELECT revision FROM profiles WHERE id=?`, profileID).Scan(&out.CurrentProfileRevision); err != nil {
		return out, err
	}
	out.PackageUsage = packageUsage(ctx, m.state)
	if snapshot == "" {
		return out, nil
	}
	out.SnapshotID = &snapshot
	db, close, err := m.generationDB(snapshot)
	if err != nil {
		return out, err
	}
	defer close()
	header, err := index.ReadGenerationHeader(ctx, db, snapshot)
	if err != nil || header.Profile.ID != profileID {
		return out, ErrGenerationRefresh
	}
	if selected != nil {
		if header.State != "partial" || header.JobID != selected.Job.ID || header.Profile.Revision != selected.Job.ProfileRevision || header.EndedAt != *selected.SnapshotEndedAt {
			return out, ErrScanResultUnavailable
		}
	} else if fixedSnapshot == "" && header.State != "complete" {
		return out, ErrUnavailable
	}
	out.State = &header.State
	out.StartedAt = &header.StartedAt
	out.EndedAt = &header.EndedAt
	out.ScanBoundaryCount = header.ScanBoundaryCount
	out.ScanBoundaries, err = boundaryExamples(ctx, db)
	if err != nil {
		return out, err
	}
	out.ScanBoundariesTruncated = string(out.ScanBoundaryCount) != strconv.Itoa(len(out.ScanBoundaries))
	out.ProfileRevision = &header.Profile.Revision
	out.CapturedProfileName = &header.Profile.Name
	out.ProfileChanged = header.Profile.Revision != out.CurrentProfileRevision
	// A bounded native reader reconstructs only the visible preview paths.
	reader, err := index.NewNodeReader(ctx, db)
	if err != nil {
		return out, err
	}
	records, err := reader.Page(0, 201)
	if err != nil {
		return out, err
	}
	size := 0
	for _, record := range records {
		n := record.Node
		raw, marshalErr := json.Marshal(n)
		if marshalErr != nil || n.SnapshotID != snapshot {
			return out, ErrUnavailable
		}
		if len(out.Nodes) >= 200 || size+len(raw) > 40*1024 {
			out.Truncated = true
			break
		}
		out.Nodes = append(out.Nodes, n)
		size += len(raw)
	}
	return out, nil
}

// ParseShares uses the shared authoritative registry parser without a separate interpretation.
func ParseShares(reader io.Reader) (map[string]string, error) {
	return inventory.ParseSharePaths(reader)
}

// FileStation revalidates both the selected generation and bound source before exposing a target.
func (m *Manager) FileStation(ctx context.Context, profileID, snapshotID, nodeID string, shares map[string]string) (FileStationTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out FileStationTarget
	if m.closed {
		return out, ErrUnavailable
	}
	if !identifier.MatchString(nodeID) {
		return out, ErrNotFound
	}
	if !identifier.MatchString(snapshotID) {
		return out, ErrInvalid
	}
	active, err := m.activeGeneration(ctx, profileID)
	if err != nil {
		return out, err
	}
	if active == "" || active != snapshotID {
		return out, ErrStaleSnapshot
	}
	db, close, err := m.generationDB(active)
	if err != nil {
		return out, err
	}
	defer close()
	var version int
	if err = db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return out, ErrUnavailable
	}
	if version != index.GenerationSchema {
		return out, ErrGenerationRefresh
	}
	if _, err = index.ReadGenerationHeader(ctx, db, active); err != nil {
		return out, ErrGenerationRefresh
	}
	// Resolve the native numeric locator without storing duplicate full paths.
	record, err := index.ReadNode(ctx, db, nodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, err
	}
	node, target, originalPath, objectIdentity := record.Node, record.TargetID, record.Path, record.ObjectIdentity
	if node.SnapshotID != active || node.ID != nodeID {
		return out, ErrUnavailable
	}
	if node.Kind != "file" && node.Kind != "directory" {
		return out, ErrNotFound
	}
	var rootsRaw, volumesRaw string
	var revision int
	if err = m.db.QueryRowContext(ctx, `SELECT roots_json,volumes_json,revision FROM profiles WHERE id=?`, profileID).Scan(&rootsRaw, &volumesRaw, &revision); err != nil {
		return out, err
	}
	// A retained old generation must never be mapped through a newer profile scope.
	var generationRevision int
	if err = m.db.QueryRowContext(ctx, `SELECT json_extract(outcome_json,'$.job.profileRevision') FROM jobs WHERE json_extract(outcome_json,'$.job.resultSnapshotId')=? AND json_extract(outcome_json,'$.job.profileId')=?`, snapshotID, profileID).Scan(&generationRevision); err != nil {
		return out, ErrUnavailable
	}
	if generationRevision != revision {
		return out, ErrStaleSnapshot
	}
	var roots map[string]scanner.RootBinding
	var volumes map[string]string
	if json.Unmarshal([]byte(rootsRaw), &roots) != nil || json.Unmarshal([]byte(volumesRaw), &volumes) != nil {
		return out, ErrUnavailable
	}
	volume, ok := volumes[target]
	if !ok || security.ValidateNode(node, snapshotID, nodeID, target, volume, string(originalPath)) != nil {
		return out, ErrNotFound
	}
	binding, ok := roots[target]
	if !ok {
		return out, ErrNotFound
	}
	if binding.MountID == "" {
		return out, ErrRootRebind
	}
	if !utf8.Valid(originalPath) {
		return out, ErrPathEncoding
	}
	// Ancestor lookup is index-only, parameter-bound and restricted to the selected target.
	lookup := func(relative string) (string, error) {
		record, err := index.FindNode(ctx, db, target, []byte(relative))
		return record.ObjectIdentity, err
	}
	out, err = filestation.Resolve(ctx, binding, string(originalPath), node.Kind, objectIdentity, shares, lookup)
	if err != nil {
		return FileStationTarget{}, ErrFileStationUnavailable
	}
	return out, nil
}
