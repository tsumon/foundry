package vmacceptance

import (
	"context"
	"errors"
	"foundry/internal/dependencybundle"
	"foundry/internal/domain"
	"foundry/internal/vmruntime"
	"reflect"
)

type EvidenceV2 struct {
	ProfileID              string                    `json:"profile_id"`
	Case                   string                    `json:"case"`
	Provenance             string                    `json:"provenance"`
	RuntimeInventoryDigest string                    `json:"runtime_inventory_digest"`
	AdmissionDigest        string                    `json:"admission_digest"`
	Inputs                 *domain.C4InputContractV2 `json:"inputs"`
	DependencyFiles        []dependencybundle.File   `json:"dependency_files"`
	ExpectedFiles          map[string]string         `json:"expected_files"`
}

func v2OperationRecord(o domain.C4Operation, n vmruntime.AuthenticatedEvidenceV2, mission string) (OperationEvidence, error) {
	fail := errors.New("native_v2_evidence_incomplete")
	digest := o.CandidateDigest
	if o.Kind == "baseline" {
		digest = o.BaseDigest
	}
	if o.SchemaVersion != 1 || o.State != "finished" || o.Approval == nil || o.Dispatch == nil || o.Created == nil || o.Started == nil || o.Result == nil || o.Result.ExitCode == nil || !sha256Hex.MatchString(o.ContainerID) || !sha256Hex.MatchString(digest) || o.CleanupState() != "confirmed" || len(o.Cleanup) != 1 {
		return OperationEvidence{}, fail
	}
	cleanup := o.Cleanup[0]
	if cleanup.ContainerID != o.ContainerID || cleanup.Owner != o.Owner || n.SessionID != o.ContainerID || n.OperationID != o.ID || n.MissionID != mission || n.Owner != o.Owner || n.Name != o.ContainerName || !sha256Hex.MatchString(n.RequestDigest) || n.Class != o.Result.Class || n.Tests != o.Result.Tests || n.ExitCode != *o.Result.ExitCode || n.Truncated != o.Result.Truncated {
		return OperationEvidence{}, fail
	}
	return OperationEvidence{Kind: o.Kind, Class: o.Result.Class, Tests: o.Result.Tests, InstanceID: o.ContainerID, CleanupConfirmed: true, CandidateDigest: digest, NativeV2: &n}, nil
}
func v2OperationEvidence(o domain.C4Operation, n vmruntime.AuthenticatedEvidenceV2, f fixtureSpec, mission string) (OperationEvidence, error) {
	record, e := v2OperationRecord(o, n, mission)
	if e != nil {
		return record, e
	}
	fail := errors.New("native_v2_evidence_incomplete")
	if n.Target.ProfileID != f.profile || !n.Checks.Valid() || n.Truncated {
		return OperationEvidence{}, fail
	}
	phases := []string{"test-node"}
	if f.profile == "c4_python_model_v2" {
		phases = []string{"test-python"}
	}
	if f.profile == "c4_typescript_model_v2" {
		phases = []string{"compile-typescript", "test-node"}
		if f.caseID == "typecheck" {
			phases = phases[:1]
		}
	}
	if len(n.Phases) != len(phases) {
		return OperationEvidence{}, fail
	}
	for i, p := range n.Phases {
		if p.Phase != phases[i] || !p.Exited || p.Canceled || !p.CleanupConfirmed {
			return OperationEvidence{}, fail
		}
		if p.Phase == "compile-typescript" && f.caseID != "typecheck" && (!sha256Hex.MatchString(p.CompiledOutputDigest) || p.ExitCode != 0 || !p.ResourceLimitsOK || p.DeadlineExceeded) {
			return OperationEvidence{}, fail
		}
	}
	last := n.Phases[len(n.Phases)-1]
	if f.expectedClass != "" {
		if n.Class != f.expectedClass || n.Tests != f.expectedTests {
			return OperationEvidence{}, fail
		}
		pressure := f.caseID == "memory" || f.caseID == "pids"
		if last.ResourceLimitsOK == pressure || last.DeadlineExceeded != (f.caseID == "timeout") {
			return OperationEvidence{}, fail
		}
		switch f.caseID {
		case "memory", "signal", "timeout":
			if last.ExitCode != -1 {
				return OperationEvidence{}, fail
			}
		case "pids", "setup", "discovery":
			if last.ExitCode != 1 {
				return OperationEvidence{}, fail
			}
		case "held_stream", "descendant":
			if last.ExitCode != 0 {
				return OperationEvidence{}, fail
			}
		case "typecheck":
			if last.ExitCode != 2 || last.CompiledOutputDigest != "" {
				return OperationEvidence{}, fail
			}
		}
	} else if n.Tests != f.expectedTests || (n.Class != "passed" && n.Class != "test_failed") || !last.ResourceLimitsOK || last.DeadlineExceeded {
		return OperationEvidence{}, fail
	}
	return record, nil
}

