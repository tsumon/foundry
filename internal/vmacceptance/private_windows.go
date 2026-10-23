//go:build windows

package vmacceptance

import (
	"foundry/internal/platformfs"
	"golang.org/x/sys/windows"
	"unsafe"
)

// Exclusivity and the private DACL are established by the same namespace call.
func reserveDataRoot(path string) error {
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
