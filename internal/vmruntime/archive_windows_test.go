package vmruntime

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestArchiveWindowsRejectsUntrustedParentBeforeWriting(t *testing.T) {
	source, instances := fixtureBundle(t)
	runtime, err := Load(source, instances)
	if err != nil {
		t.Fatal(err)
	}
	parent := archiveTestParent(t)
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	acl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(parent, "archive")
	if _, err := Retain(source, archive, runtime.Identity()); err == nil {
		t.Fatal("untrusted archive parent accepted")
	}
	if _, err := os.Lstat(archive); !os.IsNotExist(err) {
		t.Fatal("unsafe parent caused archive writes")
	}
}

func TestArchiveWindowsKeepsDestinationAncestorsPinned(t *testing.T) {
	parent := archiveTestParent(t)
	release, err := protectArchiveParent(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.Rename(parent, parent+"-replaced"); err == nil {
		_ = os.Rename(parent+"-replaced", parent)
		t.Fatal("archive parent namespace was replaceable while in use")
	}
}
