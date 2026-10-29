package query

import (
	"context"
	"testing"
)

// Every breadcrumb follows actual parent edges, including the canonical virtual NAS and volume nodes.
func TestNavigationVerifiedParents(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir())
	var file string
	if err := db.QueryRow(`SELECT id FROM nodes WHERE parent_id IS NOT NULL LIMIT 1`).Scan(&file); err != nil {
		t.Fatal(err)
	}
	nav, err := ReadNavigation(context.Background(), db, h, file, &file)
	if err != nil || len(nav.Breadcrumb) != 4 || nav.Breadcrumb[0].Kind != "nas" || nav.Breadcrumb[1].Kind != "volume" || nav.Breadcrumb[2].Kind != "other" || nav.Breadcrumb[2].Provenance == nil || nav.Breadcrumb[2].Provenance.Authority != "synthetic-fixture" || nav.Breadcrumb[3].Kind != "directory" || nav.SelectedNodeID == nil || nav.Reasons[0] != "ROOT_ANCESTOR" {
		t.Fatal(nav, err)
	}
	root := nav.RootID
	nas, err := ReadNavigation(context.Background(), db, h, "nas", &root)
	if err != nil || len(nas.Breadcrumb) != 1 || nas.ParentID != nil || nas.SelectedNodeID == nil {
		t.Fatal(nas, err)
	}
	missing := "unknown"
	fallback, err := ReadNavigation(context.Background(), db, h, missing, &missing)
	if err != nil || fallback.RootID != nas.RootID || len(fallback.Reasons) != 2 {
		t.Fatal(fallback, err)
	}
}
