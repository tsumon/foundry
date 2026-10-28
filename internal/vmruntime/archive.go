package vmruntime

import (
	"errors"
	"foundry/internal/domain"
	"foundry/internal/platformfs"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Retain copies only verified local assets into a private, content-addressed
// archive. Expected is full caller-owned provenance, not a release label. It
// never boots a guest or changes the source. Failed staging is left for explicit
// inspection; it can never be loaded as a published runtime.
func Retain(source, archive string, expected domain.C4VMIdentity) (string, error) {
	m, identity, err := loadVerifiedBoot(source, expected.SchemaVersion)
	if err != nil || expected.Validate() != nil || identity != expected {
		return "", errors.New("runtime archive source identity mismatch")
	}
	if !filepath.IsAbs(archive) || filepath.Clean(archive) != archive || validateInstanceLocation(source, filepath.Dir(archive)) != nil {
		return "", errors.New("runtime archive must be outside source")
	}
	release, err := protectArchiveParent(filepath.Dir(archive))
	if err != nil {
		return "", err
	}
	defer release()
	if err := ensureArchiveDirectory(archive); err != nil {
		return "", err
	}
	if err := validateInstanceLocation(source, archive); err != nil {
		return "", err
	}
	target := filepath.Join(archive, expected.ManifestDigest)
	if _, err := os.Lstat(target); err == nil {
		_, actual, e := loadVerifiedBoot(target, expected.SchemaVersion)
		if e != nil || actual != expected || checkArchiveDirectory(target) != nil {
			return "", errors.New("retained runtime integrity mismatch")
		}
		return target, nil
	} else if !os.IsNotExist(err) {
		return "", errors.New("cannot inspect retained runtime")
	}
	entries, err := os.ReadDir(archive)
	if err != nil || len(entries) >= 256 {
		return "", errors.New("runtime archive inventory unavailable or full")
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	stage := filepath.Join(archive, ".staging-"+id)
	if err := createPrivateDirectory(stage); err != nil {
		return "", errors.New("cannot create runtime staging directory")
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return "", errors.New("cannot open runtime source")
	}
	defer root.Close()
	for _, a := range m.Assets {
		parts := strings.Split(a.Path, "/")
		parent := stage
		for _, part := range parts[:len(parts)-1] {
			parent = filepath.Join(parent, part)
			if err := ensureArchiveDirectory(parent); err != nil {
				return "", err
			}
		}
		f, err := openAsset(root, a.Path)
		if err != nil {
			return "", err
		}
		info, statErr := f.Stat()
		f.Close()
		if statErr != nil {
			return "", errors.New("cannot inspect runtime asset mode")
		}
		mode := os.FileMode(0600)
		if info.Mode().Perm()&0111 != 0 {
			mode = 0700
		}
		out, err := os.OpenFile(filepath.Join(stage, filepath.FromSlash(a.Path)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return "", errors.New("cannot create retained runtime asset")
		}
		copyErr := verifyAsset(root, a, out)
		syncErr, closeErr := out.Sync(), out.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil {
			return "", errors.New("cannot retain verified runtime asset")
		}
	}
	manifest, err := openAsset(root, "manifest.json")
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(manifest, 1024*1024+1))
	manifest.Close()
	if err != nil || len(b) > 1024*1024 {
		return "", errors.New("cannot retain runtime manifest")
	}
	out, err := os.OpenFile(filepath.Join(stage, "manifest.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", errors.New("cannot publish runtime manifest")
	}
	_, writeErr := out.Write(b)
	syncErr, closeErr := out.Sync(), out.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return "", errors.New("cannot sync runtime manifest")
	}
	_, actual, err := loadVerifiedBoot(stage, expected.SchemaVersion)
	if err != nil || actual != expected {
		return "", errors.New("staged runtime identity mismatch")
	}
	err = filepath.WalkDir(stage, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return platformfs.SyncPath(path)
		}
		return nil
	})
	if err != nil {
		return "", errors.New("cannot sync runtime archive")
	}
	if err := publishArchive(stage, target); err != nil {
		return "", errors.New("cannot exclusively publish runtime archive")
	}
	if err := platformfs.SyncPath(archive); err != nil {
		return "", errors.New("cannot confirm runtime archive publication")
	}
	return target, nil
}

func ensureArchiveDirectory(path string) error {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		if err := createPrivateDirectory(path); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if err := canonicalDirectory(path); err != nil {
		return err
	}
	return checkArchiveDirectory(path)
}

// LoadRetained is metadata-only. Incomplete staging is never authority; a
// malformed published object fails closed rather than silently picking latest.
func LoadRetained(archive, instances string) ([]*Runtime, error) {
	release, err := protectArchiveParent(filepath.Dir(archive))
	if err != nil {
		return nil, err
	}
	defer release()
	if err := canonicalDirectory(archive); err != nil {
		return nil, err
	}
	if err := checkArchiveDirectory(archive); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(archive)
	if err != nil || len(entries) > 256 {
		return nil, errors.New("runtime archive inventory unavailable or full")
	}
	var result []*Runtime
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".staging-") {
			continue
		}
		if !hexDigest(entry.Name()) || !entry.IsDir() {
			return nil, errors.New("invalid runtime archive entry")
		}
		path := filepath.Join(archive, entry.Name())
		if err := checkArchiveDirectory(path); err != nil {
			return nil, err
		}
		schema, err := bundleSchema(path)
		if err != nil {
			return nil, err
		}
		var r *Runtime
		switch schema {
		case 1:
			r, err = Load(path, instances)
		case 2:
			r, err = LoadV2(path, instances)
		default:
			return nil, errors.New("unknown runtime schema")
		}
		if err != nil || r.Identity().ManifestDigest != entry.Name() {
			return nil, errors.New("retained runtime identity mismatch")
		}
		result = append(result, r)
	}
	return result, nil
}
