package index

import "testing"

// Exercise the real SQLite driver without a platform service or external database.
func TestSQLiteRoundTrip(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE probe(bytes TEXT, unknown TEXT); INSERT INTO probe VALUES ('9007199254740993', NULL)"); err != nil {
		t.Fatal(err)
	}
	var value string
	var unknown any
	if err = db.QueryRow("SELECT bytes, unknown FROM probe").Scan(&value, &unknown); err != nil {
		t.Fatal(err)
	}
	if value != "9007199254740993" || unknown != nil {
		t.Fatal("lossy database round trip")
	}
}
