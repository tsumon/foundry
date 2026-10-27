package store

import (
	"foundry/internal/domain"
	"testing"
	"time"
)

type inferenceSendLedger interface {
	BeginProductionInferenceSend(domain.ModelAcceptanceOperation, domain.ProductionAdmission, time.Time) error
}

func TestProductionInferenceWaitAndDurableSendWindow(t *testing.T) {
	for _, mode := range []string{"delayed read", "missing send", "late response", "future send", "replay", "immutable", "expired material", "forged local error"} {
		t.Run(mode, func(t *testing.T) {
			s, a, r, c, admission := inferenceLedgerFixture(t)
			sender, ok := any(s).(inferenceSendLedger)
			if !ok {
				t.Fatal("durable network start boundary missing")
			}
			now := time.Now()
			o, e := s.ProposeProductionInference(a.ID, inferenceProposal(a, r, c), admission, now)
			if e != nil {
				t.Fatal(e)
			}
			o, e = s.DecideModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, "approve", now)
			if e != nil {
				t.Fatal(e)
			}
			o, e = s.ClaimModelAcceptance(a.ID, o.ID, o.Revision, o.MaterialDigest, now)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.ReserveProductionInferenceDispatch(o, admission, now); e != nil {
				t.Fatal(e)
			}
			sendAt := now.Add(68 * time.Second)
			if e = s.ValidateProductionInferenceDispatch(o, admission, sendAt); e != nil {
				t.Fatal("credential wait consumed transport allowance", e)
			}
			if mode == "expired material" {
				expiry, _ := time.Parse(time.RFC3339Nano, o.Material.ExpiresAt)
				if sender.BeginProductionInferenceSend(o, admission, expiry) == nil {
					t.Fatal("expired material sent")
				}
				return
			}
			if mode != "missing send" {
				if e = sender.BeginProductionInferenceSend(o, admission, sendAt); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "replay" {
				if sender.BeginProductionInferenceSend(o, admission, sendAt) == nil {
					t.Fatal("network start replayed")
				}
				return
			}
			if mode == "forged local error" {
				if _, e = s.FinishProductionInference(o, admission, "failed", "not_sent", "credential_access_failed", domain.InferenceEvidence{}, 0, 0, false, sendAt); e == nil {
					t.Fatal("post-send receipt mislabeled local credential failure")
				}
				return
			}
			if mode == "immutable" {
				if _, e = s.db.Exec(`UPDATE model_acceptance_inference_sends SET started_at=? WHERE operation_id=?`, now.Format(time.RFC3339Nano), o.ID); e == nil {
					t.Fatal("send checkpoint rewritten")
				}
				if _, e = s.db.Exec(`DELETE FROM model_acceptance_inference_sends WHERE operation_id=?`, o.ID); e == nil {
					t.Fatal("send checkpoint deleted")
				}
				return
			}
			finished := sendAt.Add(time.Second)
			if mode == "late response" {
				finished = sendAt.Add(30 * time.Second)
			}
			if mode == "future send" {
				finished = sendAt.Add(-time.Second)
			}
			matched := true
			evidence := domain.InferenceEvidence{TerminalEvent: "response.completed", ResolvedModel: "gpt-6.1-sol", OutputMatched: &matched, OutputDigest: domain.AcceptanceDigest("FOUNDRY_INFERENCE_OK")}
			_, e = s.FinishProductionInference(o, admission, "succeeded", "completed", "", evidence, 0, 0, false, finished)
			if mode == "delayed read" && e != nil {
				t.Fatal("timely network response rejected", e)
			}
			if mode != "delayed read" && e == nil {
				t.Fatal("unsafe success", mode)
			}
		})
	}
}
