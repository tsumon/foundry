package store

import "foundry/internal/domain"

// RecoverResearchFlow performs local integrity/interruption checks only. It
// leaves successful unapplied source/Planner outputs for Engine reconciliation,
// and verified observations for explicit user continuation. No dispatch occurs.
func (s *Store) RecoverResearchFlow(missionID string) error {
	m, err := s.GetMission(missionID)
	if err != nil {
		return err
	}
	if !domain.IsResearchFlowV2(m.WorkspaceMode, m.ToolMode) {
		return ErrConflict
	}
	return s.RecoverMCPInvocations(missionID)
}

func (s *Store) checkResearchHistory(missionID string) error {
	if _, err := s.ListResearchTurnApplications(missionID); err != nil {
		return err
	}
	if _, err := s.ListResearchConsumptions(missionID); err != nil {
		return err
	}
	if _, err := s.ListResearchPlannerCheckpoints(missionID); err != nil {
		return err
	}
	if _, err := s.ListResearchPlanApplications(missionID); err != nil {
		return err
	}
	return nil
}
