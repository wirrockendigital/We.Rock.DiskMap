//go:build race

package indexbench

import "time"

// Instrumented aggregation are correctness checks, not comparable performance measurements.
const qualificationTimeout = 5 * time.Minute
const qualificationInstrumentation = "race"
