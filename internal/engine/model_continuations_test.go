package engine

import (
	"context"
	"encoding/json"
	"foundry/internal/domain"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func continuedEngineMission(t *testing.T, m domain.Mission) domain.Mission {
	t.Helper()
	var next domain.MissionModelSnapshot
	raw, _ := json.Marshal(m.ModelSnapshot)
	if err := json.Unmarshal(raw, &next); err != nil {
		t.Fatal(err)
	}
	for role, sel := range next.Roles {
		sel.Authorization.ID = "replacement-fixture"
		sel.Profile.CatalogOperationID = "new-catalog"
		sel.Catalog = &domain.ModelCatalogProvenance{OperationID: "new-catalog", ReceiptDigest: strings.Repeat("c", 64), AuthorizationGeneration: 1, ExpiresAt: "2099-01-01T00:00:00Z"}
		next.Roles[role] = sel
	}
	next.Digest, _ = domain.ModelSnapshotDigest(next)
	now := time.Now().Add(-time.Minute)
	c := domain.ModelContinuation{SchemaVersion: 1, ID: "continued", MissionID: m.ID, PreparedRevision: m.Revision, PredecessorDigest: m.ModelSnapshot.Digest, Replacement: next, AffectedRoles: domain.RolePolicyRoles(), AuthorizationGeneration: 1, AuthorizationExpiresAt: "2099-01-01T00:00:00Z", CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: now.Add(10 * time.Minute).Format(time.RFC3339Nano)}
	c.Digest = domain.ModelContinuationDigest(c)
	c.Approval = &domain.ModelContinuationFact{Revision: m.Revision + 2, Digest: c.Digest, CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano)}
	c.Application = &domain.ModelContinuationFact{Revision: m.Revision + 3, Digest: c.Digest, CreatedAt: now.Add(2 * time.Second).Format(time.RFC3339Nano)}
	m.ModelContinuations = []domain.ModelContinuation{c}
	m.Revision += 3
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestModelContinuationBindsFutureAuthorityAndSealedSelection(t *testing.T) {
	e, _, m, _ := inferenceEngineAt(t, "https://api.openai.com/v1", "live", true)
	original, err := authority(m, filepath.Join(e.dir(m.ID), "base"), false)
	if err != nil {
		t.Fatal(err)
	}
	continued := continuedEngineMission(t, m)
	effective, err := authority(continued, filepath.Join(e.dir(m.ID), "base"), false)
	if err != nil {
		t.Fatal(err)
	}
	if original == effective {
		t.Fatal("future approval still binds original snapshot")
	}
	if err = e.checkFrozenAuthority(continued); err != nil {
		t.Fatal("original source snapshot no longer verifies", err)
	}
	input, err := e.controlledModelInput(continued, domain.Main, map[string]string{"question": "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if input.Selection == nil || input.Selection.Authorization.ID != "replacement-fixture" || input.Profile.CatalogOperationID != "new-catalog" {
		t.Fatal("sealed input still uses old selection")
	}
	continued.Approvals = append(continued.Approvals, domain.Approval{Kind: "context", Digest: effective, Revision: 1})
	if hasSeal(continued, "context", effective) {
		t.Fatal("backdated approval reused")
	}
}

func TestModelContinuationHistoricalInputsResolveOriginalDigest(t *testing.T) {
	e, s, m, _ := inferenceEngineAt(t, "https://api.openai.com/v1", "live", true)
	m = approveInferenceContext(t, s, m)
	var answer struct {
		Answer string `json:"answer"`
	}
	if err := e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, domain.Main, map[string]string{"question": "fixture"}, &answer); err != nil {
		t.Fatal(err)
	}
	m, err := s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	continued := continuedEngineMission(t, m)
	raw, _ := json.Marshal(continued)
	if _, err = fixtureSQL(t, e).Exec(`UPDATE missions SET data=?,revision=? WHERE id=?`, raw, continued.Revision, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.verifiedInvocations(m.ID); err != nil {
		t.Fatal("original invocation rejected under replacement", err)
	}
	// A valid historical selection may never be reinterpreted by a different
	// snapshot digest, even if the evidence envelope itself remains intact.
	rows, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	continued.ModelContinuations[0].Replacement.Roles[domain.Main].Authorization.Subject = "changed"
	continued.ModelContinuations[0].Replacement.Digest, _ = domain.ModelSnapshotDigest(continued.ModelContinuations[0].Replacement)
	continued.ModelContinuations[0].Digest = domain.ModelContinuationDigest(continued.ModelContinuations[0])
	raw, _ = json.Marshal(continued)
	if _, err = fixtureSQL(t, e).Exec(`UPDATE missions SET data=? WHERE id=?`, raw, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = e.verifiedInvocations(m.ID); err == nil {
		t.Fatal("malformed authority history accepted for historical input")
	}
}

func TestModelContinuationRequiresNewReadinessPreservesBaseline(t *testing.T) {
	e, _, request, driver, lease := codingImport(t, "http://127.0.0.1:1234/v1")
	m, err := e.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	_, _, subscription, _ := inferenceEngineAt(t, "https://api.openai.com/v1", "live", true)
	// Install structurally valid historical identity in this in-memory fixture;
	// readiness and baseline intentionally do not attest the OAuth grant.
	m.ModelSnapshot = subscription.ModelSnapshot
	m.Profiles = subscription.Profiles
	if _, err = e.currentC4Readiness(m); err != nil {
		t.Fatal("fixture readiness not valid", err)
	}
	baseline, err := currentC4Baseline(m)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(baseline)
	calls := driver.creates
	reads := lease.reads
	continued := continuedEngineMission(t, m)
	if _, err = e.currentC4Readiness(continued); err == nil {
		t.Fatal("applied continuation reused historical readiness")
	}
	after, err := currentC4Baseline(continued)
	if err != nil {
		t.Fatal("continuation discarded source baseline", err)
	}
	raw, _ := json.Marshal(after)
	if string(before) != string(raw) || driver.creates != calls || lease.reads != reads {
		t.Fatal("continuation changed baseline or performed external work")
	}
}

func TestModelContinuationNewReportIncludesEffectiveLineage(t *testing.T) {
	e, _, m, _ := inferenceEngineAt(t, "https://api.openai.com/v1", "live", true)
	original, err := e.deliveryReport(m)
	if err != nil {
		t.Fatal(err)
	}
	continued := continuedEngineMission(t, m)
	report, err := e.deliveryReport(continued)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report, continued.ModelContinuations[0].Replacement.Digest) || !strings.Contains(report, continued.ModelSnapshot.Digest) || !strings.Contains(report, "replacement-fixture") {
		t.Fatal("new report omitted original/effective authority lineage")
	}
	again, err := e.deliveryReport(m)
	if err != nil || again != original {
		t.Fatal("historical report changed", err)
	}
}

func TestModelContinuationFreshPlanningApprovalBindsEffectiveSnapshot(t *testing.T) {
	e, s, old, _ := inferenceEngineAt(t, "https://api.openai.com/v1", "live", true)
	m, err := e.Create(context.Background(), accountCreateRequest(old, domain.WorkspaceModeGenerationsV1))
	if err != nil {
		t.Fatal(err)
	}
	m = continuedEngineMission(t, m)
	m.State = "blocked"
	raw, _ := json.Marshal(m)
	if _, err = fixtureSQL(t, e).Exec(`UPDATE missions SET data=?,revision=?,state=? WHERE id=?`, raw, m.Revision, m.State, m.ID); err != nil {
		t.Fatal(err)
	}
	m, err = e.resetGeneration(m)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := authority(m, filepath.Join(e.dir(m.ID), "base"), false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSeal(m, "context", expected) {
		t.Fatal("fresh planning consent copied original snapshot seal")
	}
	persisted, err := s.GetMission(m.ID)
	if err != nil || persisted.Approvals[len(persisted.Approvals)-1].Digest != expected {
		t.Fatal("fresh approval not persisted", err)
	}
}
