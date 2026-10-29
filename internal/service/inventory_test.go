package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
)

// inventoryResponse invokes the actual handler without requiring a Unix socket fixture.
func inventoryResponse(handler http.Handler) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/inventory", nil))
	return response
}

// TestInventorySerializedCacheAndPersistentPrior proves both stages share one refresh and restart state.
func TestInventorySerializedCacheAndPersistentPrior(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state")
	var volumesCalled, sharesCalled atomic.Int32
	volume := contracts.Volume{ID: "volume-one", Name: "volume1", MountPath: "/volume1", State: "reachable"}
	share := contracts.Share{ID: "share-one", Readable: true, VolumeID: volume.ID, Name: "example", State: "reachable"}
	handler := newInventoryHandler(state, func(ctx context.Context, previous []contracts.Volume) (inventory.Result, error) {
		volumesCalled.Add(1)
		if len(previous) != 0 {
			t.Error("unexpected prior volumes")
		}
		return inventory.Result{Volumes: []contracts.Volume{volume}, Issues: []inventory.Issue{{Code: "VOLUME_NOTE"}}}, nil
	}, func(ctx context.Context, volumes []contracts.Volume, previous []contracts.Share) (inventory.ShareResult, error) {
		sharesCalled.Add(1)
		if len(volumes) != 1 || volumes[0].ID != volume.ID || len(previous) != 0 {
			t.Error("incoherent discovery inputs")
		}
		return inventory.ShareResult{Shares: []contracts.Share{share}, Issues: []inventory.Issue{{Code: "SHARE_NOTE"}}}, nil
	})
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := inventoryResponse(handler)
			var envelope contracts.Envelope[inventorySnapshot]
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || len(envelope.Data.Volumes) != 1 || len(envelope.Data.Shares) != 1 || len(envelope.Data.Issues) != 2 {
				t.Error(response.Code, response.Body.String())
			}
		}()
	}
	wg.Wait()
	if volumesCalled.Load() != 1 || sharesCalled.Load() != 1 {
		t.Fatal("duplicate refresh", volumesCalled.Load(), sharesCalled.Load())
	}
	for path, mode := range map[string]os.FileMode{state: 0700, filepath.Join(state, "inventory.json"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatal(path, info, err)
		}
	}
	previous, err := loadInventory(state)
	if err != nil || len(previous.Issues) != 2 {
		t.Fatal(previous, err)
	}
	restarted := newInventoryHandler(state, func(ctx context.Context, volumes []contracts.Volume) (inventory.Result, error) {
		if len(volumes) != 1 || volumes[0].ID != volume.ID {
			t.Fatal(volumes)
		}
		volumes[0].State = "unmounted"
		return inventory.Result{Volumes: volumes}, nil
	}, func(ctx context.Context, volumes []contracts.Volume, shares []contracts.Share) (inventory.ShareResult, error) {
		if len(shares) != 1 || shares[0].ID != share.ID || volumes[0].State != "unmounted" {
			t.Fatal(volumes, shares)
		}
		shares[0].State = "missing"
		return inventory.ShareResult{Shares: shares}, nil
	})
	response := inventoryResponse(restarted)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "missing") || strings.Contains(response.Body.String(), "unmounted") {
		t.Fatal(response.Code, response.Body.String())
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 1 || entries[0].Name() != "inventory.json" {
		t.Fatal(entries, err)
	}
}

// TestInventoryFailuresNeverPublish proves second-stage failure cannot replace an existing snapshot.
func TestInventoryFailuresNeverPublish(t *testing.T) {
	prior := inventorySnapshot{Volumes: []contracts.Volume{}, Shares: []contracts.Share{}, Issues: []inventory.Issue{{Code: "PRESERVE"}}}
	for _, kind := range []string{"volume-error", "share-error", "shares-cap", "issues-cap", "nil-shares", "oversized", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			state := t.TempDir()
			if err := saveInventory(state, prior); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(state, "inventory.json"))
			var shareCalls int
			handler := newInventoryHandler(state, func(context.Context, []contracts.Volume) (inventory.Result, error) {
				if kind == "volume-error" {
					return inventory.Result{}, errors.New("/private/source")
				}
				return inventory.Result{Volumes: []contracts.Volume{}}, nil
			}, func(context.Context, []contracts.Volume, []contracts.Share) (inventory.ShareResult, error) {
				shareCalls++
				out := inventory.ShareResult{Shares: []contracts.Share{}}
				switch kind {
				case "share-error":
					return out, errors.New("/private/source")
				case "shares-cap":
					out.Shares = make([]contracts.Share, 1025)
				case "issues-cap":
					out.Issues = make([]inventory.Issue, 1025)
				case "nil-shares":
					out.Shares = nil
				case "oversized":
					out.Shares = []contracts.Share{{Name: strings.Repeat("x", inventoryLimit)}}
				}
				return out, nil
			})
			response := httptest.NewRecorder()
			request := httptest.NewRequest("GET", "/api/v1/inventory", nil)
			if kind == "cancelled" {
				ctx, cancel := context.WithCancel(request.Context())
				cancel()
				request = request.WithContext(ctx)
			}
			handler.ServeHTTP(response, request)
			after, _ := os.ReadFile(filepath.Join(state, "inventory.json"))
			if response.Code != 503 || string(after) != string(before) || strings.Contains(response.Body.String(), "/private") {
				t.Fatal(response.Code, response.Body.String())
			}
			if (kind == "volume-error" || kind == "cancelled") && shareCalls != 0 {
				t.Fatal("shares probed after failure")
			}
		})
	}
}

// TestInventoryStateStrict rejects legacy, corrupt and incomplete state rather than forgetting identities.
func TestInventoryStateStrict(t *testing.T) {
	for _, raw := range []string{"[]", "null", "[invalid]", `{"stateVersion":2,"volumes":[],"shares":[],"issues":[]}`, `{"stateVersion":1,"volumes":[]}`, `{"stateVersion":1,"volumes":[],"shares":[],"issues":[]} {}`} {
		state := t.TempDir()
		if err := os.WriteFile(filepath.Join(state, "inventory.json"), []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		called := false
		handler := newInventoryHandler(state, func(context.Context, []contracts.Volume) (inventory.Result, error) {
			called = true
			return inventory.Result{}, nil
		}, nil)
		response := inventoryResponse(handler)
		if response.Code != 503 || called {
			t.Fatal(raw, response.Code, called)
		}
	}
}

// TestInventoryExactShareCap proves the inclusive bound and exact persisted response content.
func TestInventoryExactShareCap(t *testing.T) {
	state := t.TempDir()
	handler := newInventoryHandler(state, func(context.Context, []contracts.Volume) (inventory.Result, error) {
		return inventory.Result{Volumes: []contracts.Volume{}}, nil
	}, func(context.Context, []contracts.Volume, []contracts.Share) (inventory.ShareResult, error) {
		return inventory.ShareResult{Shares: make([]contracts.Share, 1024)}, nil
	})
	response := inventoryResponse(handler)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var envelope contracts.Envelope[inventorySnapshot]
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	persisted, err := loadInventory(state)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(envelope.Data)
	b, _ := json.Marshal(persisted)
	if string(a) != string(b) || len(persisted.Shares) != 1024 {
		t.Fatal("response and persistence diverged")
	}
}
