package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/scanner"
)

// TestNativeScopePermissionBoundary reads only the fixed operator-provisioned fixture.
// Catalogue identities are explicit fixture inputs; descriptor checks and traversal are production code.
func TestNativeScopePermissionBoundary(t *testing.T) {
	if os.Getenv("DISKMAP_NATIVE_SCOPE_PERMISSIONS") != "1" {
		t.Skip("explicit isolated native permission probe required")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 167438 {
		t.Fatal("requires qualified native package UID")
	}
	// The operator supplies an isolated fixture root for this native-only probe.
	base := os.Getenv("DISKMAP_NATIVE_SCOPE_FIXTURE_ROOT")
	if base == "" {
		t.Fatal("DISKMAP_NATIVE_SCOPE_FIXTURE_ROOT is required for the native probe")
	}
	for _, p := range []string{base + "/readable", base + "/readable/denied-child", base + "/denied-root"} {
		info, e := os.Lstat(p)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatal("fixture directory missing or unsafe")
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 {
			t.Fatal("fixture must be root owned")
		}
		if strings.Contains(p, "denied-") && info.Mode().Perm() != 0 {
			t.Fatal("denial fixture must have mode 000")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	volumes := []contracts.Volume{{ID: "native-volume", Name: "Fixture", MountPath: base, State: "reachable", IdentityEvidence: "operator-fixture"}}
	shares := []contracts.Share{
		{ID: "native-readable", Name: "Readable", VolumeID: "native-volume", PhysicalPath: base + "/readable", State: "reachable", Readable: true},
		{ID: "native-denied", Name: "Denied", VolumeID: "native-volume", PhysicalPath: base + "/denied-root", State: "reachable", Readable: true},
	}
	// The public handler receives only this isolated catalogue and uses its real bindPreview implementation.
	catalogue := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scopeReply(w, inventorySnapshot{Volumes: volumes, Shares: shares, Issues: []inventory.Issue{}})
	})
	mux := http.NewServeMux()
	registerScopePresentation(mux, catalogue)
	for _, tc := range []struct{ id, state string }{{"native-readable", "reachable"}, {"native-denied", "unreadable"}} {
		body, _ := json.Marshal(inventory.ScopePreviewRequest{ContractVersion: 1, Scope: contracts.Scope{Kind: "shares", TargetIDs: []string{tc.id}}})
		r := httptest.NewRequest(http.MethodPost, "/api/v1/scope-preview", strings.NewReader(string(body))).WithContext(ctx)
		r.Header.Set("X-DiskMap-Principal", "native-probe")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var reply contracts.Envelope[inventory.ScopePreview]
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &reply) != nil || !inventory.ValidPreview(reply.Data) || len(reply.Data.Roots) != 1 || reply.Data.Roots[0].State != tc.state || strings.Contains(w.Body.String(), base) {
			t.Fatal("unexpected or leaking root preflight")
		}
		if tc.state == "unreadable" && (len(reply.Data.Issues) != 1 || reply.Data.Issues[0].Code != "SCOPE_BINDING_UNREADABLE") {
			t.Fatal("missing informative denied-root issue")
		}
		t.Logf("root=%s preflight=%s", tc.id, reply.Data.Roots[0].State)
	}
	// A positive root precheck cannot attest readability of subsequently encountered descendants.
	scope := contracts.Scope{Kind: "shares", TargetIDs: []string{"native-readable"}}
	normalized, e := inventory.Normalize(scope, volumes, shares)
	if e != nil {
		t.Fatal(e)
	}
	manifest, e := inventory.FreezeScope(ctx, normalized, bindPreview)
	if e != nil || len(manifest.Roots) != 1 || manifest.Roots[0].Binding == nil || manifest.Roots[0].State != "reachable" {
		t.Fatal("positive root binding required")
	}
	db, e := index.Open(":memory:")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	profile := contracts.Profile{ID: "native-permissions", Revision: 1, Scope: scope, ResourceMode: "balanced", Limits: contracts.ResourceLimits{Workers: 1}}
	result, e := scanner.ScanManifest(ctx, db, "native-permissions", profile, manifest, index.DefaultFileTypeMapping())
	if e != nil || result.Errors != 1 || result.Bytes != "3" || result.Entries != 3 {
		t.Fatalf("unexpected scan counters entries=%d bytes=%s errors=%d scanError=%t", result.Entries, result.Bytes, result.Errors, e != nil)
	}
	var state string
	if e = db.QueryRowContext(ctx, "SELECT state FROM coverage WHERE target_id=?", "native-readable").Scan(&state); e != nil || state != "partial" {
		t.Fatal("positive preflight incorrectly implied complete coverage")
	}
	var count int
	if e = db.QueryRowContext(ctx, "SELECT count(*) FROM scan_boundaries WHERE target_id=? AND path_bytes=? AND reason_code=?", "native-readable", []byte("denied-child"), "SOURCE_UNREADABLE").Scan(&count); e != nil || count != 1 {
		t.Fatal("child permission failure not preserved")
	}
	t.Logf("scan coverage=%s entries=%d logicalBytes=%s errors=%d boundary=SOURCE_UNREADABLE", state, result.Entries, result.Bytes, result.Errors)
}
