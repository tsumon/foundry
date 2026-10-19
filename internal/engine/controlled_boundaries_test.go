package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlledPersistenceFailuresNeverDispatchWithoutClaim(t *testing.T) {
	for _, tc := range []struct {
		name, trigger, wantStatus string
		calls, budget             int
	}{
		{"grant", `CREATE TRIGGER fixture_failure BEFORE INSERT ON read_grants BEGIN SELECT RAISE(ABORT,'fixture grant failure'); END`, "", 0, 0},
		{"reservation", `CREATE TRIGGER fixture_failure BEFORE INSERT ON invocations BEGIN SELECT RAISE(ABORT,'fixture reservation failure'); END`, "", 0, 0},
		{"claim", `CREATE TRIGGER fixture_failure BEFORE UPDATE ON invocations WHEN NEW.status='dispatching' BEGIN SELECT RAISE(ABORT,'fixture claim failure'); END`, "authorized_pending", 0, 1},
		{"finalization", `CREATE TRIGGER fixture_failure BEFORE UPDATE ON invocations WHEN NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'fixture finish failure'); END`, "dispatching", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s, g, _, m := controlledSetup(t, controlledFinal)
			db := fixtureSQL(t, e)
			if _, err := db.Exec(tc.trigger); err != nil {
				t.Fatal(err)
			}
			_, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true)
			if tc.name == "grant" && err == nil {
				t.Fatal("failed grant reported start")
			}
			m = finishControlled(t, e, s, m.ID)
			records, err := s.ListInvocations(m.ID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			if m.State != "blocked" || m.Budget.UsedCalls != tc.budget || len(g.roles) != tc.calls {
				t.Fatal(m.State, m.Error, m.Budget, len(g.roles))
			}
			if tc.wantStatus == "" {
				if len(records) != 0 {
					t.Fatal("rolled back reservation exists")
				}
			} else if len(records) != 1 || records[0].Status != tc.wantStatus || records[0].Result != nil {
				t.Fatal(records)
			}
			if tc.name == "finalization" {
				body := []byte(controlledFinal)
				sum := sha256.Sum256(body)
				orphan := domain.EvidenceEnvelope{
					SchemaVersion: 1,
					Kind:          "model_output",
					InvocationID:  records[0].ID,
					MissionID:     records[0].MissionID,
					Epoch:         records[0].Epoch,
					Producer:      string(records[0].Role),
					CapturedAt:    "2026-10-18T00:00:00Z",
					Digest:        hex.EncodeToString(sum[:]),
					Bytes:         int64(len(body)),
					ContentType:   "application/json",
					BindingDigest: records[0].BindingDigest,
				}
				raw, readErr := e.evidenceStore().Read(orphan)
				if readErr != nil || string(raw) != controlledFinal {
					t.Fatalf("sealed orphan is not independently valid: %q, %v", raw, readErr)
				}
			}
			if _, err = db.Exec(`DROP TRIGGER fixture_failure`); err != nil {
				t.Fatal(err)
			}
			if err = e.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(g.roles) != tc.calls {
				t.Fatal("replayed after persistence failure")
			}
			if tc.name == "finalization" {
				recovered, listErr := s.ListInvocations(m.ID, 0, 100)
				if listErr != nil || len(recovered) != 1 || recovered[0].Status != "outcome_unknown" || recovered[0].Result != nil {
					t.Fatalf("orphan implied success after recovery: %+v, %v", recovered, listErr)
				}
				detail, detailErr := e.InvocationDetail(m.ID, recovered[0].ID)
				if detailErr != nil || detail.ResultPreview != "" || detail.Invocation.Result != nil {
					t.Fatalf("orphan exposed as result: %+v, %v", detail, detailErr)
				}
			}
		})
	}
}

