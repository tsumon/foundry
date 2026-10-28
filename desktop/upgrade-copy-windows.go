//go:build windows

// Test-only exact private backup/restore for the release upgrade driver.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/platformfs"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

type entry struct {
	rel, digest string
	security    securityID
	directory   bool
}

type securityID struct {
	owner, aceDigest string
	protected        bool
}

// Compare the owner, protected-DACL state and ordered ACE bytes. Windows may
// normalize descriptor bookkeeping flags without changing these permissions.
func security(path string) (securityID, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return securityID{}, err
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return securityID{}, errors.New("missing owner")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return securityID{}, errors.New("missing DACL")
	}
	control, _, err := sd.Control()
	if err != nil {
		return securityID{}, err
	}
	h := sha256.New()
	revision := *(*byte)(unsafe.Pointer(dacl))
	_, _ = h.Write([]byte{revision, byte(dacl.AceCount), byte(dacl.AceCount >> 8)})
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return securityID{}, err
		}
		if ace.Header.AceSize == 0 {
			return securityID{}, errors.New("empty DACL ACE")
		}
		_, _ = h.Write(unsafe.Slice((*byte)(unsafe.Pointer(ace)), int(ace.Header.AceSize)))
	}
	return securityID{owner: owner.String(), aceDigest: hex.EncodeToString(h.Sum(nil)), protected: control&windows.SE_DACL_PROTECTED != 0}, nil
}

func cloneSecurity(source, target string) error {
	want, err := security(source)
	if err != nil {
		return err
	}
	got, err := security(target)
	if err != nil {
		return err
	}
	if got == want {
		return nil
	}
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
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("private backup source directory required")
	}
	if err := platformfs.PrivateDirectory(source); err != nil {
		return fmt.Errorf("private backup source required: %w", err)
	}
	before, err := inventory(source)
	if err != nil {
		return err
	}
	for _, row := range before {
		from := filepath.Join(source, row.rel)
		to := filepath.Join(target, row.rel)
		if row.directory {
			// Protected roots must be private at creation. An inherited child
			// is created under its already-matched private parent, so Windows
			// derives the same inherited ACEs without a later rewrite.
			if row.security.protected {
				err = platformfs.PrivateDirectory(to)
			} else {
				err = os.Mkdir(to, 0700)
			}
			if err != nil {
				return err
			}
		} else {
			info, err := os.Lstat(from)
			if err != nil {
				return err
			}
			if err := copyFile(from, to, info.Mode()); err != nil {
				return err
			}
		}
		if err := cloneSecurity(from, to); err != nil {
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
		if err := compareEntry(before[i], after[i], "backup"); err != nil {
			return err
		}
		if err := compareEntry(before[i], again[i], "source"); err != nil {
			return err
		}
	}
	return nil
}

func compareEntry(want, got entry, phase string) error {
	if want.rel != got.rel || want.directory != got.directory {
		return fmt.Errorf("%s inventory path/type changed", phase)
	}
	if want.digest != got.digest {
		return fmt.Errorf("%s file content changed at %q", phase, want.rel)
	}
	if want.security.owner != got.security.owner {
		return fmt.Errorf("%s owner changed at %q", phase, want.rel)
	}
	if want.security.protected != got.security.protected {
		return fmt.Errorf("%s DACL protection changed at %q", phase, want.rel)
	}
	if want.security.aceDigest != got.security.aceDigest {
		return fmt.Errorf("%s DACL ACEs changed at %q", phase, want.rel)
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
