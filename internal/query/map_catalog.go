package query

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// MapCategories stores only directory totals, never a second copy of millions of file names.
// Buckets are ordinary, system, recycle, and recycle-system; their sum is conserved.
type MapCategories struct{ Dirs map[int64]*MapDirectory }
type MapDirectory struct {
	Parent int64
	Flags  uint8
	Bytes  [4]uint64
	Files  uint64
}

func mapFlags(name string) uint8 {
	n := strings.ToLower(name)
	if n == "#recycle" {
		return 2
	}
	if n == "@eadir" || n == ".ds_store" || n == ".appledouble" || n == ".spotlight-v100" || n == ".trashes" || n == ".fseventsd" || strings.HasPrefix(n, ".smb") || strings.HasPrefix(n, "._") {
		return 1
	}
	return 0
}

// BuildMapCategories bounds directory metadata independently of the number of files.
// Two million directories cover large NAS snapshots without retaining file names.
func BuildMapCategories(ctx context.Context, db *sql.DB) (*MapCategories, error) {
	return buildMapCategories(ctx, db, 2000000)
}

// A separate budget argument permits small fixtures to exercise the hard memory boundary.
func buildMapCategories(ctx context.Context, db *sql.DB, directoryLimit int) (*MapCategories, error) {
	out := &MapCategories{Dirs: map[int64]*MapDirectory{}}
	order := []int64{}
	rows, err := db.QueryContext(ctx, `SELECT id,parent_id,name_bytes,kind,logical_bytes FROM nodes ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	// Reuse SQL destinations instead of allocating them for every file in a large snapshot.
	var id int64
	var parent sql.NullInt64
	var name []byte
	var kind string
	var size sql.NullString
	for rows.Next() {
		if err = rows.Scan(&id, &parent, &name, &kind, &size); err != nil {
			return nil, err
		}
		flags := mapFlags(string(name))
		var p *MapDirectory
		if parent.Valid {
			p = out.Dirs[parent.Int64]
			if p == nil {
				return nil, ErrSnapshot
			}
			flags |= p.Flags
		}
		if kind == "directory" {
			if len(order) >= directoryLimit {
				return nil, ErrBudget
			}
			out.Dirs[id] = &MapDirectory{Parent: parent.Int64, Flags: flags}
			order = append(order, id)
		} else if kind == "file" && p != nil {
			p.Files++
			if size.Valid {
				b, e := strconv.ParseUint(size.String, 10, 64)
				if e != nil {
					return nil, ErrSnapshot
				}
				if p.Bytes[flags] > ^uint64(0)-b {
					return nil, ErrSnapshot
				}
				p.Bytes[flags] += b
			}
		}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i := len(order) - 1; i >= 0; i-- {
		d := out.Dirs[order[i]]
		if p := out.Dirs[d.Parent]; p != nil {
			p.Files += d.Files
			for k, b := range d.Bytes {
				if p.Bytes[k] > ^uint64(0)-b {
					return nil, ErrSnapshot
				}
				p.Bytes[k] += b
			}
		}
	}
	return out, nil
}
