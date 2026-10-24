package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestModelContinuationCreationCoreRejectsSuppliedHistory(t *testing.T) {
	s := &Store{}
	for _, m := range []domain.Mission{
		{ModelContinuationsInvalid: true},
		{ModelContinuations: []domain.ModelContinuation{{ID: "fabricated"}}},
	} {
		if _, err := s.createMission(m, nil, ""); !errors.Is(err, ErrConflict) {
			t.Fatalf("shared creation core did not reject supplied history: %v", err)
		}
	}
}

type continuationLedger interface {
	PrepareModelContinuation(string, int64, string, string, time.Time) (domain.Mission, error)
	ApproveModelContinuation(string, string, int64, string, time.Time) (domain.Mission, error)
	ApplyModelContinuation(string, string, int64, string, time.Time) (domain.Mission, error)
}

func continuationFixture(t *testing.T, active ...bool) (*Store, domain.Mission, string) {
	t.Helper()
	s, a, r, catalog := completedDesktopCatalog(t)
	p := domain.Profile{ID: "continuation-profile", Name: "Synthetic", Kind: "direct", Protocol: r.Protocol, BaseURL: r.BaseURL, AccountID: a.ID, RouteID: r.ID, Model: "gpt-5.1", CatalogOperationID: catalog.ID}
	if err := s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if err := s.PutSettings(domain.Settings{DefaultProfileID: p.ID, RoleBindings: map[domain.Role]string{}}); err != nil {
		t.Fatal(err)
	}
	def, profiles, snapshot, err := s.ResolveMissionModels("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.CreateMission(domain.Mission{ID: "continuation-mission", State: "planning", Epoch: 1, WorkspaceMode: "generations_account_snapshot_v2", CodingVersion: 1, Spec: domain.Spec{ProfileID: def}, Profiles: profiles, ModelSnapshot: snapshot, Budget: domain.Budget{MaxCalls: 7}, Approvals: []domain.Approval{{Kind: "context", Digest: strings.Repeat("b", 64)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, v, err := s.BeginInvocation(inferenceInvocation(m), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	if len(active) == 0 || !active[0] {
		if err = s.RecoverInvocations(m.ID); err != nil {
			t.Fatal(err)
		}
	}
	m, err = s.GetMission(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, err = s.UpdateMission(m.ID, m.Revision, "fixture.blocked", func(m *domain.Mission) error { m.State = "blocked"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	connection := runningProduction(t, s, s, a, r)
	if err = s.ReserveProductionConnectionDispatch(connection, productionAdmission(), time.Now()); err != nil {
		t.Fatal(err)
	}
	old, err := s.GetModelAuthorization("catalog-proof")
	if err != nil {
		t.Fatal(err)
	}
	old.Binding.ID = "replacement-proof"
	if _, err = s.BeginModelAuthorization(a.ID, "replacement-signin", "sign_in", old.Binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommitModelAuthorization("replacement-signin", old.Binding, old.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingModelAuthorizationCleanup()
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range pending {
		if err = s.CompleteModelAuthorizationCleanup(op.ID); err != nil {
			t.Fatal(err)
		}
	}
	connection, err = s.FinishProductionConnection(connection, "succeeded", "completed", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	admission := domain.ProductionAdmission{Generation: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Profile: "openai_siwc_catalog_v1"}
	catalog, err = s.ProposeProductionCatalog(a.ID, domain.ModelAcceptanceProposal{Phase: "catalog", RouteID: r.ID, ExpectedAccountRevision: a.Revision, PrerequisiteID: connection.ID}, admission, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err = s.DecideModelAcceptance(a.ID, catalog.ID, catalog.Revision, catalog.MaterialDigest, "approve", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err = s.ClaimModelAcceptance(a.ID, catalog.ID, catalog.Revision, catalog.MaterialDigest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveProductionCatalogDispatch(catalog, admission, time.Now()); err != nil {
		t.Fatal(err)
	}
	catalog, err = s.FinishProductionCatalog(catalog, "succeeded", "completed", "", []domain.ModelAcceptanceCatalogEntry{{ID: p.Model, DisplayName: "Synthetic"}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return s, m, catalog.ID
}

func TestModelContinuationRejectsStaleAndUnavailableMaterials(t *testing.T) {
	for _, change := range []string{"task_revision", "wrong_digest", "expired", "expired_grant", "account", "pending_signin", "cleanup", "catalog_lineage", "missing_catalog", "old_grant", "active_call", "plan", "changes", "ordinary_append", "ordinary_apply", "ordinary_delete"} {
		t.Run(change, func(t *testing.T) {
			s, m, catalog := continuationFixture(t, change == "active_call")
			if change == "active_call" {
				if _, err := s.PrepareModelContinuation(m.ID, m.Revision, "replacement-proof", catalog, time.Now()); err == nil {
					t.Fatal("active invocation allowed continuation")
				}
				return
			}
			if change == "missing_catalog" || change == "old_grant" {
				auth := "replacement-proof"
				if change == "missing_catalog" {
					catalog = "missing"
				} else {
					auth = "catalog-proof"
				}
				if _, err := s.PrepareModelContinuation(m.ID, m.Revision, auth, catalog, time.Now()); err == nil {
					t.Fatal("invalid selection prepared")
				}
				return
			}
			prepared, err := s.PrepareModelContinuation(m.ID, m.Revision, "replacement-proof", catalog, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			c := prepared.ModelContinuations[0]
			digest := c.Digest
			now := time.Now()
			switch change {
			case "task_revision":
				prepared, err = s.UpdateMission(m.ID, prepared.Revision, "fixture.title", func(m *domain.Mission) error { m.Title = "changed"; return nil })
			case "wrong_digest":
				digest = strings.Repeat("0", 64)
			case "expired":
				now = now.Add(11 * time.Minute)
			case "expired_grant":
				now = now.Add(2 * time.Hour)
			case "account":
				a, e := s.GetModelAccount("production")
				if e != nil {
					t.Fatal(e)
				}
				a.Name = "changed"
				_, err = s.SaveModelAccount(a, a.Revision)
			case "pending_signin":
				_, err = s.BeginModelAuthorization("production", "pending-op", "sign_in", "pending-proof")
			case "cleanup":
				_, err = s.db.Exec(`UPDATE model_authorization_operations SET cleanup_pending=1 WHERE id='replacement-signin'`)
			case "catalog_lineage":
				a, e := s.GetModelAccount("production")
				if e != nil {
					t.Fatal(e)
				}
				r, e := s.GetModelRoute("production-route")
				if e != nil {
					t.Fatal(e)
				}
				_ = runningProduction(t, s, s, a, r)
			case "plan":
				prepared, err = s.UpdateMission(m.ID, prepared.Revision, "fixture.plan", func(m *domain.Mission) error { m.Plan = &domain.Plan{Revision: 1}; return nil })
			case "changes":
				prepared, err = s.UpdateMission(m.ID, prepared.Revision, "fixture.changes", func(m *domain.Mission) error { m.Changes = []domain.Change{{}}; return nil })
			case "ordinary_append", "ordinary_apply", "ordinary_delete":
				_, err = s.UpdateMission(m.ID, prepared.Revision, "model_continuation.applied", func(m *domain.Mission) error {
					switch change {
					case "ordinary_append":
						m.ModelContinuations = append(m.ModelContinuations, c)
					case "ordinary_delete":
						m.ModelContinuations = nil
					default:
						m.ModelContinuations[0].Application = &domain.ModelContinuationFact{Revision: m.Revision + 1, Digest: c.Digest, CreatedAt: now.Format(time.RFC3339Nano)}
					}
					return nil
				})
				if err == nil {
					t.Fatal("ordinary update changed continuation history")
				}
				return
			}
			if err != nil {
				t.Fatal("fixture mutation failed", err)
			}
			if _, err = s.ApproveModelContinuation(m.ID, c.ID, prepared.Revision, digest, now); err == nil {
				t.Fatal("stale or unavailable material approved")
			}
			after, e := s.GetMission(m.ID)
			if e != nil || after.Budget != m.Budget || after.ModelContinuations[0].Approval != nil {
				t.Fatal("rejection mutated authority or budget", e)
			}
		})
	}
}

func TestModelContinuationConcurrentPreparationHasOneWinner(t *testing.T) {
	s, m, catalog := continuationFixture(t)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.PrepareModelContinuation(m.ID, m.Revision, "replacement-proof", catalog, time.Now()); err == nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("concurrent prepare winners", winners.Load())
	}
}

func TestModelContinuationMalformedEnvelopeIsReadableButClosed(t *testing.T) {
	for _, value := range []string{`"future"`, `null`, `[{"schema_version":99}]`, `[{"id":"one","id":"two"}]`} {
		t.Run(value, func(t *testing.T) {
			s, m, _ := continuationFixture(t)
			raw, _ := json.Marshal(m)
			raw = append(raw[:len(raw)-1], []byte(`,"model_continuations":`+value+`}`)...)
			if _, err := s.db.Exec(`UPDATE missions SET data=? WHERE id=?`, raw, m.ID); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetMission(m.ID)
			if err != nil {
				t.Fatal("unreadable historical task", err)
			}
			if _, err = domain.EffectiveModelSnapshot(got); err == nil {
				t.Fatal("malformed continuation granted authority")
			}
			if _, err = s.UpdateMission(m.ID, m.Revision, "repair", func(m *domain.Mission) error { m.Title = "changed"; return nil }); err == nil {
				t.Fatal("malformed history rewritten")
			}
		})
	}
}

func TestModelContinuationAtomicApplyAndReopen(t *testing.T) {
	s, m, catalog := continuationFixture(t)
	prepared, err := s.PrepareModelContinuation(m.ID, m.Revision, "replacement-proof", catalog, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c := prepared.ModelContinuations[0]
	approved, err := s.ApproveModelContinuation(m.ID, c.ID, prepared.Revision, c.Digest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER fail_continuation_event BEFORE INSERT ON events WHEN NEW.kind='model_continuation.applied' BEGIN SELECT RAISE(ABORT,'fixture failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyModelContinuation(m.ID, c.ID, approved.Revision, c.Digest, time.Now()); err == nil {
		t.Fatal("fixture transaction did not fail")
	}
	got, err := s.GetMission(m.ID)
	if err != nil || got.Revision != approved.Revision || got.ModelContinuations[0].Application != nil {
		t.Fatal("partial apply committed", err)
	}
	if _, err = s.db.Exec(`DROP TRIGGER fail_continuation_event`); err != nil {
		t.Fatal(err)
	}
	applied, err := s.ApplyModelContinuation(m.ID, c.ID, approved.Revision, c.Digest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var seq int
	var name, path string
	if err = s.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err = reopened.GetMission(m.ID)
	if err != nil || !domain.SameModelContinuations(applied.ModelContinuations, got.ModelContinuations) || got.Budget != m.Budget {
		t.Fatal("committed apply changed after reopen", err)
	}
	if _, err = reopened.ApplyModelContinuation(m.ID, c.ID, got.Revision, c.Digest, time.Now()); err == nil {
		t.Fatal("lost-response apply replayed")
	}
}

func TestModelContinuationPreservesUnknownBudgetAndFrozenHistory(t *testing.T) {
	// Catches applying replacement authority by rewriting the original snapshot,
	// refunding unknown provider usage, or selecting an unapproved material.
	s, m, catalogID := continuationFixture(t)
	ledger, ok := any(s).(continuationLedger)
	if !ok {
		t.Fatal("dedicated continuation ledger is missing")
	}
	before, _ := json.Marshal(m.ModelSnapshot)
	profiles, _ := json.Marshal(m.Profiles)
	if m.Budget.UsedCalls != 1 || !m.Budget.UsageUnknown {
		t.Fatal("fixture did not retain unknown consumed call")
	}
	prepared, err := ledger.PrepareModelContinuation(m.ID, m.Revision, "replacement-proof", catalogID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var view struct {
		Records []struct {
			ID     string `json:"id"`
			Digest string `json:"digest"`
		} `json:"model_continuations"`
	}
	raw, _ := json.Marshal(prepared)
	if err = json.Unmarshal(raw, &view); err != nil || len(view.Records) != 1 {
		t.Fatalf("missing material: %s %v", raw, err)
	}
	rec := view.Records[0]
	if err = s.CheckModelCall(m.ID, domain.Planner); err == nil {
		t.Fatal("preparation granted authority")
	}
	if _, err = ledger.ApplyModelContinuation(m.ID, rec.ID, prepared.Revision, rec.Digest, time.Now()); err == nil {
		t.Fatal("unapproved material applied")
	}
	approved, err := ledger.ApproveModelContinuation(m.ID, rec.ID, prepared.Revision, rec.Digest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	applied, err := ledger.ApplyModelContinuation(m.ID, rec.ID, approved.Revision, rec.Digest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(applied.ModelSnapshot)
	afterProfiles, _ := json.Marshal(applied.Profiles)
	if !bytes.Equal(before, after) || !bytes.Equal(profiles, afterProfiles) || applied.Budget != m.Budget {
		t.Fatal("continuation rewrote history or budget")
	}
	if err = s.CheckModelCall(m.ID, domain.Planner); err != nil {
		t.Fatal("effective replacement not available", err)
	}
	if _, err = ledger.ApplyModelContinuation(m.ID, rec.ID, applied.Revision, rec.Digest, time.Now()); err == nil {
		t.Fatal("applied twice")
	}
	if err = s.RecoverInvocations(m.ID); err != nil {
		t.Fatal(err)
	}
	historic, err := s.GetInvocation("inference-call")
	if err != nil || historic.Status != "outcome_unknown" {
		t.Fatalf("historic invocation changed: %+v %v", historic, err)
	}
	effective, err := domain.EffectiveModelSnapshot(applied)
	if err != nil || effective.Roles[domain.Researcher].Authorization.ID != "replacement-proof" {
		t.Fatal("wrong effective snapshot", err)
	}
	applied, err = s.UpdateMission(m.ID, applied.Revision, "fixture.planning", func(m *domain.Mission) error { m.State = "planning"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	intent := modelInvocation("stale-context", applied, domain.ReadGrant{AuthorityDigest: strings.Repeat("b", 64)}, applied.ModelSnapshot.Digest)
	intent.Role = domain.Planner
	intent.GrantID = ""
	if _, _, err = s.BeginInvocation(intent, applied.Revision); err == nil {
		t.Fatal("old snapshot and approval dispatched after application")
	}
	intent = modelInvocation("new-context", applied, domain.ReadGrant{AuthorityDigest: strings.Repeat("c", 64)}, effective.Digest)
	intent.Role = domain.Planner
	intent.GrantID = ""
	if _, _, err = s.BeginInvocation(intent, applied.Revision); err == nil {
		t.Fatal("new selection dispatched without fresh approval")
	}
	applied, err = s.UpdateMission(m.ID, applied.Revision, "fixture.context", func(m *domain.Mission) error {
		m.Approvals = append(m.Approvals, domain.Approval{Kind: "context", Digest: strings.Repeat("c", 64), Revision: m.Revision, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, next, err := s.BeginInvocation(intent, applied.Revision)
	if err != nil {
		t.Fatal("fresh effective authority refused", err)
	}
	if next.Ordinal != 2 {
		t.Fatal("invocation ordinal restarted", next.Ordinal)
	}
}
