package keychaincheck

import (
	"errors"
	"foundry/internal/model"
	"foundry/internal/platformfs"
	"io"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Synthetic SIDs require no account, owner reassignment, elevation, or token
// privilege. Each malformed descriptor isolates a fail-closed policy check.
func TestWindowsJournalSecurityDescriptorRejectsUnsafeACL(t *testing.T) {
	const user = "S-1-5-21-1-2-3-1001"
	userSID, err := windows.StringToSid(user)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, sddl, want string
	}{
		{"valid", "O:" + user + "D:P(A;;FA;;;SY)(A;;FA;;;" + user + ")", ""},
		{"wrong-owner", "O:BAD:P(A;;FA;;;SY)(A;;FA;;;" + user + ")", "journal owner mismatch"},
		{"unprotected", "O:" + user + "D:(A;;FA;;;SY)(A;;FA;;;" + user + ")", "journal DACL is not protected"},
		{"unexpected-trustee", "O:" + user + "D:P(A;;FA;;;WD)(A;;FA;;;" + user + ")", "unexpected journal trustee"},
		{"inheritable-ace", "O:" + user + "D:P(A;OI;FA;;;SY)(A;;FA;;;" + user + ")", "journal ACE mismatch"},
		{"inherited-ace", "O:" + user + "D:P(A;ID;FA;;;SY)(A;;FA;;;" + user + ")", "journal ACE mismatch"},
		{"wrong-mask", "O:" + user + "D:P(A;;FR;;;SY)(A;;FA;;;" + user + ")", "journal ACE mismatch"},
		{"deny-ace", "O:" + user + "D:P(D;;FA;;;SY)(A;;FA;;;" + user + ")", "journal ACE mismatch"},
		{"missing-system", "O:" + user + "D:P(A;;FA;;;" + user + ")", "journal DACL mismatch"},
		{"duplicate-user", "O:" + user + "D:P(A;;FA;;;" + user + ")(A;;FA;;;" + user + ")", "journal trustees mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tc.sddl)
			if err != nil {
				t.Fatalf("invalid descriptor fixture: %v", err)
			}
			err = validateJournalSecurityDescriptor(sd, userSID)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("private descriptor rejected: %v", err)
				}
			} else if err == nil || err.Error() != tc.want {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// Keep a real-file boundary check as well as descriptor policy coverage: dropping
// validation from secureJournalFile/openPrivateJournalFile must fail these tests.
func TestWindowsSecureJournalFileRejectsUnsafeACL(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	owner := "O:" + user.User.Sid.String()
	userACE := "(A;;FA;;;" + user.User.Sid.String() + ")"
	for _, tc := range []struct {
		name, dacl, want string
	}{
		{"private", "D:P(A;;FA;;;SY)" + userACE, ""},
		{"unprotected", "D:(A;;FA;;;SY)" + userACE, "journal DACL is not protected"},
		{"unexpected-trustee", "D:P(A;;FA;;;WD)" + userACE, "unexpected journal trustee"},
		{"ace-flags", "D:P(A;OI;FA;;;SY)" + userACE, "journal ACE mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal.json")
			sd, err := windows.SecurityDescriptorFromString(owner + tc.dacl)
			if err != nil {
				t.Fatal(err)
			}
			ptr, err := windows.UTF16PtrFromString(path)
			if err != nil {
				t.Fatal(err)
			}
			sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
			h, err := windows.CreateFile(ptr, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.READ_CONTROL, windows.FILE_SHARE_READ, &sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
			if err != nil {
				t.Fatalf("create ACL fixture: %v", err)
			}
			f := &journalFile{File: os.NewFile(uintptr(h), path)}
			t.Cleanup(func() { _ = f.Close() })
			b, err := encodeJournal(journalRecord{Version: journalVersion, ProbeID: testProbeID, State: "pending", Cleanup: "unconfirmed"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write(b); err != nil {
				t.Fatal(err)
			}
			err = secureJournalFile(f)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("private handle rejected: %v", err)
				}
			} else if err == nil || err.Error() != tc.want {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = readJournal(path)
			if (err == nil) != (tc.want == "") {
				t.Fatalf("journal read bypassed ACL decision: %v", err)
			}
		})
	}
}

// These diagnostics use only memory, never an actual account.
type windowsProbeSecrets struct{ value string }

func (s *windowsProbeSecrets) Set(_ string, v string) error { s.value = v; return nil }
func (s *windowsProbeSecrets) Get(string) (string, error) {
	if s.value == "" {
		return "", model.ErrSecretNotFound
	}
	return s.value, nil
}
func (s *windowsProbeSecrets) Delete(string) error { s.value = ""; return nil }

func TestWindowsDesktopProbeUsesPrivateJournalAndExactCleanup(t *testing.T) {
	root, err := platformfs.PrivateTemp(t.TempDir(), "diagnostic-")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	const id = "foundry-openai-stage0-0123456789abcdef0123456789abcdef"
	fake := &windowsProbeSecrets{}
	calls := 0
	deps := commandDeps{sourceRoot: filepath.Join(root, "missing-packaged-source"), generateID: func() (string, error) { return id, nil }, newStore: func(got string) (model.Secrets, error) {
		if got != id {
			t.Fatal("wrong exact ID")
		}
		calls++
		return fake, nil
	}}
	if err := runDesktopProbe(root, deps, io.Discard); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "openai-desktop-diagnostics"))
	if err != nil || len(entries) != 1 {
		t.Fatal("missing journal", err)
	}
	record, err := readJournal(filepath.Join(root, "openai-desktop-diagnostics", entries[0].Name()))
	if err != nil || record.State != "passed" || record.Cleanup != "confirmed" || calls != 1 || fake.value != "" {
		t.Fatal("diagnostic not confirmed", err)
	}
	pending := journalRecord{Version: journalVersion, ProbeID: id, State: "pending", Cleanup: "unconfirmed"}
	if err := createJournal(filepath.Join(root, "openai-desktop-diagnostics", "interrupted.json"), pending, nil); err != nil {
		t.Fatal(err)
	}
	if err := runDesktopProbe(root, deps, io.Discard); err == nil || calls != 1 {
		t.Fatal("unresolved cleanup bypassed")
	}
}

func TestWindowsProbeRejectsUnsafeStorageBeforeFactory(t *testing.T) {
	root := t.TempDir()
	deps := commandDeps{newStore: func(string) (model.Secrets, error) {
		t.Fatal("unsafe path reached store")
		return nil, errors.New("forbidden")
	}}
	if err := runDesktopProbe(root, deps, io.Discard); err == nil {
		t.Fatal("inherited directory accepted")
	}
	if err := runDesktopProbe(filepath.Join(root, "missing"), deps, io.Discard); err == nil {
		t.Fatal("missing root created")
	}
	if _, err := os.Stat(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatal("validation created root")
	}
}
