package repositorytask

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"foundry/internal/workspace"
	"reflect"
)

// Gateway has deliberately no router, credential, provider, or process seam.
type Gateway struct{ store *store.Store }

func New(s *store.Store) *Gateway { return &Gateway{store: s} }

type requestInput struct {
	Identity struct {
		MissionID string               `json:"mission_id"`
		Epoch     int64                `json:"epoch"`
		Candidate domain.GenerationRef `json:"candidate_generation"`
	} `json:"coding_identity"`
	Goal     string                `json:"goal"`
	Spec     domain.Spec           `json:"spec"`
	Context  map[string]string     `json:"context"`
	Plan     *domain.Plan          `json:"plan"`
	Changes  []domain.Change       `json:"changes"`
	Evidence []domain.Verification `json:"evidence"`
	Failure  *domain.FailureBundle `json:"replan_context"`
	Extra    json.RawMessage       `json:"extra"`
}

func (g *Gateway) Complete(ctx context.Context, req model.Request) (model.Result, error) {
	if err := ctx.Err(); err != nil {
		return model.Result{}, err
	}
	if g == nil || g.store == nil || len(req.Prompt) > 2*1024*1024 {
		return model.Result{}, errors.New("owned local store and bounded request required")
	}
	var in requestInput
	if err := json.Unmarshal([]byte(req.Prompt), &in); err != nil {
		return model.Result{}, err
	}
	m, err := g.store.GetMission(in.Identity.MissionID)
	if err != nil {
		return model.Result{}, err
	}
	if m.RepositoryImport == nil {
		return model.Result{}, errors.New("frozen repository import required")
	}
	receipt, err := g.store.GetRepositoryInspection(m.RepositoryImport.InspectionID)
	if err != nil || receipt.Digest != m.RepositoryImport.InspectionDigest || !reflect.DeepEqual(m.Spec.Protected, append(append([]string{}, receipt.Spec.Protected...), receipt.ProtectedInputs...)) {
		return model.Result{}, errors.New("protected import inputs drifted")
	}
	return complete(m, req)
}

