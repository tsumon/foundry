package engine

import (
	"context"
	"testing"
)

func TestResearchMCPStatusVerifiedContinueCheckpoint(t *testing.T) {
	e, _, m := researchFixture(t)
	m, v := prepareResearchCheckpoint(t, e, m, `{"kind":"final","research":{"summary":"Synthetic final","findings":[],"unknowns":[]}}`)
	m, _, err := e.applyResearchSource(m, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	status, err := e.ResearchMCPStatus(m.ID, m.Revision)
	if err != nil || !status.Current || !status.CanContinue || status.CanDecide {
		t.Fatalf("resumable %+v %v", status, err)
	}
	stale, err := e.ResearchMCPStatus(m.ID, m.Revision-1)
	if err != nil || stale.Current || stale.CanContinue || stale.WhyNot != "stale_snapshot" {
		t.Fatalf("stale %+v %v", stale, err)
	}
	e.gateway = &scripted{outputs: []string{plan}}
	if _, err = e.ContinueResearch(context.Background(), m.ID, m.Revision, "continue-status"); err != nil {
		t.Fatal(err)
	}
	m = researchIdle(t, e, m.ID)
	status, err = e.ResearchMCPStatus(m.ID, m.Revision)
	if err != nil || status.CanContinue || status.CanDecide {
		t.Fatalf("consumed %+v %v", status, err)
	}
}
