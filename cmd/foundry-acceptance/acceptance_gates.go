package main

import (
	"context"
	"encoding/json"
	"errors"
	"foundry/internal/acceptance"
	"foundry/internal/accounts"
	"foundry/internal/domain"
	"foundry/internal/model"
	"io"
	"net/http"
	"reflect"
	"strings"
)

func startAcceptanceGates(r *acceptanceRuntime, accountService *accounts.Service) error {
	var e error
	r.gateProtocol, e = newProtocolFixture()
	if e != nil {
		return e
	}
	r.gateProtocol.mu.Lock()
	r.gateProtocol.acceptanceGates = true
	r.gateProtocol.mu.Unlock()
	r.gateSIWC, e = accounts.NewSIWCSimulation(accountService, r.gateProtocol.siwcOptions())
	if e != nil {
		return e
	}
	routes := []domain.ModelRoute{}
	identityRoutes := []string{}
	subscriptionRoutes := []string{}
	for _, fixture := range []string{"openai", "kimi", "openai-identity", "openai-subscription"} {
		provider := fixture
		if fixture == "openai-identity" || fixture == "openai-subscription" {
			provider = "openai"
		}
		a := domain.ModelAccount{ID: "acceptance-gate-" + provider, Name: "SYNTHETIC acceptance gates — " + provider, Provider: provider, BaseURL: r.gateProtocol.modelBaseURL(), AuthMethod: "openai_siwc", BillingMode: "subscription", Enabled: true}
		if fixture == "openai-identity" {
			a.ID = "acceptance-gate-openai-identity"
			a.Name = "SYNTHETIC OpenAI — identity only"
		} else if fixture == "openai-subscription" {
			a.ID = "acceptance-gate-openai-subscription"
			a.Name = "SYNTHETIC OpenAI — subscription connection"
		}
		route := domain.ModelRoute{ID: a.ID + "-route", AccountID: a.ID, Adapter: "openai_siwc_responses", Protocol: "responses", Environment: "simulation", SpendClass: "subscription_only", ExtraUsage: "off"}
		if provider == "kimi" {
			a.BaseURL = r.gateProtocol.kimiBaseURL()
			a.AuthMethod = "api_key"
			route.Adapter = "kimi_code_chat"
			route.Protocol = "chat_completions"
			route.SpendClass = "interactive_package"
		}
		route.BaseURL = a.BaseURL
		if _, e = r.store.SaveModelAccount(a, 0); e != nil {
			return e
		}
		route, e = r.store.CreateModelRoute(route)
		if e != nil {
			return e
		}
		routes = append(routes, route)
		if fixture == "openai-identity" {
			identityRoutes = append(identityRoutes, route.ID)
		} else if fixture == "openai-subscription" {
			subscriptionRoutes = append(subscriptionRoutes, route.ID)
		}
	}
	router := model.NewRouter(nil, accountService, model.RouterOptions{SimulationOrigins: []string{r.gateProtocol.modelOrigin()}})
	r.gates = acceptance.NewSimulation(r.store, acceptance.Options{Routes: routes, IdentityOnlyRouteIDs: identityRoutes, SubscriptionConnectionRouteIDs: subscriptionRoutes, Router: router, Connect: func(ctx context.Context, m domain.ModelAcceptanceMaterial) error {
		switch m.Account.Provider {
		case "openai":
			if m.ConnectionPurpose == "identity_only" {
				return completeOwnedIdentityFixtureAuthorization(ctx, r.gateSIWC, m.Account, "identity")
			}
			if m.ConnectionPurpose == "subscription_only" {
				if !m.ValidConnectionPurpose() || m.RequestedResource != domain.OpenAISubscriptionRequestedResource || !reflect.DeepEqual(m.RequestedScopes, []string{"openid", "profile", "email", "offline_access", "resource.invoke", "chatgpt.tokens.use.direct"}) {
					return errors.New("invalid synthetic subscription permission")
				}
				return completeOwnedSubscriptionConnectionAuthorization(ctx, r.gateSIWC, m.Account, "approve")
			}
			return completeOwnedFixtureAuthorization(ctx, r.gateSIWC, m.Account, "approve")
		case "kimi":
			op, e := randomFixtureValue(18)
			if e != nil {
				return e
			}
			_, e = accountService.AddCredential(ctx, m.Account.ID, m.Account.Revision, "gate-"+op, r.gateProtocol.kimiKey)
			return e
		}
		return errors.New("unsupported synthetic connection")
	}})
	return r.gates.Recover()
}

var gateCatalog = []string{"synthetic-siwc-success-gate", "synthetic-quota-exhausted", "synthetic-permission-denied", "synthetic-rate-limited", "synthetic-sse-outcome-unknown"}

func (f *protocolFixture) handleGateModel(w http.ResponseWriter, r *http.Request) {
	if !f.authorized(r) || r.Header.Get("User-Agent") != "Foundry/0.1.0" {
		f.providerError(w, 401, "invalid_api_key")
		return
	}
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
		f.mu.Lock()
		f.directories++
		siwc := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		f.mu.Unlock()
		items := []map[string]string{}
		for _, id := range gateCatalog {
			if siwc {
				items = append(items, map[string]string{"slug": id, "display_name": id, "visibility": "list"})
			} else {
				items = append(items, map[string]string{"id": id, "display_name": id})
			}
		}
		key := "data"
		if siwc {
			key = "models"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{key: items})
		return
	}
	if r.Method != http.MethodPost || (!strings.HasSuffix(r.URL.Path, "/responses") && !strings.HasSuffix(r.URL.Path, "/chat/completions")) {
		http.NotFound(w, r)
		return
	}
	raw, e := io.ReadAll(io.LimitReader(r.Body, 8192))
	var payload map[string]json.RawMessage
	if e != nil || json.Unmarshal(raw, &payload) != nil {
		f.providerError(w, 400, "invalid_request_error")
		return
	}
	var id string
	if json.Unmarshal(payload["model"], &id) != nil {
		f.providerError(w, 400, "invalid_request_error")
		return
	}
	// Assert fixed synthetic inputs and absence of tool or repository material at
	// the owned HTTP boundary, not just at the service call site.
	if !strings.Contains(string(raw), domain.AcceptancePrompt) || !strings.Contains(string(raw), domain.AcceptanceSystem) || payload["tools"] != nil {
		f.setInvalid("acceptance request was not fixed synthetic input")
		f.providerError(w, 400, "invalid_request_error")
		return
	}
	f.mu.Lock()
	f.modelHits[id]++
	f.mu.Unlock()
	switch id {
	case "synthetic-quota-exhausted":
		f.providerError(w, 429, "insufficient_quota")
	case "synthetic-permission-denied":
		f.providerError(w, 403, "permission_error")
	case "synthetic-rate-limited":
		f.providerError(w, 429, "rate_limit_exceeded")
	case "synthetic-sse-outcome-unknown":
		if strings.HasSuffix(r.URL.Path, "/responses") {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n")
		} else {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[`)
		}
	case "synthetic-siwc-success-gate":
		if strings.HasSuffix(r.URL.Path, "/responses") {
			f.completedSSE(w, id, "synthetic output")
		} else {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "synthetic-response", "object": "chat.completion", "model": id, "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"role": "assistant", "content": "synthetic output"}}}, "usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 7}})
		}
	default:
		f.providerError(w, 400, "model_not_found")
	}
}
