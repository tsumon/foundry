package store

import (
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"reflect"
	"testing"
)

func TestModelSnapshotFormatFenceRejectsUnwrappedAccountFields(t *testing.T) {
	s := openTestStore(t)
	for _, raw := range []string{
		`{"id":"raw-account","profiles":{"worker":{"id":"p","account_id":"a"}}}`,
		`{"id":"raw-version","profiles":{"worker":{"id":"p","credential_version":1}}}`,
		`{"id":"missing","workspace_mode":"account_snapshot_v1"}`,
		`{"id":"future","workspace_mode":"account_snapshot_v1","model_snapshot":{"schema_version":2,"roles":{},"digest":"bad"}}`,
	} {
		var m domain.Mission
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateMission(m); err == nil {
			t.Errorf("accepted invalid format %s", m.ID)
		}
	}
	events, err := s.Events(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("rejected creation emitted %d events", len(events))
	}
}

func snapshotStoreAccount(t *testing.T, s *Store) domain.ModelAccount {
	t.Helper()
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "account", Name: "Synthetic", Provider: "custom", BaseURL: "https://example.com", AuthMethod: "api_key", BillingMode: "unknown", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	a = snapshotVersion(t, s, a, "one")
	if err = s.PutProfile(domain.Profile{ID: "bound", AccountID: a.ID, Name: "Synthetic", Protocol: "responses", Model: "fake", BaseURL: a.BaseURL}); err != nil {
		t.Fatal(err)
	}
	if err = s.PutSettings(domain.Settings{DefaultProfileID: "bound"}); err != nil {
		t.Fatal(err)
	}
	return a
}
func snapshotVersion(t *testing.T, s *Store, a domain.ModelAccount, op string) domain.ModelAccount {
	t.Helper()
	_, err := s.BeginModelCredential(domain.ModelCredentialVersion{AccountID: a.ID, OperationID: op, SecretRef: "foundry-model-account-fake-" + op}, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishModelCredential(op, "available"); err != nil {
		t.Fatal(err)
	}
	a, err = s.GetModelAccount(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func snapshotMission(t *testing.T, s *Store, id string) domain.Mission {
	t.Helper()
	def, profiles, snapshot, err := s.ResolveMissionModels("")
	if err != nil {
		t.Fatal(err)
	}
	return domain.Mission{ID: id, WorkspaceMode: "account_snapshot_v1", Spec: domain.Spec{ProfileID: def}, Profiles: profiles, ModelSnapshot: snapshot, State: "planning", Epoch: 1, Budget: domain.Budget{MaxCalls: 16}}
}
func TestModelSnapshotFreezesExactVersionsAndRejectsDrift(t *testing.T) {
	s := openTestStore(t)
	a := snapshotStoreAccount(t, s)
	first := snapshotMission(t, s, "first")
	m, err := s.CreateMission(first)
	if err != nil {
		t.Fatal(err)
	}
	stale := snapshotMission(t, s, "stale")
	a = snapshotVersion(t, s, a, "two")
	if _, err = s.CreateMission(stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale create=%v", err)
	}
	next := snapshotMission(t, s, "next")
	if next.Profiles[domain.Worker].CredentialVersion != 2 {
		t.Fatal("new snapshot missed rotation")
	}
	historical, err := s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if historical.Profiles[domain.Worker].CredentialVersion != 1 {
		t.Fatal("historical snapshot followed current")
	}
	if _, err = s.ReserveModelCall(m.ID, m.Revision, domain.Worker); err != nil {
		t.Fatalf("old available version blocked by new current: %v", err)
	}
	for _, mutate := range []func(*domain.Mission){func(m *domain.Mission) { m.ModelSnapshot = nil }, func(m *domain.Mission) { m.ModelSnapshot.SchemaVersion = 2 }, func(m *domain.Mission) {
		p := m.Profiles[domain.Worker]
		p.Model = "changed"
		m.Profiles[domain.Worker] = p
	}} {
		current, _ := s.GetMission(m.ID)
		_, err = s.UpdateMission(m.ID, current.Revision, "tamper", func(m *domain.Mission) error { mutate(m); return nil })
		if err == nil {
			t.Fatal("snapshot mutation allowed")
		}
	}
	a.Enabled = false
	if _, err = s.SaveModelAccount(a, a.Revision); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetMission(m.ID)
	events, _ := s.Events(0, 100)
	if _, err = s.ReserveModelCall(m.ID, before.Revision, domain.Worker); !errors.Is(err, ErrModelAccountUnavailable) {
		t.Fatalf("disabled reserve=%v", err)
	}
	after, _ := s.GetMission(m.ID)
	afterEvents, _ := s.Events(0, 100)
	if !reflect.DeepEqual(before, after) || len(events) != len(afterEvents) {
		t.Fatal("denied reserve changed budget or events")
	}
	if _, err = s.UpdateMission(m.ID, after.Revision, "pause", func(m *domain.Mission) error { m.State = "paused"; return nil }); err != nil {
		t.Fatalf("disabled account blocked local state: %v", err)
	}
}

func TestAccountControlledReservationAndClaimUseFrozenAvailability(t *testing.T) {
	s := openTestStore(t)
	a := snapshotStoreAccount(t, s)
	legacy, binding := controlledStoreMission(t, s, "binding-template")
	m := snapshotMission(t, s, "controlled-account")
	m.WorkspaceMode = "generations_controlled_read_account_snapshot_v1"
	m.ToolMode = domain.ToolModeControlledReadV1
	m.TaskBindings = legacy.TaskBindings
	m, err := s.CreateMission(m)
	if err != nil {
		t.Fatal(err)
	}
	m, grant := controlledGrant(t, s, m, binding)
	v := modelInvocation("account-call", m, grant, binding)
	m, v, err = s.BeginInvocation(v, m.Revision)
	if err != nil {
		t.Fatalf("valid account reservation: %v", err)
	}
	if _, err = s.RevokeModelCredential(a.ID, 1, a.Revision); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetMission(m.ID)
	events, _ := s.Events(0, 100)
	if _, err = s.ClaimInvocation(v.ID, v.Epoch); !errors.Is(err, ErrModelAccountUnavailable) {
		t.Fatalf("revoked claim=%v", err)
	}
	pending, _ := s.GetInvocation(v.ID)
	after, _ := s.GetMission(m.ID)
	afterEvents, _ := s.Events(0, 100)
	if pending.Status != "authorized_pending" || !reflect.DeepEqual(before, after) || len(events) != len(afterEvents) {
		t.Fatal("denied claim changed authority or budget")
	}
	next := modelInvocation("next-account-call", after, grant, binding)
	if _, _, err = s.BeginInvocation(next, after.Revision); !errors.Is(err, ErrModelAccountUnavailable) {
		t.Fatalf("revoked reserve=%v", err)
	}
}

func TestAccountMalformedSnapshotRecoveryDoesNotRewriteLedger(t *testing.T) {
	s := openTestStore(t)
	snapshotStoreAccount(t, s)
	legacy, binding := controlledStoreMission(t, s, "recovery-template")
	m := snapshotMission(t, s, "recovery-account")
	m.WorkspaceMode = "generations_controlled_read_account_snapshot_v1"
	m.ToolMode = domain.ToolModeControlledReadV1
	m.TaskBindings = legacy.TaskBindings
	m, err := s.CreateMission(m)
	if err != nil {
		t.Fatal(err)
	}
	m, g := controlledGrant(t, s, m, binding)
	m, v, err := s.BeginInvocation(modelInvocation("pending-account", m, g, binding), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE missions SET data=json_set(data,'$.model_snapshot.schema_version',2) WHERE id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetMission(m.ID)
	events, _ := s.Events(0, 100)
	if err = s.RecoverInvocations(m.ID); !errors.Is(err, domain.ErrInvalidModelSnapshot) {
		t.Fatalf("recovery=%v", err)
	}
	after, _ := s.GetMission(m.ID)
	pending, _ := s.GetInvocation(v.ID)
	afterEvents, _ := s.Events(0, 100)
	if !reflect.DeepEqual(before, after) || pending.Status != "authorized_pending" || len(events) != len(afterEvents) {
		t.Fatal("corrupt snapshot recovery rewrote ledger")
	}
}

func TestAccountGenericUpdateCannotReserveCalls(t *testing.T) {
	s := openTestStore(t)
	snapshotStoreAccount(t, s)
	m, err := s.CreateMission(snapshotMission(t, s, "generic"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateMission(m.ID, m.Revision, "call.reserved", func(m *domain.Mission) error { m.Budget.UsedCalls++; return nil }); err == nil {
		t.Fatal("generic event bypassed role-aware reservation")
	}
}

func TestAccountMissingCredentialAndMixedRoles(t *testing.T) {
	s := openTestStore(t)
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "empty", Name: "Synthetic empty", Provider: "custom", BaseURL: "https://example.com", AuthMethod: "api_key", BillingMode: "unknown", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	legacy := domain.Profile{ID: "legacy", Name: "legacy", Model: "fake", Protocol: "responses", BaseURL: a.BaseURL}
	bound := legacy
	bound.ID = "bound"
	bound.AccountID = a.ID
	for _, p := range []domain.Profile{legacy, bound} {
		if err = s.PutProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.PutSettings(domain.Settings{DefaultProfileID: "legacy", RoleBindings: map[domain.Role]string{domain.Worker: "bound"}}); err != nil {
		t.Fatal(err)
	}
	m := snapshotMission(t, s, "mixed")
	if len(m.ModelSnapshot.Roles) != 7 || m.Profiles[domain.Worker].CredentialVersion != 0 || m.ModelSnapshot.Roles[domain.Main].Account != nil {
		t.Fatal("mixed snapshot missing exact selections")
	}
	m, err = s.CreateMission(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveModelCall(m.ID, m.Revision, domain.Worker); !errors.Is(err, ErrModelAccountUnavailable) {
		t.Fatalf("missing credential allowed: %v", err)
	}
	if err = s.PutSettings(domain.Settings{DefaultProfileID: "bound"}); err != nil {
		t.Fatal(err)
	}
	persisted, _ := s.GetMission(m.ID)
	if persisted.Profiles[domain.Main].AccountID != "" {
		t.Fatal("historical task followed settings")
	}
}

func TestAccountCreateRevalidatesProfileAccountAndSettings(t *testing.T) {
	for _, kind := range []string{"profile", "account", "roles"} {
		t.Run(kind, func(t *testing.T) {
			s := openTestStore(t)
			a := snapshotStoreAccount(t, s)
			m := snapshotMission(t, s, "drift")
			switch kind {
			case "profile":
				p, _ := s.GetProfile("bound")
				p.Model = "new-model"
				if err := s.PutProfile(p); err != nil {
					t.Fatal(err)
				}
			case "account":
				a.Name = "new-name"
				if _, err := s.SaveModelAccount(a, a.Revision); err != nil {
					t.Fatal(err)
				}
			case "roles":
				p, _ := s.GetProfile("bound")
				p.ID = "other"
				if err := s.PutProfile(p); err != nil {
					t.Fatal(err)
				}
				if err := s.PutSettings(domain.Settings{DefaultProfileID: "bound", RoleBindings: map[domain.Role]string{domain.Worker: "other"}}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.CreateMission(m); !errors.Is(err, ErrConflict) {
				t.Fatalf("torn %s=%v", kind, err)
			}
			events, _ := s.Events(0, 100)
			if len(events) != 0 {
				t.Fatal("conflict left an event")
			}
		})
	}
}

func TestAccountResearchAndPlannerReservationsAndClaimsFailClosed(t *testing.T) {
	for _, phase := range []string{"research-reserve", "research-claim", "planner-reserve", "planner-claim"} {
		t.Run(phase, func(t *testing.T) {
			s, base, g := researchFixture(t)
			a := snapshotStoreAccount(t, s)
			selection := snapshotMission(t, s, "account-research")
			m := base
			m.ID = selection.ID
			m.WorkspaceMode = "generations_controlled_mcp_read_account_snapshot_v1"
			m.Profiles = selection.Profiles
			m.ModelSnapshot = selection.ModelSnapshot
			m.Spec.ProfileID = selection.Spec.ProfileID
			m, err := s.CreateMission(m)
			if err != nil {
				t.Fatal(err)
			}
			g.ID = "account-research-grant"
			g.MissionID = m.ID
			g.CreatedAt = ""
			m, g, err = s.CreateReadGrant(g, m.Revision)
			if err != nil {
				t.Fatal(err)
			}
			var pending domain.Invocation
			var sourceID string
			if phase != "research-reserve" {
				m, pending, err = s.BeginResearchTurn(researchInput("account-source", m, g), nil, m.Revision)
				if err != nil {
					t.Fatal(err)
				}
				if phase != "research-claim" {
					m, pending = researchFinish(t, s, pending)
					sourceID = pending.ID
					application := domain.ResearchTurnApplication{MissionID: m.ID, Epoch: m.Epoch, SessionID: g.SessionID, SourceInvocationID: pending.ID, SourceResult: *pending.Result, Kind: "final", Research: &domain.Research{Summary: "synthetic final"}}
					m, _, err = s.ApplyResearchTurn(application, nil, m.Revision)
					if err != nil {
						t.Fatal(err)
					}
					if phase == "planner-claim" {
						v := researchInput("account-planner", m, g)
						v.Role = domain.Planner
						v.GrantID = ""
						m, pending, err = s.BeginResearchPlanner(v, sourceID, m.Revision)
						if err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if _, err = s.RevokeModelCredential(a.ID, 1, a.Revision); err != nil {
				t.Fatal(err)
			}
			before, _ := s.GetMission(m.ID)
			events, _ := s.Events(0, 100)
			switch phase {
			case "research-reserve":
				_, _, err = s.BeginResearchTurn(researchInput("denied-source", m, g), nil, m.Revision)
			case "planner-reserve":
				v := researchInput("denied-planner", m, g)
				v.Role = domain.Planner
				v.GrantID = ""
				_, _, err = s.BeginResearchPlanner(v, sourceID, m.Revision)
			default:
				_, err = s.ClaimInvocation(pending.ID, pending.Epoch)
			}
			if !errors.Is(err, ErrModelAccountUnavailable) {
				t.Fatalf("%s=%v", phase, err)
			}
			after, _ := s.GetMission(m.ID)
			afterEvents, _ := s.Events(0, 100)
			if !reflect.DeepEqual(before, after) || len(events) != len(afterEvents) {
				t.Fatal("denial consumed budget or advanced ledger")
			}
			if phase == "research-claim" || phase == "planner-claim" {
				got, err := s.GetInvocation(pending.ID)
				if err != nil || got.Status != "authorized_pending" {
					t.Fatal("denied claim changed execution authority", err)
				}
			}
		})
	}
}

func TestAccountTypeInvalidSnapshotRemainsReadableButCannotMutate(t *testing.T) {
	s := openTestStore(t)
	for _, raw := range []string{`{"id":"string-snapshot","revision":1,"workspace_mode":"account_snapshot_v1","model_snapshot":"broken"}`, `{"id":"string-schema","revision":1,"workspace_mode":"account_snapshot_v1","model_snapshot":{"schema_version":"future"}}`} {
		var header struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(raw), &header); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO missions VALUES(?,1,'draft','then',?)`, header.ID, raw); err != nil {
			t.Fatal(err)
		}
		m, err := s.GetMission(header.ID)
		if err != nil {
			t.Fatalf("typed snapshot corruption hid mission: %v", err)
		}
		if domain.ValidateMissionModelSnapshot(m) == nil {
			t.Fatal("malformed snapshot executable")
		}
		if _, err = s.UpdateMission(header.ID, 1, "mutate", func(m *domain.Mission) error { m.State = "running"; return nil }); err == nil {
			t.Fatal("malformed snapshot mutated")
		}
		if err = s.RecoverInvocations(header.ID); err == nil {
			t.Fatal("malformed snapshot recovery accepted")
		}
		var after string
		if err = s.db.QueryRow(`SELECT data FROM missions WHERE id=?`, header.ID).Scan(&after); err != nil || after != raw {
			t.Fatal("malformed bytes rewritten", err)
		}
	}
	all, err := s.ListMissions()
	if err != nil || len(all) != 2 {
		t.Fatalf("malformed snapshot hid list: %d %v", len(all), err)
	}
}
