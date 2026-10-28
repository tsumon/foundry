package engine

import (
	"context"
	"encoding/json"
	"foundry/internal/dependencyadmission"
	"foundry/internal/dependencybundle"
	"foundry/internal/domain"
	"foundry/internal/gitinspect"
	"foundry/internal/workspace"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

type CodingDependencyImportRequest struct {
	RepositoryID           string              `json:"repository_id,omitempty"`
	RepositoryRevision     int64               `json:"repository_revision,omitempty"`
	RepoPath               string              `json:"repo_path,omitempty"`
	ProfileID              string              `json:"profile_id"`
	EmbeddedVM             domain.C4VMIdentity `json:"embedded_vm"`
	RuntimeInventoryDigest string              `json:"runtime_inventory_digest"`
	InputDirectory         string              `json:"input_directory"`
	Consent                bool                `json:"consent"`
}
type DependencyPackageSummary struct {
	Location string `json:"location"`
	Name     string `json:"name"`
	Version  string `json:"version"`
}
type DependencySummary struct {
	SchemaVersion   int                        `json:"schema_version"`
	Ecosystem       string                     `json:"ecosystem"`
	Policy          string                     `json:"policy"`
	Provenance      string                     `json:"provenance"`
	AdmissionDigest string                     `json:"admission_digest"`
	BundleDigest    string                     `json:"bundle_digest"`
	ManifestDigest  string                     `json:"manifest_digest"`
	FileCount       int                        `json:"file_count"`
	TotalBytes      int64                      `json:"total_bytes"`
	PackageCount    int                        `json:"package_count"`
	Packages        []DependencyPackageSummary `json:"packages"`
	OmittedCount    int                        `json:"omitted_count"`
}
type CodingDependencyImportResult struct {
	AdmissionDigest string                             `json:"admission_digest"`
	Dependencies    domain.C4DependencyBindingV2       `json:"dependencies"`
	SourceFiles     []dependencyadmission.SourceDigest `json:"source_files"`
	Summary         DependencySummary                  `json:"summary"`
}

func dependencySummary(a dependencyadmission.AdmissionReceipt) (DependencySummary, error) {
	if a.Validate() != nil {
		return DependencySummary{}, domain.ImportDependencies
	}
	s := DependencySummary{SchemaVersion: 1, Ecosystem: a.Bundle.Ecosystem, Policy: a.Policy, Provenance: a.Provenance, AdmissionDigest: a.Digest, BundleDigest: a.Bundle.BundleDigest, ManifestDigest: a.Bundle.ManifestDigest, FileCount: a.Bundle.FileCount, TotalBytes: a.Bundle.TotalBytes, PackageCount: len(a.Packages), Packages: []DependencyPackageSummary{}, OmittedCount: len(a.Packages)}
	for _, p := range a.Packages {
		row := DependencyPackageSummary{p.Location, p.Name, p.Version}
		s.Packages = append(s.Packages, row)
		s.OmittedCount--
		raw, _ := json.Marshal(s)
		// Reserve room for the fixed language/layout/contract wrapper.
		if len(raw) > (64<<10)-1024 {
			s.Packages = s.Packages[:len(s.Packages)-1]
			s.OmittedCount++
			break
		}
	}
	return s, nil
}
func (e *Engine) dependencyStoreRoot() (string, error) {
	if e.c4Root == "" {
		return "", domain.ImportDependencies
	}
	root, err := filepath.EvalSymlinks(e.options.DataDir)
	if err != nil || root != e.c4Root {
		return "", domain.ImportDependencies
	}
	return filepath.Join(root, "dependency-bundles"), nil
}
func (e *Engine) ImportCodingDependencies(parent context.Context, q CodingDependencyImportRequest) (CodingDependencyImportResult, error) {
	zero := CodingDependencyImportResult{}
	if !q.Consent {
		return zero, domain.ImportConsentRequired
	}
	selector := domain.RepositoryInspectionRequest{RepositoryID: q.RepositoryID, RepositoryRevision: q.RepositoryRevision, RepoPath: q.RepoPath, Consent: true, Spec: domain.RepositoryImportSpec{Scope: []string{"metadata"}, Acceptance: []string{"test -f metadata"}}}
	if domain.ValidateRepositoryInspectionRequest(selector) != nil || !filepath.IsAbs(q.InputDirectory) || !e.currentV2Matches(q.ProfileID, q.EmbeddedVM, q.RuntimeInventoryDigest) {
		return zero, domain.ImportInvalidRequest
	}
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	source := q.RepoPath
	if q.RepositoryID != "" {
		r, err := e.s.GetGitRepositoryForInspection(ctx, q.RepositoryID)
		if err != nil || r.Revision != q.RepositoryRevision {
			return zero, domain.ImportSourceChanged
		}
		source = r.Path
	}
	observation, err := gitinspect.Inspect(ctx, source)
	if err != nil || observation.Unborn {
		return zero, domain.ImportUnavailable
	}
	if workspace.CheckLocation(ctx, observation.Root, e.options.DataDir) != nil {
		return zero, domain.ImportLocation
	}
	metadata, err := workspace.CommittedDependencyMetadata(ctx, observation.Root, observation.HEAD, q.ProfileID)
	if err != nil {
		return zero, domain.ImportDependencies
	}
	root, err := e.dependencyStoreRoot()
	if err != nil {
		return zero, err
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return zero, domain.ImportStorageFailed
	}
	var a dependencyadmission.AdmissionReceipt
	if q.ProfileID == "c4_python_model_v2" {
		a, err = dependencyadmission.ImportPython(ctx, root, q.InputDirectory, metadata[0])
	} else {
		a, err = dependencyadmission.ImportNode(ctx, root, q.InputDirectory, dependencyadmission.NodeSource{PackageLock: metadata[0], PackageJSON: metadata[1]})
	}
	if err != nil || a.Validate() != nil {
		return zero, domain.ImportDependencies
	}
	check := domain.RepositoryInspection{Root: observation.Root, Commit: observation.HEAD, RepositoryID: q.RepositoryID, RepositoryRevision: q.RepositoryRevision}
	if _, err = e.recheckImportSource(ctx, check, source); err != nil {
		return zero, domain.ImportSourceChanged
	}
	again, err := workspace.CommittedDependencyMetadata(ctx, observation.Root, observation.HEAD, q.ProfileID)
	if err != nil || !reflect.DeepEqual(again, metadata) || !e.currentV2Matches(q.ProfileID, q.EmbeddedVM, q.RuntimeInventoryDigest) {
		return zero, domain.ImportSourceChanged
	}
	if _, err = dependencybundle.DescribeVerified(ctx, root, a.Bundle); err != nil {
		return zero, domain.ImportDependencies
	}
	if err = e.s.SaveDependencyAdmission(a); err != nil {
		return zero, domain.ImportStorageFailed
	}
	summary, err := dependencySummary(a)
	if err != nil {
		return zero, err
	}
	return CodingDependencyImportResult{a.Digest, domain.C4DependencyBindingV2(a.Bundle), a.SourceFiles, summary}, nil
}

// Trusted immutable records plus configured root, never caller paths.
func (e *Engine) resolveV2Material(ctx context.Context, m domain.Mission) (domain.C4InputContractV2, string, dependencyadmission.AdmissionReceipt, error) {
	return e.resolveV2MaterialMode(ctx, m, true)
}
func (e *Engine) resolveV2MaterialMode(ctx context.Context, m domain.Mission, verify bool) (domain.C4InputContractV2, string, dependencyadmission.AdmissionReceipt, error) {
	if m.Spec.C4 == nil || m.Spec.C4.SchemaVersion != 2 || m.RepositoryImport == nil {
		return domain.C4InputContractV2{}, "", dependencyadmission.AdmissionReceipt{}, domain.ImportInvalidBinding
	}
	v, err := e.s.GetRepositoryInspection(m.RepositoryImport.InspectionID)
	if err != nil {
		return domain.C4InputContractV2{}, "", dependencyadmission.AdmissionReceipt{}, err
	}
	if domain.ValidateRepositoryInspection(v) != nil || v.SchemaVersion != 2 || v.Status != "ready" {
		return domain.C4InputContractV2{}, "", dependencyadmission.AdmissionReceipt{}, domain.ImportInvalidBinding
	}
	var a dependencyadmission.AdmissionReceipt
	if verify {
		a, err = e.validateV2Inspection(ctx, v)
	} else {
		a, err = e.s.GetDependencyAdmission(v.Spec.VerificationV2.AdmissionDigest)
	}
	if err != nil {
		return domain.C4InputContractV2{}, "", a, err
	}
	p, protected, err := domain.DeriveV2MissionMaterial(v, a)
	if err != nil || !reflect.DeepEqual(m.Spec.C4, &p) || !reflect.DeepEqual(m.Spec.Protected, protected) {
		return domain.C4InputContractV2{}, "", a, domain.ImportInvalidBinding
	}
	root, err := e.dependencyStoreRoot()
	return *v.VerificationV2, root, a, err
}
func (e *Engine) validateV2Inspection(ctx context.Context, v domain.RepositoryInspection) (dependencyadmission.AdmissionReceipt, error) {
	if domain.ValidateRepositoryInspection(v) != nil || v.VerificationV2 == nil {
		return dependencyadmission.AdmissionReceipt{}, domain.ImportInvalidBinding
	}
	a, err := e.s.GetDependencyAdmission(v.Spec.VerificationV2.AdmissionDigest)
	if err != nil {
		return a, domain.ImportDependencies
	}
	if _, _, err = domain.DeriveV2MissionMaterial(v, a); err != nil {
		return a, err
	}
	root, err := e.dependencyStoreRoot()
	if err != nil {
		return a, err
	}
	manifest, err := dependencybundle.DescribeVerified(ctx, root, a.Bundle)
	if err != nil {
		return a, domain.ImportDependencies
	}
	lock := "package-lock.json"
	if a.Bundle.Ecosystem == "python" {
		lock = "foundry.python.lock.json"
	}
	found := false
	for _, f := range a.SourceFiles {
		if f.Path == lock && manifest.LockSHA256 == f.SHA256 {
			found = true
		}
	}
	if !found {
		return a, domain.ImportDependencies
	}
	return a, nil
}

type verificationContextV2 struct {
	Language                string            `json:"language"`
	SourceRoots             []string          `json:"source_roots"`
	SelectedTestFileCount   int               `json:"selected_test_file_count"`
	ExecutionContractDigest string            `json:"execution_contract_digest"`
	Dependencies            DependencySummary `json:"dependencies"`
}

func (e *Engine) v2ContextSummary(m domain.Mission) (verificationContextV2, error) {
	c, _, a, err := e.resolveV2Material(context.Background(), m)
	if err != nil {
		return verificationContextV2{}, err
	}
	summary, err := dependencySummary(a)
	if err != nil {
		return verificationContextV2{}, err
	}
	return verificationContextV2{m.Spec.C4.V2.Language, c.SourceRoots, len(c.TestFiles), c.ExecutionContractDigest, summary}, nil
}
func (e *Engine) validateV2ContextSummary(m domain.Mission, prompt string) error {
	if m.Spec.C4 == nil || m.Spec.C4.SchemaVersion != 2 {
		return nil
	}
	expected, err := e.v2ContextSummary(m)
	if err != nil {
		return err
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(prompt), &payload) != nil {
		return domain.ImportInvalidBinding
	}
	raw, err := json.Marshal(expected)
	if err != nil || !reflect.DeepEqual(json.RawMessage(raw), payload["verification_v2"]) {
		return domain.ImportInvalidBinding
	}
	return nil
}
