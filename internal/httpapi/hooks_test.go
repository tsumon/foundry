package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/hooks"
	"foundry/internal/model"
	"foundry/internal/store"
)

type hookHTTPFixture struct {
	t          *testing.T
	store      *store.Store
	observer   *hooks.Service
	handler    http.Handler
	cookie     *http.Cookie
	controller *engine.Engine
}

func newHookHTTPFixture(t *testing.T) *hookHTTPFixture {
	t.Helper()
	persistent, err := store.Open(filepath.Join(t.TempDir(), "foundry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = persistent.Close() })
	secrets := &memorySecrets{}
	client := model.NewClient(secrets)
	controller := engine.New(persistent, client, engine.Options{DataDir: t.TempDir(), Runner: idleRunner{}})
	t.Cleanup(func() { _ = controller.Close() })
	observer := hooks.New(persistent)
	t.Cleanup(observer.Close)
	f := &hookHTTPFixture{t: t, store: persistent, observer: observer, controller: controller}
	f.handler = New(persistent, controller, client, secrets, Options{Address: "127.0.0.1:4317", Hooks: observer})
	f.authenticate()
	return f
}

func (f *hookHTTPFixture) request(method, target, body string, authenticated bool) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, testOrigin+target, strings.NewReader(body))
	req.Host = "127.0.0.1:4317"
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if authenticated && f.cookie != nil {
		req.AddCookie(f.cookie)
	}
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

func (f *hookHTTPFixture) authenticate() {
	f.t.Helper()
	rr := f.request(http.MethodPost, "/api/session", `{}`, false)
	if rr.Code != http.StatusOK {
		f.t.Fatalf("authenticate: status=%d body=%s", rr.Code, rr.Body.String())
	}
	f.cookie = rr.Result().Cookies()[0]
}

func decodeHookError(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var response struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(response.Error.Message), "sql") || strings.Contains(response.Error.Message, "private") {
		t.Fatalf("unsafe error response=%s", rr.Body.String())
	}
	return response.Error.Code
}

func TestHooksLegacyNewIsNotStartedAndGETDoesNotProcessEvents(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	if _, err := f.store.CreateMission(domain.Mission{ID: "pending", State: "draft"}); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.HookState()
	if err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodGet, "/api/hooks", "", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var response hooks.Snapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Runtime.State != "not_started" || response.Runtime.ErrorCode != "" || len(response.HookPoints) != 13 || response.Rules == nil {
		t.Fatalf("response=%+v", response)
	}
	after, err := f.store.HookState()
	if err != nil {
		t.Fatal(err)
	}
	if after.Cursor != before.Cursor || after.LastProcessedAt != before.LastProcessedAt {
		t.Fatalf("GET processed hooks: before=%+v after=%+v", before, after)
	}
}

func TestHooksAPIUsesAuthenticationOriginStrictJSONAndClosedRoutes(t *testing.T) {
	f := newHookHTTPFixture(t)
	if rr := f.request(http.MethodGet, "/api/hooks", "", false); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d body=%s", rr.Code, rr.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, testOrigin+"/api/hooks", nil)
	req.Host = "127.0.0.1:4317"
	req.Header.Set("Origin", "https://attacker.example")
	req.AddCookie(f.cookie)
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status=%d body=%s", rr.Code, rr.Body.String())
	}

	badJSON := `{"expected_revision":0,"name":"x","enabled":true,"event_kinds":["mission.created"],"mission_id":"","action":"record_activity","unknown":true}`
	if rr = f.request(http.MethodPost, "/api/hooks/rules", badJSON, true); rr.Code != http.StatusBadRequest || decodeHookError(t, rr) != "invalid_json" {
		t.Fatalf("unknown JSON status=%d body=%s", rr.Code, rr.Body.String())
	}
	denied := `{"expected_revision":0,"name":"x","enabled":true,"event_kinds":["mission.created"],"mission_id":"","action":"webhook"}`
	if rr = f.request(http.MethodPost, "/api/hooks/rules", denied, true); rr.Code != http.StatusBadRequest || decodeHookError(t, rr) != "invalid_hook" {
		t.Fatalf("action status=%d body=%s", rr.Code, rr.Body.String())
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/hooks"}, {http.MethodGet, "/api/hooks/rules"},
		{http.MethodPost, "/api/hooks/run"}, {http.MethodPost, "/api/hooks/replay"},
		{http.MethodPost, "/api/hooks/test"}, {http.MethodDelete, "/api/hooks/rules/x"},
		{http.MethodPost, "/api/hooks/scripts"}, {http.MethodPost, "/api/hooks/webhooks"},
	} {
		if rr = f.request(route.method, route.path, `{}`, true); rr.Code != http.StatusNotFound {
			t.Errorf("%s %s status=%d body=%s", route.method, route.path, rr.Code, rr.Body.String())
		}
	}
}

