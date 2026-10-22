package keychaincheck

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsCredentialProbeIsClosedWithoutJournal(t *testing.T) {
	p := filepath.Join(t.TempDir(), "journal")
	if Run([]string{"--journal", p}, io.Discard) == nil {
		t.Fatal("credential probe enabled")
	}
	if _, e := os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("unsupported probe wrote journal")
	}
	if Probe(p) == nil {
		t.Fatal("probe enabled")
	}
}
