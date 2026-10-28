//go:build windows

package main

import (
	"foundry/internal/platformfs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

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
	after, err := inventory(source)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("restored bytes or owner/DACL differ", err)
	}
}
