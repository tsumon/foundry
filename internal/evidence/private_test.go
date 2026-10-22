package evidence

import (
	"foundry/internal/platformfs"
	"path/filepath"
	"testing"
)

func privateTestRoot(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "evidence")
	if e := platformfs.PrivateDirectory(p); e != nil {
		t.Fatal(e)
	}
	return p
}
