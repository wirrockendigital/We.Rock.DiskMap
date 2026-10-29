package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"testing"
)

// shareFixture uses synthetic UUIDs to test normalization independently from native DSM evidence.
func shareFixture() ([]RegisteredShare, []contracts.Volume, ShareProbe) {
	plain := 0
	r := []RegisteredShare{{UUID: "11111111-1111-1111-1111-111111111111", Name: "Freigabe ä%#", VolumeMountPath: "/volume1", PhysicalPath: "/volume1/Freigabe ä%#", FileStationPath: "/Freigabe ä%#", Encryption: &plain, IdentityEvidence: "synthetic DSM UUID"}}
	v := []contracts.Volume{{ID: "volume-a", MountPath: "/volume1", State: "reachable"}, {ID: "volume-b", MountPath: "/volume2", State: "reachable"}}
	probe := func(_ context.Context, r RegisteredShare) (ShareObservation, error) {
		id := "volume-a"
		if r.VolumeMountPath == "/volume2" {
			id = "volume-b"
		}
		return ShareObservation{VolumeID: id, Exists: true, Readable: true}, nil
	}
	return r, v, probe
}

// TestShareIdentityMoveRoundtrip preserves a registered identity through a verified volume move.
func TestShareIdentityMoveRoundtrip(t *testing.T) {
	r, v, probe := shareFixture()
	first, err := CollectShares(context.Background(), r, v, nil, probe)
	if err != nil {
		t.Fatal(err)
	}
	s := first.Shares[0]
	if s.State != "reachable" || s.VolumeID != "volume-a" {
		t.Fatal(s)
	}
	physical := s.PhysicalPath + "/日本/100% # 'quote\".txt"
	fs, err := MapSharePath(s, physical, true)
	if err != nil {
		t.Fatal(err)
	}
	back, err := MapSharePath(s, fs, false)
	if err != nil || back != physical {
		t.Fatal(back, err)
	}
	if _, err = MapSharePath(s, s.PhysicalPath+"-other/file", true); err == nil {
		t.Fatal("prefix collision accepted")
	}
	r[0].VolumeMountPath = "/volume2"
	r[0].PhysicalPath = "/volume2/Freigabe ä%#"
	moved, err := CollectShares(context.Background(), r, v, first.Shares, probe)
	if err != nil || len(moved.Shares) != 1 || moved.Shares[0].ID != s.ID || moved.Shares[0].VolumeID != "volume-b" {
		t.Fatal(moved, err)
	}
}

// TestShareStates never turns locked, missing, unreadable or unknown into a readable empty share.
func TestShareStates(t *testing.T) {
	r, v, probe := shareFixture()
	plain, err := CollectShares(context.Background(), r, v, nil, probe)
	if err != nil {
		t.Fatal(err)
	}
	locked := 1
	r[0].Encryption = &locked
	result, err := CollectShares(context.Background(), r, v, nil, func(context.Context, RegisteredShare) (ShareObservation, error) {
		t.Fatal("locked share probed")
		return ShareObservation{}, nil
	})
	if err != nil || result.Shares[0].State != "locked" || result.Shares[0].Readable || result.Shares[0].FileStationPath != nil {
		t.Fatal(result, err)
	}
	absent, err := CollectShares(context.Background(), nil, v, plain.Shares, probe)
	if err != nil || absent.Shares[0].State != "missing" || absent.Shares[0].Locked != nil {
		t.Fatal(absent, err)
	}
	mounted := 2
	r[0].Encryption = &mounted
	for _, state := range []string{"missing", "unreadable", "unverified"} {
		p := func(context.Context, RegisteredShare) (ShareObservation, error) {
			if state == "unverified" {
				return ShareObservation{}, errors.New("denied")
			}
			return ShareObservation{VolumeID: "volume-a", Exists: state != "missing", Readable: false}, nil
		}
		result, err = CollectShares(context.Background(), r, v, nil, p)
		if err != nil || result.Shares[0].State != state || result.Shares[0].Readable {
			t.Fatal(state, result, err)
		}
	}
	r[0].Encryption = nil
	result, err = CollectShares(context.Background(), r, v, nil, probe)
	if err != nil || result.Shares[0].State != "unverified" || result.Shares[0].Encrypted != nil {
		t.Fatal(result, err)
	}
	raw, err := json.Marshal(result.Shares[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || fields["encrypted"] != nil || fields["locked"] != nil {
		t.Fatal(string(raw))
	}
}

// TestShareAmbiguity rejects duplicate identities and disables indistinguishable launcher names.
func TestShareAmbiguity(t *testing.T) {
	r, v, probe := shareFixture()
	r = append(r, r[0])
	if _, err := CollectShares(context.Background(), r, v, nil, probe); err == nil {
		t.Fatal("duplicate UUID accepted")
	}
	r[1].UUID = "22222222-2222-2222-2222-222222222222"
	r[1].VolumeMountPath = "/volume2"
	r[1].PhysicalPath = "/volume2/Freigabe ä%#"
	result, err := CollectShares(context.Background(), r, v, nil, probe)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range result.Shares {
		if s.State != "unverified" || s.FileStationPath != nil {
			t.Fatal(s)
		}
	}
	r[0].UUID = ""
	if _, err = CollectShares(context.Background(), r, v, nil, probe); err == nil {
		t.Fatal("name-derived identity accepted")
	}
}

// A fresh root-only observation discovers granted access and removes revoked access
// without scanning descendants or relying on historical catalogue permissions.
func TestShareAccessRefreshWithoutScan(t *testing.T) {
	r, v, _ := shareFixture()
	r[0].Name = "photo"
	r[0].PhysicalPath = "/volume1/photo"
	r[0].FileStationPath = "/photo"
	var previous []contracts.Share
	calls := 0
	for _, readable := range []bool{false, true, false} {
		result, err := CollectShares(context.Background(), r, v, previous, func(_ context.Context, root RegisteredShare) (ShareObservation, error) {
			calls++
			if root.PhysicalPath != "/volume1/photo" {
				t.Fatal("unexpected descendant probe")
			}
			return ShareObservation{VolumeID: "volume-a", Exists: true, Readable: readable}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		visible := readableShares(result.Shares)
		if (len(visible) == 1) != readable {
			t.Fatalf("readable=%v, visible=%v", readable, visible)
		}
		if readable && visible[0].Name != "photo" {
			t.Fatal(visible)
		}
		previous = result.Shares
	}
	if calls != 3 {
		t.Fatalf("expected one root probe per refresh, got %d", calls)
	}
	// Unqualified or unreachable roots never become visible through a stale readable flag.
	for _, state := range []string{"missing", "locked", "unverified", "unreadable"} {
		if len(readableShares([]contracts.Share{{State: state, Readable: true}})) != 0 {
			t.Fatal(state)
		}
	}
}
