package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/model"
)

const testProbeID = "foundry-openai-stage0-0123456789abcdef0123456789abcdef"

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

func testCommandDeps(t *testing.T, store model.Secrets) (commandDeps, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	outside := filepath.Join(root, "operator")
	if err := os.MkdirAll(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0700); err != nil {
		t.Fatal(err)
	}
	return commandDeps{
		sourceRoot: source,
		generateID: func() (string, error) { return testProbeID, nil },
		newStore:   func(string) (model.Secrets, error) { return store, nil },
	}, filepath.Join(outside, "probe.json")
}

func TestJournalRunIsExclusiveOwnerOnlyAndSanitized(t *testing.T) {
	store := &commandSecrets{}
	deps, path := testCommandDeps(t, store)
	var out bytes.Buffer
	if err := runCommand([]string{"--journal", path}, deps, &out); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != 0600 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("info=%v err=%v", info, err)
	}
	record, err := readJournal(path)
	if err != nil || record.State != "passed" || record.Cleanup != "confirmed" || record.ProbeID != testProbeID {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if store.setCalls != 2 || store.getCalls != 3 || store.deleteCalls != 1 {
		t.Fatalf("store=%+v", store)
	}
	text := out.String()
	for _, forbidden := range []string{"synthetic-foundry", "raw-marker", "token", "password"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unsafe JSON %q", text)
		}
	}
	var report model.SecretProbeReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Status != "passed" {
		t.Fatalf("output=%q err=%v", text, err)
	}
	if err := runCommand([]string{"--journal", path}, deps, &bytes.Buffer{}); err == nil {
		t.Fatal("existing journal was overwritten")
	}
	if store.setCalls != 2 {
		t.Fatal("exclusive failure touched store")
	}
}

