package scanner

import (
	"context"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// Preview is an unvalidated, bounded observation; its IDs never authorize node actions.
type Preview struct {
	Sequence          uint64           `json:"sequence"`
	CapturedAt        string           `json:"capturedAt"`
	Entries           string           `json:"entries"`
	KnownLogicalBytes string           `json:"knownLogicalBytes"`
	Segments          []PreviewSegment `json:"segments"`
	Truncated         bool             `json:"truncated"`
}

// PreviewSegment contains only known captured bytes, not an inferred complete subtree size.
type PreviewSegment struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Bytes string `json:"bytes"`
}
type previewContextKey struct{}

// WithPreview installs a synchronous RAM-only observer; callers must not block on persistence.
func WithPreview(ctx context.Context, observer func(Preview)) context.Context {
	return context.WithValue(ctx, previewContextKey{}, observer)
}

type previewAccumulator struct {
	observer          func(Preview)
	segments          []PreviewSegment
	positions         map[string]int
	sums              []*big.Int
	total             big.Int
	entries, sequence uint64
	last              time.Time
	truncated         bool
	remainder         int
}

// newPreview allocates no tracking state when the caller did not request a preview.
func newPreview(ctx context.Context) *previewAccumulator {
	observer, _ := ctx.Value(previewContextKey{}).(func(Preview))
	if observer == nil {
		return nil
	}
	return &previewAccumulator{observer: observer, positions: make(map[string]int), remainder: -1}
}

// previewText bounds UTF-8 presentation bytes without modifying source path identities.
func previewText(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

// observe partitions files by captured target and first relative component without retaining all paths.
func (p *previewAccumulator) observe(target, relative, kind string, logical *contracts.Bytes) {
	if p == nil {
		return
	}
	p.entries++
	if kind == "file" && logical != nil {
		size, ok := new(big.Int).SetString(string(*logical), 10)
		if ok {
			p.total.Add(&p.total, size)
			first, _, nested := strings.Cut(relative, "/")
			key := target + "\x00" + first
			position, exists := p.positions[key]
			if !exists {
				if len(key) <= 1024 && len(p.positions) < 63 {
					position = len(p.segments)
					p.positions[key] = position
					segmentKind := "file"
					if nested {
						segmentKind = "group"
					}
					p.segments = append(p.segments, PreviewSegment{ID: "preview-" + strconv.Itoa(position+1), Name: previewText(first, 256), Path: previewText(target+"/"+first, 768), Kind: segmentKind})
					p.sums = append(p.sums, new(big.Int))
				} else {
					p.truncated = true
					position = p.remainder
					if position < 0 {
						position = len(p.segments)
						p.remainder = position
						p.segments = append(p.segments, PreviewSegment{ID: "preview-remainder", Name: "Weitere erfasste Dateien", Path: "", Kind: "group"})
						p.sums = append(p.sums, new(big.Int))
					}
				}
			}
			p.sums[position].Add(p.sums[position], size)
		}
	}
	p.emit(false)
}

// emit copies the bounded DTO so later scan progress cannot mutate a delivered observation.
func (p *previewAccumulator) emit(final bool) {
	if p == nil {
		return
	}
	now := time.Now()
	if !final && !p.last.IsZero() && now.Sub(p.last) < time.Second {
		return
	}
	p.last = now
	p.sequence++
	segments := append([]PreviewSegment{}, p.segments...)
	for i := range segments {
		segments[i].Bytes = p.sums[i].String()
	}
	p.observer(Preview{Sequence: p.sequence, CapturedAt: now.UTC().Format(time.RFC3339Nano), Entries: strconv.FormatUint(p.entries, 10), KnownLogicalBytes: p.total.String(), Segments: segments, Truncated: p.truncated})
}
