package dependencybundle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"foundry/internal/platformfs"
)

var (
	ErrIO                 = errors.New("dependency bundle I/O failed")
	ErrIntegrity          = errors.New("dependency bundle integrity failed")
	ErrCleanup            = errors.New("dependency bundle cleanup failed")
	ErrPublicationUnknown = errors.New("dependency bundle published; durability unknown")
)

type directoryHandle interface {
	io.Closer
	Stat() (os.FileInfo, error)
}

type directory struct {
	root *os.Root
	hold directoryHandle
	info os.FileInfo
}

func (d *directory) close() {
	d.root.Close()
	if d.hold != nil {
		d.hold.Close()
	}
}

// openDirectory admits an existing absolute, canonical, symlink-free directory.
// The controller owns the store and its ancestors; source files may be hostile.
func openDirectory(p string, private bool) (*directory, error) {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == filepath.VolumeName(p)+string(filepath.Separator) {
		return nil, ErrInvalid
	}
	resolved, e := filepath.EvalSymlinks(p)
	if e != nil || resolved != p {
		return nil, ErrInvalid
	}
	hold, e := holdDirectory(p)
	if e != nil {
		return nil, ErrIO
	}
	fail := func() (*directory, error) {
		if hold != nil {
			hold.Close()
		}
		return nil, ErrIO
	}
	info, e := hold.Stat()
	named, ne := os.Lstat(p)
	if e != nil || ne != nil || !info.IsDir() || !identity(info, named) {
		return fail()
	}
	if private && privateDirectory(p, info) != nil {
		return fail()
	}
	r, e := os.OpenRoot(p)
	if e != nil {
		return fail()
	}
	got, e := r.Lstat(".")
	resolved, re := filepath.EvalSymlinks(p)
	if e != nil || re != nil || resolved != p || !identity(info, got) {
		r.Close()
		return fail()
	}
	return &directory{r, hold, info}, nil
}

func same(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
func identity(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Mode() == b.Mode()
}
func checkContext(ctx context.Context) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	return nil
}

// inventory reads directories in bounded batches, and opens each subdirectory
// relative to its parent handle. Only the exact declared ancestors are allowed.
func inventory(ctx context.Context, r *os.Root, want map[string]bool) (map[string]os.FileInfo, error) {
	got := make(map[string]os.FileInfo, len(want))
	var walk func(*os.Root, string) error
	walk = func(current *os.Root, prefix string) error {
		if e := checkContext(ctx); e != nil {
			return e
		}
		f, e := current.Open(".")
		if e != nil {
			return ErrIO
		}
		defer f.Close()
		for {
			entries, e := f.ReadDir(128)
			if e != nil && e != io.EOF {
				return ErrIO
			}
			for _, entry := range entries {
				if e := checkContext(ctx); e != nil {
					return e
				}
				p := entry.Name()
				if prefix != "" {
					p = prefix + "/" + p
				}
				isDir, ok := want[p]
				_, seen := got[p]
				if !ok || seen {
					return ErrIntegrity
				}
				info, e := current.Lstat(entry.Name())
				if e != nil || info.Mode()&os.ModeSymlink != 0 || info.IsDir() != isDir {
					return ErrIntegrity
				}
				if isDir {
					child, e := current.OpenRoot(entry.Name())
					if e != nil {
						return ErrIO
					}
					opened, e := child.Lstat(".")
					if e != nil || !identity(info, opened) {
						child.Close()
						return ErrIntegrity
					}
					got[p] = opened
					e = walk(child, p)
					child.Close()
					if e != nil {
						return e
					}
					after, e := current.Lstat(entry.Name())
					if e != nil || !identity(opened, after) {
						return ErrIntegrity
					}
				} else {
					if !info.Mode().IsRegular() || !payloadMode(info.Mode()) {
						return ErrIntegrity
					}
					got[p] = info
				}
			}
			if e == io.EOF {
				break
			}
		}
		return nil
	}
	if e := walk(r, ""); e != nil {
		return nil, e
	}
	if len(got) != len(want) {
		return nil, ErrIntegrity
	}
	return got, nil
}
func expected(m Manifest, prefix string) map[string]bool {
	want := map[string]bool{}
	for _, f := range m.Files {
		p := prefix + f.Path
		want[p] = false
		for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
			want[parent] = true
		}
	}
	return want
}
func unchanged(before, after map[string]os.FileInfo) bool {
	if len(before) != len(after) {
		return false
	}
	for p, info := range before {
		if !same(info, after[p]) {
			return false
		}
	}
	return true
}
func openFile(r *os.Root, p string) (*platformfs.File, os.FileInfo, error) {
	parts := strings.Split(p, "/")
	for i := 1; i <= len(parts); i++ {
		info, e := r.Lstat(strings.Join(parts[:i], "/"))
		if e != nil || info.Mode()&os.ModeSymlink != 0 || i < len(parts) && !info.IsDir() {
			return nil, nil, ErrIntegrity
		}
	}
	f, e := platformfs.OpenRootRegular(r, p)
	if e != nil {
		return nil, nil, ErrIO
	}
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || !payloadMode(info.Mode()) || singleLink(f.File, info) != nil {
		f.Close()
		return nil, nil, ErrIntegrity
	}
	named, e := r.Lstat(p)
	if e != nil || !same(info, named) {
		f.Close()
		return nil, nil, ErrIntegrity
	}
	return f, info, nil
}

