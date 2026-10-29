package engine

import (
	"context"
	"foundry/internal/domain"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeCodingDependencyUnsafeWindowsRoot(t *testing.T) {
	for _, profile := range []string{"c4_node_model_v2", "c4_typescript_model_v2", "c4_python_model_v2"} {
		t.Run(profile, func(t *testing.T) {
			f := newDependencyMetadataFixture(t, profile)
			defer f.assertNoExecution(t)
			root, err := f.e.dependencyStoreRoot()
			if err != nil {
				t.Fatal(err)
			}
			// Characterize the old fresh-root behavior through actual Win32:
			// mode0700 is not a protected DACL. Do not fix it in the fixture.
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			security := func() *windows.SECURITY_DESCRIPTOR {
				t.Helper()
				sd, err := windows.GetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
				if err != nil {
					t.Fatal(err)
				}
				return sd
			}
			sd := security()
			control, _, err := sd.Control()
			if err != nil || control&windows.SE_DACL_PROTECTED != 0 {
				t.Fatalf("ordinary MkdirAll fixture unexpectedly protected: %v", err)
			}
			before := sd.String()
			marker := filepath.Join(root, "owner.txt")
			if err := os.WriteFile(marker, []byte("do not adopt or repair"), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := f.e.ImportCodingDependencies(context.Background(), f.request)
			if err != domain.ImportStorageFailed || result.AdmissionDigest != "" {
				t.Fatalf("unsafe existing root must fail at storage admission: %+v %v", result, err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 || entries[0].Name() != "owner.txt" || security().String() != before {
				t.Fatalf("failed import changed unsafe root or DACL: %v", err)
			}
			if b, err := os.ReadFile(marker); err != nil || string(b) != "do not adopt or repair" {
				t.Fatal("failed import changed existing data")
			}
		})
	}
}
