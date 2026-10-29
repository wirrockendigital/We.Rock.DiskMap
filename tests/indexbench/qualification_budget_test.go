//go:build !race

package indexbench

import "time"

// Preserve the existing uninstrumented cooperative qualification deadline.
const qualificationTimeout = 60 * time.Second
const qualificationInstrumentation = "none"
