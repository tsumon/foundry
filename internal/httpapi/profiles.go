package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"foundry/internal/domain"
	"foundry/internal/model"
)

type profileRequest struct {
	AccountID string `json:"account_id"`
	RouteID   string `json:"route_id"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Protocol  string `json:"protocol"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	APIKey    string `json:"api_key"`
}

func (s *server) listProfiles(w http.ResponseWriter) {
	profiles, err := s.store.ListProfiles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not list profiles")
		return
	}
	settings, err := s.store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not read settings")
		return
	}
	views := make([]safeAccountProfile, 0, len(profiles))
	for _, p := range profiles {
		v, err := s.profileView(p)
		if err != nil {
			writeAccountError(w, err)
			return
		}
		views = append(views, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": views, "settings": settings})
}

func (s *server) saveProfile(w http.ResponseWriter, r *http.Request) {
	var request profileRequest
	if err := decodeAccountJSON(w, r, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	if err := validateProfileRequest(request); err != nil {
		writeError(w, http.StatusBadRequest, "validation", "profile fields are invalid")
		return
	}
	s.profileMu.Lock()
	defer s.profileMu.Unlock()

	var existing *domain.Profile
	if request.ID != "" {
		profile, err := s.store.GetProfile(request.ID)
		if err != nil {
			writeMappedError(w, err)
			return
		}
		existing = &profile
		if profile.HasKey && profile.BaseURL != request.BaseURL {
			writeError(w, http.StatusConflict, "credential_origin", "a profile with a credential cannot move to a new endpoint; create a new profile")
			return
		}
		if profile.HasKey && request.APIKey != "" {
			writeError(w, http.StatusConflict, "legacy_credential_immutable", "existing credentials cannot be replaced; create an account or a new profile")
			return
		}
		if profile.AccountID != request.AccountID || profile.AccountID != "" && profile.BaseURL != request.BaseURL {
			writeError(w, http.StatusConflict, "account_binding_immutable", "create a new profile for a different account or endpoint")
			return
		}
		if profile.RouteID != request.RouteID {
			writeError(w, http.StatusConflict, "route_binding_immutable", "create a new profile for a different model route")
			return
		}
	} else {
		id, err := opaqueID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "could not create profile")
			return
		}
		request.ID = id
	}

	profile := domain.Profile{
		AccountID: request.AccountID, RouteID: request.RouteID,
		ID: request.ID, Name: strings.TrimSpace(request.Name), Kind: request.Kind,
		Protocol: request.Protocol, BaseURL: request.BaseURL, Model: strings.TrimSpace(request.Model),
	}
	if existing != nil {
		profile.HasKey = existing.HasKey
	}
	key := request.APIKey
	if request.AccountID != "" {
		account, err := s.store.GetModelAccount(request.AccountID)
		if err != nil {
			writeAccountError(w, err)
			return
		}
		if request.APIKey != "" || account.BaseURL != profile.BaseURL {
			writeError(w, http.StatusBadRequest, "invalid_model_account", "account profile requires its fixed endpoint and no inline credential")
			return
		}
		if request.RouteID != "" {
			route, err := s.store.GetModelRoute(request.RouteID)
			if err != nil {
				writeMappedError(w, err)
				return
			}
			if route.AccountID != account.ID || route.BaseURL != profile.BaseURL || route.Protocol != profile.Protocol || domain.ValidateModelRoute(route, account) != nil {
				writeError(w, http.StatusBadRequest, "invalid_model_route", "profile route does not match its account, endpoint, or protocol")
				return
			}
		}
	}
	if key != "" {
		if err := s.secrets.Set(profile.ID, key); err != nil {
			writeError(w, http.StatusInternalServerError, "credential_storage", "could not store provider credential")
			return
		}
		profile.HasKey = true
	}
	if err := s.store.PutProfile(profile); err != nil {
		if key != "" {
			_ = s.secrets.Delete(profile.ID)
		}
		writeMappedError(w, err)
		return
	}
	view, err := s.profileView(profile)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profile": view})
}

