package query

import (
	"context"
	"database/sql"
	"strings"
)

type searchPathPart struct {
	parent int64
	name   []byte
}
type searchPathIndex struct {
	parts       map[int64]searchPathPart
	cache       map[int64][]byte
	cachedBytes int
}

// Keep each directory name once; expanded ancestor paths are bounded and reconstructed on demand.
func searchDirectoryPaths(ctx context.Context, db *sql.DB, scope searchScope) (*searchPathIndex, error) {
	query := `SELECT id,parent_id,name_bytes FROM nodes WHERE kind='directory' ORDER BY id`
	var args []any
	if scope.parent != "" {
		// Direct lists need only their parent chain; recursive lists add directories in that subtree.
		query = `WITH RECURSIVE ancestors(id,parent_id) AS (SELECT id,parent_id FROM nodes WHERE id=? UNION ALL SELECT n.id,n.parent_id FROM nodes n JOIN ancestors a ON n.id=a.parent_id)`
		args = append(args, scope.parent)
		wanted := `SELECT id FROM ancestors`
		if !scope.direct {
			query += `, descendants(id) AS (SELECT id FROM nodes WHERE id=? UNION ALL SELECT n.id FROM nodes n JOIN descendants d ON n.parent_id=d.id WHERE n.kind='directory')`
			args = append(args, scope.parent)
			wanted += ` UNION SELECT id FROM descendants`
		}
		query += ` SELECT id,parent_id,name_bytes FROM nodes WHERE kind='directory' AND id IN (` + wanted + `) ORDER BY id`
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &searchPathIndex{parts: map[int64]searchPathPart{}, cache: map[int64][]byte{}}
	retained := 0
	for rows.Next() {
		var id int64
		var parent sql.NullInt64
		var name []byte
		if err = rows.Scan(&id, &parent, &name); err != nil {
			return nil, err
		}
		if parent.Valid {
			if _, ok := out.parts[parent.Int64]; !ok || parent.Int64 >= id || len(name) == 0 || strings.ContainsAny(string(name), "/\x00") || string(name) == "." || string(name) == ".." {
				return nil, ErrSnapshot
			}
		}
		// Bound compact metadata, not the sum of repeatedly expanded ancestor names.
		retained += len(name) + 64
		if retained > 64*1024*1024 {
			return nil, ErrBudget
		}
		out.parts[id] = searchPathPart{parent: parent.Int64, name: name}
	}
	return out, rows.Err()
}

// Called only for a candidate whose filename did not already satisfy the search.
func (p *searchPathIndex) path(id int64) ([]byte, error) {
	if value, ok := p.cache[id]; ok {
		return value, nil
	}
	chain := []searchPathPart{}
	current := id
	var base []byte
	for depth := 0; ; depth++ {
		if depth > 257 {
			return nil, ErrSnapshot
		}
		if value, ok := p.cache[current]; ok {
			base = value
			break
		}
		part, ok := p.parts[current]
		if !ok {
			return nil, ErrSnapshot
		}
		if part.parent == 0 {
			base = []byte(".")
			break
		}
		chain = append(chain, part)
		current = part.parent
	}
	value := append([]byte{}, base...)
	for i := len(chain) - 1; i >= 0; i-- {
		if string(value) == "." {
			value = value[:0]
		} else {
			value = append(value, '/')
		}
		value = append(value, chain[i].name...)
		if len(value) > 1024*1024 {
			return nil, ErrBudget
		}
	}
	if len(p.cache) >= 256 || p.cachedBytes+len(value) > 1024*1024 {
		p.cache = map[int64][]byte{}
		p.cachedBytes = 0
	}
	p.cache[id] = value
	p.cachedBytes += len(value)
	return value, nil
}