// copyVerified reads at most the declared size plus one byte; hashes are checked
// on the same stable handle used for copying. No filesystem error includes paths.
func copyVerified(ctx context.Context, r *os.Root, p string, meta File, w io.Writer) (os.FileInfo, error) {
	f, info, e := openFile(r, p)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	if info.Size() != meta.Size {
		return nil, ErrIntegrity
	}
	h := sha256.New()
	out := io.MultiWriter(h, w)
	buf := make([]byte, 32*1024)
	remaining := meta.Size + 1
	var n int64
	for remaining > 0 {
		if e = checkContext(ctx); e != nil {
			return nil, e
		}
		chunk := buf
		if remaining < int64(len(chunk)) {
			chunk = chunk[:remaining]
		}
		count, re := f.Read(chunk)
		if count > 0 {
			if n+int64(count) > meta.Size {
				return nil, ErrIntegrity
			}
			written, we := out.Write(chunk[:count])
			if we != nil || written != count {
				return nil, ErrIO
			}
			n += int64(count)
			remaining -= int64(count)
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			return nil, ErrIO
		}
		if count == 0 {
			return nil, ErrIO
		}
	}
	after, e := f.Stat()
	named, ne := r.Lstat(p)
	if e != nil || ne != nil || !same(info, after) || !same(info, named) || singleLink(f.File, after) != nil || n != meta.Size || hex.EncodeToString(h.Sum(nil)) != meta.SHA256 {
		return nil, ErrIntegrity
	}
	return info, nil
}

// ImportFrozen accepts an already admitted, materialized manifest. LockSHA256 is
// the caller's binding of exact lock bytes, not evidence of ecosystem admission.
// root must already be a controller-owned private directory. No existing bundle
// is replaced or repaired. A successful result is durable under platformfs's
// existing platform durability model, not a power-loss certification.
// If the store-root sync fails after publication, the complete bundle is kept
// and its binding is returned with ErrPublicationUnknown. Callers must verify
// that binding rather than automatically deleting or retrying publication.
func ImportFrozen(ctx context.Context, root, sourceDir string, m Manifest) (Binding, error) {
	return importFrozen(ctx, root, sourceDir, m, syncRoot)
}

