//go:build windows

package vmruntime

import (
	"context"
	"foundry/internal/platformfs"
	"path/filepath"
	"testing"
)

func TestWindowsSessionCreatesProtectedExclusiveDirectory(t *testing.T) {
	for _, privateParent := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-parent", true: "protected-parent"}[privateParent], func(t *testing.T) {
			r, q := testRuntime(t)
			if privateParent {
				parent := filepath.Join(r.instanceRoot, "private")
				if e := platformfs.PrivateDirectory(parent); e != nil {
					t.Fatal(e)
				}
				r.instanceRoot = parent
			}
			s, e := r.Create(context.Background(), q)
			if e != nil {
				t.Fatal(e)
			}
			if e = platformfs.PrivateDirectory(s.dir); e != nil {
				t.Fatalf("session DACL lacks exact protection: %v", e)
			}
			if createPrivateDirectory(s.dir) == nil {
				t.Fatal("existing instance admitted")
			}
			if e = s.Close(context.Background()); e != nil {
				t.Fatal(e)
			}
		})
	}
}
