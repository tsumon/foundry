//go:build windows

package vmruntime

import (
	"context"
	"errors"
	"foundry/internal/platformfs"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsPrivateTildeDirectoryKeepsExclusiveOwnerProtection(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "expat_2.9.0-1~deb13u1.debian.tar.xz")
	for _, p := range []string{parent, filepath.Join(parent, "debian")} {
		if err := createPrivateDirectory(p); err != nil {
			t.Fatalf("create literal tilde directory: %v", err)
		}
		if err := platformfs.PrivateDirectory(p); err != nil {
			t.Fatalf("owner/protected DACL postcheck: %v", err)
		}
		if err := createPrivateDirectory(p); !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			t.Fatalf("existing directory must not become a new owner: %v", err)
		}
	}
	public := filepath.Join(t.TempDir(), "public~deb")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	if err := platformfs.PrivateDirectory(public); err == nil {
		t.Fatal("tilde directory bypassed protected DACL requirement")
	}
	ptr, err := windows.UTF16PtrFromString(parent)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 32768)
	n, err := windows.GetShortPathName(ptr, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || n >= uint32(len(buf)) {
		t.Fatalf("short-name availability: %d %v", n, err)
	}
	base := filepath.Base(windows.UTF16ToString(buf[:n]))
	if strings.EqualFold(base, filepath.Base(parent)) {
		t.Log("volume did not mint a short name; exact long-name creation and DACL/exclusivity assertions completed")
		return
	}
	alias := filepath.Join(filepath.Dir(parent), base)
	if err := createPrivateDirectory(filepath.Join(alias, "must-not-create")); err == nil {
		t.Fatal("creation through a DOS alias ancestor admitted")
	}
	if _, err := os.Lstat(filepath.Join(parent, "must-not-create")); !os.IsNotExist(err) {
		t.Fatalf("failed alias admission created a directory: %v", err)
	}
	if err := platformfs.PrivateDirectory(alias); err == nil {
		t.Fatal("existing DOS alias admitted as private directory")
	}
	if err := os.Rename(parent, parent+"-moved"); err != nil {
		t.Fatalf("failed acquisition leaked directory handles: %v", err)
	}
}

func TestWindowsSessionCreatesProtectedExclusiveDirectory(t *testing.T) {
	for _, privateParent := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-parent", true: "protected-parent"}[privateParent], func(t *testing.T) {
			r, q := testRuntime(t)
			if privateParent {
				parent := filepath.Join(r.instanceRoot, "private")
				if e := platformfs.PrivateDirectory(parent); e != nil {
					t.Fatal(e)
				}
				r.instanceRoot = parent
			}
			s, e := r.Create(context.Background(), q)
			if e != nil {
				t.Fatal(e)
			}
			if e = platformfs.PrivateDirectory(s.dir); e != nil {
				t.Fatalf("session DACL lacks exact protection: %v", e)
			}
			if createPrivateDirectory(s.dir) == nil {
				t.Fatal("existing instance admitted")
			}
			if e = s.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
		})
	}
}
