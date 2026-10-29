package index

import (
	"context"
	"path/filepath"
	"testing"
)

// TestIntegrityRejectsInvalidSQLiteRecords detects engine-level corruption independently of node semantics.
func TestIntegrityRejectsInvalidSQLiteRecords(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "integrity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TABLE checked(v INTEGER CHECK(v>0)); PRAGMA ignore_check_constraints=ON; INSERT INTO checked VALUES(-1); PRAGMA ignore_check_constraints=OFF`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = validateSQLite(context.Background(), tx); err == nil {
		t.Fatal("engine integrity violation accepted")
	}
}
