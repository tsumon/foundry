package vmacceptance

import (
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/model"
	"testing"
)

func TestIdeaFixturePlansAndEmitsRequirementAssertions(t *testing.T) {
	f, err := selectFixture("c4_node_model_v2", "idea-app")
	if err != nil {
		t.Fatal(err)
	}
	reply, err := fixtureReplyFor(model.Request{Role: domain.Planner}, &f)
	if err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Steps []struct {
			Files []string `json:"files"`
		} `json:"steps"`
	}
	if json.Unmarshal([]byte(reply), &plan) != nil || len(plan.Steps) != 1 || len(plan.Steps[0].Files) != 2 {
		t.Fatal("idea plan must include app and requirement checks", reply)
	}
	reply, err = fixtureReplyFor(model.Request{Role: domain.Worker}, &f)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Edits []struct{ Path, Content string } `json:"edits"`
	}
	if json.Unmarshal([]byte(reply), &result) != nil || len(result.Edits) != 2 || result.Edits[0].Path != "src/app.js" || result.Edits[1].Path != "src/checks.js" {
		t.Fatal("worker must emit business code and assertions", reply)
	}
}
