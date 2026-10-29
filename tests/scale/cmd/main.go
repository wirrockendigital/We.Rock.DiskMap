// Command scalefixtures streams synthetic metadata to stdout and its manifest to stderr.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/wirrockendigital/We.Rock.DiskMap/tests/scale"
)

// main never creates source files; shell redirection selects the caller's explicit output.
func main() {
	count := flag.Int("count", 100000, "Number of synthetic metadata nodes (1000..10000000)")
	seed := flag.Uint64("seed", 42, "Deterministic seed (0..65535)")
	shape := flag.String("shape", "wide", "wide, deep or skewed")
	verify := flag.Bool("verify", false, "Verify NDJSON from stdin instead of generating")
	flag.Parse()
	c := scale.Config{Count: *count, Seed: *seed, Shape: *shape}
	var m scale.Manifest
	var err error
	if *verify {
		m, err = scale.Verify(os.Stdin, c)
	} else {
		m, err = scale.Generate(os.Stdout, c)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stderr).Encode(m); err != nil {
		os.Exit(1)
	}
}
