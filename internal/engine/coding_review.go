package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/workspace"
	"io/fs"
	"os"
	"path/filepath"
)

// This schema is intentionally separate from reviewOutput: version zero does
// not acquire checklist fields or new parsing behavior.
type codingReviewOutput struct {
	Passed        *bool             `json:"passed"`
	Summary       string            `json:"summary"`
	Findings      legacyFindings    `json:"findings"`
	BindingDigest string            `json:"binding_digest"`
	Checks        []json.RawMessage `json:"checks"`
}

func codingChecks(raw []json.RawMessage, role domain.Role) ([]domain.CodingReviewCheck, error) {
	checks := []domain.CodingReviewCheck{}
	for _, b := range raw {
		var common struct {
			ID      string   `json:"id"`
			Passed  *bool    `json:"passed"`
			Summary string   `json:"summary"`
			Paths   []string `json:"paths"`
		}
		c := domain.CodingReviewCheck{}
		if role == domain.QA {
			out := struct {
				ID              string   `json:"id"`
				Passed          *bool    `json:"passed"`
				Summary         string   `json:"summary"`
				Paths           []string `json:"paths"`
				VerificationID  string   `json:"verification_id"`
				AcceptanceIndex *int     `json:"acceptance_index"`
			}{}
			if err := decodeControlled(string(b), &out); err != nil {
				return nil, err
			}
			common.ID, common.Passed, common.Summary, common.Paths = out.ID, out.Passed, out.Summary, out.Paths
			c.VerificationID, c.AcceptanceIndex = out.VerificationID, out.AcceptanceIndex
		} else {
			out := struct {
				ID              string   `json:"id"`
				Passed          *bool    `json:"passed"`
				Summary         string   `json:"summary"`
				Paths           []string `json:"paths"`
				ReferenceDigest string   `json:"reference_digest"`
			}{}
			if err := decodeControlled(string(b), &out); err != nil {
				return nil, err
			}
			common.ID, common.Passed, common.Summary, common.Paths = out.ID, out.Passed, out.Summary, out.Paths
			c.ReferenceDigest = out.ReferenceDigest
		}
		c.ID, c.Passed, c.Summary, c.Paths = common.ID, common.Passed, common.Summary, common.Paths
		checks = append(checks, c)
	}
	return checks, nil
}

func codingReviewPrompt(role domain.Role) string {
	base := commonPrompt + ` This is coding review version 1. extra is a controller-supplied review contract. Copy its binding_digest exactly; it binds task, epoch, candidate, plan, execution approval, evidence and pre-review trace. Return {"passed":true,"summary":"bounded assessment","findings":[],"binding_digest":"supplied digest","checks":[...]}. Each check requires explicit passed boolean, a bounded summary and paths array containing only real supplied paths (empty allowed). Include every required_checks ID exactly once. No extra check fields. Reject insufficient evidence. source_version=1 synthetic_stub/synthetic_content_check means no command executed; isolated_execution is distinct; unsupported or missing provenance is unknown. Model review is not test execution. `
	if role == domain.QA {
		return base + `QA: evaluate every frozen acceptance command against the latest supplied verification and its source. Check schema: {"id":"acceptance:0","passed":true,"summary":"...","verification_id":"supplied ID","acceptance_index":0,"paths":[]}. Do not emit reference_digest. A failed command cannot pass.`
	}
	return base + `Auditor: independently assess exact approved plan files/scope, protected/base integrity, current approval/plan identity, and evidence integrity. Required IDs: scope, protected, approval, integrity. Check schema: {"id":"scope","passed":true,"summary":"...","reference_digest":"supplied digest","paths":[]}. Do not emit verification_id or acceptance_index. Controller checks, not these opinions, enforce authority.`
}

