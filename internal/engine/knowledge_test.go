package engine

import (
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/knowledge"
	"strings"
	"testing"
)

func TestControlledKnowledgeUsesExistingTaskInputAllowlist(t *testing.T) {
	e, _, _, _, m := controlledSetup(t)
	m.Title = "PRIVATE_TASK_TITLE"
	m.Delivery = &domain.Delivery{PatchPath: "PRIVATE_DELIVERY_PATH"}
	m.RunArchives = []domain.RunArchive{{CandidateDigest: "PRIVATE_ARCHIVE"}}
	m.Approvals = []domain.Approval{{Kind: "PRIVATE_APPROVAL"}}
	m.Plan = &domain.Plan{Revision: 1, Steps: []domain.Step{{ID: "one", Title: "Existing plan step"}}}
	input, err := e.controlledModelInput(m, domain.Researcher, nil)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Knowledge knowledge.Context `json:"knowledge"`
	}
	if err = json.Unmarshal([]byte(input.Prompt), &payload); err != nil {
		t.Fatal(err)
	}
	graph := payload.Knowledge
	if graph.SchemaVersion != knowledge.SchemaVersion || graph.MissionID != m.ID {
		t.Fatal("knowledge context missing")
	}
	raw, _ := json.Marshal(graph)
	if strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("administrative/historical data added to model context")
	}
	for _, n := range graph.Nodes {
		if n.Kind == "repository" || n.Kind == "approval" || n.Kind == "delivery" || n.Kind == "review" {
			t.Fatalf("unapproved additional field %s", n.Kind)
		}
	}
}
