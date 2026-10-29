package query

import (
	"context"
	"database/sql"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/index"
	"time"
)

// projectedDetailAggregate uses the same bounded partition as hierarchy, including directory self counts.
func projectedDetailAggregate(ctx context.Context, db *sql.DB, h index.GenerationHeader, id string) (contracts.ContentAggregate, error) {
	e := &engine{ctx: ctx, db: db, h: h, r: Request{SnapshotID: h.SnapshotID, Metric: "logical", Filter: Filter{Categories: []string{}}}, now: time.Now}
	if err := e.topology(); err != nil {
		return contracts.ContentAggregate{}, err
	}
	var sum func(string) (contracts.ContentAggregate, error)
	sum = func(id string) (contracts.ContentAggregate, error) {
		out := contracts.ContentAggregate{FileCount: "0", DirectoryCount: "0", SpecialCount: "0", Logical: contracts.MetricAggregate{KnownBytes: "0", UnknownFileCount: "0"}, Allocated: contracts.MetricAggregate{KnownBytes: "0", UnknownFileCount: "0"}, Types: map[string]contracts.TypeAggregate{}}
		if children, ok := e.virtual[id]; ok {
			for _, child := range children {
				v, err := sum(child.node.ID)
				if err != nil {
					return out, err
				}
				if err = mergeDetailContent(&out, v); err != nil {
					return out, err
				}
			}
			return out, nil
		}
		a, err := index.ReadDirectoryAggregate(ctx, db, id)
		if err != nil {
			return out, err
		}
		out = a.Subtree
		out.DirectoryCount, err = arithmetic(out.DirectoryCount, "1", false)
		if err != nil {
			return out, err
		}
		for excluded := range e.excluded[id] {
			a, err := index.ReadDirectoryAggregate(ctx, db, excluded)
			if err != nil {
				return out, err
			}
			v := a.Subtree
			v.DirectoryCount, err = arithmetic(v.DirectoryCount, "1", false)
			if err != nil {
				return out, err
			}
			if err = combineDetailContent(&out, v, true); err != nil {
				return out, err
			}
		}
		return out, nil
	}
	return sum(id)
}
