package index

import (
	"context"
	"database/sql"
	"errors"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	"modernc.org/sqlite"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Repeated directory entries are distinct from corrupt storage or invalid ancestry.
var ErrDuplicateEntry = errors.New("DUPLICATE_DIRECTORY_ENTRY")
var ErrChangedEntry = errors.New("DIRECTORY_ENTRY_CHANGED")

// NodeRecord reconstructs API metadata from one compact tree node and its qualified root.
type NodeRecord struct {
	Node           contracts.Node
	TargetID       string
	Name, Path     []byte
	ObjectIdentity string
	RowID          int64
}

// NodeWriter owns prepared inserts within the caller's atomic scan transaction.
type NodeWriter struct {
	ctx  context.Context
	tx   *sql.Tx
	stmt *sql.Stmt
}

func NewNodeWriter(ctx context.Context, tx *sql.Tx, snapshot string) (*NodeWriter, error) {
	var actual string
	if err := tx.QueryRowContext(ctx, `SELECT snapshot_id FROM generation_identity WHERE singleton=1`).Scan(&actual); err != nil {
		return nil, err
	}
	if actual != snapshot {
		return nil, ErrGeneration
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO nodes(parent_id,root_id,name_bytes,kind,logical_bytes,allocated_bytes,object_identity,link_count) VALUES(?,?,?,?,?,?,?,?)`)
	if err != nil {
		return nil, err
	}
	return &NodeWriter{ctx: ctx, tx: tx, stmt: stmt}, nil
}

// AddRoot stores source membership once, before any nodes for that source are written.
func (w *NodeWriter) AddRoot(target, volume string, share *string) (int64, error) {
	if !security.ValidID(target) || !security.ValidID(volume) || share != nil && *share != target {
		return 0, ErrGeneration
	}
	result, err := w.tx.ExecContext(w.ctx, `INSERT INTO roots(target_id,volume_id,share_id) VALUES(?,?,?)`, target, volume, share)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

// Insert allocates a generation-local numeric locator; parents must already exist.
func (w *NodeWriter) Insert(rootID, parentID int64, name []byte, kind string, logical, allocated *contracts.Bytes, identity string, links *int) (int64, error) {
	if rootID < 1 || parentID < 0 || len(name) == 0 || strings.ContainsAny(string(name), "/\x00") || parentID != 0 && (string(name) == "." || string(name) == "..") || logical != nil && logical.Validate() != nil || allocated != nil && allocated.Validate() != nil {
		return 0, ErrGeneration
	}
	if contracts.ValidateNodeType(contracts.Node{Kind: kind, LogicalBytes: logical, AllocatedBytes: allocated}) != nil || kind == "directory" && (logical != nil || allocated != nil) {
		return 0, ErrGeneration
	}
	var parent any
	if parentID != 0 {
		parent = parentID
	}
	result, err := w.stmt.ExecContext(w.ctx, parent, rootID, name, kind, logical, allocated, identity, links)
	if err != nil {
		// Query only a name conflict; the normal insertion path performs no extra lookup.
		var constraint *sqlite.Error
		if parentID != 0 && errors.As(err, &constraint) && constraint.Code() == 2067 {
			var existing int64
			var same bool
			queryErr := w.tx.QueryRowContext(w.ctx, `SELECT id,kind=? AND logical_bytes IS ? AND allocated_bytes IS ? AND object_identity=? AND link_count IS ? FROM nodes WHERE root_id=? AND parent_id=? AND name_bytes=?`, kind, logical, allocated, identity, links, rootID, parentID, name).Scan(&existing, &same)
			if queryErr == nil {
				if same {
					return existing, ErrDuplicateEntry
				}
				return existing, ErrChangedEntry
			}
		}
		return 0, err
	}
	return result.LastInsertId()
}

// Close releases only the statement; transaction lifetime remains with the scan owner.
func (w *NodeWriter) Close() error { return w.stmt.Close() }

// nodeDB supports the same native readers inside validation transactions and ordinary reader leases.
type nodeDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type rawNode struct {
	id, parent, root   int64
	name               []byte
	kind               string
	logical, allocated sql.NullString
	identity           string
	links              sql.NullInt64
}

const rawNodeSQL = `SELECT id,coalesce(parent_id,0),root_id,name_bytes,kind,logical_bytes,allocated_bytes,object_identity,link_count FROM nodes`

func scanRaw(row interface{ Scan(...any) error }) (rawNode, error) {
	var n rawNode
	err := row.Scan(&n.id, &n.parent, &n.root, &n.name, &n.kind, &n.logical, &n.allocated, &n.identity, &n.links)
	return n, err
}
func displayNodeBytes(value []byte) string {
	if utf8.Valid(value) {
		return string(value)
	}
	return strconv.QuoteToASCII(string(value))
}

// NumericNodeID rejects alternate encodings, overflow and cross-generation path hashes.
func NumericNodeID(id string) (int64, error) {
	v, e := strconv.ParseInt(id, 10, 64)
	if e != nil || v < 1 || strconv.FormatInt(v, 10) != id {
		return 0, ErrGeneration
	}
	return v, nil
}

// readNode bounds ancestry work and memory independently of tree size; parent IDs strictly decrease.
func readNode(ctx context.Context, db nodeDB, id string) (NodeRecord, error) {
	numeric, err := NumericNodeID(id)
	if err != nil {
		return NodeRecord{}, sql.ErrNoRows
	}
	n, err := scanRaw(db.QueryRowContext(ctx, rawNodeSQL+` WHERE id=?`, numeric))
	if err != nil {
		return NodeRecord{}, err
	}
	return recordNode(ctx, db, n)
}

// NodeReader retains at most 256 directory paths and one MiB of path bytes across pages.
// Clearing the bounded cache may add ancestry reads, but never changes qualification.
type NodeReader struct {
	ctx       context.Context
	db        nodeDB
	snapshot  string
	roots     map[int64]nodeRoot
	paths     map[int64]cachedPath
	pathBytes int
}
type nodeRoot struct {
	target, volume string
	share          sql.NullString
}
type cachedPath struct {
	path  []byte
	root  int64
	depth int
}

func newNodeReader(ctx context.Context, db nodeDB) (*NodeReader, error) {
	r := &NodeReader{ctx: ctx, db: db, roots: map[int64]nodeRoot{}, paths: map[int64]cachedPath{}}
	if err := db.QueryRowContext(ctx, `SELECT snapshot_id FROM generation_identity WHERE singleton=1`).Scan(&r.snapshot); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT id,target_id,volume_id,share_id FROM roots ORDER BY id LIMIT 1025`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var root nodeRoot
		if err = rows.Scan(&id, &root.target, &root.volume, &root.share); err != nil {
			return nil, err
		}
		if id < 1 || !security.ValidID(root.target) || !security.ValidID(root.volume) || root.share.Valid && root.share.String != root.target {
			return nil, ErrGeneration
		}
		r.roots[id] = root
	}
	if len(r.roots) > 1024 {
		return nil, ErrGeneration
	}
	return r, rows.Err()
}

// NewNodeReader binds a reusable, bounded reconstruction cache to one immutable generation.
func NewNodeReader(ctx context.Context, db *sql.DB) (*NodeReader, error) {
	return newNodeReader(ctx, db)
}
func (r *NodeReader) remember(id int64, p cachedPath) {
	if len(p.path) > 1<<20 {
		return
	}
	if len(r.paths) >= 256 || r.pathBytes+len(p.path) > 1<<20 {
		r.paths = map[int64]cachedPath{}
		r.pathBytes = 0
	}
	if _, ok := r.paths[id]; !ok {
		r.paths[id] = p
		r.pathBytes += len(p.path)
	}
}
func recordNode(ctx context.Context, db nodeDB, n rawNode) (NodeRecord, error) {
	r, e := newNodeReader(ctx, db)
	if e != nil {
		return NodeRecord{}, e
	}
	return r.record(n)
}
func (r *NodeReader) record(n rawNode) (NodeRecord, error) {
	out := NodeRecord{RowID: n.id, Name: n.name, ObjectIdentity: n.identity}
	root, ok := r.roots[n.root]
	if !ok {
		return out, ErrGeneration
	}
	out.TargetID = root.target
	chain := make([]rawNode, 0, 16)
	current := n
	var base cachedPath
	for depth := 0; ; depth++ {
		if depth > 257 || current.root != n.root || current.id < 1 || current.parent < 0 || current.parent >= current.id {
			return out, ErrGeneration
		}
		if p, ok := r.paths[current.id]; ok {
			if p.root != n.root {
				return out, ErrGeneration
			}
			base = p
			break
		}
		if current.parent == 0 {
			if current.kind != "directory" {
				return out, ErrGeneration
			}
			base = cachedPath{path: []byte("."), root: n.root}
			r.remember(current.id, base)
			break
		}
		if len(current.name) == 0 || strings.ContainsAny(string(current.name), "/\x00") || string(current.name) == "." || string(current.name) == ".." {
			return out, ErrGeneration
		}
		chain = append(chain, current)
		// A cached parent directory avoids even one SQL read for ordinary sibling files.
		if p, ok := r.paths[current.parent]; ok {
			if p.root != n.root {
				return out, ErrGeneration
			}
			base = p
			break
		}
		var err error
		current, err = scanRaw(r.db.QueryRowContext(r.ctx, rawNodeSQL+` WHERE id=?`, current.parent))
		if err != nil {
			return out, err
		}
		if current.kind != "directory" {
			return out, ErrGeneration
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		v := chain[i]
		base.depth++
		// A terminal file may follow the maximum directory ancestry.
		if base.depth > 257 || (v.kind == "directory" && base.depth > 256) {
			return out, ErrGeneration
		}
		prefix := base.path
		if string(prefix) == "." {
			prefix = nil
		}
		next := make([]byte, 0, len(prefix)+len(v.name)+1)
		next = append(next, prefix...)
		if len(next) > 0 {
			next = append(next, '/')
		}
		next = append(next, v.name...)
		base.path = next
		if v.kind == "directory" {
			r.remember(v.id, base)
		}
	}
	out.Path = append([]byte(nil), base.path...)
	out.Node = contracts.Node{SnapshotID: r.snapshot, ID: strconv.FormatInt(n.id, 10), OriginalName: displayNodeBytes(n.name), RelativePath: displayNodeBytes(out.Path), Kind: n.kind, VolumeID: root.volume}
	if n.parent != 0 {
		v := strconv.FormatInt(n.parent, 10)
		out.Node.ParentID = &v
	}
	if root.share.Valid {
		out.Node.ShareID = &root.share.String
	}
	if n.logical.Valid {
		v := contracts.Bytes(n.logical.String)
		if v.Validate() != nil {
			return out, ErrGeneration
		}
		out.Node.LogicalBytes = &v
	}
	if n.allocated.Valid {
		v := contracts.Bytes(n.allocated.String)
		if v.Validate() != nil {
			return out, ErrGeneration
		}
		out.Node.AllocatedBytes = &v
	}
	if n.links.Valid {
		v := int(n.links.Int64)
		out.Node.LinkCount = &v
	}
	if security.ValidateNode(out.Node, r.snapshot, out.Node.ID, out.TargetID, root.volume, string(out.Path)) != nil {
		return out, ErrGeneration
	}
	return out, nil
}

// ReadNode resolves one snapshot-local locator without storing a full path.
func ReadNode(ctx context.Context, db *sql.DB, id string) (NodeRecord, error) {
	return readNode(ctx, db, id)
}

// FindNode walks indexed child names; it never scans an entire tree or follows filesystem paths.
func FindNode(ctx context.Context, db *sql.DB, target string, path []byte) (NodeRecord, error) {
	return findNode(ctx, db, target, path)
}
func findNode(ctx context.Context, db nodeDB, target string, path []byte) (NodeRecord, error) {
	if security.RelativeBytes(string(path)) != nil {
		return NodeRecord{}, ErrGeneration
	}
	n, err := scanRaw(db.QueryRowContext(ctx, rawNodeSQL+` WHERE root_id=(SELECT id FROM roots WHERE target_id=?) AND parent_id IS NULL`, target))
	if err != nil {
		return NodeRecord{}, err
	}
	if string(path) != "." {
		parts := strings.Split(string(path), "/")
		if len(parts) > 257 {
			return NodeRecord{}, ErrGeneration
		}
		for _, name := range parts {
			n, err = scanRaw(db.QueryRowContext(ctx, rawNodeSQL+` WHERE parent_id=? AND name_bytes=?`, n.id, []byte(name)))
			if err != nil {
				return NodeRecord{}, err
			}
		}
	}
	return recordNode(ctx, db, n)
}

// nodeList closes its bounded SQL cursor before reconstructing parent chains on the same connection.
func (r *NodeReader) list(where string, args ...any) ([]NodeRecord, error) {
	rows, err := r.db.QueryContext(r.ctx, rawNodeSQL+where, args...)
	if err != nil {
		return nil, err
	}
	batch := []rawNode{}
	for rows.Next() {
		n, e := scanRaw(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		batch = append(batch, n)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := make([]NodeRecord, 0, len(batch))
	for _, n := range batch {
		v, e := r.record(n)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, nil
}
func nodeList(ctx context.Context, db *sql.DB, where string, args ...any) ([]NodeRecord, error) {
	r, e := NewNodeReader(ctx, db)
	if e != nil {
		return nil, e
	}
	return r.list(where, args...)
}

// Page resumes a bounded raw-node scan while reusing directory ancestry across pages.
func (r *NodeReader) Page(after int64, limit int) ([]NodeRecord, error) {
	if after < 0 || limit < 1 || limit > 4096 {
		return nil, ErrGeneration
	}
	return r.list(` WHERE id>? ORDER BY id LIMIT ?`, after, limit)
}

// Read fetches one node using the same bounded ancestor cache.
func (r *NodeReader) Read(id string) (NodeRecord, error) {
	v, e := NumericNodeID(id)
	if e != nil {
		return NodeRecord{}, sql.ErrNoRows
	}
	n, e := scanRaw(r.db.QueryRowContext(r.ctx, rawNodeSQL+` WHERE id=?`, v))
	if e != nil {
		return NodeRecord{}, e
	}
	return r.record(n)
}

// Children uses keyset pagination with a hard page limit.
func Children(ctx context.Context, db *sql.DB, parent string, after int64, limit int) ([]NodeRecord, error) {
	id, e := NumericNodeID(parent)
	if e != nil || limit < 1 || limit > 4096 {
		return nil, ErrGeneration
	}
	return nodeList(ctx, db, ` WHERE parent_id=? AND id>? ORDER BY id LIMIT ?`, id, after, limit)
}

// RootNodes has the same fixed source-root ceiling as the manifest contract.
func RootNodes(ctx context.Context, db *sql.DB) ([]NodeRecord, error) {
	out, e := nodeList(ctx, db, ` WHERE parent_id IS NULL ORDER BY id LIMIT 1025`)
	if len(out) > 1024 {
		return nil, ErrGeneration
	}
	return out, e
}

// WalkNodes streams bounded pages; callback errors and cancellation stop further reads immediately.
func WalkNodes(ctx context.Context, db *sql.DB, visit func(NodeRecord) error) error {
	r, e := NewNodeReader(ctx, db)
	if e != nil {
		return e
	}
	var after int64
	for {
		batch, e := r.Page(after, 128)
		if e != nil {
			return e
		}
		if len(batch) == 0 {
			return nil
		}
		for _, v := range batch {
			if e = ctx.Err(); e != nil {
				return e
			}
			if e = visit(v); e != nil {
				return e
			}
			after = v.RowID
		}
	}
}

// IsNodeNotFound distinguishes absent paths from malformed tree failures.
func IsNodeNotFound(err error) bool { return errors.Is(err, sql.ErrNoRows) }
