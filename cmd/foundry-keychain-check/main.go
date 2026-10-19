package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"foundry/internal/model"
)

const journalVersion = 1
const maxJournalBytes = 4096

type journalRecord struct {
	Version int                     `json:"version"`
	ProbeID string                  `json:"probe_id"`
	State   string                  `json:"state"`
	Cleanup string                  `json:"cleanup"`
	Report  model.SecretProbeReport `json:"report,omitempty"`
}

type commandDeps struct {
	sourceRoot    string
	generateID    func() (string, error)
	newStore      func(string) (model.Secrets, error)
	writeTerminal func(string, []byte) error
	syncParent    func(string) error
}

func main() {
	if err := runCommand(os.Args[1:], defaultCommandDeps(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func defaultCommandDeps() commandDeps {
	_, file, _, _ := runtime.Caller(0)
	return commandDeps{
		sourceRoot: filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..")),
		generateID: model.GenerateSecretProbeID,
		newStore:   model.NewSecretProbeStore,
	}
}

func runCommand(args []string, deps commandDeps, out io.Writer) error {
	flags := flag.NewFlagSet("foundry-keychain-check", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	journalPath := flags.String("journal", "", "new absolute recovery journal path")
	cleanupPath := flags.String("cleanup", "", "existing recovery journal path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || (*journalPath == "") == (*cleanupPath == "") {
		return errors.New("specify exactly one of --journal or --cleanup")
	}
	path := *journalPath
	if path == "" {
		path = *cleanupPath
	}
	if *journalPath != "" {
		if err := validateNewJournalPath(path, deps.sourceRoot); err != nil {
			return err
		}
	} else if _, err := resolveJournalPath(path); err != nil {
		return err
	}
	lock, err := acquireJournalLock(path + ".lock")
	if err != nil {
		return err
	}
	defer lock.release()
	if *journalPath != "" {
		return runNewJournal(path, deps, out)
	}
	return runCleanup(path, deps, out)
}

func runNewJournal(path string, deps commandDeps, out io.Writer) error {
	id, err := deps.generateID()
	if err != nil || model.ValidateSecretProbeID(id) != nil {
		return errors.New("generate diagnostic identifier failed")
	}
	pending := journalRecord{Version: journalVersion, ProbeID: id, State: "pending", Cleanup: "unconfirmed"}
	if err = createJournal(path, pending, deps.syncParent); err != nil {
		return err
	}
	store, err := deps.newStore(id)
	if err != nil || store == nil {
		report := model.SecretProbeReport{ProbeID: id, Status: "failed", FailedStep: "storage_unavailable", Cleanup: "unconfirmed"}
		return finishJournal(path, pending, report, deps, out)
	}
	report := model.RunSecretProbe(store, id)
	return finishJournal(path, pending, report, deps, out)
}

func runCleanup(path string, deps commandDeps, out io.Writer) error {
	record, err := readJournal(path)
	if err != nil {
		return err
	}
	if record.Cleanup == "confirmed" {
		if err = writeReport(out, record.Report); err != nil {
			return errors.New("write diagnostic report failed")
		}
		return nil
	}
	store, err := deps.newStore(record.ProbeID)
	if err != nil || store == nil {
		report := model.SecretProbeReport{ProbeID: record.ProbeID, Status: "failed", FailedStep: "storage_unavailable", Cleanup: "unconfirmed"}
		return finishJournal(path, record, report, deps, out)
	}
	report := model.CleanupSecretProbe(store, record.ProbeID)
	report.Status = "failed"
	if report.FailedStep == "" {
		report.FailedStep = "interrupted"
	}
	if err = persistJournalReport(path, record, report, deps, out); err != nil {
		return err
	}
	if report.Cleanup != "confirmed" {
		return errors.New("diagnostic cleanup is unconfirmed")
	}
	return nil
}

func finishJournal(path string, record journalRecord, report model.SecretProbeReport, deps commandDeps, out io.Writer) error {
	if err := persistJournalReport(path, record, report, deps, out); err != nil {
		return err
	}
	if report.Status != "passed" || report.Cleanup != "confirmed" {
		return errors.New("diagnostic did not pass")
	}
	return nil
}

func persistJournalReport(path string, record journalRecord, report model.SecretProbeReport, deps commandDeps, out io.Writer) error {
	record.State, record.Cleanup, record.Report = report.Status, report.Cleanup, report
	if record.State != "passed" {
		record.State = "failed"
	}
	b, err := encodeJournal(record)
	if err != nil {
		return errors.New("encode terminal journal failed")
	}
	writeTerminal := deps.writeTerminal
	if writeTerminal == nil {
		writeTerminal = func(path string, b []byte) error { return atomicWriteJournal(path, b, deps.syncParent) }
	}
	if err = writeTerminal(path, b); err != nil {
		report.Status = "failed"
		report.FailedStep = "journal_terminal"
		_ = writeReport(out, report)
		return errors.New("write terminal journal failed")
	}
	if err = writeReport(out, report); err != nil {
		return errors.New("write diagnostic report failed")
	}
	return nil
}

func writeReport(out io.Writer, report model.SecretProbeReport) error {
	return json.NewEncoder(out).Encode(report)
}

func resolveJournalPath(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("journal path must be clean and absolute")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", errors.New("journal parent is unavailable")
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

func validateNewJournalPath(path, sourceRoot string) error {
	candidate, err := resolveJournalPath(path)
	if err != nil {
		return err
	}
	source, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		return errors.New("source root is unavailable")
	}
	rel, err := filepath.Rel(source, candidate)
	if err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))) {
		return errors.New("journal path must be outside source checkout")
	}
	return nil
}

type journalLock struct {
	path string
	file *os.File
}

func acquireJournalLock(path string) (*journalLock, error) {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.New("journal lock exists or is unavailable; after confirming no process is running, remove only this journal's adjacent .lock file")
	}
	f := os.NewFile(uintptr(fd), path)
	if err = f.Chmod(0600); err == nil {
		_, err = fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	}
	if err == nil {
		err = f.Sync()
	}
	if err != nil {
		f.Close()
		os.Remove(path)
		return nil, errors.New("initialize journal lock failed")
	}
	return &journalLock{path: path, file: f}, nil
}

