package acceptance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"
)

type ProductionInferenceOptions struct {
	Route    domain.ModelRoute
	Resolver model.CredentialResolver
}
type ProductionInferenceStatus struct {
	Enabled   bool
	Blocker   string
	Admission *domain.ProductionAdmission
}
type productionInference struct {
	route     domain.ModelRoute
	admission domain.ProductionAdmission
	resolver  model.CredentialResolver
	http      *http.Client
}

func NewProductionInference(ledger *store.Store, options ProductionInferenceOptions) *Service {
	s := New(ledger)
	s.inference = &productionInference{route: options.Route, resolver: options.Resolver}
	if options.Route.AccountID != "" {
		s.ownedAccounts[options.Route.AccountID] = true
	}
	saved, e := ledger.GetModelRoute(options.Route.ID)
	a, ae := ledger.GetModelAccount(options.Route.AccountID)
	if e != nil || ae != nil || saved != options.Route || domain.ValidateModelRoute(saved, a) != nil || !a.Enabled || a.Provider != "openai" || a.AuthMethod != "openai_siwc" || a.BillingMode != "subscription" || a.BaseURL != domain.OpenAISubscriptionRequestedResource || saved.Environment != "live" || saved.Adapter != "openai_siwc_responses" || saved.Protocol != "responses" || saved.SpendClass != "subscription_only" || saved.ExtraUsage != "off" || saved.BaseURL != domain.OpenAISubscriptionRequestedResource || options.Resolver == nil {
		return s
	}
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return s
	}
	s.inference.admission = domain.ProductionAdmission{Generation: hex.EncodeToString(nonce), Profile: "openai_siwc_inference_v1"}
	// Exclusive fresh connection: never proxy, redirect, retry, or reuse HTTP/2.
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ForceAttemptHTTP2: false, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second}
	s.inference.http = &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	s.routes[saved.ID] = saved
	return s
}
func (s *Service) ProductionInferenceStatus(account string) ProductionInferenceStatus {
	status := ProductionInferenceStatus{}
	if s.inference == nil || !s.OwnsAccount(account) {
		return status
	}
	if !s.inference.admission.Valid() {
		status.Blocker = "admission_unavailable"
		return status
	}
	copy := s.inference.admission
	status.Admission = &copy
	if !s.ready.Load() {
		status.Blocker = "admission_unavailable"
		return status
	}
	saved, e := s.store.GetModelRoute(s.inference.route.ID)
	if e != nil || saved != s.inference.route {
		status.Blocker = "admission_unavailable"
		return status
	}
	status.Blocker = s.store.ProductionInferenceReadiness(account, s.inference.route.ID, s.inference.admission, s.now())
	status.Enabled = status.Blocker == ""
	return status
}
func (s *Service) ownedInference(account, id string) (domain.ModelAcceptanceOperation, error) {
	if !s.ready.Load() || !s.OwnsAccount(account) || !s.inference.admission.Valid() {
		return domain.ModelAcceptanceOperation{}, ErrDisabled
	}
	o, e := s.Get(account, id)
	if e != nil {
		return s.mutationResult(o, e)
	}
	m := o.Material
	if o.Source != "production_inference" || o.LiveAccess != "inference_once" || o.Phase != "inference" || m.ProductionAdmission == nil || *m.ProductionAdmission != s.inference.admission || !m.ValidProductionInference() || m.Route != s.inference.route {
		return domain.ModelAcceptanceOperation{}, ErrDisabled
	}
	return o, nil
}
func (s *Service) executeInference(ctx context.Context, o domain.ModelAcceptanceOperation) (domain.ModelAcceptanceOperation, error) {
	if e := s.store.ReserveProductionInferenceDispatch(o, s.inference.admission, s.now()); e != nil {
		return s.finishInference(o, model.Result{}, catalogFailure("request_rejected", "not_sent"))
	}
	started, _ := time.Parse(time.RFC3339Nano, o.StartedAt)
	expiry, _ := time.Parse(time.RFC3339Nano, o.Material.ExpiresAt)
	deadline := started.Add(30 * time.Second)
	if expiry.Before(deadline) {
		deadline = expiry
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	result, e := s.requestInference(ctx, o)
	if e == nil && ctx.Err() != nil {
		e = catalogFailure("transport_unknown", "unknown")
	}
	return s.finishInference(o, result, e)
}
func (s *Service) requestInference(ctx context.Context, o domain.ModelAcceptanceOperation) (model.Result, error) {
	fail := func(code, certainty string) (model.Result, error) {
		return model.Result{}, catalogFailure(code, certainty)
	}
	validate := func() bool {
		return ctx.Err() == nil && s.ready.Load() && s.store.ValidateProductionInferenceDispatch(o, s.inference.admission, s.now()) == nil
	}
	if !validate() {
		return fail("request_rejected", "not_sent")
	}
	lease, e := s.inference.resolver.ResolveModelCredential(ctx, o.Material.Selection())
	if e != nil || lease.Secret == "" || strings.ContainsAny(lease.Secret, "\r\n") || lease.Generation != o.Material.Authorization.Generation {
		return fail("auth_invalid", "not_sent")
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, domain.OpenAIInferenceURL, strings.NewReader(domain.OpenAIInferenceBody))
	if e != nil {
		return fail("request_rejected", "not_sent")
	}
	// No replay body, even for a redirect-capable client.
	req.GetBody = nil
	req.Header.Set("Authorization", "Bearer "+lease.Secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "Foundry/0.1.0")
	if !validate() {
		return fail("auth_invalid", "not_sent")
	}
	response, e := s.inference.http.Do(req)
	if e != nil {
		return fail("transport_unknown", "unknown")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		if response.StatusCode < 400 {
			return fail("response_incomplete", "unknown")
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
		if err != nil {
			return fail("transport_unknown", "unknown")
		}
		return model.Result{}, model.SubscriptionInferenceHTTPFailure(response.StatusCode, response.Header, body, lease.Secret)
	}
	media, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if e != nil || media != "text/event-stream" {
		return fail("response_incomplete", "unknown")
	}
	result, e := model.DecodeSubscriptionInference(response.Body, lease.Secret)
	id := response.Header.Get("X-Request-ID")
	if domain.ValidModelCatalogIdentifier(id) && !strings.Contains(id, lease.Secret) {
		if e == nil {
			result.RequestID = id
		} else {
			var pe *model.ProviderError
			if errors.As(e, &pe) && pe.Certainty == "rejected" {
				pe.RequestID = id
			}
		}
	}
	return result, e
}
func (s *Service) finishInference(o domain.ModelAcceptanceOperation, result model.Result, cause error) (domain.ModelAcceptanceOperation, error) {
	state, certainty, code := "succeeded", "completed", ""
	evidence := domain.InferenceEvidence{}
	if cause == nil {
		matched := strings.TrimSpace(result.Text) == domain.OpenAIInferenceExpected
		evidence = domain.InferenceEvidence{TerminalEvent: "response.completed", OutputMatched: &matched, OutputDigest: domain.AcceptanceDigest(result.Text), RequestID: result.RequestID, ResolvedModel: result.ResolvedModel}
	} else {
		result = model.Result{}
		state, certainty, code = "unknown", "unknown", "transport_unknown"
		var pe *model.ProviderError
		if errors.As(cause, &pe) {
			code = pe.Error()
			if pe.Certainty == "rejected" || pe.Certainty == "not_sent" {
				state, certainty = "failed", pe.Certainty
			}
			if pe.Certainty == "rejected" {
				evidence.RequestID = pe.RequestID
				evidence.ProviderCode = pe.ProviderCode
				evidence.ProviderParam = pe.ProviderParam
			}
		}
		var streamFailure *model.SubscriptionInferenceFailure
		if errors.As(cause, &streamFailure) {
			evidence.TerminalEvent = streamFailure.TerminalEvent
			result.InputTokens = streamFailure.InputTokens
			result.OutputTokens = streamFailure.OutputTokens
			result.UsageKnown = streamFailure.UsageKnown
		}
	}
	saved, e := s.store.FinishProductionInference(o, s.inference.admission, state, certainty, code, evidence, result.InputTokens, result.OutputTokens, result.UsageKnown, s.now())
	if e != nil {
		s.ready.Store(false)
		_, _ = s.store.FinishProductionInference(o, s.inference.admission, "unknown", "unknown", "transport_unknown", domain.InferenceEvidence{}, 0, 0, false, s.now())
		return domain.ModelAcceptanceOperation{}, ErrPersistence
	}
	return saved, nil
}
