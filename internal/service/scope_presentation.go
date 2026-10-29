package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/inventory"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"net/http"
	"sort"
	"strings"
	"time"
)

// scopeFreshKey is internal only; client headers cannot bypass the shared cache policy.
type scopeFreshKey struct{}

// catalogueCapture bounds private in-process output before projecting public fields.
type catalogueCapture struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	overflow bool
}

func (c *catalogueCapture) Header() http.Header { return c.header }
func (c *catalogueCapture) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}
func (c *catalogueCapture) Write(p []byte) (int, error) {
	if c.status == 0 {
		c.status = 200
	}
	if c.body.Len()+len(p) > inventoryLimit {
		c.overflow = true
		return 0, errors.New("inventory limit")
	}
	return c.body.Write(p)
}

// currentCatalogue invokes the existing discovery owner, not a second discovery implementation.
func currentCatalogue(ctx context.Context, handler http.Handler) (inventorySnapshot, error) {
	r, _ := http.NewRequestWithContext(context.WithValue(ctx, scopeFreshKey{}, true), http.MethodGet, "http://localhost/api/v1/inventory", nil)
	w := &catalogueCapture{header: make(http.Header)}
	handler.ServeHTTP(w, r)
	var envelope contracts.Envelope[inventorySnapshot]
	if w.status != 200 || w.overflow || security.DecodeJSON(bytes.NewReader(w.body.Bytes()), inventoryLimit, &envelope) != nil || envelope.ContractVersion != 1 || !validInventory(envelope.Data) {
		return inventorySnapshot{}, errors.New("inventory unavailable")
	}
	return envelope.Data, nil
}

// catalogueIdentity excludes changing capacity/timestamps but includes every source binding.
func catalogueIdentity(s inventorySnapshot) string {
	rows := []string{}
	for _, v := range s.Volumes {
		raw, _ := json.Marshal([]string{v.ID, v.MountPath, v.IdentityEvidence, v.State})
		rows = append(rows, string(raw))
	}
	for _, v := range s.Shares {
		raw, _ := json.Marshal([]any{v.ID, v.VolumeID, v.PhysicalPath, v.IdentityEvidence, v.State, v.Readable, v.Locked})
		rows = append(rows, string(raw))
	}
	sort.Strings(rows)
	return strings.Join(rows, "\n")
}

