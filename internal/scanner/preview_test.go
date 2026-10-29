package scanner

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// Preview accounting is disjoint, bounded and preserves immutable delivered DTOs.
func TestPreviewBoundedKnownBytes(t *testing.T) {
	var received []Preview
	p := newPreview(WithPreview(context.Background(), func(v Preview) { received = append(received, v) }))
	size := contracts.Bytes("9007199254740993")
	p.observe("a", "folder/one", "file", &size)
	p.observe("a", "folder/two", "file", &size)
	p.observe("a", "unknown", "file", nil)
	p.observe("a", "special", "symlink", nil)
	for i := 0; i < 70; i++ {
		p.observe("b", fmt.Sprintf("f%d", i), "file", &size)
	}
	p.emit(true)
	final := received[len(received)-1]
	if len(received) != 2 || final.Entries != "74" || final.KnownLogicalBytes != "648518346341351496" || len(final.Segments) != 64 || !final.Truncated {
		t.Fatal(final, len(received))
	}
	if received[0].Segments[0].Bytes != string(size) || final.Segments[0].Bytes != "18014398509481986" {
		t.Fatal("observation mutated or subtree counted twice")
	}
	if final.Segments[0].Path != "a/folder" {
		t.Fatal("target missing from display path")
	}
	if final.Segments[63].Kind != "group" || final.Segments[63].Path != "" {
		t.Fatal("remainder invalid")
	}
}

// Separate targets cannot collide, and malformed source bytes never produce invalid JSON strings.
func TestPreviewTargetAndTextBounds(t *testing.T) {
	var final Preview
	p := newPreview(WithPreview(context.Background(), func(v Preview) { final = v }))
	size := contracts.Bytes("1")
	name := strings.Repeat("界", 250) + "\xff"
	p.observe("a", name, "file", &size)
	p.observe("b", name, "file", &size)
	p.emit(true)
	if len(final.Segments) != 2 || final.Segments[0].ID == final.Segments[1].ID {
		t.Fatal("target identity lost")
	}
	for _, s := range final.Segments {
		if len(s.Name) > 256 || len(s.Path) > 768 || !utf8.ValidString(s.Name) || !utf8.ValidString(s.Path) {
			t.Fatal("unbounded display")
		}
	}
}

// An oversized first key enters the remainder safely before any ordinary group exists.
func TestPreviewOversizedFirstKey(t *testing.T) {
	var last Preview
	p := newPreview(WithPreview(context.Background(), func(v Preview) { last = v }))
	size := contracts.Bytes("3")
	p.observe("target", strings.Repeat("x", 1025), "file", &size)
	p.observe("target", "normal", "file", &size)
	p.emit(true)
	if len(last.Segments) != 2 || last.Segments[0].ID != "preview-remainder" || last.KnownLogicalBytes != "6" || !last.Truncated {
		t.Fatal(last)
	}
}
