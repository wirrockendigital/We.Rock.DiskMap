// Package migration provides an offline, fail-closed control-database upgrade boundary.
package migration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wirrockendigital/We.Rock.DiskMap/internal/security"
	_ "modernc.org/sqlite"
)

const ControlVersion = 7
const MaxBundles = 2
const maxDBBytes = 512 << 20

var ErrUpgrade = errors.New("UPGRADE_RECOVERY_REQUIRED")
var ErrVersion = errors.New("CONTROL_SCHEMA_UNSUPPORTED")

// Manifest records exact recovery bytes and a versioned operation, never a reversible downgrade promise.
type Manifest struct {
	CreatedAt     string `json:"createdAt"`
	Version       int    `json:"version"`
	ID            string `json:"id"`
	BeforeVersion int    `json:"beforeVersion"`
	TargetVersion int    `json:"targetVersion"`
	ControlSHA    string `json:"controlSHA"`
	InventorySHA  string `json:"inventorySHA,omitempty"`
	CandidateSHA  string `json:"candidateSHA,omitempty"`
	Stage         string `json:"stage"`
}
type workspace struct {
	ctx  context.Context
	root *os.Root
	lock *os.File
	dir  *os.File
	path string
}

// open pins the package state and acquires the same singleton lock as the daemon.
func open(ctx context.Context, state string) (*workspace, error) {
	state, e := filepath.EvalSymlinks(state)
	if e != nil {
		return nil, ErrUpgrade
	}
	lock, e := security.OpenPrivateFile(filepath.Join(state, "daemon.lock"), os.O_RDWR|os.O_CREATE)
	if e != nil {
		return nil, ErrUpgrade
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		lock.Close()
		return nil, ErrUpgrade
	}
	root, e := os.OpenRoot(state)
	if e != nil {
		lock.Close()
		return nil, ErrUpgrade
	}
	dir, e := root.Open(".")
	if e != nil {
		root.Close()
		lock.Close()
		return nil, ErrUpgrade
	}
	info, e := dir.Stat()
	if e != nil {
		dir.Close()
		root.Close()
		lock.Close()
		return nil, ErrUpgrade
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok || info.Mode().Perm() != 0700 || sys.Uid != uint32(os.Geteuid()) {
		dir.Close()
		root.Close()
		lock.Close()
		return nil, ErrUpgrade
	}
	w := &workspace{ctx: ctx, root: root, dir: dir, lock: lock, path: state}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, e := root.Lstat("control.db" + suffix); !errors.Is(e, os.ErrNotExist) {
			w.close()
			return nil, ErrUpgrade
		}
	}
	return w, nil
}

// syncParent makes newly written names durable before a manifest can reference them.
func (w *workspace) syncParent(name string) error {
	d, e := w.root.Open(filepath.Dir(name))
	if e != nil {
		return ErrUpgrade
	}
	defer d.Close()
	if d.Sync() != nil {
		return ErrUpgrade
	}
	return nil
}
func (w *workspace) close() { w.dir.Close(); w.root.Close(); w.lock.Close() }

