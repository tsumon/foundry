package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestCodingSubscriptionRequiresCatalogSelection(t *testing.T) {
	a := domain.ModelAccount{ID: "a", Revision: 1, Name: "Synthetic", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: "https://api.openai.com/v1"}
	r := domain.ModelRoute{ID: "r", AccountID: "a", Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"}
	p := domain.Profile{ID: "p", AccountID: "a", RouteID: "r", BaseURL: r.BaseURL, Protocol: r.Protocol, Model: "gpt-5.1"}
	profiles := map[domain.Role]domain.Profile{}
	snap := &domain.MissionModelSnapshot{SchemaVersion: 2, Roles: map[domain.Role]domain.RoleModelSelection{}}
	for _, role := range domain.RolePolicyRoles() {
		profiles[role] = p
		snap.Roles[role] = domain.RoleModelSelection{Profile: p, Account: &a, Route: &r}
	}
	if e := newModelC4Profiles(profiles, snap); e == nil {
		t.Fatal("unproven live subscription model enabled a new coding task")
	}
}

func TestCodingNewSubscriptionTaskWithoutCatalogRejectedBeforeEffects(t *testing.T) {
	e, s, request, driver, lease := codingImport(t, "http://127.0.0.1:1/v1")
	a, err := s.SaveModelAccount(domain.ModelAccount{ID: "subscription", Name: "Metadata only", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: domain.OpenAISubscriptionRequestedResource}, 0)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.CreateModelRoute(domain.ModelRoute{ID: "subscription-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	p := domain.Profile{ID: "pre-catalog-profile", Name: "Legacy metadata", Kind: "openai", AccountID: a.ID, RouteID: r.ID, BaseURL: r.BaseURL, Protocol: r.Protocol, Model: "gpt-5.1"}
	if err = s.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	request.Spec.ProfileID = p.ID
	if _, err = e.Create(context.Background(), request); !errors.Is(err, ErrC4Unavailable) {
		t.Fatalf("new live subscription task was not denied by admission before effects: %v", err)
	}
	missions, err := s.ListMissions()
	if err != nil || len(missions) != 0 || lease.reads != 0 || driver.creates != 0 {
		t.Fatal("rejected new task had effects", err)
	}
}

func TestCodingFinalFrozenSelectionRejectsBetweenReadSettingsSwap(t *testing.T) {
	for _, change := range []string{"default", "worker"} {
		t.Run(change, func(t *testing.T) {
			e, s, request, driver, lease := codingImport(t, "http://127.0.0.1:1/v1")
			forbidden := &c4Forbidden{}
			e.gateway, e.options.C4Controlled.models = forbidden, forbidden
			a, err := s.SaveModelAccount(domain.ModelAccount{ID: "subscription", Name: "Metadata only", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: domain.OpenAISubscriptionRequestedResource}, 0)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.CreateModelRoute(domain.ModelRoute{ID: "subscription-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"})
			if err != nil {
				t.Fatal(err)
			}
			p := domain.Profile{ID: "pre-catalog-profile", Name: "Legacy metadata", Kind: "openai", AccountID: a.ID, RouteID: r.ID, BaseURL: r.BaseURL, Protocol: r.Protocol, Model: "gpt-5.1"}
			if err = s.PutProfile(p); err != nil {
				t.Fatal(err)
			}
			settings := domain.Settings{DefaultProfileID: request.Spec.ProfileID, RoleBindings: map[domain.Role]string{}}
			if err = s.PutSettings(settings); err != nil {
				t.Fatal(err)
			}
			request.Spec.ProfileID = ""
			sourceBefore := importSourceDigest(t, request.RepoPath)
			// Split the actual Engine.Create phases deterministically. Its mutex
			// is held just as in production; settings writes use their own Store
			// transaction and are not serialized by this engine lock.
			e.mu.Lock()
			defer e.mu.Unlock()
			if err = e.c4CreateGate(request); err != nil {
				t.Fatalf("initial valid selection denied: %v", err)
			}
			if change == "default" {
				settings.DefaultProfileID = p.ID
			} else {
				settings.RoleBindings[domain.Worker] = p.ID
			}
			if err = s.PutSettings(settings); err != nil {
				t.Fatal(err)
			}
			_, err = e.createRepositoryImport(context.Background(), request)
			if !errors.Is(err, ErrC4Unavailable) {
				t.Fatalf("changed final selection was not denied before import: %v", err)
			}
			missions, err := s.ListMissions()
			if err != nil || len(missions) != 0 {
				t.Fatal("rejected selection created a mission", err)
			}
			imports, err := s.ListRepositoryImportOperations()
			if err != nil || len(imports) != 0 {
				t.Fatal("rejected selection reserved an import", err)
			}
			entries, err := os.ReadDir(e.options.DataDir)
			if err != nil || len(entries) != 0 {
				t.Fatal("rejected selection created import files", err)
			}
			if sourceBefore != importSourceDigest(t, request.RepoPath) || forbidden.calls != 0 || lease.reads != 0 || driver.creates != 0 || driver.starts != 0 || driver.inspects != 0 || driver.removes != 0 {
				t.Fatal("rejected selection caused source, credential, provider or sandbox effects")
			}
		})
	}
}

// This is a persisted pre-catalog metadata fixture, not proof of real account
// or sandbox use. All initial baseline work uses the in-memory synthetic driver.
func TestCodingRecoveryPreservesPersistedPreCatalogSubscriptionHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	e, _, request, driver, lease := codingImportWithStore(t, "http://127.0.0.1:1/v1", s)
	m, err := e.Create(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	m = controlReady(t, e, m)
	m = c4Baseline(t, e, m)
	// Seed the old on-disk snapshot shape directly: today's new-task admission
	// must not be used to manufacture a historical pre-catalog task.
	m.State = "delivered"
	a := domain.ModelAccount{ID: "old-subscription", Revision: 1, Name: "Historical metadata", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true, BaseURL: domain.OpenAISubscriptionRequestedResource}
	r := domain.ModelRoute{ID: "old-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "live", SpendClass: "subscription_only", ExtraUsage: "off"}
	p := domain.Profile{ID: "old-profile", Kind: "openai", AccountID: a.ID, AccountRevision: a.Revision, RouteID: r.ID, BaseURL: r.BaseURL, Protocol: r.Protocol, Model: "gpt-5.1"}
	for _, role := range domain.RolePolicyRoles() {
		m.Profiles[role] = p
		m.ModelSnapshot.Roles[role] = domain.RoleModelSelection{Profile: p, Account: &a, Route: &r}
	}
	m.ModelSnapshot.Digest, err = domain.ModelSnapshotDigest(*m.ModelSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err = domain.ValidateMissionModelSnapshot(m); err != nil {
		t.Fatal(err)
	}
	if err = domain.ValidateC4Mission(m); err != nil {
		t.Fatal(err)
	}
	if err = domain.ValidateCodingTrace(m); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`UPDATE missions SET state=?,data=? WHERE id=?`, m.State, raw, m.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = CheckCodingRecoveryModelSnapshots(reopened); err != nil {
		t.Fatalf("valid pre-catalog history forced diagnostic startup: %v", err)
	}
	forbidden := &c4Forbidden{}
	cap := *e.options.C4Controlled
	cap.config.RuntimeID = "fresh-recovery-runtime"
	cap.models = forbidden
	restarted := New(reopened, forbidden, Options{DataDir: e.options.DataDir, Runner: forbidden, C4Controlled: &cap, CodingWorkbench: true})
	if !restarted.CodingWorkbenchAvailable() {
		t.Fatal("workbench unavailable")
	}
	if err = restarted.CheckC4ControlledRecovery(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = restarted.Recover(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = restarted.Chat(context.Background(), m.ID, m.Revision, "must not invoke"); err == nil {
		t.Fatal("historical completed task gained model authority")
	}
	if err = restarted.AdmitCodingModelCall(context.Background(), model.Request{Role: domain.Main, Profile: p}); err == nil {
		t.Fatal("recovery granted an unclaimed model invocation")
	}
	var after []byte
	if err = db.QueryRow(`SELECT data FROM missions WHERE id=?`, m.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(after) != string(raw) {
		t.Fatal("recovery or denied call rewrote frozen history")
	}
	if forbidden.calls != 0 || lease.reads != 0 || driver.creates != 1 || driver.starts != 1 || driver.removes != 1 {
		t.Fatal("recovery or denied call performed external effects")
	}
}
