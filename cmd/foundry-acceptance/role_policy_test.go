package main

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"foundry/internal/domain"
	"foundry/internal/engine"
)

// Breaks if startup stops publishing the safe role-policy fixture, confirms it
// implicitly, loses the two real invocation outcomes, or routes a candidate
// outside the owned model fixture.
func TestRolePolicyScenariosSeedUnconfirmedProtocolEvidence(t *testing.T) {
	runtime := startProtocolAcceptanceForTest(t)
	manifest := runtime.manifest()
	proposalID := manifest.Scenarios["role_policy_proposal_unconfirmed"]
	oldMissionID := manifest.Scenarios["role_policy_old_mission"]
	if proposalID == "" || oldMissionID != manifest.Scenarios["plan_success"] {
		t.Fatalf("role-policy scenario IDs missing or inconsistent: %+v", manifest.Scenarios)
	}
	if manifest.BrowserPaths["role_model_policy"] == "" {
		t.Fatalf("role-policy browser path missing: %+v", manifest.BrowserPaths)
	}

	state, err := runtime.store.GetRolePolicyState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Policy.Revision != 1 || state.LatestProposal == nil || state.LatestProposal.ID != proposalID || !state.LatestProposal.Confirmable || state.ActiveDecision != nil {
		t.Fatalf("startup proposal is not fresh and unconfirmed: %+v", state)
	}
	planner := state.LatestProposal.Roles[domain.Planner]
	if planner.SelectedProfileID != "synthetic-siwc-success-planner" || len(planner.Candidates) != 3 {
		t.Fatalf("planner fixture does not expose the reviewed candidate set: %+v", planner)
	}
	completedInvocations, err := runtime.store.ListInvocations(manifest.Scenarios["plan_success"], 0, 100)
	if err != nil || len(completedInvocations) != 1 {
		t.Fatalf("completed evidence source unavailable: count=%d err=%v", len(completedInvocations), err)
	}
	unknownInvocations, err := runtime.store.ListInvocations(manifest.Scenarios["sse_outcome_unknown"], 0, 100)
	if err != nil || len(unknownInvocations) != 1 {
		t.Fatalf("unknown evidence source unavailable: count=%d err=%v", len(unknownInvocations), err)
	}
	completed := rolePolicyCandidateResult(t, planner, "synthetic-siwc-success-planner")
	unknown := rolePolicyCandidateResult(t, planner, "synthetic-sse-outcome-unknown-profile")
	deniedFee := rolePolicyCandidateResult(t, planner, "synthetic-kimi-package-profile")
	if !completed.Eligible || completed.Evidence == nil || completed.Evidence.InvocationID != completedInvocations[0].ID || completed.Evidence.MissionID != manifest.Scenarios["plan_success"] || completed.Evidence.Role != domain.Planner || completed.Evidence.Status != "completed" || completed.Evidence.ErrorCode != "" || completed.Evidence.Environment != "simulation" || completed.Evidence.FinishedAt != completedInvocations[0].FinishedAt {
		t.Fatalf("completed invocation was not represented accurately: %+v", completed)
	}
	if unknown.Eligible || unknown.Evidence == nil || unknown.Evidence.InvocationID != unknownInvocations[0].ID || unknown.Evidence.MissionID != manifest.Scenarios["sse_outcome_unknown"] || unknown.Evidence.Role != domain.Planner || unknown.Evidence.Status != "outcome_unknown" || unknown.Evidence.ErrorCode != "response_incomplete" || unknown.Evidence.Environment != "simulation" || unknown.Evidence.FinishedAt != unknownInvocations[0].FinishedAt || !containsString(unknown.Reasons, domain.RolePolicyReasonCapabilityMissing) {
		t.Fatalf("unknown capability-negative evidence was upgraded or lost: %+v", unknown)
	}
	if deniedFee.Eligible || deniedFee.Evidence != nil || !containsString(deniedFee.Reasons, domain.RolePolicyReasonSpendClassNotAllowed) {
		t.Fatalf("interactive-package fee denial was not explicit: %+v", deniedFee)
	}

	for _, role := range domain.RolePolicyRoles() {
		for _, candidate := range state.Policy.Roles[role].Candidates {
			profile, getErr := runtime.store.GetProfile(candidate.ProfileID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if !strings.HasPrefix(profile.BaseURL, runtime.protocol.modelOrigin()+"/") || profile.RouteID == "" || profile.AccountID == "" {
				t.Fatalf("candidate %s escaped the owned protocol fixture: %+v", candidate.ProfileID, profile)
			}
		}
	}
	oldMission, err := runtime.store.GetMission(oldMissionID)
	if err != nil {
		t.Fatal(err)
	}
	if oldMission.ModelPolicySnapshot != nil || oldMission.ModelSnapshot == nil || oldMission.ModelSnapshot.Roles[domain.Planner].Profile.ID != "synthetic-siwc-success-planner" {
		t.Fatalf("historical mission was rewritten by policy seeding: %+v", oldMission)
	}
}

// Breaks if any policy HTTP action dispatches inference/discovery/token I/O,
// accepts a stale proposal, reapplies a duplicate decision after manual edits,
// or retrofits policy provenance onto an existing mission.
func TestRolePolicyHTTPFlowIsDeterministicAndFutureOnly(t *testing.T) {
	runtime := startProtocolAcceptanceForTest(t)
	trafficBefore := snapshotProtocolTraffic(runtime.protocol)
	oldMissionBefore, err := runtime.store.GetMission(runtime.scenarios["role_policy_old_mission"])
	if err != nil {
		t.Fatal(err)
	}
	client := newAcceptanceAPIClient(t, runtime.URL)

	var initial struct {
		State domain.RolePolicyState `json:"state"`
	}
	client.json(http.MethodGet, "/api/role-model-policy", nil, http.StatusOK, &initial)
	if initial.State.LatestProposal == nil || initial.State.LatestProposal.ID != runtime.scenarios["role_policy_proposal_unconfirmed"] {
		t.Fatalf("GET omitted startup proposal: %+v", initial.State)
	}

	roles := rolePolicyInputFromStored(initial.State.Policy)
	var saved struct {
		Policy domain.RoleModelPolicy `json:"policy"`
	}
	client.json(http.MethodPut, "/api/role-model-policy", map[string]any{
		"expected_revision": initial.State.Policy.Revision,
		"profiles_digest":   initial.State.ProfilesDigest,
		"roles":             roles,
	}, http.StatusOK, &saved)
	var stale struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	client.json(http.MethodPost, "/api/role-model-policy/proposals/"+initial.State.LatestProposal.ID+"/confirm", map[string]any{
		"digest":  initial.State.LatestProposal.Digest,
		"confirm": true,
	}, http.StatusConflict, &stale)
	if stale.Error.Code != "role_policy_stale" {
		t.Fatalf("stale preview returned non-finite error: %+v", stale)
	}

	var proposed struct {
		Proposal domain.RolePolicyProposal `json:"proposal"`
	}
	client.json(http.MethodPost, "/api/role-model-policy/proposals", map[string]any{}, http.StatusOK, &proposed)
	if !proposed.Proposal.Confirmable || proposed.Proposal.PolicyRevision != saved.Policy.Revision {
		t.Fatalf("fresh proposal is not confirmable: %+v", proposed.Proposal)
	}
	var confirmed struct {
		Decision domain.RolePolicyDecision `json:"decision"`
		Settings domain.Settings           `json:"settings"`
		Applied  bool                      `json:"applied"`
	}
	client.json(http.MethodPost, "/api/role-model-policy/proposals/"+proposed.Proposal.ID+"/confirm", map[string]any{
		"digest":  proposed.Proposal.Digest,
		"confirm": true,
	}, http.StatusOK, &confirmed)
	if !confirmed.Applied || confirmed.Decision.ProposalID != proposed.Proposal.ID || confirmed.Settings.ModelPolicyDecisionID != confirmed.Decision.ID {
		t.Fatalf("fresh confirmation was not applied exactly once: %+v", confirmed)
	}

	create := engine.CreateRequest{
		Title: "SYNTHETIC role policy future mission", Goal: "Freeze policy provenance without planning or model calls",
		RepoPath: runtime.Repo, WorkspaceMode: domain.WorkspaceModeGenerationsV1,
		Spec: domain.Spec{Scope: []string{"src/"}, Acceptance: []string{"test -f /workspace/src/main.txt"}, Image: "synthetic:stub", ProfileID: "synthetic-siwc-success-main", MaxCalls: 16},
	}
	var created struct {
		Mission domain.Mission `json:"mission"`
	}
	client.json(http.MethodPost, "/api/missions", create, http.StatusOK, &created)
	if created.Mission.ModelPolicySnapshot == nil || created.Mission.ModelPolicySnapshot.DecisionID != confirmed.Decision.ID || created.Mission.ModelPolicySnapshot.PolicyRevision != saved.Policy.Revision {
		t.Fatalf("new mission did not freeze confirmed policy provenance: %+v", created.Mission.ModelPolicySnapshot)
	}
	if len(created.Mission.ModelPolicySnapshot.Roles) != 7 || created.Mission.ModelSnapshot == nil || len(created.Mission.ModelSnapshot.Roles) != 7 || len(created.Mission.Profiles) != 7 {
		t.Fatalf("new mission did not freeze all seven policy selections: %+v", created.Mission)
	}
	for _, role := range domain.RolePolicyRoles() {
		policyRole, policyOK := created.Mission.ModelPolicySnapshot.Roles[role]
		modelRole, modelOK := created.Mission.ModelSnapshot.Roles[role]
		profile, profileOK := created.Mission.Profiles[role]
		if !policyOK || !modelOK || !profileOK || policyRole.SelectedProfileID != profile.ID || modelRole.Profile.ID != profile.ID {
			t.Fatalf("role %s policy/model/profile selections disagree: policy=%+v model=%+v profile=%+v", role, policyRole, modelRole, profile)
		}
	}
	if created.Mission.State != "draft" || created.Mission.Budget.UsedCalls != 0 {
		t.Fatalf("mission creation crossed the planning boundary: %+v", created.Mission)
	}

	manual := allRoleBindings("synthetic-kimi-package-profile", nil)
	var manualResult struct {
		Settings domain.Settings `json:"settings"`
	}
	client.json(http.MethodPut, "/api/settings", manual, http.StatusOK, &manualResult)
	if manualResult.Settings.ModelPolicyDecisionID != "" {
		t.Fatalf("manual settings did not exit active policy mode: %+v", manualResult.Settings)
	}
	var duplicate struct {
		Decision domain.RolePolicyDecision `json:"decision"`
		Settings domain.Settings           `json:"settings"`
		Applied  bool                      `json:"applied"`
	}
	client.json(http.MethodPost, "/api/role-model-policy/proposals/"+proposed.Proposal.ID+"/confirm", map[string]any{
		"digest":  proposed.Proposal.Digest,
		"confirm": true,
	}, http.StatusOK, &duplicate)
	if duplicate.Applied || duplicate.Decision.ID != confirmed.Decision.ID || duplicate.Settings.ModelPolicyDecisionID != "" || !reflect.DeepEqual(duplicate.Settings.RoleBindings, manual.RoleBindings) {
		t.Fatalf("duplicate confirmation reapplied settings: %+v", duplicate)
	}
	manualCreate := create
	manualCreate.Title = "SYNTHETIC manual settings after duplicate confirmation"
	var manualCreated struct {
		Mission domain.Mission `json:"mission"`
	}
	client.json(http.MethodPost, "/api/missions", manualCreate, http.StatusOK, &manualCreated)
	if manualCreated.Mission.ModelPolicySnapshot != nil || manualCreated.Mission.ModelSnapshot == nil || len(manualCreated.Mission.ModelSnapshot.Roles) != 7 || len(manualCreated.Mission.Profiles) != 7 {
		t.Fatalf("post-duplicate mission regained policy provenance or lost manual selections: %+v", manualCreated.Mission)
	}
	for _, role := range domain.RolePolicyRoles() {
		modelRole, modelOK := manualCreated.Mission.ModelSnapshot.Roles[role]
		profile, profileOK := manualCreated.Mission.Profiles[role]
		if !modelOK || !profileOK || modelRole.Profile.ID != "synthetic-kimi-package-profile" || profile.ID != "synthetic-kimi-package-profile" {
			t.Fatalf("post-duplicate mission role %s ignored persisted manual binding: model=%+v profile=%+v", role, modelRole, profile)
		}
	}
	if manualCreated.Mission.State != "draft" || manualCreated.Mission.Budget.UsedCalls != 0 {
		t.Fatalf("post-duplicate mission crossed the planning boundary: %+v", manualCreated.Mission)
	}

	oldMissionAfter, err := runtime.store.GetMission(oldMissionBefore.ID)
	if err != nil {
		t.Fatal(err)
	}
	if oldMissionAfter.ModelPolicySnapshot != nil || !reflect.DeepEqual(oldMissionAfter.ModelSnapshot, oldMissionBefore.ModelSnapshot) || !reflect.DeepEqual(oldMissionAfter.Profiles, oldMissionBefore.Profiles) {
		t.Fatalf("policy flow rewrote historical mission selection: before=%+v after=%+v", oldMissionBefore, oldMissionAfter)
	}
	if got := snapshotProtocolTraffic(runtime.protocol); !reflect.DeepEqual(got, trafficBefore) {
		t.Fatalf("metadata-only policy flow emitted protocol traffic: before=%+v after=%+v", trafficBefore, got)
	}
}

func rolePolicyCandidateResult(t *testing.T, role domain.RolePolicyRoleResult, profileID string) domain.RolePolicyCandidateResult {
	t.Helper()
	for _, candidate := range role.Candidates {
		if candidate.ProfileID == profileID {
			return candidate
		}
	}
	t.Fatalf("candidate %s missing from %+v", profileID, role)
	return domain.RolePolicyCandidateResult{}
}

func rolePolicyInputFromStored(policy domain.RoleModelPolicy) map[domain.Role]domain.RolePolicyRoleInput {
	roles := make(map[domain.Role]domain.RolePolicyRoleInput, len(policy.Roles))
	for role, stored := range policy.Roles {
		input := domain.RolePolicyRoleInput{
			Candidates: []domain.RolePolicyCandidateInput{}, RequiredCapabilities: append([]string{}, stored.RequiredCapabilities...),
			AllowedSpendClasses: append([]string{}, stored.AllowedSpendClasses...), Preference: stored.Preference,
		}
		for _, candidate := range stored.Candidates {
			copyCandidate := candidate.RolePolicyCandidateInput
			copyCandidate.Capabilities = append([]string{}, candidate.Capabilities...)
			input.Candidates = append(input.Candidates, copyCandidate)
		}
		roles[role] = input
	}
	return roles
}

type protocolTraffic struct {
	TokenCalls         int
	Directories        int
	SignedTokens       int
	Revocations        int
	PKCEValidated      bool
	AuthorizationReqs  int
	AuthorizationCodes int
	IssuedTokens       int
	ActiveTokens       int
	RefreshGrants      int
	ActiveRefreshes    int
	ModelHits          map[string]int
}

func snapshotProtocolTraffic(fixture *protocolFixture) protocolTraffic {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	hits := make(map[string]int, len(fixture.modelHits))
	for modelID, count := range fixture.modelHits {
		hits[modelID] = count
	}
	activeTokens := 0
	for _, active := range fixture.tokens {
		if active {
			activeTokens++
		}
	}
	activeRefreshes := 0
	for _, grant := range fixture.refreshes {
		if !grant.revoked && grant.current != "" {
			activeRefreshes++
		}
	}
	return protocolTraffic{
		TokenCalls: fixture.tokenCalls, Directories: fixture.directories, SignedTokens: fixture.signed,
		Revocations: fixture.revokeCalls, PKCEValidated: fixture.pkce, AuthorizationReqs: len(fixture.requests),
		AuthorizationCodes: len(fixture.codes), IssuedTokens: len(fixture.tokens), ActiveTokens: activeTokens,
		RefreshGrants: len(fixture.refreshes), ActiveRefreshes: activeRefreshes, ModelHits: hits,
	}
}

type acceptanceAPIClient struct {
	t      *testing.T
	base   string
	client *http.Client
	cookie *http.Cookie
}

func newAcceptanceAPIClient(t *testing.T, base string) *acceptanceAPIClient {
	t.Helper()
	client := &acceptanceAPIClient{t: t, base: base, client: &http.Client{Timeout: 5 * time.Second}}
	client.json(http.MethodPost, "/api/session", map[string]any{}, http.StatusOK, nil)
	return client
}

func (c *acceptanceAPIClient) json(method, path string, body any, wantStatus int, target any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		reader = strings.NewReader(string(raw))
	}
	request, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		c.t.Fatal(err)
	}
	if method != http.MethodGet {
		request.Header.Set("Origin", c.base)
		request.Header.Set("Content-Type", "application/json")
	}
	if c.cookie != nil {
		request.AddCookie(c.cookie)
	}
	response, err := c.client.Do(request)
	if err != nil {
		c.t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	if path == "/api/session" && response.StatusCode == http.StatusOK {
		cookies := response.Cookies()
		if len(cookies) != 1 {
			c.t.Fatalf("session cookie count=%d", len(cookies))
		}
		c.cookie = cookies[0]
	}
	if response.StatusCode != wantStatus {
		c.t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, wantStatus, raw)
	}
	if target != nil {
		if err = json.Unmarshal(raw, target); err != nil {
			c.t.Fatalf("decode %s %s: %v body=%s", method, path, err, raw)
		}
	}
}
