package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func inspectionFixture() RepositoryInspection {
	n := 1
	b := int64(7)
	v := RepositoryInspection{SchemaVersion: 1, ID: "inspection", Root: "/tmp/repo", Branch: "main", Commit: strings.Repeat("a", 40), Spec: RepositoryImportSpec{Scope: []string{"file.txt"}, Acceptance: []string{"test -f file.txt"}}, PolicyVersion: "committed_text_v1", Status: "ready", Stage: "complete", CheckedAt: "2026-10-21T00:00:00Z", WorkingTreeStatus: "not_checked", FileCount: &n, TotalBytes: &b, ContextFileCount: &n, ContextBytes: &b, ContextManifest: []FileMeta{{Path: "file.txt", Bytes: 7, SHA256: strings.Repeat("b", 64)}}, ProtectedInputs: []string{}}
	v.Digest = RepositoryInspectionDigest(v)
	return v
}

// Omitting any immutable field from the digest permits this mutation matrix.
func TestRepositoryInspectionDigestAndSelection(t *testing.T) {
	v := inspectionFixture()
	if err := ValidateRepositoryInspection(v); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*RepositoryInspection){
		func(v *RepositoryInspection) { v.ID = "other" }, func(v *RepositoryInspection) { v.Root = "/other" }, func(v *RepositoryInspection) { v.Branch = "other" }, func(v *RepositoryInspection) { v.Commit = strings.Repeat("c", 40) }, func(v *RepositoryInspection) { v.CheckedAt = "2026-10-20T00:00:00Z" }, func(v *RepositoryInspection) { v.Spec.Image = "other" }, func(v *RepositoryInspection) { v.RepositoryID = "repo"; v.RepositoryRevision = 1 }, func(v *RepositoryInspection) { v.ProtectedInputs = []string{"file.txt"} }, func(v *RepositoryInspection) { n := 2; v.FileCount = &n }, func(v *RepositoryInspection) {
			v.ContextManifest = []FileMeta{{Path: "file.txt", Bytes: 7, SHA256: strings.Repeat("c", 64)}}
		},
	} {
		copy := v
		change(&copy)
		if ValidateRepositoryInspection(copy) == nil {
			t.Fatalf("accepted altered record: %+v", copy)
		}
	}
	selection := ImportSelection{InspectionID: v.ID, Digest: v.Digest}
	if err := ValidateImportSelection(v, selection); err != nil {
		t.Fatal(err)
	}
	v.Status = "blocked"
	v.Stage = "tree"
	v.Code = ImportBinary
	v.FileCount = nil
	v.TotalBytes = nil
	v.ContextFileCount = nil
	v.ContextBytes = nil
	v.ContextManifest = nil
	v.ProtectedInputs = nil
	v.Digest = RepositoryInspectionDigest(v)
	if err := ValidateRepositoryInspection(v); err != nil {
		t.Fatal(err)
	}
	selection.Digest = v.Digest
	if ValidateImportSelection(v, selection) == nil {
		t.Fatal("selected blocked record")
	}
	raw, _ := json.Marshal(v)
	if !strings.Contains(string(raw), `"file_count":null`) {
		t.Fatal(string(raw))
	}
}

func TestRepositoryInspectionRejectsBoundsAndUnknownCategories(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*RepositoryInspection)
	}{
		{"schema", func(v *RepositoryInspection) { v.SchemaVersion = 2 }},
		{"id", func(v *RepositoryInspection) { v.ID = strings.Repeat("x", 65) }},
		{"scope", func(v *RepositoryInspection) { v.Spec.Scope = make([]string, 501) }},
		{"aggregate_spec", func(v *RepositoryInspection) {
			v.Spec.Scope = make([]string, 500)
			for i := range v.Spec.Scope {
				v.Spec.Scope[i] = strings.Repeat("a", 4096)
			}
		}},
		{"protected", func(v *RepositoryInspection) { v.Spec.Protected = make([]string, 501) }},
		{"commands", func(v *RepositoryInspection) { v.Spec.Acceptance = make([]string, 13) }},
		{"command_bytes", func(v *RepositoryInspection) { v.Spec.Acceptance = []string{strings.Repeat("x", 8193)} }},
		{"root", func(v *RepositoryInspection) { v.Root = "/" + strings.Repeat("x", 4096) }},
		{"status", func(v *RepositoryInspection) { v.Status = "partial_ready" }},
		{"policy", func(v *RepositoryInspection) { v.PolicyVersion = "future" }},
		{"dirty", func(v *RepositoryInspection) { v.WorkingTreeStatus = "clean" }},
		{"count", func(v *RepositoryInspection) { n := 501; v.FileCount = &n }},
		{"byte_count", func(v *RepositoryInspection) { b := int64(10<<20 + 1); v.TotalBytes = &b }},
		{"unknown_measured", func(v *RepositoryInspection) { v.TotalBytes = nil }},
		{"manifest_bytes", func(v *RepositoryInspection) { b := int64(6); v.ContextBytes = &b }},
		{"duplicate_inputs", func(v *RepositoryInspection) { v.ProtectedInputs = []string{"a", "a"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := inspectionFixture()
			tc.change(&v)
			v.Digest = RepositoryInspectionDigest(v)
			if ValidateRepositoryInspection(v) == nil {
				t.Fatal("invalid record accepted")
			}
		})
	}
	v := inspectionFixture()
	zero := int64(0)
	v.TotalBytes = &zero
	v.ContextBytes = &zero
	v.ContextManifest[0].Bytes = 0
	v.Digest = RepositoryInspectionDigest(v)
	if err := ValidateRepositoryInspection(v); err != nil {
		t.Fatal("known empty file rejected", err)
	}
}