// The final durability operation is separate from atomic publication.
func importFrozen(ctx context.Context, root, sourceDir string, m Manifest, syncPublished func(*os.Root) error) (result Binding, err error) {
	if e := checkContext(ctx); e != nil {
		return result, e
	}
	raw, e := m.CanonicalJSON()
	if e != nil {
		return result, e
	}
	binding, e := m.Binding()
	if e != nil {
		return result, e
	}
	store, e := openDirectory(root, true)
	if e != nil {
		return result, e
	}
	defer store.close()
	source, e := openDirectory(sourceDir, false)
	if e != nil {
		return result, e
	}
	defer source.close()
	want := expected(m, "")
	before, e := inventory(ctx, source.root, want)
	if e != nil {
		return result, e
	}
	// Validate source bytes even when the destination already exists.
	for _, f := range m.Files {
		info, e := copyVerified(ctx, source.root, f.Path, f, io.Discard)
		if e != nil {
			return result, e
		}
		if !same(before[f.Path], info) {
			return result, ErrIntegrity
		}
	}
	if _, e = store.root.Lstat(binding.BundleDigest); e == nil {
		if e = Verify(ctx, root, binding); e != nil {
			return result, e
		}
		after, e := inventory(ctx, source.root, want)
		if e != nil {
			return result, e
		}
		if !unchanged(before, after) {
			return result, ErrIntegrity
		}
		if e = checkContext(ctx); e != nil {
			return result, e
		}
		if e = checkNamed(sourceDir, source); e != nil {
			return result, e
		}
		if e = checkNamed(root, store); e != nil {
			return result, e
		}
		return binding, nil
	} else if !os.IsNotExist(e) {
		return result, ErrIO
	}
	staging, e := makeStage(store.root)
	if e != nil {
		return result, ErrIO
	}
	own, e := store.root.Lstat(staging)
	if e != nil {
		return result, errors.Join(ErrIO, ErrCleanup)
	}
	published := false
	defer func() {
		if published {
			return
		}
		info, e := store.root.Lstat(staging)
		if e != nil || !identity(own, info) || store.root.RemoveAll(staging) != nil {
			err = errors.Join(err, ErrCleanup)
		}
	}()
	stage, e := store.root.OpenRoot(staging)
	if e != nil {
		return result, ErrIO
	}
	// Close the staging root before publication (Windows does not share deletion).
	stageOpen := true
	defer func() {
		if stageOpen {
			stage.Close()
		}
	}()
	if e = makeDirectory(stage, "payload"); e != nil {
		return result, ErrIO
	}
	for _, f := range m.Files {
		if e = checkContext(ctx); e != nil {
			return result, e
		}
		p := "payload/" + f.Path
		parts := strings.Split(path.Dir(p), "/")
		for i := 1; i <= len(parts); i++ {
			if e = makeDirectory(stage, strings.Join(parts[:i], "/")); e != nil {
				return result, ErrIO
			}
		}
		out, e := stage.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if e != nil {
			return result, ErrIO
		}
		info, copyErr := copyVerified(ctx, source.root, f.Path, f, out)
		if copyErr == nil && !same(before[f.Path], info) {
			copyErr = ErrIntegrity
		}
		if copyErr == nil {
			copyErr = out.Chmod(0644)
		}
		if copyErr == nil {
			copyErr = flush(out)
		}
		closeErr := out.Close()
		if copyErr != nil {
			return result, safeError(copyErr)
		}
		if closeErr != nil {
			return result, ErrIO
		}
	}
	out, e := stage.OpenFile("manifest.json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if e != nil {
		return result, ErrIO
	}
	n, e := out.Write(raw)
	if e == nil && n != len(raw) {
		e = io.ErrShortWrite
	}
	if e == nil {
		e = out.Chmod(0644)
	}
	if e == nil {
		e = flush(out)
	}
	ce := out.Close()
	if e != nil || ce != nil {
		return result, ErrIO
	}
	after, e := inventory(ctx, source.root, want)
	if e != nil {
		return result, e
	}
	if !unchanged(before, after) {
		return result, ErrIntegrity
	}
	// Rehash source once more after copying to detect same-inode content mutation.
	for _, f := range m.Files {
		if _, e = copyVerified(ctx, source.root, f.Path, f, io.Discard); e != nil {
			return result, e
		}
	}
	after, e = inventory(ctx, source.root, want)
	if e != nil {
		return result, e
	}
	if !unchanged(before, after) {
		return result, ErrIntegrity
	}
	if e = verifyRoot(ctx, stage, binding, nil); e != nil {
		return result, e
	}
	if e = syncDirectories(ctx, stage, m); e != nil {
		return result, safeError(e)
	}
	if e = stage.Close(); e != nil {
		return result, ErrIO
	}
	stageOpen = false
	if e = checkContext(ctx); e != nil {
		return result, e
	}
	// The source and store root identities must remain the same named directories.
	if e = checkNamed(sourceDir, source); e != nil {
		return result, e
	}
	if e = checkNamed(root, store); e != nil {
		return result, e
	}
	after, e = inventory(ctx, source.root, want)
	if e != nil {
		return result, e
	}
	if !unchanged(before, after) {
		return result, ErrIntegrity
	}
	if e = checkContext(ctx); e != nil {
		return result, e
	}
	if e = publishAbsent(store.root, staging, binding.BundleDigest); e != nil {
		if _, se := store.root.Lstat(binding.BundleDigest); se != nil {
			return result, ErrIO
		}
		if e = Verify(ctx, root, binding); e != nil {
			return result, e
		}
		return binding, nil
	}
	published = true
	if e = syncPublished(store.root); e != nil {
		return binding, ErrPublicationUnknown
	}
	return binding, nil
}
func safeError(e error) error {
	if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) || errors.Is(e, ErrIntegrity) {
		return e
	}
	return ErrIO
}
func flush(f *os.File) error {
	if e := platformfs.FlushWritten(f); e != nil {
		return e
	}
	return syncFile(f)
}
func checkNamed(p string, d *directory) error {
	named, e := os.Lstat(p)
	opened, oe := d.root.Lstat(".")
	if e != nil || oe != nil || !identity(named, opened) || !identity(d.info, opened) {
		return ErrIntegrity
	}
	return nil
}
func syncDirectories(ctx context.Context, r *os.Root, m Manifest) error {
	dirs := map[string]bool{".": true, "payload": true}
	for p, isDir := range expected(m, "payload/") {
		if isDir {
			dirs[p] = true
		}
	}
	order := make([]string, 0, len(dirs))
	for p := range dirs {
		order = append(order, p)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(order)))
	for _, p := range order {
		if e := checkContext(ctx); e != nil {
			return e
		}
		child, e := r.OpenRoot(p)
		if e != nil {
			return e
		}
		e = syncRoot(child)
		child.Close()
		if e != nil {
			return e
		}
	}
	return nil
}

