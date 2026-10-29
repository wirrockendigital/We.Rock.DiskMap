package query

import (
	"context"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"testing"
)

// Wildcards are opt-in; extension, path and Unicode matching cannot become SQL patterns.
func TestListWildcardMatcher(t *testing.T) {
	for _, tc := range []struct {
		value, pattern string
		want           bool
	}{{"Folder/A.ZIP", "*.zip", true}, {"folder/report.pdf", "folder/*.pdf", true}, {"abc", "a*c", true}, {"ab", "ab*b", false}, {"a.zip.tmp", "*.zip", false}, {"Grüße.pdf", "grü*.pdf", true}, {"any", "*", true}, {"a%b", "a%b", true}} {
		if got := searchTextMatches([]byte(tc.value), index.SearchFold(tc.pattern), "glob"); got != tc.want {
			t.Fatalf("%q %q: %v", tc.value, tc.pattern, got)
		}
	}
	if searchTextMatches([]byte("a.zip"), "*.zip", "") {
		t.Fatal("literal mode changed")
	}
}

// Full cursor traversal must keep folders first in both directions without dropped rows.
func TestListOptionsCursorOrdering(t *testing.T) {
	db, h := searchFixture(t)
	for _, direction := range []string{"asc", "desc"} {
		r := baseSearch(h)
		r.Field = "name"
		r.Sort = "type"
		r.CategorySort = true
		r.FoldersFirst = true
		r.Direction = direction
		r.Limit = 7
		seen := map[string]bool{}
		cursor := ""
		files := false
		for {
			p, e := Search(context.Background(), db, h, r, cursor)
			if e != nil {
				t.Fatal(e)
			}
			for _, v := range p.Items {
				if seen[v.NodeID] {
					t.Fatal("duplicate")
				}
				seen[v.NodeID] = true
				if v.Kind == "file" {
					files = true
				}
				if files && v.Kind == "directory" {
					t.Fatal("folder after file")
				}
			}
			if !p.HasMore {
				break
			}
			cursor = p.NextCursor
		}
		if len(seen) != 999 {
			t.Fatalf("missing rows: %d", len(seen))
		}
	}
	r := baseSearch(h)
	r.MatchMode = "glob"
	r.Text = "*cde*"
	r.Field = "name"
	p, e := Search(context.Background(), db, h, r, "")
	if e != nil || len(p.Items) != 2 {
		t.Fatal(p, e)
	}
	r.Field = "both"
	p, e = Search(context.Background(), db, h, r, "")
	if e != nil || len(p.Items) != 2 {
		t.Fatal(p, e)
	}
}
