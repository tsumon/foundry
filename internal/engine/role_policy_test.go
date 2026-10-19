package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/mcptest"
	"foundry/internal/store"
)

func cloneRolePolicyMission(t *testing.T, mission domain.Mission) domain.Mission {
	t.Helper()
	raw, err := json.Marshal(mission)
	if err != nil {
		t.Fatal(err)
	}
	var clone domain.Mission
	if err = json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func rolePolicyAuthorityMutations(t *testing.T, mission domain.Mission) map[string]domain.Mission {
	t.Helper()
	removed := cloneRolePolicyMission(t, mission)
	removed.ModelPolicySnapshot = nil
	replaced := cloneRolePolicyMission(t, mission)
	replaced.ModelPolicySnapshot.DecisionID += "-replacement"
	var err error
	replaced.ModelPolicySnapshot.Digest, err = domain.MissionRolePolicySnapshotDigest(*replaced.ModelPolicySnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = domain.ValidateMissionModelSnapshot(replaced); err != nil {
		t.Fatalf("valid replacement fixture rejected: %v", err)
	}
	return map[string]domain.Mission{"removed": removed, "valid replacement": replaced}
}

func withRolePolicyTestSeal(t *testing.T, mission domain.Mission, kind, digest string) domain.Mission {
	t.Helper()
	sealed := cloneRolePolicyMission(t, mission)
	sealed.Approvals = append(sealed.Approvals, domain.Approval{Kind: kind, Digest: digest, Revision: sealed.Revision, CreatedAt: now()})
	return sealed
}

func confirmEngineRolePolicy(t *testing.T, s *store.Store, profileID string) domain.RolePolicyDecision {
	t.Helper()
	state, err := s.GetRolePolicyState()
	if err != nil {
		t.Fatal(err)
	}
	input := domain.RolePolicyInput{Roles: map[domain.Role]domain.RolePolicyRoleInput{}}
	for _, role := range domain.RolePolicyRoles() {
		input.Roles[role] = domain.RolePolicyRoleInput{
			Candidates:           []domain.RolePolicyCandidateInput{{ProfileID: profileID, Capabilities: []string{"coding"}, Rationale: "User declaration; not independently verified."}},
			RequiredCapabilities: []string{"coding"}, AllowedSpendClasses: []string{domain.RolePolicySpendUnknown}, Preference: domain.RolePolicyPreferenceCandidateOrder,
		}
	}
	if _, err = s.SaveRoleModelPolicy(input, state.Policy.Revision, state.ProfilesDigest); err != nil {
		t.Fatal(err)
	}
	proposal, err := s.ProposeRoleModels("engine-proposal")
	if err != nil || !proposal.Confirmable {
		t.Fatalf("proposal = %+v, %v", proposal, err)
	}
	decision, _, applied, err := s.ConfirmRoleModelProposal(proposal.ID, proposal.Digest, "engine-decision")
	if err != nil || !applied {
		t.Fatalf("decision = %+v, applied=%v, err=%v", decision, applied, err)
	}
	return decision
}

func TestMissionRolePolicyCreationAndAuthorityFreezeConfirmedProvenance(t *testing.T) {
	e, s, _, _, old := setup(t)
	decision := confirmEngineRolePolicy(t, s, "test")
	m, err := e.Create(context.Background(), accountCreateRequest(old, ""))
	if err != nil {
		t.Fatal(err)
	}
	if m.ModelPolicySnapshot == nil || m.ModelPolicySnapshot.DecisionID != decision.ID || len(m.ModelPolicySnapshot.Roles) != 7 {
		t.Fatalf("missing frozen policy provenance: %+v", m.ModelPolicySnapshot)
	}
	base := filepath.Join(e.dir(m.ID), "base")
	priorMission := m
	priorMission.ModelPolicySnapshot = nil
	prior, err := authority(priorMission, base, false)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("FOUNDRY role policy authority v1\x00" + m.ModelPolicySnapshot.Digest + "\x00" + prior))
	got, err := authority(m, base, false)
	if err != nil || got != hex.EncodeToString(want[:]) {
		t.Fatalf("policy authority = %q, %v; want %q", got, err, hex.EncodeToString(want[:]))
	}
	if !hasSeal(m, "snapshot", got) {
		t.Fatal("creation sealed authority before assigning provenance")
	}
	removed := m
	removed.ModelPolicySnapshot = nil
	removedAuthority, err := authority(removed, base, false)
	if err != nil {
		t.Fatal(err)
	}
	if removedAuthority == got || hasSeal(removed, "snapshot", removedAuthority) {
		t.Fatal("out-of-band provenance removal retained prior approval")
	}
	tampered := m
	tampered.ModelPolicySnapshot.Digest = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if _, err = authority(tampered, base, false); !errors.Is(err, domain.ErrInvalidModelSnapshot) {
		t.Fatalf("tampered provenance authority = %v", err)
	}
	historical, err := s.GetMission(old.ID)
	if err != nil || historical.ModelPolicySnapshot != nil {
		t.Fatalf("old mission gained provenance: %+v, %v", historical.ModelPolicySnapshot, err)
	}
}

func TestResearchMCPCurrentUsesCommonRolePolicyAuthorityWrapper(t *testing.T) {
	oldEngine, s, _, runner, old := setup(t)
	confirmEngineRolePolicy(t, s, "test")
	fixture, err := mcptest.Start(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fixture.Close() })
	descriptor := fixture.Descriptor()
	e := New(s, &researchGateway{s: s}, Options{DataDir: oldEngine.options.DataDir, Runner: runner, LocalMCPTest: &descriptor})
	t.Cleanup(func() { e.Close() })
	request := controlledRequest(t, s, old)
	request.WorkspaceMode = domain.WorkspaceModeControlledMCPReadV2
	request.ToolMode = domain.ToolModeControlledMCPReadV2
	view, err := e.LocalMCPTestSelection()
	if err != nil {
		t.Fatal(err)
	}
	request.MCPSelection = view.Selection
	m, err := e.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if m.ModelPolicySnapshot == nil {
		t.Fatal("research mission missing policy provenance")
	}
	m, err = s.UpdateMission(m.ID, m.Revision, "fixture.context", func(m *domain.Mission) error {
		m.Approvals = append(m.Approvals, domain.Approval{Kind: "context", Digest: m.Approvals[0].Digest})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = e.mcpCurrent(m); err != nil {
		t.Fatalf("research MCP rejected common wrapped authority: %v", err)
	}
	base := filepath.Join(e.dir(m.ID), "base")
	for name, mutated := range rolePolicyAuthorityMutations(t, m) {
		t.Run(name, func(t *testing.T) {
			mutatedAuthority, authorityErr := authority(mutated, base, false)
			if authorityErr != nil {
				t.Fatal(authorityErr)
			}
			if hasSeal(mutated, "snapshot", mutatedAuthority) || hasSeal(mutated, "context", mutatedAuthority) {
				t.Fatal("mutated provenance matched existing research snapshot/context seals")
			}
			if currentErr := e.mcpCurrent(mutated); currentErr == nil {
				t.Fatal("research current-authority check accepted stale snapshot provenance")
			}
			snapshotResealed := withRolePolicyTestSeal(t, mutated, "snapshot", mutatedAuthority)
			if !hasSeal(snapshotResealed, "snapshot", mutatedAuthority) || hasSeal(snapshotResealed, "context", mutatedAuthority) {
				t.Fatal("fixture did not isolate stale research context seal")
			}
			if currentErr := e.mcpCurrent(snapshotResealed); currentErr == nil || !strings.Contains(currentErr.Error(), "current MCP snapshot and context approval required") {
				t.Fatalf("research context boundary error = %v", currentErr)
			}
			contextResealed := withRolePolicyTestSeal(t, snapshotResealed, "context", mutatedAuthority)
			if currentErr := e.mcpCurrent(contextResealed); currentErr != nil {
				t.Fatalf("research context positive control failed: %v", currentErr)
			}
		})
	}
}

func TestMissionRolePolicyRemovalAndReplacementInvalidateIndependentApprovals(t *testing.T) {
	e, s, _, _, old := setup(t, research, plan)
	confirmEngineRolePolicy(t, s, "test")
	m, err := e.Create(context.Background(), accountCreateRequest(old, ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Plan(context.Background(), m.ID, m.Revision); err != nil {
		t.Fatal(err)
	}
	m = wait(t, s, m.ID, "awaiting_approval")
	var proposalDigest string
	for index := len(m.Approvals) - 1; index >= 0; index-- {
		if m.Approvals[index].Kind == "plan_proposal" {
			proposalDigest = m.Approvals[index].Digest
			break
		}
	}
	if proposalDigest == "" {
		t.Fatal("planning path did not independently seal plan proposal")
	}
	m, err = s.ApproveMissionExecution(m.ID, m.Revision, proposalDigest)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(e.dir(m.ID), "base")
	contextAuthority, err := authority(m, base, false)
	if err != nil {
		t.Fatal(err)
	}
	executeAuthority, err := authority(m, base, true)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSeal(m, "snapshot", contextAuthority) || !hasSeal(m, "context", contextAuthority) || !hasSeal(m, "execute", executeAuthority) {
		t.Fatalf("fixture lacks independently created approvals: %+v", m.Approvals)
	}
	if err = e.checkSnapshot(m); err != nil {
		t.Fatalf("valid policy snapshot/context rejected: %v", err)
	}
	if _, err = e.candidate(m); err != nil {
		t.Fatalf("valid policy execution rejected: %v", err)
	}

	for name, mutated := range rolePolicyAuthorityMutations(t, m) {
		t.Run(name, func(t *testing.T) {
			mutatedContext, authorityErr := authority(mutated, base, false)
			if authorityErr != nil {
				t.Fatal(authorityErr)
			}
			mutatedExecute, authorityErr := authority(mutated, base, true)
			if authorityErr != nil {
				t.Fatal(authorityErr)
			}
			if snapshotErr := e.checkSnapshot(mutated); snapshotErr == nil || !strings.Contains(snapshotErr.Error(), "frozen snapshot/spec/provider inputs changed") {
				t.Fatalf("snapshot boundary error = %v", snapshotErr)
			}
			snapshotResealed := withRolePolicyTestSeal(t, mutated, "snapshot", mutatedContext)
			if snapshotErr := e.checkSnapshot(snapshotResealed); snapshotErr != nil {
				t.Fatalf("snapshot positive control failed: %v", snapshotErr)
			}
			if hasSeal(snapshotResealed, "execute", mutatedExecute) {
				t.Fatal("fixture did not isolate stale includePlan=true execute seal")
			}
			if _, candidateErr := e.candidate(snapshotResealed); candidateErr == nil || !strings.Contains(candidateErr.Error(), "execution approval inputs changed") {
				t.Fatalf("execution boundary error = %v", candidateErr)
			}
			executeResealed := withRolePolicyTestSeal(t, snapshotResealed, "execute", mutatedExecute)
			if _, candidateErr := e.candidate(executeResealed); candidateErr != nil {
				t.Fatalf("execution positive control failed: %v", candidateErr)
			}
		})
	}
}

func TestRecoveryPreflightRejectsInvalidRolePolicySnapshots(t *testing.T) {
	for _, mission := range []domain.Mission{
		{ModelPolicySnapshotInvalid: true},
		{ModelPolicySnapshot: &domain.MissionRolePolicySnapshot{SchemaVersion: 99}},
	} {
		if err := validateRecoveryModelSnapshots([]domain.Mission{mission}); !errors.Is(err, domain.ErrInvalidModelSnapshot) {
			t.Fatalf("recovery preflight error = %v, want ErrInvalidModelSnapshot", err)
		}
	}
	if err := validateRecoveryModelSnapshots([]domain.Mission{{ID: "legacy"}}); err != nil {
		t.Fatalf("legacy recovery rejected: %v", err)
	}
}
