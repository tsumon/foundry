package main

import (
	"fmt"

	"foundry/internal/domain"
)

const syntheticRolePolicyProposalID = "synthetic-role-policy-proposal-unconfirmed"

func seedRolePolicyScenarios(r *acceptanceRuntime) error {
	completed, err := solePlannerInvocation(r, r.scenarios["plan_success"], "succeeded", "")
	if err != nil {
		return fmt.Errorf("seed completed role-policy evidence: %w", err)
	}
	unknown, err := solePlannerInvocation(r, r.scenarios["sse_outcome_unknown"], "outcome_unknown", "response_incomplete")
	if err != nil {
		return fmt.Errorf("seed unknown role-policy evidence: %w", err)
	}
	state, err := r.store.GetRolePolicyState()
	if err != nil {
		return err
	}
	if state.Policy.Revision != 0 || state.LatestProposal != nil || state.ActiveDecision != nil {
		return fmt.Errorf("role-policy fixture requires a fresh policy store")
	}
	policy, err := r.store.SaveRoleModelPolicy(syntheticRolePolicyInput(completed.ID, unknown.ID), 0, state.ProfilesDigest)
	if err != nil {
		return err
	}
	proposal, err := r.store.ProposeRoleModels(syntheticRolePolicyProposalID)
	if err != nil {
		return err
	}
	if policy.Revision != 1 || proposal.PolicyRevision != policy.Revision || !proposal.Confirmable {
		return fmt.Errorf("synthetic role-policy proposal is not confirmable")
	}
	r.scenarios["role_policy_proposal_unconfirmed"] = proposal.ID
	r.scenarios["role_policy_old_mission"] = r.scenarios["plan_success"]
	return nil
}

func solePlannerInvocation(r *acceptanceRuntime, missionID, status, errorCode string) (domain.Invocation, error) {
	if missionID == "" {
		return domain.Invocation{}, fmt.Errorf("scenario mission is missing")
	}
	invocations, err := r.store.ListInvocations(missionID, 0, 100)
	if err != nil {
		return domain.Invocation{}, err
	}
	if len(invocations) != 1 {
		return domain.Invocation{}, fmt.Errorf("expected one planner invocation, got %d", len(invocations))
	}
	invocation := invocations[0]
	if invocation.Role != domain.Planner || invocation.Status != status || invocation.ErrorCode != errorCode {
		return domain.Invocation{}, fmt.Errorf("unexpected planner invocation metadata: role=%s status=%s error_code=%s", invocation.Role, invocation.Status, invocation.ErrorCode)
	}
	return invocation, nil
}

func syntheticRolePolicyInput(completedInvocationID, unknownInvocationID string) domain.RolePolicyInput {
	required := map[domain.Role]string{
		domain.Main:       "concise_explanation",
		domain.Researcher: "long_context",
		domain.Planner:    "structured_output",
		domain.Worker:     "coding",
		domain.Debugger:   "reasoning",
		domain.QA:         "review",
		domain.Auditor:    "review",
	}
	roles := make(map[domain.Role]domain.RolePolicyRoleInput, len(required))
	for _, role := range domain.RolePolicyRoles() {
		capability := required[role]
		candidate := domain.RolePolicyCandidateInput{
			ProfileID: "synthetic-siwc-success-" + string(role), Capabilities: []string{capability},
			Rationale: "SYNTHETIC user declaration for the owned local protocol fixture; not an independent capability test.",
		}
		if role == domain.Planner {
			candidate.Capabilities = []string{"structured_output", "reasoning"}
			candidate.EvidenceInvocationID = completedInvocationID
		}
		roles[role] = domain.RolePolicyRoleInput{
			Candidates: []domain.RolePolicyCandidateInput{candidate}, RequiredCapabilities: []string{capability},
			AllowedSpendClasses: []string{domain.RolePolicySpendSubscriptionOnly}, Preference: domain.RolePolicyPreferenceCandidateOrder,
		}
	}
	planner := roles[domain.Planner]
	planner.Candidates = append(planner.Candidates,
		domain.RolePolicyCandidateInput{
			ProfileID: "synthetic-sse-outcome-unknown-profile", Capabilities: []string{"reasoning"},
			Rationale:            "SYNTHETIC incomplete local response; deliberately missing the required structured-output declaration.",
			EvidenceInvocationID: unknownInvocationID,
		},
		domain.RolePolicyCandidateInput{
			ProfileID: "synthetic-kimi-package-profile", Capabilities: []string{"structured_output"},
			Rationale: "SYNTHETIC interactive-package candidate excluded by the subscription-only fee-channel policy.",
		},
	)
	roles[domain.Planner] = planner
	return domain.RolePolicyInput{Roles: roles}
}
