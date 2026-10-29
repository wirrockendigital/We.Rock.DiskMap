// Package scanner collects metadata without reading or modifying source contents.
package scanner

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"math/big"
	"os"
	"strconv"
	"syscall"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/profiles"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/resources"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scopemanifest"
)

// Result contains measured progress and explicit incomplete coverage.
type Result struct {
	Entries uint64
	Bytes   string
	Errors  uint64
	// DuplicateEntries counts repeated enumeration records, never extra logical files.
	DuplicateEntries uint64
}

// ScanManifest consumes captured per-root identities and retains unavailable coverage.
func ScanManifest(ctx context.Context, db *sql.DB, snapshot string, profile contracts.Profile, manifest scopemanifest.ScopeManifest, mapping contracts.FileTypeMapping) (Result, error) {
	return scanManifestWithOperations(ctx, db, snapshot, profile, manifest, mapping, walkOperations{inspect: inspectEntry, directory: openDirectory, readDirectory: func(f *os.File, n int) ([]os.DirEntry, error) { return f.ReadDir(n) }})
}

// scanManifestWithOperations keeps deterministic source-failure tests on the production traversal.
func scanManifestWithOperations(ctx context.Context, db *sql.DB, snapshot string, profile contracts.Profile, manifest scopemanifest.ScopeManifest, mapping contracts.FileTypeMapping, operations walkOperations) (Result, error) {

	// Reject unsupported captured settings before opening any source or generation.
	policy, err := resources.Resolve(profile.ResourceMode, profile.Limits)
	if err != nil {
		return Result{}, err
	}
	pacer, err := resources.NewPacer(policy)
	if err != nil {
		return Result{}, err
	}
	operations.directoryBatch = policy.DirectoryBatch
	if manifest.ContractVersion != scopemanifest.ScopeManifestVersion || len(manifest.Roots) == 0 {
		return Result{}, errors.New("INVALID_SCOPE_MANIFEST")
	}
	// Compile captured profile rules before touching any source or generation.
	if err := index.ValidateQualifiedSubvolumes(manifest); err != nil {
		return Result{}, err
	}
	exclusions, err := profiles.CompileExclusions(profile.Exclusions)
	if err != nil {
		return Result{}, err
	}
	roots := map[string]RootBinding{}
	volumes := map[string]string{}
	for _, r := range manifest.Roots {
		if r.Binding != nil {
			b := r.Binding
			roots[r.TargetID] = RootBinding{Path: b.Path, Device: b.Device, Inode: b.Inode, MountID: b.MountID}
		}
		volumes[r.TargetID] = r.VolumeID
	}

	result := Result{Bytes: "0"}
	// Preview shares successful insertion boundaries and never performs source reads.
	preview := newPreview(ctx)
	defer preview.emit(true)
	total := new(big.Int)
	progress := Progress{KnownLogicalBytes: "0"}
	report := progressReporter(ctx)
	sample := func() Progress {
		progress.Entries = result.Entries
		progress.KnownLogicalBytes = total.String()
		progress.Warnings = result.Errors
		return progress
	}
	// Materialize decimal totals only when the throttled observer needs a sample.
	emit := func(final bool) { report(sample, final) }
	defer func() { emit(true) }()
	if err := index.InitializeGeneration(ctx, db, snapshot); err != nil {
		return result, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	writer, err := index.NewNodeWriter(ctx, tx, snapshot)
	if err != nil {
		return result, err
	}
	defer writer.Close()
	// Aggregates share the node transaction and retain only the current DFS ancestry.
	aggregates, err := index.NewOnlineAggregates(ctx, tx, mapping)
	if err != nil {
		return result, err
	}
	defer aggregates.Close()
	// Scope issues preserve their own target identity, including missing covered shares.
	issueCounts := map[string]uint64{}
	rootIDs := map[string]bool{}
	for _, root := range manifest.Roots {
		rootIDs[root.TargetID] = true
	}
	for _, issue := range manifest.Issues {
		code := sourceStateReason(issue.State)
		if issue.Code == "SCOPE_BINDING_CHANGED" {
			code = "SOURCE_CHANGED"
		}
		if err = index.RecordBoundary(ctx, tx, issue.TargetID, ".", code); err != nil {
			return result, err
		}
		issueCounts[issue.TargetID]++
		if err = pacer.Step(ctx); err != nil {
			return result, err
		}
		if !rootIDs[issue.TargetID] {
			result.Errors++
		}
	}
	for target, count := range issueCounts {
		if !rootIDs[target] {
			if _, err = tx.ExecContext(ctx, `INSERT INTO coverage VALUES(?,?,?)`, target, "partial", count); err != nil {
				return result, err
			}
		}
	}
	for _, manifestRoot := range manifest.Roots {
		target := manifestRoot.TargetID
		progress.TargetID = target
		emit(false)
		if err := Checkpoint(ctx); err != nil {
			return result, err
		}
		before := result.Errors
		result.Errors += issueCounts[target]
		// Only a descriptor matching the identity persisted at import may be scanned.
		root, e := openVerifiedRoot(roots[target])
		if _, bound := roots[target]; !bound {
			e = errors.New("ROOT_UNVERIFIED")
		}
		if e != nil {
			code := sourceReason(e)
			if _, bound := roots[target]; !bound {
				code = sourceStateReason(manifestRoot.State)
			}
			if issueCounts[target] == 0 {
				if err = index.RecordBoundary(ctx, tx, target, ".", code); err != nil {
					return result, err
				}
			}
			// A preflight issue already accounts for an unbound root; do not count it twice.
			if issueCounts[target] == 0 {
				result.Errors++
			}
			if _, e = tx.ExecContext(ctx, `INSERT INTO coverage VALUES(?,?,?)`, target, "partial", result.Errors-before); e != nil {
				return result, e
			}
			if err = pacer.Step(ctx); err != nil {
				return result, err
			}
			continue
		}
		directory, e := root.Open(".")
		if e != nil {
			root.Close()
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			if err = index.RecordBoundary(ctx, tx, target, ".", sourceReason(e)); err != nil {
				return result, err
			}
			result.Errors++
			if _, err = tx.ExecContext(ctx, `INSERT INTO coverage VALUES(?,?,?)`, target, "partial", result.Errors-before); err != nil {
				return result, err
			}
			if err = pacer.Step(ctx); err != nil {
				return result, err
			}
			continue
		}
		var share *string
		if manifestRoot.Kind == "share" {
			share = &target
		}
		rootID, e := writer.AddRoot(target, volumes[target], share)
		if e != nil {
			directory.Close()
			root.Close()
			return result, e
		}
		// Only the current DFS ancestry is retained, independent of the number of scanned files.
		parents := []scanParent{}

		operations.guardDirectory = subvolumeGuard(target, roots[target], manifest.QualifiedSubvolumes, NativeSubvolumeID)
		e = walkMetadata(ctx, directory, ".", roots[target].MountID, func(rel string, info os.FileInfo, walkErr error) (visitErr error) {
			defer emit(false)
			// Work pacing includes successfully recorded exclusions and source gaps, not just indexed nodes.
			// SQL, matcher and context failures are fatal and must retain their original error.
			defer func() {
				if visitErr == nil || errors.Is(visitErr, fs.SkipDir) {
					if err := pacer.Step(ctx); err != nil {
						visitErr = err
					}
				}
			}()
			// Package state is an explicit mandatory manifest boundary, checked before enumeration.
			for _, excluded := range manifest.Exclusions {
				if excluded.TargetID == target && excluded.Code == "PACKAGE_STATE_EXCLUDED" && excluded.RelativePath == rel && walkErr == nil && info.IsDir() {
					if e := index.RecordBoundary(ctx, tx, target, rel, "PACKAGE_STATE_EXCLUDED"); e != nil {
						return e
					}
					return fs.SkipDir
				}
			}

			// Rules prune directories before enumeration and omit files without opening their contents.
			matched := false
			if info != nil {
				var matchErr error
				matched, matchErr = exclusions.MatchContext(ctx, rel, info.IsDir())
				if matchErr != nil {
					return matchErr
				}
			}
			if matched {
				if e := index.RecordBoundary(ctx, tx, target, rel, "PROFILE_EXCLUDED"); e != nil {
					return e
				}
				if info.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if err := Checkpoint(ctx); err != nil {
				return err
			}
			if walkErr != nil {
				if e := index.RecordBoundary(ctx, tx, target, rel, sourceReason(walkErr)); e != nil {
					return e
				}
				result.Errors++
				return nil
			}
			stat := info.Sys().(*syscall.Stat_t)
			// A held descriptor can outlive its final pathname after unlink; it is not a current entry.
			if stat.Nlink == 0 {
				if e := index.RecordBoundary(ctx, tx, target, rel, "SOURCE_MISSING"); e != nil {
					return e
				}
				result.Errors++
				if info.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			kind := metadataKind(info.Mode())
			if kind == "" {
				if e := index.RecordBoundary(ctx, tx, target, rel, "SOURCE_UNVERIFIED"); e != nil {
					return e
				}
				result.Errors++
				return nil
			}
			var logical, allocated *contracts.Bytes
			if kind == "file" {
				logical, allocated = metadataBytes(info.Size(), stat.Blocks)
				if logical == nil {
					if e := index.RecordBoundary(ctx, tx, target, rel, "SOURCE_UNVERIFIED"); e != nil {
						return e
					}
					result.Errors++
				}
			}
			parentID, parentErr := scanParentID(&parents, rel)
			if parentErr != nil {
				return &Failure{"ANCESTRY", parentErr}
			}
			links := int(stat.Nlink)
			id, insertErr := writer.Insert(rootID, parentID, []byte(info.Name()), kind, logical, allocated, ObjectIdentity(info), &links)
			// Some filesystems repeat a directory entry across enumeration batches.
			// Keep one canonical node; changed identities remain an explicit coverage gap.
			if errors.Is(insertErr, index.ErrDuplicateEntry) || errors.Is(insertErr, index.ErrChangedEntry) {
				result.DuplicateEntries++
				if errors.Is(insertErr, index.ErrChangedEntry) {
					if err := index.RecordBoundary(ctx, tx, target, rel, "SOURCE_CHANGED"); err != nil {
						return err
					}
					result.Errors++
				}
				if info.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if insertErr != nil {
				return &Failure{"NODE_WRITE", insertErr}
			}
			// Observe only committed-to-transaction nodes; aggregate failures abort the whole scan.
			if err := aggregates.Observe(id, parentID, kind, []byte(info.Name()), logical, allocated); err != nil {
				return &Failure{"AGGREGATE", err}
			}
			preview.observe(target, rel, kind, logical)
			if kind == "directory" {
				parents = append(parents, scanParent{relative: rel, id: id})
			}
			// Counters share the successful insertion boundary, including SQLITE_FULL failures.
			if logical != nil {
				// The measured logical size is a validated nonnegative int64.
				total.Add(total, big.NewInt(info.Size()))
			}
			result.Entries++
			switch kind {
			case "file":
				progress.Files++
				if logical == nil {
					progress.UnknownLogical++
				}
			case "directory":
				progress.Directories++
			default:
				progress.Specials++
			}
			return nil
		}, operations)
		directory.Close()
		root.Close()
		if e != nil {
			// Preserve a more specific visitor failure; otherwise identify traversal itself.
			var failure *Failure
			if !errors.As(e, &failure) {
				e = &Failure{"TRAVERSAL", e}
			}
			return result, e
		}
		// Flush inserted empty or unreadable directories as well as normally exhausted branches.
		if err = aggregates.FinishRoot(); err != nil {
			return result, &Failure{"AGGREGATE_FINISH", err}
		}
		if err = Checkpoint(ctx); err != nil {
			return result, err
		}
		fresh, observed, recheckErr := OpenBoundRoot(roots[target].Path)
		if recheckErr != nil {
			if err = index.RecordBoundary(ctx, tx, target, ".", sourceReason(recheckErr)); err != nil {
				return result, err
			}
			result.Errors++
		} else {
			fresh.Close()
			if observed != roots[target] {
				if err = index.RecordBoundary(ctx, tx, target, ".", "SOURCE_CHANGED"); err != nil {
					return result, err
				}
				result.Errors++
			}
		}
		state := "complete"
		if result.Errors > before {
			state = "partial"
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO coverage VALUES(?,?,?)`, target, state, result.Errors-before); e != nil {
			return result, e
		}
	}
	result.Bytes = total.String()
	if err = tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

// displayPath renders invalid original bytes explicitly; private BLOB columns preserve exact identity.
func displayPath(value string) string {
	if utf8.ValidString(value) {
		return value
	}
	return strconv.QuoteToASCII(value)
}
