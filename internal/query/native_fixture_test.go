package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"path"
	"strconv"
	"sync"
)

// Test locators are generation-local positive integers allocated parent-first.
var fixtureIDs = struct {
	sync.Mutex
	generations map[string]map[string]string
}{generations: map[string]map[string]string{}}

func initializeQueryGeneration(ctx context.Context, db *sql.DB, snapshot string) error {
	fixtureIDs.Lock()
	fixtureIDs.generations[snapshot] = map[string]string{}
	fixtureIDs.Unlock()
	return index.InitializeGeneration(ctx, db, snapshot)
}
func fixtureNodeID(snapshot, target, relative string) string {
	// The streaming scale fixture already carries a unique numeric suffix; avoid an O(N) test map.
	if snapshot == "synthetic-v1" && target == "volume" {
		if relative == "." {
			return "1"
		}
		leaf := path.Base(relative)
		if len(leaf) > 1 && leaf[0] == 'n' {
			if n, err := strconv.ParseInt(leaf[1:], 10, 64); err == nil && n >= 0 && n < 1<<62 {
				return strconv.FormatInt(n+1, 10)
			}
		}
	}

	fixtureIDs.Lock()
	defer fixtureIDs.Unlock()
	ids := fixtureIDs.generations[snapshot]
	if ids == nil {
		ids = map[string]string{}
		fixtureIDs.generations[snapshot] = ids
	}
	var add func(string) string
	add = func(p string) string {
		key := target + "\x00" + p
		if id := ids[key]; id != "" {
			return id
		}
		if p != "." {
			add(path.Dir(p))
		}
		id := strconv.Itoa(len(ids) + 1)
		ids[key] = id
		return id
	}
	return add(relative)
}

// Test-authored DTOs enter only native normalized roots and numeric node columns.
func insertQueryFixtureNode(q interface {
	Exec(string, ...any) (sql.Result, error)
}, args ...any) (sql.Result, error) {
	var n contracts.Node
	if err := json.Unmarshal([]byte(args[2].(string)), &n); err != nil {
		return nil, err
	}
	target := args[3].(string)
	if _, err := q.Exec(`INSERT INTO roots(target_id,volume_id,share_id) SELECT ?,?,? WHERE NOT EXISTS(SELECT 1 FROM roots WHERE target_id=?)`, target, n.VolumeID, n.ShareID, target); err != nil {
		return nil, err
	}
	return q.Exec(`INSERT INTO nodes(id,parent_id,root_id,name_bytes,kind,logical_bytes,allocated_bytes,object_identity,link_count) SELECT ?,?,id,?,?,?,?,?,? FROM roots WHERE target_id=?`, n.ID, n.ParentID, args[4], n.Kind, n.LogicalBytes, n.AllocatedBytes, args[6], n.LinkCount, target)
}

// The transaction remains owned by the fixture; this adapter creates no compatibility schema.
type nativeFixtureStmt struct{ tx *sql.Tx }

func prepareQueryFixtureNode(tx *sql.Tx) (*nativeFixtureStmt, error) {
	return &nativeFixtureStmt{tx}, nil
}
func (s *nativeFixtureStmt) Exec(args ...any) (sql.Result, error) {
	return insertQueryFixtureNode(s.tx, args...)
}
func (s *nativeFixtureStmt) Close() error { return nil }
