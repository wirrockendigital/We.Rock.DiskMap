// Package scale provides synthetic streaming fixtures without touching source storage.
package scale

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strconv"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// Config pins every input that influences the generated NDJSON bytes.
type Config struct {
	Count int    `json:"count"`
	Seed  uint64 `json:"seed"`
	Shape string `json:"shape"`
}

// Manifest records immutable stream identity and independently predictable totals.
type Manifest struct {
	Version      int    `json:"version"`
	Config       Config `json:"config"`
	SHA256       string `json:"sha256"`
	Count        int    `json:"count"`
	KnownSum     string `json:"knownSum"`
	Unknown      int    `json:"unknown"`
	Zero         int    `json:"zero"`
	MaximumDepth int    `json:"maximumDepth"`
}

// validate bounds generation and rejects unsupported fixture modes.
func validate(c Config) error {
	if c.Count < 1000 || c.Count > 10000000 || c.Seed > 65535 {
		return fmt.Errorf("invalid fixture bounds")
	}
	if c.Shape != "deep" && c.Shape != "wide" && c.Shape != "skewed" {
		return fmt.Errorf("invalid shape")
	}
	return nil
}

// parent emits parents before children with fixed-memory topology.
func parent(c Config, i int) int {
	if i == 0 {
		return -1
	}
	if c.Shape == "deep" {
		if i < 257 {
			return i - 1
		}
		return (i + int(c.Seed)) % 257
	}
	if c.Shape == "skewed" && i > 64 {
		if i%10 != 0 {
			return 1
		}
		return 1 + (i+int(c.Seed))%64
	}
	return 0
}

// directory identifies the small directory spine shared by all generated leaves.
func directory(c Config, i int) bool {
	return i == 0 || c.Shape == "deep" && i < 257 || c.Shape == "skewed" && i <= 64
}

// amount cycles through known, unknown, zero and extremely large byte counts.
func amount(seed uint64, i int) *contracts.Bytes {
	var s string
	switch i % 4 {
	case 0:
		s = "0"
	case 1:
		return nil
	case 2:
		s = "9007199254740993"
	case 3:
		s = strconv.FormatUint(seed+uint64(i%1000)+1, 10)
	}
	b := contracts.Bytes(s)
	return &b
}

// Generate writes deterministic contract-compatible Node records and hashes exact NDJSON bytes.
func Generate(w io.Writer, c Config) (Manifest, error) {
	if err := validate(c); err != nil {
		return Manifest{}, err
	}
	hash := sha256.New()
	out := bufio.NewWriterSize(io.MultiWriter(w, hash), 65536)
	encoder := json.NewEncoder(out)
	paths := make(map[int]string)
	sum := new(big.Int)
	value := new(big.Int)
	unknown, zero, maxDepth := 0, 0, 0
	depths := make(map[int]int)
	for i := 0; i < c.Count; i++ {
		p := parent(c, i)
		name := "n" + strconv.Itoa(i)
		path := name
		var pid *string
		depth := 0
		if p >= 0 {
			id := strconv.Itoa(p)
			pid = &id
			path = paths[p] + "/" + name
			depth = depths[p] + 1
		}
		kind := "file"
		if directory(c, i) {
			kind = "directory"
			paths[i] = path
			depths[i] = depth
		}
		logical := amount(c.Seed, i)
		if kind == "directory" {
			b := contracts.Bytes("0")
			logical = &b
		}
		if logical == nil {
			unknown++
		} else {
			value.SetString(string(*logical), 10)
			sum.Add(sum, value)
			if *logical == "0" {
				zero++
			}
		}
		if depth > maxDepth {
			maxDepth = depth
		}
		n := contracts.Node{ID: strconv.Itoa(i), SnapshotID: "synthetic-v1", ParentID: pid, OriginalName: name, Kind: kind, VolumeID: "synthetic-volume", RelativePath: path, LogicalBytes: logical, AllocatedBytes: nil}
		if err := encoder.Encode(n); err != nil {
			return Manifest{}, err
		}
	}
	if err := out.Flush(); err != nil {
		return Manifest{}, err
	}
	return Manifest{1, c, hex.EncodeToString(hash.Sum(nil)), c.Count, sum.String(), unknown, zero, maxDepth}, nil
}

