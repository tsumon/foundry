package store

import (
	"encoding/json"
	"foundry/internal/domain"
	"strings"
	"testing"
)

// Unknown/null controls must not disappear into a writable legacy record.
func TestC4ControlStoredUnknownReadOnly(t *testing.T) {
	for _, raw := range []string{
		`{"id":"c4","c4_controls":null}`,
		`{"id":"c4","c4_controls":[]}`,
		`{"id":"c4","c4_controls":[{"surprise":true}]}`,
	} {
		m, err := decodeMission([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if domain.ValidateC4Mission(m) == nil {
			t.Fatalf("unknown control representation accepted: %s", raw)
		}
	}
}

func c4ControlStoreMission() domain.Mission {
	m := c4StoreMission()
	m.C4Operations = nil
	m.Spec.C4.ControlVersion = 1
	m.Revision = 1
	c := domain.C4Control{SchemaVersion: 1, ID: "readiness-1", Kind: "readiness", MissionID: m.ID, Epoch: m.Epoch, BaseCommit: m.BaseCommit, BaseDigest: strings.Repeat("c", 64), ProfileDigest: m.Spec.C4.ProfileDigest(), ActivationDigest: strings.Repeat("d", 64), ActivationExpiresAt: "2026-10-21T01:00:00Z", SourceKind: "synthetic_c4_control", PreparedRevision: 1, PreparedAt: "2026-10-21T00:00:00Z", ExpiresAt: "2026-10-21T00:10:00Z", State: "prepared"}
	c.MaterialDigest = domain.C4ControlDigest(c)
	m.C4Controls = []domain.C4Control{c}
	return m
}
func TestC4ControlStoreCreateCannotInventConsent(t *testing.T) {
	for _, stage := range []string{"approved", "dispatched", "completed"} {
		t.Run(stage, func(t *testing.T) {
			s := openTestStore(t)
			m := c4ControlStoreMission()
			c := &m.C4Controls[0]
			c.Approval = &domain.C4Approval{Digest: c.MaterialDigest, Revision: 1, At: c.PreparedAt}
			c.State = stage
			if stage != "approved" {
				c.Dispatch = &domain.C4Fact{Revision: 1, At: c.PreparedAt}
			}
			if stage == "completed" {
				c.Result = &domain.C4ControlResult{Status: "ready", Revision: 1, At: c.PreparedAt, ValidUntil: c.ExpiresAt, Fingerprint: strings.Repeat("f", 64)}
			}
			if _, err := s.CreateMission(m); err == nil {
				t.Fatal("fabricated prior consent accepted")
			}
		})
	}
}
func TestC4ControlStoreStagesCASAndRollback(t *testing.T) {
	s := openTestStore(t)
	m, err := s.CreateMission(c4ControlStoreMission())
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"approved", "dispatched", "completed"} {
		before := m
		update := func(m *domain.Mission) error {
			c := &m.C4Controls[0]
			c.State = stage
			switch stage {
			case "approved":
				c.Approval = &domain.C4Approval{Digest: c.MaterialDigest, Revision: m.Revision, At: c.PreparedAt}
			case "dispatched":
				c.Dispatch = &domain.C4Fact{Revision: m.Revision, At: c.PreparedAt}
			case "completed":
				c.Result = &domain.C4ControlResult{Status: "ready", Revision: m.Revision, At: c.PreparedAt, ValidUntil: c.ExpiresAt, Fingerprint: strings.Repeat("f", 64)}
			}
			return nil
		}
		m, err = s.UpdateMission(m.ID, m.Revision, stage, update)
		if err != nil {
			t.Fatal(stage, err)
		}
		called := false
		if _, err := s.UpdateMission(m.ID, before.Revision, "stale", func(*domain.Mission) error { called = true; return nil }); err == nil || called {
			t.Fatal("CAS bypass")
		}
		raw, _ := json.Marshal(m)
		if _, err := s.UpdateMission(m.ID, m.Revision, "replay", update); err == nil {
			t.Fatal("replay accepted", stage)
		}
		got, err := s.GetMission(m.ID)
		if err != nil {
			t.Fatal(err)
		}
		gotRaw, _ := json.Marshal(got)
		if string(raw) != string(gotRaw) {
			t.Fatal("rollback changed history")
		}
	}
}
func TestC4ControlCorruptionDoesNotReachMutationOrRewriteBytes(t *testing.T) {
	original, _ := json.Marshal(c4ControlStoreMission())
	for _, raw := range []string{
		strings.Replace(string(original), `"c4_controls":[`, `"c4_controls":null,"c4_controls":[`, 1),
		strings.Replace(string(original), `"c4_controls":`, `"C4_Controls":`, 1),
		strings.Replace(string(original), `"c4_controls":[`, `"c4_controls":null,"ignored":[`, 1),
		strings.Replace(string(original), `"state":"prepared"`, `"state":"prepared","state":"approved"`, 1),
		strings.Replace(string(original), `"state":"prepared"`, `"state":"prepared","approval":null`, 1),
		strings.Replace(string(original), `"control_version":1`, `"control_version":null`, 1),
		strings.Replace(string(original), `"source_kind":"synthetic_c4_control"`, `"source_kind":"docker_c4_control"`, 1),
	} {
		t.Run(raw, func(t *testing.T) {
			s := openTestStore(t)
			if _, err := s.db.Exec(`INSERT INTO missions(id,revision,state,updated_at,data) VALUES('c4',1,'running','now',?)`, raw); err != nil {
				t.Fatal(err)
			}
			m, err := s.GetMission("c4")
			if err != nil {
				t.Fatal(err)
			}
			if domain.ValidateC4Mission(m) == nil {
				t.Fatal("corrupt history accepted")
			}
			called := false
			if _, err := s.UpdateMission("c4", 1, "erase", func(m *domain.Mission) error { called = true; m.C4Controls = nil; m.C4Invalid = false; return nil }); err == nil || called {
				t.Fatal("corruption reached mutation")
			}
			var stored string
			if err := s.db.QueryRow(`SELECT data FROM missions WHERE id='c4'`).Scan(&stored); err != nil || stored != raw {
				t.Fatal("stored bytes changed", err)
			}
		})
	}
}

func TestC4ControlStoredNullVersionReadOnly(t *testing.T) {
	m := c4StoreMission()
	raw, _ := json.Marshal(m)
	var v map[string]any
	_ = json.Unmarshal(raw, &v)
	p := v["spec"].(map[string]any)["c4"].(map[string]any)
	for _, version := range []any{nil, 0, 2, "1"} {
		p["control_version"] = version
		b, _ := json.Marshal(v)
		got, err := decodeMission(b)
		if err != nil {
			t.Fatal(err)
		}
		if domain.ValidateC4Mission(got) == nil {
			t.Fatalf("invalid version accepted: %v", version)
		}
	}
}
