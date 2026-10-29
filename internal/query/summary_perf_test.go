package query

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// referenceArithmetic retains the pre-optimization implementation for exact differential checks.
func referenceArithmetic(a, b contracts.Bytes, subtract bool) (contracts.Bytes, error) {
	if len(a) > 128 || len(b) > 128 || a.Validate() != nil || b.Validate() != nil {
		return "", ErrSnapshot
	}
	x, _ := new(big.Int).SetString(string(a), 10)
	y, _ := new(big.Int).SetString(string(b), 10)
	if subtract {
		x.Sub(x, y)
	} else {
		x.Add(x, y)
	}
	if x.Sign() < 0 || len(x.String()) > 128 {
		return "", ErrSnapshot
	}
	return contracts.Bytes(x.String()), nil
}

// Zero identities must still validate both operands and preserve overflow/underflow errors.
func TestArithmeticIdentityParity(t *testing.T) {
	values := []contracts.Bytes{"0", "1", "9", "9007199254740993", contracts.Bytes(strings.Repeat("9", 128)), contracts.Bytes("1" + strings.Repeat("0", 127)), "", "00", "01", "-1", "+1", " 0", "0\x00", "1e3", "١", contracts.Bytes(strings.Repeat("1", 129))}
	for _, a := range values {
		for _, b := range values {
			for _, sub := range []bool{false, true} {
				want, we := referenceArithmetic(a, b, sub)
				got, ge := arithmetic(a, b, sub)
				if got != want || (we == nil) != (ge == nil) {
					t.Fatalf("%q/%q subtract=%v: got %q/%v want %q/%v", a, b, sub, got, ge, want, we)
				}
			}
		}
	}
}

// These inputs model zero counters and repeated residual subtraction in a hierarchy response.
func BenchmarkSummaryArithmetic(b *testing.B) {
	for _, v := range []struct {
		name string
		a, c contracts.Bytes
		sub  bool
	}{{"zero-left", "0", "9007199254740993", false}, {"zero-right", "9007199254740993", "0", true}, {"subtract", "22517854021665679143456", "9007199254740993", true}} {
		b.Run(v.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := arithmetic(v.a, v.c, v.sub); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// An opt-in sealed fixture measures the real pipeline with identical, fully materialized output.
func TestHierarchyPerfMeasurement(t *testing.T) {
	if os.Getenv("DISKMAP_QUERY_LOCAL_PERF") != "1" {
		t.Skip("explicit local measurement required")
	}
	db, h, _ := fixture(t, "wide", 3000, t.TempDir())
	r := Request{SnapshotID: h.SnapshotID, RootID: "nas", Metric: "logical", Depth: 4, ElementBudget: 2817}
	at := time.Now()
	clock := func() time.Time { return at }
	want, err := hierarchy(context.Background(), db, h, r, clock)
	if err != nil {
		t.Fatal(err)
	}
	conserved(t, want)
	raw, _ := json.Marshal(want)
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			out, err := hierarchy(context.Background(), db, h, r, clock)
			if err != nil || len(out.Nodes) != len(want.Nodes) {
				b.Fatal(err)
			}
		}
	})
	t.Logf("nodes=%d bytes=%d ns/op=%d B/op=%d allocs/op=%d", len(want.Nodes), len(raw), result.NsPerOp(), result.AllocedBytesPerOp(), result.AllocsPerOp())
}
