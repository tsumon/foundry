package engine

import (
	"database/sql"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/store"
	"github.com/mattn/go-sqlite3"
	"strings"
)

// ResearchStatus is a read-only hint, never permission. Mutations independently
// recheck current authority while holding the same controller lock.
type ResearchStatus struct {
	Current     bool   `json:"current"`
	CanDecide   bool   `json:"can_decide"`
	CanContinue bool   `json:"can_continue"`
	WhyNot      string `json:"why_not,omitempty"`
}

func (e *Engine) ResearchMCPStatus(id string, revision int64) (ResearchStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.s.GetMission(id)
	if err != nil {
		return ResearchStatus{}, err
	}
	if !domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return ResearchStatus{}, store.ErrConflict
	}
	if m.Revision != revision {
		return ResearchStatus{WhyNot: "stale_snapshot"}, nil
	}
	status, err := e.researchStatus(m)
	if err != nil && !researchStatusInfrastructureError(err) {
		return ResearchStatus{WhyNot: "history_unavailable"}, nil
	}
	return status, err
}

func researchStatusInfrastructureError(err error) bool {
	var sqliteError sqlite3.Error
	return errors.As(err, &sqliteError) || errors.Is(err, sql.ErrConnDone) || errors.Is(err, sql.ErrTxDone) || strings.HasPrefix(err.Error(), "sql:")
}

func (e *Engine) researchStatus(m domain.Mission) (ResearchStatus, error) {
	v := ResearchStatus{}
	if err := e.verifyResearchHistory(m); err != nil {
		return v, err
	}
	if e.closed || e.options.LocalMCPTest == nil || e.options.LocalMCPTest.Alive == nil || !e.options.LocalMCPTest.Alive() {
		v.WhyNot = "local_test_disabled"
		return v, nil
	}
	// Reuse authority and snapshot validation, including frozen profiles/Skills;
	// do not derive permission from the presence of a historical proposal.
	if err := e.mcpCurrent(m); err != nil {
		if researchStatusInfrastructureError(err) {
			return v, err
		}
		v.WhyNot = "authority_unavailable"
		return v, nil
	}
	if err := e.checkSnapshot(m); err != nil {
		if researchStatusInfrastructureError(err) {
			return v, err
		}
		v.WhyNot = "authority_unavailable"
		return v, nil
	}
	v.Current = true
	if e.active != nil {
		v.WhyNot = "engine_busy"
		return v, nil
	}
	if m.State != "planning" {
		v.WhyNot = "not_planning"
		return v, nil
	}
	v.CanDecide = m.Stage == "awaiting_tool_approval"
	_, _, err := e.researchResumable(m)
	if err != nil && !errors.Is(err, errNoResearchCheckpoint) {
		return v, err
	}
	v.CanContinue = err == nil
	if !v.CanDecide && !v.CanContinue {
		v.WhyNot = "no_pending_action"
	}
	return v, nil
}
