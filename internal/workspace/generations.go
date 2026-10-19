package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type generationManifest struct {
	Version    int                   `json:"version"`
	Generation domain.GenerationRef  `json:"generation"`
	Parent     *domain.GenerationRef `json:"parent,omitempty"`
}

// ProposalValidationError distinguishes untrusted edit-policy violations from
// filesystem/durability failures without changing the underlying error text.
type ProposalValidationError struct{ Err error }

func (e *ProposalValidationError) Error() string { return e.Err.Error() }
func (e *ProposalValidationError) Unwrap() error { return e.Err }

// PrepareGeneration publishes a complete, sealed copy without selecting it.
// Source must be the managed base or the verified parent generation tree.
func PrepareGeneration(dir, id, source string, parent *domain.GenerationRef, edits []Edit, scope, protected []string) (domain.GenerationRef, []domain.Change, error) {
	return prepareGeneration(durableFS(), dir, id, source, parent, edits, scope, protected)
}
func prepareGeneration(fsys filesystem, dir, id, source string, parent *domain.GenerationRef, edits []Edit, scope, protected []string) (domain.GenerationRef, []domain.Change, error) {
	fail := func(err error) (domain.GenerationRef, []domain.Change, error) {
		return domain.GenerationRef{}, nil, err
	}
	if !validHex(id, 32) {
		return fail(errors.New("invalid generation ID"))
	}
	if err := realDirectory(dir); err != nil {
		return fail(err)
	}
	allowed := filepath.Clean(source) == filepath.Join(dir, "base")
	if parent != nil {
		if err := VerifyGeneration(dir, *parent); err != nil {
			return fail(err)
		}
		parentPath, err := GenerationPath(dir, *parent)
		if err != nil {
			return fail(err)
		}
		allowed = allowed || filepath.Clean(source) == parentPath
	}
	if !allowed {
		return fail(errors.New("source must be managed base or verified parent generation"))
	}
	files, err := readTree(source)
	if err != nil {
		return fail(err)
	}
	before, err := Digest(source)
	if err != nil {
		return fail(err)
	}
	src, err := os.OpenRoot(source)
	if err != nil {
		return fail(err)
	}
	defer src.Close()
	modes := map[string]os.FileMode{}
	for p := range files {
		info, err := src.Stat(p)
		if err != nil {
			return fail(err)
		}
		modes[p] = info.Mode().Perm()
	}
	changes := []domain.Change{}
	if len(edits) > 30 {
		return fail(&ProposalValidationError{errors.New("too many edits")})
	}
	seen := map[string]bool{}
	for _, edit := range edits {
		if err := ValidateEditPath(edit.Path, scope, protected); err != nil {
			return fail(&ProposalValidationError{err})
		}
		if len(edit.Content) > 512*1024 || !utf8.ValidString(edit.Content) || strings.ContainsRune(edit.Content, 0) {
			return fail(&ProposalValidationError{errors.New("edit exceeds text bounds")})
		}
		if seen[strings.ToLower(edit.Path)] {
			return fail(&ProposalValidationError{errors.New("duplicate edit")})
		}
		seen[strings.ToLower(edit.Path)] = true
		changes = append(changes, domain.Change{Path: edit.Path, Before: string(files[edit.Path]), After: edit.Content})
		files[edit.Path] = []byte(edit.Content)
		if _, ok := modes[edit.Path]; !ok {
			modes[edit.Path] = 0644
		}
	}
	if err := validateTreePaths(files); err != nil {
		return fail(&ProposalValidationError{err})
	}
	container, err := managedContainer(fsys, dir, "generations")
	if err != nil {
		return fail(err)
	}
	final := filepath.Join(container, id)
	if _, err := os.Lstat(final); !os.IsNotExist(err) {
		return fail(errors.New("generation destination occupied or unavailable"))
	}
	stage, err := os.MkdirTemp(container, ".stage-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(stage)
	tree := filepath.Join(stage, "tree")
	if err := os.Mkdir(tree, 0755); err != nil {
		return fail(err)
	}
	keys := make([]string, 0, len(files))
	for p := range files {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		name := filepath.Join(tree, p)
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			return fail(err)
		}
		if err := fsys.write(name, files[p], modes[p]); err != nil {
			return fail(err)
		}
	}
	digest, err := Digest(tree)
	if err != nil {
		return fail(err)
	}
	if after, err := Digest(source); err != nil || after != before {
		return fail(errors.New("source changed during preparation"))
	}
	ref := domain.GenerationRef{ID: id, Digest: digest}
	if err := syncTree(fsys, tree); err != nil {
		return fail(err)
	}
	seal, err := json.Marshal(generationManifest{Version: 1, Generation: ref, Parent: parent})
	if err != nil {
		return fail(err)
	}
	if err := fsys.write(filepath.Join(stage, "manifest.json"), seal, 0600); err != nil {
		return fail(err)
	}
	if err := fsys.sync(filepath.Join(stage, "manifest.json")); err != nil {
		return fail(err)
	}
	if err := fsys.sync(stage); err != nil {
		return fail(err)
	}
	if err := fsys.publish(stage, final); err != nil {
		return fail(err)
	}
	if err := fsys.sync(container); err != nil {
		return fail(err)
	}
	return ref, changes, nil
}

