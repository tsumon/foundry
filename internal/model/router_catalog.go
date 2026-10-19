package model

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"foundry/internal/domain"
)

// CatalogModel is display-only untrusted metadata, never a route or authority.
// ID is the model slug for SIWC and the model id for standard API catalogs.
type CatalogModel struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// ProbeSelection is explicit discovery, not inference verification. There is no
// automatic probing or catalog cache (in particular no cross-account cache).
func (r *Router) ProbeSelection(ctx context.Context, schema int, s domain.RoleModelSelection) ([]CatalogModel, error) {
	if err := r.PreflightModelSelection(ctx, schema, s); err != nil {
		return nil, err
	}
	if s.Account == nil || s.Route == nil {
		return nil, providerFailure("request_rejected", "not_sent")
	}
	response, _, err := r.send(ctx, s, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, providerFailure("response_incomplete", "unknown")
	}
	body, err := readBounded(response.Body)
	if err != nil {
		return nil, providerFailure("response_incomplete", "unknown")
	}
	var envelope struct {
		Models *[]struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
		} `json:"models"`
		Data *[]struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return nil, providerFailure("response_incomplete", "unknown")
	}
	models := make([]CatalogModel, 0)
	if s.Route.Adapter == "openai_siwc_responses" {
		if envelope.Models == nil {
			return nil, providerFailure("response_incomplete", "unknown")
		}
		for _, m := range *envelope.Models {
			if m.Visibility != "list" {
				continue
			}
			if safeIdentifier(m.Slug) == "" || !validCatalogName(m.DisplayName) || echoesRequestCredential(response.Request, m.Slug, m.DisplayName) {
				return nil, providerFailure("response_incomplete", "unknown")
			}
			models = append(models, CatalogModel{ID: m.Slug, DisplayName: m.DisplayName})
		}
	} else {
		if envelope.Data == nil {
			return nil, providerFailure("response_incomplete", "unknown")
		}
		for _, m := range *envelope.Data {
			if safeIdentifier(m.ID) == "" {
				return nil, providerFailure("response_incomplete", "unknown")
			}
			name := m.DisplayName
			if name == "" {
				name = m.ID
			}
			if !validCatalogName(name) || echoesRequestCredential(response.Request, m.ID, name) {
				return nil, providerFailure("response_incomplete", "unknown")
			}
			models = append(models, CatalogModel{ID: m.ID, DisplayName: name})
		}
	}
	return models, nil
}

func validCatalogName(s string) bool {
	return utf8.ValidString(s) && strings.TrimSpace(s) != "" && utf8.RuneCountInString(s) <= 200 && !strings.ContainsAny(s, "<>") && strings.IndexFunc(s, unicode.IsControl) < 0
}
