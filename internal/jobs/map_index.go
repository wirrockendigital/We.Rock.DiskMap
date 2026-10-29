package jobs

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/mapindex"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
)

var ErrMapPreparing = errors.New("MAP_PREPARING")
var ErrMapUnavailable = errors.New("MAP_INDEX_UNAVAILABLE")

// MapRequest binds immutable block addresses to an existing authorized snapshot lease.
type MapRequest struct {
	ContractVersion int           `json:"contractVersion"`
	ProfileID       string        `json:"profileId"`
	ViewID          string        `json:"viewId"`
	Query           query.Request `json:"query"`
	Cursor          string        `json:"cursor,omitempty"`
	IndexID         string        `json:"indexId,omitempty"`
	Blocks          []uint64      `json:"blocks,omitempty"`
}

// hierarchy shares admission without anonymous JSON fields at the strict public boundary.
func (r MapRequest) hierarchy() HierarchyRequest {
	return HierarchyRequest{ContractVersion: r.ContractVersion, ProfileID: r.ProfileID, ViewID: r.ViewID, Query: r.Query, Cursor: r.Cursor}
}

// Validate bounds every request before view admission or disk access.
func (r MapRequest) Validate() error {
	if r.hierarchy().Validate() != nil || r.Cursor != "" || r.Query.Metric != "logical" || r.Query.Filter.HasPredicates() || len(r.Blocks) > 128 {
		return ErrInvalid
	}
	if r.IndexID != "" && !validQueryCacheKey(r.IndexID) {
		return ErrInvalid
	}
	seen := map[uint64]bool{}
	for _, n := range r.Blocks {
		if seen[n] {
			return ErrInvalid
		}
		seen[n] = true
	}
	return nil
}

// MapManifest distinguishes preparation from scan coverage and advertises immutable dimensions.
type MapManifest struct {
	Version    int                 `json:"version"`
	Status     string              `json:"status"`
	SnapshotID string              `json:"snapshotId"`
	IndexID    string              `json:"indexId"`
	RootID     string              `json:"rootId"`
	Root       uint64              `json:"root"`
	Count      uint64              `json:"count"`
	BlockBytes int                 `json:"blockBytes"`
	Record     []byte              `json:"record"`
	Identities []query.MapIdentity `json:"identities"`
}

