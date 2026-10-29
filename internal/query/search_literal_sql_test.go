package query

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"reflect"
	"strings"
	"testing"
	"time"
)

// A rare literal must still find globally sorted hits throughout the complete captured scope.
func TestNativeLiteralSearchScan(t *testing.T) {
	const total = 100000
	db, h, _ := fixture(t, "wide", total, t.TempDir(), func(db *sql.DB) {
		for row := 10000; row <= total; row += 10000 {
			name := fmt.Sprintf("codex-%06d", row)
			// Preserve canonical identity and scope while assigning sparse recorded names for literal matching.
			if _, err := db.Exec(`UPDATE nodes SET name_bytes=? WHERE id=?`, []byte(name), row); err != nil {
				t.Fatal(err)
			}
		}
	})
	r := baseSearch(h)
	r.RootID = "nas"
	r.Text = "codex"
	r.Field = "name"
	r.Sort = "size"
	r.Direction = "desc"
	at := time.Now()
	out, err := Search(context.Background(), db, h, r, "")
	elapsed := time.Since(at)
	if err != nil || len(out.Items) != 10 || out.HasMore || out.WorkLimited {
		t.Fatalf("items=%d more=%t limited=%t err=%v", len(out.Items), out.HasMore, out.WorkLimited, err)
	}
	for i, item := range out.Items {
		if i > 0 && out.Items[i-1].Size != nil && item.Size != nil && compareBytes(*out.Items[i-1].Size, *item.Size) < 0 {
			t.Fatal("non-global numeric order")
		}
	}
	t.Logf("native metadata rows=%d sparse matches=%d search=%s", total, len(out.Items), elapsed)
	// A common one-character term still requires the complete global top page, not an early prefix.
	r.Text = "n"
	at = time.Now()
	broad, err := Search(context.Background(), db, h, r, "")
	if err != nil || len(broad.Items) != 50 || !broad.HasMore || broad.WorkLimited {
		t.Fatalf("broad rows=%d more=%t err=%v", len(broad.Items), broad.HasMore, err)
	}
	t.Logf("native metadata rows=%d broad one-character search=%s", total, time.Since(at))
	// More than 99% of this fixture matches; compare both full pages with the
	// independent path-reconstructing traversal, not just timing or hit counts.
	reference := r
	reference.Field = "both"
	want, e := Search(context.Background(), db, h, reference, "")
	if e != nil || !reflect.DeepEqual(broad.Items, want.Items) || broad.HasMore != want.HasMore {
		t.Fatalf("dense first page parity: %v", e)
	}
	next, e := Search(context.Background(), db, h, r, broad.NextCursor)
	if e != nil {
		t.Fatal(e)
	}
	nextWant, e := Search(context.Background(), db, h, reference, want.NextCursor)
	if e != nil || !reflect.DeepEqual(next.Items, nextWant.Items) || next.HasMore != nextWant.HasMore {
		t.Fatalf("dense second page parity: %v", e)
	}

}

// The SQL predicate must agree with the established display/fold contract for every field value.
func TestNativeLiteralPredicateParity(t *testing.T) {
	values := [][]byte{[]byte("CoDeX 100%_ ' OR 1=1 --"), []byte("Grüße 東京 Σςσ KkK ſS İı"), {'a', 0, 'b'}, {0xff, 'x', 0xfe}, []byte("abc bcd cde"), {}, []byte("e\u0301 é")}
	terms := []string{"", "c", "co", "CODEX", "%_", "' OR", "ü", "東京", "σ", "k", "s", "İ", "ı", "\x00", "\\xff", "abcde", "é", "e\u0301"}
	for _, raw := range values {
		for _, term := range terms {
			folded := index.SearchFold(term)
			want := strings.Contains(index.SearchFold(searchDisplay(raw)), folded)
			got := nativeLiteralContains(raw, folded)
			if got != want {
				t.Fatalf("raw=%x term=%q got=%t want=%t", raw, term, got, want)
			}
		}
	}
	// Exercise every byte as both valid ASCII and invalid UTF-8, including literal NUL.
	for value := 0; value < 256; value++ {
		raw := []byte{'x', byte(value), 'z'}
		folded := index.SearchFold(searchDisplay([]byte{byte(value)}))
		want := strings.Contains(index.SearchFold(searchDisplay(raw)), folded)
		if nativeLiteralContains(raw, folded) != want {
			t.Fatalf("byte parity %d", value)
		}
	}
}
