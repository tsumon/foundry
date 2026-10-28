package dependencyadmission

import (
	"context"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/dependencybundle"
	"foundry/internal/platformfs"
)

type pythonDirectoryHandle interface {
	io.Closer
	Stat() (os.FileInfo, error)
}
type pythonDirectory struct {
	root *os.Root
	hold pythonDirectoryHandle
	info os.FileInfo
}

func (d *pythonDirectory) close() { d.root.Close(); d.hold.Close() }
func pythonSame(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
func pythonIdentity(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Mode() == b.Mode()
}
func openPythonDirectory(p string, private bool) (*pythonDirectory, error) {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == filepath.VolumeName(p)+string(filepath.Separator) {
		return nil, errInvalid
	}
	resolved, e := filepath.EvalSymlinks(p)
	if e != nil || resolved != p {
		return nil, errInvalid
	}
	hold, e := pythonHoldDirectory(p)
	if e != nil {
		return nil, dependencybundle.ErrIO
	}
	fail := func() (*pythonDirectory, error) { hold.Close(); return nil, dependencybundle.ErrIntegrity }
	info, e := hold.Stat()
	named, ne := os.Lstat(p)
	if e != nil || ne != nil || !info.IsDir() || !pythonIdentity(info, named) {
		return fail()
	}
	if private && pythonPrivateDirectory(p, info) != nil {
		return fail()
	}
	root, e := os.OpenRoot(p)
	if e != nil {
		return fail()
	}
	got, e := root.Lstat(".")
	resolved, re := filepath.EvalSymlinks(p)
	if e != nil || re != nil || resolved != p || !pythonIdentity(info, got) {
		root.Close()
		return fail()
	}
	return &pythonDirectory{root, hold, info}, nil
}
func (d *pythonDirectory) checkNamed(p string) error {
	named, e := os.Lstat(p)
	opened, oe := d.root.Lstat(".")
	resolved, re := filepath.EvalSymlinks(p)
	if e != nil || oe != nil || re != nil || resolved != p || !pythonIdentity(named, opened) || !pythonIdentity(d.info, opened) {
		return dependencybundle.ErrIntegrity
	}
	return nil
}
func pythonInventory(ctx context.Context, d *pythonDirectory, wheels []pythonWheel) (map[string]os.FileInfo, error) {
	expected := map[string]pythonWheel{}
	for _, w := range wheels {
		expected[w.Filename] = w
	}
	f, e := d.root.Open(".")
	if e != nil {
		return nil, dependencybundle.ErrIO
	}
	defer f.Close()
	got := map[string]os.FileInfo{}
	for {
		entries, e := f.ReadDir(64)
		if e != nil && e != io.EOF {
			return nil, dependencybundle.ErrIO
		}
		for _, entry := range entries {
			if e := ctx.Err(); e != nil {
				return nil, e
			}
			w, ok := expected[entry.Name()]
			if !ok || got[entry.Name()] != nil {
				return nil, errors.New("extraneous_dependency")
			}
			info, e := d.root.Lstat(entry.Name())
			if e != nil || !info.Mode().IsRegular() || info.Size() != w.Size {
				return nil, dependencybundle.ErrIntegrity
			}
			got[entry.Name()] = info
		}
		if e == io.EOF {
			break
		}
	}
	if len(got) != len(wheels) {
		return nil, errors.New("missing_dependency")
	}
	return got, nil
}
func readPythonArchive(ctx context.Context, d *pythonDirectory, w pythonWheel, before os.FileInfo) ([]byte, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	f, e := platformfs.OpenRootRegular(d.root, w.Filename)
	if e != nil {
		return nil, dependencybundle.ErrIO
	}
	info, e := f.Stat()
	named, ne := d.root.Lstat(w.Filename)
	if e != nil || ne != nil || !info.Mode().IsRegular() || !pythonSame(before, info) || !pythonSame(info, named) || info.Size() != w.Size || !pythonSingleLink(f.File, info) {
		f.Close()
		return nil, dependencybundle.ErrIntegrity
	}
	// Allocations are preceded by lock, inventory and handle size checks. Read in
	// bounded chunks so cancellation does not wait for an entire large archive.
	data := make([]byte, 0, int(w.Size))
	buf := make([]byte, 32<<10)
	for {
		if e = ctx.Err(); e != nil {
			f.Close()
			return nil, e
		}
		n, re := f.Read(buf)
		if int64(len(data))+int64(n) > w.Size {
			f.Close()
			return nil, dependencybundle.ErrIntegrity
		}
		data = append(data, buf[:n]...)
		if re == io.EOF {
			break
		}
		if re != nil {
			f.Close()
			return nil, dependencybundle.ErrIO
		}
	}
	after, se := f.Stat()
	named, ne = d.root.Lstat(w.Filename)
	single := se == nil && pythonSingleLink(f.File, after)
	ce := f.Close()
	if se != nil || ne != nil || ce != nil || !single || !pythonSame(info, after) || !pythonSame(info, named) || int64(len(data)) != w.Size || sum(data) != w.SHA256 {
		return nil, dependencybundle.ErrIntegrity
	}
	return data, nil
}
func pythonGraph(l pythonLock, wheels []admittedWheel) ([]DependencyEdge, error) {
	byName := map[string]admittedWheel{}
	for _, w := range wheels {
		byName[w.identity.Name] = w
	}
	edges := []DependencyEdge{}
	add := func(from, name, requirement, spec, status string) error {
		if len(edges) >= maxEdges {
			return errInvalid
		}
		to := ""
		if status == "active" {
			target, ok := byName[name]
			if !ok {
				return errors.New("missing_dependency")
			}
			if spec != "" {
				yes, e := pythonSatisfies(spec, target.identity.Version)
				if e != nil {
					return errors.New("unsupported_dependency_spec")
				}
				if !yes {
					return errors.New("dependency_version_mismatch")
				}
			}
			to = target.identity.Location
		}
		edges = append(edges, DependencyEdge{From: from, Name: name, To: to, Requirement: requirement, Status: status})
		return nil
	}
	for _, r := range l.Roots {
		target, ok := byName[r.Name]
		if !ok {
			return nil, errors.New("missing_dependency")
		}
		if r.Version != target.identity.Version {
			return nil, errors.New("dependency_version_mismatch")
		}
		if e := add("", r.Name, "=="+r.Version, "=="+r.Version, "active"); e != nil {
			return nil, e
		}
	}
	for _, w := range wheels {
		for _, raw := range w.requirements {
			name, spec, status, e := pythonRequirement(raw, w.extras)
			if e != nil {
				return nil, pythonRequirementError(raw)
			}
			if e = add(w.identity.Location, name, raw, spec, status); e != nil {
				return nil, e
			}
		}
	}
	seen := map[string]bool{"": true}
	for changed := true; changed; {
		changed = false
		for _, e := range edges {
			if e.Status == "active" && seen[e.From] && !seen[e.To] {
				seen[e.To] = true
				changed = true
			}
		}
	}
	if len(seen) != len(wheels)+1 {
		return nil, errors.New("extraneous_dependency")
	}
	return edges, nil
}
func pythonRequirementError(s string) error {
	if strings.Contains(s, ";") {
		return errors.New("unsupported_marker")
	}
	if strings.Contains(s, "[") {
		return errors.New("unsupported_extra")
	}
	return errors.New("unsupported_dependency_spec")
}

// pythonCleanupError retains the owned path solely for internal diagnostics. Its
// public string is closed and contains no input/store paths or payload bytes.
type pythonCleanupError struct{ retainedPath string }

func (*pythonCleanupError) Error() string { return "dependency staging cleanup failed" }
func (*pythonCleanupError) Unwrap() error { return dependencybundle.ErrCleanup }

// ImportPython verifies a supplied exact pure-wheel closure without executing
// package code. Archive hashes establish agreement with the user's lock only.
func ImportPython(ctx context.Context, storeRoot, wheelDir string, lock SourceBytes) (result AdmissionReceipt, err error) {
	if e := ctx.Err(); e != nil {
		return result, e
	}
	lock, e := ownedSource(lock, pythonLockPath)
	if e != nil {
		return result, e
	}
	l, e := parsePythonLock(lock.Data)
	if e != nil {
		return result, e
	}
	source, e := openPythonDirectory(wheelDir, false)
	if e != nil {
		return result, e
	}
	defer source.close()
	before, e := pythonInventory(ctx, source, l.Wheels)
	if e != nil {
		return result, e
	}
	wheels := make([]admittedWheel, 0, len(l.Wheels))
	files := map[string][]byte{}
	var total int64
	entries := 0
	edgeCount := len(l.Roots)
	for _, w := range l.Wheels {
		b, e := readPythonArchive(ctx, source, w, before[w.Filename])
		if e != nil {
			return result, e
		}
		admitted, e := admitWheel(ctx, w, b, &total, &entries)
		if e != nil {
			return result, e
		}
		if len(admitted.requirements) > maxEdges-edgeCount {
			return result, errInvalid
		}
		edgeCount += len(admitted.requirements)
		for p, b := range admitted.files {
			if _, ok := files[p]; ok {
				return result, errors.New("unsafe_dependency_layout")
			}
			files[p] = b
		}
		wheels = append(wheels, admitted)
	}
	receipt := AdmissionReceipt{SchemaVersion: 1, Policy: pythonPolicy, SourceFiles: []SourceDigest{sourceDigest(lock)}, Packages: []PackageIdentity{}, Edges: []DependencyEdge{}, Provenance: "user_supplied_wheels"}
	for _, w := range wheels {
		receipt.Packages = append(receipt.Packages, w.identity)
	}
	receipt.Edges, e = pythonGraph(l, wheels)
	if e != nil {
		return result, e
	}
	canonicalReceipt(&receipt)
	manifest := dependencybundle.Manifest{SchemaVersion: 1, Ecosystem: "python", LockSHA256: sum(lock.Data), Files: []dependencybundle.File{}}
	for p, b := range files {
		manifest.Files = append(manifest.Files, dependencybundle.File{Path: p, Size: int64(len(b)), SHA256: sum(b)})
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	binding, e := manifest.Binding()
	if e != nil {
		return result, e
	}
	receipt, e = finishReceipt(receipt, binding, nil)
	if e != nil {
		return result, e
	}
	// Re-read every selected archive and the exact flat inventory after all graph
	// checks. A mutable external tree never becomes the staging source.
	if e = checkPythonInputs(ctx, source, wheelDir, l.Wheels, before); e != nil {
		return result, e
	}
	store, e := openPythonDirectory(storeRoot, true)
	if e != nil {
		return result, e
	}
	defer store.close()
	stagePath, e := platformfs.PrivateTemp(storeRoot, ".python-admission-")
	if e != nil {
		return result, dependencybundle.ErrIO
	}
	stageName := filepath.Base(stagePath)
	own, e := store.root.Lstat(stageName)
	if e != nil {
		return result, &pythonCleanupError{stagePath}
	}
	defer func() {
		named, e := store.root.Lstat(stageName)
		if e != nil || !pythonIdentity(own, named) || store.root.RemoveAll(stageName) != nil {
			err = errors.Join(err, &pythonCleanupError{stagePath})
			result.Digest = ""
		}
	}()
	stage, e := store.root.OpenRoot(stageName)
	if e != nil {
		return result, dependencybundle.ErrIO
	}
	stageOpen := true
	defer func() {
		if stageOpen {
			stage.Close()
		}
	}()
	for _, f := range manifest.Files {
		if e = ctx.Err(); e != nil {
			return result, e
		}
		if parent := path.Dir(f.Path); parent != "." {
			parts := strings.Split(parent, "/")
			for i := 1; i <= len(parts); i++ {
				if e = platformfs.PrivateDirectory(filepath.Join(stagePath, filepath.FromSlash(strings.Join(parts[:i], "/")))); e != nil {
					return result, dependencybundle.ErrIO
				}
			}
		}
		out, e := stage.OpenFile(f.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return result, dependencybundle.ErrIO
		}
		n, we := out.Write(files[f.Path])
		ce := out.Close()
		if we != nil || ce != nil || int64(n) != f.Size {
			return result, dependencybundle.ErrIO
		}
	}
	if e = stage.Close(); e != nil {
		return result, dependencybundle.ErrIO
	}
	stageOpen = false
	if e = store.checkNamed(storeRoot); e != nil {
		return result, e
	}
	if e = checkPythonInputs(ctx, source, wheelDir, l.Wheels, before); e != nil {
		return result, e
	}
	binding, e = dependencybundle.ImportFrozen(ctx, storeRoot, stagePath, manifest)
	return finishReceipt(receipt, binding, e)
}
func checkPythonInputs(ctx context.Context, d *pythonDirectory, p string, wheels []pythonWheel, before map[string]os.FileInfo) error {
	after, e := pythonInventory(ctx, d, wheels)
	if e != nil {
		return e
	}
	for _, w := range wheels {
		if !pythonSame(before[w.Filename], after[w.Filename]) {
			return dependencybundle.ErrIntegrity
		}
		if _, e = readPythonArchive(ctx, d, w, before[w.Filename]); e != nil {
			return e
		}
	}
	after, e = pythonInventory(ctx, d, wheels)
	if e != nil {
		return e
	}
	for p, info := range before {
		if !pythonSame(info, after[p]) {
			return dependencybundle.ErrIntegrity
		}
	}
	if e = d.checkNamed(p); e != nil {
		return e
	}
	return ctx.Err()
}
