package domain

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode/utf8"
)

// A pointer preserves the difference between an explicit false and an omitted
// decision. References are controller facts, never model-granted authority.
type CodingReviewCheck struct {
	ID              string   `json:"id"`
	Passed          *bool    `json:"passed"`
	Summary         string   `json:"summary"`
	VerificationID  string   `json:"verification_id,omitempty"`
	AcceptanceIndex *int     `json:"acceptance_index,omitempty"`
	ReferenceDigest string   `json:"reference_digest,omitempty"`
	Paths           []string `json:"paths"`
}

type CodingReviewReference struct {
	ID              string `json:"id"`
	VerificationID  string `json:"verification_id,omitempty"`
	AcceptanceIndex *int   `json:"acceptance_index,omitempty"`
	ReferenceDigest string `json:"reference_digest,omitempty"`
}

type CodingReviewContract struct {
	MissionID           string                  `json:"mission_id"`
	Epoch               int64                   `json:"epoch"`
	PlanRevision        int64                   `json:"plan_revision"`
	CandidateGeneration GenerationRef           `json:"candidate_generation"`
	PlanDigest          string                  `json:"plan_digest"`
	TraceDigest         string                  `json:"trace_digest"`
	EvidenceDigest      string                  `json:"evidence_digest"`
	ExecutionApproval   Approval                `json:"execution_approval"`
	BindingDigest       string                  `json:"binding_digest"`
	Required            []CodingReviewReference `json:"required_checks"`
}

// Review attempts are excluded to avoid QA invalidating itself or Auditor
// invalidating QA. CodingTraceDigest remains the full archived-run binding.
func CodingReviewTraceDigest(m Mission) string {
	attempts := []CodingAttempt{}
	for _, a := range m.CodingAttempts {
		if a.Phase != "qa" && a.Phase != "auditor" {
			attempts = append(attempts, a)
		}
	}
	return codingDigest("pre-review trace", attempts)
}

func NewCodingReviewContract(m Mission, role Role) (CodingReviewContract, error) {
	var c CodingReviewContract
	if m.CodingVersion != 1 || (role != QA && role != Auditor) || m.ID == "" || m.Plan == nil || m.CandidateGeneration == nil || m.CandidateGeneration.Digest != m.CandidateDigest {
		return c, errors.New("missing coding review identity")
	}
	if err := ValidatePlan(*m.Plan, m.Spec); err != nil {
		return c, err
	}
	if err := ValidatePlanAdjustments(m, *m.Plan); err != nil {
		return c, err
	}
	for _, a := range m.Plan.Adjustments {
		if a.Unresolved {
			return c, errors.New("unresolved failure cannot pass review")
		}
	}
	for i := len(m.Approvals) - 1; i >= 0; i-- {
		if m.Approvals[i].Kind == "execute" {
			c.ExecutionApproval = m.Approvals[i]
			break
		}
	}
	if c.ExecutionApproval.Digest == "" || c.ExecutionApproval.PlanRevision != m.Plan.Revision {
		return c, errors.New("current execution approval required for review")
	}
	if len(m.Evidence) == 0 {
		return c, errors.New("review requires verification")
	}
	ids := map[string]bool{}
	for _, evidence := range m.Evidence {
		if evidence.ID == "" || ids[evidence.ID] {
			return c, errors.New("ambiguous verification identity")
		}
		ids[evidence.ID] = true
	}
	v := m.Evidence[len(m.Evidence)-1]
	if v.ID == "" || !v.Passed || v.CandidateDigest != m.CandidateDigest || v.AcceptanceRevision != m.Plan.Revision || v.ImageID != m.Spec.ImageID {
		return c, errors.New("review verification is not current and passing")
	}
	if err := validateCommandResults(v.Results, m.Spec.Acceptance); err != nil {
		return c, err
	}
	linked := false
	for _, a := range m.CodingAttempts {
		if a.Epoch == m.Epoch && a.PlanRevision == m.Plan.Revision && a.Phase == "verification" && a.Status == "succeeded" && a.VerificationID == v.ID && reflect.DeepEqual(a.After, m.CandidateGeneration) {
			linked = true
		}
		if a.Phase != "qa" && a.Phase != "auditor" && a.Status == "running" {
			return c, errors.New("execution trace is not terminal")
		}
	}
	if !linked {
		return c, errors.New("verification has no current controller attempt")
	}
	plan := *m.Plan
	plan.Steps = append([]Step(nil), plan.Steps...)
	for i := range plan.Steps {
		plan.Steps[i].Status = ""
	}
	c.MissionID, c.Epoch, c.PlanRevision = m.ID, m.Epoch, m.Plan.Revision
	c.CandidateGeneration = *m.CandidateGeneration
	c.PlanDigest = codingDigest("review plan", plan)
	c.TraceDigest = CodingReviewTraceDigest(m)
	// Unlike the historical digest, C2 binds even unknown/legacy provenance.
	c.EvidenceDigest = codingDigest("review evidence", m.Evidence)
	c.BindingDigest = codingDigest("review binding", struct {
		Contract CodingReviewContract
		Spec     Spec
		Base     string
		Context  []FileMeta
		Changes  []Change
		Failure  *FailureBundle
	}{c, m.Spec, m.BaseCommit, m.ContextManifest, m.Changes, m.ReplanContext})
	if role == QA {
		for i := range m.Spec.Acceptance {
			index := i
			c.Required = append(c.Required, CodingReviewReference{ID: fmt.Sprintf("acceptance:%d", i), VerificationID: v.ID, AcceptanceIndex: &index})
		}
	} else {
		for _, id := range []string{"scope", "protected", "approval", "integrity"} {
			c.Required = append(c.Required, CodingReviewReference{ID: id, ReferenceDigest: codingDigest("auditor "+id, c.BindingDigest)})
		}
	}
	return c, nil
}