// Verify reopens the binding-derived directory and checks canonical metadata,
// exact inventory, modes, single-link regular files, sizes and all payload hashes.
func Verify(ctx context.Context, root string, b Binding) error {
	return ReadVerified(ctx, root, b, nil)
}

// ReadVerified validates the whole bundle first, then reopens and rehashes each
// file into a bounded <=8MiB snapshot before calling emit. emit may retain bytes;
// no mutable path is exposed. The binding is unchanged across the whole transfer.
// A callback may receive earlier verified files before a later error; callers must
// discard any incomplete transfer and only commit after ReadVerified returns nil.
func ReadVerified(ctx context.Context, root string, b Binding, emit func(File, []byte) error) error {
	if e := checkContext(ctx); e != nil {
		return e
	}
	if !b.valid() {
		return ErrInvalid
	}
	store, e := openDirectory(root, true)
	if e != nil {
		return e
	}
	defer store.close()
	info, e := store.root.Lstat(b.BundleDigest)
	if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrIntegrity
	}
	r, e := store.root.OpenRoot(b.BundleDigest)
	if e != nil {
		return ErrIO
	}
	defer r.Close()
	opened, e := r.Lstat(".")
	if e != nil || !identity(info, opened) {
		return ErrIntegrity
	}
	if privateDirectory(filepath.Join(root, b.BundleDigest), opened) != nil {
		return ErrIntegrity
	}
	if e = verifyRoot(ctx, r, b, emit); e != nil {
		return e
	}
	named, e := store.root.Lstat(b.BundleDigest)
	if e != nil || !identity(info, named) {
		return ErrIntegrity
	}
	return checkNamed(root, store)
}
func verifyRoot(ctx context.Context, r *os.Root, b Binding, emit func(File, []byte) error) error {
	meta, manifestInfo, e := openFile(r, "manifest.json")
	if e != nil {
		return e
	}
	if manifestInfo.Size() > MaxManifestBytes {
		meta.Close()
		return ErrInvalid
	}
	raw, e := io.ReadAll(io.LimitReader(meta, MaxManifestBytes+1))
	ce := meta.Close()
	if e != nil || ce != nil || len(raw) > MaxManifestBytes {
		return ErrIO
	}
	var m Manifest
	// Requiring exact canonical bytes rejects duplicate/case-alias/unknown keys,
	// null fields, whitespace, alternate numeric spellings and trailing JSON alike.
	if decodeManifest(raw, &m) != nil {
		return ErrInvalid
	}
	canonical, e := m.CanonicalJSON()
	if e != nil || !bytes.Equal(raw, canonical) {
		return ErrInvalid
	}
	derived, e := m.Binding()
	if e != nil || derived != b {
		return ErrIntegrity
	}
	want := expected(m, "payload/")
	want["manifest.json"] = false
	want["payload"] = true
	before, e := inventory(ctx, r, want)
	if e != nil {
		return e
	}
	if !same(manifestInfo, before["manifest.json"]) {
		return ErrIntegrity
	}
	for p, info := range before {
		if info.IsDir() {
			if privateDirectory(filepath.Join(r.Name(), filepath.FromSlash(p)), info) != nil {
				return ErrIntegrity
			}
		} else {
			mode, me := platformfs.CanonicalMode(info.Mode())
			if me != nil || mode != 0644 {
				return ErrIntegrity
			}
		}
	}
	for _, f := range m.Files {
		info, e := copyVerified(ctx, r, "payload/"+f.Path, f, io.Discard)
		if e != nil {
			return e
		}
		if !same(before["payload/"+f.Path], info) {
			return ErrIntegrity
		}
	}
	if emit != nil {
		var total int64
		for _, f := range m.Files {
			if e = checkContext(ctx); e != nil {
				return e
			}
			total += f.Size
			if total > MaxPayloadBytes {
				return ErrInvalid
			}
			var data bytes.Buffer
			data.Grow(int(f.Size))
			info, e := copyVerified(ctx, r, "payload/"+f.Path, f, &data)
			if e != nil {
				return e
			}
			if !same(before["payload/"+f.Path], info) {
				return ErrIntegrity
			}
			if e = checkContext(ctx); e != nil {
				return e
			}
			if e = emit(f, data.Bytes()); e != nil {
				return e
			}
		}
	}
	// Check the manifest again through a fresh rooted handle after payload reads;
	// initial parsing alone is not authority for a subsequently changed manifest.
	if _, e = copyVerified(ctx, r, "manifest.json", File{Path: "manifest.json", Size: int64(len(raw)), SHA256: b.ManifestDigest}, io.Discard); e != nil {
		return e
	}
	after, e := inventory(ctx, r, want)
	if e != nil {
		return e
	}
	if !unchanged(before, after) {
		return ErrIntegrity
	}
	return checkContext(ctx)
}

