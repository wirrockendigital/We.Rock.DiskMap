package index

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// Online vectors must equal the independent SQL postorder reference, including unknown and huge values.
func TestOnlineAggregatesReference(t *testing.T) {
	db, tx, nodes, rid, root := numericFixture(t)
	writer, err := NewOnlineAggregates(context.Background(), tx, DefaultFileTypeMapping())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err = writer.Observe(root, 0, "directory", []byte("volume"), nil, nil); err != nil {
		t.Fatal(err)
	}
	huge := contracts.Bytes("184467440737095516160")
	zero := contracts.Bytes("0")
	insert := func(parent int64, name, kind string, size *contracts.Bytes) int64 {
		t.Helper()
		id, e := nodes.Insert(rid, parent, []byte(name), kind, size, nil, "identity", nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = writer.Observe(id, parent, kind, []byte(name), size, nil); e != nil {
			t.Fatal(e)
		}
		return id
	}
	dir := insert(root, "nested", "directory", nil)
	insert(dir, "image.jpg", "file", &huge)
	empty := insert(dir, "empty", "directory", nil)
	_ = empty
	insert(root, "unknown.txt", "file", nil)
	insert(root, "zero.zip", "file", &zero)
	insert(root, "link", "symlink", nil)
	if err = writer.FinishRoot(); err != nil {
		t.Fatal(err)
	}
	if len(writer.stack) != 0 {
		t.Fatal("unclosed ancestry")
	}
	// A second source remains a separate aggregate root in the same transaction.
	secondRoot, err := nodes.AddRoot("second", "second", nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := nodes.Insert(secondRoot, 0, []byte("second"), "directory", nil, nil, "identity", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Observe(second, 0, "directory", []byte("second"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = writer.FinishRoot(); err != nil {
		t.Fatal(err)
	}

	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	read := func() map[int64]string {
		t.Helper()
		rows, e := db.Query(`SELECT node_id,aggregate_json FROM directory_aggregates`)
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		result := map[int64]string{}
		for rows.Next() {
			var id int64
			var raw string
			if e = rows.Scan(&id, &raw); e != nil {
				t.Fatal(e)
			}
			result[id] = raw
		}
		if e = rows.Err(); e != nil {
			t.Fatal(e)
		}
		return result
	}
	online := read()
	if _, err = db.Exec(`DELETE FROM directory_aggregates; DELETE FROM aggregate_mapping`); err != nil {
		t.Fatal(err)
	}
	if err = BuildDerivedProjections(context.Background(), db, DefaultFileTypeMapping()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(online, read()) {
		t.Fatal("online vectors differ from independent postorder")
	}
}

// Cancellation and durable write errors poison the writer until the caller rolls back.
func TestOnlineAggregatesFailure(t *testing.T) {
	for _, mode := range []string{"cancel", "write", "order", "root"} {
		t.Run(mode, func(t *testing.T) {
			_, tx, _, _, root := numericFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			writer, err := NewOnlineAggregates(ctx, tx, DefaultFileTypeMapping())
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if err = writer.Observe(root, 0, "directory", []byte("volume"), nil, nil); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "cancel":
				cancel()
				err = writer.FinishRoot()
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case "write":
				if _, err = tx.Exec(`CREATE TRIGGER reject_online BEFORE INSERT ON directory_aggregates BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
					t.Fatal(err)
				}
				err = writer.FinishRoot()
			case "order":
				err = writer.Observe(root+1, 999, "file", []byte("file"), nil, nil)
			case "root":
				err = writer.Observe(root+1, 0, "directory", []byte("other"), nil, nil)
			}
			if err == nil {
				t.Fatal("invalid stream accepted")
			}
			if again := writer.FinishRoot(); again != err {
				t.Fatal("first failure not preserved", again, err)
			}
		})
	}
}

// The stack grows with directory ancestry and does not grow with sibling file count.
func TestOnlineAggregatesStackBound(t *testing.T) {
	_, tx, nodes, rid, root := numericFixture(t)
	writer, err := NewOnlineAggregates(context.Background(), tx, DefaultFileTypeMapping())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err = writer.Observe(root, 0, "directory", []byte("volume"), nil, nil); err != nil {
		t.Fatal(err)
	}
	parent := root
	for i := 0; i < 256; i++ {
		id, e := nodes.Insert(rid, parent, []byte("d"), "directory", nil, nil, "identity", nil)
		if e != nil {
			t.Fatal(e)
		}
		if e = writer.Observe(id, parent, "directory", []byte("d"), nil, nil); e != nil {
			t.Fatal(e)
		}
		parent = id
	}
	if len(writer.stack) != 257 {
		t.Fatal(len(writer.stack))
	}
	for i := 0; i < 10000; i++ {
		// The observer does not retain file rows; numeric locators are supplied by its scan owner.
		if err = writer.Observe(parent+1+int64(i), parent, "file", []byte("f"), nil, nil); err != nil {
			t.Fatal(err)
		}
		if len(writer.stack) != 257 {
			t.Fatal("file expanded stack")
		}
	}
	if err = writer.Observe(parent+10001, parent, "directory", []byte("too-deep"), nil, nil); err == nil {
		t.Fatal("unbounded ancestry")
	}
}

// Projection writes are owned by the scan transaction and disappear with its rollback.
func TestOnlineAggregatesRollback(t *testing.T) {
	db, tx, _, _, root := numericFixture(t)
	writer, err := NewOnlineAggregates(context.Background(), tx, DefaultFileTypeMapping())
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Observe(root, 0, "directory", []byte("volume"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if err = writer.FinishRoot(); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM directory_aggregates)+(SELECT count(*) FROM aggregate_mapping)`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