func boundedReviewText(s string) bool {
	return strings.TrimSpace(s) != "" && len(s) <= 4000 && utf8.ValidString(s)
}

// ValidateCodingReview re-resolves references against current material, not
// just the originally supplied prompt. Engine separately verifies disk/authority.
func ValidateCodingReview(m Mission, r Review) error {
	c, err := NewCodingReviewContract(m, r.Role)
	if err != nil {
		return err
	}
	if r.CodingVersion != 1 || r.PlanRevision != c.PlanRevision || r.TraceDigest != c.TraceDigest || r.BindingDigest != c.BindingDigest || r.CandidateDigest != m.CandidateDigest || !boundedReviewText(r.Summary) || r.Findings == nil {
		return errors.New("coding review binding or summary invalid")
	}
	paths := map[string]bool{}
	for _, f := range m.ContextManifest {
		paths[f.Path] = true
	}
	for _, change := range m.Changes {
		paths[change.Path] = true
	}
	validPaths := func(ps []string) bool {
		if ps == nil || len(ps) > 128 {
			return false
		}
		seen := map[string]bool{}
		for _, p := range ps {
			if !paths[p] || seen[p] {
				return false
			}
			seen[p] = true
		}
		return true
	}
	if len(r.Findings) > 64 {
		return errors.New("too many review findings")
	}
	for _, f := range r.Findings {
		if !boundedReviewText(f.Summary) || !validPaths(f.Paths) {
			return errors.New("invalid review finding text or paths")
		}
		switch f.Severity {
		case "info", "low", "medium":
		case "high", "critical", "blocking":
			if r.Passed {
				return errors.New("blocking finding cannot pass")
			}
		default:
			return errors.New("invalid finding severity")
		}
	}
	if len(r.Checks) != len(c.Required) {
		return errors.New("mandatory review checks missing or duplicated")
	}
	refs := map[string]CodingReviewReference{}
	for _, ref := range c.Required {
		refs[ref.ID] = ref
	}
	for _, check := range r.Checks {
		ref, ok := refs[check.ID]
		if !ok || check.Passed == nil || !boundedReviewText(check.Summary) || !validPaths(check.Paths) || check.VerificationID != ref.VerificationID || !reflect.DeepEqual(check.AcceptanceIndex, ref.AcceptanceIndex) || check.ReferenceDigest != ref.ReferenceDigest {
			return errors.New("invalid, duplicate, foreign or stale review check")
		}
		if r.Passed && !*check.Passed {
			return errors.New("failed mandatory check cannot pass review")
		}
		delete(refs, check.ID)
	}
	return nil
}
