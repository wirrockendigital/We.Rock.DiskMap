package inventory_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
)

// normalizationFixture represents authoritative identities; source access is injected separately.
func normalizationFixture() ([]contracts.Volume, []contracts.Share) {
	return []contracts.Volume{{ID: "volume-a", MountPath: "/volume1", State: "reachable"}, {ID: "volume-b", MountPath: "/volume2", State: "reachable"}}, []contracts.Share{
		{ID: "share-a", VolumeID: "volume-a", PhysicalPath: "/volume1/Alpha", State: "reachable", Readable: true},
		{ID: "share-b", VolumeID: "volume-a", PhysicalPath: "/volume1/Beta", State: "reachable", Readable: true},
		{ID: "share-c", VolumeID: "volume-b", PhysicalPath: "/volume2/Gamma", State: "reachable", Readable: true},
	}
}

// TestNormalizeDisjointAccess proves bind requests stay within the selected normalized roots.
func TestNormalizeDisjointAccess(t *testing.T) {
	cases := []struct {
		name, kind    string
		ids, expected []string
	}{
		{"single-share", "shares", []string{"share-a"}, []string{"share-a"}},
		{"volume-and-own-share", "mixed", []string{"share-a", "volume-a", "share-a"}, []string{"share-a", "share-b"}},
		{"two-volumes", "volumes", []string{"volume-b", "volume-a"}, []string{"share-a", "share-b", "share-c"}},
		{"volume-and-foreign-share", "mixed", []string{"share-c", "volume-a"}, []string{"share-a", "share-b", "share-c"}},
		{"nas", "nas", nil, []string{"share-a", "share-b", "share-c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, s := normalizationFixture()
			n, err := inventory.Normalize(contracts.Scope{Kind: tc.kind, TargetIDs: tc.ids}, v, s)
			if err != nil {
				t.Fatal(err)
			}
			calls := []string{}
			m, err := inventory.FreezeScope(context.Background(), n, func(_ context.Context, r inventory.NormalizedRoot) (inventory.RootIdentity, error) {
				calls = append(calls, r.TargetID)
				return inventory.RootIdentity{Path: r.PhysicalPath, Device: "1", Inode: "2", MountID: "3"}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, tc.expected) {
				t.Fatalf("unexpected access: %v", calls)
			}
			if len(m.Roots) != len(tc.expected) {
				t.Fatal("duplicate weighting roots")
			}
			for _, r := range m.Roots {
				if r.Binding == nil || r.VolumeID == "" || r.Kind == "share" && r.ShareID == nil || r.Kind == "volume" && r.ShareID != nil {
					t.Fatalf("lost root identity: %+v", r)
				}
			}
		})
	}
}

// TestNormalizeDeterministic verifies stable order independently of selection and inventory order.
func TestNormalizeDeterministic(t *testing.T) {
	v, s := normalizationFixture()
	scope := contracts.Scope{Kind: "mixed", TargetIDs: []string{"share-c", "share-a", "volume-a"}}
	a, err := inventory.Normalize(scope, v, s)
	if err != nil {
		t.Fatal(err)
	}
	v[0], v[1] = v[1], v[0]
	s[0], s[2] = s[2], s[0]
	scope.TargetIDs = []string{"volume-a", "share-a", "share-c", "share-c"}
	b, err := inventory.Normalize(scope, v, s)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("nondeterministic normalization: %+v / %+v", a, b)
	}
	if !reflect.DeepEqual(a.CoveredTargetIDs, []string{"share-a", "share-b", "share-c", "volume-a"}) {
		t.Fatal(a.CoveredTargetIDs)
	}
}

