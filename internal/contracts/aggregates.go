package contracts

// MetricAggregate keeps known sums separate from unavailable regular-file values.
type MetricAggregate struct {
	KnownBytes       Bytes `json:"knownBytes"`
	UnknownFileCount Bytes `json:"unknownFileCount"`
}

// TypeAggregate describes regular path entries of one configured category.
type TypeAggregate struct {
	FileCount Bytes           `json:"fileCount"`
	Logical   MetricAggregate `json:"logical"`
	Allocated MetricAggregate `json:"allocated"`
}

// ContentAggregate excludes the directory itself; absent type keys denote zero vectors.
type ContentAggregate struct {
	FileCount      Bytes                    `json:"fileCount"`
	DirectoryCount Bytes                    `json:"directoryCount"`
	SpecialCount   Bytes                    `json:"specialCount"`
	Logical        MetricAggregate          `json:"logical"`
	Allocated      MetricAggregate          `json:"allocated"`
	Types          map[string]TypeAggregate `json:"types"`
}

// DirectoryAggregate separates immediate children from all descendants.
type DirectoryAggregate struct {
	Direct  ContentAggregate `json:"direct"`
	Subtree ContentAggregate `json:"subtree"`
}

// ExtensionMapping uses one suffix without a dot; ASCII is folded, Unicode is exact.
type ExtensionMapping struct {
	Extension string `json:"extension"`
	Category  string `json:"category"`
}

// FileTypeMapping is captured before traversal and never changes within a generation.
type FileTypeMapping struct {
	Version     int                `json:"version"`
	Revision    uint64             `json:"revision"`
	Assignments []ExtensionMapping `json:"assignments"`
}