// Oracle computes expected counts and sums from residue classes, without generated records.
func Oracle(c Config) (Manifest, error) {
	if err := validate(c); err != nil {
		return Manifest{}, err
	}
	dirs := 1
	if c.Shape == "deep" {
		dirs = 257
	}
	if c.Shape == "skewed" {
		dirs = 65
	}
	countResidue := func(r int) int {
		if c.Count-1 < r {
			return 0
		}
		return (c.Count-1-r)/4 + 1
	}
	unknown := countResidue(1)
	zero := countResidue(0)
	large := countResidue(2)
	sum := new(big.Int).Mul(big.NewInt(int64(large)), big.NewInt(9007199254740993))
	// The small-value cycle repeats every 1000 entries, independently of hierarchy shape.
	for r := 3; r < 1000; r += 4 {
		n := 0
		if c.Count > r {
			n = (c.Count-1-r)/1000 + 1
		}
		term := new(big.Int).SetUint64(uint64(n) * (c.Seed + uint64(r) + 1))
		sum.Add(sum, term)
	}
	for i := 0; i < dirs; i++ {
		switch i % 4 {
		case 1:
			unknown--
			zero++
		case 2:
			sum.Sub(sum, big.NewInt(9007199254740993))
			zero++
		case 3:
			sum.Sub(sum, new(big.Int).SetUint64(c.Seed+uint64(i%1000)+1))
			zero++
		}
	}
	depth := 1
	if c.Shape == "deep" {
		depth = 257
	}
	if c.Shape == "skewed" {
		depth = 2
	}
	return Manifest{Version: 1, Config: c, Count: c.Count, KnownSum: sum.String(), Unknown: unknown, Zero: zero, MaximumDepth: depth}, nil
}

// Verify independently parses the stream and checks count, topology, paths, sums and hash.
func Verify(r io.Reader, c Config) (Manifest, error) {
	expected, err := Oracle(c)
	if err != nil {
		return Manifest{}, err
	}
	hash := sha256.New()
	decoder := json.NewDecoder(io.TeeReader(r, hash))
	sum := new(big.Int)
	v := new(big.Int)
	dirs := map[int]string{}
	depths := map[int]int{}
	count, unknown, zero, maxDepth := 0, 0, 0, 0
	for {
		var n contracts.Node
		err := decoder.Decode(&n)
		if err == io.EOF {
			break
		}
		if err != nil {
			return Manifest{}, err
		}
		i := count
		if n.ID != strconv.Itoa(i) || n.SnapshotID != "synthetic-v1" {
			return Manifest{}, fmt.Errorf("identity mismatch at %d", i)
		}
		depth := 0
		path := n.OriginalName
		if i == 0 {
			if n.ParentID != nil {
				return Manifest{}, fmt.Errorf("root parent")
			}
		} else {
			if n.ParentID == nil {
				return Manifest{}, fmt.Errorf("missing parent")
			}
			p, e := strconv.Atoi(*n.ParentID)
			if e != nil || p < 0 || p >= i {
				return Manifest{}, fmt.Errorf("ancestry order")
			}
			prefix, ok := dirs[p]
			if !ok {
				return Manifest{}, fmt.Errorf("parent is not a directory")
			}
			depth = depths[p] + 1
			path = prefix + "/" + n.OriginalName
			// Shape assertions are separate from the generator's parent selector.
			if c.Shape == "wide" && p != 0 || c.Shape == "deep" && i <= 256 && p != i-1 || c.Shape == "deep" && i > 256 && p != (i+int(c.Seed))%257 || c.Shape == "skewed" && i <= 64 && p != 0 || c.Shape == "skewed" && i > 64 && i%10 != 0 && p != 1 || c.Shape == "skewed" && i > 64 && i%10 == 0 && p != 1+(i+int(c.Seed))%64 {
				return Manifest{}, fmt.Errorf("shape mismatch")
			}
		}
		wantDir := i == 0 || c.Shape == "deep" && i <= 256 || c.Shape == "skewed" && i <= 64
		if (n.Kind == "directory") != wantDir || (!wantDir && n.Kind != "file") || n.RelativePath != path || n.OriginalName != "n"+strconv.Itoa(i) {
			return Manifest{}, fmt.Errorf("node structure mismatch")
		}
		if wantDir {
			dirs[i] = path
			depths[i] = depth
		}
		if n.LogicalBytes == nil {
			unknown++
		} else {
			if _, ok := v.SetString(string(*n.LogicalBytes), 10); !ok {
				return Manifest{}, fmt.Errorf("invalid bytes")
			}
			sum.Add(sum, v)
			if v.Sign() == 0 {
				zero++
			}
		}
		if depth > maxDepth {
			maxDepth = depth
		}
		count++
		if count > c.Count {
			return Manifest{}, fmt.Errorf("too many nodes")
		}
	}
	if count != expected.Count || sum.String() != expected.KnownSum || unknown != expected.Unknown || zero != expected.Zero || maxDepth != expected.MaximumDepth {
		return Manifest{}, fmt.Errorf("oracle mismatch: count=%d sum=%s unknown=%d zero=%d depth=%d", count, sum.String(), unknown, zero, maxDepth)
	}
	expected.SHA256 = hex.EncodeToString(hash.Sum(nil))
	return expected, nil
}
