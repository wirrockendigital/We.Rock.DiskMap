package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
)

const inventoryLimit = 1 << 20

// inventoryDiscover keeps kernel discovery inside the daemon and permits focused tests.
type inventoryDiscover func(context.Context, []contracts.Volume) (inventory.Result, error)
type inventoryShareDiscover func(context.Context, []contracts.Volume, []contracts.Share) (inventory.ShareResult, error)

// inventorySnapshot publishes both discovery stages as one coherent bounded observation.
type inventorySnapshot struct {
	Volumes []contracts.Volume `json:"volumes"`
	Shares  []contracts.Share  `json:"shares"`
	Issues  []inventory.Issue  `json:"issues"`
}

// inventoryState is the private versioned cache format, never a legacy volume-only fallback.
type inventoryState struct {
	StateVersion int `json:"stateVersion"`
	inventorySnapshot
}

// validInventory prevents malformed empty successes and excessive retained identities.
func validInventory(s inventorySnapshot) bool {
	return s.Volumes != nil && s.Shares != nil && s.Issues != nil && len(s.Volumes) <= 512 && len(s.Shares) <= 1024 && len(s.Issues) <= 1024
}

// registerInventory must be mounted inside the existing kernel-peer-protected local API.
func registerInventory(mux *http.ServeMux, statePath string) {
	shared := newInventoryHandler(statePath, inventory.Discover, inventory.DiscoverShares)
	mux.Handle("GET /api/v1/inventory", shared)
	// Public projections reuse the same serialized discovery owner and private cache.
	registerScopePresentation(mux, shared)
}

// newInventoryHandler serializes refreshes and shares a bounded two-second successful cache.
func newInventoryHandler(statePath string, discover inventoryDiscover, discoverShares inventoryShareDiscover) http.Handler {
	lock := make(chan struct{}, 1)
	lock <- struct{}{}
	var cached []byte
	var refreshed time.Time
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		select {
		case <-ctx.Done():
			inventoryFailure(w)
			return
		case <-lock:
		}
		defer func() { lock <- struct{}{} }()
		if ctx.Err() != nil {
			inventoryFailure(w)
			return
		}
		force, _ := r.Context().Value(scopeFreshKey{}).(bool)
		if cached == nil || force || time.Since(refreshed) >= 2*time.Second {
			previous, err := loadInventory(statePath)
			if err != nil {
				inventoryFailure(w)
				return
			}
			result, err := discover(ctx, previous.Volumes)
			if err != nil || ctx.Err() != nil || len(result.Volumes) > 512 || len(result.Issues) > 1024 {
				inventoryFailure(w)
				return
			}
			shares, err := discoverShares(ctx, result.Volumes, previous.Shares)
			combined := inventorySnapshot{Volumes: inventory.VisibleVolumes(result.Volumes, shares.Shares), Shares: shares.Shares, Issues: append(append([]inventory.Issue{}, result.Issues...), shares.Issues...)}
			if err != nil || ctx.Err() != nil || !validInventory(combined) {
				inventoryFailure(w)
				return
			}
			encoded, err := json.Marshal(contracts.Envelope[inventorySnapshot]{ContractVersion: contracts.Version, RequestID: "inventory", Data: combined})
			if err != nil || len(encoded) >= inventoryLimit {
				inventoryFailure(w)
				return
			}
			if err = saveInventory(statePath, combined); err != nil {
				inventoryFailure(w)
				return
			}
			cached = append(encoded, '\n')
			refreshed = time.Now()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(cached)
	})
}

// loadInventory fails closed on corrupt or oversized prior state instead of forgetting missing volumes.
func loadInventory(statePath string) (inventorySnapshot, error) {
	empty := inventorySnapshot{Volumes: []contracts.Volume{}, Shares: []contracts.Share{}, Issues: []inventory.Issue{}}
	file, err := os.Open(filepath.Join(statePath, "inventory.json"))
	if errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	if err != nil {
		return inventorySnapshot{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, inventoryLimit+1))
	if err != nil || len(raw) > inventoryLimit {
		return inventorySnapshot{}, errors.New("invalid inventory state")
	}
	var previous inventoryState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&previous); err != nil || previous.StateVersion != 1 || !validInventory(previous.inventorySnapshot) {
		return inventorySnapshot{}, errors.New("invalid inventory state")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return inventorySnapshot{}, errors.New("invalid inventory state")
	}
	return previous.inventorySnapshot, nil
}

// saveInventory publishes only complete JSON through a private, synced same-directory rename.
func saveInventory(statePath string, snapshot inventorySnapshot) error {
	if !validInventory(snapshot) {
		return errors.New("invalid inventory state")
	}
	raw, err := json.Marshal(inventoryState{StateVersion: 1, inventorySnapshot: snapshot})
	if err != nil || len(raw) > inventoryLimit {
		return errors.New("invalid inventory state")
	}
	if err = os.MkdirAll(statePath, 0700); err != nil {
		return err
	}
	if err = os.Chmod(statePath, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(statePath, ".inventory-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(statePath, "inventory.json")); err != nil {
		return err
	}
	directory, err := os.Open(statePath)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// inventoryFailure exposes only a stable error code, never kernel or filesystem diagnostics.
func inventoryFailure(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{"contractVersion": contracts.Version, "error": contracts.APIError{Code: "INVENTORY_UNAVAILABLE", MessageKey: "inventory.unavailable", RequestID: "inventory", Retryable: true}})
}
