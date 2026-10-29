package contracts

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// pointer creates explicit nullable fixture fields without production helpers.
func pointer[T any](value T) *T { return &value }

// hierarchyFixture mirrors the language-independent decimal and identity cases.
func hierarchyFixture() Hierarchy {
	return Hierarchy{ContractVersion: Version, SnapshotID: "snapshot-A", Nodes: []Node{
		{Kind: "directory", ID: "root", SnapshotID: "snapshot-A", LogicalBytes: pointer(Bytes("9007199254740993"))},
		{Kind: "file", ID: "child", SnapshotID: "snapshot-A", ParentID: pointer("root"), LogicalBytes: pointer(Bytes("0")), AllocatedBytes: pointer(Bytes("18446744073709551615"))},
	}}
}

// TestHierarchyRoundTrip verifies null, zero and integers beyond JavaScript precision.
func TestHierarchyRoundTrip(t *testing.T) {
	want := hierarchyFixture()
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got Hierarchy
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHierarchy(got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed contract: %s", data)
	}
	if !strings.Contains(string(data), `"allocatedBytes":null`) || !strings.Contains(string(data), `"logicalBytes":"0"`) {
		t.Fatalf("null or zero encoding changed: %s", data)
	}
}

// TestBytesRejectLossyValues tests incoming JSON and manually constructed values.
func TestBytesRejectLossyValues(t *testing.T) {
	for _, data := range []string{`0`, `9007199254740992`, `null`, `true`, `[]`, `{}`, `"-1"`, `"01"`, `"1e9"`, `""`, `" 1"`} {
		t.Run(data, func(t *testing.T) {
			var b Bytes
			if err := json.Unmarshal([]byte(data), &b); err == nil {
				t.Fatalf("accepted %s", data)
			}
		})
	}
	for _, value := range []Bytes{"", "01", "-1", "1.0"} {
		if _, err := json.Marshal(value); err == nil {
			t.Fatalf("marshaled invalid %q", value)
		}
	}
	var unknown *Bytes
	if err := json.Unmarshal([]byte(`null`), &unknown); err != nil || unknown != nil {
		t.Fatal("nullable bytes did not preserve null")
	}
}

// TestVersionMismatch rejects missing, wrong-type and unsupported envelope versions.
func TestVersionMismatch(t *testing.T) {
	for _, data := range []string{`{}`, `{"contractVersion":0}`, `{"contractVersion":2}`, `{"contractVersion":"1"}`} {
		if _, err := DecodeEnvelope[Volume]([]byte(data)); err == nil {
			t.Fatalf("accepted version: %s", data)
		}
	}
	if _, err := DecodeEnvelope[Volume]([]byte(`{"contractVersion":1,"requestId":"r","data":{"id":"v"}}`)); err != nil {
		t.Fatal(err)
	}
}

// TestHierarchyFailures separates snapshot, parent and cycle violations.
func TestHierarchyFailures(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Hierarchy)
		code   string
	}{
		{"version", func(h *Hierarchy) { h.ContractVersion = 2 }, "CONTRACT_VERSION_UNSUPPORTED"},
		{"snapshot", func(h *Hierarchy) { h.Nodes[1].SnapshotID = "other" }, "SNAPSHOT_MISMATCH"},
		{"parent", func(h *Hierarchy) { h.Nodes[1].ParentID = pointer("foreign") }, "PARENT_MISSING"},
		{"empty parent", func(h *Hierarchy) { h.Nodes[1].ParentID = pointer("") }, "PARENT_INVALID"},
		{"duplicate", func(h *Hierarchy) { h.Nodes[1].ID = "root" }, "NODE_ID_INVALID"},
		{"self cycle", func(h *Hierarchy) { h.Nodes[0].ParentID = pointer("root") }, "HIERARCHY_CYCLE"},
		{"cycle", func(h *Hierarchy) { h.Nodes[0].ParentID = pointer("child") }, "HIERARCHY_CYCLE"},
		{"invalid bytes", func(h *Hierarchy) { h.Nodes[0].LogicalBytes = pointer(Bytes("1e9")) }, "INVALID_BYTES"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := hierarchyFixture()
			tc.mutate(&h)
			if err := ValidateHierarchy(h); err == nil || err.Error() != tc.code {
				t.Fatalf("want %s, got %v", tc.code, err)
			}
		})
	}
}