func TestCleanupPendingJournalDoesNotReplayProbe(t *testing.T) {
	store := &commandSecrets{value: "left-behind-synthetic"}
	deps, path := testCommandDeps(t, store)
	writeTestJournal(t, path, journalRecord{Version: journalVersion, ProbeID: testProbeID, State: "pending", Cleanup: "unconfirmed"})
	var out bytes.Buffer
	if err := runCommand([]string{"--cleanup", path}, deps, &out); err != nil {
		t.Fatal(err)
	}
	record, err := readJournal(path)
	if err != nil || record.State != "failed" || record.Cleanup != "confirmed" {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	if store.setCalls != 0 || store.deleteCalls != 1 || store.getCalls != 1 {
		t.Fatalf("cleanup replayed probe: %+v", store)
	}
}

func TestCleanupConfirmedTerminalJournalIsReadOnly(t *testing.T) {
	store := &commandSecrets{}
	deps, path := testCommandDeps(t, store)
	writeTestJournal(t, path, journalRecord{Version: journalVersion, ProbeID: testProbeID, State: "passed", Cleanup: "confirmed", Report: model.SecretProbeReport{ProbeID: testProbeID, Status: "passed", Cleanup: "confirmed"}})
	if err := runCommand([]string{"--cleanup", path}, deps, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if store.setCalls+store.getCalls+store.deleteCalls != 0 {
		t.Fatalf("confirmed journal touched store: %+v", store)
	}
}

func TestCleanupRejectsLockSymlinkOversizeAndMalformedJournal(t *testing.T) {
	cases := []string{"lock", "symlink", "oversize", "malformed_id"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			store := &commandSecrets{}
			deps, path := testCommandDeps(t, store)
			switch name {
			case "lock":
				writeTestJournal(t, path, journalRecord{Version: journalVersion, ProbeID: testProbeID, State: "pending", Cleanup: "unconfirmed"})
				if err := os.WriteFile(path+".lock", []byte("active\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := path + ".target"
				writeTestJournal(t, target, journalRecord{Version: journalVersion, ProbeID: testProbeID, State: "pending", Cleanup: "unconfirmed"})
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.WriteFile(path, bytes.Repeat([]byte("x"), maxJournalBytes+1), 0600); err != nil {
					t.Fatal(err)
				}
			case "malformed_id":
				writeTestJournal(t, path, journalRecord{Version: journalVersion, ProbeID: "../../account", State: "pending", Cleanup: "unconfirmed"})
			}
			if err := runCommand([]string{"--cleanup", path}, deps, &bytes.Buffer{}); err == nil {
				t.Fatal("unsafe journal accepted")
			}
			if store.setCalls+store.getCalls+store.deleteCalls != 0 {
				t.Fatal("unsafe journal touched store")
			}
		})
	}
}

func TestTerminalWriteFailureAndCleanupUncertaintyAreNonzero(t *testing.T) {
	t.Run("terminal write", func(t *testing.T) {
		store := &commandSecrets{}
		deps, path := testCommandDeps(t, store)
		deps.writeTerminal = func(string, []byte) error { return errors.New("disk raw-marker") }
		if err := runCommand([]string{"--journal", path}, deps, &bytes.Buffer{}); err == nil {
			t.Fatal("terminal write failure reported success")
		}
		record, err := readJournal(path)
		if err != nil || record.State != "pending" {
			t.Fatalf("pending recovery journal lost: %+v %v", record, err)
		}
	})
	t.Run("cleanup uncertain", func(t *testing.T) {
		store := &commandSecrets{getError: errors.New("permission denied raw-marker")}
		deps, path := testCommandDeps(t, store)
		var out bytes.Buffer
		if err := runCommand([]string{"--journal", path}, deps, &out); err == nil {
			t.Fatal("unconfirmed cleanup reported success")
		}
		record, err := readJournal(path)
		if err != nil || record.State != "failed" || record.Cleanup != "unconfirmed" {
			t.Fatalf("record=%+v err=%v", record, err)
		}
		if strings.Contains(out.String(), "raw-marker") || strings.Contains(out.String(), "permission denied") {
			t.Fatalf("raw error leaked: %q", out.String())
		}
	})
}

func TestJournalDirectoryDurabilityFailureIsFailClosed(t *testing.T) {
	t.Run("pending journal", func(t *testing.T) {
		store := &commandSecrets{}
		deps, path := testCommandDeps(t, store)
		factoryCalls := 0
		deps.newStore = func(string) (model.Secrets, error) {
			factoryCalls++
			return store, nil
		}
		deps.syncParent = func(string) error { return errors.New("directory sync failed") }
		if err := runCommand([]string{"--journal", path}, deps, &bytes.Buffer{}); err == nil {
			t.Fatal("pending directory sync failure reported success")
		}
		if factoryCalls != 0 || store.setCalls+store.getCalls+store.deleteCalls != 0 {
			t.Fatalf("native boundary crossed: factory=%d store=%+v", factoryCalls, store)
		}
	})
	t.Run("terminal journal", func(t *testing.T) {
		store := &commandSecrets{}
		deps, path := testCommandDeps(t, store)
		syncCalls := 0
		deps.syncParent = func(string) error {
			syncCalls++
			if syncCalls == 2 {
				return errors.New("directory sync failed")
			}
			return nil
		}
		if err := runCommand([]string{"--journal", path}, deps, &bytes.Buffer{}); err == nil {
			t.Fatal("terminal directory sync failure reported success")
		}
		if syncCalls != 2 || store.deleteCalls != 1 {
			t.Fatalf("syncCalls=%d store=%+v", syncCalls, store)
		}
	})
}

func TestJournalPathMustBeAbsoluteNewAndOutsideSource(t *testing.T) {
	store := &commandSecrets{}
	deps, outside := testCommandDeps(t, store)
	inside := filepath.Join(deps.sourceRoot, "probe.json")
	for _, path := range []string{"relative.json", inside} {
		if err := runCommand([]string{"--journal", path}, deps, &bytes.Buffer{}); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected outside journal: %v", err)
	}
}

func writeTestJournal(t *testing.T, path string, record journalRecord) {
	t.Helper()
	b, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