// mapIndexDirectory refuses links and foreign permissions before creating private staging data.
func (m *Manager) mapIndexDirectory() (string, error) {
	p := filepath.Join(m.state, "map-index")
	if err := os.Mkdir(p, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	s, err := os.Lstat(p)
	if err != nil || !s.IsDir() || s.Mode().Perm() != 0700 {
		return "", ErrUnavailable
	}
	// Bind the private directory to its held descriptor before accepting its path.
	root, _, err := scanner.OpenBoundRoot(p)
	if err != nil {
		return "", err
	}
	defer root.Close()
	held, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer held.Close()
	after, err := held.Stat()
	if err != nil || !os.SameFile(s, after) || !privateFilterFile(held, true) {
		return "", ErrUnavailable
	}
	return p, nil
}

// prepareMapIndex publishes a fresh directory only after all immutable files have been synced.
func (m *Manager) prepareMapIndex(ctx context.Context, db *sql.DB, h index.GenerationHeader, key string) error {
	root, err := m.mapIndexDirectory()
	if err != nil {
		return err
	}
	if !validQueryCacheKey(key) {
		return ErrInvalid
	}
	target := filepath.Join(root, key)
	if r, e := mapindex.Open(target); e == nil {
		r.Close()
		return nil
	}
	stage, err := os.MkdirTemp(root, ".prepare-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage) // Only this newly created private directory is owned by this build.
	builder, e := mapindex.NewBuilder(ctx, stage)
	if e != nil {
		return e
	}
	err = query.FeedMapIndex(ctx, db, h, builder)
	if err == nil {
		err = builder.Finish()
	}
	builder.Close()
	if err == nil {
		err = mapindex.SealBlocks(ctx, stage)
	}
	if err != nil {
		return err
	}
	binding, _ := json.Marshal(mapIndexBinding{Snapshot: h.SnapshotID, Key: key})
	if err = os.WriteFile(filepath.Join(stage, "binding.json"), binding, 0600); err != nil {
		return err
	}
	bindingFile, e := os.Open(filepath.Join(stage, "binding.json"))
	if e != nil {
		return e
	}
	e = bindingFile.Sync()
	bindingFile.Close()
	if e != nil {
		return e
	}
	stageDir, e := os.Open(stage)
	if e != nil {
		return e
	}
	e = stageDir.Sync()
	stageDir.Close()
	if e != nil {
		return e
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	// An existing invalid artifact is quarantined, never traversed or recursively removed here.
	if _, e := os.Lstat(target); e == nil {
		if err = os.Rename(target, stage+".invalid"); err != nil {
			return err
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if err = os.Rename(stage, target); err != nil {
		return err
	}
	f, err := os.Open(root)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// requestMapIndex starts one bounded background producer while keeping CGI requests short.
func (m *Manager) requestMapIndex(snapshot, key string, foreground bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrUnavailable
	}
	m.mapIndexMu.Lock()
	defer m.mapIndexMu.Unlock()
	if !foreground && time.Since(m.mapIndexLastRead) < 30*time.Second {
		return ErrMapPreparing
	}
	if until := m.mapIndexFailure[key]; time.Now().Before(until) {
		return ErrMapUnavailable
	}
	if m.mapIndexBusy {
		if foreground && m.mapIndexKey == key {
			m.mapIndexForeground = true
		}
		if foreground && !m.mapIndexForeground && m.mapIndexKey != key && m.mapIndexCancel != nil {
			m.mapIndexCancel()
		}
		return ErrMapPreparing
	}
	if m.active != nil {
		return ErrMapPreparing
	}
	m.mapIndexBusy = true
	m.mapIndexKey = key
	m.mapIndexForeground = foreground
	buildctx, cancel := context.WithCancel(m.ctx)
	m.mapIndexCancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		db, release, err := m.generationDB(snapshot)
		if err == nil {
			defer release()
			var h index.GenerationHeader
			h, err = index.ReadGenerationHeader(buildctx, db, snapshot)
			if err == nil && m.querySourceKey(buildctx, db, snapshot, "visual-v4") != key {
				err = ErrStaleSnapshot
			}
			if err == nil {
				err = m.prepareMapIndex(buildctx, db, h, key)
			}
		}
		m.mapIndexMu.Lock()
		defer m.mapIndexMu.Unlock()
		m.mapIndexBusy = false
		m.mapIndexCancel = nil
		m.mapIndexKey = ""
		m.mapIndexForeground = false
		if err != nil && !errors.Is(err, context.Canceled) {
			if len(m.mapIndexFailure) >= 64 {
				m.mapIndexFailure = nil
			}
			if m.mapIndexFailure == nil {
				m.mapIndexFailure = map[string]time.Time{}
			}
			m.mapIndexFailure[key] = time.Now().Add(10 * time.Second)
		}
	}()
	return ErrMapPreparing
}

// mapReader revalidates the current source descriptor even on warm derived-index reads.
func (m *Manager) mapReader(ctx context.Context, owner string, in MapRequest) (*mapindex.Reader, *sql.DB, index.GenerationHeader, string, func(), error) {
	var h index.GenerationHeader
	noop := func() {}
	if in.Validate() != nil {
		return nil, nil, h, "", noop, ErrInvalid
	}
	release, err := m.reserveHierarchy(ctx, owner, in.hierarchy())
	if err != nil {
		return nil, nil, h, "", noop, err
	}
	db, closeDB, err := m.generationDB(in.Query.SnapshotID)
	if err != nil {
		release()
		return nil, nil, h, "", noop, err
	}
	closeAll := func() { closeDB(); release() }
	h, err = index.ReadGenerationHeader(ctx, db, in.Query.SnapshotID)
	if err != nil || h.Profile.ID != in.ProfileID {
		closeAll()
		return nil, nil, h, "", noop, ErrStaleSnapshot
	}
	key := m.querySourceKey(ctx, db, in.Query.SnapshotID, "visual-v4")
	if key == "" || in.IndexID != "" && in.IndexID != key {
		closeAll()
		return nil, nil, h, "", noop, ErrStaleSnapshot
	}
	// Browsing a prepared map takes disk priority over speculative preparation of other scans.
	m.mapIndexMu.Lock()
	m.mapIndexLastRead = time.Now()
	if m.mapIndexBusy && !m.mapIndexForeground && m.mapIndexKey != key && m.mapIndexCancel != nil {
		m.mapIndexCancel()
	}
	m.mapIndexMu.Unlock()
	root, err := m.mapIndexDirectory()
	if err != nil {
		closeAll()
		return nil, nil, h, key, noop, ErrMapUnavailable
	}
	r, err := mapindex.Open(filepath.Join(root, key))
	if err != nil {
		closeAll()
		err = m.requestMapIndex(in.Query.SnapshotID, key, true)
		return nil, nil, h, key, noop, err
	}
	return r, db, h, key, func() { r.Close(); closeAll() }, nil
}

// ReadMapManifest loads only topology metadata and the selected root record.
func (m *Manager) ReadMapManifest(ctx context.Context, owner string, in MapRequest) (MapManifest, error) {
	ctx, cancel := context.WithTimeout(ctx, query.QueryTimeout+100*time.Millisecond)
	defer cancel()
	out := MapManifest{Version: mapindex.Version, SnapshotID: in.Query.SnapshotID, RootID: in.Query.RootID, Status: "preparing", BlockBytes: mapindex.BlockBytes}
	if len(in.Blocks) != 0 {
		return out, ErrInvalid
	}
	r, db, h, key, release, err := m.mapReader(ctx, owner, in)
	defer release()
	out.IndexID = key
	if errors.Is(err, ErrMapPreparing) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.RootID, out.Identities, err = query.MapIndexIdentities(ctx, db, h, in.Query.RootID, r)
	if err != nil {
		return out, err
	}
	out.Root, err = r.Slot(ctx, out.RootID)
	if err != nil {
		return out, query.ErrRoot
	}
	n, err := r.Record(out.Root)
	if err != nil {
		m.invalidateMapIndex(key, r)
		_ = m.requestMapIndex(in.Query.SnapshotID, key, true)
		return out, ErrMapUnavailable
	}
	out.Record = make([]byte, mapindex.RecordBytes)
	n.Encode(out.Record)
	out.Count = r.Count
	out.Status = "ready"
	return out, nil
}

// ReadMapChunks returns a bounded binary frame with an index binding and per-block checksums.
func (m *Manager) ReadMapChunks(ctx context.Context, owner string, in MapRequest) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, query.QueryTimeout+100*time.Millisecond)
	defer cancel()
	if len(in.Blocks) == 0 || in.IndexID == "" {
		return nil, ErrInvalid
	}
	r, db, h, key, release, err := m.mapReader(ctx, owner, in)
	defer release()
	if err != nil {
		return nil, err
	}
	if err = query.ValidateMapIndexRoot(ctx, db, h, in.Query.RootID); err != nil {
		return nil, err
	}
	// Bound encoded bytes independently of requested record count; return a complete prefix.
	const responseLimit = 4 * 1024 * 1024
	out := make([]byte, 44, responseLimit)
	copy(out, "WRDCH003")
	binding, _ := hex.DecodeString(key)
	copy(out[8:40], binding)
	var returned uint32
	for _, block := range in.Blocks {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		raw, digest, e := r.CompressedBlock(block)
		if e != nil {
			if block < r.Blocks {
				m.invalidateMapIndex(key, r)
				_ = m.requestMapIndex(in.Query.SnapshotID, key, true)
			}
			return nil, ErrMapUnavailable
		}
		if len(out)+44+len(raw) > responseLimit {
			break
		}
		at := len(out)
		out = append(out, make([]byte, 44)...)
		binary.LittleEndian.PutUint64(out[at:], block)
		binary.LittleEndian.PutUint32(out[at+8:], uint32(len(raw)))
		sum, _ := hex.DecodeString(digest)
		copy(out[at+12:], sum)
		out = append(out, raw...)
		returned++
	}
	binary.LittleEndian.PutUint32(out[40:], returned)
	return out, nil
}

// invalidateMapIndex detaches a corrupt derived artifact without touching the scan generation.
func (m *Manager) invalidateMapIndex(key string, reader *mapindex.Reader) {
	if !validQueryCacheKey(key) {
		return
	}
	m.mapIndexMu.Lock()
	defer m.mapIndexMu.Unlock()
	root, err := m.mapIndexDirectory()
	if err != nil || !reader.MatchesDirectory(filepath.Join(root, key)) {
		return
	}
	_ = os.Rename(filepath.Join(root, key), filepath.Join(root, ".prepare-corrupt-"+key+"-"+strconv.FormatInt(time.Now().UnixNano(), 10)))
}
