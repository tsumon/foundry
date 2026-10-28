package vmacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/dependencybundle"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/platformfs"
	"foundry/internal/store"
	"foundry/internal/vmruntime"
	"foundry/internal/workspace"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"
)

// Run has no injectable VM or credential authority. Only an installed, verified
// production runtime can produce the native evidence required for Passed.
func Run(parent context.Context, c Config) (summary Summary, err error) {
	summary = Summary{SchemaVersion: 1, Kind: SummaryKind, Source: "embedded_vm_c4_control", Models: ModelProvenance, HostPlatform: runtime.GOOS + "/" + runtime.GOARCH, RoleCounts: map[domain.Role]int{}, Operations: []OperationEvidence{}, Stage: "admission"}
	reserved := false
	defer func() {
		if err != nil {
			summary.Passed = false
			if summary.FailureCode == "" {
				summary.FailureCode = summary.Stage + "_failed"
			}
		}
		if reserved {
			raw, e := json.MarshalIndent(summary, "", "  ")
			if e == nil {
				e = os.WriteFile(filepath.Join(c.DataDir, "summary.json"), append(raw, '\n'), 0600)
			}
			if e != nil {
				summary.Passed = false
				summary.FailureCode = "summary_persistence_failed"
				err = errors.New(summary.FailureCode)
			}
		}
	}()
	if err = c.Validate(); err != nil {
		summary.FailureCode = err.Error()
		return
	}
	f := fixtureSpec{path: "triple.go", initial: initialSource, worker: workerSource, repaired: repairedSource, verifier: "/opt/foundry/verify-go-v1", expectedTests: 2}
	if c.Profile != "" {
		f, err = selectFixture(c.Profile, c.Case)
		if err != nil {
			return
		}
		summary.SchemaVersion = 2
		summary.V2 = &EvidenceV2{ProfileID: f.profile, Case: f.caseID, Provenance: "repository_owned_local_fixture", DependencyFiles: []dependencybundle.File{}, ExpectedFiles: map[string]string{}}
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Minute)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return
	}
	summary.Stage = "reserve"
	if err = reserveDataRoot(c.DataDir); err != nil {
		return
	}
	reserved = true
	for _, name := range []string{"instances", "engine"} {
		if err = platformfs.PrivateDirectory(filepath.Join(c.DataDir, name)); err != nil {
			return
		}
	}
	summary.Stage = "bundle"
	var vm *vmruntime.Runtime
	var e error
	if c.Profile == "" {
		vm, e = vmruntime.Load(c.Bundle, filepath.Join(c.DataDir, "instances"))
	} else {
		vm, e = vmruntime.LoadV2(c.Bundle, filepath.Join(c.DataDir, "instances"))
	}
	if e != nil {
		err = e
		return
	}
	identity := vm.Identity()
	summary.ManifestDigest = identity.ManifestDigest
	summary.GuestPlatform = identity.GuestPlatform
	summary.Acceleration = "virtualization_framework"
	if identity.HostPlatform == "windows/amd64" {
		summary.Acceleration = "whpx"
	}
	summary.Stage = "repository"
	source := filepath.Join(c.DataDir, "source")
	if c.Profile == "" {
		err = createRepository(ctx, source)
	} else {
		err = f.create(ctx, source, filepath.Join(c.DataDir, "dependency-input"))
	}
	if err != nil {
		return
	}
	// This digest includes the committed .git tree, so source mutation cannot be
	// hidden by changing the index or commit while leaving the three files intact.
	summary.SourceDigest, err = sourceTreeDigest(source)
	if err != nil {
		return
	}
	protected := protectedDigests
	if c.Profile != "" {
		protected = f.protectedDigests
		summary.V2.ExpectedFiles = f.fileDigests()
	}
	summary.ProtectedDigests, err = protected(source)
	if err != nil {
		return
	}
	summary.Stage = "setup"
	s, e := store.Open(filepath.Join(c.DataDir, "acceptance.db"))
	if e != nil {
		err = e
		return
	}
	defer s.Close()
	fixture := newLocalFixture()
	if c.Profile != "" {
		fixture.spec = &f
	}
	server := httptest.NewServer(fixture)
	defer server.Close()
	lease := &fixtureLease{base: server.URL + "/v1"}
	var controller *engine.Engine
	router := model.NewRouter(nil, lease, model.RouterOptions{SimulationOrigins: []string{server.URL}, CodingAdmission: func(ctx context.Context, q model.Request) error {
		if controller == nil {
			return errors.New("controller_not_ready")
		}
		if e := controller.AdmitCodingModelCall(ctx, q); e != nil {
			return e
		}
		return fixture.authorize(q)
	}})
	var cap *engine.C4ControlledCapability
	if c.Profile == "" {
		cap, e = engine.NewCodingVMCapability(router, vm)
	} else {
		cap, e = engine.NewCodingVMCapabilityWithV2(router, nil, vm)
	}
	if e != nil {
		err = e
		return
	}
	controller = engine.New(s, router, engine.Options{DataDir: filepath.Join(c.DataDir, "engine"), CodingWorkbench: true, C4Controlled: cap, DisableControlledReadCreation: true})
	if c.Profile != "" {
		defer func() {
			if err != nil && summary.MissionID != "" && summary.V2.Inputs != nil {
				if current, getErr := s.GetMission(summary.MissionID); getErr == nil {
					summary.Operations = v2FailureOperations(current, vm, *summary.V2.Inputs)
				}
				fixture.mu.Lock()
				defer fixture.mu.Unlock()
				summary.RoleCounts = map[domain.Role]int{}
				for _, q := range fixture.calls {
					summary.RoleCounts[q.Role]++
				}
			}
		}()
	}
	defer controller.Close()
	defer func() {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		for _, q := range fixture.calls {
			summary.RoleCounts[q.Role]++
		}
	}()
	if err = registerFixture(s, lease.base); err != nil {
		return
	}
	spec := domain.RepositoryImportSpec{Scope: []string{"triple.go"}, Protected: []string{"go.mod", "triple_test.go"}, Acceptance: []string{"/opt/foundry/verify-go-v1"}, Image: "embedded-vm-native-acceptance"}
	if c.Profile != "" {
		summary.Stage = "dependencies"
		var selected *engine.CodingProfileTemplateV2
		for _, row := range controller.CodingProfilesV2() {
			if row.ProfileID == c.Profile {
				copy := row
				selected = &copy
			}
		}
		if selected == nil || !selected.Available || selected.EmbeddedVM == nil {
			err = errors.New("current_v2_profile_unavailable")
			return
		}
		admission, admitErr := controller.ImportCodingDependencies(ctx, engine.CodingDependencyImportRequest{RepoPath: source, ProfileID: c.Profile, EmbeddedVM: *selected.EmbeddedVM, RuntimeInventoryDigest: selected.RuntimeInventoryDigest, InputDirectory: filepath.Join(c.DataDir, "dependency-input"), Consent: true})
		if admitErr != nil {
			err = admitErr
			return
		}
		summary.V2.RuntimeInventoryDigest = selected.RuntimeInventoryDigest
		summary.V2.AdmissionDigest = admission.AdmissionDigest
		dependencyRoot := filepath.Join(c.DataDir, "engine", "dependency-bundles")
		manifest, manifestErr := dependencybundle.DescribeVerified(ctx, dependencyRoot, dependencybundle.Binding(admission.Dependencies))
		if manifestErr != nil {
			err = manifestErr
			return
		}
		summary.V2.DependencyFiles = manifest.Files
		spec = domain.RepositoryImportSpec{Scope: []string{f.path}, Protected: []string{}, Acceptance: []string{f.verifier}, VerificationV2: &domain.C4ImportSelectionV2{SchemaVersion: 2, ProfileID: c.Profile, EmbeddedVM: *selected.EmbeddedVM, RuntimeInventoryDigest: selected.RuntimeInventoryDigest, AdmissionDigest: admission.AdmissionDigest, Dependencies: admission.Dependencies, SourceRoot: f.sourceRoot, TestRoot: "tests", TestSupportPrefixes: []string{}}}
		summary.Stage = "inspection"
	}
	inspection, e := controller.InspectRepository(ctx, domain.RepositoryInspectionRequest{RepoPath: source, Consent: true, Spec: spec})
	if e != nil || inspection.Status != "ready" {
		err = errors.New("repository_inspection_not_ready")
		return
	}
	profile, ok := controller.CodingVMProfile()
	if c.Profile != "" {
		profile = domain.C4Profile{SchemaVersion: 2, ID: c.Profile, Mode: "embedded_vm"}
		ok = inspection.VerificationV2 != nil
		summary.V2.Inputs = inspection.VerificationV2
	}
	if !ok {
		err = errors.New("trusted_vm_profile_missing")
		return
	}
	summary.Stage = "create"
	m, e := controller.Create(ctx, engine.CreateRequest{ImportVersion: 1, ImportSelection: &domain.ImportSelection{InspectionID: inspection.ID, Digest: inspection.Digest}, CodingVersion: 1, WorkspaceMode: "generations_v1", Title: "SYNTHETIC model / native VM acceptance", Goal: "Implement Triple using addition only, preserving positive and negative behavior. Do not modify protected tests or module metadata.", RepoPath: inspection.Root, Spec: domain.Spec{Scope: inspection.Spec.Scope, Protected: inspection.Spec.Protected, Acceptance: inspection.Spec.Acceptance, Image: inspection.Spec.Image, ProfileID: "native-fixture-profile", Research: true, MaxCalls: 6, C4: &profile}})
	if e != nil {
		err = e
		return
	}
	summary.MissionID = m.ID
	initialSnapshot, _ := json.Marshal(m.ModelSnapshot)
	initialPolicy, _ := json.Marshal(m.ModelPolicySnapshot)
	// Every denial is checked for unchanged durable state and no model or VM
	// effects, not merely a non-nil error from a possibly mutating action.
	deny := func(action func() error) error {
		before, e := s.GetMission(m.ID)
		if e != nil {
			return e
		}
		entries, e := os.ReadDir(filepath.Join(c.DataDir, "instances"))
		if e != nil {
			return e
		}
		fixture.mu.Lock()
		calls := len(fixture.calls)
		fixture.mu.Unlock()
		reads := lease.reads.Load()
		if action() == nil {
			return errors.New("approval_gate_accepted")
		}
		after, e := s.GetMission(m.ID)
		if e != nil || !reflect.DeepEqual(before, after) {
			return errors.New("approval_denial_mutated_mission")
		}
		afterEntries, e := os.ReadDir(filepath.Join(c.DataDir, "instances"))
		if e != nil || len(entries) != len(afterEntries) {
			return errors.New("approval_denial_created_vm")
		}
		fixture.mu.Lock()
		same := calls == len(fixture.calls)
		fixture.mu.Unlock()
		if !same || reads != lease.reads.Load() {
			return errors.New("approval_denial_called_model")
		}
		return nil
	}
	if err = deny(func() error { _, e := controller.PlanControlled(ctx, m.ID, m.Revision, true); return e }); err != nil {
		return
	}
	if err = deny(func() error { _, e := controller.PrepareC4Baseline(ctx, m.ID, m.Revision); return e }); err != nil {
		return
	}
	if err = deny(func() error { _, e := controller.Deliver(ctx, m.ID, m.Revision); return e }); err != nil {
		return
	}
	summary.Stage = "readiness"
	m, err = controller.PrepareC4Readiness(ctx, m.ID, m.Revision)
	if err != nil {
		return
	}
	control := m.C4Controls[len(m.C4Controls)-1]
	if err = deny(func() error {
		_, e := controller.ExecuteC4Readiness(ctx, m.ID, m.Revision, control.ID, control.MaterialDigest)
		return e
	}); err != nil {
		return
	}
	m, err = controller.ApproveC4Control(ctx, m.ID, m.Revision, control.ID, control.MaterialDigest)
	if err != nil {
		return
	}
	m, err = controller.ExecuteC4Readiness(ctx, m.ID, m.Revision, control.ID, control.MaterialDigest)
	if err != nil {
		return
	}
	control = m.C4Controls[len(m.C4Controls)-1]
	if control.SourceKind != summary.Source || control.Result == nil || control.Result.Status != "ready" {
		err = errors.New("native_readiness_not_ready")
		return
	}
	summary.Stage = "baseline"
	m, err = controller.PrepareC4Baseline(ctx, m.ID, m.Revision)
	if err != nil {
		return
	}
	op := m.C4Operations[len(m.C4Operations)-1]
	if err = deny(func() error {
		_, e := controller.ExecuteC4Baseline(ctx, m.ID, m.Revision, op.ID, op.MaterialDigest)
		return e
	}); err != nil {
		return
	}
	m, err = controller.ApproveC4Baseline(ctx, m.ID, m.Revision, op.ID, op.MaterialDigest)
	if err != nil {
		return
	}
	m, err = controller.ExecuteC4Baseline(ctx, m.ID, m.Revision, op.ID, op.MaterialDigest)
	if err != nil {
		return
	}
	if c.Profile != "" && f.expectedClass != "" {
		err = finishV2Baseline(ctx, &summary, m, vm, f, source, filepath.Join(c.DataDir, "engine", "missions", m.ID, "base"), filepath.Join(c.DataDir, "engine", "dependency-bundles"), fixture, lease)
		return
	}
	if len(m.C4Operations) != 1 || !m.C4Operations[0].Passed() || m.C4Operations[0].Result.Tests != f.expectedTests {
		err = errors.New("native_baseline_not_two_passes")
		return
	}
	summary.Stage = "plan"
	if err = deny(func() error { _, e := controller.Plan(ctx, m.ID, m.Revision); return e }); err != nil {
		return
	}
	if _, err = controller.PlanControlled(ctx, m.ID, m.Revision, true); err != nil {
		return
	}
	if err = controller.WaitIdle(ctx); err != nil {
		return
	}
	m, err = s.GetMission(m.ID)
	if err != nil {
		return
	}
	if m.State != "awaiting_approval" {
		err = errors.New("planning_not_awaiting_approval")
		return
	}
	if err = deny(func() error { _, e := controller.Deliver(ctx, m.ID, m.Revision); return e }); err != nil {
		return
	}
	summary.Stage = "execute"
	if _, err = controller.Approve(ctx, m.ID, m.Revision); err != nil {
		return
	}
	if err = controller.WaitIdle(ctx); err != nil {
		return
	}
	m, err = s.GetMission(m.ID)
	if err != nil {
		return
	}
	if m.State != "awaiting_delivery" {
		err = errors.New("execution_not_awaiting_delivery")
		return
	}
	summary.Checks.ApprovalGates = true
	summary.Stage = "verify"
	if c.Profile == "" {
		summary.Operations, err = operationEvidence(m.C4Operations)
	} else {
		summary.Operations, err = v2Operations(m, vm, *summary.V2.Inputs, f)
	}
	if err != nil {
		summary.Operations = []OperationEvidence{}
		return
	}
	if err = domain.ValidateC4Mission(m); err != nil {
		return
	}
	if err = verificationEvidence(m); err != nil {
		return
	}
	currentSnapshot, _ := json.Marshal(m.ModelSnapshot)
	currentPolicy, _ := json.Marshal(m.ModelPolicySnapshot)
	if string(initialSnapshot) != string(currentSnapshot) || string(initialPolicy) != string(currentPolicy) {
		err = errors.New("frozen_model_snapshot_changed")
		return
	}
	for _, o := range m.C4Operations {
		session, ok := vm.Lookup(o.ContainerID)
		digest := o.CandidateDigest
		if o.Kind == "baseline" {
			digest = o.BaseDigest
		}
		matches := ok && !session.Running()
		if c.Profile == "" {
			matches = matches && session.Matches(vmruntime.Request{OperationID: o.ID, MissionID: m.ID, Owner: o.Owner, Name: o.ContainerName, CandidateDigest: digest})
		} else if matches {
			_, matches = session.AuthenticatedEvidenceV2(vmruntime.RequestV2{Owner: vmruntime.Request{OperationID: o.ID, MissionID: m.ID, Owner: o.Owner, Name: o.ContainerName, CandidateDigest: digest}, Profile: *m.Spec.C4, Inputs: *summary.V2.Inputs})
		}
		if !matches {
			err = errors.New("native_session_not_owned_closed")
			return
		}
		generationTree := filepath.Join(c.DataDir, "engine", "missions", m.ID, "base")
		if o.Kind == "candidate" {
			if o.CandidateGeneration == nil {
				err = errors.New("candidate_generation_missing")
				return
			}
			generationTree, err = workspace.GenerationPath(filepath.Join(c.DataDir, "engine", "missions", m.ID), *o.CandidateGeneration)
			if err != nil {
				return
			}
		}
		actualProtected, e := protected(generationTree)
		if e != nil || !reflect.DeepEqual(actualProtected, summary.ProtectedDigests) {
			err = errors.New("protected_history_changed")
			return
		}
	}
	summary.Checks.ExactInstances = true
	if m.CandidateGeneration == nil || len(m.Changes) != 2 || m.Changes[0].Before != f.initial || m.Changes[0].After != f.worker || m.Changes[1].After != f.repaired {
		err = errors.New("repair_trace_not_preserved")
		return
	}
	tree, e := workspace.GenerationPath(filepath.Join(c.DataDir, "engine", "missions", m.ID), *m.CandidateGeneration)
	if e != nil {
		err = e
		return
	}
	candidate, e := os.ReadFile(filepath.Join(tree, filepath.FromSlash(f.path)))
	if e != nil || string(candidate) != f.repaired {
		err = errors.New("candidate_not_addition_only")
		return
	}
	actual, e := protected(tree)
	if e != nil || !reflect.DeepEqual(actual, summary.ProtectedDigests) {
		err = errors.New("protected_candidate_changed")
		return
	}
	summary.Checks.ProtectedUnchanged = true
	after, e := sourceTreeDigest(source)
	if e != nil || after != summary.SourceDigest {
		err = errors.New("source_tree_changed")
		return
	}
	summary.Checks.SourceUnchanged = true
	if c.Profile != "" {
		if _, err = dependencybundle.DescribeVerified(ctx, filepath.Join(c.DataDir, "engine", "dependency-bundles"), dependencybundle.Binding(summary.V2.Inputs.Dependencies)); err != nil {
			return
		}
	}
	if len(m.Reviews) != 2 {
		err = errors.New("review_count_mismatch")
		return
	}
	reviewRoles := map[domain.Role]bool{}
	for _, review := range m.Reviews {
		if !review.Passed || domain.ValidateCodingReview(m, review) != nil || reviewRoles[review.Role] {
			err = errors.New("review_not_bound")
			return
		}
		reviewRoles[review.Role] = true
	}
	if !reviewRoles[domain.QA] || !reviewRoles[domain.Auditor] {
		err = errors.New("review_roles_missing")
		return
	}
	summary.Checks.QAAuditorBound = true
	if err = verifyLocalModels(s, m, fixture, lease); err != nil {
		return
	}
	summary.Checks.LocalModelOnly = true
	summary.Stage = "deliver"
	if _, err = controller.Deliver(ctx, m.ID, m.Revision); err != nil {
		return
	}
	report, e := controller.ReadDelivery(m.ID, "report")
	if e != nil {
		err = e
		return
	}
	patch, e := controller.ReadDelivery(m.ID, "patch")
	if e != nil {
		err = e
		return
	}
	patchMarker := "n + n + n"
	if c.Profile != "" {
		patchMarker = f.deliveryPatchMarker()
	}
	if len(patch) == 0 || !strings.Contains(string(patch), patchMarker) || !strings.Contains(string(report), "synthetic-local-6") || !strings.Contains(string(report), fixtureModel) || !strings.Contains(string(report), "simulation") {
		err = errors.New("delivery_provenance_missing")
		return
	}
	m, err = s.GetMission(m.ID)
	if err != nil {
		return
	}
	if err = deny(func() error { _, e := controller.PrepareC4Readiness(ctx, m.ID, m.Revision); return e }); err != nil {
		return
	}
	if err = deny(func() error { _, e := controller.Chat(ctx, m.ID, m.Revision, "explain delivered patch"); return e }); err != nil {
		return
	}
	againReport, e := controller.ReadDelivery(m.ID, "report")
	if e != nil {
		err = e
		return
	}
	againPatch, e := controller.ReadDelivery(m.ID, "patch")
	if e != nil || !reflect.DeepEqual(report, againReport) || !reflect.DeepEqual(patch, againPatch) {
		err = errors.New("delivery_changed")
		return
	}
	summary.PatchDigest = digestBytes(patch)
	summary.ReportDigest = digestBytes(report)
	summary.Checks.DeliveryFrozen = true
	summary.Stage = "complete"
	summary.Passed = true
	return
}

