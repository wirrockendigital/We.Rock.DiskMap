package mapindex

import (
	"context"
	"database/sql"
	"encoding/binary"
	"github.com/wirrockendigital/We.Rock.DiskMap/internal/contracts"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Input is one already-qualified source node; parents must precede their children.
type Input struct {
	ID, Parent  string
	Source      uint64
	Kind, Flags uint8
	Bytes       [4]Size
	Unknown     bool
}

// Builder uses SQLite's disk-backed ordering and a disk queue instead of a wide-directory heap.
type Builder struct {
	ctx            context.Context
	db             *sql.DB
	tx             *sql.Tx
	insert, lookup *sql.Stmt
	file           *os.File
	pages          *packPages
	count          uint64
	lastParent     string
	lastSlot       uint64
	lastRecord     Record
	dir            string
}

// NewBuilder requires a fresh private directory owned by the publication caller.
func NewBuilder(ctx context.Context, dir string) (*Builder, error) {
	f, err := os.OpenFile(filepath.Join(dir, "tree.bin"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	b := &Builder{ctx: ctx, file: f, dir: dir}
	fail := func(err error) (*Builder, error) { b.Close(); return nil, err }
	if err = f.Truncate(HeaderBytes + RecordBytes); err != nil {
		return fail(err)
	}
	// Fixed dirty pages combine tiny record updates into bounded disk writes.
	b.pages = newPackPages(f, 4096)
	b.db, err = sql.Open("sqlite", filepath.Join(dir, "lookup.sqlite"))
	if err != nil {
		return fail(err)
	}
	b.db.SetMaxOpenConns(1)
	if _, err = b.db.ExecContext(ctx, `PRAGMA journal_mode=DELETE; PRAGMA synchronous=OFF; PRAGMA temp_store=FILE; PRAGMA cache_size=-8192;
CREATE TABLE nodes(id TEXT PRIMARY KEY,slot INTEGER NOT NULL UNIQUE,parent INTEGER NOT NULL,kind INTEGER NOT NULL,total TEXT NOT NULL,total_len INTEGER NOT NULL);
CREATE INDEX children ON nodes(parent,total_len,total,slot);`); err != nil {
		return fail(err)
	}
	if err = os.Chmod(filepath.Join(dir, "lookup.sqlite"), 0600); err != nil {
		return fail(err)
	}
	b.tx, err = b.db.BeginTx(ctx, nil)
	if err != nil {
		return fail(err)
	}
	b.insert, err = b.tx.PrepareContext(ctx, `INSERT INTO nodes VALUES(?,?,?,?,?,?)`)
	if err != nil {
		return fail(err)
	}
	b.lookup, err = b.tx.PrepareContext(ctx, `SELECT slot FROM nodes WHERE id=?`)
	if err != nil {
		return fail(err)
	}
	return b, nil
}

// Close abandons an unpublished build without deleting caller-owned paths.
func (b *Builder) Close() error {
	if b.insert != nil {
		b.insert.Close()
	}
	if b.lookup != nil {
		b.lookup.Close()
	}
	if b.tx != nil {
		b.tx.Rollback()
		b.tx = nil
	}
	if b.db != nil {
		b.db.Close()
		b.db = nil
	}
	if b.file != nil {
		err := b.file.Close()
		b.file = nil
		return err
	}
	return nil
}

// read accesses a fixed record without retaining the tree in memory.
func (b *Builder) read(slot uint64) (Record, error) {
	if slot == 0 || slot > b.count {
		return Record{}, ErrInvalid
	}
	var raw [RecordBytes]byte
	if err := b.pages.read(raw[:], HeaderBytes+int64(slot)*RecordBytes); err != nil {
		return Record{}, err
	}
	return Decode(raw[:])
}

// write updates only the private staging artifact.
func (b *Builder) write(slot uint64, r Record) error {
	var raw [RecordBytes]byte
	r.Encode(raw[:])
	return b.pages.write(raw[:], HeaderBytes+int64(slot)*RecordBytes)
}

// append assigns dense slots independently of sparse source IDs.
func (b *Builder) append(r Record) (uint64, error) {
	b.count++
	if b.count > uint64((1<<63-HeaderBytes)/RecordBytes) {
		return 0, ErrInvalid
	}
	return b.count, b.write(b.count, r)
}

// Add preserves source identity and inherits category membership from the real parent.
func (b *Builder) Add(in Input) error {
	if err := b.ctx.Err(); err != nil {
		return err
	}
	if in.ID == "" || in.Kind < File || in.Kind > Virtual || in.Flags > 3 {
		return ErrInvalid
	}
	r := Record{Source: in.Source, Kind: in.Kind, Flags: in.Flags, Bytes: in.Bytes}
	if in.Parent != "" {
		if in.Parent != b.lastParent {
			var slot uint64
			if err := b.lookup.QueryRowContext(b.ctx, in.Parent).Scan(&slot); err != nil {
				return ErrInvalid
			}
			p, err := b.read(slot)
			if err != nil {
				return err
			}
			b.lastParent, b.lastSlot, b.lastRecord = in.Parent, slot, p
		}
		p := b.lastRecord
		if p.Kind != Directory && p.Kind != Virtual || p.Depth >= 512 {
			return ErrInvalid
		}
		r.Parent = b.lastSlot
		r.Flags |= p.Flags
		r.Depth = p.Depth + 1
	}
	r.Presence = r.Flags
	r.MaxDepth = r.Depth
	if r.Kind == File {
		r.Files = 1
		if in.Unknown {
			r.Unknown = 1
		}
		var total Size
		for _, v := range r.Bytes {
			var err error
			total, err = total.Add(v)
			if err != nil {
				return err
			}
		}
		r.Bytes = [4]Size{}
		r.Bytes[r.Flags] = total
	}
	total, err := r.Total()
	if err != nil {
		return err
	}
	slot, err := b.append(r)
	if err != nil {
		return err
	}
	_, err = b.insert.ExecContext(b.ctx, in.ID, slot, r.Parent, r.Kind, total.String(), len(total.String()))
	return err
}

type weightedSlot struct {
	slot   uint64
	weight Size
}

// diskQueue is monotonic because merging the two least weights produces a sorted stream.
type diskQueue struct {
	f               *os.File
	readAt, writeAt int64
}

// push appends a bounded queue record; no directory-width allocation is needed.
func (q *diskQueue) push(v weightedSlot) error {
	var raw [24]byte
	binary.LittleEndian.PutUint64(raw[:], v.slot)
	binary.LittleEndian.PutUint64(raw[8:], v.weight.Lo)
	binary.LittleEndian.PutUint64(raw[16:], v.weight.Hi)
	_, err := q.f.WriteAt(raw[:], q.writeAt)
	q.writeAt += 24
	return err
}

// peek leaves the queue cursor unchanged until the selected head is consumed.
func (q *diskQueue) peek() (weightedSlot, bool, error) {
	if q.readAt == q.writeAt {
		return weightedSlot{}, false, nil
	}
	var raw [24]byte
	if _, err := q.f.ReadAt(raw[:], q.readAt); err != nil {
		return weightedSlot{}, false, err
	}
	return weightedSlot{binary.LittleEndian.Uint64(raw[:]), Size{binary.LittleEndian.Uint64(raw[8:]), binary.LittleEndian.Uint64(raw[16:])}}, true, nil
}

// balance combines sorted real children and generated compounds in linear time after SQL sorting.
func (b *Builder) balance(parent uint64, q *diskQueue) error {
	if err := q.f.Truncate(0); err != nil {
		return err
	}
	q.readAt, q.writeAt = 0, 0
	rows, err := b.tx.QueryContext(b.ctx, `SELECT slot,total FROM nodes WHERE parent=? ORDER BY total_len,total,slot`, parent)
	if err != nil {
		return err
	}
	var head weightedSlot
	present := false
	advance := func() error {
		present = rows.Next()
		if !present {
			return rows.Err()
		}
		var s string
		if err := rows.Scan(&head.slot, &s); err != nil {
			return err
		}
		var e error
		head.weight, e = ParseSize(s)
		return e
	}
	if err = advance(); err != nil {
		rows.Close()
		return err
	}
	pop := func() (weightedSlot, bool, error) {
		v, ok, e := q.peek()
		if e != nil {
			return v, false, e
		}
		if present && (!ok || head.weight.Less(v.weight) || head.weight == v.weight && head.slot < v.slot) {
			v = head
			e = advance()
			return v, true, e
		}
		if ok {
			q.readAt += 24
		}
		return v, ok, nil
	}
	var root uint64
	for {
		if err = b.ctx.Err(); err != nil {
			break
		}
		a, ok, e := pop()
		if e != nil {
			err = e
			break
		}
		if !ok {
			break
		}
		c, ok, e := pop()
		if e != nil {
			err = e
			break
		}
		if !ok {
			root = a.slot
			break
		}
		x, e := b.read(a.slot)
		if e != nil {
			err = e
			break
		}
		y, e := b.read(c.slot)
		if e != nil {
			err = e
			break
		}
		r := Record{Parent: parent, Left: a.slot, Right: c.slot, Flags: x.Flags & y.Flags, Presence: x.Presence | y.Presence, MaxDepth: max(x.MaxDepth, y.MaxDepth)}
		if ^uint64(0)-x.Files < y.Files || ^uint64(0)-x.Unknown < y.Unknown {
			err = ErrInvalid
			break
		}
		r.Files = x.Files + y.Files
		r.Unknown = x.Unknown + y.Unknown
		for k := range r.Bytes {
			r.Bytes[k], err = x.Bytes[k].Add(y.Bytes[k])
			if err != nil {
				break
			}
		}
		if err != nil {
			break
		}
		slot, e := b.append(r)
		if e != nil {
			err = e
			break
		}
		w, e := a.weight.Add(c.weight)
		if e != nil {
			err = e
			break
		}
		if err = q.push(weightedSlot{slot, w}); err != nil {
			break
		}
	}
	rows.Close()
	if err != nil {
		return err
	}
	r, err := b.read(parent)
	if err != nil {
		return err
	}
	r.Left = root
	if root != 0 {
		child, e := b.read(root)
		if e != nil {
			return e
		}
		r.Presence |= child.Presence
		r.Bytes = child.Bytes
		r.Files = child.Files
		r.Unknown = child.Unknown
		r.MaxDepth = child.MaxDepth
	}
	if err = b.write(parent, r); err != nil {
		return err
	}
	w, err := r.Total()
	if err != nil {
		return err
	}
	_, err = b.tx.ExecContext(b.ctx, `UPDATE nodes SET total=?,total_len=? WHERE slot=?`, w.String(), len(w.String()), parent)
	return err
}

// Finish seals all ancestors bottom-up and writes the immutable manifest header last.
func (b *Builder) Finish() error {
	qf, err := os.OpenFile(filepath.Join(b.dir, "balance.tmp"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() { qf.Close(); os.Remove(filepath.Join(b.dir, "balance.tmp")) }()
	q := &diskQueue{f: qf}
	// Keyset iteration permits writes on the same transaction without retaining directory IDs.
	after := b.count + 1
	for {
		var slot uint64
		err = b.tx.QueryRowContext(b.ctx, `SELECT slot FROM nodes WHERE slot<? AND kind IN (?,?) ORDER BY slot DESC LIMIT 1`, after, Directory, Virtual).Scan(&slot)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return err
		}
		if err = b.balance(slot, q); err != nil {
			return err
		}
		after = slot
	}
	if err = b.tx.Commit(); err != nil {
		return err
	}
	b.tx = nil
	if err = b.pages.flush(); err != nil {
		return err
	}
	if err = b.file.Truncate(HeaderBytes + int64(b.count+1)*RecordBytes); err != nil {
		return err
	}
	var header [HeaderBytes]byte
	copy(header[:], "WRDMAP01")
	binary.LittleEndian.PutUint64(header[8:], b.count)
	if _, err = b.file.WriteAt(header[:], 0); err != nil {
		return err
	}
	if err = b.file.Sync(); err != nil {
		return err
	}
	if err = b.db.Close(); err != nil {
		return err
	}
	b.db = nil
	lookup, err := os.Open(filepath.Join(b.dir, "lookup.sqlite"))
	if err != nil {
		return err
	}
	err = lookup.Sync()
	lookup.Close()
	if err != nil {
		return err
	}
	return Pack(b.ctx, b.dir)
}

// Keep the file cursor interface explicit for disk-backed queue tests.
var _ io.ReaderAt = (*os.File)(nil)

// AddVisualNode accepts exact query data without granting the query package filesystem access.
func (b *Builder) AddVisualNode(n contracts.VisualIndexNode) error {
	in := Input{ID: n.ID, Parent: n.Parent, Source: n.Source, Kind: n.Kind, Flags: n.Flags, Unknown: n.Unknown}
	if n.Bytes != "" {
		v, err := ParseSize(n.Bytes)
		if err != nil {
			return err
		}
		in.Bytes[0] = v
	}
	return b.Add(in)
}
