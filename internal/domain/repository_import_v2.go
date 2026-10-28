package domain

import (
	"foundry/internal/dependencyadmission"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// C4ImportSelectionV2 is an inspected declaration, never execution authority.
type C4ImportSelectionV2 struct {
	SchemaVersion          int                   `json:"schema_version"`
	ProfileID              string                `json:"profile_id"`
	EmbeddedVM             C4VMIdentity          `json:"embedded_vm"`
	RuntimeInventoryDigest string                `json:"runtime_inventory_digest"`
	AdmissionDigest        string                `json:"admission_digest"`
	Dependencies           C4DependencyBindingV2 `json:"dependencies"`
	SourceRoot             string                `json:"source_root"`
	TestRoot               string                `json:"test_root"`
	TestSupportPrefixes    []string              `json:"test_support_prefixes"`
}

func (s C4ImportSelectionV2) Validate() error {
	language, _, _, err := c4LanguageProfileV2(s.ProfileID)
	if err != nil || s.SchemaVersion != 2 || s.EmbeddedVM.SchemaVersion != 2 || s.EmbeddedVM.Validate() != nil || !c4SHA.MatchString(s.RuntimeInventoryDigest) || !c4SHA.MatchString(s.AdmissionDigest) || s.Dependencies.Validate() != nil || (s.SourceRoot != "." && s.SourceRoot != "src") || (s.TestRoot != "test" && s.TestRoot != "tests") || language == "python" && s.TestRoot != "tests" {
		return ImportInputs
	}
	ecosystem := "node"
	if language == "python" {
		ecosystem = "python"
	}
	if ecosystem != s.Dependencies.Ecosystem {
		return ImportDependencies
	}
	if !c4InputListV2(s.TestSupportPrefixes, newC4InputPathsV2(), 0) {
		return ImportInputs
	}
	return nil
}
func validInspectionVersion(v RepositoryInspection) bool {
	if v.SchemaVersion == 1 {
		return v.PolicyVersion == "committed_text_v1" && v.Spec.VerificationV2 == nil && v.VerificationV2 == nil
	}
	return v.SchemaVersion == 2 && v.PolicyVersion == "committed_text_c4_v2" && v.Spec.VerificationV2 != nil
}
func validateInspectionV2(v RepositoryInspection) error {
	s, c := v.Spec.VerificationV2, v.VerificationV2
	if s == nil || c == nil || s.Validate() != nil || c.Validate() != nil || c.ProfileID != s.ProfileID || c.AdmissionDigest != s.AdmissionDigest || c.Dependencies != s.Dependencies || !reflect.DeepEqual(c.SourceRoots, []string{s.SourceRoot}) {
		return ImportInputs
	}
	_, _, verifier, _ := c4LanguageProfileV2(s.ProfileID)
	if !reflect.DeepEqual(v.Spec.Acceptance, []string{verifier}) || v.Spec.Image != "" {
		return ImportInputs
	}
	for _, p := range c.TestFiles {
		if !strings.HasPrefix(p, s.TestRoot+"/") {
			return ImportInputs
		}
	}
	expected := append([]string{".foundry-build", ".venv", "node_modules", "site-packages", s.TestRoot}, s.TestSupportPrefixes...)
	sort.Strings(expected)
	expected = slices.Compact(expected)
	if !reflect.DeepEqual(expected, c.ProtectedPrefixes) {
		return ImportInputs
	}
	protected := make([]string, 0, len(c.Protected))
	for _, f := range c.Protected {
		protected = append(protected, f.Path)
	}
	if !reflect.DeepEqual(protected, v.ProtectedInputs) {
		return ImportInputs
	}
	return nil
}

// DeriveV2MissionMaterial checks only immutable records, so it is safe inside a
// store transaction. Payload integrity is checked separately by the engine.
func DeriveV2MissionMaterial(v RepositoryInspection, a dependencyadmission.AdmissionReceipt) (C4Profile, []string, error) {
	if ValidateRepositoryInspection(v) != nil || v.Status != "ready" || v.SchemaVersion != 2 || a.Validate() != nil {
		return C4Profile{}, nil, ImportInvalidBinding
	}
	s, c := v.Spec.VerificationV2, v.VerificationV2
	b := C4DependencyBindingV2(a.Bundle)
	if s.AdmissionDigest != a.Digest || s.Dependencies != b {
		return C4Profile{}, nil, ImportInvalidBinding
	}
	for _, f := range a.SourceFiles {
		found := false
		for _, source := range c.SourceFiles {
			if source.Path == f.Path && source.Bytes == f.Bytes && source.SHA256 == f.SHA256 {
				found = true
			}
		}
		if !found {
			return C4Profile{}, nil, ImportInvalidBinding
		}
	}
	expected := 2
	if b.Ecosystem == "python" {
		expected = 1
	}
	if len(a.SourceFiles) != expected {
		return C4Profile{}, nil, ImportInvalidBinding
	}
	language, runner, verifier, _ := c4LanguageProfileV2(s.ProfileID)
	id := s.EmbeddedVM
	p := C4Profile{SchemaVersion: 2, ControlVersion: 1, ID: s.ProfileID, Mode: "embedded_vm", Platform: id.GuestPlatform, Verifier: verifier, EmbeddedVM: &id, V2: &C4ProfileV2{Language: language, RunnerID: runner, RuntimeInventoryDigest: s.RuntimeInventoryDigest, DependencyBundleDigest: b.BundleDigest, ProtectedInputsDigest: c.ProtectedInputsDigest, ExecutionContractDigest: c.ExecutionContractDigest}}
	if c.ValidateProfileBinding(p) != nil {
		return C4Profile{}, nil, ImportInvalidBinding
	}
	protected := append([]string{}, v.Spec.Protected...)
	protected = append(protected, v.ProtectedInputs...)
	protected = append(protected, c.ProtectedPrefixes...)
	sort.Strings(protected)
	protected = slices.Compact(protected)
	return p, protected, nil
}
