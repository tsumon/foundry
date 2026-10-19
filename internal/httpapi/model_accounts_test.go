package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/engine"
	"foundry/internal/model"
	"foundry/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyCredentialReplacementIsImmutable(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	p := domain.Profile{ID: "legacy", Name: "legacy", Kind: "compatible", Protocol: "chat_completions", BaseURL: "https://example.com", Model: "test", HasKey: true}
	if err := f.store.PutProfile(p); err != nil {
		t.Fatal(err)
	}
	srv := f.handler.(*server)
	srv.secrets = forbiddenSecrets{t}
	body := `{"id":"legacy","name":"renamed","kind":"compatible","protocol":"chat_completions","base_url":"https://example.com","model":"test","api_key":"new-fake"}`
	rr := f.request(http.MethodPost, "/api/profiles", body, true)
	if rr.Code != 409 || !strings.Contains(rr.Body.String(), "legacy_credential_immutable") {
		t.Fatalf("replacement=%d %s", rr.Code, rr.Body.String())
	}
	rr = f.request(http.MethodPost, "/api/profiles", strings.Replace(body, "new-fake", "", 1), true)
	if rr.Code != 200 {
		t.Fatalf("blank retention=%d %s", rr.Code, rr.Body.String())
	}
}

type forbiddenSecrets struct{ t *testing.T }

func (s forbiddenSecrets) Get(string) (string, error) {
	s.t.Error("unexpected secret Get")
	return "fake-old", nil
}
func (s forbiddenSecrets) Set(string, string) error { s.t.Error("unexpected secret Set"); return nil }
func (s forbiddenSecrets) Delete(string) error      { s.t.Error("unexpected secret Delete"); return nil }

func TestAccountHTTPLifecycleAndStrictInput(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	body := `{"name":"Synthetic","provider":"custom","base_url":"https://example.com","auth_method":"api_key","billing_mode":"unknown"}`
	for _, bad := range []string{strings.Replace(body, `"name":"Synthetic"`, `"name":"Synthetic","name":"duplicate"`, 1), strings.Replace(body, `"name":"Synthetic"`, `"name":"Synthetic","Name":"alias"`, 1), strings.Replace(body, `"name":"Synthetic"`, `"name":"Synthetic","\u006eame":"escaped duplicate"`, 1), strings.Replace(body, `"Synthetic"`, `"\ud800"`, 1), strings.Replace(body, `"unknown"`, `"unknown","enabled":true`, 1)} {
		rr := f.request("POST", "/api/model-accounts", bad, true)
		if rr.Code != 400 {
			t.Fatalf("strict input=%d %s", rr.Code, rr.Body.String())
		}
	}
	rr := f.request("POST", "/api/model-accounts", body, true)
	if rr.Code != 200 {
		t.Fatalf("create=%d %s", rr.Code, rr.Body.String())
	}
	t.Logf("Task3 account DTO: %s", rr.Body.String())
	var result struct {
		Account domain.ModelAccount `json:"account"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	id := result.Account.ID
	rr = f.request("POST", "/api/model-accounts/"+id+"/credentials", `{"expected_revision":1,"operation_id":"synthetic-one","key":"fake-only"}`, true)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), "fake-only") || strings.Contains(rr.Body.String(), "secret_ref") {
		t.Fatalf("credential=%d %s", rr.Code, rr.Body.String())
	}
	t.Logf("Task3 credential operation DTO: %s", rr.Body.String())
	rr = f.request("PUT", "/api/model-accounts/"+id, `{"name":"stale","enabled":false,"expected_revision":1}`, true)
	if rr.Code != 409 {
		t.Fatalf("stale=%d", rr.Code)
	}
	rr = f.request("GET", "/api/model-accounts/"+id, "", true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"identity_status":"unverified"`) || !strings.Contains(rr.Body.String(), `"inference_enabled":false`) || strings.Contains(rr.Body.String(), "secret_ref") {
		t.Fatalf("detail=%d %s", rr.Code, rr.Body.String())
	}
	rr = f.request("GET", "/api/model-accounts/other/operations/synthetic-one", "", true)
	if rr.Code != 404 {
		t.Fatalf("cross account=%d", rr.Code)
	}
	for _, bad := range []string{`{"expected_revision":3,"operation_id":"bad operation","key":"fake-only"}`, `{"expected_revision":999,"operation_id":"stale","key":"fake-only"}`, `{"expected_revision":3,"operation_id":"op","operation_id":"duplicate","key":"fake-only"}`} {
		rr = f.request("POST", "/api/model-accounts/"+id+"/credentials", bad, true)
		if rr.Code != 400 && rr.Code != 409 {
			t.Fatalf("bad credential=%d", rr.Code)
		}
	}
	rr = f.request("GET", "/api/model-accounts/"+id+"/operations/synthetic-one", "", true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"version":1`) {
		t.Fatal("lost-response operation cannot be queried", rr.Body.String())
	}
	rr = f.request("POST", "/api/model-accounts/"+id+"/credentials/01/revoke", `{"expected_revision":3}`, true)
	if rr.Code != 400 {
		t.Fatal("noncanonical version accepted")
	}
	rr = f.request("POST", "/api/model-accounts/"+id+"/credentials/1/revoke", `{"expected_revision":3}`, true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"state":"revoked"`) {
		t.Fatalf("revoke=%d %s", rr.Code, rr.Body.String())
	}
	rr = f.request("GET", "/api/model-accounts/"+id+"/credentials", "", true)
	if rr.Code != 200 || strings.Contains(rr.Body.String(), "fake-only") || strings.Contains(rr.Body.String(), "secret_ref") {
		t.Fatal("unsafe credential history")
	}
}

