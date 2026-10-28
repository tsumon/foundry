package store

import (
	"foundry/internal/domain"
	"testing"
)

func TestExchangeReceiptCannotRewriteHistoryOrAuthorizeAnything(t *testing.T) {
	s := openTestStore(t)
	m := inferenceMission(t, s)
	_, v, err := s.BeginInvocation(inferenceInvocation(m), m.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimInvocation(v.ID, v.Epoch); err != nil {
		t.Fatal(err)
	}
	d := domain.HTTPExchangeDiagnostic{Version: 1, ClientRequestID: "fdy_0123456789abcdef0123456789abcdef", ProxyMode: "disabled", PeerClass: "loopback", Headers: domain.HTTPPhaseDiagnostic{State: "completed", Attempts: 1}, FirstByteMillis: 0, FinalStatus: 200, RequestWrites: 1, HeaderFields: 1, Transport: domain.HTTPTransportDiagnostic{Version: 1, ContentType: "absent", Protocol: "http1_1", BodyLength: "unknown"}}
	r := domain.ModelCallReceipt{HTTPStatus: 200, Certainty: "unknown", ExchangeDiagnostic: d}
	bad := r
	bad.ExchangeDiagnostic.PeerClass = "PRIVATE"
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "outcome_unknown", nil, "response_incomplete", domain.CallUsage{ModelReceipt: &bad}); err == nil {
		t.Fatal("unsafe trace accepted")
	}
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "outcome_unknown", nil, "response_incomplete", domain.CallUsage{ModelReceipt: &r}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetInvocation(v.ID)
	if err != nil || got.ModelReceipt == nil || *got.ModelReceipt != r || got.Result != nil || got.UsageKnown {
		t.Fatal(got, err)
	}
	changed := r
	changed.ExchangeDiagnostic.ClientRequestID = "fdy_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err = s.FinishInvocation(v.ID, v.Epoch, "outcome_unknown", nil, "response_incomplete", domain.CallUsage{ModelReceipt: &changed}); err == nil {
		t.Fatal("existing trace replaced")
	}
	after, err := s.GetMission(m.ID)
	if err != nil || after.Budget.UsedCalls != 1 || len(after.Approvals) != len(m.Approvals) {
		t.Fatal("trace changed budget or authority", err)
	}
}
