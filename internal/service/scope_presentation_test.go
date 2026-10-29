package service

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scopeFixture uses the real shared discovery handler with explicit bounded catalogue fixtures.
func scopeFixture(t *testing.T, changed bool, bind inventory.BindRoot) *http.ServeMux {
	t.Helper()
	calls := 0
	discover := func(context.Context, []contracts.Volume) (inventory.Result, error) {
		calls++
		evidence := "stable"
		if changed && calls > 1 {
			evidence = "replacement"
		}
		return inventory.Result{Volumes: []contracts.Volume{{ID: "volume-a", Name: "Volume", MountPath: "/private/volume", State: "reachable", IdentityEvidence: evidence}}, Issues: []inventory.Issue{}}, nil
	}
	shares := func(context.Context, []contracts.Volume, []contracts.Share) (inventory.ShareResult, error) {
		return inventory.ShareResult{Shares: []contracts.Share{{ID: "share-a", Name: "Shared", VolumeID: "volume-a", PhysicalPath: "/private/volume/share", State: "reachable", Readable: true}}, Issues: []inventory.Issue{}}, nil
	}
	mux := http.NewServeMux()
	registerScopePresentationWithBinder(mux, newInventoryHandler(t.TempDir(), discover, shares), bind)
	return mux
}

// Hundreds of legitimate share IDs must fit both the transport and semantic selection limits.
func TestScopePreviewLargeSelection(t *testing.T) {
	shares := []contracts.Share{}
	ids := []string{}
	for i := 0; i < 600; i++ {
		id := fmt.Sprintf("share-%04d-%s", i, strings.Repeat("a", 64))
		ids = append(ids, id)
		shares = append(shares, contracts.Share{ID: id, Name: id, VolumeID: "volume-a", PhysicalPath: fmt.Sprintf("/private/volume/s%d", i), State: "reachable", Readable: true})
	}
	discover := func(context.Context, []contracts.Volume) (inventory.Result, error) {
		return inventory.Result{Volumes: []contracts.Volume{{ID: "volume-a", Name: "Volume", MountPath: "/private/volume", State: "reachable"}}, Issues: []inventory.Issue{}}, nil
	}
	discoverShares := func(context.Context, []contracts.Volume, []contracts.Share) (inventory.ShareResult, error) {
		return inventory.ShareResult{Shares: shares, Issues: []inventory.Issue{}}, nil
	}
	calls := 0
	mux := http.NewServeMux()
	registerScopePresentationWithBinder(mux, newInventoryHandler(t.TempDir(), discover, discoverShares), func(_ context.Context, r inventory.NormalizedRoot) (inventory.RootIdentity, error) {
		calls++
		return inventory.RootIdentity{Path: r.PhysicalPath, Device: "1", Inode: "2", MountID: "3"}, nil
	})
	raw, _ := json.Marshal(inventory.ScopePreviewRequest{ContractVersion: 1, Scope: contracts.Scope{Kind: "shares", TargetIDs: ids}})
	if len(raw) <= 16384 {
		t.Fatal("fixture must exceed former limit")
	}
	r := httptest.NewRequest("POST", "/api/v1/scope-preview", strings.NewReader(string(raw)))
	r.Header.Set("X-DiskMap-Principal", "admin")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || calls != 600 {
		t.Fatal(w.Code, calls)
	}
}

// Public projection tests prove that neither source paths nor private mount evidence escape.
func TestScopePresentationProjection(t *testing.T) {
	mux := scopeFixture(t, false, func(context.Context, inventory.NormalizedRoot) (inventory.RootIdentity, error) {
		t.Fatal("inventory listing must not bind roots")
		return inventory.RootIdentity{}, nil
	})
	r := httptest.NewRequest("GET", "/api/v1/scope-inventory", nil)
	r.Header.Set("X-DiskMap-Principal", "admin")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "/private") || strings.Contains(w.Body.String(), "stable") {
		t.Fatal(w.Code, w.Body.String())
	}
	var out contracts.Envelope[inventory.Presentation]
	if json.Unmarshal(w.Body.Bytes(), &out) != nil || !inventory.ValidPresentation(out.Data) || len(out.Data.Shares) != 1 {
		t.Fatal("invalid catalogue")
	}
}

