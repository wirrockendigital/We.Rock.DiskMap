// Package jobs serializes scans and persists terminal outcomes independently of clients.
package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/migration"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/resources"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/uninstall"
)

// Stable errors deliberately omit source paths and database diagnostics.
var (
	ErrInvalid     = errors.New("INVALID_REQUEST")
	ErrConflict    = errors.New("JOB_CONFLICT")
	ErrUnavailable = errors.New("SERVICE_UNAVAILABLE")
)
var ErrIdempotencyConflict = errors.New("IDEMPOTENCY_CONFLICT")

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

// Import defines the privileged local bootstrap, not a public scanning-path API.
type Import struct {
	SourceMode string            `json:"sourceMode"`
	RootKinds  map[string]string `json:"rootKinds"`
	Profile    contracts.Profile `json:"profile"`
	Roots      map[string]string `json:"roots"`
	VolumeIDs  map[string]string `json:"volumeIds"`
}

// Outcome adds execution identity to the canonical persisted job.
type Outcome struct {
	// Scanner diagnostics count repeated metadata records without storing private pathnames.
	DuplicateEntries uint64 `json:"duplicateEntries,omitempty"`
	// PreviewPrincipal is RAM-only browser ownership, never scanner filesystem identity.
	PreviewPrincipal string                    `json:"-"`
	Control          *JobControlStatus         `json:"control"`
	SnapshotEndedAt  *string                   `json:"snapshotEndedAt"`
	PhaseHistory     []JobPhase                `json:"phaseHistory"`
	StoredData       *StoredDataStatus         `json:"storedData"`
	FileTypeMapping  contracts.FileTypeMapping `json:"fileTypeMapping"`
	ScopeManifest    *inventory.ScopeManifest  `json:"scopeManifest"`
	Job              contracts.Job             `json:"job"`
	ServiceUID       int                       `json:"serviceUid"`
	SnapshotState    string                    `json:"snapshotState"`
	CoverageErrors   uint64                    `json:"coverageErrors"`
}

// Submission records whether the caller joined the same profile revision and scan mode.
type Submission struct {
	Outcome
	Deduplicated bool `json:"deduplicated"`
}

// Manager owns one package-wide scanner and the private SQLite control store.
type Manager struct {
	// Reset work outlives a CGI request; polling never waits on the mutation lock.
	resetMu         sync.Mutex
	resetOperations map[string]*resetOperation
	// Bounded, immutable map catalogs are independent of active view navigation.
	queryCacheMu sync.Mutex // Serialize bounded reconstructible cache publication.
	mapCatalogMu sync.Mutex
	mapCatalogs  map[string]*mapCatalogSlot
	// Visualization preparation is single-flight and independent of short HTTP requests.
	mapIndexMu         sync.Mutex
	mapIndexBusy       bool
	mapIndexCancel     context.CancelFunc
	mapIndexKey        string
	mapIndexForeground bool
	mapIndexLastRead   time.Time // Foreground map traffic postpones speculative disk work.
	mapIndexFailure    map[string]time.Time
	mapIndexCleanAfter string // Last examined artifact name bounds each cleanup pass without starvation.
	// Immutable generation category metadata is shared across bounded observation batches.
	recycleMu    sync.Mutex
	recycleCache map[string]*recycleObservationSlot
	// Only the current job holds preview data; queued identities are bounded by queue capacity.
	preview       *scanPreviewSlot
	previewOwners map[string]string

	// Retention rounds advance fairly; the optional probe is test-only and never configured at runtime.
	retentionProfileCursor string
	// Retention discovery state is bounded and worker-local, separate from lifecycle ownership.
	retentionWorkMu          sync.Mutex
	retentionDiscoveryReady  bool
	retentionDiscoveryCursor int64
	retentionRetry           map[string]time.Time
	retentionProbe           func()

	// Disposable filter projections share the manager lifetime, never source storage.
	filterCache *filterCacheManager
	// Committed integrity verdicts are readable without nesting control-store transactions.
	integrityStates   sync.Map
	hierarchySlots    chan struct{}
	hierarchyWaiters  chan struct{} // Bounded admission backlog, separate from active query slots.
	viewLeases        map[string]*viewLease
	viewClock         func() time.Time
	runner            *jobRunner
	workerRunning     bool
	publishHook       func(string) error
	phaseHook         func(string) error
	spaceProbe        func() (uint64, error)
	readerMu          sync.Mutex
	generationFiles   sync.Map // Bind each SQLite reader to its actually held source inode.
	generationReaders map[string]int
	catalog           catalogReader
	lockFile          *os.File
	mu                sync.Mutex
	readDB            *sql.DB // Bounded read-only observation pool; writer transactions stay on db.
	db                *sql.DB
	state             string
	ctx               context.Context
	cancel            context.CancelFunc
	wg                sync.WaitGroup
	active            *Outcome
	closed            bool
}

