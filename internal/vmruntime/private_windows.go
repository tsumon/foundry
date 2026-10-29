//go:build windows

package vmruntime

import (
	"foundry/internal/platformfs"
	"golang.org/x/sys/windows"
	"path/filepath"
	"strings"
	"unsafe"
)

// Create with the protected DACL in the very first namespace operation. Never
// accept ERROR_ALREADY_EXISTS: an existing name is not this session's owner.
func createPrivateDirectory(path string) error {
	// Retained immutable assets include literal Debian ~deb directory names.
	// The shadow is lexical validation only; native handles validate original
	// long-name identities and remain pinned through creation and the postcheck.
	if e := platformfs.WindowsAbsolute(strings.ReplaceAll(path, "~", "_")); e != nil {
		return e
	}
	parent, e := platformfs.OpenDirectory(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer parent.Close()
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return e
	}
	sid := user.User.Sid.String()
	sd, e := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + sid + ")")
	if e != nil {
		return e
	}
	ptr, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if e = windows.CreateDirectory(ptr, &sa); e != nil {
		return e
	}
	return platformfs.PrivateDirectory(path)
}