// file verifies held regular private single-link inodes before reading or writing.
func (w *workspace) file(name string, flags int) (*os.File, error) {
	// All intermediate components must remain private real directories, not in-root aliases.
	parent := filepath.Dir(name)
	if parent != "." {
		parts := strings.Split(parent, "/")
		current := ""
		for _, part := range parts {
			if current != "" {
				current += "/"
			}
			current += part
			d, e := w.root.OpenFile(current, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
			if e != nil {
				return nil, ErrUpgrade
			}
			st, e := d.Stat()
			d.Close()
			if e != nil {
				return nil, ErrUpgrade
			}
			sys, ok := st.Sys().(*syscall.Stat_t)
			if !ok || !st.IsDir() || st.Mode().Perm() != 0700 || sys.Uid != uint32(os.Geteuid()) {
				return nil, ErrUpgrade
			}
		}
	}
	f, e := w.root.OpenFile(name, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, ErrUpgrade
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || s.Nlink != 1 || s.Uid != uint32(os.Geteuid()) {
		f.Close()
		return nil, ErrUpgrade
	}
	return f, nil
}
func fdpath(fd uintptr) string {
	prefix := "/proc/self/fd/"
	if runtime.GOOS == "darwin" {
		prefix = "/dev/fd/"
	}
	return prefix + strconv.FormatUint(uint64(fd), 10)
}

// check opens only a held immutable file; a cold database with sidecars was rejected at entry.
func check(ctx context.Context, f *os.File) (int, error) {
	u := url.URL{Scheme: "file", Path: fdpath(f.Fd())}
	db, e := sql.Open("sqlite", u.String()+"?mode=ro&immutable=1")
	if e != nil {
		return 0, ErrUpgrade
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var result string
	var version int
	if db.QueryRowContext(ctx, "PRAGMA integrity_check(1)").Scan(&result) != nil || result != "ok" || db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version) != nil {
		return 0, ErrUpgrade
	}
	return version, nil
}
func digest(ctx context.Context, f *os.File) (string, error) {
	if _, e := f.Seek(0, 0); e != nil {
		return "", e
	}
	h := sha256.New()
	n, e := copyStream(ctx, h, io.LimitReader(f, maxDBBytes+1))
	if e != nil || n > maxDBBytes {
		return "", ErrUpgrade
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// copy uses fixed-size streaming buffers and fsyncs the completed recovery/candidate file.
func (w *workspace) copy(src, dst string) (string, error) {
	f, e := w.file(src, os.O_RDONLY)
	if e != nil {
		return "", e
	}
	defer f.Close()
	out, e := w.file(dst, os.O_CREATE|os.O_EXCL|os.O_RDWR)
	if e != nil {
		return "", e
	}
	defer out.Close()
	h := sha256.New()
	n, e := copyStream(w.ctx, io.MultiWriter(out, h), io.LimitReader(f, maxDBBytes+1))
	if e != nil || n > maxDBBytes || out.Sync() != nil || w.syncParent(dst) != nil {
		return "", ErrUpgrade
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (w *workspace) write(name string, value any) error {
	raw, e := json.Marshal(value)
	if e != nil || len(raw) > 4096 {
		return ErrUpgrade
	}
	// Unique temporary names cannot strand later recovery after a process crash.
	var nonce [16]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		return ErrUpgrade
	}
	tmp := name + ".tmp-" + hex.EncodeToString(nonce[:])
	f, e := w.file(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if e != nil {
		return ErrUpgrade
	}
	// Remove only this invocation's private temporary inode after ordinary failure.
	defer w.root.Remove(tmp)
	_, e = f.Write(raw)
	if e == nil {
		e = f.Sync()
	}
	f.Close()
	if e != nil {
		return ErrUpgrade
	}
	if w.root.Rename(tmp, name) != nil || w.syncParent(name) != nil {
		return ErrUpgrade
	}
	return nil
}

// current validates the fixed pointer and manifest without accepting caller-controlled paths.
func (w *workspace) current() (Manifest, string, error) {
	f, e := w.file("upgrade-current.json", os.O_RDONLY)
	if e != nil {
		return Manifest{}, "", ErrUpgrade
	}
	raw, e := io.ReadAll(io.LimitReader(f, 4097))
	f.Close()
	var pointer struct {
		ID string `json:"id"`
	}
	if e != nil || len(raw) > 4096 || json.Unmarshal(raw, &pointer) != nil || len(pointer.ID) != 32 {
		return Manifest{}, "", ErrUpgrade
	}
	if _, e = hex.DecodeString(pointer.ID); e != nil {
		return Manifest{}, "", ErrUpgrade
	}
	base := "upgrades/" + pointer.ID
	f, e = w.file(base+"/manifest.json", os.O_RDONLY)
	if e != nil {
		return Manifest{}, "", ErrUpgrade
	}
	raw, e = io.ReadAll(io.LimitReader(f, 4097))
	f.Close()
	var m Manifest
	if e != nil || len(raw) > 4096 || json.Unmarshal(raw, &m) != nil || m.Version != 1 || m.ID != pointer.ID || (m.TargetVersion != ControlVersion && !((m.TargetVersion == 3 || m.TargetVersion == 4 || m.TargetVersion == 5 || m.TargetVersion == 6) && (m.Stage == "applied" || m.Stage == "restored"))) {
		return Manifest{}, "", ErrUpgrade
	}
	return m, base, nil
}

// Prepare writes a new durable recovery bundle before any database changes.
func Prepare(ctx context.Context, state string) error {
	w, e := open(ctx, state)
	if e != nil {
		return e
	}
	defer w.close()
	src, e := w.file("control.db", os.O_RDONLY)
	if e != nil {
		return ErrUpgrade
	}
	version, e := check(ctx, src)
	src.Close()
	if e != nil {
		return e
	}
	if version != 0 && version != 2 && version != 3 && version != 4 && version != 5 && version != 6 && version != ControlVersion {
		return ErrVersion
	}
	if _, e := w.root.Lstat("upgrade-current.json"); e == nil {
		m, _, e := w.current()
		if e != nil || (m.Stage != "applied" && m.Stage != "restored") {
			return ErrUpgrade
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return ErrUpgrade
	}
	if e = w.root.Mkdir("upgrades", 0700); e != nil && !errors.Is(e, os.ErrExist) {
		return ErrUpgrade
	}
	d, e := w.root.OpenFile("upgrades", os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return ErrUpgrade
	}
	info, e := d.Stat()
	if e != nil || info.Mode().Perm() != 0700 {
		d.Close()
		return ErrUpgrade
	}
	entries, e := d.ReadDir(1025)
	d.Close()
	if e != nil && e != io.EOF {
		return ErrUpgrade
	}
	if len(entries) > 1024 {
		return ErrUpgrade
	}
	var id [16]byte
	if _, e = rand.Read(id[:]); e != nil {
		return ErrUpgrade
	}
	m := Manifest{CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Version: 1, ID: hex.EncodeToString(id[:]), BeforeVersion: version, TargetVersion: ControlVersion, Stage: "prepared"}
	base := "upgrades/" + m.ID
	if w.root.Mkdir(base, 0700) != nil || w.syncParent(base) != nil {
		return ErrUpgrade
	}
	m.ControlSHA, e = w.copy("control.db", base+"/control.before.db")
	if e != nil {
		return ErrUpgrade
	}
	if _, e = w.root.Lstat("inventory.json"); e == nil {
		m.InventorySHA, e = w.copy("inventory.json", base+"/inventory.before.json")
		if e != nil {
			return ErrUpgrade
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return ErrUpgrade
	}
	backup, e := w.file(base+"/control.before.db", os.O_RDONLY)
	if e != nil {
		return ErrUpgrade
	}
	bv, be := check(ctx, backup)
	bh, he := digest(w.ctx, backup)
	backup.Close()
	if be != nil || he != nil || bv != version || bh != m.ControlSHA {
		return ErrUpgrade
	}
	if w.write(base+"/manifest.json", m) != nil {
		return ErrUpgrade
	}
	if e = w.write("upgrade-current.json", struct {
		ID string `json:"id"`
	}{m.ID}); e != nil {
		return e
	}
	return w.trimBundles(m.ID)
}

// Apply migrates a private candidate and atomically publishes it only after integrity validation.
func Apply(ctx context.Context, state string, migrate func(context.Context, *sql.DB) error) (result error) {
	// Preserve cancellation classification without retaining private underlying error messages.
	defer func() {
		if result != nil && ctx.Err() != nil {
			result = errors.Join(ErrUpgrade, ctx.Err())
		}
	}()
	w, e := open(ctx, state)
	if e != nil {
		return e
	}
	defer w.close()
	m, base, e := w.current()
	if e == nil && m.TargetVersion != ControlVersion {
		return ErrVersion
	}
	if e != nil {
		return e
	}
	live, e := w.file("control.db", os.O_RDONLY)
	if e != nil {
		return ErrUpgrade
	}
	sha, e := digest(w.ctx, live)
	live.Close()
	if e != nil {
		return ErrUpgrade
	}
	if m.Stage == "publishing" && sha == m.CandidateSHA {
		m.Stage = "applied"
		return w.write(base+"/manifest.json", m)
	}
	if m.Stage == "applied" && sha == m.CandidateSHA {
		return nil
	}
	if m.Stage != "prepared" || sha != m.ControlSHA {
		return ErrUpgrade
	}
	candidate := base + "/candidate.db"
	if copied, copyErr := w.copy(base+"/control.before.db", candidate); copyErr != nil || copied != m.ControlSHA {
		return ErrUpgrade
	}
	// The candidate is inside the held private state directory, never a generation file.
	held, e := w.file(candidate, os.O_RDWR)
	if e != nil {
		return ErrUpgrade
	}
	defer held.Close()
	u := url.URL{Scheme: "file", Path: fdpath(held.Fd())}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return ErrUpgrade
	}
	db.SetMaxOpenConns(1)
	if _, e = db.ExecContext(ctx, "PRAGMA journal_mode=MEMORY; PRAGMA synchronous=FULL"); e == nil {
		e = migrate(ctx, db)
	}
	closeErr := db.Close()
	if e != nil || closeErr != nil {
		// A callback may wrap its own deadline even before the outer context expires.
		for _, cause := range []error{context.DeadlineExceeded, context.Canceled} {
			if errors.Is(e, cause) || errors.Is(closeErr, cause) {
				return errors.Join(ErrUpgrade, cause)
			}
		}
		return ErrUpgrade
	}
	f, e := w.file(candidate, os.O_RDWR)
	if e != nil {
		return ErrUpgrade
	}
	v, e := check(ctx, f)
	if e == nil && v == ControlVersion {
		m.CandidateSHA, e = digest(w.ctx, f)
	} else {
		e = ErrUpgrade
	}
	if e == nil {
		e = f.Sync()
	}
	f.Close()
	if e != nil {
		return ErrUpgrade
	}
	m.Stage = "publishing"
	if w.write(base+"/manifest.json", m) != nil {
		return ErrUpgrade
	}
	if w.root.Rename(candidate, "control.db") != nil || w.dir.Sync() != nil {
		return ErrUpgrade
	}
	m.Stage = "applied"
	return w.write(base+"/manifest.json", m)
}

// Restore is explicit recovery to exact saved bytes; it never claims that a newer schema can downgrade.
func Restore(ctx context.Context, state string) error {
	w, e := open(ctx, state)
	if e != nil {
		return e
	}
	defer w.close()
	m, base, e := w.current()
	if e == nil && m.TargetVersion != ControlVersion {
		return ErrVersion
	}
	if e != nil {
		return e
	}
	f, e := w.file(base+"/control.before.db", os.O_RDONLY)
	if e != nil {
		return ErrUpgrade
	}
	sha, e := digest(w.ctx, f)
	v, checkErr := check(ctx, f)
	f.Close()
	if e != nil || checkErr != nil || sha != m.ControlSHA || v != m.BeforeVersion {
		return ErrUpgrade
	}
	// Preserve the failed/current database for investigation before replacing its directory entry.
	var id [16]byte
	if _, e = rand.Read(id[:]); e != nil {
		return ErrUpgrade
	}
	suffix := hex.EncodeToString(id[:])
	if _, e = w.copy("control.db", base+"/recovery-displaced-"+suffix+".db"); e != nil {
		return ErrUpgrade
	}
	restored := base + "/restore-" + suffix + ".db"
	if _, e = w.copy(base+"/control.before.db", restored); e != nil {
		return ErrUpgrade
	}
	if w.root.Rename(restored, "control.db") != nil || w.dir.Sync() != nil {
		return ErrUpgrade
	}
	m.Stage = "restored"
	return w.write(base+"/manifest.json", m)
}

// Deadline gives offline operations an explicit cooperative execution budget.
func Deadline(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 2*time.Minute)
}

// trimBundles deletes only manifest-bound, fully qualified regular files in obsolete owned bundles.
func (w *workspace) trimBundles(current string) error {
	dir, e := w.root.OpenFile("upgrades", os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return ErrUpgrade
	}
	entries, e := dir.ReadDir(1025)
	dir.Close()
	if e != nil && e != io.EOF {
		return ErrUpgrade
	}
	if len(entries) > 1024 {
		return ErrUpgrade
	}
	type item struct{ id, created string }
	var items []item
	for _, entry := range entries {
		id := entry.Name()
		if len(id) != 32 {
			return ErrUpgrade
		}
		if _, e := hex.DecodeString(id); e != nil {
			return ErrUpgrade
		}
		f, e := w.file("upgrades/"+id+"/manifest.json", os.O_RDONLY)
		if e != nil {
			return ErrUpgrade
		}
		raw, e := io.ReadAll(io.LimitReader(f, 4097))
		f.Close()
		var m Manifest
		if e != nil || len(raw) > 4096 || json.Unmarshal(raw, &m) != nil || m.Version != 1 || m.ID != id {
			return ErrUpgrade
		}
		if _, e := time.Parse(time.RFC3339Nano, m.CreatedAt); e != nil {
			return ErrUpgrade
		}
		backup, e := w.file("upgrades/"+id+"/control.before.db", os.O_RDONLY)
		if e != nil {
			return ErrUpgrade
		}
		sha, e := digest(w.ctx, backup)
		backup.Close()
		if e != nil || sha != m.ControlSHA {
			return ErrUpgrade
		}
		items = append(items, item{id, m.CreatedAt})
	}
	sort.Slice(items, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, items[i].created)
		b, _ := time.Parse(time.RFC3339Nano, items[j].created)
		return a.After(b)
	})
	kept := 1
	for _, it := range items {
		if it.id == current {
			continue
		}
		if kept < MaxBundles {
			kept++
			continue
		}
		base := "upgrades/" + it.id
		sub, e := w.root.OpenRoot(base)
		if e != nil {
			return ErrUpgrade
		}
		f, e := sub.Open(".")
		if e != nil {
			sub.Close()
			return ErrUpgrade
		}
		files, e := f.ReadDir(64)
		f.Close()
		if e != nil && e != io.EOF {
			sub.Close()
			return ErrUpgrade
		}
		if len(files) > 63 {
			sub.Close()
			return ErrUpgrade
		}
		// Validate the whole deletion set before unlinking the first name.
		for _, entry := range files {
			name := entry.Name()
			allowed := name == "manifest.json" || name == "control.before.db" || name == "inventory.before.json" || name == "candidate.db" || name == "candidate.db-journal" || name == "manifest.json.tmp"
			// A crashed manifest write is inert; only its exact private name class is eligible.
			if strings.HasPrefix(name, "manifest.json.tmp-") {
				nonce := strings.TrimPrefix(name, "manifest.json.tmp-")
				_, err := hex.DecodeString(nonce)
				allowed = err == nil && len(nonce) == 32
			}
			if strings.HasPrefix(name, "recovery-displaced-") && strings.HasSuffix(name, ".db") {
				id := strings.TrimSuffix(strings.TrimPrefix(name, "recovery-displaced-"), ".db")
				_, e := hex.DecodeString(id)
				allowed = e == nil && len(id) == 32
			}
			if !allowed {
				sub.Close()
				return ErrUpgrade
			}
			held, e := w.file(base+"/"+name, os.O_RDONLY)
			if e != nil {
				sub.Close()
				return ErrUpgrade
			}
			held.Close()
		}
		for _, entry := range files {
			if sub.Remove(entry.Name()) != nil {
				sub.Close()
				return ErrUpgrade
			}
		}
		sub.Close()
		if w.root.Remove(base) != nil {
			return ErrUpgrade
		}
	}
	return w.dir.Sync()
}

// CheckReady prevents ordinary startup after an unfinished or explicitly restored upgrade.
// The caller already owns daemon.lock; no second lock acquisition is attempted.
func CheckReady(state string) error {
	root, e := os.OpenRoot(state)
	if e != nil {
		return ErrUpgrade
	}
	defer root.Close()
	if _, e = root.Lstat("upgrade-current.json"); errors.Is(e, os.ErrNotExist) {
		return nil
	} else if e != nil {
		return ErrUpgrade
	}
	w := &workspace{root: root}
	m, _, e := w.current()
	if e != nil || m.Stage != "applied" {
		return ErrUpgrade
	}
	return nil
}

// copyStream checks cancellation between bounded I/O chunks, never promising interruption of a kernel call.
func copyStream(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 32*1024)
	var total int64
	for {
		if e := ctx.Err(); e != nil {
			return total, e
		}
		n, e := src.Read(buffer)
		if n > 0 {
			if ce := ctx.Err(); ce != nil {
				return total, ce
			}
			written, we := dst.Write(buffer[:n])
			total += int64(written)
			if we != nil {
				return total, we
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if e == io.EOF {
			return total, nil
		}
		if e != nil {
			return total, e
		}
		if n == 0 {
			return total, io.ErrNoProgress
		}
	}
}
