package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDomainJSONUsesSnakeCase(t *testing.T) {
	b, err := json.Marshal(Mission{
		ID:              "mission-1",
		BaseCommit:      "abc",
		CandidateDigest: "digest",
		ContextManifest: []FileMeta{{Path: "main.go", SHA256: "hash", Bytes: 7}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, field := range []string{`"base_commit"`, `"candidate_digest"`, `"context_manifest"`, `"sha256"`} {
		if !strings.Contains(got, field) {
			t.Fatalf("JSON %s does not contain %s", got, field)
		}
	}
	for _, field := range []string{`"BaseCommit"`, `"CandidateDigest"`, `"ContextManifest"`} {
		if strings.Contains(got, field) {
			t.Fatalf("JSON %s contains Go field %s", got, field)
		}
	}
}

func TestValidateSpecRejectsEmptyAcceptance(t *testing.T) {
	spec := validSpec()
	spec.Acceptance = nil
	if err := ValidateSpec(spec); err == nil {
		t.Fatal("empty acceptance was accepted")
	}
}

func TestValidateSpecRejectsCallBudgetAboveV1Limit(t *testing.T) {
	spec := validSpec()
	spec.MaxCalls = 17
	if err := ValidateSpec(spec); err == nil {
		t.Fatal("call budget above V1 limit was accepted")
	}
}

func TestValidatePlanRejectsCycle(t *testing.T) {
	plan := Plan{Revision: 1, Steps: []Step{
		{ID: "a", Title: "A", DependsOn: []string{"b"}, Files: []string{"src/a.ts"}},
		{ID: "b", Title: "B", DependsOn: []string{"a"}, Files: []string{"src/b.ts"}},
	}}
	if err := ValidatePlan(plan, validSpec()); err == nil {
		t.Fatal("cyclic plan was accepted")
	}
}

func TestValidatePlanRejectsUnknownDependency(t *testing.T) {
	plan := Plan{Revision: 1, Steps: []Step{
		{ID: "a", Title: "A", DependsOn: []string{"missing"}, Files: []string{"src/a.ts"}},
	}}
	if err := ValidatePlan(plan, validSpec()); err == nil {
		t.Fatal("unknown dependency was accepted")
	}
}

func TestValidatePlanRejectsOutOfScopeFile(t *testing.T) {
	plan := Plan{Revision: 1, Steps: []Step{
		{ID: "a", Title: "A", Files: []string{"tests/acceptance.test.ts"}},
	}}
	if err := ValidatePlan(plan, validSpec()); err == nil {
		t.Fatal("out-of-scope file was accepted")
	}
}

func TestCanDeliverRejectsStaleEvidence(t *testing.T) {
	m := deliverableMission()
	if err := CanDeliver(m, "changed"); err == nil {
		t.Fatal("changed candidate was accepted")
	}
}

func TestCanDeliverRequiresLatestPassingEvidence(t *testing.T) {
	m := deliverableMission()
	m.Evidence = append(m.Evidence, Verification{
		ID: "evidence-2", CandidateDigest: "digest", ImageID: "image-id",
		AcceptanceRevision: 3, Passed: false,
	})
	if err := CanDeliver(m, "digest"); err == nil {
		t.Fatal("failed latest evidence was accepted")
	}
}

func TestCanDeliverRejectsEmptyEvidence(t *testing.T) {
	m := deliverableMission()
	m.Evidence = nil
	if err := CanDeliver(m, "digest"); err == nil {
		t.Fatal("empty evidence was accepted")
	}
}

func TestCanDeliverRejectsEvidenceForOldPlanRevision(t *testing.T) {
	m := deliverableMission()
	m.Evidence[0].AcceptanceRevision = 2
	if err := CanDeliver(m, "digest"); err == nil {
		t.Fatal("evidence for an old plan revision was accepted")
	}
}

func TestCanDeliverRequiresQAAndAuditorForCandidate(t *testing.T) {
	m := deliverableMission()
	m.Reviews = []Review{{Role: QA, Passed: true, CandidateDigest: "digest"}}
	if err := CanDeliver(m, "digest"); err == nil {
		t.Fatal("mission without auditor review was accepted")
	}
	m = deliverableMission()
	m.Reviews[1].CandidateDigest = "old"
	if err := CanDeliver(m, "digest"); err == nil {
		t.Fatal("stale auditor review was accepted")
	}
}

func TestCanDeliverAcceptsExactCandidateEvidenceAndReviews(t *testing.T) {
	m := deliverableMission()
	if err := CanDeliver(m, "digest"); err != nil {
		t.Fatalf("deliverable mission rejected: %v", err)
	}
}

func validSpec() Spec {
	return Spec{
		Scope: []string{"src/"}, Protected: []string{"tests/"},
		Acceptance: []string{"npm test"}, Image: "node", ImageID: "sha256:image",
		ProfileID: "profile-1", MaxCalls: 16,
	}
}

func deliverableMission() Mission {
	return Mission{
		State: "awaiting_delivery", CandidateDigest: "digest",
		Spec: Spec{ImageID: "image-id", Acceptance: []string{"npm test"}},
		Plan: &Plan{Revision: 3},
		Evidence: []Verification{{
			ID: "evidence-1", CandidateDigest: "digest", ImageID: "image-id",
			AcceptanceRevision: 3, Passed: true,
			Results: []CommandResult{{Command: "npm test", ExitCode: 0}},
		}},
		Reviews: []Review{
			{Role: QA, Passed: true, CandidateDigest: "digest"},
			{Role: Auditor, Passed: true, CandidateDigest: "digest"},
		},
	}
}
