package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"time"

	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/store"
)

type acceptanceManifest struct {
	Label        string            `json:"label"`
	URL          string            `json:"url"`
	Root         string            `json:"root"`
	Repo         string            `json:"repo"`
	FixturePath  string            `json:"fixture_path"`
	Scenarios    map[string]string `json:"scenarios"`
	BrowserPaths map[string]string `json:"browser_paths"`
	C4Readiness  string            `json:"c4_readiness"`
}

func (r *acceptanceRuntime) manifest() acceptanceManifest {
	scenarios := make(map[string]string, len(r.scenarios))
	for name, id := range r.scenarios {
		scenarios[name] = id
	}
	return acceptanceManifest{
		Label:       "SYNTHETIC ACCEPTANCE ONLY: actual owned loopback SIWC/model protocols; synthetic repository/runner; no cloud, Docker, or real-account validation",
		URL:         r.URL,
		Root:        r.Root,
		Repo:        r.Repo,
		FixturePath: acceptanceFixturePath,
		Scenarios:   scenarios,
		C4Readiness: "not_checked",
		BrowserPaths: map[string]string{
			"fixture":             "Open /acceptance-fixture/ from the printed root URL",
			"model_settings":      "Settings → Models",
			"role_model_policy":   "Settings → Models → Role policy and recommendations",
			"mission_plan":        "Projects → select scenario title → Plan",
			"mission_invocations": "Projects → select scenario title → Activity",
			"repository_import":   "Project assets → SYNTHETIC C3 local import → registered repository → create → explicit inspection; source registry is metadata only",
			"recovery_diagnostic": "Open C3 draft or isolated corrupt-evidence mission → Overview → candidate/local operations → manual recovery diagnosis",
			"interrupted_import":  "Create task → recent import records (read only); synthetic-c3-no-mission is reserved only",
			"c4_baseline_pending": "Pending decisions → SYNTHETIC C4 baseline_pending → existing decision area: approve baseline, then explicitly execute; separate context, plan and delivery approvals follow",
			"c4_cleanup_failure":  "Pending decisions → SYNTHETIC C4 cleanup_failure → inspect infrastructure/cleanup failure; explicit exact-object cleanup only, never re-execution or a passing result",
		},
	}
}

func allRoleBindings(defaultProfile string, overrides map[domain.Role]string) domain.Settings {
	bindings := map[domain.Role]string{}
	for _, role := range []domain.Role{domain.Main, domain.Researcher, domain.Planner, domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
		bindings[role] = defaultProfile
	}
	for role, profile := range overrides {
		bindings[role] = profile
	}
	return domain.Settings{DefaultProfileID: defaultProfile, RoleBindings: bindings}
}

func createSyntheticAccount(service *accounts.Service, name, provider, baseURL, auth, billing string) (domain.ModelAccount, error) {
	return service.Save(domain.ModelAccount{Name: name, Provider: provider, BaseURL: baseURL, AuthMethod: auth, BillingMode: billing, Enabled: true}, 0)
}

func createSyntheticRoute(s *store.Store, account domain.ModelAccount, id, adapter, protocol, spend, extra string) (domain.ModelRoute, error) {
	return s.CreateModelRoute(domain.ModelRoute{ID: id, AccountID: account.ID, Adapter: adapter, Protocol: protocol, BaseURL: account.BaseURL, Environment: "simulation", SpendClass: spend, ExtraUsage: extra})
}

func putSyntheticProfile(s *store.Store, id, name, protocol, baseURL, modelID, accountID, routeID string) error {
	return s.PutProfile(domain.Profile{ID: id, Name: name, Kind: "compatible", Protocol: protocol, BaseURL: baseURL, Model: modelID, AccountID: accountID, RouteID: routeID})
}

