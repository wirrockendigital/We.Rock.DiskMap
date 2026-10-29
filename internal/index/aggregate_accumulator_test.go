package index

import (
	"math/big"
	"math/rand"
	"reflect"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// TestAccumulatorExactMetrics compares a large mixed stream against independent integer totals.
func TestAccumulatorExactMetrics(t *testing.T) {
	var a contentAccumulator
	var logical, allocated big.Int
	unknownL, unknownA := 0, 0
	rng := rand.New(rand.NewSource(168))
	for i := 0; i < 2000; i++ {
		l := contracts.Bytes(new(big.Int).Lsh(big.NewInt(rng.Int63()), uint(i%100)).String())
		p := contracts.Bytes(new(big.Int).Lsh(big.NewInt(rng.Int63()), uint(i%90)).String())
		var lp, ap *contracts.Bytes
		if i%7 == 0 {
			unknownL++
		} else {
			lp = &l
			v, _ := new(big.Int).SetString(string(l), 10)
			logical.Add(&logical, v)
		}
		if i%11 == 0 {
			unknownA++
		} else {
			ap = &p
			v, _ := new(big.Int).SetString(string(p), 10)
			allocated.Add(&allocated, v)
		}
		if err := a.file("document", lp, ap); err != nil {
			t.Fatal(err)
		}
	}
	c := a.content()
	if string(c.Logical.KnownBytes) != logical.String() || string(c.Allocated.KnownBytes) != allocated.String() || c.FileCount != "2000" || string(c.Logical.UnknownFileCount) != big.NewInt(int64(unknownL)).String() || string(c.Allocated.UnknownFileCount) != big.NewInt(int64(unknownA)).String() {
		t.Fatal(c)
	}
	if !reflect.DeepEqual(c.Types["document"], contracts.TypeAggregate{FileCount: c.FileCount, Logical: c.Logical, Allocated: c.Allocated}) {
		t.Fatal("type totals")
	}
	// A subsequent mutation cannot change an already emitted directory result.
	var parent contentAccumulator
	parent.add(c)
	if !reflect.DeepEqual(parent.content(), c) {
		t.Fatal("merge")
	}
	if err := a.file("image", nil, nil); err != nil {
		t.Fatal(err)
	}
	if c.FileCount != "2000" || len(c.Types) != 1 {
		t.Fatal("aliased result")
	}
	for _, bad := range []contracts.Bytes{"-1", "01", "1.5"} {
		if a.file("otherUnknown", &bad, nil) == nil {
			t.Fatal("invalid metric accepted", bad)
		}
	}
}
