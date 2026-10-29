package query

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"math/big"
	"strings"
	"testing"
)

// Numeric locators are canonical, generation-bound and never fall back to former hash identities.
func TestNumericQueryLocatorAdmission(t *testing.T) {
	db, h, _ := fixture(t, "wide", 1000, t.TempDir())
	for _, id := range []string{"0", "-1", "01", "+1", "9223372036854775808", strings.Repeat("a", 64)} {
		r := Request{SnapshotID: h.SnapshotID, RootID: id, Metric: "logical", Depth: 1, ElementBudget: 10}
		if _, err := Hierarchy(context.Background(), db, h, r); err == nil {
			t.Fatalf("accepted noncanonical locator %q", id)
		}
	}
	r := baseSearch(h)
	for _, row := range []int64{-1, 0} {
		if _, _, err := decodeSearchCursor(r, encodeSearchCursor(r, row)); err == nil {
			t.Fatal("accepted nonpositive search cursor")
		}
	}
	wrong := h
	wrong.SnapshotID = "another-generation"
	if _, err := Search(context.Background(), db, wrong, baseSearch(wrong), ""); err == nil {
		t.Fatal("accepted wrong generation")
	}
}

// The native top-page key preserves exact 128-digit values without floating-point or lexical-size errors.
func TestNumericSearchSizeKeyOracle(t *testing.T) {
	values := []string{"0", "1", "9", "10", "99", "100", "9007199254740993", strings.Repeat("9", 128)}
	for i, a := range values {
		for j, b := range values {
			av, bv := contracts.Bytes(a), contracts.Bytes(b)
			ak, err := candidateKey(SearchRequest{Sort: "size"}, SearchItem{Size: &av})
			if err != nil {
				t.Fatal(err)
			}
			bk, err := candidateKey(SearchRequest{Sort: "size"}, SearchItem{Size: &bv})
			if err != nil {
				t.Fatal(err)
			}
			ai, _ := new(big.Int).SetString(a, 10)
			bi, _ := new(big.Int).SetString(b, 10)
			for _, desc := range []bool{false, true} {
				want := ai.Cmp(bi) < 0
				if desc {
					want = ai.Cmp(bi) > 0
				}
				if candidateLess(searchCandidate{key: ak, row: 1}, searchCandidate{key: bk, row: 1}, desc) != want {
					t.Fatal(i, j, desc)
				}
			}
		}
	}
	unknown, _ := candidateKey(SearchRequest{Sort: "size"}, SearchItem{})
	zero := contracts.Bytes("0")
	known, _ := candidateKey(SearchRequest{Sort: "size"}, SearchItem{Size: &zero})
	if !candidateLess(searchCandidate{key: unknown}, searchCandidate{key: known}, false) {
		t.Fatal("unknown size became zero")
	}
}
