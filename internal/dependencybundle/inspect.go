package dependencybundle

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sort"
	"strings"
)

// InspectFrozen discovers a bounded materialized tree using the store's protected
// file reads. inspect receives owned snapshots, but has no publication authority.
// Earlier observations must be discarded on any error. The returned manifest is
// authoritative only for these observed bytes; ImportFrozen rechecks it again.
func InspectFrozen(ctx context.Context, sourceDir, ecosystem, lockSHA256 string, inspect func(File, []byte) error) (Manifest, error) {
	m := Manifest{SchemaVersion: 1, Ecosystem: ecosystem, LockSHA256: lockSHA256, Files: []File{}}
	emptyJSON, e := m.CanonicalJSON()
	if e != nil {
		return Manifest{}, e
	}
	manifestBytes := len(emptyJSON)
	if e := checkContext(ctx); e != nil {
		return Manifest{}, e
	}
	source, e := openDirectory(sourceDir, false)
	if e != nil {
		return Manifest{}, e
	}
	defer source.close()
	before := map[string]os.FileInfo{}
	aliases := map[string]string{}
	var total int64
	var walk func(*os.Root, string) error
	walk = func(r *os.Root, prefix string) error {
		f, e := r.Open(".")
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
				if !validPath(p) || len(before) >= MaxFiles*33 {
					return ErrInvalid
				}
				key := strings.ToLower(p)
				if _, ok := aliases[key]; ok {
					return ErrInvalid
				}
				aliases[key] = p
				info, e := r.Lstat(entry.Name())
				if e != nil || info.Mode()&os.ModeSymlink != 0 {
					return ErrIntegrity
				}
				before[p] = info
				if info.IsDir() {
					child, e := r.OpenRoot(entry.Name())
					if e != nil {
						return ErrIO
					}
					opened, e := child.Lstat(".")
					if e != nil || !identity(info, opened) {
						child.Close()
						return ErrIntegrity
					}
					e = walk(child, p)
					child.Close()
					if e != nil {
						return e
					}
					after, e := r.Lstat(entry.Name())
					if e != nil || !identity(info, after) {
						return ErrIntegrity
					}
				} else {
					if !info.Mode().IsRegular() || !payloadMode(info.Mode()) {
						return ErrIntegrity
					}
					if info.Size() < 0 || info.Size() > MaxFileBytes || len(m.Files) >= MaxFiles {
						return ErrInvalid
					}
					total += info.Size()
					if total > MaxPayloadBytes {
						return ErrInvalid
					}
					// Validate hardlink count on a protected handle before any payload read.
					file, opened, e := openFile(source.root, p)
					if e != nil {
						return e
					}
					file.Close()
					if !same(info, opened) {
						return ErrIntegrity
					}
					meta := File{Path: p, Size: info.Size(), SHA256: sum(nil)}
					raw, _ := json.Marshal(meta)
					extra := len(raw)
					if len(m.Files) > 0 {
						extra++
					}
					if manifestBytes+extra > MaxManifestBytes {
						return ErrInvalid
					}
					manifestBytes += extra
					m.Files = append(m.Files, meta)
				}
			}
			if e == io.EOF {
				break
			}
		}
		return nil
	}
	if e = walk(source.root, ""); e != nil {
		return Manifest{}, e
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	if _, e = m.CanonicalJSON(); e != nil {
		return Manifest{}, e
	}
	for i := range m.Files {
		meta := &m.Files[i]
		file, info, e := openFile(source.root, meta.Path)
		if e != nil {
			return Manifest{}, e
		}
		if !same(before[meta.Path], info) {
			file.Close()
			return Manifest{}, ErrIntegrity
		}
		data, e := io.ReadAll(io.LimitReader(file, meta.Size+1))
		after, se := file.Stat()
		named, ne := source.root.Lstat(meta.Path)
		var le error
		if se == nil {
			le = singleLink(file.File, after)
		}
		ce := file.Close()
		if e != nil || ce != nil {
			return Manifest{}, ErrIO
		}
		if se != nil || ne != nil || le != nil || int64(len(data)) != meta.Size || !same(info, after) || !same(info, named) {
			return Manifest{}, ErrIntegrity
		}
		meta.SHA256 = sum(data)
		if e = checkContext(ctx); e != nil {
			return Manifest{}, e
		}
		if inspect != nil {
			if e = inspect(*meta, data); e != nil {
				return Manifest{}, e
			}
		}
	}
	if _, e = m.CanonicalJSON(); e != nil {
		return Manifest{}, e
	}
	want := expected(m, "")
	after, e := inventory(ctx, source.root, want)
	if e != nil {
		return Manifest{}, e
	}
	if !unchanged(before, after) {
		return Manifest{}, ErrIntegrity
	}
	for _, f := range m.Files {
		if _, e = copyVerified(ctx, source.root, f.Path, f, io.Discard); e != nil {
			return Manifest{}, e
		}
	}
	after, e = inventory(ctx, source.root, want)
	if e != nil {
		return Manifest{}, e
	}
	if !unchanged(before, after) {
		return Manifest{}, ErrIntegrity
	}
	if e = checkNamed(sourceDir, source); e != nil {
		return Manifest{}, e
	}
	if e = checkContext(ctx); e != nil {
		return Manifest{}, e
	}
	return m, nil
}
