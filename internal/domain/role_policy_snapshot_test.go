package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
)

func validMissionRolePolicySnapshot(t *testing.T) Mission {
	t.Helper()
	profile := Profile{ID: "selected", Name: "Selected", Kind: "local", Protocol: "responses", BaseURL: "http://127.0.0.1:1234/v1", Model: "synthetic"}
	profileDigest, err := RolePolicyProfileDigest(profile)
	if err != nil {
		t.Fatal(err)
	}
	m := Mission{Profiles: map[Role]Profile{}, ModelPolicySnapshot: &MissionRolePolicySnapshot{
		SchemaVersion: 1, DecisionID: "decision-one", PolicyRevision: 4,
		RecommendationDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ConfirmedAt:          "2026-10-19T10:00:00Z", Roles: map[Role]RolePolicyRoleResult{},
	}}
	for _, role := range RolePolicyRoles() {
		m.Profiles[role] = profile
		m.ModelPolicySnapshot.Roles[role] = RolePolicyRoleResult{
			SelectedProfileID: "selected", RequiredCapabilities: []string{"coding"},
			Candidates: []RolePolicyCandidateResult{{
				ProfileID: "selected", ProfileDigest: profileDigest, SpendClass: RolePolicySpendUnknown,
				Eligible: true, Reasons: []string{}, Capabilities: []string{"coding", "reasoning"},
				Rationale: "User declaration; not independently verified.", ExecutionNotice: RolePolicyNoticeLegacyUnknown,
			}},
		}
	}
	m.ModelPolicySnapshot.Digest, err = MissionRolePolicySnapshotDigest(*m.ModelPolicySnapshot)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMissionRolePolicySnapshotDigestUsesIndependentDomain(t *testing.T) {
	m := validMissionRolePolicySnapshot(t)
	snapshot := *m.ModelPolicySnapshot
	snapshot.Digest = "caller-controlled"
	raw, err := json.Marshal(struct {
		SchemaVersion        int                           `json:"schema_version"`
		DecisionID           string                        `json:"decision_id"`
		PolicyRevision       int64                         `json:"policy_revision"`
		RecommendationDigest string                        `json:"recommendation_digest"`
		ConfirmedAt          string                        `json:"confirmed_at"`
		Roles                map[Role]RolePolicyRoleResult `json:"roles"`
	}{snapshot.SchemaVersion, snapshot.DecisionID, snapshot.PolicyRevision, snapshot.RecommendationDigest, snapshot.ConfirmedAt, snapshot.Roles})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append([]byte("FOUNDRY mission role model policy v1\x00"), raw...))
	want := hex.EncodeToString(sum[:])
	got, err := MissionRolePolicySnapshotDigest(snapshot)
	if err != nil || got != want {
		t.Fatalf("digest = %q, %v; want %q", got, err, want)
	}
}

func TestMissionRolePolicySnapshotValidationAndNilCompatibility(t *testing.T) {
	if err := ValidateMissionRolePolicySnapshot(Mission{}); err != nil {
		t.Fatalf("legacy mission rejected: %v", err)
	}
	m := validMissionRolePolicySnapshot(t)
	if err := ValidateMissionRolePolicySnapshot(m); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}
	if err := ValidateMissionModelSnapshot(m); err != nil {
		t.Fatalf("composed model validation rejected policy mission: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Mission)
	}{
		{"future schema", func(m *Mission) { m.ModelPolicySnapshot.SchemaVersion = 99 }},
		{"decode diagnostic", func(m *Mission) { m.ModelPolicySnapshotInvalid = true }},
		{"digest", func(m *Mission) { m.ModelPolicySnapshot.Digest = "wrong" }},
		{"missing role", func(m *Mission) { delete(m.ModelPolicySnapshot.Roles, Worker) }},
		{"selected profile", func(m *Mission) {
			result := m.ModelPolicySnapshot.Roles[Worker]
			result.SelectedProfileID = "other"
			m.ModelPolicySnapshot.Roles[Worker] = result
		}},
		{"profile digest", func(m *Mission) {
			result := m.ModelPolicySnapshot.Roles[Worker]
			result.Candidates[0].ProfileDigest = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
			m.ModelPolicySnapshot.Roles[Worker] = result
		}},
		{"excluded candidate", func(m *Mission) {
			result := m.ModelPolicySnapshot.Roles[Worker]
			result.Candidates = append(result.Candidates, result.Candidates[0])
			m.ModelPolicySnapshot.Roles[Worker] = result
		}},
		{"missing required capability", func(m *Mission) {
			result := m.ModelPolicySnapshot.Roles[Worker]
			result.Candidates[0].Capabilities = []string{"reasoning"}
			m.ModelPolicySnapshot.Roles[Worker] = result
		}},
		{"notice disagrees with profile", func(m *Mission) {
			result := m.ModelPolicySnapshot.Roles[Worker]
			result.Candidates[0].ExecutionNotice = RolePolicyNoticeLiveClosed
			m.ModelPolicySnapshot.Roles[Worker] = result
		}},
		{"invented evidence error", func(m *Mission) {
			result := m.ModelPolicySnapshot.Roles[Worker]
			result.Candidates[0].Evidence = &RolePolicyEvidence{InvocationID: "invocation", MissionID: "source", Role: Worker, Status: "failed", ErrorCode: "invented", Environment: "simulation", FinishedAt: "2026-10-19T10:00:00Z"}
			m.ModelPolicySnapshot.Roles[Worker] = result
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := validMissionRolePolicySnapshot(t)
			tc.mutate(&got)
			if tc.name != "digest" {
				got.ModelPolicySnapshot.Digest, _ = MissionRolePolicySnapshotDigest(*got.ModelPolicySnapshot)
			}
			if err := ValidateMissionRolePolicySnapshot(got); !errors.Is(err, ErrInvalidModelSnapshot) {
				t.Fatalf("error = %v, want ErrInvalidModelSnapshot", err)
			}
			if err := ValidateMissionModelSnapshot(got); !errors.Is(err, ErrInvalidModelSnapshot) {
				t.Fatalf("composed error = %v, want ErrInvalidModelSnapshot", err)
			}
		})
	}
}

func TestFuturePolicySnapshotIsNotTrusted(t *testing.T) {
	m := Mission{ModelPolicySnapshot: &MissionRolePolicySnapshot{SchemaVersion: 99}}
	if ValidateMissionRolePolicySnapshot(m) == nil {
		t.Fatal("future snapshot accepted")
	}
	if ValidateMissionRolePolicySnapshot(Mission{}) != nil {
		t.Fatal("legacy mission rejected")
	}
}
