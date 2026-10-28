package dependencyadmission

import (
	"foundry/internal/platformfs"
	"os"
	"syscall"
)

func pythonHoldDirectory(p string) (pythonDirectoryHandle, error) { return platformfs.OpenDirectory(p) }
func pythonSingleLink(f *os.File, _ os.FileInfo) bool {
	var i syscall.ByHandleFileInformation
	return syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &i) == nil && i.NumberOfLinks == 1
}
func pythonPrivateDirectory(p string, _ os.FileInfo) error { return platformfs.PrivateDirectory(p) }
