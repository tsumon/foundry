package knowledge

import (
	"encoding/json"
	"foundry/internal/domain"
	"strings"
	"testing"
)

func TestProjectionProvenanceHistoryAndRedaction(t *testing.T) {
	m := domain.Mission{ID: "m", Title: "Task", Revision: 9, RepoPath: "SECRET_REPO", Profiles: map[domain.Role]domain.Profile{domain.Main: {BaseURL: "SECRET_ACCOUNT"}}, CandidateDigest: "candidate", ContextManifest: []domain.FileMeta{{Path: "main.go", SHA256: "hash"}}, Plan: &domain.Plan{Revision: 2, Steps: []domain.Step{{ID: "a", Title: "Edit", Files: []string{"main.go"}}, {ID: "b", DependsOn: []string{"missing"}}}}, Changes: []domain.Change{{Path: "main.go", After: "SECRET_BODY"}}, Evidence: []domain.Verification{{ID: "e", CandidateDigest: "candidate", Passed: true, SourceVersion: 1, SourceKind: "synthetic_stub", Results: []domain.CommandResult{{Output: "SECRET_OUTPUT"}}}}, RunArchives: []domain.RunArchive{{Epoch: 1, Revision: 4, CandidateDigest: "old", Evidence: []domain.Verification{{ID: "e", CandidateDigest: "old", Passed: true}}}}}
	g, err := Project(Source{Missions: []domain.Mission{m}, Catalog: domain.CatalogSnapshot{Resources: []domain.Resource{{CatalogMeta: domain.CatalogMeta{ID: "r"}, Name: "Resource", Account: "SECRET_ACCOUNT", ExternalID: "SECRET_EXTERNAL"}}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(g)
	if strings.Contains(string(raw), "SECRET_") {
		t.Fatal("sensitive source field projected")
	}
	if len(g.Issues) == 0 {
		t.Fatal("missing step reference not reported")
	}
	count := 0
	for _, n := range g.Nodes {
		if n.Kind == "evidence" {
			count++
			if n.Historical && n.EvidenceSource != "unknown" {
				t.Fatal("legacy history provenance upgraded")
			}
			if !n.Historical && n.EvidenceSource != "synthetic_stub" {
				t.Fatal("synthetic provenance lost")
			}
		}
	}
	if count != 2 {
		t.Fatalf("evidence count %d", count)
	}
	again, _ := Project(Source{Missions: []domain.Mission{m}, Catalog: domain.CatalogSnapshot{Resources: []domain.Resource{{CatalogMeta: domain.CatalogMeta{ID: "r"}, Name: "Resource", Account: "changed"}}}})
	if again.SourceDigest != g.SourceDigest {
		t.Fatal("excluded fields changed projection digest")
	}
}
func TestValidateRejectsDanglingAndIllegalEdges(t *testing.T) {
	g, err := Project(Source{Missions: []domain.Mission{{ID: "m"}}})
	if err != nil {
		t.Fatal(err)
	}
	g.Edges = []Edge{{ID: "x", From: g.Nodes[0].ID, To: "missing", Kind: "contains", Source: Provenance{ID: "m"}}}
	if Validate(g) == nil {
		t.Fatal("dangling edge accepted")
	}
	g.Edges[0].To = g.Nodes[0].ID
	g.Edges[0].Kind = "verified_by"
	if Validate(g) == nil {
		t.Fatal("illegal endpoint kind accepted")
	}
	g.SchemaVersion++
	if Validate(g) == nil {
		t.Fatal("unsupported version accepted")
	}
}
func TestTaskContextIsBoundedCurrentInputOnly(t *testing.T) {
	m := domain.Mission{ID: "m", Revision: 2, Title: "PRIVATE_TITLE", RepoPath: "PRIVATE_REPO", BaseCommit: "PRIVATE_COMMIT", CandidateDigest: "PRIVATE_CANDIDATE", Approvals: []domain.Approval{{Kind: "PRIVATE_APPROVAL"}}, Reviews: []domain.Review{{Summary: "PRIVATE_REVIEW"}}, RunArchives: []domain.RunArchive{{CandidateDigest: "PRIVATE_ARCHIVE"}}}
	for i := 0; i < 90; i++ {
		m.ContextManifest = append(m.ContextManifest, domain.FileMeta{Path: strings.Repeat("x", i+1)})
	}
	c := TaskContext(m)
	if !c.Truncated || len(c.Nodes) > 60 || len(c.Edges) > 100 {
		t.Fatal("context bounds missing")
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("extra source metadata leaked")
	}
	ids := map[string]bool{}
	for _, n := range c.Nodes {
		ids[n.ID] = true
		if n.MissionID != "m" || n.Historical {
			t.Fatal("foreign/historical context")
		}
	}
	for _, e := range c.Edges {
		if !ids[e.From] || !ids[e.To] {
			t.Fatal("dangling truncated context")
		}
	}
}
func TestEmptyAndMalformedSources(t *testing.T) {
	g, err := Project(Source{})
	if err != nil || len(g.Nodes) != 0 || g.Nodes == nil || g.Edges == nil {
		t.Fatal("invalid empty graph", err)
	}
	g, err = Project(Source{Catalog: domain.CatalogSnapshot{Targets: []domain.DeliveryTarget{{CatalogMeta: domain.CatalogMeta{ID: "t"}, ProjectID: "missing"}}}})
	if err != nil || len(g.Issues) != 1 || len(g.Nodes) != 1 || len(g.Edges) != 0 {
		t.Fatalf("malformed reference fabricated endpoint: %+v %v", g, err)
	}
}

func TestCatalogMembershipAndStoredPlanHistory(t *testing.T) {
	current := domain.Plan{Revision: 2, Steps: []domain.Step{{ID: "current"}}}
	old := domain.Plan{Revision: 1, Steps: []domain.Step{{ID: "old", Files: []string{"not-recorded.go"}}}}
	g, err := Project(Source{Catalog: domain.CatalogSnapshot{Projects: []domain.Project{{CatalogMeta: domain.CatalogMeta{ID: "p"}}}, MissionLinks: []domain.MissionProjectLink{{MissionID: "m", ProjectID: "p"}}}, Missions: []domain.Mission{{ID: "m", Plan: &current, CandidateDigest: "c", Reviews: []domain.Review{{Role: domain.QA, CandidateDigest: "c", Passed: true}}, RunArchives: []domain.RunArchive{{Epoch: 1, Plan: &old}}}}, PlanRevisions: []domain.PlanRevision{{MissionID: "m", PlanRevision: 1, Plan: old}, {MissionID: "m", PlanRevision: 2, Plan: current}, {MissionID: "m", PlanRevision: 3, Plan: domain.Plan{Revision: 3}, MissionRevision: 8}}})
	if err != nil {
		t.Fatal(err)
	}
	plans := 0
	historical := 0
	reviewed := false
	for _, n := range g.Nodes {
		if n.MissionID == "m" && n.ProjectID != "p" {
			t.Fatal("task descendant lost project membership")
		}
		if n.Kind == "plan" {
			plans++
			if n.Historical {
				historical++
			}
		}
		if n.Kind == "file" && (n.State != "planned" || n.Source.Kind != "plan") {
			t.Fatal("planned file presented as repository fact")
		}
	}
	for _, e := range g.Edges {
		if e.Kind == "reviewed_by" {
			reviewed = true
		}
	}
	if plans != 3 || historical != 2 || !reviewed {
		t.Fatalf("bad historical plan/review projection: plans %d history %d reviewed %v", plans, historical, reviewed)
	}
}
func TestSchemaPairsRejectContainsCartesianProduct(t *testing.T) {
	g, _ := Project(Source{Missions: []domain.Mission{{ID: "m", RepoPath: "repo", Approvals: []domain.Approval{{Kind: "context"}}}}})
	var repo, approval string
	for _, n := range g.Nodes {
		if n.Kind == "repository" {
			repo = n.ID
		}
		if n.Kind == "approval" {
			approval = n.ID
		}
	}
	g.Edges = append(g.Edges, Edge{ID: "illegal", From: repo, To: approval, Kind: "contains", Source: Provenance{ID: "m"}})
	if Validate(g) == nil {
		t.Fatal("repository→approval Cartesian pair accepted")
	}
}

func TestRepairCandidateLineageAndReferencedLegacyCandidate(t *testing.T) {
	m := domain.Mission{ID: "m", CandidateDigest: "fixed", CodingAttempts: []domain.CodingAttempt{{ID: "repair", Before: &domain.GenerationRef{ID: "g1", Digest: "failed"}, After: &domain.GenerationRef{ID: "g2", Digest: "fixed"}}}, Evidence: []domain.Verification{{ID: "failure", CandidateDigest: "failed", Passed: false}, {ID: "success", CandidateDigest: "fixed", Passed: true, SourceVersion: 1, SourceKind: "isolated_execution"}, {ID: "legacy", CandidateDigest: "unavailable"}}}
	g, err := Project(Source{Missions: []domain.Mission{m}})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Issues) != 0 {
		t.Fatalf("recorded previous candidates treated as missing: %+v", g.Issues)
	}
	states := map[string]Node{}
	for _, n := range g.Nodes {
		if n.Kind == "candidate" {
			states[n.Properties["digest"]] = n
		}
	}
	if !states["failed"].Historical || states["failed"].State != "previous" || states["fixed"].Historical || states["unavailable"].State != "referenced" || states["unavailable"].Properties["snapshot_availability"] != "not_recorded" {
		t.Fatal("candidate availability/history lost")
	}
	evolves, verified := 0, 0
	for _, e := range g.Edges {
		if e.Kind == "evolves_to" {
			evolves++
		}
		if e.Kind == "verified_by" {
			verified++
		}
	}
	if evolves != 1 || verified != 3 {
		t.Fatalf("missing repair/retest lineage %d %d", evolves, verified)
	}
}

func TestProposalIsNotApprovalAndRepeatedChangesAreOneFile(t *testing.T) {
	m := domain.Mission{ID: "m", CandidateDigest: "c", Plan: &domain.Plan{Revision: 2}, Approvals: []domain.Approval{{Kind: "plan_proposal", PlanRevision: 2}}, Changes: []domain.Change{{Path: "same.go"}, {Path: "same.go"}}, Evidence: []domain.Verification{{ID: "e", CandidateDigest: "c"}}}
	g, err := Project(Source{Missions: []domain.Mission{m}})
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Issues) != 0 {
		t.Fatal(g.Issues)
	}
	files := 0
	for _, n := range g.Nodes {
		if n.Kind == "file" {
			files++
		}
	}
	for _, e := range g.Edges {
		if e.Kind == "approved_by" {
			t.Fatal("proposal misrepresented as authorization")
		}
	}
	if files != 1 {
		t.Fatal("duplicate changes created duplicate files")
	}
	c := TaskContext(m)
	for _, n := range c.Nodes {
		if n.Kind == "evidence" && n.Historical {
			t.Fatal("current evidence falsely historical")
		}
	}
}