// TestDeepHierarchy protects the iterative traversal against recursion limits.
func TestDeepHierarchy(t *testing.T) {
	h := Hierarchy{ContractVersion: Version, SnapshotID: "s", Nodes: make([]Node, 20000)}
	for i := range h.Nodes {
		h.Nodes[i] = Node{Kind: "directory", ID: fmt.Sprint(i), SnapshotID: "s"}
		if i > 0 {
			h.Nodes[i].ParentID = pointer(fmt.Sprint(i - 1))
		}
	}
	if err := ValidateHierarchy(h); err != nil {
		t.Fatal(err)
	}
}

// TestCoreWireNames checks all eight DTOs for explicit lower-camel JSON fields.
func TestCoreWireNames(t *testing.T) {
	values := []any{Volume{}, Share{}, Profile{}, Job{}, Snapshot{}, Node{}, Coverage{}, UserPreference{}}
	for _, value := range values {
		typeOf := reflect.TypeOf(value)
		for i := 0; i < typeOf.NumField(); i++ {
			field := typeOf.Field(i)
			tag := field.Tag.Get("json")
			if tag == "" || tag[0] < 'a' || tag[0] > 'z' || strings.Contains(tag, "omitempty") {
				t.Fatalf("%s.%s has invalid JSON contract %q", typeOf.Name(), field.Name, tag)
			}
		}
	}
}

// TestAllCoreObjectsRoundTrip retains their complete JSON shapes and nullable fields.
func TestAllCoreObjectsRoundTrip(t *testing.T) {
	values := []any{
		Volume{ID: "volume-1", Name: "Volume 1", State: "reachable", Capacity: Measurement{Value: pointer(Bytes("9007199254740993"))}},
		Share{ID: "share-1", VolumeID: "volume-1", FileStationPath: pointer("/share-1"), Readable: true},
		Profile{ID: "profile-1", Revision: 1, Scope: Scope{Kind: "shares", TargetIDs: []string{"share-1"}}, Exclusions: []Exclusion{}},
		Job{ID: "job-1", BytesObserved: Bytes("0"), EntriesVisited: "0", Warnings: []APIError{}},
		Snapshot{ID: "snapshot-1", LogicalBytes: Bytes("9007199254740993"), StorageBytes: Bytes("0"), Methods: []string{}, Coverage: []Coverage{}},
		hierarchyFixture().Nodes[0],
		Coverage{SnapshotID: "snapshot-1", RootID: "root", State: "missing", MeasurementLimits: []string{"source-unavailable"}},
		UserPreference{Version: PreferenceVersion, Display: DefaultPreferenceDisplay(), DSMUserID: "user-1", Revision: 1, Language: "de", Theme: "auto", Map: "treemap"},
	}
	for _, value := range values {
		t.Run(reflect.TypeOf(value).Name(), func(t *testing.T) {
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			decoded := reflect.New(reflect.TypeOf(value))
			if err := json.Unmarshal(data, decoded.Interface()); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(value, decoded.Elem().Interface()) {
				t.Fatalf("round trip changed %s", data)
			}
		})
	}
}

// TestNodeTypeMetadata rejects payload sizes invented for non-file special objects.
func TestNodeTypeMetadata(t *testing.T) {
	for _, kind := range []string{"fifo", "socket", "blockDevice", "characterDevice"} {
		n := Node{Kind: kind}
		if err := ValidateNodeType(n); err != nil {
			t.Fatal(err)
		}
		n.LogicalBytes = pointer(Bytes("0"))
		if ValidateNodeType(n) == nil {
			t.Fatal("invented special payload accepted", kind)
		}
	}
	if ValidateNodeType(Node{Kind: "other"}) == nil {
		t.Fatal("unknown kind accepted")
	}
}
