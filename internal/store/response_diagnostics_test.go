package store

import (
	"foundry/internal/domain"
	"testing"
)

func TestResponseDiagnosticReceiptIsImmutableAndNotApproval(t *testing.T) {
	s := openTestStore(t)
	m := inferenceMission(t, s)
	_, v, err := s.BeginInvocation(inferenceInvocation(m), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	d := domain.ResponseDiagnostic{Version: 1, Stage: "stream_read", Reason: "missing_terminal", MediaType: "sse", BytesRead: 100, EventsSeen: 1}
	r := domain.ModelCallReceipt{HTTPStatus: 200, Certainty: "unknown", ResponseDiagnostic: d}
	for _, certainty := range []string{"completed", "not_sent", "rejected"} {
		forged := r
		forged.Certainty = certainty
		status := "failed"
		if certainty == "completed" {
			status = "succeeded"
		}
		if validateModelReceipt(&forged, status) == nil {
			t.Fatalf("failure observation laundered into %s", certainty)
		}
	}
	bad := r
	bad.ResponseDiagnostic.TerminalEvent = "PRIVATE"
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "outcome_unknown", nil, "response_incomplete", domain.CallUsage{ModelReceipt: &bad}); err == nil {
		t.Fatal("unsafe diagnostic accepted")
	}
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "outcome_unknown", nil, "response_incomplete", domain.CallUsage{ModelReceipt: &r}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetInvocation(v.ID)
	if err != nil || got.ModelReceipt == nil || *got.ModelReceipt != r || got.Result != nil || got.UsageKnown {
		t.Fatal(got, err)
	}
	changed := r
	changed.ResponseDiagnostic.Reason = "read_error"
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "outcome_unknown", nil, "response_incomplete", domain.CallUsage{ModelReceipt: &changed}); err == nil {
		t.Fatal("historical receipt overwritten")
	}
	after, err := s.GetMission(m.ID)
	if err != nil || after.Budget.UsedCalls != 1 || len(after.Approvals) != len(m.Approvals) {
		t.Fatal("receipt changed budget/approvals", err)
	}
}
