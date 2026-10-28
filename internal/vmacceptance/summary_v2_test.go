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