// presentation strips every path, filesystem statistic and private identity proof.
func presentation(s inventorySnapshot) inventory.Presentation {
	p := inventory.Presentation{Volumes: []inventory.PresentationVolume{}, Shares: []inventory.PresentationShare{}, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	for _, v := range s.Volumes {
		p.Volumes = append(p.Volumes, inventory.PresentationVolume{ID: v.ID, Name: v.Name, State: v.State})
	}
	for _, v := range s.Shares {
		var volume *string
		if v.VolumeID != "" {
			id := v.VolumeID
			volume = &id
		}
		p.Shares = append(p.Shares, inventory.PresentationShare{ID: v.ID, Name: v.Name, VolumeID: volume, State: v.State, Readable: v.Readable})
	}
	return p
}

// bindPreview opens only a root descriptor; it never enumerates children or grants permissions.
func bindPreview(ctx context.Context, r inventory.NormalizedRoot) (inventory.RootIdentity, error) {
	if e := ctx.Err(); e != nil {
		return inventory.RootIdentity{}, e
	}
	root, b, e := inventory.OpenScopeRoot(r)
	if e != nil {
		return inventory.RootIdentity{}, e
	}
	defer root.Close()
	return inventory.RootIdentity{Path: b.Path, Device: b.Device, Inode: b.Inode, MountID: b.MountID}, nil
}

// registerScopePresentation exposes only safe catalogue and root-preflight projections.
func registerScopePresentation(mux *http.ServeMux, shared http.Handler) {
	registerScopePresentationWithBinder(mux, shared, bindPreview)
}

// The injected binder permits permission and cancellation tests without changing actual ACLs.
func registerScopePresentationWithBinder(mux *http.ServeMux, shared http.Handler, bind inventory.BindRoot) {
	// One bounded browser cache belongs to this service lifetime, never a process-global session.
	browser := &inventory.FolderBrowser{}
	for _, kind := range []string{"scope-inventory", "scope-preview", "scope-folders"} {
		kind := kind
		mux.HandleFunc("/api/v1/"+kind, func(w http.ResponseWriter, r *http.Request) {
			principals := r.Header.Values("X-DiskMap-Principal")
			if len(principals) != 1 || principals[0] == "" || len(principals[0]) > 256 || r.URL.RawQuery != "" {
				security.ActionError(w, 400, "INVALID_REQUEST")
				return
			}
			want := http.MethodGet
			if kind != "scope-inventory" {
				want = http.MethodPost
			}
			if r.Method != want {
				w.Header().Set("Allow", want)
				w.WriteHeader(405)
				return
			}
			var in inventory.ScopePreviewRequest
			if kind == "scope-preview" && (security.DecodeJSON(r.Body, inventory.ScopeRequestLimit, &in) != nil || in.Validate() != nil) {
				security.ActionError(w, 400, "INVALID_REQUEST")
				return
			}
			var folders inventory.FolderRequest
			if kind == "scope-folders" && (security.DecodeJSON(r.Body, inventory.ScopeRequestLimit, &folders) != nil || folders.Validate() != nil) {
				security.ActionError(w, 400, "INVALID_REQUEST")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
			defer cancel()
			catalogue, e := currentCatalogue(ctx, shared)
			if e != nil {
				inventoryFailure(w)
				return
			}
			if kind == "scope-inventory" {
				out := presentation(catalogue)
				// Probe only the fixed recycle entry, independently of index inclusion and scan history.
				for i, share := range catalogue.Shares {
					out.Shares[i].RecycleState = "unknown"
					if share.Readable && share.State == "reachable" {
						out.Shares[i].RecycleState = inventory.ObserveRecycle(ctx, share.PhysicalPath)
					}
				}
				if !inventory.ValidPresentation(out) {
					inventoryFailure(w)
					return
				}
				scopeReply(w, out)
				return
			}
			if kind == "scope-folders" {
				out, err := browser.Browse(ctx, folders, catalogue.Volumes, catalogue.Shares)
				if err != nil {
					if errors.Is(err, inventory.ErrRootChanged) {
						security.ActionError(w, 409, "SCOPE_CHANGED")
					} else {
						inventoryFailure(w)
					}
					return
				}
				after, err := currentCatalogue(ctx, shared)
				if err != nil {
					inventoryFailure(w)
					return
				}
				if catalogueIdentity(catalogue) != catalogueIdentity(after) {
					security.ActionError(w, 409, "SCOPE_CHANGED")
					return
				}
				if !inventory.ValidFolders(out) {
					inventoryFailure(w)
					return
				}
				scopeReply(w, out)
				return
			}
			n, e := inventory.Normalize(in.Scope, catalogue.Volumes, catalogue.Shares)
			if e != nil {
				security.ActionError(w, 400, "INVALID_REQUEST")
				return
			}
			manifest, e := inventory.FreezeScope(ctx, n, bind)
			if e != nil {
				inventoryFailure(w)
				return
			}
			// A changed catalogue invalidates the observation rather than authorizing a replacement mount.
			after, e := currentCatalogue(ctx, shared)
			if e != nil {
				inventoryFailure(w)
				return
			}
			if catalogueIdentity(catalogue) != catalogueIdentity(after) {
				security.ActionError(w, 409, "SCOPE_CHANGED")
				return
			}
			out := inventory.ScopePreview{Scope: in.Scope, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Roots: []inventory.PreviewRoot{}, CoveredTargetIDs: manifest.CoveredTargetIDs, Issues: manifest.Issues}
			out.Scope.TargetIDs = append([]string{}, in.Scope.TargetIDs...)
			sort.Strings(out.Scope.TargetIDs)
			for _, root := range manifest.Roots {
				var volume *string
				if root.VolumeID != "" {
					id := root.VolumeID
					volume = &id
				}
				out.Roots = append(out.Roots, inventory.PreviewRoot{TargetID: root.TargetID, Kind: root.Kind, VolumeID: volume, State: root.State})
			}
			if !inventory.ValidPreview(out) {
				inventoryFailure(w)
				return
			}
			scopeReply(w, out)
		})
	}
}

// scopeReply fails before sending any oversized public projection.
func scopeReply(w http.ResponseWriter, out any) {
	raw, e := json.Marshal(contracts.Envelope[any]{ContractVersion: 1, RequestID: "scope", Data: out})
	if e != nil || len(raw) > inventory.PresentationLimit {
		inventoryFailure(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}
