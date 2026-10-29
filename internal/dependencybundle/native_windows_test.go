package dependencybundle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"foundry/internal/platformfs"
)

// Go preserves the terminal dot in OpenRoot(".").Name(). It is a trusted
// handle-derived name, while the same spelling must stay invalid as user input.
func TestWindowsSyncGeneratedRootName(t *testing.T) {
	store, _ := dirs(t)
	root, err := os.OpenRoot(store)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	child, err := root.OpenRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	if platformfs.ValidateAbsolute(child.Name()) == nil {
		t.Fatal("external dot path was admitted")
	}
	if err := syncRoot(child); err != nil {
		t.Fatalf("trusted generated directory name cannot sync: %v", err)
	}
}

func TestWindowsInspectCanonicalNamespaceAliases(t *testing.T) {
	for _, tc := range []struct{ spelling, name string }{{"bad:name", "bad"}, {"trailing.", "trailing"}} {
		t.Run(tc.spelling, func(t *testing.T) {
			_, source := dirs(t)
			if err := os.WriteFile(filepath.Join(source, tc.spelling), nil, 0644); err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(source)
			if err != nil || len(entries) != 1 || entries[0].Name() != tc.name {
				t.Fatalf("unexpected materialized namespace: %v %v", entries, err)
			}
			m, err := InspectFrozen(context.Background(), source, "node", sum(nil), nil)
			if err != nil || len(m.Files) != 1 || m.Files[0].Path != tc.name || m.Files[0].Size != 0 {
				t.Fatalf("inspection did not bind canonical entry: %+v %v", m, err)
			}
			m.Files[0].Path = tc.spelling
			if _, err := m.Binding(); err == nil {
				t.Fatal("alias spelling became an admitted payload path")
			}
		})
	}
}
