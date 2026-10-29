package mapindex

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

// SealBlocks writes per-block digests after the builder has finished all mutations.
func SealBlocks(ctx context.Context, dir string) error {
	f, err := os.Open(filepath.Join(dir, "tree.bin"))
	if err != nil {
		return err
	}
	defer f.Close()
	out, err := os.OpenFile(filepath.Join(dir, "blocks.sha256"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer out.Close()
	packed, err := os.OpenFile(filepath.Join(dir, "blocks.gz"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer packed.Close()
	offsets, err := os.OpenFile(filepath.Join(dir, "offsets.bin"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer offsets.Close()
	var position uint64
	var address [8]byte
	if _, err = offsets.Write(address[:]); err != nil {
		return err
	}
	buf := make([]byte, BlockBytes)
	// The header has a separate digest, followed by all record blocks including reserved slot zero.
	if _, err = io.ReadFull(f, buf[:HeaderBytes]); err != nil {
		return err
	}
	sum := sha256.Sum256(buf[:HeaderBytes])
	if _, err = out.Write(sum[:]); err != nil {
		return err
	}
	blocks := (binary.LittleEndian.Uint64(buf[8:]) + 1 + RecordsPerBlock - 1) / RecordsPerBlock
	var block uint64
	planes := make([]byte, BlockBytes)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, e := io.ReadFull(f, buf)
		if e != nil && e != io.EOF && e != io.ErrUnexpectedEOF {
			return e
		}
		if n == 0 {
			break
		}
		// Group equal byte positions so immutable records compress efficiently without losing any bits.
		rows := n / RecordBytes
		for column := 0; column < RecordBytes; column++ {
			for row := 0; row < rows; row++ {
				planes[column*rows+row] = buf[row*RecordBytes+column]
			}
		}
		var encoded bytes.Buffer
		compressor, e2 := gzip.NewWriterLevel(&encoded, gzip.BestSpeed)
		if e2 != nil {
			return e2
		}
		if _, e2 = compressor.Write(planes[:n]); e2 != nil {
			return e2
		}
		if e2 = compressor.Close(); e2 != nil {
			return e2
		}
		if _, e2 = packed.Write(encoded.Bytes()); e2 != nil {
			return e2
		}
		position += uint64(encoded.Len())
		binary.LittleEndian.PutUint64(address[:], position)
		if _, e2 = offsets.Write(address[:]); e2 != nil {
			return e2
		}
		sum = sha256.Sum256(buf[:n])
		if _, err = out.WriteAt(sum[:], int64(block+1)*32); err != nil {
			return err
		}
		// Authenticate the exact wire bytes without inflating them again for every request.
		packedSum := sha256.Sum256(encoded.Bytes())
		if _, err = out.WriteAt(packedSum[:], int64(blocks+1+block)*32); err != nil {
			return err
		}
		block++
		if e != nil {
			break
		}
	}
	if err = packed.Sync(); err != nil {
		return err
	}
	if err = offsets.Sync(); err != nil {
		return err
	}
	return out.Sync()
}

// Reader owns immutable descriptors; its caller must qualify the private directory and source binding.
type Reader struct {
	file, digests, lookup, packed, offsets *os.File
	db                                     *sql.DB
	Count, Blocks                          uint64
}

// Open validates dimensions and the sealed header before allowing any block reads.
func Open(dir string) (*Reader, error) {
	r := &Reader{}
	fail := func(e error) (*Reader, error) { r.Close(); return nil, e }
	var err error
	dfd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail(err)
	}
	defer unix.Close(dfd)
	var ds unix.Stat_t
	if unix.Fstat(dfd, &ds) != nil || ds.Uid != uint32(os.Geteuid()) || ds.Mode&0777 != 0700 {
		return fail(ErrInvalid)
	}
	open := func(name string) (*os.File, error) {
		fd, e := unix.Openat(dfd, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if e != nil {
			return nil, e
		}
		var s unix.Stat_t
		if unix.Fstat(fd, &s) != nil || s.Mode&unix.S_IFMT != unix.S_IFREG || s.Mode&0777 != 0600 || s.Uid != uint32(os.Geteuid()) || s.Nlink != 1 {
			unix.Close(fd)
			return nil, ErrInvalid
		}
		return os.NewFile(uintptr(fd), name), nil
	}
	r.file, err = open("tree.bin")
	if err != nil {
		return fail(err)
	}
	r.digests, err = open("blocks.sha256")
	if err != nil {
		return fail(err)
	}
	var header [HeaderBytes]byte
	if _, err = r.file.ReadAt(header[:], 0); err != nil {
		return fail(err)
	}
	var digest [32]byte
	if _, err = r.digests.ReadAt(digest[:], 0); err != nil {
		return fail(err)
	}
	sum := sha256.Sum256(header[:])
	if sum != digest || string(header[:8]) != "WRDMAP01" {
		return fail(ErrInvalid)
	}
	r.Count = binary.LittleEndian.Uint64(header[8:])
	if r.Count == 0 || r.Count > uint64((1<<63-HeaderBytes)/RecordBytes)-1 {
		return fail(ErrInvalid)
	}
	for _, v := range header[16:] {
		if v != 0 {
			return fail(ErrInvalid)
		}
	}
	r.Blocks = (r.Count + 1 + RecordsPerBlock - 1) / RecordsPerBlock
	info, err := r.file.Stat()
	if err != nil {
		return fail(err)
	}
	if info.Size() != HeaderBytes+int64(r.Count+1)*RecordBytes {
		return fail(ErrInvalid)
	}
	info, err = r.digests.Stat()
	if err != nil {
		return fail(err)
	}
	if info.Size() != int64(2*r.Blocks+1)*32 {
		return fail(ErrInvalid)
	}
	r.packed, err = open("blocks.gz")
	if err != nil {
		return fail(err)
	}
	r.offsets, err = open("offsets.bin")
	if err != nil {
		return fail(err)
	}
	info, err = r.offsets.Stat()
	if err != nil {
		return fail(err)
	}
	if info.Size() != int64(r.Blocks+1)*8 {
		return fail(ErrInvalid)
	}
	r.lookup, err = open("lookup.sqlite")
	if err != nil {
		return fail(err)
	}
	prefix := "/proc/self/fd/"
	if runtime.GOOS == "darwin" {
		prefix = "/dev/fd/"
	}
	u := url.URL{Scheme: "file", Path: prefix + strconv.FormatUint(uint64(r.lookup.Fd()), 10), RawQuery: "mode=ro&immutable=1"}
	r.db, err = sql.Open("sqlite", u.String())
	if err != nil {
		return fail(err)
	}
	r.db.SetMaxOpenConns(2)
	return r, nil
}

// Close releases descriptors only; generation retention owns artifact deletion.
func (r *Reader) Close() {
	if r.packed != nil {
		r.packed.Close()
	}
	if r.offsets != nil {
		r.offsets.Close()
	}
	if r.db != nil {
		r.db.Close()
	}
	if r.file != nil {
		r.file.Close()
	}
	if r.digests != nil {
		r.digests.Close()
	}
	if r.lookup != nil {
		r.lookup.Close()
	}
}

// Slot resolves an opaque original node ID without reconstructing ancestry or scanning records.
func (r *Reader) Slot(ctx context.Context, id string) (uint64, error) {
	var slot uint64
	err := r.db.QueryRowContext(ctx, `SELECT slot FROM nodes WHERE id=?`, id).Scan(&slot)
	if err != nil {
		return 0, err
	}
	if slot == 0 || slot > r.Count {
		return 0, ErrInvalid
	}
	// Verify numeric identity against the sealed record rather than trusting the lookup alone.
	if source, parseErr := strconv.ParseUint(id, 10, 64); parseErr == nil {
		node, readErr := r.Record(slot)
		if readErr != nil || node.Source != source || node.Kind == Compound || node.Kind == Virtual {
			return 0, ErrInvalid
		}
	}
	return slot, nil
}

// Identity is needed only for visible labels, navigation, and real-node actions.
func (r *Reader) Identity(ctx context.Context, slot uint64) (string, error) {
	var id string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM nodes WHERE slot=?`, slot).Scan(&id)
	return id, err
}

// Block validates a small immutable range before returning any bytes to the caller.
func (r *Reader) Block(block uint64) ([]byte, string, error) {
	if block >= r.Blocks {
		return nil, "", ErrInvalid
	}
	length := uint64(BlockBytes)
	remaining := (r.Count+1)*RecordBytes - block*BlockBytes
	if remaining < length {
		length = remaining
	}
	raw := make([]byte, int(length))
	if _, err := r.file.ReadAt(raw, HeaderBytes+int64(block)*BlockBytes); err != nil {
		return nil, "", err
	}
	var expected [32]byte
	if _, err := r.digests.ReadAt(expected[:], int64(block+1)*32); err != nil {
		return nil, "", err
	}
	actual := sha256.Sum256(raw)
	if !bytes.Equal(expected[:], actual[:]) {
		return nil, "", ErrInvalid
	}
	return raw, hex.EncodeToString(actual[:]), nil
}

// Record qualifies edges against artifact dimensions after validating the containing block.
func (r *Reader) Record(slot uint64) (Record, error) {
	if slot == 0 || slot > r.Count {
		return Record{}, ErrInvalid
	}
	raw, _, err := r.Block(slot / RecordsPerBlock)
	if err != nil {
		return Record{}, err
	}
	offset := (slot % RecordsPerBlock) * RecordBytes
	n, err := Decode(raw[offset : offset+RecordBytes])
	if err != nil {
		return n, err
	}
	if n.Parent > r.Count || n.Left > r.Count || n.Right > r.Count || n.Left == slot || n.Right == slot || n.Parent == slot {
		return Record{}, ErrInvalid
	}
	return n, nil
}

// CompressedBlock returns a bounded independently compressed block and its uncompressed digest.
// The sealed compressed digest protects transport bytes; the browser also verifies reconstructed records.
func (r *Reader) CompressedBlock(block uint64) ([]byte, string, error) {
	if block >= r.Blocks {
		return nil, "", ErrInvalid
	}
	var offsets [16]byte
	if _, err := r.offsets.ReadAt(offsets[:], int64(block)*8); err != nil {
		return nil, "", err
	}
	start, end := binary.LittleEndian.Uint64(offsets[:]), binary.LittleEndian.Uint64(offsets[8:])
	if end <= start || end-start > BlockBytes+1024 || end > 1<<63-1 {
		return nil, "", ErrInvalid
	}
	raw := make([]byte, end-start)
	if _, err := r.packed.ReadAt(raw, int64(start)); err != nil {
		return nil, "", err
	}
	var packedSum, expected [32]byte
	if _, err := r.digests.ReadAt(packedSum[:], int64(r.Blocks+1+block)*32); err != nil {
		return nil, "", err
	}
	if sha256.Sum256(raw) != packedSum {
		return nil, "", ErrInvalid
	}
	if _, err := r.digests.ReadAt(expected[:], int64(block+1)*32); err != nil {
		return nil, "", err
	}
	return raw, hex.EncodeToString(expected[:]), nil
}

// MatchesDirectory prevents an old failing reader from invalidating a newer published replacement.
func (r *Reader) MatchesDirectory(dir string) bool {
	held, err := r.file.Stat()
	if err != nil {
		return false
	}
	current, err := os.Lstat(filepath.Join(dir, "tree.bin"))
	return err == nil && os.SameFile(held, current)
}
