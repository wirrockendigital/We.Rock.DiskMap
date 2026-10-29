package nativefixture

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"os"
	"testing"
)

// Unsupported formats are rejected before output creation; no implicit migration is provided.
func TestTransferRejectsOldSchema(t *testing.T) {
	for _, version := range []string{"10", "11", "12"} {
		t.Run(version, func(t *testing.T) {
			o := smallOptions(t)
			db, err := index.Open(o.Source)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.Exec("PRAGMA user_version=" + version); err != nil {
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(o.Source)
			if err != nil {
				t.Fatal(err)
			}
			o.ExpectedSHA, err = hashHeld(context.Background(), f)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			if _, err = transfer(context.Background(), o); err == nil {
				t.Fatal("old schema accepted")
			}
			if _, err = os.Stat(o.Output); !os.IsNotExist(err) {
				t.Fatal("output created for unsupported source", err)
			}
		})
	}
}
