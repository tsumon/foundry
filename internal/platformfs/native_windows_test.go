package platformfs

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsReadTildePathsPreserveBytesAndSharedAdmission(t *testing.T) {
	root := nativeFixtureRoot(t)
	openedRoot, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer openedRoot.Close()
	for _, rel := range []string{
		"libexpat1_2.9.0-1~deb13u1_amd64.deb",
		"legal/source-licenses/expat_2.9.0-1~deb13u1.debian.tar.xz/debian/copyright",
		"legal/source-licenses/openssl_3.5.7-1~deb13u3.debian.tar.xz/debian/copyright",
	} {
		t.Run(rel, func(t *testing.T) {
			p := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
				t.Fatal(err)
			}
			want := []byte("owned immutable fixture: " + rel)
			if err := os.WriteFile(p, want, 0600); err != nil {
				t.Fatal(err)
			}
			if ValidateRelative(rel) == nil || ValidateAbsolute(p) == nil {
				t.Fatal("shared project admission gained tilde support")
			}
			for _, rootRelative := range []bool{false, true} {
				var f *File
				if rootRelative {
					f, err = OpenRootRegular(openedRoot, rel)
				} else {
					f, err = OpenRegular(p)
				}
				if err != nil {
					t.Fatalf("read root-relative=%v: %v", rootRelative, err)
				}
				got, readErr := io.ReadAll(f)
				closeErr := f.Close()
				if readErr != nil || closeErr != nil || string(got) != string(want) || sha256.Sum256(got) != sha256.Sum256(want) {
					t.Fatalf("exact read/hash root-relative=%v: %v, close %v", rootRelative, readErr, closeErr)
				}
			}
			if err := ValidateDirectory(filepath.Dir(p)); err != nil {
				t.Fatalf("literal tilde ancestor directory: %v", err)
			}
		})
	}
}

func TestWindowsReadLexicalValidationPreservesOtherRejections(t *testing.T) {
	for _, p := range []string{`C:\owned\expat_2.9.0-1~deb13u1.debian.tar.xz`, `D:/owned/用户~deb/copyright`} {
		if err := validateNativeAbsolute(p); err != nil {
			t.Fatalf("literal tilde path: %v", err)
		}
	}
	for _, p := range []string{`\\server\share\a~deb`, `\\?\C:\a~deb`, `C:relative~deb`, `C:\a~deb:stream`, `C:\a~deb\CON.txt`, `C:\a~deb\NUL`, `C:\a~deb\COM¹`, `C:\a~deb\LPT9.txt`, `C:\a~deb.`, `C:\a~deb `, `C:\a~deb\..\b`, "C:\\a~deb\\nul\x00"} {
		if validateNativeAbsolute(p) == nil {
			t.Errorf("read admitted unsupported spelling %q", p)
		}
	}
	for _, p := range []string{"", "../a~deb", "/a~deb", "a~deb//file", `a~deb\..\file`, "a~deb:stream"} {
		if validateNativeRelative(p) == nil {
			t.Errorf("relative read admitted unsupported spelling %q", p)
		}
	}
}

