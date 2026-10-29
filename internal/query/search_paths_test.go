package query

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// Long repeated ancestry previously exceeded the global expanded-path budget before any search.
func TestSearchPathsCompactAndScoped(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`CREATE TABLE nodes(id INTEGER PRIMARY KEY,parent_id INTEGER,name_bytes BLOB,kind TEXT); CREATE INDEX parents ON nodes(parent_id)`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO nodes VALUES(?,?,?,'directory')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.Exec(1, nil, []byte(".")); err != nil {
		t.Fatal(err)
	}
	name := strings.Repeat("x", 180)
	id := int64(1)
	expanded := 0
	for branch := 0; branch < 25; branch++ {
		parent := int64(1)
		for depth := 1; depth <= 100; depth++ {
			id++
			if _, err = stmt.Exec(id, parent, []byte(name)); err != nil {
				t.Fatal(err)
			}
			parent = id
			expanded += depth*(len(name)+1) + 64
		}
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if expanded <= 16*1024*1024 {
		t.Fatal("fixture does not exceed former budget")
	}
	ctx := context.Background()
	all, err := searchDirectoryPaths(ctx, db, searchScope{})
	if err != nil {
		t.Fatal(err)
	}
	path, err := all.path(101)
	if err != nil || strings.Count(string(path), "/") != 99 {
		t.Fatal("ancestry lost", err)
	}
	direct, err := searchDirectoryPaths(ctx, db, searchScope{parent: "2", direct: true})
	if err != nil || len(direct.parts) != 2 {
		t.Fatal("direct query loaded unrelated directories", err)
	}
	subtree, err := searchDirectoryPaths(ctx, db, searchScope{parent: "2"})
	if err != nil || len(subtree.parts) != 101 {
		t.Fatal("subtree query escaped its scope", err)
	}
	for n := int64(2); n <= id; n++ {
		if _, err = all.path(n); err != nil {
			t.Fatal(err)
		}
	}
	if len(all.cache) > 256 || all.cachedBytes > 1024*1024 {
		t.Fatal("expanded cache grew unbounded")
	}
}