func waitSyntheticMission(s *store.Store, id string, states ...string) (domain.Mission, error) {
	wanted := map[string]bool{}
	for _, state := range states {
		wanted[state] = true
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		mission, err := s.GetMission(id)
		if err != nil {
			return domain.Mission{}, err
		}
		if wanted[mission.State] {
			return mission, nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	mission, _ := s.GetMission(id)
	return mission, fmt.Errorf("synthetic mission %s did not reach %v (state=%s error=%s)", id, states, mission.State, mission.Error)
}

func createProtocolMission(r *acceptanceRuntime, title, profileID string, settings domain.Settings, plan bool) (domain.Mission, error) {
	if err := r.store.PutSettings(settings); err != nil {
		return domain.Mission{}, err
	}
	mission, err := r.engine.Create(context.Background(), engine.CreateRequest{
		Title:    title,
		Goal:     "Use only the disposable synthetic repository and owned local protocol fixtures",
		RepoPath: r.Repo, WorkspaceMode: domain.WorkspaceModeGenerationsV1,
		Spec: domain.Spec{Scope: []string{"src/"}, Acceptance: []string{"test -f /workspace/src/main.txt"}, Image: "synthetic:stub", ProfileID: profileID, MaxCalls: 16},
	})
	if err != nil || !plan {
		return mission, err
	}
	if _, err = r.engine.Plan(context.Background(), mission.ID, mission.Revision); err != nil {
		return mission, err
	}
	return waitSyntheticMission(r.store, mission.ID, "awaiting_approval", "blocked")
}

func seedProtocolScenarios(r *acceptanceRuntime, service *accounts.Service) error {
	ctx := context.Background()
	primary, err := createSyntheticAccount(service, "SYNTHETIC SIWC subscription", "openai", r.protocol.modelBaseURL(), "openai_siwc", "subscription")
	if err != nil {
		return err
	}
	if err = completeFixtureAuthorization(ctx, r.siwc, primary, "approve"); err != nil {
		return fmt.Errorf("seed signed synthetic authorization: %w", err)
	}
	primaryRoute, err := createSyntheticRoute(r.store, primary, "synthetic-siwc-route", "openai_siwc_responses", "responses", "subscription_only", "off")
	if err != nil {
		return err
	}

	roleProfiles := map[domain.Role]string{}
	for _, role := range []domain.Role{domain.Main, domain.Researcher, domain.Planner, domain.Worker, domain.Debugger, domain.QA, domain.Auditor} {
		id := "synthetic-siwc-success-" + string(role)
		if err = putSyntheticProfile(r.store, id, "SYNTHETIC actual local HTTP "+string(role), "responses", primary.BaseURL, id, primary.ID, primaryRoute.ID); err != nil {
			return err
		}
		roleProfiles[role] = id
	}
	successSettings := domain.Settings{DefaultProfileID: roleProfiles[domain.Main], RoleBindings: roleProfiles}

	faultProfiles := []struct{ scenario, modelID string }{
		{"quota_exhausted", "synthetic-quota-exhausted"},
		{"permission_denied", "synthetic-permission-denied"},
		{"temporary_provider_error", "synthetic-temporary-provider-error"},
		{"sse_outcome_unknown", "synthetic-sse-outcome-unknown"},
	}
	for _, fault := range faultProfiles {
		scenario, modelID := fault.scenario, fault.modelID
		profileID := modelID + "-profile"
		if err = putSyntheticProfile(r.store, profileID, "SYNTHETIC "+scenario, "responses", primary.BaseURL, modelID, primary.ID, primaryRoute.ID); err != nil {
			return err
		}
		settings := domain.Settings{DefaultProfileID: roleProfiles[domain.Main], RoleBindings: map[domain.Role]string{}}
		for role, id := range roleProfiles {
			settings.RoleBindings[role] = id
		}
		settings.RoleBindings[domain.Planner] = profileID
		mission, createErr := createProtocolMission(r, "SYNTHETIC protocol: "+scenario, roleProfiles[domain.Main], settings, true)
		if createErr != nil {
			return createErr
		}
		if mission.State != "blocked" {
			return fmt.Errorf("fault scenario %s unexpectedly reached %s", scenario, mission.State)
		}
		r.scenarios[scenario] = mission.ID
	}

	identity, err := createSyntheticAccount(service, "SYNTHETIC identity-only SIWC", "openai", r.protocol.modelBaseURL(), "openai_siwc", "subscription")
	if err != nil {
		return err
	}
	if err = completeFixtureAuthorization(ctx, r.siwc, identity, "identity"); err != nil {
		return fmt.Errorf("seed identity-only authorization: %w", err)
	}
	identityRoute, err := createSyntheticRoute(r.store, identity, "synthetic-identity-route", "openai_siwc_responses", "responses", "subscription_only", "off")
	if err != nil {
		return err
	}
	identityProfile := "synthetic-identity-only-profile"
	if err = putSyntheticProfile(r.store, identityProfile, "SYNTHETIC identity only (inference denied)", "responses", identity.BaseURL, "synthetic-identity-only", identity.ID, identityRoute.ID); err != nil {
		return err
	}
	identityMission, err := createProtocolMission(r, "SYNTHETIC protocol: identity-only inference denied", identityProfile, allRoleBindings(identityProfile, nil), false)
	if err != nil {
		return err
	}
	if _, planErr := r.engine.Plan(ctx, identityMission.ID, identityMission.Revision); planErr == nil || !strings.Contains(planErr.Error(), "entitlement") {
		return fmt.Errorf("identity-only inference did not fail closed: %v", planErr)
	}
	r.scenarios["identity_only_denied"] = identityMission.ID

	explicit, err := createSyntheticAccount(service, "SYNTHETIC explicit API", "openai", r.protocol.modelBaseURL(), "api_key", "api_credits")
	if err != nil {
		return err
	}
	if _, err = service.AddCredential(ctx, explicit.ID, explicit.Revision, "synthetic-explicit-key", r.protocol.explicitKey); err != nil {
		return err
	}
	explicit, err = r.store.GetModelAccount(explicit.ID)
	if err != nil {
		return err
	}
	explicitRoute, err := createSyntheticRoute(r.store, explicit, "synthetic-explicit-route", "openai_api_chat", "chat_completions", "explicit_api", "unknown")
	if err != nil {
		return err
	}
	explicitProfile := "synthetic-explicit-directory-profile"
	if err = putSyntheticProfile(r.store, explicitProfile, "SYNTHETIC explicit API directory (probe only)", "chat_completions", explicit.BaseURL, "synthetic-explicit-model", explicit.ID, explicitRoute.ID); err != nil {
		return err
	}
	r.scenarios["explicit_directory_probe"] = explicitProfile

	kimi, err := createSyntheticAccount(service, "SYNTHETIC Kimi interactive package", "kimi", r.protocol.kimiBaseURL(), "api_key", "subscription")
	if err != nil {
		return err
	}
	if _, err = service.AddCredential(ctx, kimi.ID, kimi.Revision, "synthetic-kimi-key", r.protocol.kimiKey); err != nil {
		return err
	}
	kimi, err = r.store.GetModelAccount(kimi.ID)
	if err != nil {
		return err
	}
	kimiRoute, err := createSyntheticRoute(r.store, kimi, "synthetic-kimi-package-route", "kimi_code_chat", "chat_completions", "interactive_package", "off")
	if err != nil {
		return err
	}
	if err = putSyntheticProfile(r.store, "synthetic-kimi-package-profile", "SYNTHETIC Kimi package (no fallback)", "chat_completions", kimi.BaseURL, "synthetic-kimi-package", kimi.ID, kimiRoute.ID); err != nil {
		return err
	}
	r.scenarios["kimi_package_configuration"] = "synthetic-kimi-package-profile"

	revokedAccount, err := createSyntheticAccount(service, "SYNTHETIC confirmed local revoke", "openai", r.protocol.modelBaseURL(), "openai_siwc", "subscription")
	if err != nil {
		return err
	}
	if err = completeFixtureAuthorization(ctx, r.siwc, revokedAccount, "approve"); err != nil {
		return fmt.Errorf("seed revocation authorization: %w", err)
	}
	revokedAuthorization, err := r.store.CurrentModelAuthorization(revokedAccount.ID)
	if err != nil {
		return err
	}
	revoked, err := r.siwc.Revoke(ctx, revokedAuthorization.Binding.ID, revokedAccount.Revision)
	if err != nil || revoked.LocalState != "revoked" || revoked.RemoteState != "confirmed" {
		return fmt.Errorf("seed synthetic revoke: %+v %w", revoked, err)
	}
	r.scenarios["authorization_revoked"] = revokedAccount.ID

	for _, scenario := range []string{"authorization_success", "authorization_identity", "authorization_decline"} {
		account, createErr := createSyntheticAccount(service, "SYNTHETIC browser "+scenario, "openai", r.protocol.modelBaseURL(), "openai_siwc", "subscription")
		if createErr != nil {
			return createErr
		}
		r.authAccounts[scenario] = account.ID
		r.scenarios[scenario] = account.ID
	}

	success, err := createProtocolMission(r, "SYNTHETIC protocol: valid completion awaiting plan approval", roleProfiles[domain.Main], successSettings, true)
	if err != nil {
		return err
	}
	if success.State != "awaiting_approval" {
		return fmt.Errorf("successful protocol scenario reached %s: %s (fixture=%s)", success.State, success.Error, r.protocol.invalidRequest())
	}
	r.scenarios["plan_success"] = success.ID
	return r.store.PutSettings(successSettings)
}

type acceptanceHandler struct {
	runtime *acceptanceRuntime
	base    http.Handler
	host    string
}

func (h acceptanceHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path != acceptanceFixturePath && request.URL.Path != acceptanceFixtureAuthPath {
		h.base.ServeHTTP(w, request)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'")
	if request.Host != h.host {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if request.URL.Path == acceptanceFixturePath {
		h.fixturePage(w, request)
		return
	}
	h.startAuthorization(w, request)
}

func (h acceptanceHandler) fixturePage(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || request.URL.RawQuery != "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, `<!doctype html><html><head><title>SYNTHETIC acceptance fixture</title></head><body><main><h1>SYNTHETIC local acceptance fixture</h1><p>This uses fake identities, fake credentials, an owned loopback issuer, and an owned loopback model service. It performs no real account or cloud-provider validation.</p>`)
	for _, scenario := range []string{"authorization_success", "authorization_identity", "authorization_decline"} {
		_, _ = fmt.Fprintf(w, `<form method="post" action="%s?scenario=%s"><button>%s</button></form>`, acceptanceFixtureAuthPath, scenario, html.EscapeString(strings.ReplaceAll(scenario, "_", " ")))
	}
	_, _ = io.WriteString(w, `<p>Inference and fault tasks are already placed at safe checkpoints. Open Settings → Models or Projects in the normal workbench UI.</p></main></body></html>`)
}

func (h acceptanceHandler) startAuthorization(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	values := request.URL.Query()
	if len(values) != 1 || len(values["scenario"]) != 1 {
		http.Error(w, "invalid scenario", http.StatusBadRequest)
		return
	}
	accountID := h.runtime.authAccounts[values.Get("scenario")]
	if accountID == "" {
		http.Error(w, "invalid scenario", http.StatusBadRequest)
		return
	}
	account, err := h.runtime.store.GetModelAccount(accountID)
	if err != nil {
		http.Error(w, "scenario unavailable", http.StatusConflict)
		return
	}
	flow, err := h.runtime.siwc.Begin(request.Context(), account.ID, account.Revision)
	if err != nil || !literal127URL(flow.HandoffURL, "/auth/start") {
		http.Error(w, "scenario unavailable", http.StatusConflict)
		return
	}
	http.Redirect(w, request, flow.HandoffURL, http.StatusSeeOther)
}
