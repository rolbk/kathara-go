package vfs

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// memFS is the `mem://` implementation (model/Lab.py:81).
type memFS struct {
	mu    sync.RWMutex
	nodes map[string]*memNode // key: cleaned path; "." is the always-present root
	clock func() time.Time
}

type memNode struct {
	dir  bool
	data []byte
	mode os.FileMode
	mod  time.Time
}

// Memory returns an in-memory FS with no host path.
func Memory() FS {
	return &memFS{
		nodes: map[string]*memNode{
			".": {dir: true, mode: fs.ModeDir | 0o755},
		},
		clock: time.Now,
	}
}

func (m *memFS) TypeName() string { return "memory" }

func (m *memFS) SysPath(string) (string, bool) { return "", false }

// --- reading ---------------------------------------------------------------

func (m *memFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	n, ok := m.nodes[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if n.dir {
		return &memDir{fsys: m, name: name, info: n.stat(path.Base(name))}, nil
	}
	return &memFile{
		info:   n.stat(path.Base(name)),
		Reader: bytes.NewReader(n.data),
	}, nil
}

func (m *memFS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	n, ok := m.nodes[name]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return n.stat(path.Base(name)), nil
}

func (m *memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()

	n, ok := m.nodes[name]
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	if !n.dir {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: ErrDirectoryExpected}
	}
	return m.childrenLocked(name), nil
}

