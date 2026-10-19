package store

import (
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"testing"
)

func TestAccountGenericUpdateCannotGrantExecutionAuthority(t *testing.T) {
	for name, mutate := range map[string]func(*domain.Mission){
		"new-seal": func(m *domain.Mission) {
			m.Approvals = append(m.Approvals, domain.Approval{Kind: "execute", Digest: "new"})
		},
		"replaced-seal":      func(m *domain.Mission) { m.Approvals[0].Digest = "replacement" },
		"running-transition": func(m *domain.Mission) { m.State = "running"; m.Epoch++ },
	} {
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t)
			snapshotStoreAccount(t, s)
			m := snapshotMission(t, s, "approval")
			m.State = "awaiting_approval"
			m.Approvals = []domain.Approval{{Kind: "execute", Digest: "historical"}}
			m, err := s.CreateMission(m)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(m)
			events, _ := s.Events(0, 100)
			if _, err = s.UpdateMission(m.ID, m.Revision, "arbitrary.event", func(m *domain.Mission) error { mutate(m); return nil }); err == nil {
				t.Fatal("generic mutation granted account execution authority")
			}
			got, _ := s.GetMission(m.ID)
			after, _ := json.Marshal(got)
			afterEvents, _ := s.Events(0, 100)
			if string(before) != string(after) || len(events) != len(afterEvents) {
				t.Fatal("denied mutation changed mission/events")
			}
		})
	}
}

func TestAccountExecutionApprovalChecksOnlyExecutionRoles(t *testing.T) {
	for _, role := range []domain.Role{domain.Main, domain.Researcher, domain.Planner, domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
		t.Run(string(role), func(t *testing.T) {
			s := openTestStore(t)
			a := snapshotStoreAccount(t, s)
			if err := s.PutProfile(domain.Profile{ID: "legacy", Name: "Legacy", Protocol: "responses", Model: "fake", BaseURL: "https://example.com"}); err != nil {
				t.Fatal(err)
			}
			if err := s.PutSettings(domain.Settings{DefaultProfileID: "legacy", RoleBindings: map[domain.Role]string{role: "bound"}}); err != nil {
				t.Fatal(err)
			}
			m := snapshotMission(t, s, "approval")
			m.State = "awaiting_approval"
			m.Plan = &domain.Plan{Revision: 2}
			m.Approvals = []domain.Approval{{Kind: "plan_proposal", Digest: "approved", PlanRevision: 2}}
			m, err := s.CreateMission(m)
			if err != nil {
				t.Fatal(err)
			}
			a.Enabled = false
			if _, err = s.SaveModelAccount(a, a.Revision); err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(m)
			events, _ := s.Events(0, 100)
			got, err := s.ApproveMissionExecution(m.ID, m.Revision, "approved")
			if role == domain.Worker || role == domain.Debugger || role == domain.QA || role == domain.Auditor {
				if !errors.Is(err, ErrModelAccountUnavailable) {
					t.Fatalf("unavailable %s approval=%v", role, err)
				}
				got, _ = s.GetMission(m.ID)
				after, _ := json.Marshal(got)
				afterEvents, _ := s.Events(0, 100)
				if string(before) != string(after) || len(events) != len(afterEvents) {
					t.Fatal("denied execution approval changed mission/events")
				}
				return
			}
			if err != nil {
				t.Fatalf("unrelated account role blocked execution: %v", err)
			}
			if got.State != "running" || got.Epoch != m.Epoch+1 || got.Revision != m.Revision+1 || got.Budget != m.Budget || len(got.Approvals) != len(m.Approvals)+1 {
				t.Fatalf("wrong approval transition: %+v", got)
			}
			approval := got.Approvals[len(got.Approvals)-1]
			if approval.Kind != "execute" || approval.Digest != "approved" || approval.PlanRevision != 2 || approval.Revision != m.Revision {
				t.Fatalf("wrong execute seal: %+v", approval)
			}
		})
	}
}

func TestAccountExecutionApprovalRequiresCurrentProposal(t *testing.T) {
	s := openTestStore(t)
	snapshotStoreAccount(t, s)
	m := snapshotMission(t, s, "approval")
	m.State = "awaiting_approval"
	m.Plan = &domain.Plan{Revision: 2}
	m.Approvals = []domain.Approval{{Kind: "plan_proposal", Digest: "old"}, {Kind: "plan_proposal", Digest: "current"}}
	m, err := s.CreateMission(m)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(m)
	events, _ := s.Events(0, 100)
	for _, digest := range []string{"", "old", "unapproved"} {
		if _, err = s.ApproveMissionExecution(m.ID, m.Revision, digest); err == nil {
			t.Fatalf("approved %q", digest)
		}
	}
	if _, err = s.ApproveMissionExecution(m.ID, m.Revision-1, "current"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale approval=%v", err)
	}
	got, _ := s.GetMission(m.ID)
	after, _ := json.Marshal(got)
	afterEvents, _ := s.Events(0, 100)
	if string(before) != string(after) || len(events) != len(afterEvents) {
		t.Fatal("invalid proposal changed mission/events")
	}
}
