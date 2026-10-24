//go:build windows

package keychaincheck

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"foundry/internal/platformfs"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"unsafe"
)

// Keep checked ancestors pinned until the journal handle closes.
type journalFile struct {
	*os.File
	parent        *platformfs.File
	ownedByParent bool
}

func (f *journalFile) Close() error {
	if f.ownedByParent {
		return f.parent.Close()
	}
	err := f.File.Close()
	if f.parent != nil {
		_ = f.parent.Close()
		f.parent = nil
	}
	return err
}
func createDesktopDirectory(path string) error { return platformfs.PrivateDirectory(path) }
func validateDesktopDirectory(path string) error {
	// Require existence before PrivateDirectory: validation never creates roots
	// or repairs an existing directory's ACL.
	f, err := platformfs.OpenDirectory(path)
	if err != nil {
		return errors.New("desktop diagnostic directory is unavailable or unsafe")
	}
	defer f.Close()
	return platformfs.PrivateDirectory(path)
}
func createPrivateJournalFile(path string) (*journalFile, error) {
	if err := platformfs.ValidateAbsolute(path); err != nil {
		return nil, err
	}
	parent, err := platformfs.OpenDirectory(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		parent.Close()
		return nil, err
	}
	sid := user.User.Sid.String()
	sd, err := windows.SecurityDescriptorFromString("O:" + sid + "D:P(A;;FA;;;SY)(A;;FA;;;" + sid + ")")
	if err != nil {
		parent.Close()
		return nil, err
	}
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		parent.Close()
		return nil, err
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	h, err := windows.CreateFile(ptr, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL, windows.FILE_SHARE_READ, &sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		parent.Close()
		return nil, err
	}
	return &journalFile{File: os.NewFile(uintptr(h), path), parent: parent}, nil
}
func openPrivateJournalFile(path string) (*journalFile, error) {
	f, err := platformfs.OpenRegular(path)
	if err != nil {
		return nil, err
	}
	j := &journalFile{File: f.File, parent: f, ownedByParent: true}
	if err = secureJournalFile(j); err != nil {
		f.Close()
		return nil, err
	}
	return j, nil
}
func createPrivateJournalTemp(path string) (*journalFile, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	return createPrivateJournalFile(filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp-"+hex.EncodeToString(nonce[:])))
}
func secureJournalFile(f *journalFile) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || !windows.EqualSid(owner, user.User.Sid) {
		return errors.New("journal owner mismatch")
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return errors.New("journal DACL is not protected")
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil || acl.AceCount != 2 {
		return errors.New("journal DACL mismatch")
	}
	system, err := windows.StringToSid("S-1-5-18")
	if err != nil {
		return err
	}
	seenUser, seenSystem := false, false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, i, &ace); err != nil {
			return err
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags != 0 || ace.Mask != 0x1f01ff {
			return errors.New("journal ACE mismatch")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if windows.EqualSid(sid, user.User.Sid) {
			seenUser = true
		} else if windows.EqualSid(sid, system) {
			seenSystem = true
		} else {
			return errors.New("unexpected journal trustee")
		}
	}
	if !seenUser || !seenSystem {
		return errors.New("journal trustees mismatch")
	}
	return nil
}
func privateJournalMode(info os.FileInfo) bool { return info.Mode().IsRegular() }
func replaceJournal(source, target string) error {
	parent, err := platformfs.OpenDirectory(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer parent.Close()
	f, err := openPrivateJournalFile(target)
	if err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	a, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
