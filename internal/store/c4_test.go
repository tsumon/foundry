package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"foundry/internal/domain"
)

// Dropping C4 validation must not turn unsupported/partial declarations into legacy missions.
func TestC4CreateRejectsInvalidDeclarations(t *testing.T) {
	for _, raw := range []string{
		`{"id":"bad","spec":{"c4":{}}}`,
		`{"id":"bad","spec":{"c4":{"schema_version":2,"id":"c4_go_v1"}}}`,
		`{"id":"bad","c4_operations":[{"schema_version":1,"id":"orphan"}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			s := openTestStore(t)
			var m domain.Mission
			if err := json.Unmarshal([]byte(raw), &m); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateMission(m); err == nil {
				t.Fatal("invalid C4 declaration accepted")
			}
		})
	}
}

func c4StoreMission() domain.Mission {
	p := &domain.C4Profile{SchemaVersion: 1, ID: "c4_go_v1", Mode: "synthetic", ImageID: "sha256:" + strings.Repeat("a", 64), Platform: "linux/arm64", DockerPath: "/synthetic/docker", Endpoint: "unix:///synthetic/docker.sock", Verifier: "/opt/foundry/verify-go-v1"}
	m := domain.Mission{ID: "c4", Epoch: 1, BaseCommit: strings.Repeat("b", 40), Spec: domain.Spec{C4: p}}
	o := domain.C4Operation{SchemaVersion: 1, ID: "baseline-1", Kind: "baseline", MissionID: m.ID, Epoch: 1, BaseCommit: m.BaseCommit, BaseDigest: strings.Repeat("c", 64), ProfileDigest: p.ProfileDigest(), ContainerName: "foundry-c4-baseline-1", Owner: "owner-1", State: "prepared", PreparedAt: "2026-10-21T00:00:00Z"}
	o.MaterialDigest = domain.C4MaterialDigest(o)
	m.C4Operations = []domain.C4Operation{o}
	return m
}

func TestC4StoreFrozenSnapshotAndRollback(t *testing.T) {
	for _, kind := range []string{"profile", "version", "identity", "duplicate", "nested_approval", "callback_failure", "terminal_rewrite", "candidate_pointer"} {
		t.Run(kind, func(t *testing.T) {
			s := openTestStore(t)
			input := c4StoreMission()
			if kind == "candidate_pointer" {
				input.Plan = &domain.Plan{Revision: 1}
				input.CandidateGeneration = &domain.GenerationRef{ID: "candidate", Digest: strings.Repeat("d", 64)}
				o := &input.C4Operations[0]
				o.Kind = "candidate"
				o.PlanRevision = 1
				o.CandidateGeneration = input.CandidateGeneration
				o.CandidateDigest = input.CandidateGeneration.Digest
				o.MaterialDigest = domain.C4MaterialDigest(*o)
				input.Approvals = []domain.Approval{{Kind: "execute", PlanRevision: 1, Digest: strings.Repeat("e", 64), Revision: 1, CreatedAt: "2026-10-21T00:00:00Z"}}
			}
			candidateOperations := input.C4Operations
			if kind == "candidate_pointer" {
				input.C4Operations = nil
			}
			m, err := s.CreateMission(input)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "candidate_pointer" {
				m, err = s.UpdateMission(m.ID, m.Revision, "prepare", func(m *domain.Mission) error { m.C4Operations = candidateOperations; return nil })
				if err != nil {
					t.Fatal(err)
				}
			}
			if kind == "nested_approval" || kind == "terminal_rewrite" {
				m, err = s.UpdateMission(m.ID, m.Revision, "approve", func(m *domain.Mission) error {
					o := &m.C4Operations[0]
					o.State = "approved"
					o.Approval = &domain.C4Approval{Digest: o.MaterialDigest, Revision: m.Revision, At: o.PreparedAt}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if kind == "terminal_rewrite" {
				m, err = s.UpdateMission(m.ID, m.Revision, "interrupt", func(m *domain.Mission) error {
					o := &m.C4Operations[0]
					o.State = "finished"
					o.Result = &domain.C4Result{Class: "interrupted", Revision: m.Revision, At: o.PreparedAt}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, _ := json.Marshal(m)
			_, err = s.UpdateMission(m.ID, m.Revision, "tamper", func(m *domain.Mission) error {
				o := &m.C4Operations[0]
				switch kind {
				case "profile":
					m.Spec.C4.Endpoint = "unix:///different.sock"
					o.ProfileDigest = m.Spec.C4.ProfileDigest()
					o.MaterialDigest = domain.C4MaterialDigest(*o)
				case "version":
					m.Spec.C4 = nil
					m.C4Operations = nil
				case "identity":
					o.Owner = "other"
					o.MaterialDigest = domain.C4MaterialDigest(*o)
				case "duplicate":
					m.C4Operations = append(m.C4Operations, *o)
				case "nested_approval":
					o.Approval.At = "2026-10-21T00:00:01Z"
				case "callback_failure":
					m.Title = "changed"
					return errors.New("abort")
				case "terminal_rewrite":
					o.Result.Class = "outcome_unknown"
				case "candidate_pointer":
					o.CandidateGeneration.ID = "different"
					o.MaterialDigest = domain.C4MaterialDigest(*o)
				}
				return nil
			})
			if err == nil {
				t.Fatal("invalid mutation accepted")
			}
			got, err := s.GetMission(m.ID)
			if err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(got)
			if string(raw) != string(after) {
				t.Fatal("failed transaction changed original")
			}
		})
	}
}

func TestC4StoreCASAndDuplicateDispatch(t *testing.T) {
	s := openTestStore(t)
	m, err := s.CreateMission(c4StoreMission())
	if err != nil {
		t.Fatal(err)
	}
	approve := func(m *domain.Mission) error {
		o := &m.C4Operations[0]
		o.State = "approved"
		o.Approval = &domain.C4Approval{Digest: o.MaterialDigest, Revision: m.Revision, At: o.PreparedAt}
		return nil
	}
	old := m.Revision
	m, err = s.UpdateMission(m.ID, m.Revision, "approve", approve)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err := s.UpdateMission(m.ID, old, "stale", func(m *domain.Mission) error { called = true; return nil }); !errors.Is(err, ErrConflict) || called {
		t.Fatal("stale CAS ran mutation", err)
	}
	dispatch := func(m *domain.Mission) error {
		o := &m.C4Operations[0]
		o.State = "dispatched"
		o.Dispatch = &domain.C4Fact{Revision: m.Revision, At: o.PreparedAt}
		return nil
	}
	m, err = s.UpdateMission(m.ID, m.Revision, "dispatch", dispatch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateMission(m.ID, m.Revision, "redispatch", dispatch); err == nil {
		t.Fatal("duplicate dispatch overwrote consumed fact")
	}
	if _, err := s.UpdateMission(m.ID, m.Revision, "reapprove", approve); err == nil {
		t.Fatal("approval reset dispatch")
	}
}

func TestC4StoreCleanupStagesRollbackAtomically(t *testing.T) {
	advance := func(t *testing.T, s *Store, m domain.Mission, stage string) domain.Mission {
		t.Helper()
		next, err := s.UpdateMission(m.ID, m.Revision, "c4."+stage, func(m *domain.Mission) error {
			o := &m.C4Operations[0]
			switch stage {
			case "approve":
				o.State = "approved"
				o.Approval = &domain.C4Approval{Digest: o.MaterialDigest, Revision: m.Revision, At: o.PreparedAt}
			case "dispatch":
				o.State = "dispatched"
				o.Dispatch = &domain.C4Fact{Revision: m.Revision, At: o.PreparedAt}
			case "created":
				o.State = "created"
				o.ContainerID = "container-1"
				o.Created = &domain.C4Fact{Revision: m.Revision, At: o.PreparedAt}
			case "started":
				o.State = "started"
				o.Started = &domain.C4Fact{Revision: m.Revision, At: o.PreparedAt}
			case "result":
				o.State = "finished"
				o.Result = &domain.C4Result{Class: "infrastructure_failed", Revision: m.Revision, At: o.PreparedAt}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return next
	}

	newStarted := func(t *testing.T) (*Store, domain.Mission) {
		t.Helper()
		s := openTestStore(t)
		m, err := s.CreateMission(c4StoreMission())
		if err != nil {
			t.Fatal(err)
		}
		for _, stage := range []string{"approve", "dispatch", "created", "started"} {
			m = advance(t, s, m, stage)
		}
		return s, m
	}

	t.Run("result and cleanup", func(t *testing.T) {
		s, m := newStarted(t)
		before, _ := json.Marshal(m)
		_, err := s.UpdateMission(m.ID, m.Revision, "c4.result-and-cleanup", func(m *domain.Mission) error {
			o := &m.C4Operations[0]
			o.State = "finished"
			o.Result = &domain.C4Result{Class: "infrastructure_failed", Revision: m.Revision, At: o.PreparedAt}
			o.Cleanup = append(o.Cleanup, domain.C4Cleanup{State: "confirmed", ContainerID: o.ContainerID, Owner: o.Owner, Revision: m.Revision, At: o.PreparedAt})
			return nil
		})
		if err == nil {
			t.Fatal("combined result and cleanup mutation committed")
		}
		got, getErr := s.GetMission(m.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		after, _ := json.Marshal(got)
		if string(before) != string(after) {
			t.Fatal("rejected combined mutation changed stored mission")
		}
	})

	t.Run("failed and confirmed cleanup", func(t *testing.T) {
		s, m := newStarted(t)
		m = advance(t, s, m, "result")
		before, _ := json.Marshal(m)
		_, err := s.UpdateMission(m.ID, m.Revision, "c4.double-cleanup", func(m *domain.Mission) error {
			o := &m.C4Operations[0]
			o.Cleanup = append(o.Cleanup,
				domain.C4Cleanup{State: "failed", ContainerID: o.ContainerID, Owner: o.Owner, Revision: m.Revision, At: o.PreparedAt},
				domain.C4Cleanup{State: "confirmed", ContainerID: o.ContainerID, Owner: o.Owner, Revision: m.Revision, At: o.PreparedAt},
			)
			return nil
		})
		if err == nil {
			t.Fatal("multiple cleanup attempts committed together")
		}
		got, getErr := s.GetMission(m.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		after, _ := json.Marshal(got)
		if string(before) != string(after) {
			t.Fatal("rejected multiple cleanup mutation changed stored mission")
		}

		for _, state := range []string{"failed", "confirmed"} {
			m, err = s.UpdateMission(m.ID, m.Revision, "c4.cleanup", func(m *domain.Mission) error {
				o := &m.C4Operations[0]
				o.Cleanup = append(o.Cleanup, domain.C4Cleanup{State: state, ContainerID: o.ContainerID, Owner: o.Owner, Revision: m.Revision, At: o.PreparedAt})
				return nil
			})
			if err != nil {
				t.Fatalf("separate %s cleanup rejected: %v", state, err)
			}
		}
	})
}

func TestC4UnknownCorruptAndPartialRecordsRemainReadOnly(t *testing.T) {
	for _, raw := range []string{
		`{"id":"future","spec":{"c4":{"schema_version":9}}}`,
		`{"id":"future","spec":{"c4":"invalid"}}`,
		`{"id":"future","spec":{"c4":null}}`,
		`{"id":"future","spec":{"c4":{"schema_version":1,"future_policy":true}}}`,
		`{"id":"future","c4_operations":"invalid"}`,
		`{"id":"future","c4_operations":[{"schema_version":1,"future_fact":true}]}`,
	} {
		t.Run(raw, func(t *testing.T) {
			s := openTestStore(t)
			if _, err := s.db.Exec(`INSERT INTO missions(id,revision,state,updated_at,data) VALUES('future',1,'running','now',?)`, raw); err != nil {
				t.Fatal(err)
			}
			m, err := s.GetMission("future")
			if err != nil {
				t.Fatal("corrupt C4 must remain inspectable", err)
			}
			if domain.ValidateC4Mission(m) == nil {
				t.Fatal("corrupt record validated")
			}
			called := false
			_, err = s.UpdateMission("future", 1, "erase", func(m *domain.Mission) error {
				called = true
				m.Spec.C4 = nil
				m.C4Operations = nil
				m.C4Invalid = false
				return nil
			})
			if err == nil || called {
				t.Fatal("invalid record reached mutation", err)
			}
			var got string
			_ = s.db.QueryRow(`SELECT data FROM missions WHERE id='future'`).Scan(&got)
			if got != raw {
				t.Fatal("corrupt bytes rewritten")
			}
		})
	}
}

func TestC4LegacyMissionStaysOmittedAndCannotOptInLater(t *testing.T) {
	s := openTestStore(t)
	m, err := s.CreateMission(domain.Mission{ID: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.UpdateMission(m.ID, m.Revision, "edit", func(m *domain.Mission) error { m.Title = "ordinary"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m)
	if strings.Contains(string(b), "c4") {
		t.Fatal("legacy serialization changed")
	}
	if _, err = s.UpdateMission(m.ID, m.Revision, "upgrade", func(m *domain.Mission) error { m.Spec.C4 = c4StoreMission().Spec.C4; return nil }); err == nil {
		t.Fatal("legacy profile added silently")
	}
}

func TestC4CreateCannotNormalizeStageRevisions(t *testing.T) {
	s := openTestStore(t)
	m := c4StoreMission()
	m.Revision = 20
	o := &m.C4Operations[0]
	o.State = "approved"
	o.Approval = &domain.C4Approval{Digest: o.MaterialDigest, Revision: 20, At: o.PreparedAt}
	if _, err := s.CreateMission(m); err == nil {
		t.Fatal("creation rewrote revision leaving invalid durable facts")
	}
}

func TestC4CreateCannotFabricatePriorConsent(t *testing.T) {
	s := openTestStore(t)
	m := c4StoreMission()
	m.Revision = 1
	o := &m.C4Operations[0]
	o.State = "approved"
	o.Approval = &domain.C4Approval{Digest: o.MaterialDigest, Revision: 1, At: o.PreparedAt}
	if _, err := s.CreateMission(m); err == nil {
		t.Fatal("new mission fabricated prior baseline approval")
	}
}