func (l *journalLock) release() {
	if l == nil {
		return
	}
	_ = l.file.Close()
	_ = os.Remove(l.path)
}

func createJournal(path string, record journalRecord, syncParent func(string) error) error {
	b, err := encodeJournal(record)
	if err != nil {
		return errors.New("encode pending journal failed")
	}
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return errors.New("journal must be a new file")
	}
	f := os.NewFile(uintptr(fd), path)
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		if syncParent == nil {
			syncParent = syncParentDirectory
		}
		err = syncParent(path)
	}
	if err != nil {
		return errors.New("create pending journal failed")
	}
	ok = true
	return nil
}

func readJournal(path string) (journalRecord, error) {
	var zero journalRecord
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() < 1 || info.Size() > maxJournalBytes {
		return zero, errors.New("journal is unavailable, unsafe, or oversized")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return zero, errors.New("journal is unavailable, unsafe, or oversized")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	limited, err := io.ReadAll(io.LimitReader(f, maxJournalBytes+1))
	if err != nil || len(limited) > maxJournalBytes {
		return zero, errors.New("read journal failed")
	}
	decoder := json.NewDecoder(bytes.NewReader(limited))
	decoder.DisallowUnknownFields()
	var record journalRecord
	if err = decoder.Decode(&record); err != nil {
		return zero, errors.New("journal is malformed")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || validateJournal(record) != nil {
		return zero, errors.New("journal is malformed")
	}
	return record, nil
}

func validateJournal(record journalRecord) error {
	if record.Version != journalVersion || model.ValidateSecretProbeID(record.ProbeID) != nil {
		return errors.New("invalid journal identity")
	}
	if (record.State != "pending" && record.State != "passed" && record.State != "failed") || (record.Cleanup != "confirmed" && record.Cleanup != "unconfirmed") {
		return errors.New("invalid journal state")
	}
	if record.State == "pending" {
		if record.Cleanup != "unconfirmed" || record.Report.ProbeID != "" {
			return errors.New("invalid pending journal")
		}
		return nil
	}
	r := record.Report
	if r.ProbeID != record.ProbeID || r.Status != record.State || r.Cleanup != record.Cleanup || !safeProbeStep(r.FailedStep) {
		return errors.New("invalid terminal journal")
	}
	if record.State == "passed" && (r.FailedStep != "" || record.Cleanup != "confirmed") {
		return errors.New("invalid passing journal")
	}
	return nil
}

func safeProbeStep(step string) bool {
	switch step {
	case "", "identifier", "storage_unavailable", "write_initial", "read_initial_missing", "read_initial_failed", "read_initial_mismatch", "write_replace", "read_replace_missing", "read_replace_failed", "read_replace_mismatch", "cleanup_delete", "cleanup_verify_failed", "interrupted", "journal_terminal":
		return true
	}
	return false
}

func encodeJournal(record journalRecord) ([]byte, error) {
	b, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	b = append(b, '\n')
	if len(b) > maxJournalBytes {
		return nil, errors.New("journal too large")
	}
	return b, nil
}

func atomicWriteJournal(path string, b []byte, syncParent func(string) error) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("terminal journal target is unsafe")
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmpPath, path)
	}
	if err == nil {
		if syncParent == nil {
			syncParent = syncParentDirectory
		}
		err = syncParent(path)
	}
	if err != nil {
		return err
	}
	ok = true
	return nil
}

func syncParentDirectory(path string) error {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
