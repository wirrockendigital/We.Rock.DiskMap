package mapindex

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
)

// Pack clusters nested trees by transport block and keeps wide flat directory levels together.
// Disk-backed address translation keeps preparation independent of total tree size.
func Pack(ctx context.Context, dir string) error {
	source, err := os.Open(filepath.Join(dir, "tree.bin"))
	if err != nil {
		return err
	}
	defer source.Close()
	var header [HeaderBytes]byte
	if _, err = io.ReadFull(source, header[:]); err != nil {
		return err
	}
	count := binary.LittleEndian.Uint64(header[8:])
	if string(header[:8]) != "WRDMAP01" || count == 0 || count > uint64((1<<63-HeaderBytes)/RecordBytes)-1 {
		return ErrInvalid
	}
	old, err := sql.Open("sqlite", filepath.Join(dir, "lookup.sqlite"))
	if err != nil {
		return err
	}
	defer old.Close()
	old.SetMaxOpenConns(1)
	// All temporary files belong to this unpublished staging directory.
	output, err := os.OpenFile(filepath.Join(dir, "packed.tmp"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer output.Close()
	mapping, err := os.OpenFile(filepath.Join(dir, "addresses.tmp"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer func() { mapping.Close(); os.Remove(filepath.Join(dir, "addresses.tmp")) }()
	if err = mapping.Truncate(int64(count+1) * 8); err != nil {
		return err
	}
	if _, err = output.Write(header[:]); err != nil {
		return err
	}
	if _, err = output.Write(make([]byte, RecordBytes)); err != nil {
		return err
	}
	// Fixed caches amortize random reads without mapping the complete artifact into memory.
	sourcePages := newPackPages(source, 8192)
	addresses := newPackPages(mapping, 2048)
	writer := bufio.NewWriterSize(output, 1024*1024)
	address := func(slot uint64) (uint64, error) {
		if slot > count {
			return 0, ErrInvalid
		}
		var raw [8]byte
		e := addresses.read(raw[:], int64(slot)*8)
		return binary.LittleEndian.Uint64(raw[:]), e
	}
	// A flat directory wider than a transport block keeps its levels together; nested trees use block-sized clusters.
	readRecord := func(slot uint64) (Record, error) {
		var raw [RecordBytes]byte
		if slot == 0 || slot > count {
			return Record{}, ErrInvalid
		}
		if e := sourcePages.read(raw[:], HeaderBytes+int64(slot)*RecordBytes); e != nil {
			return Record{}, e
		}
		return Decode(raw[:])
	}
	flat := func(r Record) bool {
		return r.Kind == Directory && r.MaxDepth == r.Depth+1 && r.Files > RecordsPerBlock
	}
	var emitted uint64
	// A forest is supported for test fixtures; published query projections have one virtual root.
	rows, err := old.QueryContext(ctx, `SELECT slot FROM nodes WHERE parent=0 ORDER BY slot`)
	if err != nil {
		return err
	}
	roots := []uint64{}
	for rows.Next() {
		var slot uint64
		if err = rows.Scan(&slot); err != nil {
			rows.Close()
			return err
		}
		roots = append(roots, slot)
		if len(roots) > 10000 {
			rows.Close()
			return ErrInvalid
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Disk-backed frontiers support both bounded clusters and wide flat directory levels.
	scratch := func(name string) (*os.File, error) {
		return os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	}
	workFile, err := scratch("pack-work.tmp")
	if err != nil {
		return err
	}
	defer func() { workFile.Close(); os.Remove(filepath.Join(dir, "pack-work.tmp")) }()
	queueFile, err := scratch("pack-queue.tmp")
	if err != nil {
		return err
	}
	defer func() { queueFile.Close(); os.Remove(filepath.Join(dir, "pack-queue.tmp")) }()
	work, queue := newPackSlots(workFile), newPackSlots(queueFile)
	for _, slot := range roots {
		if err = work.push(slot); err != nil {
			return err
		}
	}
	for {
		root, ok, e := work.popLast()
		if e != nil {
			return e
		}
		if !ok {
			break
		}
		initial, e := readRecord(root)
		if e != nil {
			return e
		}
		wide := flat(initial)
		if initial.Kind == Compound && initial.Parent != 0 {
			parent, e := readRecord(initial.Parent)
			if e != nil {
				return e
			}
			wide = flat(parent)
		}
		clustered := uint64(0)
		queue.reset()
		if err = queue.push(root); err != nil {
			return err
		}
		for {
			if err = ctx.Err(); err != nil {
				return err
			}
			slot, ok, e := queue.pop()
			if e != nil {
				return e
			}
			if !ok {
				break
			}
			seen, e := address(slot)
			if e != nil {
				return e
			}
			if slot == 0 || seen != 0 {
				return ErrInvalid
			}
			var raw [RecordBytes]byte
			if err = sourcePages.read(raw[:], HeaderBytes+int64(slot)*RecordBytes); err != nil {
				return err
			}
			record, e := Decode(raw[:])
			if e != nil {
				return e
			}
			emitted++
			clustered++
			if emitted > count {
				return ErrInvalid
			}
			var translated [8]byte
			binary.LittleEndian.PutUint64(translated[:], emitted)
			if err = addresses.write(translated[:], int64(slot)*8); err != nil {
				return err
			}
			if _, err = writer.Write(raw[:]); err != nil {
				return err
			}
			destination := queue
			if flat(record) {
				destination = work
			}
			for _, child := range []uint64{record.Right, record.Left} {
				if child != 0 {
					if err = destination.push(child); err != nil {
						return err
					}
				}
			}
			if !wide && clustered >= RecordsPerBlock {
				break
			}
		}
		// Reverse the deferred frontier onto the disk stack so each clustered subtree stays adjacent.
		for {
			slot, ok, e := queue.popLast()
			if e != nil {
				return e
			}
			if !ok {
				break
			}
			if e = work.push(slot); e != nil {
				return e
			}
		}
	}
	if emitted != count {
		return ErrInvalid
	}
	// Rewrite edges in bounded sequential batches after every destination address is known.
	if err = writer.Flush(); err != nil {
		return err
	}
	if err = addresses.flush(); err != nil {
		return err
	}
	buffer := make([]byte, 1024*1024)
	for slot := uint64(1); slot <= count; {
		if err = ctx.Err(); err != nil {
			return err
		}
		records := min(uint64(len(buffer)/RecordBytes), count-slot+1)
		raw := buffer[:int(records)*RecordBytes]
		offset := HeaderBytes + int64(slot)*RecordBytes
		if _, err = output.ReadAt(raw, offset); err != nil {
			return err
		}
		for pos := 0; pos < len(raw); pos += RecordBytes {
			for _, at := range []int{8, 16, 24} {
				previous := binary.LittleEndian.Uint64(raw[pos+at:])
				next, e := address(previous)
				if e != nil {
					return e
				}
				if previous != 0 && next == 0 {
					return ErrInvalid
				}
				binary.LittleEndian.PutUint64(raw[pos+at:], next)
			}
		}
		if _, err = output.WriteAt(raw, offset); err != nil {
			return err
		}
		slot += records
	}
	// Rebuild only the identity lookup; preparation-only sort columns are no longer needed.
	lookupPath := filepath.Join(dir, "lookup-packed.tmp")
	next, err := sql.Open("sqlite", lookupPath)
	if err != nil {
		return err
	}
	defer next.Close()
	next.SetMaxOpenConns(1)
	if _, err = next.ExecContext(ctx, `PRAGMA journal_mode=OFF; PRAGMA synchronous=OFF; PRAGMA cache_size=-8192; CREATE TABLE nodes(id TEXT PRIMARY KEY,slot INTEGER NOT NULL UNIQUE)`); err != nil {
		return err
	}
	if err = os.Chmod(lookupPath, 0600); err != nil {
		return err
	}
	tx, err := next.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	insert, err := tx.PrepareContext(ctx, `INSERT INTO nodes VALUES(?,?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	rows, err = old.QueryContext(ctx, `SELECT id,slot FROM nodes ORDER BY slot`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var slot uint64
		if err = rows.Scan(&id, &slot); err != nil {
			rows.Close()
			return err
		}
		mapped, e := address(slot)
		if e != nil || mapped == 0 {
			rows.Close()
			return ErrInvalid
		}
		if _, err = insert.ExecContext(ctx, id, mapped); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if err = next.Close(); err != nil {
		return err
	}
	if err = old.Close(); err != nil {
		return err
	}
	if err = output.Sync(); err != nil {
		return err
	}
	if err = output.Close(); err != nil {
		return err
	}
	lookup, err := os.Open(lookupPath)
	if err != nil {
		return err
	}
	err = lookup.Sync()
	lookup.Close()
	if err != nil {
		return err
	}
	if err = os.Rename(filepath.Join(dir, "packed.tmp"), filepath.Join(dir, "tree.bin")); err != nil {
		return err
	}
	return os.Rename(lookupPath, filepath.Join(dir, "lookup.sqlite"))
}
