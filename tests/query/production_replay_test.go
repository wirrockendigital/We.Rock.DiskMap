package querybench

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/query"
	"github.com/wirrockendigital/We.Rock.DiskMap/tests/scale"
)

// percentile uses nearest rank and never substitutes the best observed request for p95.
func percentile(samples []time.Duration) time.Duration {
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	return samples[(95*len(samples)+99)/100-1]
}

// exactConservation checks every rendered parent's integer byte sum independently of geometry.
func exactConservation(t *testing.T, out query.Response) {
	t.Helper()
	fields := func(s query.Summary) []string {
		return []string{string(s.KnownBytes), string(s.UnknownFileCount), string(s.FileCount), string(s.DirectoryCount), string(s.SpecialCount)}
	}
	sums := map[string][]*big.Int{}
	for _, n := range out.Nodes {
		if n.ParentID == nil {
			continue
		}
		if sums[*n.ParentID] == nil {
			sums[*n.ParentID] = []*big.Int{new(big.Int), new(big.Int), new(big.Int), new(big.Int), new(big.Int)}
		}
		for i, v := range fields(n.Summary) {
			amount, ok := new(big.Int).SetString(v, 10)
			if !ok {
				t.Fatal("invalid count")
			}
			sums[*n.ParentID][i].Add(sums[*n.ParentID][i], amount)
		}
	}
	for _, n := range out.Nodes {
		if sum := sums[n.ID]; sum != nil {
			for i, want := range fields(n.Summary) {
				expected, _ := new(big.Int).SetString(want, 10)
				if i == 3 && n.Kind == "directory" {
					expected.Sub(expected, big.NewInt(1))
				}
				if sum[i].Cmp(expected) != 0 {
					t.Fatal("parent conservation mismatch", n.ID, i)
				}
			}
		}
	}
}