func TestAccountProfileRejectsInlineKeysAndReturnsSafeSelection(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	a, err := f.handler.(*server).accounts.Save(domain.ModelAccount{Name: "Synthetic", Provider: "custom", BaseURL: "https://example.com", AuthMethod: "api_key", BillingMode: "subscription", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.handler.(*server).secrets = forbiddenSecrets{t}
	body := `{"account_id":"` + a.ID + `","name":"bound","kind":"compatible","protocol":"responses","model":"fake","base_url":"https://example.com"}`
	for _, bad := range []string{strings.TrimSuffix(body, "}") + `,"api_key":"fake-only"}`, strings.Replace(body, "https://example.com", "https://other.example", 1), strings.TrimSuffix(body, "}") + `,"credential_version":3}`} {
		rr := f.request("POST", "/api/profiles", bad, true)
		if rr.Code != 400 {
			t.Fatalf("account profile accepted: %d %s", rr.Code, rr.Body.String())
		}
	}
	rr := f.request("POST", "/api/profiles", body, true)
	if rr.Code != 200 {
		t.Fatalf("profile=%d %s", rr.Code, rr.Body.String())
	}
	t.Logf("Task3 account profile DTO: %s", rr.Body.String())
	var result struct {
		Profile domain.Profile `json:"profile"`
	}
	if err = json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{strings.Replace(body, a.ID, "", 1), strings.Replace(body, "https://example.com", "https://other.example", 1)} {
		changed = strings.TrimSuffix(changed, "}") + `,"id":"` + result.Profile.ID + `"}`
		rr = f.request("POST", "/api/profiles", changed, true)
		if rr.Code != 409 {
			t.Fatalf("binding changed=%d", rr.Code)
		}
	}
	rr = f.request("GET", "/api/profiles", "", true)
	if !strings.Contains(rr.Body.String(), `"identity_status":"unverified"`) || !strings.Contains(rr.Body.String(), `"usage_status":"unknown"`) {
		t.Fatalf("unsafe/incomplete profile DTO: %s", rr.Body.String())
	}
	rr = f.request("POST", "/api/profiles/"+result.Profile.ID+"/probe", `{}`, true)
	if rr.Code != 409 || !strings.Contains(rr.Body.String(), "r5a_account_inference_disabled") {
		t.Fatalf("probe=%d %s", rr.Code, rr.Body.String())
	}
	if err = f.store.PutSettings(domain.Settings{DefaultProfileID: result.Profile.ID}); err != nil {
		t.Fatal(err)
	}
	def, profiles, snapshot, err := f.store.ResolveMissionModels("")
	if err != nil {
		t.Fatal(err)
	}
	m, err := f.store.CreateMission(domain.Mission{ID: "safe-account-mission", WorkspaceMode: "account_snapshot_v1", Profiles: profiles, ModelSnapshot: snapshot, Spec: domain.Spec{ProfileID: def}})
	if err != nil {
		t.Fatal(err)
	}
	a.Name = "new live name"
	if _, err = f.handler.(*server).accounts.Save(a, a.Revision); err != nil {
		t.Fatal(err)
	}
	rr = f.request("GET", "/api/missions/"+m.ID, "", true)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"model_snapshot_status":"valid"`) || !strings.Contains(rr.Body.String(), `"identity_status":"unverified"`) || strings.Contains(rr.Body.String(), "new live name") || strings.Contains(rr.Body.String(), "base_url") {
		t.Fatalf("mission DTO=%d %s", rr.Code, rr.Body.String())
	}
	var taskDTO map[string]json.RawMessage
	if err = json.Unmarshal(rr.Body.Bytes(), &taskDTO); err != nil {
		t.Fatal(err)
	}
	var missionDTO map[string]json.RawMessage
	if err = json.Unmarshal(taskDTO["mission"], &missionDTO); err != nil {
		t.Fatal(err)
	}
	t.Logf("Task3 mission snapshot DTO: %s; status=%s", missionDTO["model_snapshot"], missionDTO["model_snapshot_status"])
	m.ModelSnapshot.SchemaVersion = 2
	view, err := f.handler.(*server).missionView(m)
	if err != nil {
		t.Fatal("invalid snapshot must remain inspectable", err)
	}
	raw, _ := json.Marshal(view)
	if !strings.Contains(string(raw), `"model_snapshot_status":"invalid_or_unsupported"`) || strings.Contains(string(raw), "base_url") {
		t.Fatalf("corrupt safe view: %s", raw)
	}
}

func TestAccountHTTPAuthenticationAndOriginHaveNoSideEffects(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	for _, target := range []string{"/api/model-accounts", "/api/model-accounts/absent", "/api/model-accounts/absent/credentials", "/api/model-accounts/absent/operations/op"} {
		if rr := f.request("GET", target, "", false); rr.Code != 401 {
			t.Fatalf("unauthenticated %s=%d", target, rr.Code)
		}
	}
	for _, tc := range []struct {
		host, origin, content string
		cookie                bool
		want                  int
	}{
		{"127.0.0.1:4317", testOrigin, "application/json", false, 401},
		{"localhost:4317", testOrigin, "application/json", true, 403},
		{"127.0.0.1:4317", "https://attacker.example", "application/json", true, 403},
		{"127.0.0.1:4317", testOrigin, "text/plain", true, 403},
	} {
		r := httptest.NewRequest("POST", testOrigin+"/api/model-accounts", strings.NewReader(`{"name":"denied","provider":"custom","base_url":"https://example.com","auth_method":"api_key","billing_mode":"unknown"}`))
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("Content-Type", tc.content)
		if tc.cookie {
			r.AddCookie(f.cookie)
		}
		rr := httptest.NewRecorder()
		f.handler.ServeHTTP(rr, r)
		if rr.Code != tc.want {
			t.Fatalf("auth=%d want %d", rr.Code, tc.want)
		}
	}
	items, err := f.store.ListModelAccounts()
	if err != nil || len(items) != 0 {
		t.Fatalf("unauthorized writes: %d %v", len(items), err)
	}
	for _, mode := range []string{"account_snapshot_v1", "generations_account_snapshot_v1", "generations_controlled_read_account_snapshot_v1", "generations_controlled_mcp_read_account_snapshot_v1"} {
		rr := f.request("POST", "/api/missions", `{"workspace_mode":"`+mode+`","tool_mode":"controlled_mcp_read_v2"}`, true)
		if rr.Code != 400 {
			t.Fatalf("caller wrapper=%d %s", rr.Code, rr.Body.String())
		}
	}
}

func TestAccountHealthCapabilitiesAreHonest(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	rr := f.request("GET", "/api/health", "", true)
	var result struct {
		Capabilities map[string]bool `json:"capabilities"`
	}
	if rr.Code != 200 || json.Unmarshal(rr.Body.Bytes(), &result) != nil {
		t.Fatal("health unreadable")
	}
	for flag, want := range map[string]bool{"account_management": true, "credential_versions": true, "model_snapshots": true, "provider_login": false, "account_inference": false, "automatic_paid_fallback": false} {
		got, present := result.Capabilities[flag]
		if !present || got != want {
			t.Fatalf("capability %s=%v present %v", flag, got, present)
		}
	}
}

func TestAccountTypeInvalidSnapshotHTTPHistoryRemainsInspectable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	secrets := forbiddenSecrets{t}
	client := model.NewClient(secrets)
	e := engine.New(s, client, engine.Options{DataDir: t.TempDir(), Runner: idleRunner{}})
	defer e.Close()
	f := &fixture{t: t, store: s, handler: New(s, e, client, secrets, Options{Address: "127.0.0.1:4317"})}
	f.authenticate()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, raw := range []string{`{"id":"bad-value","revision":1,"state":"draft","workspace_mode":"account_snapshot_v1","model_snapshot":"broken"}`, `{"id":"bad-schema","revision":1,"state":"planning","workspace_mode":"generations_controlled_mcp_read_account_snapshot_v1","tool_mode":"controlled_mcp_read_v2","model_snapshot":{"schema_version":"future"}}`} {
		var h struct {
			ID string `json:"id"`
		}
		if err = json.Unmarshal([]byte(raw), &h); err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(`INSERT INTO missions VALUES(?,1,'draft','then',?)`, h.ID, raw); err != nil {
			t.Fatal(err)
		}
		rr := f.request("GET", "/api/missions/"+h.ID, "", true)
		if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"model_snapshot_status":"invalid_or_unsupported"`) || strings.Contains(rr.Body.String(), "broken") {
			t.Fatalf("history=%d %s", rr.Code, rr.Body.String())
		}
	}
	rr := f.request("GET", "/api/missions", "", true)
	if rr.Code != 200 || strings.Count(rr.Body.String(), `"model_snapshot_status":"invalid_or_unsupported"`) != 2 {
		t.Fatalf("list=%d %s", rr.Code, rr.Body.String())
	}
	if err = e.Recover(context.Background()); err == nil {
		t.Fatal("malformed recovery accepted")
	}
	rr = f.request("POST", "/api/missions/bad-value/plan", `{"revision":1}`, true)
	if rr.Code != 409 {
		t.Fatalf("malformed plan=%d %s", rr.Code, rr.Body.String())
	}
}