// childrenLocked lists the direct children of dir, sorted by name (io/fs
// requires ReadDir to be sorted, and it makes tree walks deterministic).
func (m *memFS) childrenLocked(dir string) []fs.DirEntry {
	prefix := ""
	if dir != "." {
		prefix = dir + "/"
	}
	var out []fs.DirEntry
	for p, n := range m.nodes {
		if p == "." || !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		out = append(out, fs.FileInfoToDirEntry(n.stat(rest)))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// --- writing ---------------------------------------------------------------

func (m *memFS) Create(name string) (io.WriteCloser, error) {
	return m.openWrite(name, "create", false)
}

func (m *memFS) Append(name string) (io.WriteCloser, error) {
	return m.openWrite(name, "append", true)
}

func (m *memFS) openWrite(name, op string, appendMode bool) (io.WriteCloser, error) {
	cleaned, err := CleanPath(name)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if n, ok := m.nodes[cleaned]; ok && n.dir {
		return nil, &fs.PathError{Op: op, Path: cleaned, Err: ErrFileExpected}
	}
	// Parent must exist and be a directory: pyfilesystem's fs.open(p, "w"|"a")
	// raises ResourceNotFound otherwise, which is why every FilesystemMixin
	// creator calls makedirs first (probe P6b). ResourceNotFound is also what
	// MemoryFS raises when a path COMPONENT is a file, not DirectoryExpected —
	// verified live: with a file at /afile, open("/afile/child", "w") and
	// open("/afile/child", "a") both raise ResourceNotFound on mem:// and on
	// osfs://. (DirectoryExpected comes from makedirs, which runs first for
	// the create_* family — probe MD1 — and is unchanged.)
	parent := parentDir(cleaned)
	pn, ok := m.nodes[parent]
	if !ok || !pn.dir {
		return nil, &fs.PathError{Op: op, Path: cleaned, Err: fs.ErrNotExist}
	}

	w := &memWriter{fsys: m, name: cleaned}
	if appendMode {
		if n, ok := m.nodes[cleaned]; ok {
			w.buf.Write(n.data)
			return w, nil
		}
	}
	// pyfilesystem creates the entry — and, for "w", truncates it — at OPEN
	// time, not at close. Verified live on both backends: right after
	// fs.open(p, "w") the file exists, reads back b"" and lists in its parent,
	// and removedir on that parent fails with DirectoryNotEmpty. Creating the
	// node here reproduces that and closes a real hole: with the node deferred
	// to Close, Remove(parent) saw an empty directory and succeeded, and the
	// later Close then stored a file with no parent — reachable by Exists and
	// ReadFile but invisible to ReadDir and Walk.
	m.nodes[cleaned] = &memNode{mode: 0o644, mod: m.clock()}
	return w, nil
}

func (m *memFS) MkdirAll(name string, perm os.FileMode) error {
	cleaned, err := CleanPath(name)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if cleaned == "." {
		return nil
	}
	parts := strings.Split(cleaned, "/")
	cur := "."
	for _, p := range parts {
		cur = path.Join(cur, p)
		n, ok := m.nodes[cur]
		if ok {
			if !n.dir {
				return &fs.PathError{Op: "mkdir", Path: cur, Err: ErrDirectoryExpected}
			}
			continue // recreate=True: existing directories are fine
		}
		m.nodes[cur] = &memNode{dir: true, mode: fs.ModeDir | perm.Perm(), mod: m.clock()}
	}
	return nil
}

func (m *memFS) Remove(name string) error {
	cleaned, err := CleanPath(name)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	n, ok := m.nodes[cleaned]
	if !ok {
		return &fs.PathError{Op: "remove", Path: cleaned, Err: fs.ErrNotExist}
	}
	if n.dir {
		// fs.errors.RemoveRootError: pyfilesystem refuses the root whether it
		// is empty or not. OSDir refuses it identically.
		if cleaned == "." {
			return &fs.PathError{Op: "remove", Path: cleaned, Err: ErrRemoveRoot}
		}
		if len(m.childrenLocked(cleaned)) > 0 {
			return &fs.PathError{Op: "remove", Path: cleaned, Err: ErrDirectoryNotEmpty}
		}
	}
	delete(m.nodes, cleaned)
	return nil
}

func (m *memFS) store(name string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes[name] = &memNode{data: data, mode: 0o644, mod: m.clock()}
}

// --- node plumbing ---------------------------------------------------------

func (n *memNode) stat(base string) fs.FileInfo {
	return &memInfo{name: base, node: n}
}

type memInfo struct {
	name string
	node *memNode
}

func (i *memInfo) Name() string { return i.name }
func (i *memInfo) Size() int64 {
	if i.node.dir {
		return 0
	}
	return int64(len(i.node.data))
}
func (i *memInfo) Mode() fs.FileMode  { return i.node.mode }
func (i *memInfo) ModTime() time.Time { return i.node.mod }
func (i *memInfo) IsDir() bool        { return i.node.dir }
func (i *memInfo) Sys() any           { return nil }

type memFile struct {
	info fs.FileInfo
	*bytes.Reader
}

func (f *memFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *memFile) Close() error               { return nil }

type memDir struct {
	fsys   *memFS
	name   string
	info   fs.FileInfo
	offset int
}

func (d *memDir) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *memDir) Close() error               { return nil }

func (d *memDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: ErrFileExpected}
}

func (d *memDir) ReadDir(n int) ([]fs.DirEntry, error) {
	d.fsys.mu.RLock()
	all := d.fsys.childrenLocked(d.name)
	d.fsys.mu.RUnlock()

	if n <= 0 {
		rest := all[d.offset:]
		d.offset = len(all)
		return rest, nil
	}
	if d.offset >= len(all) {
		return nil, io.EOF
	}
	end := min(d.offset+n, len(all))
	rest := all[d.offset:end]
	d.offset = end
	return rest, nil
}

// memWriter buffers until Close so a failed write leaves no half-file. The
// entry itself is created (and, for Create, truncated) by openWrite, matching
// pyfilesystem's open-time semantics; only the CONTENT lands on Close.
type memWriter struct {
	fsys   *memFS
	name   string
	buf    bytes.Buffer
	closed bool
}

func (w *memWriter) Write(p []byte) (int, error) {
	if w.closed {
		return 0, fs.ErrClosed
	}
	return w.buf.Write(p)
}

func (w *memWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	w.fsys.store(w.name, bytes.Clone(w.buf.Bytes()))
	return nil
}
