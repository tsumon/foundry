package vmacceptance

import (
	"encoding/json"
	"foundry/internal/domain"
	"strings"
	"testing"
)

func fixtureTestOutput(failed bool) string {
	out := ""
	for _, name := range []string{"TestTriplePositive", "TestTripleNegative"} {
		action := "pass"
		if failed && name == "TestTripleNegative" {
			action = "fail"
		}
		b, _ := json.Marshal(map[string]string{"Action": action, "Test": name, "Package": "foundry-native-acceptance"})
		out += string(b) + "\n"
	}
	return out
}

func operationFixture() []domain.C4Operation {
	result := make([]domain.C4Operation, 3)
	for i, class := range []string{"passed", "test_failed", "passed"} {
		exit := 0
		if class == "test_failed" {
			exit = 1
		}
		kind := "candidate"
		if i == 0 {
			kind = "baseline"
		}
		id := strings.Repeat(string(rune('a'+i)), 64)
		result[i] = domain.C4Operation{SchemaVersion: 1, ID: "operation-" + id[:1], Kind: kind, State: "finished", ContainerID: id, Owner: "owned", Approval: &domain.C4Approval{}, Dispatch: &domain.C4Fact{}, Created: &domain.C4Fact{}, Started: &domain.C4Fact{}, CandidateDigest: id, BaseDigest: strings.Repeat("d", 64), Result: &domain.C4Result{Class: class, ExitCode: &exit, Tests: 2}, Cleanup: []domain.C4Cleanup{{State: "confirmed", ContainerID: id, Owner: "owned"}}}
		result[i].Result.Output = fixtureTestOutput(class == "test_failed")
	}
	return result
}

func TestOperationSummaryRequiresExactFailureRepairAndOwnedCleanup(t *testing.T) {
	ops := operationFixture()
	got, err := operationEvidence(ops)
	if err != nil || len(got) != 3 || got[0].Class != "passed" || got[1].Class != "test_failed" || got[2].Class != "passed" || got[1].Tests != 2 {
		t.Fatal(got, err)
	}
	for _, kind := range []string{"zero", "partial", "unknown", "unclean", "same-instance", "same-candidate", "missing-start", "wrong-exit", "extra-operation", "wrong-test-failure", "missing-test-events"} {
		t.Run(kind, func(t *testing.T) {
			ops := operationFixture()
			switch kind {
			case "wrong-test-failure":
				ops[1].Result.Output = fixtureTestOutput(false)
			case "missing-test-events":
				ops[2].Result.Output = ""
			case "zero":
				ops[2].Result.Tests = 0
			case "partial":
				ops[2].Result.Truncated = true
			case "unknown":
				ops[1].Result.Class = "outcome_unknown"
			case "unclean":
				ops[2].Cleanup[0].State = "failed"
			case "same-instance":
				ops[2].ContainerID = ops[0].ContainerID
			case "same-candidate":
				ops[2].CandidateDigest = ops[1].CandidateDigest
			case "missing-start":
				ops[2].Started = nil
			case "wrong-exit":
				*ops[1].Result.ExitCode = 0
			case "extra-operation":
				ops = append(ops, ops[0])
			}
			if _, err := operationEvidence(ops); err == nil {
				t.Fatal("invalid native sequence qualified", kind)
			}
		})
	}
}

func TestVerificationEvidenceMustRemainIsolatedAndCorrelated(t *testing.T) {
	ops := operationFixture()
	m := domain.Mission{C4Operations: ops, Spec: domain.Spec{ImageID: "embedded-vm:sha256:" + strings.Repeat("e", 64)}}
	for _, o := range ops[1:] {
		m.Evidence = append(m.Evidence, domain.Verification{ID: o.ID, CandidateDigest: o.CandidateDigest, ImageID: m.Spec.ImageID, SourceVersion: 1, SourceKind: "isolated_execution", Passed: o.Passed()})
	}
	if err := verificationEvidence(m); err != nil {
		t.Fatal(err)
	}
	m.Evidence[1].SourceKind = "synthetic_stub"
	if verificationEvidence(m) == nil {
		t.Fatal("synthetic verification qualified")
	}
	m.Evidence[1].SourceKind = "isolated_execution"
	m.Evidence[1].ID = m.Evidence[0].ID
	if verificationEvidence(m) == nil {
		t.Fatal("wrong operation qualified")
	}
}