// TestNormalizeUnavailable preserves locked and disappeared targets even beneath a selected volume.
func TestNormalizeUnavailable(t *testing.T) {
	v, s := normalizationFixture()
	locked := true
	s[0].State = "locked"
	s[0].Readable = false
	s[0].Locked = &locked
	s[1].State = "missing"
	s[1].Readable = false
	n, err := inventory.Normalize(contracts.Scope{Kind: "mixed", TargetIDs: []string{"volume-a", "share-a", "share-b"}}, v, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(n.Roots) != 2 || len(n.Issues) != 2 || n.Issues[0].Code != "SCOPE_TARGET_LOCKED" || n.Issues[1].Code != "SCOPE_TARGET_MISSING" {
		t.Fatalf("lost unavailable coverage %+v", n)
	}
	n, err = inventory.Normalize(contracts.Scope{Kind: "shares", TargetIDs: []string{"share-a", "share-b"}}, v, s)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	m, err := inventory.FreezeScope(context.Background(), n, func(context.Context, inventory.NormalizedRoot) (inventory.RootIdentity, error) {
		calls++
		return inventory.RootIdentity{}, nil
	})
	if err != nil || calls != 0 || len(m.Roots) != 2 || len(m.Issues) != 2 {
		t.Fatalf("unavailable source accessed: %+v %v %d", m, err, calls)
	}
}

// TestNormalizeRejectsAmbiguity rejects unknown identities and paths inconsistent with native volume membership.
func TestNormalizeRejectsAmbiguity(t *testing.T) {
	for _, scenario := range []string{"unknown", "wrong-kind", "wrong-volume", "prefix-confusion", "overlap", "duplicate-id", "empty", "nas-explicit", "reachable-unreadable", "locked-reachable"} {
		t.Run(scenario, func(t *testing.T) {
			v, s := normalizationFixture()
			scope := contracts.Scope{Kind: "shares", TargetIDs: []string{"share-a", "share-b"}}
			switch scenario {
			case "unknown":
				scope.TargetIDs = []string{"missing-id"}
			case "wrong-kind":
				scope.TargetIDs = []string{"volume-a"}
			case "wrong-volume":
				s[0].VolumeID = "volume-b"
			case "prefix-confusion":
				s[0].PhysicalPath = "/volume11/Alpha"
			case "overlap":
				s[1].PhysicalPath = "/volume1/Alpha/nested"
			case "duplicate-id":
				s[1].ID = s[0].ID
			case "empty":
				scope.TargetIDs = nil
			case "nas-explicit":
				scope.Kind = "nas"
			case "reachable-unreadable":
				s[0].Readable = false
			case "locked-reachable":
				locked := true
				s[0].Locked = &locked
			}
			if _, err := inventory.Normalize(scope, v, s); err == nil {
				t.Fatal("unsafe scope accepted")
			}
		})
	}
}

// TestFreezeDetached proves post-capture inventory, selection, and pointer mutations cannot alter the manifest.
func TestFreezeDetached(t *testing.T) {
	v, s := normalizationFixture()
	n, err := inventory.Normalize(contracts.Scope{Kind: "shares", TargetIDs: []string{"share-a"}}, v, s)
	if err != nil {
		t.Fatal(err)
	}
	m, err := inventory.FreezeScope(context.Background(), n, func(_ context.Context, r inventory.NormalizedRoot) (inventory.RootIdentity, error) {
		*r.ShareID = "binder-mutated"
		return inventory.RootIdentity{Path: r.PhysicalPath, Device: "1", Inode: "2", MountID: "3"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	s[0].PhysicalPath = "/elsewhere"
	n.Roots[0].PhysicalPath = "/mutated"
	*n.Roots[0].ShareID = "mutated"
	n.CoveredTargetIDs[0] = "mutated"
	if m.Roots[0].PhysicalPath != "/volume1/Alpha" || *m.Roots[0].ShareID != "share-a" || m.CoveredTargetIDs[0] != "share-a" || m.Roots[0].Binding.Path != "/volume1/Alpha" {
		t.Fatalf("aliased manifest %+v", m)
	}
}

// TestFreezeFailures rejects incomplete identity and cancellation without claiming successful coverage.
func TestFreezeFailures(t *testing.T) {
	v, s := normalizationFixture()
	n, err := inventory.Normalize(contracts.Scope{Kind: "shares", TargetIDs: []string{"share-a"}}, v, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"error", "missing-mount", "wrong-path"} {
		t.Run(failure, func(t *testing.T) {
			m, err := inventory.FreezeScope(context.Background(), n, func(_ context.Context, r inventory.NormalizedRoot) (inventory.RootIdentity, error) {
				id := inventory.RootIdentity{Path: r.PhysicalPath, Device: "1", Inode: "2", MountID: "3"}
				switch failure {
				case "error":
					return id, errors.New("sensitive local path")
				case "missing-mount":
					id.MountID = ""
				case "wrong-path":
					id.Path = "/outside"
				}
				return id, nil
			})
			if err != nil || m.Roots[0].Binding != nil || m.Roots[0].State != "unverified" || len(m.Issues) != 1 || m.Issues[0].Code != "SCOPE_BINDING_UNAVAILABLE" {
				t.Fatalf("false coverage %+v %v", m, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	if _, err := inventory.FreezeScope(ctx, n, func(context.Context, inventory.NormalizedRoot) (inventory.RootIdentity, error) {
		calls++
		return inventory.RootIdentity{}, nil
	}); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("canceled capture touched source")
	}
}

// TestNASRetainsUnmappedIssue keeps unknown volume associations visible without guessing a root.
func TestNASRetainsUnmappedIssue(t *testing.T) {
	v, s := normalizationFixture()
	s[0].VolumeID = ""
	s[0].State = "unverified"
	s[0].Readable = false
	n, err := inventory.Normalize(contracts.Scope{Kind: "nas"}, v, s)
	if err != nil || len(n.Issues) != 1 || n.Issues[0].TargetID != "share-a" {
		t.Fatalf("lost unmapped NAS issue %+v %v", n, err)
	}
}

// TestFreezeRejectsMalformedRoot prevents a modified object from requesting overlapping root access.
func TestFreezeRejectsMalformedRoot(t *testing.T) {
	v, s := normalizationFixture()
	n, err := inventory.Normalize(contracts.Scope{Kind: "mixed", TargetIDs: []string{"volume-a", "share-c"}}, v, s)
	if err != nil {
		t.Fatal(err)
	}
	n.Roots[0].PhysicalPath = "/volume1/Beta/nested"
	calls := 0
	_, err = inventory.FreezeScope(context.Background(), n, func(context.Context, inventory.NormalizedRoot) (inventory.RootIdentity, error) {
		calls++
		return inventory.RootIdentity{}, nil
	})
	if err == nil || calls != 0 {
		t.Fatal("modified overlapping scope was accessed")
	}
}

// TestReusedMountPathRetainsOldUnavailableIdentity permits only one traversable root at a reused path.
func TestReusedMountPathRetainsOldUnavailableIdentity(t *testing.T) {
	volumes := []contracts.Volume{{ID: "old-volume", MountPath: "/volume1", State: "unmounted"}, {ID: "new-volume", MountPath: "/volume1", State: "reachable"}}
	// Native NAS traversal opens only current registered shares, never a reused bare volume root.
	shares := []contracts.Share{{ID: "new-share", VolumeID: "new-volume", PhysicalPath: "/volume1/Current", State: "reachable", Readable: true}}
	n, err := inventory.Normalize(contracts.Scope{Kind: "nas"}, volumes, shares)
	if err != nil || len(n.Roots) != 1 || len(n.Issues) != 1 || n.Issues[0].TargetID != "old-volume" {
		t.Fatalf("old medium erased %+v %v", n, err)
	}
	calls := []string{}
	m, err := inventory.FreezeScope(context.Background(), n, func(_ context.Context, r inventory.NormalizedRoot) (inventory.RootIdentity, error) {
		calls = append(calls, r.TargetID)
		return inventory.RootIdentity{Path: r.PhysicalPath, Device: "1", Inode: "2", MountID: "3"}, nil
	})
	if err != nil || len(calls) != 1 || calls[0] != "new-share" || len(m.Issues) != 1 {
		t.Fatalf("reused path traversed twice %+v %v", calls, err)
	}
}

// TestFreezePreservesObservedBindingFailures retains known causes without changing the selection.
func TestFreezePreservesObservedBindingFailures(t *testing.T) {
	v, s := normalizationFixture()
	n, err := inventory.Normalize(contracts.Scope{Kind: "shares", TargetIDs: []string{"share-a"}}, v, s)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		err         error
		state, code string
	}{{os.ErrNotExist, "missing", "SCOPE_BINDING_MISSING"}, {os.ErrPermission, "unreadable", "SCOPE_BINDING_UNREADABLE"}, {inventory.ErrRootChanged, "unverified", "SCOPE_BINDING_CHANGED"}, {errors.New("private diagnostic"), "unverified", "SCOPE_BINDING_UNAVAILABLE"}} {
		m, e := inventory.FreezeScope(context.Background(), n, func(context.Context, inventory.NormalizedRoot) (inventory.RootIdentity, error) {
			return inventory.RootIdentity{}, &os.PathError{Op: "open", Path: "private", Err: tc.err}
		})
		if e != nil || m.Roots[0].State != tc.state || len(m.Issues) != 1 || m.Issues[0].Code != tc.code || n.Roots[0].State != "reachable" {
			t.Fatal(m, e)
		}
	}
}
