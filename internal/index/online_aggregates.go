package index

import (
	"context"
	"database/sql"
	"encoding/json"
	"math/big"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
)

// OnlineAggregateWriter folds a depth-first stream using only the open directory ancestry.
// The caller owns the transaction and must roll it back after any writer error.
type OnlineAggregateWriter struct {
	ctx     context.Context
	insert  *sql.Stmt
	mapping map[string]string
	stack   []onlineDirectory
	lastID  int64
	failure error
	closed  bool
}
type onlineDirectory struct {
	id                  int64
	direct, descendants contentAccumulator
}

// NewOnlineAggregates records the canonical mapping once in an otherwise empty projection.
func NewOnlineAggregates(ctx context.Context, tx *sql.Tx, mapping contracts.FileTypeMapping) (*OnlineAggregateWriter, error) {
	canonical, err := CanonicalFileTypeMapping(mapping)
	if err != nil {
		return nil, err
	}
	var existing int
	if err = tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM directory_aggregates)+(SELECT count(*) FROM aggregate_mapping)`).Scan(&existing); err != nil {
		return nil, err
	}
	if existing != 0 {
		return nil, ErrGeneration
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO aggregate_mapping VALUES(1,?)`, string(encoded)); err != nil {
		return nil, err
	}
	statement, err := tx.PrepareContext(ctx, `INSERT INTO directory_aggregates(node_id,aggregate_json) VALUES(?,?)`)
	if err != nil {
		return nil, err
	}
	writer := &OnlineAggregateWriter{ctx: ctx, insert: statement, mapping: map[string]string{}}
	for _, assignment := range canonical.Assignments {
		writer.mapping[assignment.Extension] = assignment.Category
	}
	return writer, nil
}

// fail preserves the first error so a partial stream can never resume and look complete.
func (w *OnlineAggregateWriter) fail(err error) error {
	if w.failure == nil {
		w.failure = err
	}
	return w.failure
}
func (w *OnlineAggregateWriter) ready() error {
	if w.failure != nil {
		return w.failure
	}
	if w.closed {
		return w.fail(ErrGeneration)
	}
	if err := w.ctx.Err(); err != nil {
		return w.fail(err)
	}
	return nil
}

// Observe accepts exactly one inserted node, in strictly increasing DFS insertion order.
// A new root requires FinishRoot; returning to an already closed directory is invalid.
func (w *OnlineAggregateWriter) Observe(id, parentID int64, kind string, name []byte, logical, allocated *contracts.Bytes) error {
	if err := w.ready(); err != nil {
		return err
	}
	if id <= w.lastID || parentID < 0 || parentID >= id || contracts.ValidateNodeType(contracts.Node{Kind: kind, LogicalBytes: logical, AllocatedBytes: allocated}) != nil || logical != nil && logical.Validate() != nil || allocated != nil && allocated.Validate() != nil || kind == "directory" && (logical != nil || allocated != nil) {
		return w.fail(ErrGeneration)
	}
	if parentID == 0 {
		if len(w.stack) != 0 || kind != "directory" {
			return w.fail(ErrGeneration)
		}
	} else {
		parentIndex := -1
		for i := len(w.stack) - 1; i >= 0; i-- {
			if w.stack[i].id == parentID {
				parentIndex = i
				break
			}
		}
		if parentIndex < 0 {
			return w.fail(ErrGeneration)
		}
		for len(w.stack) > parentIndex+1 {
			if err := w.finishDirectory(); err != nil {
				return err
			}
		}
	}
	w.lastID = id
	if kind == "directory" {
		// Root plus 256 nested directories is the generation's fixed ancestry ceiling.
		if len(w.stack) >= 257 {
			return w.fail(ErrGeneration)
		}
		if len(w.stack) > 0 {
			a := &w.stack[len(w.stack)-1]
			a.direct.directories.Add(&a.direct.directories, big.NewInt(1))
		}
		w.stack = append(w.stack, onlineDirectory{id: id})
		return nil
	}
	if len(w.stack) == 0 {
		return w.fail(ErrGeneration)
	}
	parent := &w.stack[len(w.stack)-1]
	if kind == "file" {
		if err := parent.direct.file(classifyName(name, w.mapping), logical, allocated); err != nil {
			return w.fail(err)
		}
	} else {
		parent.direct.special.Add(&parent.direct.special, big.NewInt(1))
	}
	return nil
}

// finishDirectory persists one complete vector before transferring it to its parent.
func (w *OnlineAggregateWriter) finishDirectory() error {
	if err := w.ready(); err != nil {
		return err
	}
	top := &w.stack[len(w.stack)-1]
	direct := top.direct.content()
	top.descendants.add(direct)
	value := contracts.DirectoryAggregate{Direct: direct, Subtree: top.descendants.content()}
	if !validContent(value.Direct) || !validContent(value.Subtree) {
		return w.fail(ErrGeneration)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return w.fail(err)
	}
	if _, err = w.insert.ExecContext(w.ctx, top.id, string(encoded)); err != nil {
		return w.fail(err)
	}
	w.stack[len(w.stack)-1] = onlineDirectory{}
	w.stack = w.stack[:len(w.stack)-1]
	if len(w.stack) > 0 {
		w.stack[len(w.stack)-1].descendants.add(value.Subtree)
	}
	return nil
}

// FinishRoot closes the current DFS root without committing the caller's transaction.
func (w *OnlineAggregateWriter) FinishRoot() error {
	if err := w.ready(); err != nil {
		return err
	}
	for len(w.stack) > 0 {
		if err := w.finishDirectory(); err != nil {
			return err
		}
	}
	return nil
}

// Close releases prepared SQL only; unfinished directories are deliberately not published.
func (w *OnlineAggregateWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	return w.insert.Close()
}