// GenerationPath resolves an existing real tree; use VerifyGeneration before use.
func GenerationPath(dir string, ref domain.GenerationRef) (string, error) {
	if !validHex(ref.ID, 32) || !validHex(ref.Digest, 64) {
		return "", errors.New("invalid generation reference")
	}
	for _, p := range []string{dir, filepath.Join(dir, "generations"), filepath.Join(dir, "generations", ref.ID), filepath.Join(dir, "generations", ref.ID, "tree")} {
		if err := realDirectory(p); err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, "generations", ref.ID, "tree"), nil
}

// VerifyGeneration checks identity, seal structure, and the actual tree digest.
func VerifyGeneration(dir string, ref domain.GenerationRef) error {
	tree, err := GenerationPath(dir, ref)
	if err != nil {
		return err
	}
	if err := exactEntries(filepath.Dir(tree), "tree", "manifest.json"); err != nil {
		return err
	}
	var manifest generationManifest
	if err := readManifest(filepath.Join(filepath.Dir(tree), "manifest.json"), &manifest); err != nil {
		return err
	}
	if manifest.Version != 1 || manifest.Generation != ref {
		return errors.New("generation seal mismatch")
	}
	if manifest.Parent != nil && (!validHex(manifest.Parent.ID, 32) || !validHex(manifest.Parent.Digest, 64) || manifest.Parent.ID == ref.ID) {
		return errors.New("invalid parent reference")
	}
	digest, err := Digest(tree)
	if err != nil {
		return err
	}
	if digest != ref.Digest {
		return errors.New("generation digest mismatch")
	}
	return nil
}

func validHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func realDirectory(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return errors.New("clean absolute managed path required")
	}
	info, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("managed root must be a real directory")
	}
	return nil
}
func managedContainer(fsys filesystem, dir, name string) (string, error) {
	if err := realDirectory(dir); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	if err := os.Mkdir(p, 0700); err != nil && !os.IsExist(err) {
		return "", err
	}
	if err := realDirectory(p); err != nil {
		return "", err
	}
	if err := fsys.sync(dir); err != nil {
		return "", err
	}
	return p, nil
}
func validateTreePaths(files map[string][]byte) error {
	total := 0
	paths := map[string]string{}
	kinds := map[string]bool{}
	for p, b := range files {
		total += len(b)
		if err := safePath(p); err != nil {
			return err
		}
		for part := p; part != "."; part = filepath.Dir(part) {
			key := strings.ToLower(part)
			isFile := part == p
			if old, ok := paths[key]; ok && (old != part || kinds[key] != isFile) {
				return errors.New("case collision or file/directory conflict")
			}
			paths[key] = part
			kinds[key] = isFile
		}
	}
	if len(files) > 500 || total > MaxSnapshotBytes {
		return errors.New("candidate bounds exceeded")
	}
	return nil
}
func exactEntries(dir string, names ...string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != len(names) {
		return errors.New("unexpected sealed directory entries")
	}
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	for _, entry := range entries {
		if !allowed[entry.Name()] || entry.Type()&os.ModeSymlink != 0 {
			return errors.New("unexpected sealed directory entry")
		}
	}
	return nil
}
func readBoundedFile(p string, limit int64) ([]byte, error) {
	info, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid bounded regular file")
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("file changed during read")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("file exceeds bound")
	}
	return b, nil
}
func readManifest(p string, target any) error {
	b, err := readBoundedFile(p, 16384)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("trailing manifest input")
	}
	return nil
}
