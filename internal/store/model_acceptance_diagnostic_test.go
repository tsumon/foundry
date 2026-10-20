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

func TestAcceptanceDiagnosticRejectsUntrustedOrContradictoryEvidence(t *testing.T) {
	cases := []domain.ConnectionDiagnostic{
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
	s, account, o := claimedGateOperation(t)
	before := `{"version":99,"stage":"provider-secret","code":"provider-secret","credential_commit":"committed","cleanup":"completed"}`
	var raw []byte
	if err := s.db.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=?`, o.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	record["connection_diagnostic"] = json.RawMessage(before)
	corrupt, _ := json.Marshal(record)
	if _, err := s.db.Exec(`UPDATE model_acceptance_operations SET data=? WHERE id=?`, corrupt, o.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetModelAcceptance(account.ID, o.ID)
	if err != nil || got.ConnectionDiagnostic != nil {
		t.Fatalf("malformed diagnostic reflected: %+v %v", got.ConnectionDiagnostic, err)
	}
	var after []byte
	if err = s.db.QueryRow(`SELECT data FROM model_acceptance_operations WHERE id=?`, o.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if string(after) != string(corrupt) {
		t.Fatal("read projection rewrote historical JSON")
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
