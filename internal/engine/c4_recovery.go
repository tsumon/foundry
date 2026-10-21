package engine

import "foundry/internal/domain"

// Caller owns engine lock. Recovery records uncertainty only; it never probes,
// discovers, removes, replays, refunds or manufactures a command outcome.
func (e *Engine) recoverC4(m domain.Mission) error {
	interrupted := false
	for index := range m.C4Operations {
		if m.C4Operations[index].Result != nil {
			continue
		}
		var err error
		m, err = e.s.UpdateMission(m.ID, m.Revision, "c4.outcome_unknown", func(current *domain.Mission) error {
			o := &current.C4Operations[index]
			o.Result = &domain.C4Result{Class: "outcome_unknown", Output: "Synthetic C4 operation interrupted; no replay authorized.", Revision: current.Revision, At: now()}
			o.State = "finished"
			return nil
		})
		if err != nil {
			return err
		}
		interrupted = true
	}
	if unfinishedCoding(m) {
		interrupted = true
	}
	switch m.State {
	case "running", "planning", "verifying", "pausing":
		interrupted = true
	}
	if !interrupted {
		return nil
	}
	if err := e.interruptUnfinished(m); err != nil {
		return err
	}
	_, err := e.s.UpdateMission(m.ID, m.Revision, "c4.recovery_stopped", func(current *domain.Mission) error {
		interruptCoding(current)
		current.Epoch++
		current.State = "blocked"
		current.Error = "C4 outcome unknown; inspect recorded facts and request exact-owned cleanup separately. No automatic resume or replay."
		return nil
	})
	return err
}

func c4Diagnostic(m domain.Mission) domain.RecoveryCheck {
	c := recoveryCheck("c4", domain.RecoveryVerified, "synthetic_records_only", "Synthetic C4 facts only; no Docker readiness or real Go execution checked.")
	if c4Structure(m) != nil {
		return recoveryCheck("c4", domain.RecoveryBlocked, "c4_invalid", "C4 records are invalid or unsupported; read-only diagnosis only.")
	}
	for _, o := range m.C4Operations {
		c.Records = append(c.Records, recoveryRefs("c4_operation", o.ID)...)
		if o.Result == nil || o.Result.Class == "outcome_unknown" || o.CleanupState() != "confirmed" {
			c.Status = domain.RecoveryBlocked
			c.Code = "c4_outcome_unknown_or_cleanup_pending"
			c.Message = "C4 outcome_unknown or unconfirmed cleanup: inspect exact operation facts. Explicit exact-owned cleanup cannot resume execution or establish a pass."
		}
	}
	return c
}