// Open repairs interrupted jobs before accepting new work.
func Open(ctx context.Context, state string) (*Manager, error) {
	if err := security.PrepareRuntime(state); err != nil {
		return nil, err
	}
	// Resolve the DSM package VAR symlink before comparing source and state roots.
	resolved, err := filepath.EvalSymlinks(state)
	if err != nil {
		return nil, err
	}
	state = resolved
	if err := os.Chmod(state, 0700); err != nil {
		return nil, err
	}
	lock, err := security.OpenPrivateFile(filepath.Join(state, "daemon.lock"), os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, ErrConflict
	}
	successful := false
	defer func() {
		if !successful {
			lock.Close()
		}
	}()
	// An interrupted uninstall must be resumed before opening or recreating control state.
	if err = uninstall.CheckReady(state); err != nil {
		return nil, err
	}
	if err = migration.CheckReady(state); err != nil {
		return nil, err
	}
	// Create or verify the private database inode before SQLite can open it.
	databaseFile, err := security.OpenPrivateFile(filepath.Join(state, "control.db"), os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, err
	}
	databaseFile.Close()
	if err = verifyControlSidecars(state); err != nil {
		return nil, err
	}
	db, err := openControlWriter(ctx, state)
	if err != nil {
		return nil, err
	}
	if err = os.Chmod(filepath.Join(state, "control.db"), 0600); err != nil {
		db.Close()
		return nil, err
	}
	// Existing databases may only change schema through the explicit offline upgrade path.
	if err = validateOrInitializeControl(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	if err = enableControlWAL(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	child, cancel := context.WithCancel(ctx)
	m := &Manager{generationReaders: map[string]int{}, catalog: nativeCatalog, lockFile: lock, db: db, state: state, ctx: child, cancel: cancel}
	if err = m.loadIntegrityState(ctx); err != nil {
		m.Close()
		return nil, err
	}
	// Add a bounded live-status index before dispatch without changing Control version.
	if err = initializeStatusSnapshot(ctx, m.db); err != nil {
		m.Close()
		return nil, err
	}
	if err = m.recoverInterruptedJobs(ctx); err != nil {
		m.Close()
		return nil, err
	}
	if err = m.validateQueue(); err != nil {
		m.Close()
		return nil, err
	}
	m.cleanupProfiles(ctx)
	// Cache failure disables filtering without invalidating an immutable snapshot.
	if err = m.initializeFilterCache(); err != nil {
		m.Close()
		return nil, err
	}
	if m.readDB, err = openControlReadPool(ctx, state); err != nil {
		m.Close()
		return nil, err
	}
	m.startFilterCacheWorker()
	m.startRetentionWorker()
	m.startMapIndexWorker()
	// Serialized integrity work shares the manager shutdown lifecycle.
	m.startIntegrityWorker()
	m.mu.Lock()
	m.ensureWorker()
	m.mu.Unlock()
	successful = true
	return m, nil
}

// Close cancels actual traversal and waits for its final persistent outcome.
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	m.preview = nil
	m.previewOwners = nil
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
	var readErr error
	if m.readDB != nil {
		readErr = m.readDB.Close()
	}
	checkpointErr := checkpointControlForOffline(m.db)
	err := m.db.Close()
	return errors.Join(readErr, checkpointErr, err, m.lockFile.Close())
}

// ImportProfile accepts only a validated, deliberately narrow bootstrap subset.
func (m *Manager) ImportProfile(in Import) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return ErrUnavailable
	}
	if n, e := m.queueCount(); e != nil {
		return e
	} else if m.active != nil || n > 0 {
		return ErrConflict
	}
	p := in.Profile
	// Unsupported policy settings fail explicitly instead of being silently ignored.
	if !identifier.MatchString(p.ID) || p.Revision < 1 || p.Name == "" || len(p.Scope.TargetIDs) > 16 || !scanProfileSupported(p) || p.FreshnessSeconds < 0 {
		return ErrInvalid
	}
	sources, normalized, err := prepareImport(m.ctx, in, m.state, m.catalog)
	if err != nil {
		return ErrInvalid
	}
	bindings := map[string]scanner.RootBinding{}
	volumesByTarget := map[string]string{}
	for _, r := range normalized.Roots {
		volumesByTarget[r.TargetID] = r.VolumeID
		if r.State != "reachable" {
			continue
		}
		descriptor, binding, e := inventory.OpenScopeRoot(r)
		if e != nil {
			return ErrInvalid
		}
		descriptor.Close()
		bindings[r.TargetID] = binding
	}
	if err = confirmNativeBindings(m.ctx, p, sources, bindings, m.catalog); err != nil {
		return err
	}
	stampNativeBindings(&sources, p, bindings)
	var previous int
	err = m.db.QueryRow(`SELECT revision FROM profiles WHERE id=?`, p.ID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && p.Revision <= previous {
		return ErrConflict
	}
	roots, _ := json.Marshal(bindings)
	volumes, _ := json.Marshal(volumesByTarget)
	sourceJSON, _ := json.Marshal(sources)
	tx, err := m.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	known := map[string]bool{}
	for _, id := range normalized.CoveredTargetIDs {
		known[id] = true
	}
	for _, r := range normalized.Roots {
		known[r.TargetID] = true
	}
	err = profileError(profiles.Import(m.ctx, tx, profiles.Record{Profile: p, RootsJSON: string(roots), VolumesJSON: string(volumes)}, known))
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO profile_sources VALUES(?,?) ON CONFLICT(profile_id) DO UPDATE SET sources_json=excluded.sources_json`, p.ID, string(sourceJSON)); err != nil {
		return err
	}
	return tx.Commit()
}

// Start preserves local CLI profile-revision deduplication without requiring browser headers.
func (m *Manager) Start(id, mode string) (Submission, error) { return m.start(id, mode, nil) }

// BrowserStartIdentity binds a key to a server-authenticated principal and fixed operation.
type BrowserStartIdentity struct {
	Principal string
	Key       string
}

// StartBrowser persists the key atomically with the job before launching any scanner.
func (m *Manager) StartBrowser(id, mode string, identity BrowserStartIdentity) (Submission, error) {
	if !validPrincipal(identity.Principal) || !identifier.MatchString(identity.Key) {
		return Submission{}, ErrInvalid
	}
	return m.start(id, mode, &identity)
}

// validPrincipal accepts exact DSM Unicode usernames but rejects invalid HTTP identity values.
func validPrincipal(value string) bool {
	if len(value) == 0 || len(value) > 256 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, c := range value {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// start serializes job and replay decisions under the package-wide manager lock.
func (m *Manager) start(id, mode string, identity *BrowserStartIdentity) (Submission, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx.Err() != nil {
		return Submission{}, ErrUnavailable
	}
	payloadBytes, _ := json.Marshal(struct {
		ContractVersion int    `json:"contractVersion"`
		ProfileID       string `json:"profileId"`
		Mode            string `json:"mode"`
	}{1, id, mode})
	payload := string(payloadBytes)
	tx, err := m.db.Begin()
	if err != nil {
		return Submission{}, err
	}
	defer tx.Rollback()
	var replayRequest ReplayRequest
	if identity != nil {
		replayRequest = ReplayRequest{Principal: identity.Principal, Operation: "scan.start", Key: identity.Key, Payload: payload}
		result, found, e := LookupReplay(tx, replayRequest)
		if e != nil {
			return Submission{}, e
		}
		if found {
			var ref struct {
				JobID string `json:"jobId"`
			}
			if json.Unmarshal(result, &ref) != nil || !identifier.MatchString(ref.JobID) {
				return Submission{}, ErrUnavailable
			}
			var raw string
			var out Outcome
			if err = tx.QueryRow(`SELECT outcome_json FROM jobs WHERE id=?`, ref.JobID).Scan(&raw); err != nil {
				return Submission{}, err
			}
			if err = json.Unmarshal([]byte(raw), &out); err != nil {
				return Submission{}, err
			}
			return Submission{out, true}, nil
		}
	}

	if !identifier.MatchString(id) || !contracts.ValidScanMode(mode) {
		return Submission{}, ErrInvalid
	}
	var profileJSON, rootsJSON, volumesJSON string
	if err = tx.QueryRow(`SELECT profile_json,roots_json,volumes_json FROM profiles WHERE id=?`, id).Scan(&profileJSON, &rootsJSON, &volumesJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Submission{}, ErrInvalid
		}
		return Submission{}, err
	}
	var p contracts.Profile
	var roots map[string]scanner.RootBinding
	var volumes map[string]string
	if json.Unmarshal([]byte(profileJSON), &p) != nil || json.Unmarshal([]byte(rootsJSON), &roots) != nil || json.Unmarshal([]byte(volumesJSON), &volumes) != nil {
		return Submission{}, ErrInvalid
	}
	if !scanProfileSupported(p) {
		return Submission{}, ErrInvalid
	}
	var out Outcome
	var startSources profileSources
	deduplicated := false
	pending, found, err := m.pendingProfile(tx, id)
	if err != nil {
		return Submission{}, err
	}
	if found {
		if pending.Job.ProfileRevision != p.Revision || pending.Job.ScanMode != mode {
			return Submission{}, ErrConflict
		}
		out = pending
		deduplicated = true
	} else {
		var waiting int
		if err = tx.QueryRow(`SELECT count(*) FROM scan_queue`).Scan(&waiting); err != nil {
			return Submission{}, err
		}
		if m.active != nil {
			waiting++
		}
		if waiting >= QueueCapacity+1 {
			return Submission{}, ErrQueueFull
		}
		started := now()
		trigger := "cli"
		if identity != nil {
			trigger = "ui"
		}
		var sourcesJSON string
		if err = tx.QueryRow(`SELECT sources_json FROM profile_sources WHERE profile_id=?`, p.ID).Scan(&sourcesJSON); err != nil {
			return Submission{}, errors.New("PROFILE_REIMPORT_REQUIRED")
		}
		sources, e := loadSources(sourcesJSON)
		if e != nil {
			return Submission{}, e
		}
		capture, e := captureScope(m.ctx, p, sources, roots, m.state, m.catalog)
		if e != nil {
			return Submission{}, e
		}
		startSources = capture.Sources
		manifest := capture.Manifest
		updatedSources, _ := json.Marshal(capture.Sources)
		updatedBindings, _ := json.Marshal(capture.Bindings)
		for _, r := range capture.Manifest.Roots {
			volumes[r.TargetID] = r.VolumeID
		}
		updatedVolumes, _ := json.Marshal(volumes)
		if _, err = tx.Exec(`UPDATE profile_sources SET sources_json=? WHERE profile_id=?`, string(updatedSources), p.ID); err != nil {
			return Submission{}, err
		}
		if _, err = tx.Exec(`UPDATE profiles SET roots_json=?,volumes_json=? WHERE id=?`, string(updatedBindings), string(updatedVolumes), p.ID); err != nil {
			return Submission{}, err
		}
		mapping, mappingErr := readFileTypeMapping(m.ctx, tx)
		if mappingErr != nil {
			return Submission{}, mappingErr
		}
		out = Outcome{FileTypeMapping: mapping, ScopeManifest: &manifest, ServiceUID: os.Geteuid(), Job: contracts.Job{ID: randomID(), ProfileID: id, ProfileRevision: p.Revision, CapturedProfile: p, Trigger: trigger, State: "queued", ScanMode: mode, CreatedAt: started, EntriesVisited: "0", BytesObserved: "0", Warnings: []contracts.APIError{}, ScannerVersion: index.ScannerVersion, SchemaVersion: index.GenerationSchema}}
		if identity != nil {
			out.PreviewPrincipal = identity.Principal
		}
		if len(capture.Added) > 0 {
			out.Job.Warnings = append(out.Job.Warnings, contracts.APIError{Code: "SCOPE_TARGET_ADDED", MessageKey: "scope.target_added", RequestID: out.Job.ID})
		}
		raw, marshalErr := json.Marshal(out)
		if marshalErr != nil {
			return Submission{}, marshalErr
		}
		if _, err = tx.Exec(`INSERT INTO jobs VALUES(?,?,?)`, out.Job.ID, out.Job.State, string(raw)); err != nil {
			return Submission{}, err
		}
		sourceBytes, e := json.Marshal(startSources)
		if e != nil {
			return Submission{}, e
		}
		if _, err = tx.Exec(`INSERT INTO scan_queue(job_id,profile_id,sources_json) VALUES(?,?,?)`, out.Job.ID, id, string(sourceBytes)); err != nil {
			return Submission{}, err
		}
		if err = recordJobPhaseTx(m.ctx, tx, out.Job.ID, "queued", started); err != nil {
			return Submission{}, err
		}
	}
	if identity != nil {
		result, _ := json.Marshal(struct {
			JobID string `json:"jobId"`
		}{out.Job.ID})
		if err = RecordReplay(tx, replayRequest, result); err != nil {
			return Submission{}, err
		}
	}
	// Return a detached value before asynchronous execution can begin.
	response, err := detachOutcome(out)
	if err != nil {
		return Submission{}, err
	}
	if err = CommitReplay(tx, func() {
		if !deduplicated {
			m.rememberPreviewOwner(out)
			m.ensureWorker()
		}
	}); err != nil {
		return Submission{}, err
	}

	return Submission{response, deduplicated}, nil
}

// Get retrieves a job after the submitting CLI or browser has disconnected.
func (m *Manager) Get(id string) (Outcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Outcome{}, ErrUnavailable
	}
	var raw string
	var o Outcome
	if !identifier.MatchString(id) {
		return o, ErrInvalid
	}
	if err := m.db.QueryRow(`SELECT outcome_json FROM jobs WHERE id=?`, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return o, ErrInvalid
		}
		return o, err
	}
	err := json.Unmarshal([]byte(raw), &o)
	if err != nil {
		return o, err
	}
	readCtx, cancelRead := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelRead()
	o.PhaseHistory, err = m.phaseHistory(readCtx, id)
	if err != nil {
		return o, err
	}
	status, e := m.storedDataStatus(readCtx, o.Job.ProfileID)
	if e != nil {
		return o, e
	}
	o.StoredData = &status
	o.Control, err = m.readJobControl(readCtx, id)
	return o, err
}

// execute stages a generation and publishes only after the database is closed.
func (m *Manager) execute(o Outcome, startSources profileSources) {
	defer func() {
		m.mu.Lock()
		if m.active != nil && m.active.Job.ID == o.Job.ID {
			m.preview = nil
			m.active = nil
		}
		m.mu.Unlock()
	}()
	// A job-local cancellation cannot stop the service or the next FIFO entry.
	m.mu.Lock()
	runctx := m.ctx
	o.PreviewPrincipal = m.previewOwners[o.Job.ID]
	delete(m.previewOwners, o.Job.ID)
	m.preview = nil
	var runner *jobRunner
	if m.runner != nil && m.runner.id == o.Job.ID {
		runner = m.runner
		runctx = m.runner.ctx
	}
	m.mu.Unlock()
	runctx = scanner.WithCheckpoint(runctx, func() error { return m.runningCheckpoint(&o, runner) })
	// Every exit flushes measured metadata counters, including cancellation.
	observer, finishProgress := m.trackProgress(o)
	defer finishProgress()
	runctx = scanner.WithProgress(runctx, observer)
	if o.PreviewPrincipal != "" {
		runctx = scanner.WithPreview(runctx, func(p scanner.Preview) { m.capturePreview(o, p) })
	}
	snapshot := randomID()
	generation := filepath.Join(m.state, "generations", snapshot)
	staging := generation + ".staging"
	// Queue claim already persisted preflight before dispatch.
	err := runctx.Err()
	// Resolve once from captured inputs; a later profile edit cannot alter this run's limits.
	policy, policyErr := resources.Resolve(o.Job.CapturedProfile.ResourceMode, o.Job.CapturedProfile.Limits)
	if err == nil {
		err = policyErr
	}
	if err == nil && m.phaseHook != nil {
		err = m.phaseHook("preflight")
	}
	if err == nil {
		err = scanner.Checkpoint(runctx)
	}
	if err == nil {
		err = m.preflightContext(runctx, &o)
	}
	if err == nil {
		err = m.phase(runctx, &o, "scanning")
	}
	var work *jobWorkDir
	if err == nil {
		work, err = createJobWork(filepath.Dir(staging), filepath.Base(staging), snapshot)
	}
	defer work.Close()
	var result scanner.Result
	end := now()
	if err == nil {
		var db *sql.DB
		db, err = index.Open(filepath.Join(staging, "index.db"))
		if err == nil {
			err = os.Chmod(filepath.Join(staging, "index.db"), 0600)
			if err == nil {
				err = index.ConfigureScan(runctx, db, policy)
			}
			if err == nil {
				if o.ScopeManifest == nil {
					err = errors.New("SCOPE_MANIFEST_REQUIRED")
				} else {
					result, err = scanner.ScanManifest(runctx, db, snapshot, o.Job.CapturedProfile, *o.ScopeManifest, o.FileTypeMapping)
					o.DuplicateEntries = result.DuplicateEntries
					// Join the bounded progress writer before validation or publication, including scan errors.
					finishProgress()
					endGaps := []index.EndValidationGap{}
					if err == nil {
						var canceled bool
						if err = scanner.Checkpoint(runctx); err != nil {
							canceled = true
						} else {
							endGaps, canceled = validateScopeEnd(runctx, o.Job.CapturedProfile, *o.ScopeManifest, startSources, m.catalog)
						}
						if err == nil && canceled {
							err = runctx.Err()
						} else if err == nil {
							err = recordScopeEnd(runctx, db, endGaps)
						}
						if err == nil {
							result.Errors += uint64(len(endGaps))
							for _, gap := range endGaps {
								o.Job.Warnings = append(o.Job.Warnings, contracts.APIError{Code: gap.Code, MessageKey: "scope.changed", RequestID: o.Job.ID})
							}
						}
					}
					if err == nil {
						// Capture time is immutable snapshot provenance, separate from final job completion.
						end = now()
						o.SnapshotEndedAt = &end
						// Completed traversal counters are measured metadata, not an estimated denominator.
						o.Job.EntriesVisited = fmt.Sprint(result.Entries)
						o.Job.BytesObserved = contracts.Bytes(result.Bytes)
						o.CoverageErrors = result.Errors
						// Directory totals are already committed by the bounded online scan writer.
						err = m.reserve()
					}
					if err == nil {
						state := "complete"
						if result.Errors > 0 {
							state = "partial"
						}
						err = m.phase(runctx, &o, "validating")
						if err == nil {
							err = index.FinalizeGeneration(runctx, db, index.GenerationHeader{AggregateVersion: index.AggregateVersion, FileTypeMapping: o.FileTypeMapping, EndValidation: endGaps, SnapshotID: snapshot, JobID: o.Job.ID, Profile: o.Job.CapturedProfile, Manifest: *o.ScopeManifest, StartedAt: *o.Job.StartedAt, EndedAt: end, ScannerVersion: o.Job.ScannerVersion, SchemaVersion: o.Job.SchemaVersion, Methods: capturedSizeMethods(), State: state, Entries: result.Entries, LogicalBytes: contracts.Bytes(result.Bytes), CoverageErrors: result.Errors})
						}
					}
				}
			}
			err = errors.Join(err, db.Close())
		}
	}
	o.Job.EntriesVisited = fmt.Sprint(result.Entries)
	o.Job.BytesObserved = contracts.Bytes(result.Bytes)
	if result.Bytes == "" {
		o.Job.BytesObserved = "0"
	}
	o.CoverageErrors = result.Errors
	state := "complete"
	if result.Errors > 0 {
		state = "partial"
		o.Job.Warnings = append(o.Job.Warnings, contracts.APIError{Code: "INCOMPLETE_COVERAGE", MessageKey: "scan.incomplete", RequestID: o.Job.ID})
	}
	if err == nil {
		err = m.reserve()
	}
	if err == nil {
		err = m.phase(runctx, &o, "publishing")
	}
	if err == nil {
		err = m.publishGeneration(runctx, o, staging, snapshot, state)
		if err == nil {
			return
		}
	}
	if errors.Is(err, ErrPublicationUncertain) {
		m.mu.Lock()
		m.closed = true
		m.cancel()
		m.mu.Unlock()
		return
	}
	o.Job.State = "failed"
	if errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && m.ctx.Err() != nil) {
		o.Job.State = m.cancellationState(o.Job.ID)
		if cleanupErr := work.cleanupCancelledGeneration(); cleanupErr != nil {
			o.Job.Warnings = append(o.Job.Warnings, contracts.APIError{Code: "WORK_CLEANUP_PENDING", MessageKey: "scan.cleanup_pending", RequestID: o.Job.ID})
		}
	}
	terminalAt := now()
	o.Job.EndedAt = &terminalAt
	o.SnapshotEndedAt = nil
	o.Job.ResultSnapshotID = nil
	o.SnapshotState = ""
	failureCode, failureKey := executionFailure(err), "scan.failed"
	if o.Job.State == "interrupted" {
		failureCode, failureKey = "SCAN_INTERRUPTED", "scan.interrupted"
	}
	o.Job.Warnings = append(o.Job.Warnings, contracts.APIError{Code: failureCode, MessageKey: failureKey, RequestID: o.Job.ID})
	m.mu.Lock()
	defer m.mu.Unlock()
	m.active = nil
	if e := m.persistOutcome(o); e != nil {
		m.closed = true
		m.cancel()
	}

}

// save persists an outcome using canonical JSON and an indexed state.
func (m *Manager) save(o Outcome) error {
	raw, err := json.Marshal(o)
	if err != nil {
		return err
	}
	_, err = m.db.Exec(`INSERT INTO jobs VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,outcome_json=excluded.outcome_json`, o.Job.ID, o.Job.State, string(raw))
	return err
}

// overlap includes ancestor and descendant relationships without prefix collisions.
func overlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(os.PathSeparator)) || strings.HasPrefix(b, a+string(os.PathSeparator))
}

// now uses a UTC timestamp independent of DSM display preferences.
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// randomID uses filesystem-safe cryptographic identifiers independent of source names.
func randomID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(value[:])
}
