package engine

import (
	"context"
	"foundry/internal/domain"
	"reflect"
)

func importSpecForReceipt(spec domain.Spec, v domain.RepositoryInspection) (domain.RepositoryImportSpec, error) {
	original := importSpec(spec)
	if original.Protected == nil {
		original.Protected = []string{}
	}
	expected := v.Spec
	expected.VerificationV2 = nil
	if expected.Protected == nil {
		expected.Protected = []string{}
	}
	if !reflect.DeepEqual(original, expected) {
		return domain.RepositoryImportSpec{}, domain.ImportSourceChanged
	}
	if v.SchemaVersion == 2 {
		if spec.C4 == nil || spec.C4.ID != v.Spec.VerificationV2.ProfileID {
			return domain.RepositoryImportSpec{}, domain.ImportInvalidBinding
		}
		original.VerificationV2 = v.Spec.VerificationV2
	}
	if domain.RepositoryImportSpecDigest(original) != domain.RepositoryImportSpecDigest(v.Spec) {
		return domain.RepositoryImportSpec{}, domain.ImportSourceChanged
	}
	return original, nil
}
func (e *Engine) bindV2Create(ctx context.Context, r *CreateRequest) error {
	if r.Spec.C4 == nil || r.Spec.C4.SchemaVersion != 2 {
		return nil
	}
	p := r.Spec.C4
	if _, err := domain.FixedC4VMPolicyV2(p.ID); err != nil {
		return ErrC4Unavailable
	}
	if r.ImportSelection == nil || p.ControlVersion != 0 && p.ControlVersion != 1 || p.Mode != "embedded_vm" || p.Platform != "" || p.Verifier != "" || p.EmbeddedVM != nil || p.V2 != nil || p.ImageID != "" || p.DockerPath != "" || p.Endpoint != "" {
		return domain.ImportInvalidRequest
	}
	v, err := e.s.GetRepositoryInspection(r.ImportSelection.InspectionID)
	if err != nil || domain.ValidateImportSelection(v, *r.ImportSelection) != nil || v.SchemaVersion != 2 {
		if _, priorErr := e.s.GetRepositoryImportByInspection(r.ImportSelection.InspectionID); priorErr == nil {
			return domain.ImportIntentConflict
		}
		return domain.ImportInvalidRecord
	}
	if p.ID != v.Spec.VerificationV2.ProfileID {
		return domain.ImportInvalidBinding
	}
	a, err := e.validateV2Inspection(ctx, v)
	if err != nil {
		return err
	}
	s := v.Spec.VerificationV2
	if !e.currentV2Matches(s.ProfileID, s.EmbeddedVM, s.RuntimeInventoryDigest) {
		return ErrC4Unavailable
	}
	profile, _, err := domain.DeriveV2MissionMaterial(v, a)
	if err != nil {
		return err
	}
	r.Spec.C4 = &profile
	return nil
}
