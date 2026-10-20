package domain

import "testing"

func TestConnectionDiagnosticValidAcceptsOnlyBoundedEvidence(t *testing.T) {
	valid := ConnectionDiagnostic{
		Version:          1,
		Stage:            "token_exchange",
		Code:             "auth_invalid",
		HTTPStatus:       403,
		ProviderCode:     "invalid_grant",
		CredentialCommit: "not_committed",
		Cleanup:          "completed",
	}
	if !valid.Valid() {
		t.Fatal("bounded diagnostic rejected")
	}
	for name, mutate := range map[string]func(*ConnectionDiagnostic){
		"version":          func(v *ConnectionDiagnostic) { v.Version = 2 },
		"stage":            func(v *ConnectionDiagnostic) { v.Stage = "raw-provider-stage" },
		"code":             func(v *ConnectionDiagnostic) { v.Code = "raw-provider-error" },
		"http status":      func(v *ConnectionDiagnostic) { v.HTTPStatus = 999 },
		"provider code":    func(v *ConnectionDiagnostic) { v.ProviderCode = "secret-provider-text" },
		"credential claim": func(v *ConnectionDiagnostic) { v.CredentialCommit = "probably_saved" },
		"cleanup claim":    func(v *ConnectionDiagnostic) { v.Cleanup = "probably_deleted" },
	} {
		t.Run(name, func(t *testing.T) {
			got := valid
			mutate(&got)
			if got.Valid() {
				t.Fatalf("unbounded diagnostic accepted: %+v", got)
			}
		})
	}
}

func TestConnectionDiagnosticCloneDoesNotShareMutableReceiptState(t *testing.T) {
	original := &ConnectionDiagnostic{Version: 1, Stage: "session", Code: "canceled", CredentialCommit: "not_committed", Cleanup: "not_required"}
	cloned := CloneConnectionDiagnostic(original)
	if cloned == nil || cloned == original || !cloned.Valid() {
		t.Fatal("diagnostic was not copied")
	}
	cloned.Stage = "credential_commit"
	if original.Stage != "session" {
		t.Fatal("diagnostic clone mutated the original")
	}
}

func TestConnectionDiagnosticAllowsPostCommitFailureEvidence(t *testing.T) {
	for _, cleanup := range []string{"not_required", "completed", "pending", "unknown"} {
		diagnostic := ConnectionDiagnostic{Version: 1, Stage: "session", Code: "transport_unknown", CredentialCommit: "committed", Cleanup: cleanup}
		if !diagnostic.Valid() {
			t.Fatalf("post-commit failure evidence rejected for cleanup %q", cleanup)
		}
	}
}
