package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
)

func installRouter(f *fixture, origins ...string) {
	srv := f.handler.(*server)
	router := model.NewRouter(srv.client, srv.accounts, model.RouterOptions{SimulationOrigins: origins})
	f.handler = New(f.store, srv.engine, srv.client, f.secrets, Options{
		Address: "127.0.0.1:4317", Accounts: srv.accounts, ModelRouter: router,
	})
	f.cookie = nil
}

func routeAccount(t *testing.T, f *fixture, provider, auth, billing, baseURL string) domain.ModelAccount {
	t.Helper()
	a, err := f.handler.(*server).accounts.Save(domain.ModelAccount{
		Name: "Synthetic route account", Provider: provider, AuthMethod: auth,
		BillingMode: billing, BaseURL: baseURL, Enabled: true,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestModelRoutesRuntimeContractAndStrictMutationBoundary(t *testing.T) {
	f := newFixture(t)
	installRouter(f, "http://127.0.0.1:9876", "http://localhost:9876")
	f.authenticate()

	if rr := f.request(http.MethodGet, "/api/model-routes", "", false); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated read=%d %s", rr.Code, rr.Body.String())
	}
	rr := f.request(http.MethodGet, "/api/model-routes", "", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("runtime=%d %s", rr.Code, rr.Body.String())
	}
	var runtime struct {
		Routes  []domain.ModelRoute `json:"routes"`
		Runtime struct {
			LiveAuthorization       bool `json:"live_authorization"`
			LiveDiscovery           bool `json:"live_discovery"`
			LiveInference           bool `json:"live_inference"`
			SimulationInference     bool `json:"simulation_inference"`
			SimulationAuthorization bool `json:"simulation_authorization"`
			AutomaticPaidFallback   bool `json:"automatic_paid_fallback"`
			Adapters                []struct {
				ID          string `json:"id"`
				Provider    string `json:"provider"`
				AuthMethod  string `json:"auth_method"`
				Protocol    string `json:"protocol"`
				SpendClass  string `json:"spend_class"`
				Implemented bool   `json:"implemented"`
				LiveEnabled bool   `json:"live_enabled"`
			} `json:"adapters"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &runtime); err != nil {
		t.Fatal(err)
	}
	if runtime.Routes == nil || len(runtime.Routes) != 0 || !runtime.Runtime.SimulationInference || runtime.Runtime.SimulationAuthorization || runtime.Runtime.LiveAuthorization || runtime.Runtime.LiveDiscovery || runtime.Runtime.LiveInference || runtime.Runtime.AutomaticPaidFallback {
		t.Fatalf("unsafe runtime=%+v", runtime)
	}
	if len(runtime.Runtime.Adapters) != 7 {
		t.Fatalf("adapters=%+v", runtime.Runtime.Adapters)
	}
	for _, adapter := range runtime.Runtime.Adapters {
		if adapter.ID == "" || adapter.Provider == "" || adapter.AuthMethod == "" || adapter.Protocol == "" || adapter.SpendClass == "" || !adapter.Implemented || adapter.LiveEnabled {
			t.Fatalf("adapter=%+v", adapter)
		}
	}

	a := routeAccount(t, f, "openai", "api_key", "api_credits", "https://api.openai.com/v1")
	body := `{"account_id":"` + a.ID + `","expected_account_revision":1,"adapter":"openai_api_responses","environment":"live","extra_usage":"off"}`
	for _, bad := range []string{
		strings.Replace(body, `"account_id":"`, `"account_id":"`+a.ID+`","account_id":"`, 1),
		strings.TrimSuffix(body, "}") + `,"protocol":"responses"}`,
		strings.Replace(body, `"expected_account_revision":1`, `"expected_account_revision":2`, 1),
		strings.Replace(body, `"openai_api_responses"`, `"zhipu_api_chat"`, 1),
		strings.Replace(body, `"live"`, `"production"`, 1),
	} {
		rr = f.request(http.MethodPost, "/api/model-routes", bad, true)
		if rr.Code != http.StatusBadRequest && rr.Code != http.StatusConflict {
			t.Fatalf("bad route accepted status=%d body=%s input=%s", rr.Code, rr.Body.String(), bad)
		}
	}
	rr = f.request(http.MethodPost, "/api/model-routes", body, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("create=%d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		Route domain.ModelRoute `json:"route"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Route.ID == "" || created.Route.AccountID != a.ID || created.Route.Adapter != "openai_api_responses" || created.Route.Protocol != "responses" || created.Route.BaseURL != a.BaseURL || created.Route.Environment != "live" || created.Route.SpendClass != "explicit_api" || created.Route.ExtraUsage != "off" || created.Route.CreatedAt == "" {
		t.Fatalf("route=%+v", created.Route)
	}
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if got := f.request(method, "/api/model-routes/"+created.Route.ID, `{}`, true); got.Code != http.StatusNotFound {
			t.Fatalf("mutable route %s=%d %s", method, got.Code, got.Body.String())
		}
	}
}

func TestModelRoutesReuseExactHostOriginSessionAndRecoveryGuards(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a := routeAccount(t, f, "openai", "api_key", "api_credits", "https://api.openai.com/v1")
	body := `{"account_id":"` + a.ID + `","expected_account_revision":1,"adapter":"openai_api_chat","environment":"live","extra_usage":"off"}`
	for _, tc := range []struct{ host, origin string }{
		{host: "localhost:4317", origin: testOrigin},
		{host: "127.0.0.1:4317", origin: "https://attacker.invalid"},
	} {
		req := httptest.NewRequest(http.MethodPost, testOrigin+"/api/model-routes", strings.NewReader(body))
		req.Host = tc.host
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(f.cookie)
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("host=%q origin=%q status=%d", tc.host, tc.origin, rr.Code)
		}
	}

	srv := f.handler.(*server)
	f.handler = New(f.store, srv.engine, srv.client, f.secrets, Options{Address: "127.0.0.1:4317", Accounts: srv.accounts, RecoveryReadOnly: true})
	f.cookie = nil
	f.authenticate()
	if rr := f.request(http.MethodPost, "/api/model-routes", body, true); rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), `"code":"recovery_read_only"`) {
		t.Fatalf("recovery mutation=%d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request(http.MethodGet, "/api/model-routes", "", true); rr.Code != http.StatusOK {
		t.Fatalf("recovery read=%d %s", rr.Code, rr.Body.String())
	}
}

