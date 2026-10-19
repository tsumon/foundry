package engine

import (
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
)

func modelFailureReceipt(result model.Result, err error) (string, string, *domain.ModelCallReceipt) {
	status, code := "outcome_unknown", "model_transport_unknown"
	r := domain.ModelCallReceipt{RequestID: result.RequestID, ResolvedModel: result.ResolvedModel, CredentialGeneration: result.CredentialGeneration, Certainty: "unknown"}
	var p *model.ProviderError
	if errors.As(err, &p) && p != nil {
		code = p.Error()
		if code == "model_provider_error" {
			code = "model_transport_unknown"
		} else if p.Certainty == "not_sent" || p.Certainty == "rejected" {
			status = "failed"
			r.Certainty = p.Certainty
		}
		r.RequestID = p.RequestID
		r.HTTPStatus = p.HTTPStatus
		r.ProviderCode = p.ProviderCode
		r.ProviderParam = p.ProviderParam
		r.BodyShape = p.BodyShape
		r.RetryAfterSeconds = p.RetryAfterSeconds
	}
	r = domain.SanitizeModelCallReceipt(r)
	return status, code, &r
}

func unknownModelUsage(input controlledInput, result model.Result, err error) domain.CallUsage {
	if input.SnapshotSchema != 2 {
		return domain.CallUsage{}
	}
	_, _, receipt := modelFailureReceipt(result, err)
	receipt.Certainty = "unknown"
	return domain.CallUsage{ModelReceipt: receipt}
}
