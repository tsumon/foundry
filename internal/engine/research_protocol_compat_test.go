package engine

import "testing"

func TestLegacyResearchAndReviewRejectV2Sources(t *testing.T) {
	for _, sources := range []string{`[]`, `null`, `[{"kind":"mcp","invocation_id":"call","digest":"digest"}]`} {
		finding := `{"severity":"info","summary":"fact","paths":["README.md"],"sources":` + sources + `}`
		t.Run("R3/"+sources, func(t *testing.T) {
			if _, err := parseResearchProposal(`{"kind":"final","research":{"summary":"brief","findings":[` + finding + `],"unknowns":[]}}`); err == nil {
				t.Fatal("legacy R3 accepted v2 sources field")
			}
		})
		for _, decoder := range []struct {
			name   string
			decode func(string, any) error
		}{{"legacy", decodeStrict}, {"controlled", decodeControlled}} {
			t.Run(decoder.name+"/research/"+sources, func(t *testing.T) {
				var out legacyResearchOutput
				if err := decoder.decode(`{"summary":"brief","findings":[`+finding+`],"unknowns":[]}`, &out); err == nil {
					t.Fatal("legacy Researcher accepted v2 sources field")
				}
			})
			t.Run(decoder.name+"/review/"+sources, func(t *testing.T) {
				var out reviewOutput
				if err := decoder.decode(`{"passed":true,"summary":"review","findings":[`+finding+`]}`, &out); err == nil {
					t.Fatal("review accepted Researcher-only v2 sources field")
				}
			})
		}
	}
	valid := `{"summary":"brief","findings":[{"severity":"info","summary":"fact","paths":["README.md"]}],"unknowns":[]}`
	var old legacyResearchOutput
	if err := decodeStrict(valid, &old); err != nil || len(old.research().Findings) != 1 {
		t.Fatalf("old valid output changed: %+v %v", old, err)
	}
	if _, err := parseResearchProposal(`{"kind":"final","research":` + valid + `}`); err != nil {
		t.Fatal(err)
	}
	if _, err := parseResearchTurn(`{"kind":"final","research":{"summary":"brief","findings":[{"severity":"info","summary":"fact","paths":[],"sources":[{"kind":"mcp","invocation_id":"call","digest":"digest"}]}],"unknowns":[]}}`); err != nil {
		t.Fatal("v2 citation schema lost", err)
	}
}

func TestResearchDecisionDisclosesEveryDerivedBriefRecipient(t *testing.T) {
	e, _, m := researchFixture(t)
	if _, err := e.PlanControlled(t.Context(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	researchIdle(t, e, m.ID)
	v, err := e.ResearchMCPDecision(m.ID)
	if err != nil || v == nil {
		t.Fatalf("decision: %+v %v", v, err)
	}
	for role, profile := range m.Profiles {
		found := false
		for _, recipient := range v.Recipients {
			found = found || recipient.Role == role && recipient.Name == profile.Name && recipient.Protocol == profile.Protocol && recipient.Model == profile.Model
		}
		if !found {
			t.Errorf("missing frozen recipient %s", role)
		}
	}
}