// Only selected roots are opened; failed root readability stays explicit and does not alter permissions.
func TestScopePreviewReadability(t *testing.T) {
	for _, denied := range []bool{false, true} {
		t.Run(map[bool]string{false: "readable", true: "unreadable"}[denied], func(t *testing.T) {
			calls := 0
			mux := scopeFixture(t, false, func(_ context.Context, r inventory.NormalizedRoot) (inventory.RootIdentity, error) {
				calls++
				if r.TargetID != "share-a" {
					t.Fatal("unexpected root")
				}
				if denied {
					return inventory.RootIdentity{}, fs.ErrPermission
				}
				return inventory.RootIdentity{Path: r.PhysicalPath, Device: "1", Inode: "2", MountID: "3"}, nil
			})
			r := httptest.NewRequest("POST", "/api/v1/scope-preview", strings.NewReader(`{"contractVersion":1,"scope":{"kind":"shares","targetIds":["share-a"]}}`))
			r.Header.Set("X-DiskMap-Principal", "admin")
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			var out contracts.Envelope[inventory.ScopePreview]
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || !inventory.ValidPreview(out.Data) || calls != 1 || len(out.Data.Roots) != 1 {
				t.Fatal(w.Code, w.Body.String(), calls)
			}
			want := "reachable"
			if denied {
				want = "unreadable"
			}
			if out.Data.Roots[0].State != want || strings.Contains(w.Body.String(), "/private") {
				t.Fatal(w.Body.String())
			}
		})
	}
}

// Replacement mounts and malformed identities fail closed rather than becoming new authorizations.
func TestScopePreviewChangedAndInvalid(t *testing.T) {
	bind := func(_ context.Context, r inventory.NormalizedRoot) (inventory.RootIdentity, error) {
		return inventory.RootIdentity{Path: r.PhysicalPath, Device: "1", Inode: "2", MountID: "3"}, nil
	}
	for _, tc := range []struct {
		name, body         string
		changed, principal bool
		status             int
	}{
		{"changed", `{"contractVersion":1,"scope":{"kind":"shares","targetIds":["share-a"]}}`, true, true, 409},
		{"foreign-path", `{"contractVersion":1,"scope":{"kind":"shares","targetIds":["/private/volume"]}}`, false, true, 400},
		{"unknown", `{"contractVersion":1,"scope":{"kind":"shares","targetIds":["unknown"]}}`, false, true, 400},
		{"no-principal", `{"contractVersion":1,"scope":{"kind":"nas","targetIds":[]}}`, false, false, 400},
		{"extra-field", `{"contractVersion":1,"scope":{"kind":"nas","targetIds":[]},"path":"/private"}`, false, true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/v1/scope-preview", strings.NewReader(tc.body))
			if tc.principal {
				r.Header.Set("X-DiskMap-Principal", "admin")
			}
			w := httptest.NewRecorder()
			scopeFixture(t, tc.changed, bind).ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

// Native NAS and volume previews must expose share roots without widening the source authority.
func TestScopePreviewUsesAuthorizedShareRoots(t *testing.T) {
	for _, scope := range []string{`{"kind":"nas","targetIds":[]}`, `{"kind":"volumes","targetIds":["volume-a"]}`} {
		mux := scopeFixture(t, false, func(_ context.Context, r inventory.NormalizedRoot) (inventory.RootIdentity, error) {
			if r.Kind != "share" || r.PhysicalPath != "/private/volume/share" {
				t.Fatal("volume authority expanded")
			}
			return inventory.RootIdentity{Path: r.PhysicalPath, Device: "1", Inode: "2", MountID: "3"}, nil
		})
		r := httptest.NewRequest("POST", "/api/v1/scope-preview", strings.NewReader(`{"contractVersion":1,"scope":`+scope+`}`))
		r.Header.Set("X-DiskMap-Principal", "standard-owner")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("preview rejected native share expansion: %d", w.Code)
		}
	}
}
