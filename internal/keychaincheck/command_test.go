//go:build !windows

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
)

// Also run this test in a -trimpath compiled test binary from a non-source cwd.
// Only the in-memory secret factory below is reachable; no native store is used.
func TestDesktopProbePackagedCWD(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	deps := defaultCommandDeps()
	fake := &commandSecrets{}
	factories := 0
	deps.generateID = func() (string, error) { return testProbeID, nil }
	deps.newStore = func(string) (model.Secrets, error) { factories++; return fake, nil }
	if err = runDesktopProbe(root, deps, io.Discard); err != nil {
		t.Fatalf("packaged desktop diagnostic rejected: sourceRoot=%q: %v", deps.sourceRoot, err)
	}
	dir := filepath.Join(root, "openai-desktop-diagnostics")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal("missing exclusive journal", err)
	}
	record, err := readJournal(filepath.Join(dir, entries[0].Name()))
	if err != nil || record.State != "passed" || record.Cleanup != "confirmed" || factories != 1 || fake.setCalls != 2 || fake.getCalls != 3 || fake.deleteCalls != 1 {
		t.Fatalf("unconfirmed memory-only diagnostic: %+v %v", record, err)
	}
	// The new trusted desktop entry must not relax the separate CLI policy.
	deps.sourceRoot = filepath.Join(root, "absent-build-source")
	if err = runCommand([]string{"--journal", filepath.Join(root, "cli.json")}, deps, io.Discard); err == nil || factories != 1 {
		t.Fatal("CLI source-unavailable policy was bypassed")
	}
	t.Log("desktop diagnostic used only the replacement memory factory; CLI rejection made no additional factory call")
}

func TestDesktopProbeRejectsUnsafeDirectoriesAndRetainedJournals(t *testing.T) {
	for _, kind := range []string{"relative-root", "unclean-root", "root-link", "root-public", "directory-link", "directory-public", "journal-link", "journal-public", "pending", "malformed", "locked"} {
		t.Run(kind, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			outside, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, "openai-desktop-diagnostics")
			if err = os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(dir, "retained.json")
			pending := journalRecord{Version: journalVersion, ProbeID: testProbeID, State: "pending", Cleanup: "unconfirmed"}
			switch kind {
			case "relative-root":
				root = "relative"
			case "unclean-root":
				root += "/."
			case "root-link":
				alias := filepath.Join(outside, "alias")
				if err = os.Symlink(root, alias); err != nil {
					t.Fatal(err)
				}
				root = alias
			case "root-public":
				if err = os.Chmod(root, 0755); err != nil {
					t.Fatal(err)
				}
			case "directory-link":
				if err = os.Remove(dir); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(outside, dir); err != nil {
					t.Fatal(err)
				}
			case "directory-public":
				if err = os.Chmod(dir, 0755); err != nil {
					t.Fatal(err)
				}
			case "journal-link":
				target := filepath.Join(outside, "retained.json")
				writeTestJournal(t, target, pending)
				if err = os.Symlink(target, journal); err != nil {
					t.Fatal(err)
				}
			case "journal-public":
				writeTestJournal(t, journal, pending)
				if err = os.Chmod(journal, 0644); err != nil {
					t.Fatal(err)
				}
			case "pending":
				writeTestJournal(t, journal, pending)
			case "malformed":
				if err = os.WriteFile(journal, []byte(`{"cleanup":"confirmed"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "locked":
				if err = os.WriteFile(journal+".lock", []byte("retained lock\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadFile(journal)
			outsideBefore, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			deps := defaultCommandDeps()
			deps.newStore = func(string) (model.Secrets, error) {
				t.Fatal("unsafe path crossed native factory boundary")
				return nil, errors.New("forbidden")
			}
			if err = runDesktopProbe(root, deps, io.Discard); err == nil {
				t.Fatal("unsafe desktop diagnostic admitted")
			}
			after, _ := os.ReadFile(journal)
			if !bytes.Equal(before, after) {
				t.Fatal("retained journal modified by failure")
			}
			outsideAfter, err := os.ReadDir(outside)
			if err != nil || len(outsideAfter) != len(outsideBefore) {
				t.Fatal("unsafe directory caused external file creation", err)
			}
			if kind == "root-public" {
				info, _ := os.Stat(root)
				if info.Mode().Perm() != 0755 {
					t.Fatal("existing root chmodded")
				}
			}
			if kind == "directory-public" {
				info, _ := os.Stat(dir)
				if info.Mode().Perm() != 0755 {
					t.Fatal("existing directory chmodded")
				}
			}
		})
	}
}

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

// Recovery belongs to the preserved journal and binary, not to a build-tree
// path that may have been moved or removed after an interrupted diagnostic.
func TestCleanupPendingJournalSurvivesUnavailableSourceRoot(t *testing.T) {
	store := &commandSecrets{value: "left-behind-synthetic"}
	deps, path := testCommandDeps(t, store)
	writeTestJournal(t, path, journalRecord{Version: journalVersion, ProbeID: testProbeID, State: "pending", Cleanup: "unconfirmed"})
	if err := os.RemoveAll(deps.sourceRoot); err != nil {
		t.Fatal(err)
	}
	if err := runCommand([]string{"--cleanup", path}, deps, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if store.setCalls != 0 || store.deleteCalls != 1 || store.getCalls != 1 {
		t.Fatalf("cleanup did not use exact recovery path: %+v", store)
	}
	record, err := readJournal(path)
	if err != nil || record.State != "failed" || record.Cleanup != "confirmed" {
		t.Fatalf("record=%+v err=%v", record, err)
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