func v2Operations(m domain.Mission, vm *vmruntime.Runtime, inputs domain.C4InputContractV2, f fixtureSpec) ([]OperationEvidence, error) {
	expected := []string{"passed", "test_failed", "passed"}
	if f.expectedClass != "" {
		expected = []string{f.expectedClass}
	}
	if m.Spec.C4 == nil || len(m.C4Operations) != len(expected) {
		return nil, errors.New("native_v2_operation_count")
	}
	out := []OperationEvidence{}
	seen := map[string]bool{}
	for i, o := range m.C4Operations {
		kind := "candidate"
		if i == 0 {
			kind = "baseline"
		}
		if o.Kind != kind || o.Result == nil || o.Result.Class != expected[i] || seen[o.ContainerID] {
			return nil, errors.New("native_v2_sequence_invalid")
		}
		n, ok := ownedV2Facts(m, o, vm, inputs)
		if !ok {
			return nil, errors.New("native_v2_session_not_closed_owned")
		}
		ev, e := v2OperationEvidence(o, n, f, m.ID)
		if e != nil {
			return nil, e
		}
		out = append(out, ev)
		seen[o.ContainerID] = true
	}
	if len(out) == 3 && out[1].CandidateDigest == out[2].CandidateDigest {
		return nil, errors.New("repair_generation_not_changed")
	}
	return out, nil
}

func ownedV2Facts(m domain.Mission, o domain.C4Operation, vm *vmruntime.Runtime, inputs domain.C4InputContractV2) (vmruntime.AuthenticatedEvidenceV2, bool) {
	if m.Spec.C4 == nil || m.Spec.C4.EmbeddedVM == nil || m.Spec.C4.V2 == nil {
		return vmruntime.AuthenticatedEvidenceV2{}, false
	}
	digest := o.CandidateDigest
	if o.Kind == "baseline" {
		digest = o.BaseDigest
	}
	session, ok := vm.Lookup(o.ContainerID)
	if !ok {
		return vmruntime.AuthenticatedEvidenceV2{}, false
	}
	n, ok := session.AuthenticatedEvidenceV2(vmruntime.RequestV2{Owner: vmruntime.Request{OperationID: o.ID, MissionID: m.ID, Owner: o.Owner, Name: o.ContainerName, CandidateDigest: digest}, Profile: *m.Spec.C4, Inputs: inputs})
	return n, ok && n.Target.Identity == *m.Spec.C4.EmbeddedVM && n.Target.RuntimeInventoryDigest == m.Spec.C4.V2.RuntimeInventoryDigest
}

// Retain actual closed native facts on an unexpected failed attempt, without
// treating a fixture expectation as an observed class/count or reading receipts
// back as live authority. Missing authenticated evidence stays missing.
func v2FailureOperations(m domain.Mission, vm *vmruntime.Runtime, inputs domain.C4InputContractV2) []OperationEvidence {
	out := []OperationEvidence{}
	for _, o := range m.C4Operations {
		n, ok := ownedV2Facts(m, o, vm, inputs)
		if !ok {
			continue
		}
		record, e := v2OperationRecord(o, n, m.ID)
		if e == nil {
			out = append(out, record)
		}
	}
	return out
}

func finishV2Baseline(ctx context.Context, summary *Summary, m domain.Mission, vm *vmruntime.Runtime, f fixtureSpec, source, base, dependencyRoot string, fixture *localFixture, lease *fixtureLease) error {
	ops, e := v2Operations(m, vm, *summary.V2.Inputs, f)
	if e != nil {
		return e
	}
	if domain.ValidateC4Mission(m) != nil || m.Budget.UsedCalls != 0 || len(m.Evidence) != 0 || len(m.Changes) != 0 {
		return errors.New("native_v2_unexpected_repair")
	}
	fixture.mu.Lock()
	calls := len(fixture.calls)
	fixture.mu.Unlock()
	if calls != 0 || lease.reads.Load() != 0 {
		return errors.New("native_v2_unexpected_model_call")
	}
	actual, e := f.protectedDigests(base)
	if e != nil || !reflect.DeepEqual(actual, summary.ProtectedDigests) {
		return errors.New("protected_baseline_changed")
	}
	digest, e := sourceTreeDigest(source)
	if e != nil || digest != summary.SourceDigest {
		return errors.New("source_tree_changed")
	}
	if _, e = dependencybundle.DescribeVerified(ctx, dependencyRoot, dependencybundle.Binding(summary.V2.Inputs.Dependencies)); e != nil {
		return e
	}
	summary.Operations = ops
	summary.Checks.ApprovalGates = true
	summary.Checks.ProtectedUnchanged = true
	summary.Checks.SourceUnchanged = true
	summary.Checks.ExactInstances = true
	summary.Checks.LocalModelOnly = true
	summary.Stage = "complete"
	summary.Passed = true
	return nil
}
