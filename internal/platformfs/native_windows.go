package platformfs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

func CanonicalMode(mode os.FileMode) (os.FileMode, error) { return WindowsMode(mode) }
func ValidateRelative(p string) error                     { return WindowsRelative(p) }
func ValidateAbsolute(p string) error                     { return WindowsAbsolute(p) }

// Native handles can distinguish a literal tilde from a DOS short-name alias.
// Preserve every other lexical rule in a validation-only shadow; always open
// the original spelling, then compare its normalized final handle identity.
func validateNativeRelative(p string) error {
	return WindowsRelative(strings.ReplaceAll(p, "~", "_"))
}
func validateNativeAbsolute(p string) error {
	return WindowsAbsolute(strings.ReplaceAll(p, "~", "_"))
}

func verifyFinalPath(h windows.Handle, requested string, query func(windows.Handle, *uint16, uint32, uint32) (uint32, error)) error {
	buf := make([]uint16, 32768)
	// FILE_NAME_NORMALIZED | VOLUME_NAME_DOS. Never resolve an incoming short
	// name before this comparison: that would erase the alias being rejected.
	n, err := query(h, &buf[0], uint32(len(buf)), 0)
	if err != nil || n == 0 || n >= uint32(len(buf)) || buf[n] != 0 {
		return errors.New("opened path identity unavailable")
	}
	for _, c := range buf[:n] {
		if c == 0 {
			return errors.New("opened path identity unavailable")
		}
	}
	if !strings.EqualFold(windows.UTF16ToString(buf[:n]), `\\?\`+requested) {
		return errors.New("opened path identity mismatch")
	}
	return nil
}

// Every component is opened without delete sharing before the next component.
// This pins names against ancestor replacement; OPEN_REPARSE_POINT and the
// opened handle's attributes reject symlinks, junctions and other reparse tags.
// Final long-name comparison also rejects aliases at every pinned component.
func openChain(p string, directory bool) (*File, error) {
	if e := validateNativeAbsolute(p); e != nil {
		return nil, e
	}
	p = filepath.Clean(p)
	parts := strings.Split(p[3:], string(filepath.Separator))
	current := p[:3]
	var parents []*os.File
	fail := func(e error) (*File, error) {
		for _, f := range parents {
			f.Close()
		}
		return nil, e
	}
	for i, part := range append([]string{""}, parts...) {
		if part != "" {
			current = filepath.Join(current, part)
		}
		last := i == len(parts)
		wantDir := !last || directory
		ptr, e := windows.UTF16PtrFromString(current)
		if e != nil {
			return fail(e)
		}
		// Metadata-only opens do not participate in read/write/delete sharing
		// checks. Directory-data read access makes omission of SHARE_DELETE
		// actually pin every component, including an empty terminal directory.
		access := uint32(windows.FILE_LIST_DIRECTORY | windows.FILE_READ_ATTRIBUTES | windows.READ_CONTROL)
		if !wantDir {
			access = windows.GENERIC_READ
		}
		h, e := windows.CreateFile(ptr, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if e != nil {
			return fail(e)
		}
		f := os.NewFile(uintptr(h), current)
		var info windows.ByHandleFileInformation
		e = windows.GetFileInformationByHandle(h, &info)
		kind, ke := windows.GetFileType(h)
		if e != nil || ke != nil || kind != windows.FILE_TYPE_DISK || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != wantDir {
			f.Close()
			return fail(errors.New("nonregular or reparse path refused"))
		}
		if e := verifyFinalPath(h, current, windows.GetFinalPathNameByHandle); e != nil {
			f.Close()
			return fail(e)
		}
		if last {
			return &File{File: f, parents: parents}, nil
		}
		parents = append(parents, f)
	}
	return fail(errors.New("invalid path"))
}
func OpenRegular(p string) (*File, error) { return openChain(p, false) }

// OpenDirectory retains the same no-reparse and pinned-ancestor guarantees.
func OpenDirectory(p string) (*File, error) { return openChain(p, true) }
func OpenLock(p string) (*File, error) {
	if e := WindowsAbsolute(p); e != nil {
		return nil, e
	}
	parent, e := openChain(filepath.Dir(p), true)
	if e != nil {
		return nil, e
	}
	ptr, e := windows.UTF16PtrFromString(p)
	if e != nil {
		parent.Close()
		return nil, e
	}
	h, e := windows.CreateFile(ptr, windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if e != nil {
		parent.Close()
		return nil, e
	}
	f := os.NewFile(uintptr(h), p)
	var info windows.ByHandleFileInformation
	e = windows.GetFileInformationByHandle(h, &info)
	kind, ke := windows.GetFileType(h)
	if e != nil || ke != nil || kind != windows.FILE_TYPE_DISK || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 {
		f.Close()
		parent.Close()
		return nil, errors.New("controller lock must be regular")
	}
	return &File{File: f, parents: append(parent.parents, parent.File)}, nil
}
func OpenRootRegular(root *os.Root, p string) (*File, error) {
	if e := validateNativeRelative(p); e != nil {
		return nil, e
	}
	return OpenRegular(filepath.Join(root.Name(), p))
}
func ValidateDirectory(p string) error {
	f, e := openChain(p, true)
	if e != nil {
		return e
	}
	return f.Close()
}

func PrivateDirectory(p string) error {
	if e := validateNativeAbsolute(p); e != nil {
		return e
	}
	parent, e := openChain(filepath.Dir(p), true)
	if e != nil {
		return e
	}
	defer parent.Close()
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return e
	}
	sd, e := windows.SecurityDescriptorFromString("O:" + user.User.Sid.String() + "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if e != nil {
		return e
	}
	ptr, e := windows.UTF16PtrFromString(p)
	if e != nil {
		return e
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if e = windows.CreateDirectory(ptr, &sa); e != nil && !errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		return e
	}
	f, e := openChain(p, true)
	if e != nil {
		return e
	}
	defer f.Close()
	actual, e := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	owner, _, e := actual.Owner()
	if e != nil || !windows.EqualSid(owner, user.User.Sid) {
		return errors.New("private directory owner mismatch")
	}
	control, _, e := actual.Control()
	if e != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("private directory DACL is not protected")
	}
	acl, _, e := actual.DACL()
	if e != nil || acl == nil || acl.AceCount != 2 {
		return errors.New("private directory DACL mismatch")
	}
	system, e := windows.StringToSid("S-1-5-18")
	if e != nil {
		return e
	}
	seenUser, seenSystem := false, false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e := windows.GetAce(acl, i, &ace); e != nil {
			return e
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != 0x1f01ff || ace.Header.AceFlags != 3 {
			return errors.New("private directory ACE mismatch")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if windows.EqualSid(sid, user.User.Sid) {
			seenUser = true
		} else if windows.EqualSid(sid, system) {
			seenSystem = true
		} else {
			return errors.New("unexpected directory trustee")
		}
	}
	if !seenUser || !seenSystem {
		return errors.New("private directory trustees mismatch")
	}
	return nil
}
func PrivateTemp(parent, prefix string) (string, error) {
	if parent == "" {
		parent = os.TempDir()
	}
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	p := filepath.Join(parent, prefix+hex.EncodeToString(b[:]))
	if _, e := os.Lstat(p); !os.IsNotExist(e) {
		return "", errors.New("temporary destination occupied")
	}
	if e := PrivateDirectory(p); e != nil {
		return "", e
	}
	return p, nil
}

func FlushWritten(f *os.File) error { return f.Sync() }

// Windows durability model is process-crash-tested NTFS publication, not
// recursive directory fsync or power-loss certification. Files are flushed on
// their original writable handles; MoveFileExW publishes with WRITE_THROUGH.
func SyncPath(p string) error {
	info, e := os.Lstat(p)
	if e != nil {
		return e
	}
	if info.IsDir() {
		return ValidateDirectory(p)
	}
	f, e := OpenRegular(p)
	if e != nil {
		return e
	}
	return f.Close()
}
func PublishAbsent(source, target string) error {
	if e := WindowsAbsolute(source); e != nil {
		return e
	}
	if e := WindowsAbsolute(target); e != nil {
		return e
	}
	if !strings.EqualFold(filepath.VolumeName(source), filepath.VolumeName(target)) {
		return errors.New("same-volume publication required")
	}
	sp, e := openChain(filepath.Dir(source), true)
	if e != nil {
		return e
	}
	defer sp.Close()
	tp, e := openChain(filepath.Dir(target), true)
	if e != nil {
		return e
	}
	defer tp.Close()
	a, e := windows.UTF16PtrFromString(source)
	if e != nil {
		return e
	}
	b, e := windows.UTF16PtrFromString(target)
	if e != nil {
		return e
	}
	if e = windows.MoveFileEx(a, b, windows.MOVEFILE_WRITE_THROUGH); e != nil {
		return &os.LinkError{Op: "publish", Old: source, New: target, Err: e}
	}
	return nil
}
