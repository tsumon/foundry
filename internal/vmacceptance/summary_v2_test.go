package vmacceptance

import (
	"foundry/internal/domain"
	"foundry/internal/vmruntime"
	"strings"
	"testing"
)

func TestV2ReceiptRequiresGenuineAuthenticatedOperationAndPressure(t *testing.T) {
	f, _ := selectFixture("c4_python_model_v2", "workflow")
	o := operationFixture()[0]
	o.ContainerName = "owned-name"
	o.Result.Tests = 4
	o.Result.Output = "framework diagnostics, not case authority"
	n := vmruntime.AuthenticatedEvidenceV2{SessionID: o.ContainerID, OperationID: o.ID, MissionID: "mission", Owner: o.Owner, Name: o.ContainerName, Target: vmruntime.ProbeTargetV2{ProfileID: f.profile}, RequestDigest: strings.Repeat("a", 64), Checks: vmruntime.SelfCheckV2{ReadOnlyWorkspace: true, ReadOnlyToolchain: true, NoNetwork: true, PrivateControl: true, Unprivileged: true, NoNewPrivileges: true, CgroupLimits: true, TmpfsLimits: true, ReadOnlyDependencies: true, RuntimeInventoryMatches: true, InputBindingValid: true, PhaseCleanupConfirmed: true}, Phases: []vmruntime.PhaseEvidenceV2{{Phase: "test-python", Exited: true, ResourceLimitsOK: true, CleanupConfirmed: true}}, Class: "passed", Tests: 4}
	if _, e := v2OperationEvidence(o, n, f, "mission"); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*domain.C4Operation, *vmruntime.AuthenticatedEvidenceV2){func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { n.Phases = nil }, func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { n.Tests = 1 }, func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { n.Owner = "foreign" }, func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { o.Cleanup = nil }, func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { n.Checks.PrivateControl = false }, func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { n.Phases[0].Exited = false }} {
		bad := n
		bad.Phases = append([]vmruntime.PhaseEvidenceV2{}, n.Phases...)
		op := o
		change(&op, &bad)
		if _, e := v2OperationEvidence(op, bad, f, "mission"); e == nil {
			t.Fatal("incomplete native evidence accepted")
		}
	}
	pressure, _ := selectFixture(f.profile, "memory")
	o.Result.Class = "infrastructure_failed"
	o.Result.Tests = 0
	n.Class = "infrastructure_failed"
	n.Tests = 0
	if _, e := v2OperationEvidence(o, n, pressure, "mission"); e == nil {
		t.Fatal("launch/test error pretended to prove memory limit")
	}
	n.Phases[0].ResourceLimitsOK = false
	if _, e := v2OperationEvidence(o, n, pressure, "mission"); e == nil {
		t.Fatal("memory case accepted without actual signaled exit")
	}
	n.ExitCode = -1
	n.Phases[0].ExitCode = -1
	*o.Result.ExitCode = -1
	if _, e := v2OperationEvidence(o, n, pressure, "mission"); e != nil {
		t.Fatal(e)
	}
	// A failed attempt still retains observed facts, even when its class/counts
	// contradict the selected fixture expectation. It cannot become a pass.
	n.Phases[0].ResourceLimitsOK = true
	if record, e := v2OperationRecord(o, n, "mission"); e != nil || record.NativeV2 == nil || record.Class != "infrastructure_failed" || record.Tests != 0 {
		t.Fatal("lost actual failed attempt", e)
	}
}