func complete(m domain.Mission, req model.Request) (model.Result, error) {
	bad := errors.New("repository simulation requires exact current controller identity, context and authority")
	fail := func() (model.Result, error) { return model.Result{}, bad }
	var in requestInput
	if len(req.Prompt) > 2*1024*1024 || json.Unmarshal([]byte(req.Prompt), &in) != nil {
		return fail()
	}
	if m.ID != in.Identity.MissionID || m.Epoch != in.Identity.Epoch || m.CandidateGeneration == nil || *m.CandidateGeneration != in.Identity.Candidate || m.CandidateGeneration.Digest != m.CandidateDigest || m.Goal != in.Goal || m.Spec.C4 == nil || m.Spec.C4.Mode != "synthetic" || m.Spec.C4.ControlVersion != 0 || m.CodingVersion != 1 || m.ImportVersion != 1 || m.RepositoryImport == nil || m.WorkspaceMode != "generations_v1" || m.ToolMode != "" || m.TaskBindings != nil || m.MCPBindings != nil || m.ModelSnapshot != nil || req.Selection != nil || !reflect.DeepEqual(m.Spec, in.Spec) || !reflect.DeepEqual(m.Plan, in.Plan) || !reflect.DeepEqual(m.Changes, in.Changes) || !reflect.DeepEqual(m.Evidence, in.Evidence) || !reflect.DeepEqual(m.ReplanContext, in.Failure) {
		return fail()
	}
	if len(m.Profiles) != 7 || !reflect.DeepEqual(req.Profile, m.Profiles[req.Role]) {
		return fail()
	}
	for _, role := range []domain.Role{domain.Main, domain.Researcher, domain.Planner, domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
		p, ok := m.Profiles[role]
		if !ok || p.ID == "" || p.Model == "" || p.Kind != "synthetic" || p.Protocol != "chat_completions" || p.BaseURL != "https://synthetic.invalid" || p.HasKey || p.AccountID != "" || p.RouteID != "" || p.AccountRevision != 0 || p.CredentialVersion != 0 {
			return fail()
		}
	}
	if len(in.Context) != len(m.ContextManifest) || len(in.Context) > 30 {
		return fail()
	}
	for _, f := range m.ContextManifest {
		body, ok := in.Context[f.Path]
		if !ok || int64(len(body)) != f.Bytes || fmt.Sprintf("%x", sha256.Sum256([]byte(body))) != f.SHA256 {
			return fail()
		}
	}
	d, err := Parse([]byte(in.Context[Descriptor]))
	if err != nil {
		return model.Result{}, err
	}
	spec := d.ImportSpec()
	if d.Goal != m.Goal || !reflect.DeepEqual(spec.Scope, m.Spec.Scope) || len(m.Spec.Protected) < len(spec.Protected) || !reflect.DeepEqual(spec.Protected, m.Spec.Protected[:len(spec.Protected)]) || !reflect.DeepEqual(spec.Acceptance, m.Spec.Acceptance) || spec.Image != m.Spec.Image || m.RepositoryImport.ImportSpecDigest != domain.RepositoryImportSpecDigest(spec) {
		return fail()
	}
	for _, p := range m.Spec.Protected[len(spec.Protected):] {
		protected := workspace.Protected(p)
		for _, rule := range spec.Protected {
			protected = protected || workspace.Matches(p, rule)
		}
		if !protected {
			return fail()
		}
	}
	if m.ReplanContext != nil && domain.ValidateReplanContext(m) != nil {
		return fail()
	}
	execute := req.Role == domain.Worker || req.Role == domain.Debugger || req.Role == domain.QA || req.Role == domain.Auditor
	kind := "context"
	if execute {
		kind = "execute"
	}
	approved := false
	for i := len(m.Approvals) - 1; i >= 0; i-- {
		a := m.Approvals[i]
		if a.Kind == kind {
			approved = a.Digest != "" && (!execute || m.Plan != nil && a.PlanRevision == m.Plan.Revision)
			break
		}
	}
	// Exact approval seals, immutable trees and dispatch fencing are checked by
	// the existing controller immediately before this gateway, not reimplemented.
	if !approved {
		return fail()
	}
	var current domain.CodingAttempt
	if execute {
		if m.Plan == nil || len(m.CodingAttempts) == 0 || (req.Role == domain.Worker && m.State != "running") || (req.Role != domain.Worker && m.State != "verifying") {
			return fail()
		}
		current = m.CodingAttempts[len(m.CodingAttempts)-1]
		if current.Status != "running" || current.Role != req.Role || current.Phase != string(req.Role) || current.Epoch != m.Epoch || current.PlanRevision != m.Plan.Revision || !reflect.DeepEqual(current.Before, m.CandidateGeneration) {
			return fail()
		}
	}
	var output any
	switch req.Role {
	case domain.Planner:
		if m.State != "planning" {
			return fail()
		}
		p := d.Plan()
		if in.Failure != nil {
			ids := []string{}
			for _, s := range p.Steps {
				ids = append(ids, s.ID)
			}
			for _, f := range in.Failure.Failures {
				p.Adjustments = append(p.Adjustments, domain.PlanAdjustment{FailureID: f.ID, StepIDs: ids, Reason: "Reapply the descriptor proposal and recheck frozen assertions; this is not proof of correction", Unresolved: f.Code != "verification_failed"})
			}
		}
		steps := []map[string]any{}
		for _, s := range p.Steps {
			steps = append(steps, map[string]any{"id": s.ID, "title": s.Title, "description": s.Description, "depends_on": s.DependsOn, "files": s.Files})
		}
		proposal := map[string]any{"summary": p.Summary, "steps": steps}
		if in.Failure != nil {
			proposal["adjustments"] = p.Adjustments
		}
		output = proposal
	case domain.Worker:
		var step domain.Step
		if json.Unmarshal(in.Extra, &step) != nil || step.ID != current.StepID {
			return fail()
		}
		found := false
		for _, s := range m.Plan.Steps {
			if s.ID == step.ID {
				found = reflect.DeepEqual(s, step)
			}
		}
		if !found {
			return fail()
		}
		edits, err := d.workerEdits(step.Files)
		if err != nil {
			return model.Result{}, err
		}
		output = map[string]any{"summary": "SYNTHETIC descriptor-provided full-file proposal", "edits": edits}
	case domain.Debugger:
		var extra struct {
			Round int `json:"repair_round"`
		}
		if json.Unmarshal(in.Extra, &extra) != nil || extra.Round != current.RepairRound || extra.Round < 1 || extra.Round > len(d.Repairs) || len(m.Evidence) == 0 || len(m.C4Operations) == 0 || len(m.CodingAttempts) < 2 {
			return fail()
		}
		v := m.Evidence[len(m.Evidence)-1]
		o := m.C4Operations[len(m.C4Operations)-1]
		if o.ID != v.ID {
			return fail()
		}
		a := m.CodingAttempts[len(m.CodingAttempts)-2]
		if v.ID == "" || v.Passed || v.CandidateDigest != m.CandidateDigest || v.AcceptanceRevision != m.Plan.Revision || v.EffectiveSource() != "synthetic_stub" || o.Kind != "candidate" || o.MissionID != m.ID || o.Epoch != m.Epoch || o.PlanRevision != m.Plan.Revision || o.CandidateDigest != m.CandidateDigest || !reflect.DeepEqual(o.CandidateGeneration, m.CandidateGeneration) || o.State != "finished" || o.Result == nil || o.Result.Class != "test_failed" || o.CleanupState() != "confirmed" || a.Phase != "verification" || a.Status != "failed" || a.FailureCode != "verification_failed" || a.VerificationID != v.ID || a.Epoch != m.Epoch || a.PlanRevision != m.Plan.Revision || !reflect.DeepEqual(a.Before, m.CandidateGeneration) {
			return fail()
		}
		allowed := map[string]bool{}
		for _, s := range m.Plan.Steps {
			for _, p := range s.Files {
				allowed[p] = true
			}
		}
		contents := map[string]string{}
		for p, c := range in.Context {
			contents[p] = c
		}
		for _, c := range m.Changes {
			contents[c.Path] = c.After
		}
		edits := d.Repairs[extra.Round-1]
		changed := false
		for _, e := range edits {
			if !allowed[e.Path] {
				return fail()
			}
			changed = changed || contents[e.Path] != *e.Content
		}
		if !changed {
			return fail()
		}
		output = map[string]any{"summary": "SYNTHETIC descriptor repair bound to current recorded failure", "edits": edits}
	case domain.QA, domain.Auditor:
		contract, err := domain.NewCodingReviewContract(m, req.Role)
		var supplied domain.CodingReviewContract
		if err != nil || json.Unmarshal(in.Extra, &supplied) != nil || !reflect.DeepEqual(contract, supplied) || len(m.Evidence) == 0 || m.Evidence[len(m.Evidence)-1].EffectiveSource() != "synthetic_stub" {
			return fail()
		}
		checks := []domain.CodingReviewCheck{}
		passed := true
		for _, ref := range contract.Required {
			checks = append(checks, domain.CodingReviewCheck{ID: ref.ID, Passed: &passed, Summary: "SYNTHETIC assessment of the exact controller reference; no command executed", VerificationID: ref.VerificationID, AcceptanceIndex: ref.AcceptanceIndex, ReferenceDigest: ref.ReferenceDigest, Paths: []string{}})
		}
		output = map[string]any{"passed": true, "summary": "SYNTHETIC independent role checklist, not compiler execution or model reasoning", "findings": []any{}, "binding_digest": contract.BindingDigest, "checks": checks}
	case domain.Main:
		output = map[string]string{"answer": "SYNTHETIC repository task: descriptor-provided proposals and frozen-content checks only. Baseline, context, execution and delivery require separate decisions. No model, compiler, Docker, provider or account is used."}
	default:
		return fail()
	}
	raw, err := json.Marshal(output)
	return model.Result{Text: string(raw), UsageKnown: true}, err
}
