package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCodingCannotDeliverPassingProseWithoutChecks(t *testing.T) {
	m := deliverableMission()
	m.CodingVersion = 1
	m.WorkspaceMode = WorkspaceModeGenerationsV1
	if err := CanDeliver(m, m.CandidateDigest); err == nil {
		t.Fatal("version-one passing prose without controller checklist delivered")
	}
}

func codingReviewMission(t *testing.T) Mission {
	t.Helper()
	m := deliverableMission()
	m.ID, m.Epoch, m.CodingVersion, m.WorkspaceMode = "task", 1, 1, WorkspaceModeGenerationsV1
	m.Spec = validSpec()
	m.Plan.Steps = []Step{{ID: "one", Title: "change", Files: []string{"src/main.txt"}}}
	m.ContextManifest = []FileMeta{{Path: "src/main.txt"}}
	m.CandidateGeneration = &GenerationRef{ID: "generation", Digest: m.CandidateDigest}
	m.Evidence[0].ImageID = m.Spec.ImageID
	m.Approvals = []Approval{{Kind: "execute", Digest: "authority", PlanRevision: 3, Revision: 7}}
	m.CodingAttempts = []CodingAttempt{{ID: "verification-attempt", Epoch: 1, PlanRevision: 3, Phase: "verification", Status: "succeeded", Before: m.CandidateGeneration, After: m.CandidateGeneration, VerificationID: "evidence-1", StartedAt: "2020-01-01T00:00:00Z", FinishedAt: "2020-01-01T00:00:01Z"}}
	m.Reviews = nil
	for _, role := range []Role{QA, Auditor} {
		c, err := NewCodingReviewContract(m, role)
		if err != nil {
			t.Fatal(err)
		}
		r := Review{Role: role, CodingVersion: 1, PlanRevision: 3, CandidateDigest: m.CandidateDigest, TraceDigest: c.TraceDigest, BindingDigest: c.BindingDigest, Passed: true, Summary: "assessment", Findings: []Finding{}}
		passed := true
		for _, ref := range c.Required {
			r.Checks = append(r.Checks, CodingReviewCheck{ID: ref.ID, Passed: &passed, Summary: "reviewed reference", Paths: []string{}, VerificationID: ref.VerificationID, AcceptanceIndex: ref.AcceptanceIndex, ReferenceDigest: ref.ReferenceDigest})
		}
		m.Reviews = append(m.Reviews, r)
	}
	return m
}

func TestCodingReviewTraceProjectionIsStableWithoutWeakeningArchiveDigest(t *testing.T) {
	m := codingReviewMission(t)
	if err := CanDeliver(m, m.CandidateDigest); err != nil {
		t.Fatal(err)
	}
	beforeReview, beforeFull := CodingReviewTraceDigest(m), CodingTraceDigest(m)
	m.CodingAttempts = append(m.CodingAttempts, CodingAttempt{ID: "qa-attempt", Epoch: 1, PlanRevision: 3, Phase: "qa", Role: QA, Status: "succeeded", Before: m.CandidateGeneration, After: m.CandidateGeneration, StartedAt: "2020-01-01T00:00:01Z", FinishedAt: "2020-01-01T00:00:02Z"})
	if CodingReviewTraceDigest(m) != beforeReview || CodingTraceDigest(m) == beforeFull {
		t.Fatal("review self-reference or weakened full archive digest")
	}
	if err := CanDeliver(m, m.CandidateDigest); err != nil {
		t.Fatal("review completion invalidated reviews", err)
	}
	m.Plan.Steps[0].Status = "done"
	if err := CanDeliver(m, m.CandidateDigest); err != nil {
		t.Fatal("runtime step state altered approved plan binding", err)
	}
	m.CodingAttempts[0].InvocationID = "other"
	if err := CanDeliver(m, m.CandidateDigest); err == nil {
		t.Fatal("relevant execution trace change did not invalidate reviews")
	}
}

func TestCodingReviewCannotUseAmbiguousVerificationIDs(t *testing.T) {
	m := codingReviewMission(t)
	m.Evidence = append(m.Evidence, m.Evidence[0])
	if _, err := NewCodingReviewContract(m, QA); err == nil {
		t.Fatal("duplicate verification ID was offered as unique supplied reference")
	}
}

func TestCodingReviewLegacySerializationOmitsAllNewFields(t *testing.T) {
	raw, err := json.Marshal(Review{Role: QA, Passed: true, Summary: "old", Findings: []Finding{}, CandidateDigest: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"role":"qa","passed":true,"summary":"old","findings":[],"candidate_digest":"old"}` {
		t.Fatal("historical review bytes changed", string(raw))
	}
}

func TestCodingReviewBoundsAndMissingDecisionsFailClosed(t *testing.T) {
	for _, name := range []string{"summary", "check_summary", "finding_summary", "missing_boolean", "wrong_index", "contradiction", "paths"} {
		t.Run(name, func(t *testing.T) {
			m := codingReviewMission(t)
			r := &m.Reviews[0]
			switch name {
			case "summary":
				r.Summary = strings.Repeat("x", 4001)
			case "check_summary":
				r.Checks[0].Summary = strings.Repeat("x", 4001)
			case "finding_summary":
				r.Findings = []Finding{{Severity: "info", Summary: strings.Repeat("x", 4001), Paths: []string{}}}
			case "missing_boolean":
				r.Checks[0].Passed = nil
			case "wrong_index":
				index := 1
				r.Checks[0].AcceptanceIndex = &index
			case "contradiction":
				r.Findings = []Finding{{Severity: "high", Summary: "blocking", Paths: []string{}}}
			case "paths":
				r.Checks[0].Paths = []string{"invented"}
			}
			if err := CanDeliver(m, m.CandidateDigest); err == nil {
				t.Fatal("invalid review delivered")
			}
		})
	}
}
