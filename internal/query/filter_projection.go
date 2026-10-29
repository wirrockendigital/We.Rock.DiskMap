package query

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
)

const FilterProjectionVersion = 2

var ErrFilterPending = errors.New("FILTER_PENDING")
var ErrFilterProjection = errors.New("INVALID_FILTER_PROJECTION")

// FilterBuildProgress exposes work counters, never unfinished match totals.
type FilterBuildProgress struct {
	Key           string          `json:"key"`
	Phase         string          `json:"phase"`
	ProcessedRows contracts.Bytes `json:"processedRows"`
	Complete      bool            `json:"complete"`
}

// FilterProjectionKey binds derived data to the complete immutable source and normalized predicates.
func FilterProjectionKey(h index.GenerationHeader, metric string, f Filter) (string, error) {
	f, err := f.Canonical()
	if err != nil {
		return "", err
	}
	if metric != "logical" && metric != "allocated" {
		return "", ErrRequest
	}
	b, err := json.Marshal(struct {
		Version int
		Header  index.GenerationHeader
		Metric  string
		Filter  Filter
	}{FilterProjectionVersion, h, metric, f})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}

// InitializeFilterProjection creates only a caller-owned cache; the source is never mutated.
func InitializeFilterProjection(ctx context.Context, source, cache *sql.DB, h index.GenerationHeader, metric string, f Filter) error {
	key, err := FilterProjectionKey(h, metric, f)
	if err != nil {
		return err
	}
	if err = checkHeader(ctx, source, h, Request{SnapshotID: h.SnapshotID}); err != nil {
		return err
	}
	// Partition metadata is derived once; each later batch performs only indexed cache lookups.
	topology := &engine{ctx: ctx, db: source, h: h, r: Request{SnapshotID: h.SnapshotID, Metric: metric}}
	if err := topology.topology(); err != nil {
		return err
	}
	tx, err := cache.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `CREATE TABLE filter_state(singleton INTEGER PRIMARY KEY CHECK(singleton=1),identity TEXT NOT NULL,phase TEXT NOT NULL,cursor_row INTEGER,cursor_depth INTEGER,cursor_id TEXT,processed TEXT NOT NULL);CREATE TABLE filter_nodes(source_row INTEGER UNIQUE NOT NULL,id TEXT PRIMARY KEY,parent_id TEXT,depth INTEGER NOT NULL,rank INTEGER GENERATED ALWAYS AS (-depth) STORED,kind TEXT NOT NULL,hit INTEGER NOT NULL,covered INTEGER NOT NULL DEFAULT 0,original TEXT NOT NULL,matched TEXT NOT NULL,direct TEXT NOT NULL);CREATE INDEX filter_depth ON filter_nodes(depth,id);CREATE INDEX filter_depth_desc ON filter_nodes(rank,id);CREATE INDEX filter_parent ON filter_nodes(parent_id,source_row);CREATE TABLE filter_source_parts(id TEXT PRIMARY KEY,summary TEXT,detached INTEGER NOT NULL);INSERT INTO filter_state VALUES(1,?,'collect',NULL,NULL,NULL,'0')`, key)
	if err != nil {
		return err
	}
	for id, v := range topology.projected {
		if _, err = tx.ExecContext(ctx, `INSERT INTO filter_source_parts VALUES(?,?,0)`, id, summaryJSON(v.node.Summary)); err != nil {
			return err
		}
	}
	for _, children := range topology.excluded {
		for id := range children {
			if _, err = tx.ExecContext(ctx, `INSERT INTO filter_source_parts VALUES(?,NULL,1)`, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

type filterState struct {
	key, phase string
	row, depth sql.NullInt64
	id         sql.NullString
	processed  contracts.Bytes
}

func readFilterState(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (filterState, error) {
	var s filterState
	err := q.QueryRowContext(ctx, `SELECT identity,phase,cursor_row,cursor_depth,cursor_id,processed FROM filter_state WHERE singleton=1`).Scan(&s.key, &s.phase, &s.row, &s.depth, &s.id, &s.processed)
	return s, err
}
func summaryJSON(s Summary) string { b, _ := json.Marshal(s); return string(b) }
func parseSummary(s string) (Summary, error) {
	v := zero()
	if len(s) > 2048 || json.Unmarshal([]byte(s), &v) != nil {
		return v, ErrFilterProjection
	}
	return merge(zero(), v, false)
}

// BuildFilterProjectionStep commits at most 256 source objects or cache objects per invocation.
// Source and cache must be distinct; the manager serializes calls and pins the source lease.
func BuildFilterProjectionStep(ctx context.Context, source, cache *sql.DB, h index.GenerationHeader, metric string, f Filter, maxRows int) (FilterBuildProgress, error) {
	if maxRows < 1 || maxRows > 256 || source == cache {
		return FilterBuildProgress{}, ErrRequest
	}
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	key, err := FilterProjectionKey(h, metric, f)
	if err != nil {
		return FilterBuildProgress{}, err
	}
	f, err = f.Canonical()
	if err != nil {
		return FilterBuildProgress{}, err
	}
	tx, err := cache.BeginTx(ctx, nil)
	if err != nil {
		return FilterBuildProgress{}, err
	}
	defer tx.Rollback()
	s, err := readFilterState(ctx, tx)
	if err != nil || s.key != key {
		return FilterBuildProgress{}, ErrFilterProjection
	}
	if s.phase == "complete" {
		return FilterBuildProgress{key, s.phase, s.processed, true}, nil
	}
	count := 0
	if s.phase == "collect" {
		reader, e := index.NewNodeReader(ctx, source)
		if e != nil {
			return FilterBuildProgress{}, e
		}
		batch, e := reader.Page(s.row.Int64, maxRows)
		if e != nil {
			return FilterBuildProgress{}, e
		}
		for _, stored := range batch {
			row, id, parent, path, name := stored.RowID, stored.Node.ID, stored.Node.ParentID, stored.Path, stored.Name
			n := stored.Node
			engine := engine{ctx: ctx, db: source, h: h, r: Request{SnapshotID: h.SnapshotID, Metric: metric}}
			rec, er := engine.decodeRecord(stored)
			if er != nil {
				e = er
				break
			}
			if n.Kind == "directory" {
				var projected sql.NullString
				var detached bool
				correctionErr := tx.QueryRowContext(ctx, `SELECT summary,detached FROM filter_source_parts WHERE id=?`, id).Scan(&projected, &detached)
				if correctionErr != nil && !errors.Is(correctionErr, sql.ErrNoRows) {
					e = correctionErr
					break
				}
				if correctionErr == nil {
					if detached {
						parent = nil
					}
					if projected.Valid {
						rec.node.Summary, e = parseSummary(projected.String)
						if e != nil {
							break
						}
					}
				}
			}
			var size *contracts.Bytes
			if (n.Kind == "file" || n.Kind == "directory") && rec.node.Summary.UnknownFileCount == "0" {
				v := rec.node.Summary.KnownBytes
				size = &v
			}
			depth := 0
			if string(path) != "." {
				depth = strings.Count(string(path), "/") + 1
			}
			_, e = tx.ExecContext(ctx, `INSERT INTO filter_nodes(source_row,id,parent_id,depth,kind,hit,original,matched,direct)VALUES(?,?,?,?,?,?,?,?,?)`, row, id, parent, depth, n.Kind, f.matches(n, name, size, h.FileTypeMapping), summaryJSON(rec.node.Summary), summaryJSON(zero()), summaryJSON(zero()))
			if e != nil {
				break
			}
			s.row = sql.NullInt64{Int64: row, Valid: true}
			count++
		}
		if e != nil {
			return FilterBuildProgress{}, e
		}
		if count < maxRows {
			s.phase = "inherit"
			s.row = sql.NullInt64{}
			s.depth = sql.NullInt64{}
			s.id = sql.NullString{}
		}
	} else if s.phase == "inherit" || s.phase == "aggregate" {
		column := "depth"
		cursorDepth := s.depth.Int64
		if s.phase == "aggregate" {
			column = "rank"
			cursorDepth = -cursorDepth
		}
		query := `SELECT id,parent_id,depth,kind,hit,covered,original,matched,direct FROM filter_nodes`
		args := []any{}
		if s.depth.Valid {
			query += ` WHERE (` + column + `,id)>(?,?)`
			args = append(args, cursorDepth, s.id.String)
		}
		query += ` ORDER BY ` + column + `,id ASC LIMIT ?`
		args = append(args, maxRows)
		rows, e := tx.QueryContext(ctx, query, args...)
		if e != nil {
			return FilterBuildProgress{}, e
		}
		type entry struct {
			id                        string
			parent                    *string
			depth                     int64
			kind                      string
			hit, covered              bool
			original, matched, direct string
		}
		list := []entry{}
		for rows.Next() {
			var v entry
			if e = rows.Scan(&v.id, &v.parent, &v.depth, &v.kind, &v.hit, &v.covered, &v.original, &v.matched, &v.direct); e != nil {
				break
			}
			list = append(list, v)
		}
		if e == nil {
			e = rows.Err()
		}
		rows.Close()
		if e != nil {
			return FilterBuildProgress{}, e
		}
		for _, v := range list {
			if s.phase == "inherit" {
				covered := v.hit && v.kind == "directory"
				if v.parent != nil {
					var pc bool
					if e = tx.QueryRowContext(ctx, `SELECT covered FROM filter_nodes WHERE id=?`, *v.parent).Scan(&pc); e != nil {
						return FilterBuildProgress{}, e
					}
					covered = covered || pc
				}
				own := zero()
				if covered || v.hit {
					if v.kind == "directory" {
						own.DirectoryCount = "1"
					} else {
						own, e = parseSummary(v.original)
						if e != nil {
							return FilterBuildProgress{}, e
						}
					}
				}
				if _, e = tx.ExecContext(ctx, `UPDATE filter_nodes SET covered=?,matched=? WHERE id=?`, covered, summaryJSON(own), v.id); e != nil {
					return FilterBuildProgress{}, e
				}
			} else {
				// Refresh after earlier children in this same batch updated the parent's accumulator.
				var raw string
				if e = tx.QueryRowContext(ctx, `SELECT matched FROM filter_nodes WHERE id=?`, v.id).Scan(&raw); e != nil {
					return FilterBuildProgress{}, e
				}
				m, e := parseSummary(raw)
				if e != nil {
					return FilterBuildProgress{}, e
				}
				if v.kind == "directory" && countSummary(m) != "0" && !v.covered && !v.hit {
					m.DirectoryCount, e = arithmetic(m.DirectoryCount, "1", false)
					if e != nil {
						return FilterBuildProgress{}, e
					}
				}
				if _, e = tx.ExecContext(ctx, `UPDATE filter_nodes SET matched=? WHERE id=?`, summaryJSON(m), v.id); e != nil {
					return FilterBuildProgress{}, e
				}
				if v.parent != nil && countSummary(m) != "0" {
					var pm, pd string
					if e = tx.QueryRowContext(ctx, `SELECT matched,direct FROM filter_nodes WHERE id=?`, *v.parent).Scan(&pm, &pd); e != nil {
						return FilterBuildProgress{}, e
					}
					parent, e := parseSummary(pm)
					if e != nil {
						return FilterBuildProgress{}, e
					}
					direct, e := parseSummary(pd)
					if e != nil {
						return FilterBuildProgress{}, e
					}
					parent, e = merge(parent, m, false)
					if e != nil {
						return FilterBuildProgress{}, e
					}
					if countSummary(m) != "0" {
						piece := zero()
						switch v.kind {
						case "file":
							piece.FileCount = "1"
						case "directory":
							piece.DirectoryCount = "1"
						default:
							piece.SpecialCount = "1"
						}
						direct, e = merge(direct, piece, false)
						if e != nil {
							return FilterBuildProgress{}, e
						}
					}
					if _, e = tx.ExecContext(ctx, `UPDATE filter_nodes SET matched=?,direct=? WHERE id=?`, summaryJSON(parent), summaryJSON(direct), *v.parent); e != nil {
						return FilterBuildProgress{}, e
					}
				}
			}
			count++
			s.depth = sql.NullInt64{Int64: v.depth, Valid: true}
			s.id = sql.NullString{String: v.id, Valid: true}
		}
		if count < maxRows {
			if s.phase == "inherit" {
				s.phase = "aggregate"
				s.depth = sql.NullInt64{}
				s.id = sql.NullString{}
			} else {
				s.phase = "complete"
			}
		}
	} else {
		return FilterBuildProgress{}, ErrFilterProjection
	}
	s.processed, err = arithmetic(s.processed, contracts.Bytes(fmt.Sprint(count)), false)
	if err != nil {
		return FilterBuildProgress{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE filter_state SET phase=?,cursor_row=?,cursor_depth=?,cursor_id=?,processed=? WHERE singleton=1`, s.phase, s.row, s.depth, s.id, s.processed); err != nil {
		return FilterBuildProgress{}, err
	}
	if err = tx.Commit(); err != nil {
		return FilterBuildProgress{}, err
	}
	return FilterBuildProgress{key, s.phase, s.processed, s.phase == "complete"}, nil
}

// countSummary keeps local row counters distinct from the shared exact summary counter.
func countSummary(s Summary) contracts.Bytes { return count(s) }
