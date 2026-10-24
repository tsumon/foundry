package keychaincheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/model"
	"foundry/internal/platformfs"
)

const testProbeID = "foundry-openai-stage0-0123456789abcdef0123456789abcdef"

// Only the memory secret boundary is replaced. Journals, locks, permissions,
// terminal publication and cleanup recovery use the platform implementation.
type commandSecrets struct {
	value       string
	setCalls    int
	getCalls    int
	deleteCalls int
	getError    error
}

func (s *commandSecrets) Set(_ string, value string) error { s.setCalls++; s.value = value; return nil }
func (s *commandSecrets) Get(string) (string, error) {
	s.getCalls++
	if s.getError != nil {
		return "", s.getError
	}
	if s.value == "" {
		return "", model.ErrSecretNotFound
	}
	return s.value, nil
}
func (s *commandSecrets) Delete(string) error { s.deleteCalls++; s.value = ""; return nil }

// Removing terminal-write failure propagation, forgetting the pending journal,
// replaying the probe during cleanup, or accepting pending readiness breaks this.
func TestDesktopProbeFailureRetainsJournalForExactCleanup(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := platformfs.PrivateTemp(parent, "recovery-")
	if err != nil {
		t.Fatal(err)
	}
	fake := &commandSecrets{}
	factories, publications := 0, 0
	deps := commandDeps{
		sourceRoot: filepath.Join(root, "absent-build-source"),
		generateID: func() (string, error) { return testProbeID, nil },
		newStore: func(id string) (model.Secrets, error) {
			if id != testProbeID {
				t.Fatalf("factory used unrelated cleanup ID %q", id)
			}
			factories++
			return fake, nil
		},
		writeTerminal: func(string, []byte) error {
			publications++
			return errors.New("synthetic disk failure raw-marker")
		},
	}
	var out bytes.Buffer
	if err := runDesktopProbe(root, deps, &out); err == nil {
		t.Fatal("terminal failure passed")
	}
	if factories != 1 || publications != 1 || fake.setCalls != 2 || fake.getCalls != 3 || fake.deleteCalls != 1 || fake.value != "" {
		t.Fatalf("failure did not occur after the complete synthetic probe: factories=%d publications=%d store=%+v", factories, publications, fake)
	}
	var report model.SecretProbeReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Status != "failed" || report.FailedStep != "journal_terminal" {
		t.Fatalf("terminal failure reported readiness: %+v %v", report, err)
	}
	if strings.Contains(out.String(), "raw-marker") {
		t.Fatal("terminal failure leaked raw error")
	}
	dir := filepath.Join(root, "openai-desktop-diagnostics")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("pending journal lost or lock/temp leaked", err)
	}
	path := filepath.Join(dir, entries[0].Name())
	record, err := readJournal(path)
	if err != nil || record.ProbeID != testProbeID || record.State != "pending" || record.Cleanup != "unconfirmed" {
		t.Fatalf("pending recovery record lost: %+v %v", record, err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	deps.writeTerminal = nil
	if runDesktopProbe(root, deps, io.Discard) == nil || factories != 1 {
		t.Fatal("unconfirmed cleanup retried diagnostic")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("blocked preparation changed pending journal", err)
	}
	if err := runCommand([]string{"--cleanup", path}, deps, io.Discard); err != nil {
		t.Fatal(err)
	}
	record, err = readJournal(path)
	if err != nil || record.ProbeID != testProbeID || record.State != "failed" || record.Cleanup != "confirmed" || record.Report.FailedStep != "interrupted" {
		t.Fatalf("exact cleanup lost terminal recovery state: %+v %v", record, err)
	}
	if fake.setCalls != 2 || fake.getCalls != 4 || fake.deleteCalls != 2 || factories != 2 || fake.value != "" {
		t.Fatalf("cleanup replayed probe or failed exact removal: factories=%d store=%+v", factories, fake)
	}
	before, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := runCommand([]string{"--cleanup", path}, deps, io.Discard); err != nil {
		t.Fatal(err)
	}
	after, err = os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || factories != 2 {
		t.Fatal("confirmed recovery was not read-only", err)
	}
	entries, err = os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("recovery left a lock or temporary journal", err)
	}
}
