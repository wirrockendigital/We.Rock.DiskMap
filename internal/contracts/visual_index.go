package contracts

// VisualIndexNode is numeric snapshot data passed from read-only queries to the derived-index writer.
// Kind uses the immutable format values: file=1, directory=2, special=3, virtual=4.
type VisualIndexNode struct {
	ID, Parent  string
	Source      uint64
	Kind, Flags uint8
	Bytes       string
	Unknown     bool
}
