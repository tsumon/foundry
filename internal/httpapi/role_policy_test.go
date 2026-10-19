package httpapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"

	_ "github.com/mattn/go-sqlite3"
)

func TestPolicyMutationCannotBypassLaunchSession(t *testing.T) {
	s := &server{host: "127.0.0.1:4337", origin: "http://127.0.0.1:4337", token: "test-only"}
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4337/api/role-model-policy/proposals", strings.NewReader("{}"))
	request.Header.Set("Origin", "http://127.0.0.1:4337")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", response.Code)
	}
}

func TestRolePolicyRoutesUseExactRequestAndRecoveryGuards(t *testing.T) {
	f := newFixture(t)
	f.authenticate()

	for _, tc := range []struct {
		name, host, origin, contentType string
	}{
		{name: "wrong host", host: "localhost:4317", origin: testOrigin, contentType: "application/json"},
		{name: "wrong origin", host: "127.0.0.1:4317", origin: "https://attacker.example", contentType: "application/json"},
		{name: "non json", host: "127.0.0.1:4317", origin: testOrigin, contentType: "text/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, testOrigin+"/api/role-model-policy/proposals", strings.NewReader(`{}`))
			r.Host = tc.host
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", tc.contentType)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			r.AddCookie(f.cookie)
			rr := httptest.NewRecorder()
			f.handler.ServeHTTP(rr, r)
			if rr.Code != http.StatusForbidden {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}

	recovery := &server{
		recoveryReadOnly: true,
		store:            f.store,
		host:             "127.0.0.1:4317",
		origin:           testOrigin,
		token:            "recovery-token",
	}
	request := func(method, target, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, testOrigin+target, strings.NewReader(body))
		r.Host = "127.0.0.1:4317"
		r.AddCookie(&http.Cookie{Name: sessionName, Value: recovery.token})
		if method != http.MethodGet {
			r.Header.Set("Origin", testOrigin)
			r.Header.Set("Content-Type", "application/json")
		}
		rr := httptest.NewRecorder()
		recovery.ServeHTTP(rr, r)
		return rr
	}
	if rr := request(http.MethodGet, "/api/role-model-policy", ""); rr.Code != http.StatusOK {
		t.Fatalf("recovery read=%d %s", rr.Code, rr.Body.String())
	}
	if rr := request(http.MethodPost, "/api/role-model-policy/proposals", `{}`); rr.Code != http.StatusServiceUnavailable || policyErrorCode(t, rr) != "recovery_read_only" {
		t.Fatalf("recovery mutation=%d %s", rr.Code, rr.Body.String())
	}
}

func TestRolePolicyStrictDTOsRejectUnknownDuplicateAndServerMetadata(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	valid := rolePolicyPutJSON(t, 0, strings.Repeat("0", 64), emptyRolePolicyInput())
	for _, tc := range []struct {
		name, target, body string
	}{
		{name: "unknown top level", target: "/api/role-model-policy", body: strings.TrimSuffix(valid, "}") + `,"extra":true}`},
		{name: "case alias top level", target: "/api/role-model-policy", body: strings.Replace(valid, `"expected_revision":0`, `"Expected_Revision":0`, 1)},
		{name: "case pair top level", target: "/api/role-model-policy", body: strings.Replace(valid, `"expected_revision":0`, `"expected_revision":0,"Expected_Revision":1`, 1)},
		{name: "duplicate top level", target: "/api/role-model-policy", body: strings.Replace(valid, `"expected_revision":0`, `"expected_revision":0,"expected_revision":1`, 1)},
		{name: "case alias nested", target: "/api/role-model-policy", body: strings.Replace(valid, `"preference":"candidate_order"`, `"Preference":"candidate_order"`, 1)},
		{name: "case pair nested", target: "/api/role-model-policy", body: strings.Replace(valid, `"preference":"candidate_order"`, `"preference":"candidate_order","Preference":"subscription_first"`, 1)},
		{name: "duplicate nested", target: "/api/role-model-policy", body: strings.Replace(valid, `"preference":"candidate_order"`, `"preference":"candidate_order","preference":"subscription_first"`, 1)},
		{name: "profile digest injection", target: "/api/role-model-policy", body: rolePolicyInjectedCandidateJSON("profile_digest", `"`+strings.Repeat("a", 64)+`"`)},
		{name: "profile metadata injection", target: "/api/role-model-policy", body: rolePolicyInjectedCandidateJSON("account_id", `"account-private"`)},
		{name: "evidence status injection", target: "/api/role-model-policy", body: rolePolicyInjectedCandidateJSON("evidence", `{"status":"completed"}`)},
		{name: "proposal null", target: "/api/role-model-policy/proposals", body: `null`},
		{name: "proposal unknown", target: "/api/role-model-policy/proposals", body: `{"probe":true}`},
		{name: "proposal duplicate", target: "/api/role-model-policy/proposals", body: `{"x":1,"x":2}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := http.MethodPut
			if strings.Contains(tc.target, "/proposals") {
				method = http.MethodPost
			}
			rr := f.request(method, tc.target, tc.body, true)
			if rr.Code != http.StatusBadRequest || policyErrorCode(t, rr) != "invalid_json" {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}

	if rr := f.request(http.MethodPut, "/api/settings", `{"default_profile_id":"","role_bindings":{},"model_policy_decision_id":"client-forged"}`, true); rr.Code != http.StatusBadRequest {
		t.Fatalf("manual settings accepted provenance=%d %s", rr.Code, rr.Body.String())
	}
}

func TestRolePolicyConfirmationRejectsCaseAliasesWithoutApplying(t *testing.T) {
	for _, tc := range []struct {
		name string
		body func(domain.RolePolicyProposal) string
	}{
		{
			name: "aliases only",
			body: func(proposal domain.RolePolicyProposal) string {
				return `{"Digest":"` + proposal.Digest + `","Confirm":true}`
			},
		},
		{
			name: "canonical false then case alias true",
			body: func(proposal domain.RolePolicyProposal) string {
				return `{"digest":"` + proposal.Digest + `","confirm":false,"Confirm":true}`
			},
		},
		{
			name: "canonical stale digest then case alias current digest",
			body: func(proposal domain.RolePolicyProposal) string {
				return `{"digest":"` + strings.Repeat("0", 64) + `","Digest":"` + proposal.Digest + `","confirm":true}`
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, proposal := prepareConfirmableRolePolicyHTTP(t)
			rr := f.request(http.MethodPost, "/api/role-model-policy/proposals/"+proposal.ID+"/confirm", tc.body(proposal), true)
			if rr.Code != http.StatusBadRequest || policyErrorCode(t, rr) != "invalid_json" {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
			settings, err := f.store.GetSettings()
			if err != nil || settings.ModelPolicyDecisionID != "" || len(settings.RoleBindings) != 0 {
				t.Fatalf("case alias applied settings=%+v err=%v", settings, err)
			}
		})
	}
}

func TestRolePolicyHTTPErrorCodesAreFinite(t *testing.T) {
	f := newFixture(t)
	f.authenticate()

	if rr := f.request(http.MethodPost, "/api/role-model-policy/proposals", `{}`, true); rr.Code != http.StatusConflict || policyErrorCode(t, rr) != "role_policy_incomplete" {
		t.Fatalf("missing policy=%d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request(http.MethodPut, "/api/role-model-policy", `{"expected_revision":0,"profiles_digest":"bad","roles":{}}`, true); rr.Code != http.StatusBadRequest || policyErrorCode(t, rr) != "role_policy_validation" {
		t.Fatalf("validation=%d %s", rr.Code, rr.Body.String())
	}

	state := getRolePolicyHTTPState(t, f)
	body := rolePolicyPutJSON(t, 0, state.ProfilesDigest, emptyRolePolicyInput())
	if rr := f.request(http.MethodPut, "/api/role-model-policy", body, true); rr.Code != http.StatusOK {
		t.Fatalf("save empty=%d %s", rr.Code, rr.Body.String())
	}
	if rr := f.request(http.MethodPut, "/api/role-model-policy", body, true); rr.Code != http.StatusConflict || policyErrorCode(t, rr) != "role_policy_stale" {
		t.Fatalf("stale save=%d %s", rr.Code, rr.Body.String())
	}
	proposal := proposeRolePolicyHTTP(t, f)
	if proposal.Confirmable {
		t.Fatal("empty policy proposal became confirmable")
	}
	for _, tc := range []struct {
		name, body, code string
		want             int
	}{
		{name: "absent consent", body: `{"digest":"` + proposal.Digest + `"}`, want: http.StatusBadRequest, code: "role_policy_validation"},
		{name: "negative consent", body: `{"digest":"` + proposal.Digest + `","confirm":false}`, want: http.StatusBadRequest, code: "role_policy_validation"},
		{name: "incomplete", body: `{"digest":"` + proposal.Digest + `","confirm":true}`, want: http.StatusConflict, code: "role_policy_incomplete"},
		{name: "unknown confirmation field", body: `{"digest":"` + proposal.Digest + `","confirm":true,"decision_id":"forged"}`, want: http.StatusBadRequest, code: "invalid_json"},
		{name: "duplicate confirmation field", body: `{"digest":"` + proposal.Digest + `","digest":"` + proposal.Digest + `","confirm":true}`, want: http.StatusBadRequest, code: "invalid_json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := f.request(http.MethodPost, "/api/role-model-policy/proposals/"+proposal.ID+"/confirm", tc.body, true)
			if rr.Code != tc.want || policyErrorCode(t, rr) != tc.code {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestRolePolicyCorruptStorageReturnsFiniteInternalError(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	db, err := sql.Open("sqlite3", f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const privateStoredDetail = "private rationale and database detail"
	if _, err = db.Exec(`INSERT INTO role_model_policies(revision,created_at,data) VALUES(1,'then',?)`, `{"schema_version":1,"policy":{"rationale":"`+privateStoredDetail+`"}}`); err != nil {
		t.Fatal(err)
	}
	rr := f.request(http.MethodGet, "/api/role-model-policy", "", true)
	if rr.Code != http.StatusInternalServerError || policyErrorCode(t, rr) != "internal" || strings.Contains(rr.Body.String(), privateStoredDetail) || strings.Contains(rr.Body.String(), "rationale") {
		t.Fatalf("corrupt state=%d %s", rr.Code, rr.Body.String())
	}
}

func TestRolePolicyCandidateReadFailureReturnsSafeInternalError(t *testing.T) {
	f, _ := prepareConfirmableRolePolicyHTTP(t)
	account, err := f.store.SaveModelAccount(domain.ModelAccount{ID: "broken-account", Name: "Synthetic", Provider: "openai", BaseURL: "http://127.0.0.1:1234/v1", AuthMethod: "api_key", BillingMode: "metered_api", Enabled: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	route, err := f.store.CreateModelRoute(domain.ModelRoute{ID: "broken-route", AccountID: account.ID, Adapter: "openai_api_responses", Protocol: "responses", BaseURL: account.BaseURL, Environment: "simulation", SpendClass: "explicit_api", ExtraUsage: "off"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.PutProfile(domain.Profile{ID: "broken-profile", Name: "Synthetic", Kind: "openai", AccountID: account.ID, RouteID: route.ID, Protocol: route.Protocol, BaseURL: account.BaseURL, Model: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	state := getRolePolicyHTTPState(t, f)
	input := completeRolePolicyInput("policy-profile")
	for _, role := range domain.RolePolicyRoles() {
		value := input.Roles[role]
		first := value.Candidates[0]
		first.ProfileID = "broken-profile"
		value.Candidates = append([]domain.RolePolicyCandidateInput{first}, value.Candidates...)
		value.AllowedSpendClasses = []string{"explicit_api", "unknown"}
		input.Roles[role] = value
	}
	rr := f.request(http.MethodPut, "/api/role-model-policy", rolePolicyPutJSON(t, state.Policy.Revision, state.ProfilesDigest, input), true)
	if rr.Code != http.StatusOK {
		t.Fatalf("fixture policy save: %d", rr.Code)
	}
	proposal := proposeRolePolicyHTTP(t, f)
	if !proposal.Confirmable {
		t.Fatal("fixture second candidate was not eligible")
	}
	db, err := sql.Open("sqlite3", f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`DROP TABLE model_routes`); err != nil {
		t.Fatal(err)
	}
	for _, request := range []struct{ path, body string }{
		{"/api/role-model-policy/proposals", `{}`},
		{"/api/role-model-policy/proposals/" + proposal.ID + "/confirm", `{"digest":"` + proposal.Digest + `","confirm":true}`},
	} {
		rr = f.request(http.MethodPost, request.path, request.body, true)
		if rr.Code != http.StatusInternalServerError || strings.TrimSpace(rr.Body.String()) != `{"error":{"code":"internal","message":"role policy operation failed"}}` {
			t.Fatalf("unsafe or downgraded error response: %d %s", rr.Code, rr.Body.String())
		}
	}
}

func TestRolePolicyConfirmationRejectsRealStaleInputsWithoutApplying(t *testing.T) {
	for _, mutation := range []string{"policy", "profile", "settings"} {
		t.Run(mutation, func(t *testing.T) {
			f, proposal := prepareConfirmableRolePolicyHTTP(t)
			switch mutation {
			case "policy":
				state := getRolePolicyHTTPState(t, f)
				rr := f.request(http.MethodPut, "/api/role-model-policy", rolePolicyPutJSON(t, state.Policy.Revision, state.ProfilesDigest, completeRolePolicyInput("policy-profile")), true)
				if rr.Code != http.StatusOK {
					t.Fatalf("policy mutation=%d %s", rr.Code, rr.Body.String())
				}
			case "profile":
				if err := f.store.PutProfile(domain.Profile{ID: "policy-profile", Name: "Policy profile", Kind: "compatible", Protocol: "responses", BaseURL: "http://127.0.0.1:9", Model: "changed-model"}); err != nil {
					t.Fatal(err)
				}
			case "settings":
				if err := f.store.PutSettings(domain.Settings{DefaultProfileID: "policy-profile"}); err != nil {
					t.Fatal(err)
				}
			}
			before, err := f.store.GetSettings()
			if err != nil {
				t.Fatal(err)
			}
			rr := f.request(http.MethodPost, "/api/role-model-policy/proposals/"+proposal.ID+"/confirm", `{"digest":"`+proposal.Digest+`","confirm":true}`, true)
			if rr.Code != http.StatusConflict || policyErrorCode(t, rr) != "role_policy_stale" {
				t.Fatalf("confirmation=%d %s", rr.Code, rr.Body.String())
			}
			after, err := f.store.GetSettings()
			if err != nil || !reflect.DeepEqual(after, before) || after.ModelPolicyDecisionID != "" {
				t.Fatalf("stale confirmation changed settings: before=%+v after=%+v err=%v", before, after, err)
			}
		})
	}
}

func TestRolePolicyHTTPLifecycleAndDuplicateConfirmation(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	profile := domain.Profile{ID: "policy-profile", Name: "Policy profile", Kind: "compatible", Protocol: "responses", BaseURL: "http://127.0.0.1:9", Model: "fake-model"}
	if err := f.store.PutProfile(profile); err != nil {
		t.Fatal(err)
	}
	f.handler.(*server).secrets = forbiddenSecrets{t}

	state := getRolePolicyHTTPState(t, f)
	if state.Policy.Revision != 0 || len(state.Policy.Roles) != 7 || state.ProfilesDigest == "" {
		t.Fatalf("initial state=%+v", state)
	}
	input := completeRolePolicyInput(profile.ID)
	put := f.request(http.MethodPut, "/api/role-model-policy", rolePolicyPutJSON(t, 0, state.ProfilesDigest, input), true)
	if put.Code != http.StatusOK || strings.Contains(put.Body.String(), "base_url") || strings.Contains(put.Body.String(), "account_id") {
		t.Fatalf("save=%d %s", put.Code, put.Body.String())
	}
	var saved struct {
		Policy domain.RoleModelPolicy `json:"policy"`
	}
	if err := json.Unmarshal(put.Body.Bytes(), &saved); err != nil || saved.Policy.Revision != 1 {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}

	proposal := proposeRolePolicyHTTP(t, f)
	if !proposal.Confirmable || len(proposal.Roles) != 7 {
		t.Fatalf("proposal=%+v", proposal)
	}
	stale := f.request(http.MethodPost, "/api/role-model-policy/proposals/"+proposal.ID+"/confirm", `{"digest":"`+strings.Repeat("0", 64)+`","confirm":true}`, true)
	if stale.Code != http.StatusConflict || policyErrorCode(t, stale) != "role_policy_stale" {
		t.Fatalf("stale=%d %s", stale.Code, stale.Body.String())
	}

	confirmBody := `{"digest":"` + proposal.Digest + `","confirm":true}`
	first := f.request(http.MethodPost, "/api/role-model-policy/proposals/"+proposal.ID+"/confirm", confirmBody, true)
	if first.Code != http.StatusOK {
		t.Fatalf("confirm=%d %s", first.Code, first.Body.String())
	}
	var confirmed struct {
		Decision domain.RolePolicyDecision `json:"decision"`
		Settings domain.Settings           `json:"settings"`
		Applied  bool                      `json:"applied"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &confirmed); err != nil || !confirmed.Applied || confirmed.Decision.ID == "" || confirmed.Settings.ModelPolicyDecisionID != confirmed.Decision.ID {
		t.Fatalf("confirmed=%+v err=%v", confirmed, err)
	}
	second := f.request(http.MethodPost, "/api/role-model-policy/proposals/"+proposal.ID+"/confirm", confirmBody, true)
	var duplicate struct {
		Decision domain.RolePolicyDecision `json:"decision"`
		Settings domain.Settings           `json:"settings"`
		Applied  bool                      `json:"applied"`
	}
	if second.Code != http.StatusOK || json.Unmarshal(second.Body.Bytes(), &duplicate) != nil || duplicate.Applied || duplicate.Decision.ID != confirmed.Decision.ID {
		t.Fatalf("duplicate=%d %s", second.Code, second.Body.String())
	}

	profiles := f.request(http.MethodGet, "/api/profiles", "", true)
	if profiles.Code != http.StatusOK || !strings.Contains(profiles.Body.String(), `"model_policy_decision_id":"`+confirmed.Decision.ID+`"`) {
		t.Fatalf("read-only settings provenance missing=%d %s", profiles.Code, profiles.Body.String())
	}
	manual := f.request(http.MethodPut, "/api/settings", `{"default_profile_id":"policy-profile","role_bindings":{}}`, true)
	if manual.Code != http.StatusOK || strings.Contains(manual.Body.String(), "model_policy_decision_id") {
		t.Fatalf("manual settings=%d %s", manual.Code, manual.Body.String())
	}
	settings, err := f.store.GetSettings()
	if err != nil || settings.ModelPolicyDecisionID != "" {
		t.Fatalf("manual settings retained provenance=%+v err=%v", settings, err)
	}
}

func TestMissionPolicySnapshotProjectionIsSafeForLegacyAndMalformedMissions(t *testing.T) {
	f := newFixture(t)
	f.authenticate()
	db, err := sql.Open("sqlite3", f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	profile := domain.Profile{ID: "legacy-policy-profile", Name: "Legacy policy", Kind: "compatible", Protocol: "responses", BaseURL: "http://127.0.0.1:9", Model: "fake-model"}
	digest, err := domain.RolePolicyProfileDigest(profile)
	if err != nil {
		t.Fatal(err)
	}
	roles := map[domain.Role]domain.RolePolicyRoleResult{}
	profiles := map[domain.Role]domain.Profile{}
	for _, role := range domain.RolePolicyRoles() {
		profiles[role] = profile
		roles[role] = domain.RolePolicyRoleResult{
			SelectedProfileID:    profile.ID,
			RequiredCapabilities: []string{},
			Candidates: []domain.RolePolicyCandidateResult{{
				ProfileID: profile.ID, ProfileDigest: digest, SpendClass: domain.RolePolicySpendUnknown,
				Eligible: true, Reasons: []string{}, Capabilities: []string{"coding"}, Rationale: "User declaration only",
				ExecutionNotice: domain.RolePolicyNoticeLegacyUnknown,
			}},
		}
	}
	snapshot := domain.MissionRolePolicySnapshot{
		SchemaVersion: 1, DecisionID: "decision-safe", PolicyRevision: 1,
		RecommendationDigest: strings.Repeat("a", 64), ConfirmedAt: time.Date(2026, 10, 19, 1, 2, 3, 0, time.UTC).Format(time.RFC3339Nano), Roles: roles,
	}
	snapshot.Digest, err = domain.MissionRolePolicySnapshotDigest(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	validMission := domain.Mission{ID: "legacy-valid-policy", Revision: 1, State: "draft", Stage: "brief", Profiles: profiles, ModelPolicySnapshot: &snapshot}
	validRaw, err := json.Marshal(validMission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO missions(id,revision,state,updated_at,data) VALUES(?,1,'draft','then',?)`, validMission.ID, validRaw); err != nil {
		t.Fatal(err)
	}
	malformedRaw := `{"id":"legacy-invalid-policy","revision":1,"state":"draft","stage":"brief","model_policy_snapshot":{"schema_version":"future","raw_model_output":"private output must not leak"}}`
	if _, err = db.Exec(`INSERT INTO missions(id,revision,state,updated_at,data) VALUES(?,1,'draft','then',?)`, "legacy-invalid-policy", malformedRaw); err != nil {
		t.Fatal(err)
	}

	valid := f.request(http.MethodGet, "/api/missions/"+validMission.ID, "", true)
	if valid.Code != http.StatusOK || !strings.Contains(valid.Body.String(), `"model_policy_snapshot_status":"valid"`) || !strings.Contains(valid.Body.String(), `"decision_id":"decision-safe"`) || strings.Contains(valid.Body.String(), "base_url") {
		t.Fatalf("valid projection=%d %s", valid.Code, valid.Body.String())
	}
	invalid := f.request(http.MethodGet, "/api/missions/legacy-invalid-policy", "", true)
	if invalid.Code != http.StatusOK || !strings.Contains(invalid.Body.String(), `"model_policy_snapshot_status":"invalid_or_unsupported"`) || strings.Contains(invalid.Body.String(), "private output") || strings.Contains(invalid.Body.String(), "raw_model_output") {
		t.Fatalf("invalid projection=%d %s", invalid.Code, invalid.Body.String())
	}
}

func emptyRolePolicyInput() domain.RolePolicyInput {
	input := domain.RolePolicyInput{Roles: map[domain.Role]domain.RolePolicyRoleInput{}}
	for _, role := range domain.RolePolicyRoles() {
		input.Roles[role] = domain.RolePolicyRoleInput{Candidates: []domain.RolePolicyCandidateInput{}, RequiredCapabilities: []string{}, AllowedSpendClasses: []string{}, Preference: domain.RolePolicyPreferenceCandidateOrder}
	}
	return input
}

func completeRolePolicyInput(profileID string) domain.RolePolicyInput {
	input := emptyRolePolicyInput()
	for _, role := range domain.RolePolicyRoles() {
		input.Roles[role] = domain.RolePolicyRoleInput{
			Candidates:           []domain.RolePolicyCandidateInput{{ProfileID: profileID, Capabilities: []string{"coding"}, Rationale: "User declaration only"}},
			RequiredCapabilities: []string{}, AllowedSpendClasses: []string{domain.RolePolicySpendUnknown}, Preference: domain.RolePolicyPreferenceCandidateOrder,
		}
	}
	return input
}

func prepareConfirmableRolePolicyHTTP(t *testing.T) (*fixture, domain.RolePolicyProposal) {
	t.Helper()
	f := newFixture(t)
	f.authenticate()
	profile := domain.Profile{ID: "policy-profile", Name: "Policy profile", Kind: "compatible", Protocol: "responses", BaseURL: "http://127.0.0.1:9", Model: "fake-model"}
	if err := f.store.PutProfile(profile); err != nil {
		t.Fatal(err)
	}
	f.handler.(*server).secrets = forbiddenSecrets{t}
	state := getRolePolicyHTTPState(t, f)
	rr := f.request(http.MethodPut, "/api/role-model-policy", rolePolicyPutJSON(t, 0, state.ProfilesDigest, completeRolePolicyInput(profile.ID)), true)
	if rr.Code != http.StatusOK {
		t.Fatalf("save=%d %s", rr.Code, rr.Body.String())
	}
	proposal := proposeRolePolicyHTTP(t, f)
	if !proposal.Confirmable {
		t.Fatalf("proposal is not confirmable: %+v", proposal)
	}
	return f, proposal
}

func rolePolicyPutJSON(t *testing.T, revision int64, profilesDigest string, input domain.RolePolicyInput) string {
	t.Helper()
	raw, err := json.Marshal(struct {
		ExpectedRevision int64                                      `json:"expected_revision"`
		ProfilesDigest   string                                     `json:"profiles_digest"`
		Roles            map[domain.Role]domain.RolePolicyRoleInput `json:"roles"`
	}{revision, profilesDigest, input.Roles})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func rolePolicyInjectedCandidateJSON(field, value string) string {
	roles := make([]string, 0, len(domain.RolePolicyRoles()))
	for _, role := range domain.RolePolicyRoles() {
		candidate := `[]`
		if role == domain.Main {
			candidate = `[{"profile_id":"profile","capabilities":[],"rationale":"declared","` + field + `":` + value + `}]`
		}
		roles = append(roles, `"`+string(role)+`":{"candidates":`+candidate+`,"required_capabilities":[],"allowed_spend_classes":[],"preference":"candidate_order"}`)
	}
	return `{"expected_revision":0,"profiles_digest":"` + strings.Repeat("0", 64) + `","roles":{` + strings.Join(roles, ",") + `}}`
}

func getRolePolicyHTTPState(t *testing.T, f *fixture) domain.RolePolicyState {
	t.Helper()
	rr := f.request(http.MethodGet, "/api/role-model-policy", "", true)
	if rr.Code != http.StatusOK {
		t.Fatalf("state=%d %s", rr.Code, rr.Body.String())
	}
	var response struct {
		State domain.RolePolicyState `json:"state"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.State
}

func proposeRolePolicyHTTP(t *testing.T, f *fixture) domain.RolePolicyProposal {
	t.Helper()
	rr := f.request(http.MethodPost, "/api/role-model-policy/proposals", `{}`, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("proposal=%d %s", rr.Code, rr.Body.String())
	}
	var response struct {
		Proposal domain.RolePolicyProposal `json:"proposal"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Proposal
}

func policyErrorCode(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v body=%s", err, rr.Body.String())
	}
	return response.Error.Code
}