func TestWindowsFinalPathIdentityFailsClosed(t *testing.T) {
	const requested = `C:\owned\expat_2.9.0-1~deb13u1.debian.tar.xz`
	for _, tc := range []struct {
		name, resolved string
		count          uint32
		err            error
		want           bool
	}{
		{name: "literal tilde", resolved: `\\?\` + requested, want: true},
		{name: "case only", resolved: `\\?\c:\OWNED\EXPAT_2.9.0-1~DEB13U1.DEBIAN.TAR.XZ`, want: true},
		{name: "different object", resolved: `\\?\C:\private\secret-file`},
		{name: "wrong namespace", resolved: `\\.\` + requested},
		{name: "no extended prefix", resolved: requested},
		{name: "embedded NUL", resolved: `\\?\` + requested + "\x00hidden"},
		{name: "inconsistent count", resolved: `\\?\` + requested, count: 1},
		{name: "zero result"},
		{name: "exact buffer overflow", count: 32768},
		{name: "larger overflow", count: 32769},
		{name: "API error", resolved: `\\?\` + requested, err: errors.New("private resolved path must not escape")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			query := func(h windows.Handle, out *uint16, size, flags uint32) (uint32, error) {
				calls++
				if h != windows.Handle(41) || size != 32768 || flags != 0 {
					t.Fatalf("identity query handle=%v size=%d flags=%d", h, size, flags)
				}
				buf := unsafe.Slice(out, int(size))
				// UTF16FromString rejects NUL; this intentionally malformed API
				// response must also be rejected rather than truncated to a match.
				encoded := utf16.Encode([]rune(tc.resolved))
				copy(buf, encoded)
				if tc.count != 0 {
					return tc.count, tc.err
				}
				return uint32(len(encoded)), tc.err
			}
			err := verifyFinalPath(windows.Handle(41), requested, query)
			if calls != 1 || (err == nil) != tc.want {
				t.Fatalf("identity result=%v calls=%d want=%v", err, calls, tc.want)
			}
			if err != nil && (strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), requested)) {
				t.Fatal("identity rejection leaked a path or API detail")
			}
		})
	}
	for _, requested := range []string{`C:\`, `C:\OWNED~1\file`, `C:\owned\LONGFI~1.TXT`} {
		resolved := `\\?\C:\`
		want := requested == `C:\`
		if !want {
			resolved = `\\?\C:\owned long directory\long filename.txt`
		}
		query := func(_ windows.Handle, out *uint16, size, _ uint32) (uint32, error) {
			u, err := windows.UTF16FromString(resolved)
			copy(unsafe.Slice(out, int(size)), u)
			return uint32(len(u) - 1), err
		}
		if err := verifyFinalPath(0, requested, query); (err == nil) != want {
			t.Fatalf("root or alias comparison %q = %v", requested, err)
		}
	}
}

func TestWindowsReadRejectsActualShortTerminalAndAncestorNames(t *testing.T) {
	root := nativeFixtureRoot(t)
	dir := filepath.Join(root, "owned long source directory")
	file := filepath.Join(dir, "owned long source filename.txt")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("owned alias control"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenRegular(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	for _, component := range []string{"terminal", "ancestor"} {
		t.Run(component, func(t *testing.T) {
			long := file
			if component == "ancestor" {
				long = dir
			}
			ptr, err := windows.UTF16PtrFromString(long)
			if err != nil {
				t.Fatal(err)
			}
			buf := make([]uint16, 32768)
			n, err := windows.GetShortPathName(ptr, &buf[0], uint32(len(buf)))
			if err != nil || n == 0 || n >= uint32(len(buf)) {
				t.Fatalf("query owned short-path availability: n=%d err=%v", n, err)
			}
			base := filepath.Base(windows.UTF16ToString(buf[:n]))
			if strings.EqualFold(base, filepath.Base(long)) {
				t.Log("volume did not mint a short name for this owned component; deterministic final-name rejection is covered separately")
				return
			}
			alias := filepath.Join(filepath.Dir(long), base)
			if component == "ancestor" {
				alias = filepath.Join(alias, filepath.Base(file))
			}
			if b, err := os.ReadFile(alias); err != nil || string(b) != "owned alias control" {
				t.Fatalf("short alias does not address control file: %q %v", b, err)
			}
			if f, err := OpenRegular(alias); err == nil {
				f.Close()
				t.Fatal("actual DOS short alias admitted")
			} else if !strings.Contains(err.Error(), "identity") {
				t.Fatalf("alias rejected before final handle identity check: %v", err)
			}
			// Failed acquisition must release the terminal and every ancestor.
			moved := dir + "-moved"
			if err := os.Rename(dir, moved); err != nil {
				t.Fatalf("failed alias acquisition leaked a pinned handle: %v", err)
			}
			if err := os.Rename(moved, dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWindowsTildeReadDoesNotWidenMutators(t *testing.T) {
	root := nativeFixtureRoot(t)
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "version~deb")
	if f, err := OpenLock(p); err == nil {
		f.Close()
		t.Fatal("tilde lock creation admitted")
	}
	if PublishAbsent(source, p) == nil || PublishAbsent(p, source) == nil {
		t.Fatal("tilde mutation admitted")
	}
	if _, err := os.Lstat(p); !os.IsNotExist(err) {
		t.Fatalf("rejected mutation created an entry: %v", err)
	}
	if b, err := os.ReadFile(source); err != nil || string(b) != "retain" {
		t.Fatalf("rejected publication changed source: %q %v", b, err)
	}
}

func nativeFixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := WindowsAbsolute(root); err != nil {
		t.Fatalf("native fixture requires an admitted long-path TEMP/TMP root, got %q: %v", root, err)
	}
	return root
}

func TestWindowsLongPathUnicodeSpacePrivateDirectoryAndRead(t *testing.T) {
	root := filepath.Join(nativeFixtureRoot(t), "用户 Source Folder~deb")
	if err := PrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	// Reopening checks the owner, protected DACL and exact user/SYSTEM ACEs.
	if err := PrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "数据 file.txt")
	if err := os.WriteFile(file, []byte("native long-path content"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := OpenRegular(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil || string(got) != "native long-path content" {
		t.Fatalf("read native long path = %q, %v", got, err)
	}
}

func TestWindowsPrivateDirectoryRefusesInheritedPublicRoot(t *testing.T) {
	root := filepath.Join(nativeFixtureRoot(t), "private")
	if err := PrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDirectory(root); err != nil {
		t.Fatal(err)
	}
	public := filepath.Join(nativeFixtureRoot(t), "public")
	if err := os.Mkdir(public, 0700); err != nil {
		t.Fatal(err)
	}
	if PrivateDirectory(public) == nil {
		t.Fatal("accepted directory without protected private DACL")
	}
}

func TestWindowsReadRetainsAncestorsAndRejectsDirectory(t *testing.T) {
	root := nativeFixtureRoot(t)
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

func TestWindowsOpenDirectoryPinsEmptyDirectoryChainUntilClose(t *testing.T) {
	for _, component := range []string{"leaf", "ancestor"} {
		t.Run(component, func(t *testing.T) {
			parent := filepath.Join(nativeFixtureRoot(t), "parent")
			leaf := filepath.Join(parent, "empty")
			if err := os.MkdirAll(leaf, 0700); err != nil {
				t.Fatal(err)
			}
			target := leaf
			if component == "ancestor" {
				target = parent
			}
			moved := target + "-moved"
			// Establish that ordinary access permits replacement. There is no
			// open child file whose data handle could incidentally pin the path.
			if err := os.Rename(target, moved); err != nil {
				t.Fatalf("fixture is not renameable before acquisition: %v", err)
			}
			if err := os.Rename(moved, target); err != nil {
				t.Fatal(err)
			}
			f, err := OpenDirectory(leaf)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if err := os.Rename(target, moved); err == nil {
				t.Fatal("directory namespace renamed while held")
			}
			if _, err := os.Stat(leaf); err != nil {
				t.Fatalf("held namespace disappeared: %v", err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(target, moved); err != nil {
				t.Fatalf("directory stayed pinned after release: %v", err)
			}
		})
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
	root := nativeFixtureRoot(t)
	target := filepath.Join(root, "target~deb")
	link := filepath.Join(root, "junction~deb")
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
	} else if !strings.Contains(err.Error(), "reparse") {
		t.Fatalf("junction rejected before the reparse boundary: %v", err)
	}
	if err := ValidateDirectory(link); err == nil {
		t.Fatal("terminal junction admitted as directory")
	} else if !strings.Contains(err.Error(), "reparse") {
		t.Fatalf("directory rejected before the reparse boundary: %v", err)
	}
	if err := PrivateDirectory(link); err == nil {
		t.Fatal("terminal junction admitted as private root")
	} else if !strings.Contains(err.Error(), "reparse") {
		t.Fatalf("private directory rejected before the reparse boundary: %v", err)
	}
}