func TestRoutedProfileBindingIsExactImmutableAndStrict(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a := routeAccount(t, f, "anthropic", "api_key", "metered_api", "https://api.anthropic.com/v1")
	route, err := f.store.CreateModelRouteAtAccountRevision(domain.ModelRoute{ID: "anthropic-route", AccountID: a.ID, Adapter: "anthropic_api_messages", Protocol: "messages", BaseURL: a.BaseURL, Environment: "live", SpendClass: "explicit_api", ExtraUsage: "off"}, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"account_id":"` + a.ID + `","route_id":"` + route.ID + `","name":"Bound","kind":"direct","protocol":"messages","base_url":"` + a.BaseURL + `","model":"claude-test","api_key":""}`
	for _, bad := range []string{
		strings.Replace(body, `"route_id":"`, `"route_id":"`+route.ID+`","route_id":"`, 1),
		strings.TrimSuffix(body, "}") + `,"snapshot_schema":2}`,
		strings.Replace(body, `"account_id":"`+a.ID+`"`, `"account_id":""`, 1),
		strings.Replace(body, `"api_key":""`, `"api_key":"fake"`, 1),
		strings.Replace(body, `"protocol":"messages"`, `"protocol":"responses"`, 1),
		strings.Replace(body, a.BaseURL, "https://example.com", 1),
	} {
		rr := f.request(http.MethodPost, "/api/profiles", bad, true)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("bad profile accepted=%d %s input=%s", rr.Code, rr.Body.String(), bad)
		}
	}
	rr := f.request(http.MethodPost, "/api/profiles", body, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("create=%d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		Profile domain.Profile `json:"profile"`
	}
	if err = json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Profile.RouteID != route.ID || created.Profile.Protocol != "messages" || created.Profile.AccountRevision != 0 || created.Profile.CredentialVersion != 0 {
		t.Fatalf("profile=%+v", created.Profile)
	}
	second, err := f.store.CreateModelRouteAtAccountRevision(domain.ModelRoute{ID: "anthropic-route-2", AccountID: a.ID, Adapter: route.Adapter, Protocol: route.Protocol, BaseURL: route.BaseURL, Environment: route.Environment, SpendClass: route.SpendClass, ExtraUsage: route.ExtraUsage}, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(body, route.ID, second.ID, 1)
	changed = strings.TrimSuffix(changed, "}") + `,"id":"` + created.Profile.ID + `"}`
	if rr = f.request(http.MethodPost, "/api/profiles", changed, true); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "route_binding_immutable") {
		t.Fatalf("route changed=%d %s", rr.Code, rr.Body.String())
	}
	if rr = f.request(http.MethodGet, "/api/profiles", "", true); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"route_id":"`+route.ID+`"`) {
		t.Fatalf("list=%d %s", rr.Code, rr.Body.String())
	}
}