func registerFixture(s *store.Store, base string) error {
	a, e := s.SaveModelAccount(domain.ModelAccount{ID: fixtureAccount, Name: "SYNTHETIC LOCAL FIXTURE — NOT A PROVIDER", Provider: "openai", AuthMethod: "api_key", BillingMode: "api_credits", BaseURL: base, Enabled: true}, 0)
	if e != nil {
		return e
	}
	if _, e = s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: "synthetic-local-key", SecretRef: "foundry-model-account-SYNTHETIC-local-key"}, a.Revision); e != nil {
		return e
	}
	if _, e = s.FinishModelCredential("synthetic-local-key", "available"); e != nil {
		return e
	}
	route, e := s.CreateModelRoute(domain.ModelRoute{ID: "native-fixture-route", AccountID: a.ID, Adapter: "openai_api_responses", Protocol: "responses", BaseURL: base, Environment: "simulation", SpendClass: "explicit_api", ExtraUsage: "off"})
	if e != nil {
		return e
	}
	return s.PutProfile(domain.Profile{ID: "native-fixture-profile", Name: "SYNTHETIC local protocol fixture", Kind: "openai", AccountID: a.ID, RouteID: route.ID, BaseURL: base, Protocol: "responses", Model: fixtureModel})
}
func verifyLocalModels(s *store.Store, m domain.Mission, f *localFixture, l *fixtureLease) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failed || f.pending != nil || len(f.calls) != 6 || l.reads.Load() != 6 || domain.ValidateMissionModelSnapshot(m) != nil {
		return errors.New("local_fixture_effect_count_mismatch")
	}
	for _, selection := range m.ModelSnapshot.Roles {
		if selection.Account == nil || selection.Route == nil || selection.Account.ID != fixtureAccount || selection.Route.Environment != "simulation" || selection.Route.BaseURL != l.base || selection.Profile.Model != fixtureModel {
			return errors.New("model_snapshot_not_local")
		}
	}
	invocations, e := s.ListInvocations(m.ID, 0, 100)
	if e != nil {
		return e
	}
	if len(invocations) != 6 {
		return errors.New("model_invocation_count_mismatch")
	}
	seen := map[domain.Role]bool{}
	for _, v := range invocations {
		index := -1
		for i, q := range f.calls {
			if q.InvocationID == v.ID && q.Role == v.Role {
				index = i
				break
			}
		}
		if index < 0 || seen[v.Role] || v.Status != "succeeded" || v.Input.Digest == "" || v.ModelReceipt == nil || v.ModelReceipt.RequestID != fmt.Sprintf("synthetic-local-%d", index+1) || v.ModelReceipt.ResolvedModel != fixtureModel {
			return errors.New("model_receipt_not_correlated")
		}
		seen[v.Role] = true
	}
	return nil
}
