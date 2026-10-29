package inventory_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
)

const firstUUID = "7f933e95-696c-4b33-b616-dc57b241cb50"
const secondUUID = "0e25eae0-cdbf-4534-99fa-327d969a79e4"

var measured = time.Date(2026, 10, 9, 20, 0, 0, 0, time.UTC)

// fixture uses the observed mount shapes plus explicitly synthetic negative mounts.
func fixture(t *testing.T) []inventory.Mount {
	t.Helper()
	raw, err := os.ReadFile("mountinfo.txt")
	if err != nil {
		t.Fatal(err)
	}
	mounts, err := inventory.ParseMountInfo(raw)
	if err != nil {
		t.Fatal(err)
	}
	return mounts
}

// probe supplies deterministic metadata without claiming execution on DSM.
func probe(_ context.Context, m inventory.Mount) (inventory.Observation, error) {
	uuid := firstUUID
	if m.Path == "/volume2" {
		uuid = secondUUID
	}
	return inventory.Observation{UUID: uuid, MountID: m.ID, BlockSize: 4096, Blocks: 100, Free: 40, Available: 30}, nil
}

// TestLocalDataVolumesAndCapacity proves filtering and reserved-space accounting.
func TestLocalDataVolumesAndCapacity(t *testing.T) {
	calls := 0
	result, err := inventory.Collect(context.Background(), fixture(t), nil, func(ctx context.Context, m inventory.Mount) (inventory.Observation, error) {
		calls++
		return probe(ctx, m)
	}, measured)
	if err != nil || calls != 2 || len(result.Volumes) != 2 || len(result.Issues) != 0 {
		t.Fatal(result, calls, err)
	}
	for _, v := range result.Volumes {
		if v.State != "reachable" || v.Capacity.Value == nil || *v.Capacity.Value != "409600" || *v.Used.Value != "245760" || *v.Available.Value != "122880" || v.Capacity.MeasuredAt != "2026-10-09T20:00:00Z" {
			t.Fatal(v)
		}
	}
}

// TestMissingMovedAndExpansion keeps filesystem identity separate from mount names and devices.
func TestMissingMovedAndExpansion(t *testing.T) {
	initial, err := inventory.Collect(context.Background(), fixture(t), nil, probe, measured)
	if err != nil {
		t.Fatal(err)
	}
	moved := []inventory.Mount{{ID: "70", Device: "0:99", Root: "/@syno", Path: "/volume3", Filesystem: "btrfs", Source: "/dev/mapper/expansion-data"}}
	result, err := inventory.Collect(context.Background(), moved, initial.Volumes, probe, measured)
	if err != nil || len(result.Volumes) != 2 {
		t.Fatal(result, err)
	}
	for _, v := range result.Volumes {
		if v.ID == "volume-"+firstUUID {
			if v.MountPath != "/volume3" || v.State != "reachable" {
				t.Fatal(v)
			}
		} else if v.State != "unmounted" || v.Capacity.Value != nil || v.Used.Value != nil || v.Available.Value != nil {
			t.Fatal(v)
		}
	}
}

// TestUnverifiedMountsCannotAcquireAnInventedIdentity covers malformed and ambiguous observations.
func TestUnverifiedMountsCannotAcquireAnInventedIdentity(t *testing.T) {
	base := inventory.Mount{ID: "40", Root: "/@syno", Path: "/volume1", Filesystem: "btrfs"}
	cases := []struct {
		name   string
		mounts []inventory.Mount
		probe  inventory.Probe
		code   string
	}{
		{"ioctl denied", []inventory.Mount{base}, func(context.Context, inventory.Mount) (inventory.Observation, error) {
			return inventory.Observation{}, errors.New("denied")
		}, "VOLUME_UNVERIFIED"},
		{"mount changed", []inventory.Mount{base}, func(context.Context, inventory.Mount) (inventory.Observation, error) {
			return inventory.Observation{UUID: firstUUID, MountID: "99"}, nil
		}, "MOUNT_CHANGED"},
		{"UUID absent", []inventory.Mount{base}, func(context.Context, inventory.Mount) (inventory.Observation, error) {
			return inventory.Observation{MountID: "40"}, nil
		}, "IDENTITY_UNVERIFIED"},
		{"stacked mount", []inventory.Mount{base, base}, probe, "AMBIGUOUS_MOUNT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := inventory.Collect(context.Background(), tc.mounts, nil, tc.probe, measured)
			if err != nil || len(out.Volumes) != 0 || len(out.Issues) != 1 || out.Issues[0].Code != tc.code {
				t.Fatal(out, err)
			}
		})
	}
	base.Filesystem = "ext4"
	out, err := inventory.Collect(context.Background(), []inventory.Mount{base}, nil, probe, measured)
	if err != nil || len(out.Volumes) != 0 || out.Issues[0].Code != "UNSUPPORTED_VOLUME_LAYOUT" {
		t.Fatal(out, err)
	}
}

// TestDuplicateFilesystemAndLosslessNumbers avoids double counting and floating-point rounding.
func TestDuplicateFilesystemAndLosslessNumbers(t *testing.T) {
	mounts := fixture(t)
	duplicated, err := inventory.Collect(context.Background(), mounts, nil, func(_ context.Context, m inventory.Mount) (inventory.Observation, error) {
		return inventory.Observation{UUID: firstUUID, MountID: m.ID, BlockSize: 4096, Blocks: 100, Free: 40, Available: 30}, nil
	}, measured)
	if err != nil || len(duplicated.Volumes) != 0 || len(duplicated.Issues) != 2 {
		t.Fatal(duplicated, err)
	}
	out, err := inventory.Collect(context.Background(), mounts, nil, func(_ context.Context, m inventory.Mount) (inventory.Observation, error) {
		v, _ := probe(context.Background(), m)
		v.BlockSize = 4096
		v.Blocks = 2199023255553
		v.Free = 0
		v.Available = 0
		return v, nil
	}, measured)
	if err != nil || string(*out.Volumes[0].Capacity.Value) != "9007199254745088" {
		t.Fatal(out, err)
	}
	out, err = inventory.Collect(context.Background(), mounts, nil, func(_ context.Context, m inventory.Mount) (inventory.Observation, error) {
		v, _ := probe(context.Background(), m)
		v.Available = 101
		return v, nil
	}, measured)
	if err != nil || out.Volumes[0].Capacity.Value != nil || len(out.Issues) != 2 {
		t.Fatal(out, err)
	}
}

// TestMountEscapesAndCancellation rejects malformed kernel records without partial success.
func TestMountEscapesAndCancellation(t *testing.T) {
	mounts, err := inventory.ParseMountInfo([]byte("2 1 0:1 /x\\040y /space\\040name rw shared:1 - btrfs /dev/a\\134b rw\n"))
	if err != nil || mounts[0].Root != "/x y" || mounts[0].Source != "/dev/a\\b" {
		t.Fatal(mounts, err)
	}
	for _, raw := range []string{"bad", `2 1 0:1 / /bad\099 rw - btrfs /dev/a rw`} {
		if _, err = inventory.ParseMountInfo([]byte(raw)); err == nil {
			t.Fatal(raw)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = inventory.Collect(ctx, fixture(t), []contracts.Volume{}, probe, measured); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
