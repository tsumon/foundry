package platformfs

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPrivateDirectoryRefusesInheritedPublicRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "private")
	if err := PrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	public := filepath.Join(t.TempDir(), "public")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	if PrivateDirectory(public) == nil {
		t.Fatal("accepted directory without protected private DACL")
	}
}

func TestWindowsReadRetainsAncestorsAndRejectsDirectory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "dir")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(dir, "file")
	if e := os.WriteFile(file, []byte("safe"), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := OpenRegular(file)
	if e != nil {
		t.Fatal(e)
	}
	if e := os.Rename(dir, filepath.Join(root, "moved")); e == nil {
		t.Fatal("ancestor renamed while held")
	}
	f.Close()
	// A directory is never admitted as a regular file, even if open succeeds.
	if f, e := OpenRegular(dir); e == nil {
		f.Close()
		t.Fatal("directory admitted")
	}
}

// Junction creation does not require the symlink privilege. This fixture uses
// only a newly created test-owned directory, never a shell command or real root.
func testJunction(t *testing.T, link, target string) {
	t.Helper()
	if err := os.Mkdir(link, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(link)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	substitute, err := windows.UTF16FromString(`\??\` + target)
	if err != nil {
		t.Fatal(err)
	}
	printName, err := windows.UTF16FromString(target)
	if err != nil {
		t.Fatal(err)
	}
	// REPARSE_DATA_BUFFER: 8-byte header, 8-byte mount-point offsets,
	// then NUL-terminated UTF-16 substitute and display names.
	buf := make([]byte, 16+2*(len(substitute)+len(printName)))
	binary.LittleEndian.PutUint32(buf[0:4], windows.IO_REPARSE_TAG_MOUNT_POINT)
	binary.LittleEndian.PutUint16(buf[4:6], uint16(len(buf)-8))
	binary.LittleEndian.PutUint16(buf[10:12], uint16(2*(len(substitute)-1)))
	binary.LittleEndian.PutUint16(buf[12:14], uint16(2*len(substitute)))
	binary.LittleEndian.PutUint16(buf[14:16], uint16(2*(len(printName)-1)))
	for i, c := range append(substitute, printName...) {
		binary.LittleEndian.PutUint16(buf[16+2*i:18+2*i], c)
	}
	var returned uint32
	if err := windows.DeviceIoControl(h, windows.FSCTL_SET_REPARSE_POINT, &buf[0], uint32(len(buf)), nil, 0, &returned, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsReadRejectsJunctionAncestorAndTerminal(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	link := filepath.Join(root, "junction")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "file"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	testJunction(t, link, target)
	// Prove the fixture really redirects before checking the safety boundary.
	if b, err := os.ReadFile(filepath.Join(link, "file")); err != nil || string(b) != "outside" {
		t.Fatalf("junction fixture: %q %v", b, err)
	}
	if f, err := OpenRegular(filepath.Join(link, "file")); err == nil {
		f.Close()
		t.Fatal("junction ancestor admitted")
	}
	if err := ValidateDirectory(link); err == nil {
		t.Fatal("terminal junction admitted as directory")
	}
	if err := PrivateDirectory(link); err == nil {
		t.Fatal("terminal junction admitted as private root")
	}
}
