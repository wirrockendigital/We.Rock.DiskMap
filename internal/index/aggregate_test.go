package index

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// TestAggregateMappingCanonicalization proves bounded, copied Unicode configuration without content access.
func TestAggregateMappingCanonicalization(t *testing.T) {
	m := contracts.FileTypeMapping{Version: 1, Revision: 2, Assignments: []contracts.ExtensionMapping{{Extension: "TXT", Category: "document"}, {Extension: "图像", Category: "image"}}}
	got, err := CanonicalFileTypeMapping(m)
	if err != nil {
		t.Fatal(err)
	}
	m.Assignments[0].Category = "audio"
	lookup := map[string]string{}
	for _, a := range got.Assignments {
		lookup[a.Extension] = a.Category
	}
	for name, want := range map[string]string{"FILE.TxT": "document", "picture.图像": "image", "no.extension": "otherUnknown", ".txt": "otherUnknown", "invalid\xff.txt": "otherUnknown"} {
		if v := classifyName([]byte(name), lookup); v != want {
			t.Fatal(name, v, want)
		}
	}
	for _, bad := range []contracts.FileTypeMapping{
		{}, {Version: 2, Revision: 1}, {Version: 1, Revision: 0},
		{Version: 1, Revision: 1, Assignments: []contracts.ExtensionMapping{{Extension: "txt", Category: "x"}}},
		{Version: 1, Revision: 1, Assignments: []contracts.ExtensionMapping{{Extension: "x/y", Category: "image"}}},
		{Version: 1, Revision: 1, Assignments: []contracts.ExtensionMapping{{Extension: "x.y", Category: "image"}}},
		{Version: 1, Revision: 1, Assignments: []contracts.ExtensionMapping{{Extension: "\xff", Category: "image"}}},
		{Version: 1, Revision: 1, Assignments: []contracts.ExtensionMapping{{Extension: strings.Repeat("a", 65), Category: "image"}}},
		{Version: 1, Revision: 1, Assignments: []contracts.ExtensionMapping{{Extension: "TXT", Category: "image"}, {Extension: "txt", Category: "audio"}}},
		{Version: 1, Revision: 1, Assignments: make([]contracts.ExtensionMapping, 257)},
	} {
		if _, err := CanonicalFileTypeMapping(bad); err == nil {
			t.Fatal("invalid mapping accepted")
		}
	}
}

// TestAggregateSealRejectsTampering keeps calculation distinct from independent final validation.
func TestAggregateSealRejectsTampering(t *testing.T) {
	mutations := map[string]string{
		"rawName": "UPDATE nodes SET name_bytes=CAST('large.txt' AS BLOB) WHERE id=2",
		"missing": "DELETE FROM directory_aggregates",
		"sum":     "UPDATE directory_aggregates SET aggregate_json=json_set(aggregate_json,'$.subtree.logical.knownBytes','9007199254740994')",
		"count":   "UPDATE directory_aggregates SET aggregate_json=json_set(aggregate_json,'$.direct.fileCount','4')",
		"unknown": "UPDATE directory_aggregates SET aggregate_json=json_set(aggregate_json,'$.subtree.logical.unknownFileCount','0')",
		"type":    "UPDATE directory_aggregates SET aggregate_json=json_set(aggregate_json,'$.subtree.types.otherUnknown.fileCount','1')",
		"mapping": "UPDATE aggregate_mapping SET mapping_json=json_set(mapping_json,'$.revision',2)",
		"foreign": "PRAGMA foreign_keys=OFF; INSERT INTO directory_aggregates SELECT 999,aggregate_json FROM directory_aggregates LIMIT 1",
	}
	for name, query := range mutations {
		t.Run(name, func(t *testing.T) {
			db, h := generationFixture(t)
			if _, e := db.Exec(query); e != nil {
				t.Fatal(e)
			}
			if FinalizeGeneration(context.Background(), db, h) == nil {
				t.Fatal("tampering sealed")
			}
			var n int
			db.QueryRow(`SELECT count(*) FROM generation_header`).Scan(&n)
			if n != 0 {
				t.Fatal("partial seal")
			}
		})
	}
	db, h := generationFixture(t)
	if err := FinalizeGeneration(context.Background(), db, h); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`DELETE FROM directory_aggregates`, `UPDATE directory_aggregates SET aggregate_json='{}'`, `DELETE FROM aggregate_mapping`, `UPDATE aggregate_mapping SET mapping_json='{}'`} {
		if _, err := db.Exec(q); err == nil {
			t.Fatal("sealed aggregate changed")
		}
	}
}

