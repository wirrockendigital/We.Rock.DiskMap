package scanner

// Failure identifies a fixed scanner operation without retaining source paths.
// The wrapped cause remains available for cancellation and storage classification.
type Failure struct {
	Operation string
	Cause     error
}

func (e *Failure) Error() string { return "SCAN_" + e.Operation + "_FAILED" }
func (e *Failure) Unwrap() error { return e.Cause }
