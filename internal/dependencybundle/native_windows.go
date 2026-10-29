package dependencybundle

import (
	"foundry/internal/platformfs"
	"os"
	"path/filepath"
	"syscall"
)

func holdDirectory(p string) (directoryHandle, error) { return platformfs.OpenDirectory(p) }
func privateDirectory(p string, _ os.FileInfo) error  { return platformfs.PrivateDirectory(p) }
func payloadMode(m os.FileMode) bool {
	_, e := platformfs.CanonicalMode(m)
	return m.IsRegular() && e == nil
}
func singleLink(f *os.File, _ os.FileInfo) error {
	var info syscall.ByHandleFileInformation
	if syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info) != nil || info.NumberOfLinks != 1 {
		return ErrIntegrity
	}
	return nil
}
func makeDirectory(r *os.Root, p string) error {
	return platformfs.PrivateDirectory(filepath.Join(r.Name(), filepath.FromSlash(p)))
}
func syncFile(*os.File) error { return nil }
func syncRoot(r *os.Root) error {
	// OpenRoot(".") preserves a terminal dot in Name. Clean only this trusted
	// handle-derived path; external path admission must still reject aliases.
	return platformfs.SyncPath(filepath.Clean(r.Name()))
}
func publishAbsent(r *os.Root, source, target string) error {
	return platformfs.PublishAbsent(filepath.Join(r.Name(), source), filepath.Join(r.Name(), target))
}

func makeStage(r *os.Root) (string, error) {
	p, e := platformfs.PrivateTemp(r.Name(), ".partial-")
	if e != nil {
		return "", e
	}
	return filepath.Base(p), nil
}
