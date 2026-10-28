package vmguest

import (
	"crypto/sha256"
	"encoding/hex"
	"foundry/internal/vmruntime"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type stagingTreeV2 struct {
	names  map[string]string
	files  map[string]bool
	bytes  int64
	inodes int
	count  uint32
}
type stagerV2 struct {
	source, deps string
	page         int64
	trees        [2]stagingTreeV2
}

func newStagerV2(source, deps string, page int) (*stagerV2, error) {
	if page < 4096 || page > 65536 || page&(page-1) != 0 {
		return nil, errGuestV2
	}
	for _, root := range []string{source, deps} {
		if e := os.MkdirAll(root, 0700); e != nil {
			return nil, e
		}
		st, e := os.Lstat(root)
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return nil, errGuestV2
		}
		entries, e := os.ReadDir(root)
		if e != nil || len(entries) != 0 {
			return nil, errGuestV2
		}
	}
	s := &stagerV2{source: source, deps: deps, page: int64(page)}
	for i := range s.trees {
		s.trees[i] = stagingTreeV2{names: map[string]string{}, files: map[string]bool{}, inodes: 3}
	}
	return s, nil
}
func (s *stagerV2) Stage(kind uint8, index uint32, f vmruntime.SourceFileV2, body io.Reader) error {
	if kind != 1 && kind != 2 || !relativeV2(f.Path) || f.Size < 0 {
		return errGuestV2
	}
	tree := &s.trees[kind-1]
	root := s.source
	maxBytes, maxInodes, maxFiles := int64(16<<20), 16384, uint32(500)
	if kind == 2 {
		root = s.deps
		maxBytes, maxInodes, maxFiles = 80<<20, 32768, 10000
		if f.Size > 8<<20 {
			return errGuestV2
		}
	} else {
		if f.Mode != 0644 && f.Mode != 0755 || f.Size > 10<<20 {
			return errGuestV2
		}
	}
	if index != tree.count || index >= maxFiles {
		return errGuestV2
	}
	allocation := (f.Size + s.page - 1) / s.page * s.page
	if allocation > maxBytes-tree.bytes {
		return errGuestV2
	}
	parts := strings.Split(f.Path, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		key := strings.ToLower(p)
		old, exists := tree.names[key]
		if exists && old != p || tree.files[key] || i == len(parts)-1 && exists {
			return errGuestV2
		}
		if !exists {
			tree.inodes++
			tree.names[key] = p
		}
		if tree.inodes > maxInodes {
			return errGuestV2
		}
	}
	parent := root
	for _, part := range parts[:len(parts)-1] {
		parent = filepath.Join(parent, part)
		if e := os.Mkdir(parent, 0700); e != nil && !os.IsExist(e) {
			return e
		}
		st, e := os.Lstat(parent)
		if e != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errGuestV2
		}
	}
	target := filepath.Join(root, filepath.FromSlash(f.Path))
	out, e := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	n, copyErr := io.CopyBuffer(out, io.LimitReader(body, f.Size+1), make([]byte, 32<<10))
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil || n != f.Size {
		return errGuestV2
	}
	tree.files[strings.ToLower(f.Path)] = true
	tree.bytes += allocation
	tree.count++
	return nil
}
func freezeTreeV2(root string) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		st, e := d.Info()
		if e != nil {
			return e
		}
		if st.IsDir() {
			return os.Chmod(p, 0555)
		}
		if !st.Mode().IsRegular() {
			return errGuestV2
		}
		return os.Chmod(p, 0444)
	})
}

type runtimeObservationV2 struct {
	regular  bool
	mode     os.FileMode
	uid, gid uint32
	size     int64
	sha256   string
}

