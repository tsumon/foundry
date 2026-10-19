package store

import (
	"errors"
	"time"

	"foundry/internal/domain"
)

type mcpHistoryState struct {
	status string
	phase  string
	at     time.Time
}

// Read transitions by their SQL owner, never by an embedded or caller-provided
// mission. The final ledger state must be reachable through the entire history.
func (s *Store) checkMCPTransitionHistory(missionID string, calls []domain.MCPInvocation) error {
	byID := make(map[string]domain.MCPInvocation, len(calls))
	for _, v := range calls {
		byID[v.ID] = v
	}
	states := make(map[string]mcpHistoryState, len(calls))
	rows, err := s.db.Query(`SELECT invocation_id,mission_id,version,from_status,to_status,phase,created_at FROM mcp_invocation_transitions WHERE mission_id=? ORDER BY seq`, missionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, owner, from, to, phase, createdAt string
		var version int
		if err := rows.Scan(&id, &owner, &version, &from, &to, &phase, &createdAt); err != nil {
			return err
		}
		if version != 1 {
			return ErrMCPFutureVersion
		}
		v, ok := byID[id]
		if !ok || owner != missionID || v.MissionID != owner {
			return errors.New("MCP transition owner mismatch")
		}
		at, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil || !validCatalogTime(createdAt) {
			return errors.New("invalid MCP transition timestamp")
		}
		previous := states[id]
		if from != previous.status || !legalMCPTransition(previous, to, phase) {
			return errors.New("disconnected MCP transition history")
		}
		lowerBound := previous.at
		if from == "" {
			lowerBound, _ = time.Parse(time.RFC3339Nano, v.CreatedAt)
		}
		if at.Before(lowerBound) {
			return errors.New("MCP transition time moved backwards")
		}
		if from == "authorized_pending" && to == "dispatching" {
			dispatched, _ := time.Parse(time.RFC3339Nano, v.DispatchedAt)
			if at.Before(dispatched) {
				return errors.New("MCP transition predates dispatch")
			}
		}
		states[id] = mcpHistoryState{status: to, phase: phase, at: at}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, v := range calls {
		final, ok := states[v.ID]
		if !ok || final.status != v.Status || final.phase != v.Phase {
			return errors.New("MCP history does not reach invocation state")
		}
		if v.FinishedAt != "" {
			finished, _ := time.Parse(time.RFC3339Nano, v.FinishedAt)
			if final.at.Before(finished) {
				return errors.New("MCP transition predates completion")
			}
		}
	}
	return nil
}

func legalMCPTransition(previous mcpHistoryState, to, phase string) bool {
	switch previous.status {
	case "":
		return to == "authorized_pending" && phase == "preflight"
	case "authorized_pending":
		return phase == "preflight" && (to == "dispatching" || to == "interrupted_before_dispatch")
	case "dispatching":
		if to == "dispatching" {
			return previous.phase == "preflight" && phase == "call_started"
		}
		if phase != previous.phase {
			return false
		}
		switch to {
		case "failed":
			return true
		case "succeeded", "outcome_unknown":
			return phase == "call_started"
		case "interrupted_before_call":
			return phase == "preflight"
		}
	}
	return false
}
