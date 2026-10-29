package scanner

import (
	"errors"
	"path"
	"strings"
)

// scanParent holds only one directory in the current descriptor-qualified traversal ancestry.
type scanParent struct {
	relative string
	id       int64
}

// scanParentID drops completed branches and rejects missing ancestry instead of guessing a parent.
func scanParentID(stack *[]scanParent, relative string) (int64, error) {
	if relative == "." {
		if len(*stack) != 0 {
			return 0, errors.New("INVALID_SCAN_ANCESTRY")
		}
		return 0, nil
	}
	depth := strings.Count(relative, "/") + 1
	if depth > TraversalMaxDepth || len(*stack) < depth {
		return 0, errors.New("INVALID_SCAN_ANCESTRY")
	}
	*stack = (*stack)[:depth]
	parent := (*stack)[depth-1]
	if parent.relative != path.Dir(relative) || parent.id <= 0 {
		return 0, errors.New("INVALID_SCAN_ANCESTRY")
	}
	return parent.id, nil
}
