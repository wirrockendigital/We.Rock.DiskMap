package service

import (
	"context"
	"encoding/json"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The actual public route reuses catalog authority and never returns a physical source path.
func TestScopeFoldersRoute(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	share := filepath.Join(base, "share")
	if err = os.MkdirAll(filepath.Join(share, "child"), 0700); err != nil {
		t.Fatal(err)
	}
	volumes := func(context.Context, []contracts.Volume) (inventory.Result, error) {
		return inventory.Result{Volumes: []contracts.Volume{{ID: "volume-a", Name: "Volume", MountPath: base, State: "reachable"}}, Issues: []inventory.Issue{}}, nil
	}
	shares := func(context.Context, []contracts.Volume, []contracts.Share) (inventory.ShareResult, error) {
		return inventory.ShareResult{Shares: []contracts.Share{{ID: "share-a", Name: "Share", VolumeID: "volume-a", PhysicalPath: share, State: "reachable", Readable: true}}, Issues: []inventory.Issue{}}, nil
	}
	mux := http.NewServeMux()
	registerScopePresentation(mux, newInventoryHandler(t.TempDir(), volumes, shares))
	for _, tc := range []struct {
		path, principal string
		want            int
	}{{".", "admin", 200}, {"../", "admin", 400}, {".", "", 400}} {
		raw, _ := json.Marshal(inventory.FolderRequest{ContractVersion: 1, ShareID: "share-a", RelativePath: tc.path})
		r := httptest.NewRequest("POST", "/api/v1/scope-folders", strings.NewReader(string(raw)))
		r.Header.Set("X-DiskMap-Principal", tc.principal)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != tc.want || strings.Contains(w.Body.String(), base) {
			t.Fatalf("route %s: %d", tc.path, w.Code)
		}
		if w.Code == 200 {
			var out contracts.Envelope[inventory.ScopeFolders]
			if json.Unmarshal(w.Body.Bytes(), &out) != nil || !inventory.ValidFolders(out.Data) || len(out.Data.Entries) != 1 {
				t.Fatal("invalid projection")
			}
		}
	}
}