func validateProfileRequest(request profileRequest) error {
	if strings.TrimSpace(request.Name) == "" || len(request.Name) > 200 {
		return errors.New("bounded profile name is required")
	}
	switch request.Kind {
	case "direct", "compatible", "magpie":
	default:
		return errors.New("provider kind must be direct, compatible, or magpie")
	}
	if request.Protocol != "chat_completions" && request.Protocol != "responses" && !(request.RouteID != "" && request.Protocol == "messages") {
		return errors.New("protocol must be chat_completions or responses")
	}
	if request.RouteID != "" && request.AccountID == "" {
		return errors.New("a model route requires an account")
	}
	if strings.TrimSpace(request.Model) == "" {
		return errors.New("provider model is required")
	}
	_, _, err := domain.ParseProviderBaseURL(request.BaseURL)
	return err
}

func opaqueID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (s *server) profileRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/profiles/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "probe" {
		writeError(w, http.StatusNotFound, "not_found", "route not found")
		return
	}
	var request struct{}
	if err := decodeJSON(w, r, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	profile, err := s.store.GetProfile(parts[0])
	if err != nil {
		writeMappedError(w, err)
		return
	}
	if profile.RouteID != "" {
		selection, err := s.store.ResolveModelProfile(profile.ID)
		if err != nil {
			writeMappedError(w, err)
			return
		}
		catalog, err := s.modelRouter.ProbeSelection(r.Context(), 2, selection)
		if err != nil {
			if writeModelProbeError(w, err) {
				return
			}
			writeError(w, http.StatusBadRequest, "probe_failed", "provider model directory is unavailable")
			return
		}
		models := make([]string, len(catalog))
		for i := range catalog {
			models[i] = catalog[i].ID
		}
		writeJSON(w, http.StatusOK, map[string]any{"models": models, "catalog": catalog, "inference_tested": false})
		return
	}
	models, err := s.client.Probe(r.Context(), profile)
	if err != nil {
		if errors.Is(err, model.ErrAccountInferenceDisabled) {
			writeMappedError(w, err)
			return
		}
		writeError(w, http.StatusBadRequest, "probe_failed", "provider model directory is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": models, "inference_tested": false})
}

func writeModelProbeError(w http.ResponseWriter, err error) bool {
	var provider *model.ProviderError
	if !errors.As(err, &provider) {
		return false
	}
	switch provider.Code {
	case "live_provider_access_pending_confirmation":
		writeError(w, http.StatusConflict, provider.Code, "live provider access is pending confirmation")
	case "auth_invalid", "entitlement_denied", "quota_exhausted", "rate_limited", "request_rejected", "server_error", "transport_unknown", "response_incomplete":
		writeError(w, http.StatusBadRequest, "probe_failed", "provider model directory is unavailable")
	default:
		writeError(w, http.StatusBadRequest, "probe_failed", "provider model directory is unavailable")
	}
	return true
}

func (s *server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		DefaultProfileID string                 `json:"default_profile_id"`
		RoleBindings     map[domain.Role]string `json:"role_bindings"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	settings := domain.Settings{DefaultProfileID: request.DefaultProfileID, RoleBindings: request.RoleBindings}
	if err := s.validateSettings(settings); err != nil {
		writeMappedError(w, err)
		return
	}
	if err := s.store.PutSettings(settings); err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "could not save settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

func (s *server) validateSettings(settings domain.Settings) error {
	allowed := map[domain.Role]bool{
		domain.Main: true, domain.Researcher: true, domain.Planner: true,
		domain.Worker: true, domain.Debugger: true, domain.QA: true, domain.Auditor: true,
	}
	ids := []string{settings.DefaultProfileID}
	for role, id := range settings.RoleBindings {
		if !allowed[role] {
			return errors.New("unknown role binding")
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, err := s.store.GetProfile(id); err != nil {
			return err
		}
	}
	if settings.RoleBindings == nil {
		settings.RoleBindings = map[domain.Role]string{}
	}
	return nil
}

func (s *server) discoverMagpie(w http.ResponseWriter, r *http.Request) {
	var request struct {
		BaseURL string `json:"base_url"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeDecodeError(w, err)
		return
	}
	if _, loopback, err := domain.ParseProviderBaseURL(request.BaseURL); err != nil || !loopback {
		writeError(w, http.StatusBadRequest, "validation", "Magpie discovery requires a valid loopback base URL")
		return
	}
	profile := domain.Profile{ID: "magpie-discovery", Kind: "magpie", Protocol: "chat_completions", BaseURL: request.BaseURL, Model: "directory-only"}
	models, err := s.client.Probe(r.Context(), profile)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"available": false, "models": []string{},
			"message": "Magpie model directory is unavailable at this address; no inference was attempted.",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"available": true, "models": models,
		"message": "Magpie model directory is available; inference was not tested.",
	})
}