// TestProductionQueryP95 measures a closed current-schema ten-million-row index in a separate process.
func TestProductionQueryP95(t *testing.T) {
	path := os.Getenv("DISKMAP_SCALE_REPLAY")
	if path == "" {
		t.Skip("explicit sealed local production index required")
	}
	u := url.URL{Scheme: "file", Path: path}
	db, e := sql.Open("sqlite", u.String()+"?mode=ro&immutable=1&_pragma=query_only(1)&_pragma=cache_size(-8192)&_pragma=temp_store(1)&_pragma=temp.cache_size(-2048)&_pragma=mmap_size(0)")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	h, e := index.ReadGenerationHeader(ctx, db, "synthetic-v1")
	if e != nil || h.Entries != 10000000 || h.SchemaVersion != index.GenerationSchema {
		t.Fatal("current sealed 10M required", e, h.Entries)
	}
	shape := os.Getenv("DISKMAP_SCALE_SHAPE")
	oracle, e := scale.Oracle(scale.Config{Count: 10000000, Seed: 7, Shape: shape})
	if e != nil {
		t.Fatal("explicit wide/deep/skewed shape required", e)
	}
	// Topology counts are fixed by the independent fixture definition, not the sealed header.
	directoryCount := map[string]int{"wide": 1, "deep": 257, "skewed": 65}[shape]
	rootRecord, e := index.FindNode(ctx, db, "volume", []byte("."))
	if e != nil {
		t.Fatal(e)
	}
	root := rootRecord.Node.ID
	// Sample each selector class independently so fast empty queries cannot hide slow common terms.
	for _, order := range []string{"name", "size", "mtime", "type"} {
		for _, term := range []string{"n", "n9", "n99", "n999", "missing-not-present"} {
			for _, direction := range []string{"asc", "desc"} {
				r := query.SearchRequest{SnapshotID: h.SnapshotID, RootID: root, Scope: "recursive", Field: "both", Metric: "logical", Sort: order, Direction: direction, Limit: 50, Text: term}
				t.Run(fmt.Sprintf("%s-%s-%s", order, direction, term), func(t *testing.T) {
					samples := []time.Duration{}
					for repeat := 0; repeat < 20; repeat++ {
						at := time.Now()
						out, err := query.Search(ctx, db, h, r, "")
						elapsed := time.Since(at)
						if err != nil {
							t.Fatal(order, term, direction, err)
						}
						samples = append(samples, elapsed)
						validateSearchItems(t, db, out, r)
						if term == "missing-not-present" {
							if len(out.Items) != 0 || out.HasMore {
								t.Fatal("false match")
							}
						} else if len(out.Items) != 50 || out.WorkLimited {
							t.Fatal("first fifty incomplete", order, term, len(out.Items), out.WorkLimited)
						}
						encoded, _ := json.Marshal(out)
						if len(encoded) > query.MaxResponseBytes {
							t.Fatal("response budget")
						}
						if repeat == 0 && out.HasMore {
							next, err := query.Search(ctx, db, h, r, out.NextCursor)
							if err != nil {
								t.Fatal(err)
							}
							if len(next.Items) != 50 || next.WorkLimited {
								t.Fatal("second fifty incomplete")
							}
							validateSearchItems(t, db, next, r)
							joined := append(append([]query.SearchItem{}, out.Items...), next.Items...)
							validateSearchItems(t, db, query.SearchPage{Items: joined}, r)
							seen := map[string]bool{}
							for _, item := range out.Items {
								seen[item.NodeID] = true
							}
							for _, item := range next.Items {
								if seen[item.NodeID] {
									t.Fatal("page overlap")
								}
							}
						}
					}
					p95 := percentile(samples)
					t.Logf("search sort=%s direction=%s term=%q samples=%d p95=%s max=%s", order, direction, term, len(samples), p95, samples[len(samples)-1])
					if p95 >= 2*time.Second {
						t.Errorf("search p95 budget exceeded: %s", p95)
					}
				})
			}
		}
	}
	// Both full-root opening and a real child drilldown retain explicit budgets and conservation checks.
	// Choose first, middle and last recorded directory subtrees without scanning all file rows.
	rows, e := db.QueryContext(ctx, `SELECT n.id FROM directory_aggregates a JOIN nodes n ON n.id=a.node_id WHERE n.parent_id IS NOT NULL ORDER BY n.rowid LIMIT 257`)
	if e != nil {
		t.Fatal(e)
	}
	directories := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			t.Fatal(e)
		}
		directories = append(directories, id)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	rows.Close()
	selections := []struct{ name, id string }{{"root", root}}
	seen := map[string]bool{}
	if len(directories) == 0 {
		t.Log("child drilldown not applicable: fixture has no child directories")
	} else {
		for _, position := range []int{0, len(directories) / 2, len(directories) - 1} {
			id := directories[position]
			if !seen[id] {
				selections = append(selections, struct{ name, id string }{fmt.Sprintf("directory-%d", position), id})
				seen[id] = true
			}
		}
	}
	for _, selected := range selections {
		for _, budget := range []int{2000, 10000} {
			samples := []time.Duration{}
			maxBytes, maxNodes := 0, 0
			for repeat := 0; repeat < 20; repeat++ {
				at := time.Now()
				out, err := query.Hierarchy(ctx, db, h, query.Request{SnapshotID: h.SnapshotID, RootID: selected.id, Metric: "logical", Depth: 8, ElementBudget: budget})
				elapsed := time.Since(at)
				if err != nil {
					t.Fatal(err)
				}
				samples = append(samples, elapsed)
				exactConservation(t, out)
				if repeat == 0 {
					t.Logf("subtree selected=%s id=%s files=%s directories=%s known_bytes=%s unknown_files=%s", selected.name, selected.id, out.Nodes[0].Summary.FileCount, out.Nodes[0].Summary.DirectoryCount, out.Nodes[0].Summary.KnownBytes, out.Nodes[0].Summary.UnknownFileCount)
				}
				encoded, _ := json.Marshal(out)
				if len(encoded) > query.MaxResponseBytes || len(out.Nodes) > budget {
					t.Fatal("bounded response failed")
				}
				maxBytes = max(maxBytes, len(encoded))
				maxNodes = max(maxNodes, len(out.Nodes))
				if selected.name == "root" {
					summary := out.Nodes[0].Summary
					if string(summary.KnownBytes) != oracle.KnownSum || string(summary.UnknownFileCount) != fmt.Sprint(oracle.Unknown) || string(summary.FileCount) != fmt.Sprint(10000000-directoryCount) || string(summary.DirectoryCount) != fmt.Sprint(directoryCount) || summary.SpecialCount != "0" {
						t.Fatal("independent fixture oracle mismatch")
					}
				}
			}
			p95 := percentile(samples)
			t.Logf("hierarchy selected=%s budget=%d samples=%d p95=%s max=%s max_bytes=%d max_nodes=%d", selected.name, budget, len(samples), p95, samples[len(samples)-1], maxBytes, maxNodes)
			budgetTime := time.Second
			if selected.name == "root" {
				budgetTime = 3 * time.Second
			}
			if p95 >= budgetTime {
				t.Errorf("hierarchy %s p95 budget exceeded: %s >= %s", selected.name, p95, budgetTime)
			}
		}
	}
	var usage syscall.Rusage
	if e = syscall.Getrusage(syscall.RUSAGE_SELF, &usage); e != nil {
		t.Fatal(e)
	}
	rss := uint64(usage.Maxrss)
	if runtime.GOOS == "linux" {
		rss *= 1024
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	t.Logf("schema=%d nodes=%d peak_rss_bytes=%d heap_inuse=%d goos=%s runtime=%s cache=8MiB sequential_warm_with_first_sample", h.SchemaVersion, h.Entries, rss, mem.HeapInuse, runtime.GOOS, runtime.Version())
	if rss > 1<<30 {
		t.Error("query process exceeds 1GiB")
	}
}

