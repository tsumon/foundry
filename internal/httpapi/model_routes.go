package httpapi

import (
	"errors"
	"net/http"

	"foundry/internal/domain"
	"foundry/internal/store"
)

type modelAdapterView struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	AuthMethod  string `json:"auth_method"`
	Protocol    string `json:"protocol"`
	SpendClass  string `json:"spend_class"`
	Implemented bool   `json:"implemented"`
	LiveEnabled bool   `json:"live_enabled"`
}

var modelAdapterViews = []modelAdapterView{
	{ID: "compatible_api_responses", Provider: "custom", AuthMethod: "api_key", Protocol: "responses", SpendClass: "explicit_api", Implemented: true},
	{ID: "openai_api_chat", Provider: "openai", AuthMethod: "api_key", Protocol: "chat_completions", SpendClass: "explicit_api", Implemented: true},
	{ID: "openai_api_responses", Provider: "openai", AuthMethod: "api_key", Protocol: "responses", SpendClass: "explicit_api", Implemented: true},
	{ID: "anthropic_api_messages", Provider: "anthropic", AuthMethod: "api_key", Protocol: "messages", SpendClass: "explicit_api", Implemented: true},
	{ID: "zhipu_api_chat", Provider: "zhipu", AuthMethod: "api_key", Protocol: "chat_completions", SpendClass: "explicit_api", Implemented: true},
	{ID: "kimi_api_chat", Provider: "kimi", AuthMethod: "api_key", Protocol: "chat_completions", SpendClass: "explicit_api", Implemented: true},
	{ID: "kimi_code_chat", Provider: "kimi", AuthMethod: "api_key", Protocol: "chat_completions", SpendClass: "interactive_package", Implemented: true},
	{ID: "openai_siwc_responses", Provider: "openai", AuthMethod: "openai_siwc", Protocol: "responses", SpendClass: "subscription_only", Implemented: true},
}

type modelRuntimeView struct {
	DesktopSubscription     bool               `json:"desktop_subscription"`
	LiveAuthorization       bool               `json:"live_authorization"`
	LiveDiscovery           bool               `json:"live_discovery"`
	LiveInference           bool               `json:"live_inference"`
	SimulationInference     bool               `json:"simulation_inference"`
	SimulationAuthorization bool               `json:"simulation_authorization"`
	AutomaticPaidFallback   bool               `json:"automatic_paid_fallback"`
	Adapters                []modelAdapterView `json:"adapters"`
}

func (s *server) modelRuntimeView() modelRuntimeView {
	view := modelRuntimeView{Adapters: append([]modelAdapterView(nil), modelAdapterViews...)}
	view.DesktopSubscription = s.desktopAccounts != nil && !s.recoveryReadOnly && s.codingWorkbench
	// Display of a configured task transport only. Every actual send still needs
	// its independently claimed, sealed, current engine invocation.
	if s.codingWorkbench && !s.recoveryReadOnly && s.engine.CodingWorkbenchAvailable() {
		view.LiveInference = true
		for i := range view.Adapters {
			view.Adapters[i].LiveEnabled = view.Adapters[i].Provider == "openai" || view.Adapters[i].ID == "compatible_api_responses"
		}
	}
	if s.modelRouter != nil {
		view.SimulationInference = s.modelRouter.SimulationEnabled()
	}
	if s.siwc != nil {
		view.SimulationAuthorization = s.siwc.SimulationEnabled()
	}
	return view
}

func modelAdapter(id string) (modelAdapterView, bool) {
	for _, adapter := range modelAdapterViews {
		if adapter.ID == id {
			return adapter, true
		}
	}
	return modelAdapterView{}, false
}

func (s *server) modelRoutesRoute(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/model-routes" || r.URL.RawQuery != "" {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		routes, err := s.store.ListModelRoutes()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "could not list model routes")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"routes": routes, "runtime": s.modelRuntimeView()})
	case http.MethodPost:
		var request struct {
			AccountID               string `json:"account_id"`
			ExpectedAccountRevision int64  `json:"expected_account_revision"`
			Adapter                 string `json:"adapter"`
			Environment             string `json:"environment"`
			ExtraUsage              string `json:"extra_usage"`
		}
		if err := decodeAccountJSON(w, r, &request); err != nil {
			writeDecodeError(w, err)
			return
		}
		adapter, ok := modelAdapter(request.Adapter)
		if !ok || request.AccountID == "" || request.ExpectedAccountRevision < 1 {
			writeError(w, http.StatusBadRequest, "invalid_model_route", "invalid model route request")
			return
		}
		account, err := s.store.GetModelAccount(request.AccountID)
		if err != nil {
			writeModelRouteError(w, err)
			return
		}
		id, err := opaqueID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "could not create model route")
			return
		}
		route := domain.ModelRoute{
			ID: id, AccountID: request.AccountID, Adapter: adapter.ID,
			Protocol: adapter.Protocol, BaseURL: account.BaseURL,
			Environment: request.Environment, SpendClass: adapter.SpendClass,
			ExtraUsage: request.ExtraUsage,
		}
		if adapter.Provider != account.Provider || adapter.AuthMethod != account.AuthMethod || domain.ValidateModelRoute(route, account) != nil {
			writeError(w, http.StatusBadRequest, "invalid_model_route", "invalid model route request")
			return
		}
		route, err = s.store.CreateModelRouteAtAccountRevision(route, request.ExpectedAccountRevision)
		if err != nil {
			writeModelRouteError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"route": route})
	default:
		writeError(w, http.StatusNotFound, "not_found", "route not found")
	}
}

func writeModelRouteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrConflict):
		writeMappedError(w, err)
	case errors.Is(err, domain.ErrInvalidModelRoute):
		writeError(w, http.StatusBadRequest, "invalid_model_route", "invalid model route request")
	default:
		writeError(w, http.StatusInternalServerError, "internal", "model route operation failed")
	}
}
