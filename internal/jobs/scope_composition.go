package jobs

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"math/big"
	"strings"
)

type scopeFragment struct{ snapshot, path string }

// A selected recycle subtree exposes its own size, but contributes no normal bytes to ancestors outside it.
func insideRecycleScope(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if strings.EqualFold(part, "#recycle") {
			return true
		}
	}
	return false
}

func containsScope(parent, child string) bool {
	return parent == "." || parent == child || strings.HasPrefix(child, parent+"/")
}

// Folder scans replace exactly their segment in an older encompassing observation.
func capturedSubscopes(h index.GenerationHeader, s ObservedScope) []scopeFragment {
	out := []scopeFragment{}
	for _, root := range h.Manifest.Provenance.Roots {
		if root.Kind != "share" || root.SourceID == nil || *root.SourceID != s.ShareID {
			continue
		}
		for _, r := range h.Manifest.Roots {
			if r.TargetID == root.TargetID && r.RelativePath != "" && r.RelativePath != "." && r.RelativePath != s.Path && containsScope(s.Path, r.RelativePath) {
				out = append(out, scopeFragment{h.SnapshotID, r.RelativePath})
			}
		}
	}
	return out
}

// Compose recursively over disjoint replacement roots; overlapping child scans are never added twice.
func (m *Manager) composeObservation(ctx context.Context, s ObservedScope, base ScopeObservation, fragments []scopeFragment) (ScopeObservation, error) {
	if len(fragments) == 0 {
		return base, nil
	}
	if base.Bytes == nil {
		zero := contracts.Bytes("0")
		base.Bytes = &zero
	}
	layers := append(append([]scopeFragment{}, fragments...), scopeFragment{base.SnapshotID, s.Path})
	cache := map[string]ScopeObservation{base.SnapshotID + ":" + s.Path: base}
	read := func(layer scopeFragment, path string) (ScopeObservation, error) {
		if layer.snapshot == "" {
			zero := contracts.Bytes("0")
			return ScopeObservation{ShareID: s.ShareID, Path: path, Bytes: &zero, RecycleBytes: &zero, State: "partial", coverageUnknown: true, Reasons: []contracts.ScanBoundary{}}, nil
		}
		key := layer.snapshot + ":" + path
		if v, ok := cache[key]; ok {
			return v, nil
		}
		db, close, e := m.generationDB(layer.snapshot)
		if e != nil {
			return ScopeObservation{}, e
		}
		defer close()
		h, e := index.ReadGenerationHeader(ctx, db, layer.snapshot)
		if e != nil {
			return ScopeObservation{}, e
		}
		recycled, e := m.cachedRecycleObservations(ctx, db, h.SnapshotID)
		if e != nil {
			return ScopeObservation{}, e
		}
		v, _, e := observeScope(ctx, db, h, ObservedScope{s.ShareID, path}, recycled)
		if e == nil {
			cache[key] = v
		}
		return v, e
	}
	var resolve func(string) (ScopeObservation, error)
	resolve = func(path string) (ScopeObservation, error) {
		anchor := len(layers) - 1
		for i, l := range layers {
			if containsScope(l.path, path) {
				anchor = i
				break
			}
		}
		result, e := read(layers[anchor], path)
		if e != nil {
			return result, e
		}
		// Only maximal child roots partition this area; recursion handles newer nested replacements.
		children := []string{}
		for i := 0; i < anchor; i++ {
			p := layers[i].path
			if p == path || !containsScope(path, p) {
				continue
			}
			covered := false
			for j := 0; j < anchor; j++ {
				q := layers[j].path
				if i != j && q != p && containsScope(q, p) {
					covered = true
					break
				}
			}
			if !covered {
				duplicate := false
				for _, old := range children {
					duplicate = duplicate || old == p
				}
				if !duplicate {
					children = append(children, p)
				}
			}
		}
		sum, recycle := new(big.Int), new(big.Int)
		if result.Bytes != nil {
			sum.SetString(string(*result.Bytes), 10)
		}
		if result.RecycleBytes != nil {
			recycle.SetString(string(*result.RecycleBytes), 10)
		}
		for _, child := range children {
			old, e := read(layers[anchor], child)
			if e != nil {
				return result, e
			}
			next, e := resolve(child)
			if e != nil {
				return result, e
			}
			// Project the selected child into the parent category before replacing its contribution.
			oldNormal, nextNormal := old.Bytes, next.Bytes
			if !insideRecycleScope(path) && insideRecycleScope(child) {
				oldNormal, nextNormal = nil, nil
			}
			for _, change := range []struct {
				total         *big.Int
				before, after *contracts.Bytes
			}{{sum, oldNormal, nextNormal}, {recycle, old.RecycleBytes, next.RecycleBytes}} {
				if change.before != nil {
					n, _ := new(big.Int).SetString(string(*change.before), 10)
					change.total.Sub(change.total, n)
				}
				if change.after != nil {
					n, _ := new(big.Int).SetString(string(*change.after), 10)
					change.total.Add(change.total, n)
				}
			}
			// Replace only gaps located inside the refreshed child, retaining intersecting ancestor gaps.
			result.ReasonCount += next.containedReasonCount - old.containedReasonCount
			result.containedReasonCount += next.containedReasonCount - old.containedReasonCount
			reasons := make([]contracts.ScanBoundary, 0, 16)
			for _, reason := range result.Reasons {
				if !containsScope(child, reason.RelativePath) {
					reasons = append(reasons, reason)
				}
			}
			for _, reason := range next.Reasons {
				if containsScope(child, reason.RelativePath) && len(reasons) < 16 {
					reasons = append(reasons, reason)
				}
			}
			result.Reasons = reasons
			result.coverageUnknown = result.coverageUnknown || next.coverageUnknown
			// Replace access-failure counts with the newer subtree, just like its byte totals.
			result.unreadableRecycleCount += next.containedUnreadableRecycleCount - old.containedUnreadableRecycleCount
			result.containedUnreadableRecycleCount += next.containedUnreadableRecycleCount - old.containedUnreadableRecycleCount
			result.unreadableSystemCount += next.containedUnreadableSystemCount - old.containedUnreadableSystemCount
			result.containedUnreadableSystemCount += next.containedUnreadableSystemCount - old.containedUnreadableSystemCount
			result.UnreadableRecycle = result.unreadableRecycleCount > 0
			result.UnreadableSystem = result.unreadableSystemCount > 0
			if result.State != "unscanned" {
				result.State = "complete"
				if result.ReasonCount > 0 || result.coverageUnknown {
					result.State = "partial"
				}
			}
		}
		if sum.Sign() < 0 || recycle.Sign() < 0 {
			return result, index.ErrGeneration
		}
		normal, bin := contracts.Bytes(sum.String()), contracts.Bytes(recycle.String())
		result.Bytes = &normal
		result.RecycleBytes = &bin
		return result, nil
	}
	return resolve(s.Path)
}