func TestExplicitRoutedProfileProbeUsesAtomicSelectionAndPreservesCompatibility(t *testing.T) {
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer fake-probe-key" {
			t.Errorf("request path=%q auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-one","display_name":"Model One"},{"id":"model-two"}]}`))
	}))
	defer provider.Close()

	f := newFixture(t)
	installRouter(f, provider.URL)
	f.authenticate()
	a := routeAccount(t, f, "openai", "api_key", "api_credits", provider.URL)
	if _, err := f.handler.(*server).accounts.AddCredential(context.Background(), a.ID, a.Revision, "probe-key", "fake-probe-key"); err != nil {
		t.Fatal(err)
	}
	a, _ = f.store.GetModelAccount(a.ID)
	route, err := f.store.CreateModelRouteAtAccountRevision(domain.ModelRoute{ID: "probe-route", AccountID: a.ID, Adapter: "openai_api_chat", Protocol: "chat_completions", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "explicit_api", ExtraUsage: "off"}, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutProfile(domain.Profile{ID: "probe-profile", Name: "Probe", Kind: "direct", AccountID: a.ID, RouteID: route.ID, Protocol: route.Protocol, BaseURL: route.BaseURL, Model: "model-one"}); err != nil {
		t.Fatal(err)
	}
	if got := f.request(http.MethodGet, "/api/profiles", "", true); got.Code != http.StatusOK || requests.Load() != 0 {
		t.Fatalf("GET probed provider status=%d requests=%d", got.Code, requests.Load())
	}
	rr := f.request(http.MethodPost, "/api/profiles/probe-profile/probe", `{}`, true)
	if rr.Code != http.StatusOK || requests.Load() != 1 {
		t.Fatalf("probe=%d requests=%d body=%s", rr.Code, requests.Load(), rr.Body.String())
	}
	if rr.Body.String() != `{"catalog":[{"id":"model-one","display_name":"Model One"},{"id":"model-two","display_name":"model-two"}],"inference_tested":false,"models":["model-one","model-two"]}`+"\n" {
		t.Fatalf("contract=%s", rr.Body.String())
	}
}

func TestLiveRoutedProbeStopsBeforeSecretsOrNetworkAndSchema1StaysDenied(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a := routeAccount(t, f, "openai", "api_key", "api_credits", "https://api.openai.com/v1")
	route, err := f.store.CreateModelRouteAtAccountRevision(domain.ModelRoute{ID: "live-route", AccountID: a.ID, Adapter: "openai_api_chat", Protocol: "chat_completions", BaseURL: a.BaseURL, Environment: "live", SpendClass: "explicit_api", ExtraUsage: "off"}, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []domain.Profile{
		{ID: "live-routed", Name: "Live", Kind: "direct", AccountID: a.ID, RouteID: route.ID, Protocol: route.Protocol, BaseURL: route.BaseURL, Model: "gpt-test"},
		{ID: "schema-one", Name: "Old", Kind: "direct", AccountID: a.ID, Protocol: route.Protocol, BaseURL: route.BaseURL, Model: "gpt-test"},
	} {
		if err = f.store.PutProfile(p); err != nil {
			t.Fatal(err)
		}
	}
	srv := f.handler.(*server)
	guard := forbiddenSecrets{t}
	resolver := accounts.New(f.store, guard)
	srv.modelRouter = model.NewRouter(srv.client, resolver, model.RouterOptions{})
	srv.secrets = guard
	if rr := f.request(http.MethodPost, "/api/profiles/live-routed/probe", `{}`, true); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), `"code":"live_provider_access_pending_confirmation"`) {
		t.Fatalf("live probe=%d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request(http.MethodPost, "/api/profiles/schema-one/probe", `{}`, true); rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "r5a_account_inference_disabled") {
		t.Fatalf("schema1 probe=%d %s", rr.Code, rr.Body.String())
	}
}

func TestSchema2MissionViewUsesFrozenRouteAuthorizationAndCurrentRuntimeOnly(t *testing.T) {
	f := newFixture(t)
	installRouter(f, "http://127.0.0.1:9876")
	f.authenticate()
	a := routeAccount(t, f, "openai", "openai_siwc", "subscription", "http://127.0.0.1:9876")
	route, err := f.store.CreateModelRouteAtAccountRevision(domain.ModelRoute{ID: "oauth-view-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"}, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutProfile(domain.Profile{ID: "oauth-view-profile", Name: "OAuth frozen", Kind: "direct", AccountID: a.ID, RouteID: route.ID, Protocol: route.Protocol, BaseURL: route.BaseURL, Model: "gpt-test"}); err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutSettings(domain.Settings{DefaultProfileID: "oauth-view-profile"}); err != nil {
		t.Fatal(err)
	}
	defaultID, profiles, snapshot, err := f.store.ResolveMissionModels("")
	if err != nil {
		t.Fatal(err)
	}
	// Identity may be absent before authorization, but the immutable route must
	// still be visible without reading credentials or probing a provider.
	m, err := f.store.CreateMission(domain.Mission{ID: "schema2-view", WorkspaceMode: "account_snapshot_v2", State: "draft", Spec: domain.Spec{ProfileID: defaultID}, Profiles: profiles, ModelSnapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	a.Name = "Current mutable name"
	if _, err = f.handler.(*server).accounts.Save(a, a.Revision); err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodGet, "/api/missions/"+m.ID, "", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("mission=%d %s", rr.Code, rr.Body.String())
	}
	raw := rr.Body.String()
	for _, expected := range []string{`"route":{"id":"oauth-view-route"`, `"environment":"simulation"`, `"spend_class":"subscription_only"`, `"model_runtime":`, `"simulation_inference":true`, `"live_inference":false`} {
		if !strings.Contains(raw, expected) {
			t.Fatalf("missing %s in %s", expected, raw)
		}
	}
	if strings.Contains(raw, "Current mutable name") || strings.Contains(raw, "secret_ref") || strings.Contains(raw, "access_token") || strings.Contains(raw, "authorization_url") {
		t.Fatalf("mission reread/leaked runtime state: %s", raw)
	}
}

func TestFrozenAuthorizationViewEnumeratesOnlyStableMetadata(t *testing.T) {
	snapshot := &domain.MissionModelSnapshot{SchemaVersion: 2, Roles: map[domain.Role]domain.RoleModelSelection{
		domain.Main: {
			Profile:       domain.Profile{ID: "profile", AccountID: "account", RouteID: "route", Protocol: "responses", BaseURL: "http://127.0.0.1:9876", Model: "gpt-test"},
			Account:       &domain.ModelAccount{ID: "account", Revision: 7, Name: "Frozen", Provider: "openai", AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true},
			Route:         &domain.ModelRoute{ID: "route", AccountID: "account", Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: "http://127.0.0.1:9876", Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off", CreatedAt: "then"},
			Authorization: &domain.ModelAuthorizationBinding{ID: "binding", AccountID: "account", Issuer: "https://auth.openai.com", Subject: "subject", ClientID: "issued-client", SpendClass: "subscription_only", Scopes: []string{"openid", "resource.invoke", "chatgpt.tokens.use.direct"}},
		},
	}}
	raw, err := json.Marshal(modelSnapshotView(snapshot))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, expected := range []string{`"route":{"id":"route"`, `"authorization":{"id":"binding"`, `"issuer":"https://auth.openai.com"`, `"scopes":["openid","resource.invoke","chatgpt.tokens.use.direct"]`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %s in %s", expected, text)
		}
	}
	for _, forbidden := range []string{"subject", "client_id", "issued-client", "secret_ref", "access_token", "refresh_token", "id_token", "generation", "expires_at", "write_claimed"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unsafe authorization field %q in %s", forbidden, text)
		}
	}
}

func TestSchema2OrdinaryModelInvocationPreviewIsAlwaysRedacted(t *testing.T) {
	f := newFixture(t)
	a := routeAccount(t, f, "openai", "openai_siwc", "subscription", "http://127.0.0.1:9876")
	route, err := f.store.CreateModelRouteAtAccountRevision(domain.ModelRoute{ID: "redaction-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", BaseURL: a.BaseURL, Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"}, a.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutProfile(domain.Profile{ID: "redaction-profile", Name: "Redact", Kind: "direct", AccountID: a.ID, RouteID: route.ID, Protocol: route.Protocol, BaseURL: route.BaseURL, Model: "gpt-test"}); err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutSettings(domain.Settings{DefaultProfileID: "redaction-profile"}); err != nil {
		t.Fatal(err)
	}
	defaultID, profiles, snapshot, err := f.store.ResolveMissionModels("")
	if err != nil {
		t.Fatal(err)
	}
	m := domain.Mission{ID: "redaction", WorkspaceMode: "account_snapshot_v2", State: "draft", Spec: domain.Spec{ProfileID: defaultID}, Profiles: profiles, ModelSnapshot: snapshot}
	result := domain.EvidenceEnvelope{Digest: strings.Repeat("a", 64), Bytes: 20}
	detail := domain.InvocationDetail{Invocation: domain.Invocation{ID: "call", MissionID: m.ID, Kind: "model", Status: "outcome_unknown", Result: &result}, InputPreview: "private prompt", ResultPreview: "private model output", InputTruncated: true, ResultTruncated: true}
	raw, err := json.Marshal(invocationView(m, detail))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if strings.Contains(text, "private prompt") || strings.Contains(text, "private model output") || !strings.Contains(text, `"input_redacted":true`) || !strings.Contains(text, `"result_redacted":true`) || !strings.Contains(text, `"status":"outcome_unknown"`) {
		t.Fatalf("unsafe invocation view=%s", text)
	}
}

func TestDiagnosticAccountModelInvocationPreviewFailsClosed(t *testing.T) {
	result := domain.EvidenceEnvelope{Digest: strings.Repeat("a", 64), Bytes: 20}
	accountProfile := domain.Profile{ID: "profile", AccountID: "account", RouteID: "route", Protocol: "responses", BaseURL: "https://api.openai.com/v1", Model: "gpt-test"}
	tests := []struct {
		name    string
		mission domain.Mission
		redact  bool
	}{
		{name: "invalid persisted snapshot diagnostic", mission: domain.Mission{WorkspaceMode: "account_snapshot_v2", ModelSnapshotInvalid: true}, redact: true},
		{name: "missing schema2 snapshot diagnostic", mission: domain.Mission{WorkspaceMode: "account_snapshot_v2"}, redact: true},
		{name: "future snapshot diagnostic", mission: domain.Mission{WorkspaceMode: "account_snapshot_v3", ModelSnapshot: &domain.MissionModelSnapshot{SchemaVersion: 99}}, redact: true},
		{name: "future account wrapper with missing snapshot", mission: domain.Mission{WorkspaceMode: "account_snapshot_v3"}, redact: true},
		{name: "future account wrapper with null snapshot", mission: domain.Mission{WorkspaceMode: "generations_account_snapshot_v3", ModelSnapshot: nil}, redact: true},
		{name: "unsupported workspace diagnostic", mission: domain.Mission{WorkspaceMode: "future_workspace_mode"}, redact: true},
		{name: "account profile marker without wrapper", mission: domain.Mission{Profiles: map[domain.Role]domain.Profile{domain.Main: accountProfile}}, redact: true},
		{name: "legacy ordinary preview remains compatible", mission: domain.Mission{Profiles: map[domain.Role]domain.Profile{domain.Main: {ID: "legacy", Protocol: "responses", BaseURL: "http://127.0.0.1:9", Model: "local"}}}, redact: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			detail := domain.InvocationDetail{
				Invocation:   domain.Invocation{ID: "call", Kind: "model", Status: "outcome_unknown", Result: &result},
				InputPreview: "private prompt", ResultPreview: "private model output",
			}
			raw, err := json.Marshal(invocationView(tc.mission, detail))
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			if tc.redact {
				if strings.Contains(text, "private prompt") || strings.Contains(text, "private model output") || !strings.Contains(text, `"input_redacted":true`) || !strings.Contains(text, `"result_redacted":true`) {
					t.Fatalf("diagnostic preview leaked: %s", text)
				}
			} else if !strings.Contains(text, "private prompt") || !strings.Contains(text, "private model output") || strings.Contains(text, "input_redacted") {
				t.Fatalf("legacy preview changed: %s", text)
			}
		})
	}
}

func TestRedactedInvocationKeepsOnlyFiniteSanitizedReceiptMetadata(t *testing.T) {
	result := domain.EvidenceEnvelope{Digest: strings.Repeat("a", 64), Bytes: 20}
	detail := domain.InvocationDetail{Invocation: domain.Invocation{
		ID: "call", Kind: "model", Status: "failed", ErrorCode: "auth_invalid", Result: &result,
		ModelReceipt: &domain.ModelCallReceipt{
			RequestID: "unsafe<request>", ResolvedModel: "gpt-safe", CredentialGeneration: 7,
			HTTPStatus: 999, ProviderCode: "raw-provider-secret", ProviderParam: "raw-param",
			BodyShape: "raw-shape", RetryAfterSeconds: 999999, Certainty: "raw-certainty",
		},
	}, InputPreview: "private prompt", ResultPreview: "private output"}
	m := domain.Mission{WorkspaceMode: "account_snapshot_v2", ModelSnapshotInvalid: true}
	raw, err := json.Marshal(invocationView(m, detail))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{"unsafe<request>", "raw-provider-secret", "raw-param", "raw-shape", "raw-certainty", "private prompt", "private output", `"http_status":999`, `"retry_after_seconds":999999`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("unsafe receipt field %q in %s", forbidden, text)
		}
	}
	for _, expected := range []string{`"resolved_model":"gpt-safe"`, `"credential_generation":7`, `"certainty":"unknown"`, `"status":"failed"`, `"error_code":"auth_invalid"`, `"input_redacted":true`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing safe receipt field %s in %s", expected, text)
		}
	}
}