func (e *Engine) reviewCodingCandidate(op *operation, role domain.Role, m domain.Mission) error {
	if err := e.checkCodingReviewMaterial(m); err != nil {
		return err
	}
	contract, err := domain.NewCodingReviewContract(m, role)
	if err != nil {
		return err
	}
	var out codingReviewOutput
	if err = e.call(op, role, contract, &out); err != nil {
		return err
	}
	if out.Passed == nil {
		return codingError("invalid_model_output", errors.New("explicit review decision required"))
	}
	checks, err := codingChecks(out.Checks, role)
	if err != nil {
		return codingError("invalid_model_output", err)
	}
	r := domain.Review{CodingVersion: 1, PlanRevision: contract.PlanRevision, TraceDigest: contract.TraceDigest, BindingDigest: out.BindingDigest, Checks: checks, Role: role, Passed: *out.Passed, Summary: out.Summary, Findings: out.Findings.domainFindings(), CandidateDigest: m.CandidateDigest, EvidenceDigest: domain.VerificationDigest(m.Evidence)}
	_, err = e.update(op, "review.completed", func(current *domain.Mission) error {
		if err := e.checkCodingReviewMaterial(*current); err != nil {
			return err
		}
		if err := domain.ValidateCodingReview(*current, r); err != nil {
			return codingError("invalid_model_output", err)
		}
		current.Reviews = append(current.Reviews, r)
		return nil
	})
	if err != nil {
		return err
	}
	if !r.Passed {
		return codingError("review_rejected", errors.New("independent checklist review blocked delivery"))
	}
	return nil
}

// No checkSnapshot/candidate call here: delivered reads also use this helper,
// and checkSnapshot itself calls verifyDelivery. Recheck the actual frozen base,
// candidate tree, authority and exact plan scope independently of model claims.
func (e *Engine) checkCodingReviewMaterial(m domain.Mission) error {
	if err := e.checkRepositoryImportBase(m); err != nil {
		return err
	}
	if err := e.checkReplanMaterial(m); err != nil {
		return err
	}
	if m.Plan == nil {
		return errors.New("review requires plan")
	}
	base := filepath.Join(e.dir(m.ID), "base")
	for _, includePlan := range []bool{false, true} {
		seal, err := authority(m, base, includePlan)
		if err != nil {
			return err
		}
		kind := "snapshot"
		if includePlan {
			kind = "execute"
		}
		if !hasSeal(m, kind, seal) {
			return errors.New("coding review approval inputs changed")
		}
	}
	candidate, err := e.selectedCandidate(m)
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, step := range m.Plan.Steps {
		for _, p := range step.Files {
			allowed[p] = true
		}
	}
	// Digest/selectedCandidate reject unsafe trees before bounded file reads.
	type file struct {
		content []byte
		mode    fs.FileMode
	}
	read := func(root string) (map[string]file, error) {
		files := map[string]file{}
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			i, err := d.Info()
			if err != nil {
				return err
			}
			if !i.Mode().IsRegular() {
				return errors.New("unsafe review tree")
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			files[filepath.ToSlash(rel)] = file{b, i.Mode()}
			return nil
		})
		return files, err
	}
	before, err := read(base)
	if err != nil {
		return err
	}
	after, err := read(candidate)
	if err != nil {
		return err
	}
	// Reconstruct only the bounded controller-recorded edits in memory. Every
	// intermediate before/after must agree; neither model prose nor a forged
	// Changes array may hide what the selected generation actually contains.
	expected := map[string][]byte{}
	for p, old := range before {
		expected[p] = old.content
	}
	for _, change := range m.Changes {
		if !allowed[change.Path] {
			return errors.New("recorded change outside exact plan files")
		}
		if err := workspace.ValidateEditPath(change.Path, m.Spec.Scope, m.Spec.Protected); err != nil {
			return err
		}
		if string(expected[change.Path]) != change.Before {
			return errors.New("recorded change chain differs from frozen base")
		}
		expected[change.Path] = []byte(change.After)
	}
	if len(expected) != len(after) {
		return errors.New("candidate file inventory differs from recorded changes")
	}
	for p, next := range after {
		want, ok := expected[p]
		if !ok || !bytes.Equal(want, next.content) {
			return errors.New("recorded changes differ from selected candidate")
		}
	}
	for p, old := range before {
		next, ok := after[p]
		if !ok || old.mode != next.mode {
			return errors.New("deleted file or changed mode outside edit protocol")
		}
	}
	for p, next := range after {
		old, exists := before[p]
		if exists && bytes.Equal(old.content, next.content) {
			continue
		}
		if !allowed[p] {
			return errors.New("candidate differs outside exact approved plan files")
		}
		if err := workspace.ValidateEditPath(p, m.Spec.Scope, m.Spec.Protected); err != nil {
			return err
		}
	}
	return nil
}