func TestV2ExpectedBaselineCasesKeepExactNativeQualification(t *testing.T) {
	for _, tc := range []struct {
		caseID, class string
		exit, tests   int
		resources     bool
		deadline      bool
	}{
		{"typecheck", "infrastructure_failed", 2, 0, true, false},
		{"setup", "infrastructure_failed", 1, 0, true, false},
		{"discovery", "infrastructure_failed", 1, 0, true, false},
		{"memory", "infrastructure_failed", -1, 0, false, false},
		{"pids", "infrastructure_failed", 1, 0, false, false},
		{"timeout", "timeout", -1, 0, true, true},
		{"signal", "infrastructure_failed", -1, 0, true, false},
		{"held_stream", "infrastructure_failed", 0, 0, true, false},
		{"descendant", "passed", 0, 1, true, false},
	} {
		t.Run(tc.caseID, func(t *testing.T) {
			profile, phase := "c4_python_model_v2", "test-python"
			if tc.caseID == "typecheck" {
				profile, phase = "c4_typescript_model_v2", "compile-typescript"
			}
			f, e := selectFixture(profile, tc.caseID)
			if e != nil {
				t.Fatal(e)
			}
			o := operationFixture()[0]
			o.ContainerName = "owned-name"
			o.Result.Class, o.Result.Tests, *o.Result.ExitCode = tc.class, tc.tests, tc.exit
			// Typed facts exercise only the existing evidence validator, never
			// Runtime/Run authority or a synthetic native-success execution path.
			n := vmruntime.AuthenticatedEvidenceV2{SessionID: o.ContainerID, OperationID: o.ID, MissionID: "mission", Owner: o.Owner, Name: o.ContainerName, Target: vmruntime.ProbeTargetV2{ProfileID: profile}, RequestDigest: strings.Repeat("a", 64), Checks: vmruntime.SelfCheckV2{ReadOnlyWorkspace: true, ReadOnlyToolchain: true, NoNetwork: true, PrivateControl: true, Unprivileged: true, NoNewPrivileges: true, CgroupLimits: true, TmpfsLimits: true, ReadOnlyDependencies: true, RuntimeInventoryMatches: true, InputBindingValid: true, PhaseCleanupConfirmed: true}, Phases: []vmruntime.PhaseEvidenceV2{{Phase: phase, Exited: true, ExitCode: tc.exit, ResourceLimitsOK: tc.resources, DeadlineExceeded: tc.deadline, CleanupConfirmed: true}}, Class: tc.class, Tests: tc.tests, ExitCode: tc.exit}
			if _, e = v2OperationEvidence(o, n, f, "mission"); e != nil {
				t.Fatal("exact case facts rejected", e)
			}
			for _, mutate := range []func(*domain.C4Operation, *vmruntime.AuthenticatedEvidenceV2){
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) {
					o.Result.Class, n.Class = "test_failed", "test_failed"
				},
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { o.Result.Tests++; n.Tests++ },
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) {
					*o.Result.ExitCode++
					n.ExitCode++
					n.Phases[0].ExitCode++
				},
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) {
					n.Phases[0].ResourceLimitsOK = !n.Phases[0].ResourceLimitsOK
				},
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) {
					n.Phases[0].DeadlineExceeded = !n.Phases[0].DeadlineExceeded
				},
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) {
					n.Phases[0].CleanupConfirmed = false
				},
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { n.Phases[0].Canceled = true },
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { n.Phases[0].Exited = false },
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) {
					n.Phases[0].Phase = "unrelated-phase"
				},
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { n.Owner = "foreign-owner" },
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) {
					n.Checks.PhaseCleanupConfirmed = false
				},
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { n.Checks.InputBindingValid = false },
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) {
					o.Result.Truncated, n.Truncated = true, true
				},
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { o.Started = nil },
				func(o *domain.C4Operation, n *vmruntime.AuthenticatedEvidenceV2) { o.Cleanup = nil },
			} {
				badOp, bad := o, n
				result, exit := *o.Result, *o.Result.ExitCode
				badOp.Result, result.ExitCode = &result, &exit
				bad.Phases = append([]vmruntime.PhaseEvidenceV2{}, n.Phases...)
				mutate(&badOp, &bad)
				if _, e = v2OperationEvidence(badOp, bad, f, "mission"); e == nil {
					t.Fatal("unrelated failure or incomplete authority accepted", tc.caseID)
				}
			}
		})
	}
}
