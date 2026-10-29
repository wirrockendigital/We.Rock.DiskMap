package scanner

import (
	"fmt"
	"strings"
	"testing"
)

// Numeric parent tracking retains ancestry only, including sibling branch changes and wide file batches.
func TestScanParentStackBounded(t *testing.T) {
	stack := []scanParent{}
	if id, e := scanParentID(&stack, "."); e != nil || id != 0 {
		t.Fatal(id, e)
	}
	stack = append(stack, scanParent{".", 1})
	for i := 0; i < 10000; i++ {
		if id, e := scanParentID(&stack, fmt.Sprintf("file-%d", i)); e != nil || id != 1 || len(stack) != 1 {
			t.Fatal(id, e, len(stack))
		}
	}
	if id, e := scanParentID(&stack, "a"); e != nil || id != 1 {
		t.Fatal(id, e)
	}
	stack = append(stack, scanParent{"a", 2})
	if id, e := scanParentID(&stack, "a/sub"); e != nil || id != 2 {
		t.Fatal(id, e)
	}
	stack = append(stack, scanParent{"a/sub", 3})
	if id, e := scanParentID(&stack, "a/sub/file"); e != nil || id != 3 {
		t.Fatal(id, e)
	}
	if id, e := scanParentID(&stack, "b"); e != nil || id != 1 || len(stack) != 1 {
		t.Fatal(id, e, len(stack))
	}
	stack = append(stack, scanParent{"b", 4})
	if id, e := scanParentID(&stack, "b/file"); e != nil || id != 4 {
		t.Fatal(id, e)
	}
	if _, e := scanParentID(&stack, "missing/file"); e == nil {
		t.Fatal("foreign parent accepted")
	}
	if _, e := scanParentID(&stack, strings.Repeat("d/", TraversalMaxDepth)+"file"); e == nil {
		t.Fatal("unbounded depth accepted")
	}
}
