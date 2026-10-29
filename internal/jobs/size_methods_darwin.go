//go:build darwin

package jobs

// capturedSizeMethods never labels local Darwin fixtures as qualified native DSM captures.
func capturedSizeMethods() []string { return []string{"descriptor-no-follow-stat"} }
