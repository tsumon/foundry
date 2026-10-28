//go:build windows

// Test-only exact private backup/restore for the release upgrade driver.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"foundry/internal/platformfs"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type entry struct {
	rel, digest, security string
	directory             bool
}

func security(path string) (string, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return "", err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return "", errors.New("missing owner")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return "", errors.New("missing DACL")
	}
	return sd.String(), nil
}

func cloneSecurity(source, target string) error {
	sd, err := windows.GetNamedSecurityInfo(source, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return errors.New("missing source owner")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return errors.New("missing source DACL")
	}
	control, _, err := sd.Control()
	if err != nil {
		return err
	}
	flags := windows.SECURITY_INFORMATION(windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION)
	if control&windows.SE_DACL_PROTECTED != 0 {
		flags |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	} else {
		flags |= windows.UNPROTECTED_DACL_SECURITY_INFORMATION
	}
	return windows.SetNamedSecurityInfo(target, windows.SE_FILE_OBJECT, flags, owner, nil, dacl, nil)
}

func inventory(root string) ([]entry, error) {
	var rows []entry
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
			return errors.New("nonregular backup source")
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			rel = ""
		}
		acl, err := security(path)
		if err != nil {
			return err
		}
		row := entry{rel: rel, directory: info.IsDir(), security: acl}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			h := sha256.New()
			_, err = io.Copy(h, f)
			closeErr := f.Close()
			if err != nil || closeErr != nil {
				return errors.New("cannot hash backup file")
			}
			row.digest = hex.EncodeToString(h.Sum(nil))
		}
		rows = append(rows, row)
		return nil
	})
	return rows, err
}

func copyFile(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	syncErr, closeErr := out.Sync(), out.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		return errors.New("cannot copy backup file")
	}
	return nil
}

func copyExact(source, target string) error {
	if !filepath.IsAbs(source) || !filepath.IsAbs(target) || filepath.Clean(source) != source || filepath.Clean(target) != target {
		return errors.New("absolute clean paths required")
	}
	if strings.EqualFold(source, target) {
		return errors.New("backup source and target coincide")
	}
	if rel, err := filepath.Rel(source, target); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("backup target inside source")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return errors.New("fresh backup destination required")
	}
	before, err := inventory(source)
	if err != nil {
		return err
	}
	for _, row := range before {
		to := filepath.Join(target, row.rel)
		if row.directory {
			if err := platformfs.PrivateDirectory(to); err != nil {
				return err
			}
		} else {
			from := filepath.Join(source, row.rel)
			info, err := os.Lstat(from)
			if err != nil {
				return err
			}
			if err := copyFile(from, to, info.Mode()); err != nil {
				return err
			}
		}
	}
	// Apply parent security before child security so inheritance cannot change a
	// child after its owner and DACL have been checked.
	for _, row := range before {
		if err := cloneSecurity(filepath.Join(source, row.rel), filepath.Join(target, row.rel)); err != nil {
			return err
		}
	}
	after, err := inventory(target)
	if err != nil {
		return err
	}
	again, err := inventory(source)
	if err != nil {
		return err
	}
	if len(before) != len(after) || len(before) != len(again) {
		return errors.New("backup inventory count changed")
	}
	for i := range before {
		if before[i] != after[i] || before[i] != again[i] {
			return errors.New("backup bytes or owner/DACL changed")
		}
	}
	return nil
}

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	if err := copyExact(os.Args[1], os.Args[2]); err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"result": "PASS", "owner_dacl_exact": true})
}