func TestHooksRuleCreateUpdateCASAndSafeErrors(t *testing.T) {
	f := newHookHTTPFixture(t)
	body := `{"expected_revision":0,"name":"Observe creation","enabled":false,"event_kinds":["mission.created"],"mission_id":"","action":"record_activity"}`
	rr := f.request(http.MethodPost, "/api/hooks/rules", body, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", rr.Code, rr.Body.String())
	}
	var created struct {
		Rule domain.HookRule `json:"rule"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(created.Rule.ID) || created.Rule.Revision != 1 {
		t.Fatalf("created=%+v", created.Rule)
	}
	update := `{"id":"` + created.Rule.ID + `","expected_revision":1,"name":"Observe creation v2","enabled":true,"event_kinds":["mission.created"],"mission_id":"","action":"record_activity"}`
	if rr = f.request(http.MethodPost, "/api/hooks/rules", update, true); rr.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rr.Code, rr.Body.String())
	}
	if rr = f.request(http.MethodPost, "/api/hooks/rules", update, true); rr.Code != http.StatusConflict || decodeHookError(t, rr) != "conflict" {
		t.Fatalf("CAS status=%d body=%s", rr.Code, rr.Body.String())
	}
	missing := `{"id":"0123456789abcdef0123456789abcdef","expected_revision":1,"name":"missing","enabled":true,"event_kinds":["mission.created"],"mission_id":"","action":"record_activity"}`
	if rr = f.request(http.MethodPost, "/api/hooks/rules", missing, true); rr.Code != http.StatusNotFound || decodeHookError(t, rr) != "not_found" {
		t.Fatalf("missing status=%d body=%s", rr.Code, rr.Body.String())
	}
	unknownMission := `{"expected_revision":0,"name":"unknown mission","enabled":true,"event_kinds":["mission.created"],"mission_id":"missing","action":"record_activity"}`
	if rr = f.request(http.MethodPost, "/api/hooks/rules", unknownMission, true); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown mission status=%d body=%s", rr.Code, rr.Body.String())
	}

	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	if rr = f.request(http.MethodGet, "/api/hooks", "", true); rr.Code != http.StatusInternalServerError || decodeHookError(t, rr) != "observation_storage_error" {
		t.Fatalf("closed store status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHookRuleJSONRejectsLossyUnicodeButAcceptsValidUnicode(t *testing.T) {
	f := newHookHTTPFixture(t)
	prefix := []byte(`{"expected_revision":0,"name":"`)
	suffix := []byte(`","enabled":false,"event_kinds":["mission.created"],"mission_id":"","action":"record_activity"}`)
	invalidUTF8 := append(append(append([]byte(nil), prefix...), 0xff), suffix...)
	for name, body := range map[string]string{
		"invalid UTF-8":      string(invalidUTF8),
		"unpaired surrogate": `{"expected_revision":0,"name":"\ud800","enabled":false,"event_kinds":["mission.created"],"mission_id":"","action":"record_activity"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rr := f.request(http.MethodPost, "/api/hooks/rules", body, true)
			if rr.Code != http.StatusBadRequest || decodeHookError(t, rr) != "invalid_json" {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
	for _, name := range []string{"合法规则", "literal � remains valid"} {
		body := `{"expected_revision":0,"name":"` + name + `","enabled":false,"event_kinds":["mission.created"],"mission_id":"","action":"record_activity"}`
		rr := f.request(http.MethodPost, "/api/hooks/rules", body, true)
		if rr.Code != http.StatusOK {
			t.Fatalf("valid name %q status=%d body=%s", name, rr.Code, rr.Body.String())
		}
	}
}

func TestHookRuleJSONRejectsExplicitNullScalarFields(t *testing.T) {
	f := newHookHTTPFixture(t)
	valid := `{"expected_revision":0,"name":"null boundary","enabled":false,"event_kinds":["mission.created"],"mission_id":"","action":"record_activity"}`
	for _, replacement := range []struct {
		old, next string
	}{
		{`"expected_revision":0`, `"expected_revision":null`},
		{`"name":"null boundary"`, `"name":null`},
		{`"enabled":false`, `"enabled":null`},
	} {
		body := strings.Replace(valid, replacement.old, replacement.next, 1)
		rr := f.request(http.MethodPost, "/api/hooks/rules", body, true)
		if rr.Code != http.StatusBadRequest || decodeHookError(t, rr) != "invalid_json" {
			t.Errorf("replacement %q status=%d body=%s", replacement.next, rr.Code, rr.Body.String())
		}
	}
}

func TestHookRuleJSONRequiresExactFieldNamesBeforeTypedDecode(t *testing.T) {
	f := newHookHTTPFixture(t)
	for name, body := range map[string]string{
		"uppercase null bool":  `{"expected_revision":0,"name":"case boundary","ENABLED":null,"event_kinds":["mission.created"],"mission_id":"","action":"record_activity"}`,
		"uppercase null int":   `{"EXPECTED_REVISION":null,"name":"case boundary","enabled":false,"event_kinds":["mission.created"],"mission_id":"","action":"record_activity"}`,
		"mixed case null name": `{"expected_revision":0,"NaMe":null,"enabled":false,"event_kinds":["mission.created"],"mission_id":"","action":"record_activity"}`,
		"uppercase valid name": `{"expected_revision":0,"NAME":"case boundary","enabled":false,"event_kinds":["mission.created"],"mission_id":"","action":"record_activity"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rr := f.request(http.MethodPost, "/api/hooks/rules", body, true)
			if rr.Code != http.StatusBadRequest || decodeHookError(t, rr) != "invalid_json" {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestHookRecordsPaginationFilteringAndQueryValidation(t *testing.T) {
	f := newHookHTTPFixture(t)
	mission, err := f.store.CreateMission(domain.Mission{ID: "records", State: "draft"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := f.observer.SaveRule(hooks.RuleInput{Name: "pauses", Enabled: true, EventKinds: []string{"mission.paused"}, MissionID: mission.ID, Action: "record_activity"})
	if err != nil || rule.ID == "" {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		mission, err = f.store.UpdateMission(mission.ID, mission.Revision, "mission.paused", func(current *domain.Mission) error { return nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.store.ProcessHookEvents(context.Background(), 100); err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodGet, "/api/hooks/records?mission_id=records&limit=2", "", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("records status=%d body=%s", rr.Code, rr.Body.String())
	}
	var first hooks.RecordResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Records) != 2 || first.NextBefore <= 0 || first.Records[0].ID <= first.Records[1].ID || first.Runtime.State != "not_started" {
		t.Fatalf("first page=%+v", first)
	}
	rr = f.request(http.MethodGet, "/api/hooks/records?mission_id=records&limit=2&before="+jsonNumber(first.NextBefore), "", true)
	var second hooks.RecordResponse
	if rr.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if len(second.Records) != 1 || second.NextBefore != 0 || second.Records[0].ID >= first.Records[1].ID {
		t.Fatalf("second page=%+v", second)
	}

	for _, query := range []string{
		"?other=x", "?limit=0", "?limit=101", "?limit=x", "?limit=2&limit=3",
		"?before=0", "?before=-1", "?before=x", "?mission_id=a&mission_id=b",
	} {
		rr = f.request(http.MethodGet, "/api/hooks/records"+query, "", true)
		if rr.Code != http.StatusBadRequest || decodeHookError(t, rr) != "invalid_query" {
			t.Errorf("query %q status=%d body=%s", query, rr.Code, rr.Body.String())
		}
	}
	if rr = f.request(http.MethodGet, "/api/hooks/records?mission_id=missing", "", true); rr.Code != http.StatusNotFound {
		t.Fatalf("unknown mission status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func jsonNumber(value int64) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func TestHookHealthCapabilitiesAreTruthful(t *testing.T) {
	f := newHookHTTPFixture(t)
	rr := f.request(http.MethodGet, "/api/health", "", true)
	var response struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", rr.Code, rr.Body.String())
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Capabilities["observation_hooks"] || response.Capabilities["hook_scripts"] || response.Capabilities["hook_webhooks"] || response.Capabilities["git_hooks"] {
		t.Fatalf("capabilities=%+v", response.Capabilities)
	}
}
