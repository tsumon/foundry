package store

import "foundry/internal/domain"

// Model-only schema2 authority never creates a repository/MCP grant. Controlled
// bindings retain their original digest contract; ordinary calls bind the model
// snapshot itself. Filesystem authority is recomputed by the Engine.
func invocationMissionAuthority(m domain.Mission, v domain.Invocation) (domain.TaskBindings, error) {
	if err := domain.ValidateMissionModelSnapshot(m); err != nil {
		return domain.TaskBindings{}, err
	}
	var bindings domain.TaskBindings
	if domain.IsInferenceSnapshot(m) && m.ToolMode == "" && v.Kind == "model" {
		if v.BindingDigest != m.ModelSnapshot.Digest || v.GrantID != "" {
			return bindings, ErrConflict
		}
	} else {
		var err error
		bindings, err = controlledMissionAuthority(m, v.BindingDigest)
		if err != nil {
			return bindings, err
		}
	}
	if domain.IsInferenceSnapshot(m) && v.Kind == "model" {
		kind := "context"
		switch v.Role {
		case domain.Worker, domain.Debugger, domain.QA, domain.Auditor:
			kind = "execute"
		}
		for i := len(m.Approvals) - 1; i >= 0; i-- {
			a := m.Approvals[i]
			if a.Kind != kind {
				continue
			}
			if a.Digest != v.AuthorityDigest || !lowerHex(a.Digest, 64) || (kind == "execute" && (m.Plan == nil || a.PlanRevision != m.Plan.Revision)) {
				return bindings, ErrConflict
			}
			return bindings, nil
		}
		return bindings, ErrConflict
	}
	return bindings, nil
}

func needsInvocationGrant(m domain.Mission, v domain.Invocation) bool {
	return v.Kind == "repository" || (v.Role == domain.Researcher && !(domain.IsInferenceSnapshot(m) && m.ToolMode == "" && v.Kind == "model"))
}
