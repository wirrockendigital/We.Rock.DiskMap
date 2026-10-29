package scale

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

// checkStream connects generation and independent verification with bounded pipe buffers.
func checkStream(t *testing.T, c Config) Manifest {
	t.Helper()
	reader, writer := io.Pipe()
	result := make(chan Manifest, 1)
	errors := make(chan error, 1)
	go func() { m, err := Generate(writer, c); writer.CloseWithError(err); result <- m; errors <- err }()
	verified, err := Verify(reader, c)
	reader.CloseWithError(err)
	generated := <-result
	genErr := <-errors
	if err != nil || genErr != nil {
		t.Fatalf("verify=%v generate=%v", err, genErr)
	}
	if generated != verified {
		t.Fatalf("manifest mismatch %+v / %+v", generated, verified)
	}
	return generated
}

// TestFixtures checks every standard size when the explicit scale suite is enabled.
func TestFixtures(t *testing.T) {
	sizes := []int{1000}
	if os.Getenv("DISKMAP_SCALE_FULL") == "1" {
		sizes = []int{100000, 1000000, 10000000}
	}
	for _, shape := range []string{"wide", "deep", "skewed"} {
		for _, count := range sizes {
			c := Config{count, 42, shape}
			first := checkStream(t, c)
			second := checkStream(t, c)
			if first != second {
				t.Fatal("nondeterministic fixture")
			}
			b, _ := json.Marshal(first)
			t.Log(string(b))
		}
	}
}

// TestCorruptFixtures proves deterministic rejection of ancestry, count and sum errors.
func TestCorruptFixtures(t *testing.T) {
	c := Config{1000, 42, "wide"}
	var b bytes.Buffer
	if _, err := Generate(&b, c); err != nil {
		t.Fatal(err)
	}
	valid := b.String()
	corrupt := []string{
		strings.Replace(valid, `"parentId":"0"`, `"parentId":"999999"`, 1),
		strings.Replace(valid, `"logicalBytes":"9007199254740993"`, `"logicalBytes":"9007199254740994"`, 1),
		valid[:strings.LastIndex(strings.TrimSuffix(valid, "\n"), "\n")+1],
		strings.Replace(valid, `"snapshotId":"synthetic-v1"`, `"snapshotId":"foreign"`, 1),
	}
	for _, s := range corrupt {
		if _, err := Verify(strings.NewReader(s), c); err == nil {
			t.Fatal("accepted corrupt fixture")
		}
	}
	for _, bad := range []Config{{0, 42, "wide"}, {1000, 65536, "wide"}, {1000, 42, "other"}} {
		if _, err := Generate(io.Discard, bad); err == nil {
			t.Fatal("accepted invalid configuration")
		}
	}
}

// TestGoldenStreams pins the fixture format so byte-level changes require an explicit review.
func TestGoldenStreams(t *testing.T) {
	golden := map[string]string{
		"wide":   "26675860d703da05746a0dedd4a1a918a963e6e5e351fde493f581799359fb76",
		"deep":   "24ca5cb5beb53df28cb696f64cfd875ba39f10eadbc9a1f3c2fcb96dd6ec2723",
		"skewed": "5d2b162531265705b2e897bffc75e9e86ed0d6da2f174770fa3fff22657c2190",
	}
	for shape, hash := range golden {
		manifest, err := Generate(io.Discard, Config{1000, 42, shape})
		if err != nil || manifest.SHA256 != hash {
			t.Fatalf("golden %s changed: %+v %v", shape, manifest, err)
		}
		other, err := Generate(io.Discard, Config{1000, 43, shape})
		if err != nil || other.SHA256 == hash {
			t.Fatalf("seed did not affect %s: %v", shape, err)
		}
	}
}

// TestReferenceManifests checks that all recorded scale results still match the independent oracle.
func TestReferenceManifests(t *testing.T) {
	data, err := os.ReadFile("reference-manifests.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifests []Manifest
	if err := json.Unmarshal(data, &manifests); err != nil {
		t.Fatal(err)
	}
	if len(manifests) != 9 {
		t.Fatal("incomplete scale matrix")
	}
	seen := map[Config]bool{}
	for _, recorded := range manifests {
		if seen[recorded.Config] {
			t.Fatal("duplicate configuration")
		}
		seen[recorded.Config] = true
		expected, err := Oracle(recorded.Config)
		if err != nil {
			t.Fatal(err)
		}
		expected.SHA256 = recorded.SHA256
		if len(recorded.SHA256) != 64 || expected != recorded {
			t.Fatalf("reference oracle mismatch: %+v", recorded)
		}
	}
}
