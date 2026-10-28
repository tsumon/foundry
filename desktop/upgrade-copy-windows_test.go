//go:build windows

package main

import (
	"foundry/internal/platformfs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWindowsUpgradeComparisonRejectsChanges(t *testing.T) {
	want := entry{rel: "coding\\evidence\\sealed.json", digest: "bytes-a", security: securityID{owner: "owner-a", aceDigest: "aces-a", protected: true}}
	tests := []struct {
		name, message string
		change        func(*entry)
	}{
		{"bytes", "file content changed", func(row *entry) { row.digest = "bytes-b" }},
		{"owner", "owner changed", func(row *entry) { row.security.owner = "owner-b" }},
		{"protection", "DACL protection changed", func(row *entry) { row.security.protected = false }},
		{"ordered ACEs", "DACL ACEs changed", func(row *entry) { row.security.aceDigest = "aces-b" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := want
			tc.change(&got)
			if err := compareEntry(want, got, "backup"); err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected %q rejection, got %v", tc.message, err)
			}
		})
	}
}

func TestWindowsUpgradeRejectsInheritedSourceRoot(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "backup")
	if err := copyExact(source, target); err == nil || !strings.Contains(err.Error(), "private backup source required") {
		t.Fatalf("expected private source rejection, got %v", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("rejected source created destination: %v", err)
	}
}

func TestWindowsUpgradePrivateCopyAndRestore(t *testing.T) {
	parent := t.TempDir()
	source := filepath.Join(parent, "source")
	nested := filepath.Join(source, "coding", "vm-bundles", "retained")
	for _, dir := range []string{source, filepath.Join(source, "coding"), filepath.Join(source, "coding", "vm-bundles"), nested} {
		if err := platformfs.PrivateDirectory(dir); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(nested, "manifest.json")
	if err := os.WriteFile(file, []byte("synthetic immutable runtime\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Store evidence children inherit the private coding ACL without carrying
	// SE_DACL_PROTECTED themselves; backup must preserve that case as well.
	evidence := filepath.Join(source, "coding", "evidence")
	if err := os.Mkdir(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(evidence, "sealed.json"), []byte("synthetic evidence\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if inherited, err := security(evidence); err != nil || inherited.protected {
		t.Fatal("fixture must exercise an inherited DACL", err)
	}
	before, err := inventory(source)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(parent, "backup")
	if err := copyExact(source, backup); err != nil {
		t.Fatal(err)
	}
	if err := platformfs.PrivateDirectory(filepath.Join(backup, "coding", "vm-bundles", "retained")); err != nil {
		t.Fatal("backup lost protected owner/DACL", err)
	}
	if sourceACL, err := security(evidence); err != nil {
		t.Fatal(err)
	} else if backupACL, err := security(filepath.Join(backup, "coding", "evidence")); err != nil || backupACL != sourceACL {
		t.Fatal("backup changed inherited evidence DACL", err)
	}
	failed := filepath.Join(parent, "failed-preserved")
	if err := os.Rename(source, failed); err != nil {
		t.Fatal(err)
	}
	if err := copyExact(backup, source); err != nil {
		t.Fatal(err)
	}
	if err := platformfs.PrivateDirectory(filepath.Join(source, "coding", "vm-bundles", "retained")); err != nil {
		t.Fatal("restore lost protected owner/DACL", err)
	}
	if sourceACL, err := security(evidence); err != nil {
		t.Fatal(err)
	} else if backupACL, err := security(filepath.Join(backup, "coding", "evidence")); err != nil || backupACL != sourceACL {
		t.Fatal("restore changed inherited evidence DACL", err)
	}
	after, err := inventory(source)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("restored bytes or owner/DACL differ", err)
	}
}
