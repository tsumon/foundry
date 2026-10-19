package engine

import "foundry/internal/domain"

// Wire schemas stay version-specific even when the persisted domain gains
// fields. MCP source citations belong only to the v2 Researcher protocol.
type legacyFinding struct {
	Severity string   `json:"severity"`
	Summary  string   `json:"summary"`
	Paths    []string `json:"paths"`
}
type legacyFindings []legacyFinding

func (f legacyFindings) domainFindings() []domain.Finding {
	if f == nil {
		return nil
	}
	out := make([]domain.Finding, len(f))
	for i, v := range f {
		out[i] = domain.Finding{Severity: v.Severity, Summary: v.Summary, Paths: v.Paths}
	}
	return out
}

type legacyResearchOutput struct {
	Summary  string         `json:"summary"`
	Findings legacyFindings `json:"findings"`
	Unknowns []string       `json:"unknowns"`
}

func (r legacyResearchOutput) research() domain.Research {
	return domain.Research{Summary: r.Summary, Findings: r.Findings.domainFindings(), Unknowns: r.Unknowns}
}