// decodeManifest bounds the file slice before appending any untrusted records.
// The canonical byte comparison below the decoder remains the closed-shape gate.
func decodeManifest(raw []byte, m *Manifest) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	token := func(want any) bool { got, e := d.Token(); return e == nil && got == want }
	if !token(json.Delim('{')) || !token("schema_version") || d.Decode(&m.SchemaVersion) != nil || !token("ecosystem") || d.Decode(&m.Ecosystem) != nil || !token("lock_sha256") || d.Decode(&m.LockSHA256) != nil || !token("files") || !token(json.Delim('[')) {
		return ErrInvalid
	}
	if m.SchemaVersion != 1 || !ecosystem(m.Ecosystem) || !hashValid(m.LockSHA256) {
		return ErrInvalid
	}
	m.Files = []File{}
	var total int64
	for d.More() {
		if len(m.Files) >= MaxFiles {
			return ErrInvalid
		}
		var f File
		if d.Decode(&f) != nil || !validPath(f.Path) || f.Size < 0 || f.Size > MaxFileBytes || !hashValid(f.SHA256) {
			return ErrInvalid
		}
		total += f.Size
		if total > MaxPayloadBytes {
			return ErrInvalid
		}
		m.Files = append(m.Files, f)
	}
	if !token(json.Delim(']')) || !token(json.Delim('}')) {
		return ErrInvalid
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrInvalid
	}
	return nil
}