// validateSearchItems checks fixture membership, literal matches and cross-page primary ordering.
func validateSearchItems(t *testing.T, db *sql.DB, page query.SearchPage, r query.SearchRequest) {
	t.Helper()
	for i, item := range page.Items {
		recorded, err := index.FindNode(context.Background(), db, "volume", []byte(item.Path))
		if err != nil || item.SnapshotID != r.SnapshotID || item.NodeID != recorded.Node.ID || (!strings.Contains(item.Name, r.Text) && !strings.Contains(item.Path, r.Text)) {
			t.Fatal("wrong search match", item.NodeID)
		}
		if i == 0 {
			continue
		}
		a, b := page.Items[i-1], item
		comparison := 0
		switch r.Sort {
		case "name":
			comparison = strings.Compare(a.Name, b.Name)
		case "size":
			if a.Size == nil && b.Size != nil {
				comparison = -1
			} else if a.Size != nil && b.Size == nil {
				comparison = 1
			} else if a.Size != nil {
				av, _ := new(big.Int).SetString(string(*a.Size), 10)
				bv, _ := new(big.Int).SetString(string(*b.Size), 10)
				comparison = av.Cmp(bv)
			}
		case "type":
			comparison = strings.Compare(a.Kind, b.Kind)
		case "mtime":
			if a.ModifiedAt == nil && b.ModifiedAt != nil {
				comparison = -1
			} else if a.ModifiedAt != nil && b.ModifiedAt == nil {
				comparison = 1
			} else if a.ModifiedAt != nil {
				comparison = strings.Compare(*a.ModifiedAt, *b.ModifiedAt)
			}
		}
		if (r.Direction == "asc" && comparison > 0) || (r.Direction == "desc" && comparison < 0) {
			t.Fatal("search primary sort mismatch", r.Sort, r.Direction)
		}
	}
}
