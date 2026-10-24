package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"foundry/internal/acceptance"
	"foundry/internal/domain"
	"foundry/internal/store"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Test-only fixture: temp SQLite, synthetic identities, production catalog
// lifecycle with synthetic results. No production bypass or provider adapter.
// The browser can mutate only continuation metadata, never start any other gate.
func continuationBrowserSeed(t *testing.T) (*fixture, map[string]domain.Mission) {
	t.Helper()
	f, a, route, _ := operatorHTTPFixture(t)
	a.Name = "SYNTHETIC continuation account — no provider"
	var err error
	a, err = f.store.SaveModelAccount(a, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	catalog := continuationHTTPCatalog(t, f, a, route, "synthetic-original-grant")
	p := domain.Profile{ID: "synthetic-continuation-profile", Name: "SYNTHETIC recorded model", Kind: "direct", Protocol: route.Protocol, BaseURL: route.BaseURL, AccountID: a.ID, RouteID: route.ID, Model: "gpt-5.1", CatalogOperationID: catalog}
	if err = f.store.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutSettings(domain.Settings{DefaultProfileID: p.ID, RoleBindings: map[domain.Role]string{}}); err != nil {
		t.Fatal(err)
	}
	def, profiles, snapshot, err := f.store.ResolveMissionModels("")
	if err != nil {
		t.Fatal(err)
	}
	modes := []string{"ready", "pending", "stale", "applied", "old-approval-denied", "malformed-history"}
	missions := map[string]domain.Mission{}
	for _, mode := range modes {
		m, e := f.store.CreateMission(domain.Mission{ID: "synthetic-continuation-" + mode, Title: "SYNTHETIC continuation / " + mode, Goal: "Local fixture only: preserve unknown 1/7 budget and frozen history. No real task, repository, model, native credential or VM access.", RepoPath: filepath.Join(f.dataDir, "synthetic-not-opened"), BaseCommit: strings.Repeat("a", 40), State: "planning", Stage: "plan", Epoch: 1, CodingVersion: 1, WorkspaceMode: "generations_account_snapshot_v2", Spec: domain.Spec{ProfileID: def, MaxCalls: 7}, Profiles: profiles, ModelSnapshot: snapshot, Budget: domain.Budget{MaxCalls: 7}, Approvals: []domain.Approval{{Kind: "context", Digest: strings.Repeat("b", 64), Revision: 1, CreatedAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)}}})
		if e != nil {
			t.Fatal(e)
		}
		// Reserve then recover an uncompleted synthetic dispatch. This consumes
		// exactly one ordinal without performing a model call or reading a secret.
		_, invocation, e := f.store.BeginInvocation(continuationBrowserIntent(t, m, "synthetic-unknown-"+mode), m.Revision)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = f.store.ClaimInvocation(invocation.ID, invocation.Epoch); e != nil {
			t.Fatal(e)
		}
		if e = f.store.RecoverInvocations(m.ID); e != nil {
			t.Fatal(e)
		}
		m, e = f.store.GetMission(m.ID)
		if e != nil {
			t.Fatal(e)
		}
		m, e = f.store.UpdateMission(m.ID, m.Revision, "fixture.blocked", func(m *domain.Mission) error { m.State = "blocked"; return nil })
		if e != nil {
			t.Fatal(e)
		}
		missions[mode] = m
	}
	catalog = continuationHTTPCatalog(t, f, a, route, "synthetic-replacement-grant")
	for _, mode := range modes[1:] {
		m := missions[mode]
		if mode == "malformed-history" {
			missions[mode] = corruptContinuationBrowserHistory(t, f, m)
			continue
		}
		m, err = f.store.PrepareModelContinuation(m.ID, m.Revision, "synthetic-replacement-grant", catalog, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		c := m.ModelContinuations[0]
		if mode == "stale" {
			m, err = f.store.UpdateMission(m.ID, m.Revision, "fixture.stale", func(m *domain.Mission) error {
				m.Error = "SYNTHETIC material stale: task revision changed; no retry"
				return nil
			})
		} else if mode == "applied" || mode == "old-approval-denied" {
			m, err = f.store.ApproveModelContinuation(m.ID, c.ID, m.Revision, c.Digest, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			m, err = f.store.ApplyModelContinuation(m.ID, c.ID, m.Revision, c.Digest, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "old-approval-denied" {
				m, err = f.store.UpdateMission(m.ID, m.Revision, "fixture.old_context_check", func(m *domain.Mission) error { m.State = "planning"; return nil })
				if err != nil {
					t.Fatal(err)
				}
				// Current grant/catalog metadata and the effective snapshot must pass;
				// only the retained pre-continuation context approval is stale.
				if err = f.store.CheckModelCall(m.ID, domain.Planner); err != nil {
					t.Fatal("effective selection unavailable before old approval check", err)
				}
				before, err := f.store.ListInvocations(m.ID, 0, 30)
				if err != nil {
					t.Fatal(err)
				}
				intent := continuationBrowserIntent(t, m, "synthetic-denied-old-approval")
				if len(m.Approvals) != 1 || intent.AuthorityDigest != m.Approvals[0].Digest || domain.ApprovalPostdatesModelContinuation(m, m.Approvals[0]) {
					t.Fatal("old context authority not retained")
				}
				if _, _, e := f.store.BeginInvocation(intent, m.Revision); !errors.Is(e, store.ErrConflict) {
					t.Fatal("old context approval did not fail closed with conflict", e)
				}
				after, err := f.store.GetMission(m.ID)
				if err != nil {
					t.Fatal(err)
				}
				ledger, err := f.store.ListInvocations(m.ID, 0, 30)
				if err != nil {
					t.Fatal(err)
				}
				beforeJSON, _ := json.Marshal(before)
				afterJSON, _ := json.Marshal(ledger)
				if after.Revision != m.Revision || after.Budget != m.Budget || !bytes.Equal(beforeJSON, afterJSON) {
					t.Fatal("old approval denial changed task budget or invocation ledger")
				}
				m, err = f.store.UpdateMission(m.ID, m.Revision, "fixture.old_context_denied", func(m *domain.Mission) error {
					m.State = "blocked"
					m.Error = "SYNTHETIC old context approval denied before dispatch; new effective-selection context approval required. Budget remains 1/7, usage unknown."
					return nil
				})
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		missions[mode] = m
	}
	s := f.handler.(*server)
	s.codingWorkbench = true
	s.desktopAccounts = acceptance.NewDesktopCoordinator(f.store, acceptance.DesktopOptions{SIWC: s.siwc, Resolver: s.accounts, Probe: func() error {
		t.Error("fixture attempted native diagnostic")
		return errors.New("synthetic fixture forbids native diagnostic")
	}})
	t.Cleanup(func() { _ = s.desktopAccounts.Close() })
	return f, missions
}

func continuationBrowserIntent(t *testing.T, m domain.Mission, id string) domain.Invocation {
	t.Helper()
	snapshot, err := domain.EffectiveModelSnapshot(m)
	if err != nil || snapshot == nil {
		t.Fatal("fixture effective snapshot unavailable", err)
	}
	sum := sha256.Sum256([]byte(`{}`))
	return domain.Invocation{ID: id, MissionID: m.ID, Epoch: m.Epoch, SessionID: "synthetic-session", Kind: "model", Role: domain.Planner, Name: "model.complete", BindingDigest: snapshot.Digest, AuthorityDigest: strings.Repeat("b", 64), Input: domain.EvidenceEnvelope{SchemaVersion: 1, Kind: "model_input", InvocationID: id, MissionID: m.ID, Epoch: m.Epoch, Producer: "controller", CapturedAt: time.Now().UTC().Format(time.RFC3339Nano), Digest: hex.EncodeToString(sum[:]), Bytes: 2, ContentType: "application/json", BindingDigest: snapshot.Digest}}
}

func continuationBrowserHandler(s *server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No login, catalog, model, repository, readiness, VM or desktop prepare
		// mutation is reachable, even if another workbench screen is opened.
		continuation := strings.HasPrefix(r.URL.Path, "/api/missions/synthetic-continuation-") && strings.Contains(r.URL.Path, "/model-continuations")
		if r.Method != "GET" && r.Method != "HEAD" && !(r.Method == "POST" && (r.URL.Path == "/api/session" || continuation)) {
			writeError(w, 403, "synthetic_fixture_closed", "SYNTHETIC continuation metadata only; all other mutations disabled")
			return
		}
		if r.Method == "GET" && r.URL.Path == "/" {
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, r)
			for key, values := range rr.Header() {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.Header().Del("Content-Length")
			w.WriteHeader(rr.Code)
			fmt.Fprint(w, strings.Replace(rr.Body.String(), "<body>", "<body><p role=note>SYNTHETIC LOCAL CONTINUATION FIXTURE — temporary SQLite; fake identities/catalog. No real sign-in, provider, Keychain, model, VM or Docker. Only continuation metadata mutations are enabled. Open a SYNTHETIC task → 总览.</p>", 1))
			return
		}
		s.ServeHTTP(w, r)
	})
}

func TestModelContinuationBrowserFixtureStates(t *testing.T) {
	f, missions := continuationBrowserSeed(t)
	original, _ := json.Marshal(missions["ready"].ModelSnapshot)
	for _, mode := range []string{"ready", "pending", "stale", "applied", "old-approval-denied", "malformed-history"} {
		m := missions[mode]
		if m.Budget.UsedCalls != 1 || m.Budget.MaxCalls != 7 || !m.Budget.UsageUnknown {
			t.Fatal("fixture changed unknown budget", mode)
		}
		raw, _ := json.Marshal(m.ModelSnapshot)
		if !bytes.Equal(raw, original) {
			t.Fatal("fixture rewrote original snapshot", mode)
		}
		rr := f.request("GET", "/api/missions/"+m.ID, "", true)
		if rr.Code != 200 {
			t.Fatal(mode, rr.Code, rr.Body.String())
		}
		var envelope struct {
			Mission SafeMissionView `json:"mission"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"pending": "prepared", "stale": "stale", "applied": "applied", "old-approval-denied": "applied"}[mode]
		if want != "" && (len(envelope.Mission.ModelContinuations) != 1 || envelope.Mission.ModelContinuations[0].State != want) {
			t.Fatal("wrong mode", mode, rr.Body.String())
		}
		if strings.Contains(rr.Body.String(), "private-subject") || strings.Contains(rr.Body.String(), "private-client") {
			t.Fatal("private identity in task DTO")
		}
		candidates := f.request("GET", "/api/missions/"+m.ID+"/model-continuations/candidates", "", true)
		if mode == "malformed-history" {
			if candidates.Code != 409 || envelope.Mission.ModelSnapshotStatus != "valid" || envelope.Mission.ModelContinuationStatus != "invalid_or_unsupported" || envelope.Mission.EffectiveModelSnapshot != nil || strings.Contains(rr.Body.String(), "private-malformed-history") {
				t.Fatal("malformed fixture did not retain history while blocking authority", rr.Body.String(), candidates.Body.String())
			}
		} else if candidates.Code != 200 || !strings.Contains(candidates.Body.String(), `"id":"synthetic-replacement-grant"`) || strings.Contains(candidates.Body.String(), "subject") || strings.Contains(candidates.Body.String(), "client_id") {
			t.Fatal("fixture candidate projection missing or private", candidates.Body.String())
		}
		invocations, err := f.store.ListInvocations(m.ID, 0, 30)
		if err != nil || len(invocations) != 1 || invocations[0].Status != "outcome_unknown" || invocations[0].Ordinal != 1 {
			t.Fatal("fixture invocation history changed", mode, err)
		}
		if invocations[0].BindingDigest != m.ModelSnapshot.Digest || invocations[0].Input.BindingDigest != m.ModelSnapshot.Digest {
			t.Fatal("prior unknown invocation lost original snapshot binding", mode)
		}
		if mode == "old-approval-denied" {
			effective, e := domain.EffectiveModelSnapshot(m)
			if e != nil {
				t.Fatal(e)
			}
			intent := continuationBrowserIntent(t, m, "synthetic-denied-old-approval")
			if intent.BindingDigest != effective.Digest || intent.Input.BindingDigest != effective.Digest || effective.Digest == m.ModelSnapshot.Digest {
				t.Fatal("old-approval fixture tests original snapshot mismatch instead of stale approval")
			}
			if len(m.Approvals) != 1 || intent.AuthorityDigest != m.Approvals[0].Digest || domain.ApprovalPostdatesModelContinuation(m, m.Approvals[0]) {
				t.Fatal("old-approval fixture did not retain exact old context authority")
			}
		}
	}
	rr := f.request("GET", "/api/model-accounts/production/desktop-preparation", "", true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"model":"gpt-5.1"`) {
		t.Fatal("local catalog unavailable", rr.Body.String())
	}
	s := f.handler.(*server)
	f.handler = continuationBrowserHandler(s)
	for _, path := range []string{"/api/model-authorizations/start", "/api/model-accounts/production/desktop-preparation", "/api/missions/synthetic-continuation-ready/plan"} {
		if rr := f.request("POST", path, `{}`, true); rr.Code != 403 {
			t.Fatal("fixture enabled outside mutation", path)
		}
	}
}

func TestModelContinuationBrowserFixture(t *testing.T) {
	if os.Getenv("FOUNDRY_TEST_CONTINUATION_BROWSER") != "1" {
		t.Skip("explicit synthetic browser fixture only")
	}
	f, missions := continuationBrowserSeed(t)
	s := f.handler.(*server)
	var err error
	s.webDir, err = filepath.Abs("../../web/dist")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(s.webDir, "index.html")); err != nil {
		t.Fatal("build actual web/dist first", err)
	}
	app := httptest.NewServer(continuationBrowserHandler(s)) // loopback only
	defer app.Close()
	s.host = strings.TrimPrefix(app.URL, "http://")
	s.origin = app.URL
	ids := map[string]string{}
	for mode, m := range missions {
		ids[mode] = m.ID
	}
	details, _ := json.Marshal(map[string]any{"url": app.URL, "missions": ids, "acceptance": "SYNTHETIC local fixture only; no real provider/native/model/VM acceptance"})
	fmt.Println("CONTINUATION_BROWSER_FIXTURE " + string(details))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	select {
	case <-ctx.Done():
	case <-time.After(25 * time.Minute):
	}
}
