package keychaincheck

import (
	"errors"
	"foundry/internal/model"
	"foundry/internal/platformfs"
	"io"
	"os"
	"path/filepath"
	"testing"
)

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
