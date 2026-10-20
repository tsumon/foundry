package store

import (
	"encoding/json"
	"foundry/internal/domain"
	"strings"
	"testing"
	"time"
)

func claimedGateOperation(t *testing.T) (*Store, domain.ModelAccount, domain.ModelAcceptanceOperation) {
	t.Helper()
	s, account, route := subscriptionGateStore(t)
	o, err := s.ProposeModelAcceptanceWithConnectionMode(account.ID, domain.ModelAcceptanceProposal{Phase: "connection", ConnectionPurpose: "subscription_only", RouteID: route.ID, ExpectedAccountRevision: account.Revision}, "browser_handoff", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.DecideModelAcceptance(account.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.ClaimModelAcceptance(account.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return s, account, o
}

func TestAcceptanceDiagnosticRejectsUnrelatedConnectionPhase(t *testing.T) {
	s, account, route := gateStore(t)
	o := gateProposal(t, s, account, route)
	var err error
	o, err = s.DecideModelAcceptance(account.ID, o.ID, o.Revision, o.MaterialDigest, "approve", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	o, err = s.ClaimModelAcceptance(account.ID, o.ID, o.Revision, o.MaterialDigest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := &domain.ConnectionDiagnostic{Version: 1, Stage: "token_exchange", Code: "auth_invalid", HTTPStatus: 403, ProviderCode: "invalid_grant", CredentialCommit: "not_committed", Cleanup: "completed"}
	if _, err = s.FinishModelAcceptanceWithDiagnostic(account.ID, o.ID, o.Revision, "failed", "rejected", "auth_invalid", nil, 0, 0, false, diagnostic, time.Now()); err == nil {
		t.Fatal("non-browser connection accepted a browser diagnostic")
	}
}

func TestAcceptanceDiagnosticPersistsAtomicallyAndProjectsACopy(t *testing.T) {
	s, account, o := claimedGateOperation(t)
	diagnostic := &domain.ConnectionDiagnostic{Version: 1, Stage: "token_exchange", Code: "auth_invalid", HTTPStatus: 403, ProviderCode: "invalid_grant", CredentialCommit: "not_committed", Cleanup: "completed"}
	got, err := s.FinishModelAcceptanceWithDiagnostic(account.ID, o.ID, o.Revision, "failed", "rejected", "auth_invalid", nil, 0, 0, false, diagnostic, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.ConnectionDiagnostic == nil || got.ConnectionDiagnostic.Stage != "token_exchange" {
		t.Fatalf("diagnostic missing from receipt: %+v", got)
	}
	diagnostic.Stage = "session"
	got.ConnectionDiagnostic.Stage = "credential_commit"
	stored, err := s.GetModelAcceptance(account.ID, o.ID)
	if err != nil || stored.ConnectionDiagnostic == nil || stored.ConnectionDiagnostic.Stage != "token_exchange" {
		t.Fatalf("stored diagnostic shared caller memory: %+v %v", stored, err)
	}
}

func TestAcceptanceIdentityDiagnosticSurvivesRestartWithoutChangingApproval(t *testing.T) {
	s, account, o := claimedGateOperation(t)
	var sequence int
	var name, path string
	if err := s.db.QueryRow(`PRAGMA database_list`).Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	diagnostic := &domain.ConnectionDiagnostic{Version: 1, Stage: "identity_validation", Code: "auth_invalid", IdentityCheck: "audience", CredentialCommit: "not_committed", Cleanup: "completed"}
	got, err := s.FinishModelAcceptanceWithDiagnostic(account.ID, o.ID, o.Revision, "unknown", "unknown", "transport_unknown", nil, 0, 0, false, diagnostic, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.MaterialDigest != o.MaterialDigest {
		t.Fatal("diagnostic changed approval digest")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	stored, err := reopened.GetModelAcceptance(account.ID, o.ID)
	if err != nil || stored.ConnectionDiagnostic == nil || stored.ConnectionDiagnostic.IdentityCheck != "audience" || stored.ConnectionDiagnostic.CredentialCommit != "not_committed" || stored.ConnectionDiagnostic.Cleanup != "completed" || stored.Attempts != 1 || stored.MaterialDigest != o.MaterialDigest {
		t.Fatalf("restart evidence=%+v %v", stored, err)
	}
	if _, err = reopened.ClaimModelAcceptance(account.ID, stored.ID, stored.Revision, stored.MaterialDigest, time.Now()); err == nil {
		t.Fatal("diagnostic enabled replay")
	}
}

func TestAcceptanceDiagnosticRejectsUntrustedOrContradictoryEvidence(t *testing.T) {
	cases := []domain.ConnectionDiagnostic{
		{Version: 1, Stage: "identity_validation", Code: "auth_invalid", IdentityCheck: "SYNTHETIC-PRIVATE-CHECK", CredentialCommit: "not_committed", Cleanup: "completed"},
		{Version: 1, Stage: "token_exchange", Code: "auth_invalid", IdentityCheck: "audience", CredentialCommit: "not_committed", Cleanup: "completed"},
		{Version: 2, Stage: "token_exchange", Code: "auth_invalid", CredentialCommit: "not_committed", Cleanup: "completed"},
		{Version: 1, Stage: "raw-stage", Code: "auth_invalid", CredentialCommit: "not_committed", Cleanup: "completed"},
		{Version: 1, Stage: "token_exchange", Code: "raw-error", CredentialCommit: "not_committed", Cleanup: "completed"},
		{Version: 1, Stage: "token_exchange", Code: "auth_invalid", HTTPStatus: 999, CredentialCommit: "not_committed", Cleanup: "completed"},
		{Version: 1, Stage: "token_exchange", Code: "auth_invalid", ProviderCode: "private-provider-text", CredentialCommit: "not_committed", Cleanup: "completed"},
		{Version: 1, Stage: "token_exchange", Code: "auth_invalid", CredentialCommit: "not_committed", Cleanup: "pending"},
	}
	for i := range cases {
		t.Run(strings.Repeat("x", i+1), func(t *testing.T) {
			s, account, o := claimedGateOperation(t)
			if _, err := s.FinishModelAcceptanceWithDiagnostic(account.ID, o.ID, o.Revision, "failed", "rejected", "auth_invalid", nil, 0, 0, false, &cases[i], time.Now()); err == nil {
				t.Fatalf("unsafe diagnostic persisted: %+v", cases[i])
			}
			stored, err := s.GetModelAcceptance(account.ID, o.ID)
			if err != nil || stored.State != "running" || stored.ConnectionDiagnostic != nil {
				t.Fatalf("rejected diagnostic partially committed: %+v %v", stored, err)
			}
		})
	}
}

func TestAcceptanceReadDropsMalformedStoredDiagnosticWithoutRewritingHistory(t *testing.T) {
	for _, diagnostic := range []string{
		`{"version":1,"stage":"identity_validation","code":"auth_invalid","identity_check":"SYNTHETIC-PRIVATE-CHECK","credential_commit":"not_committed","cleanup":"completed"}`,
		`{"version":1,"stage":"token_exchange","code":"auth_invalid","identity_check":"nonce","credential_commit":"not_committed","cleanup":"completed"}`,
		`{"version":99,"stage":"provider-secret","code":"provider-secret","credential_commit":"committed","cleanup":"completed"}`,
		`"bad"`,
		`[]`,
		`{"version":"1","stage":"token_exchange","code":"auth_invalid","credential_commit":"not_committed","cleanup":"completed"}`,
	} {
		t.Run(diagnostic, func(t *testing.T) {
			s, account, o := claimedGateOperation(t)
			var err error
			o, err = s.FinishModelAcceptance(account.ID, o.ID, o.Revision, "failed", "rejected", "auth_invalid", nil, 0, 0, false, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			var raw []byte
			if err := s.db.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=?`, o.ID).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			var record map[string]json.RawMessage
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			record["connection_diagnostic"] = json.RawMessage(diagnostic)
			corrupt, _ := json.Marshal(record)
			if _, err := s.db.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, corrupt, o.ID); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetModelAcceptance(account.ID, o.ID)
			if err != nil || got.ID != o.ID || got.State != "failed" || got.ConnectionDiagnostic != nil {
				t.Fatalf("malformed diagnostic reflected: %+v %v", got.ConnectionDiagnostic, err)
			}
			listed, err := s.ListModelAcceptance(account.ID)
			if err != nil || len(listed) != 1 || listed[0].ID != o.ID || listed[0].State != "failed" || listed[0].ConnectionDiagnostic != nil {
				t.Fatalf("malformed diagnostic hid valid receipt: %+v %v", listed, err)
			}
			var after []byte
			if err = s.db.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=?`, o.ID).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if string(after) != string(corrupt) {
				t.Fatal("read projection rewrote historical JSON")
			}
		})
	}
}

func TestAcceptanceDiagnosticAllowsPostCommitFailureEvidence(t *testing.T) {
	s, account, o := claimedGateOperation(t)
	diagnostic := &domain.ConnectionDiagnostic{Version: 1, Stage: "session", Code: "transport_unknown", CredentialCommit: "committed", Cleanup: "completed"}
	got, err := s.FinishModelAcceptanceWithDiagnostic(account.ID, o.ID, o.Revision, "failed", "rejected", "auth_invalid", nil, 0, 0, false, diagnostic, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got.ConnectionDiagnostic == nil || got.ConnectionDiagnostic.CredentialCommit != "committed" || got.ConnectionDiagnostic.Cleanup != "completed" {
		t.Fatalf("post-commit evidence missing from receipt: %+v", got.ConnectionDiagnostic)
	}
}