func TestControlledRepositoryPersistenceFailureIsNotProposalDenial(t *testing.T) {
	e, s, _, _, m := controlledSetup(t, controlledRead)
	db := fixtureSQL(t, e)
	if _, err := db.Exec(`CREATE TRIGGER fixture_repository_failure BEFORE INSERT ON invocations WHEN NEW.kind='repository' BEGIN SELECT RAISE(ABORT,'fixture repository persistence failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	if m.State != "blocked" || strings.Contains(m.Error, "proposal denied") {
		t.Fatal("I/O failure relabeled denial", m.State, m.Error)
	}
	calls, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(calls) != 1 || calls[0].Status != "succeeded" {
		t.Fatal(calls, err)
	}
}

func TestControlledOutputSealFailureAndRecoveryBlobCorruption(t *testing.T) {
	e, s, _, _, m := controlledSetup(t)
	e.gateway = controlledGatewayFunc(func(context.Context, model.Request) (model.Result, error) {
		sum := sha256.Sum256([]byte(controlledFinal))
		digest := hex.EncodeToString(sum[:])
		dir := filepath.Join(e.options.DataDir, "evidence", "blobs", digest[:2])
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, digest), []byte("bad"), 0600); err != nil {
			t.Fatal(err)
		}
		return model.Result{Text: controlledFinal}, nil
	})
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	calls, err := s.ListInvocations(m.ID, 0, 100)
	if err != nil || len(calls) != 1 || calls[0].Status != "outcome_unknown" || calls[0].ErrorCode != "result_seal_failed" || calls[0].Result != nil || m.Budget.UsedCalls != 1 {
		t.Fatal(calls, err)
	}
	blob := filepath.Join(e.options.DataDir, "evidence", "blobs", calls[0].Input.Digest[:2], calls[0].Input.Digest)
	if err = os.WriteFile(blob, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = e.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, _ = s.GetMission(m.ID)
	if m.State != "blocked" || !strings.Contains(m.Error, "integrity") {
		t.Fatal(m.State, m.Error)
	}
	if _, err = e.InvocationDetail(m.ID, calls[0].ID); err == nil {
		t.Fatal("corrupt preview trusted")
	}
}

func TestControlledRecoveryIsolatesCorruptLedger(t *testing.T) {
	e, s, _, _, m := controlledSetup(t, controlledRead, controlledFinal, plan)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	calls, _ := s.ListInvocations(m.ID, 0, 100)
	db := fixtureSQL(t, e)
	if _, err := db.Exec(`DROP TRIGGER invocation_immutable_intent`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TRIGGER invocation_transition`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE invocations SET intent='{}' WHERE id=?`, calls[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := e.Recover(context.Background()); err != nil {
		t.Fatal("one corrupt row stopped recovery", err)
	}
	got, err := s.GetMission(m.ID)
	if err != nil || got.State != "blocked" {
		t.Fatal(got, err)
	}
	if _, err = s.ListInvocations(m.ID, 0, 100); err == nil {
		t.Fatal("corrupt ledger looked empty")
	}
	missions, err := s.ListMissions()
	if err != nil || len(missions) != 2 {
		t.Fatal("unrelated mission hidden", err)
	}
	for _, other := range missions {
		if other.ID != m.ID && other.State != "draft" {
			t.Fatal("corruption crossed task ownership")
		}
	}
}

func TestControlledCurrentExecuteAuthorityCheckedBeforeModel(t *testing.T) {
	e, s, g, _, m := controlledSetup(t, controlledRead, controlledFinal, plan)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	// Store intentionally treats non-Researcher digests as opaque; engine must not.
	m, err := s.UpdateMission(m.ID, m.Revision, "fixture.invalid_execution", func(m *domain.Mission) error {
		m.State = "running"
		m.Approvals = append(m.Approvals, domain.Approval{Kind: "execute", Digest: strings.Repeat("a", 64)})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var out editOutput
	if err = e.call(&operation{id: m.ID, epoch: m.Epoch, ctx: context.Background()}, domain.Worker, nil, &out); err == nil {
		t.Fatal("forged execute seal dispatched")
	}
	if len(g.roles) != 3 {
		t.Fatal("authority failure purchased call")
	}
}

func TestControlledMainIdleStatesAndHistoryByteBound(t *testing.T) {
	e, s, _, _, m := controlledSetup(t, controlledRead, controlledFinal, plan)
	if _, err := e.PlanControlled(context.Background(), m.ID, m.Revision, true); err != nil {
		t.Fatal(err)
	}
	m = finishControlled(t, e, s, m.ID)
	var historyBytes []int
	answer := strings.Repeat("\n", 14000)
	raw, _ := json.Marshal(map[string]string{"answer": answer})
	e.gateway = controlledGatewayFunc(func(_ context.Context, r model.Request) (model.Result, error) {
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(r.Prompt), &payload); err != nil {
			t.Fatal(err)
		}
		historyBytes = append(historyBytes, len(payload["history"]))
		return model.Result{Text: string(raw)}, nil
	})
	for _, state := range []string{"paused", "blocked", "delivered"} {
		var err error
		m, err = s.UpdateMission(m.ID, m.Revision, "fixture.state", func(m *domain.Mission) error { m.State = state; return nil })
		if err != nil {
			t.Fatal(err)
		}
		m, err = e.Chat(context.Background(), m.ID, m.Revision, strings.Repeat("q", 8000))
		if err != nil || m.Error != "" {
			t.Fatal(state, m.Error, err)
		}
	}
	if len(historyBytes) != 3 {
		t.Fatal("idle Main denied", historyBytes)
	}
	for _, size := range historyBytes {
		if size > 32<<10 {
			t.Fatal("serialized history exceeds 32KiB", size)
		}
	}
}