func verifyInventoryV2(raw []byte, target vmruntime.ProbeTargetV2, observe func(string) (runtimeObservationV2, error)) error {
	inv, e := vmruntime.ParseRuntimeInventoryV2(raw)
	if e != nil || target.Identity.Validate() != nil || target.Identity.SchemaVersion != 2 {
		return errGuestV2
	}
	digest, e := inv.Digest()
	if e != nil || digest != target.RuntimeInventoryDigest || inv.GuestPlatform != target.Identity.GuestPlatform || inv.Supervisor.SHA256 != target.Identity.VerifierDigest {
		return errGuestV2
	}
	files := []vmruntime.RuntimeFileV2{inv.Supervisor}
	executable := map[string]bool{inv.Supervisor.Path: true}
	switch target.ProfileID {
	case "c4_node_model_v2":
		files = append(files, inv.Node.Executable, inv.Runners[0].File)
		executable[inv.Node.Executable.Path] = true
	case "c4_python_model_v2":
		files = append(files, inv.Python.Executable, inv.Runners[1].File)
		executable[inv.Python.Executable.Path] = true
	case "c4_typescript_model_v2":
		files = append(files, inv.Node.Executable, inv.TypeScript.Compiler, inv.Runners[0].File, inv.Runners[2].File)
		executable[inv.Node.Executable.Path] = true
	default:
		return errGuestV2
	}
	for _, f := range files {
		o, e := observe(f.Path)
		mode := os.FileMode(0444)
		if executable[f.Path] {
			mode = 0555
		}
		if e != nil || !o.regular || o.mode != mode || o.uid != 0 || o.gid != 0 || o.size != f.Size || o.sha256 != f.SHA256 {
			return errGuestV2
		}
	}
	return nil
}
func hashRegularV2(p string, max int64) (int64, string, error) {
	st, e := os.Lstat(p)
	if e != nil || !st.Mode().IsRegular() || st.Size() < 0 || st.Size() > max {
		return 0, "", errGuestV2
	}
	f, e := os.Open(p)
	if e != nil {
		return 0, "", e
	}
	defer f.Close()
	after, e := f.Stat()
	if e != nil || !os.SameFile(st, after) {
		return 0, "", errGuestV2
	}
	h := sha256.New()
	n, e := io.CopyBuffer(h, io.LimitReader(f, max+1), make([]byte, 32<<10))
	end, se := f.Stat()
	if e != nil || se != nil || n != st.Size() || end.Size() != st.Size() || end.ModTime() != st.ModTime() {
		return 0, "", errGuestV2
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

type mountSpecV2 struct {
	bytes, inodes uint64
	noexec        bool
}
type mountObservationV2 struct {
	bytes, inodes                          uint64
	tmpfs, nosuid, nodev, noexec, readonly bool
}

func mountSpecsV2() map[string]mountSpecV2 {
	return map[string]mountSpecV2{"/workspace": {16 << 20, 16384, true}, "/foundry-deps": {80 << 20, 32768, true}, "/workspace/node_modules": {80 << 20, 32768, true}, "/foundry-build/compiled/node_modules": {80 << 20, 32768, true}}
}
func checkInputMountsV2(stat func(string) (mountObservationV2, error)) error {
	for p, spec := range mountSpecsV2() {
		o, e := stat(p)
		if e != nil || !safeMountV2(o, spec, true) {
			return errGuestV2
		}
	}
	return nil
}
func safeMountV2(o mountObservationV2, s mountSpecV2, readonly bool) bool {
	return o.bytes == s.bytes && o.inodes == s.inodes && o.tmpfs && o.nosuid && o.nodev && (!s.noexec || o.noexec) && (!readonly || o.readonly)
}

func checkMountListV2(raw []byte) error {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return errGuestV2
	}
	allowed := map[string]string{"/": "rootfs", "/proc": "proc", "/dev": "devtmpfs", "/sys": "sysfs", "/sys/fs/cgroup": "cgroup2", "/workspace": "tmpfs", "/foundry-deps": "tmpfs", "/foundry-deps/store": "tmpfs", "/foundry-deps/python": "tmpfs", "/workspace/node_modules": "tmpfs", "/foundry-build/compiled/node_modules": "tmpfs", "/foundry-build/compiled": "tmpfs", "/tmp": "tmpfs", "/foundry-build": "tmpfs"}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		left, right, ok := strings.Cut(line, " - ")
		fields := strings.Fields(left)
		tail := strings.Fields(right)
		if !ok || len(fields) < 6 || len(tail) < 3 {
			return errGuestV2
		}
		kind, ok := allowed[fields[4]]
		if !ok || tail[0] != kind && !(fields[4] == "/" && (tail[0] == "tmpfs" || tail[0] == "ramfs")) {
			return errGuestV2
		}
	}
	return nil
}

func checkScratchV2(stat func(string) (mountObservationV2, error)) error {
	for _, m := range scratchMounts() {
		o, e := stat(m.path)
		if e != nil || !safeMountV2(o, mountSpecV2{m.maxBytes, 16384, m.noexec}, false) || o.noexec != m.noexec || o.readonly {
			return errGuestV2
		}
	}
	return nil
}
