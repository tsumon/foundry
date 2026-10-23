//go:build windows

package vmruntime

import (
	"foundry/internal/platformfs"
	"golang.org/x/sys/windows"
	"path/filepath"
	"unsafe"
)

// Create with the protected DACL in the very first namespace operation. Never
// accept ERROR_ALREADY_EXISTS: an existing name is not this session's owner.
func createPrivateDirectory(path string) error {
	if e := platformfs.ValidateAbsolute(path); e != nil {
		return e
	}
	if e := platformfs.ValidateDirectory(filepath.Dir(path)); e != nil {
		return e
	}
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