// TestAggregateCancelAndQueryPlan validates rollback and the actual postorder keyset index.
func TestAggregateCancelAndQueryPlan(t *testing.T) {
	db, _ := generationFixture(t)
	if _, err := db.Exec(`DELETE FROM directory_aggregates; DELETE FROM aggregate_mapping`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if BuildDerivedProjections(ctx, db, DefaultFileTypeMapping()) == nil {
		t.Fatal("cancel ignored")
	}
	var n int
	db.QueryRow(`SELECT count(*) FROM aggregate_mapping`).Scan(&n)
	if n != 0 {
		t.Fatal("cancel published mapping")
	}
	if _, err := db.Exec(`CREATE TEMP TABLE aggregate_order(rank INTEGER NOT NULL,node_id TEXT NOT NULL,PRIMARY KEY(rank,node_id)) WITHOUT ROWID`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT rank,node_id FROM aggregate_order WHERE (rank,node_id)>(?,?) ORDER BY rank,node_id LIMIT 64`, -1, "")
	if err != nil {
		t.Fatal(err)
	}
	var plans []string
	for rows.Next() {
		var a, b, c int
		var detail string
		if err = rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plans = append(plans, detail)
	}
	rows.Close()
	text := strings.Join(plans, " ")
	if strings.Contains(text, "TEMP B-TREE") || !strings.Contains(text, "SEARCH aggregate_order USING PRIMARY KEY") {
		t.Fatal(text)
	}
	db.Exec(`DROP TABLE aggregate_order`)
	if err = BuildDerivedProjections(context.Background(), db, DefaultFileTypeMapping()); err != nil {
		t.Fatal(err)
	}
	// JSON itself preserves the integer beyond binary64, including nested type aggregates.
	var raw string
	db.QueryRow(`SELECT aggregate_json FROM directory_aggregates LIMIT 1`).Scan(&raw)
	var got contracts.DirectoryAggregate
	if json.Unmarshal([]byte(raw), &got) != nil || got.Subtree.Logical.KnownBytes != "9007199254740993" {
		t.Fatal(raw)
	}
}

// A recorded raw name is the single path authority; rebuilding classifications cannot leave a second stale path copy.
func TestAggregateNameUsesSingleTreeAuthority(t *testing.T) {
	db, h := generationFixture(t)
	if _, err := db.Exec(`DELETE FROM directory_aggregates; DELETE FROM aggregate_mapping; UPDATE nodes SET name_bytes=CAST('large.txt' AS BLOB) WHERE id=2`); err != nil {
		t.Fatal(err)
	}
	if err := BuildDerivedProjections(context.Background(), db, h.FileTypeMapping); err != nil {
		t.Fatal(err)
	}
	if err := FinalizeGeneration(context.Background(), db, h); err != nil {
		t.Fatal(err)
	}
	v, err := ReadNode(context.Background(), db, "2")
	if err != nil || string(v.Path) != "large.txt" || v.Node.OriginalName != "large.txt" {
		t.Fatal(v, err)
	}
}

// TestAggregateMappingEncodedBudget rejects escape expansion before persistent settings can be poisoned.
func TestAggregateMappingEncodedBudget(t *testing.T) {
	m := contracts.FileTypeMapping{Version: 1, Revision: 2}
	for i := 0; i < 256; i++ {
		m.Assignments = append(m.Assignments, contracts.ExtensionMapping{Extension: strings.Repeat("&", 60) + string(rune(0x400+i)), Category: "document"})
	}
	// With HTML escaping disabled this configuration fits in the private request budget.
	var input bytes.Buffer
	encoder := json.NewEncoder(&input)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(m); err != nil || input.Len() >= 32768 {
		t.Fatal(input.Len(), err)
	}
	if _, err := CanonicalFileTypeMapping(m); err == nil {
		t.Fatal("encoded mapping overflow accepted")
	}
}
