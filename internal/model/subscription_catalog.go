package model

import (
	"encoding/json"
	"foundry/internal/domain"
	"foundry/internal/jsontext"
	"net/http"
	"strings"
)

// SubscriptionCatalogFailure reuses the finite provider classification but
// intentionally discards provider identifiers, headers and echoed text.
func SubscriptionCatalogFailure(status int, body []byte) error {
	if status < 400 {
		return providerFailure("response_incomplete", "unknown")
	}
	if len(body) > 1<<20 {
		return providerFailure("transport_unknown", "unknown")
	}
	classified := classifyProviderError(status, http.Header{}, body)
	return providerFailure(classified.Code, classified.Certainty)
}

// DecodeSubscriptionCatalog projects bounded untrusted provider data into
// display-only evidence. No response body or credentials survive this boundary.
func DecodeSubscriptionCatalog(body []byte, secret string) ([]domain.ModelAcceptanceCatalogEntry, error) {
	fail := func() ([]domain.ModelAcceptanceCatalogEntry, error) {
		return nil, providerFailure("response_incomplete", "unknown")
	}
	if len(body) > 1<<20 || !jsontext.Lossless(body) {
		return fail()
	}
	var envelope struct {
		Models *[]struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
		} `json:"models"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.Models == nil {
		return fail()
	}
	entries := []domain.ModelAcceptanceCatalogEntry{}
	seen := map[string]bool{}
	for _, m := range *envelope.Models {
		if !domain.ValidModelCatalogIdentifier(m.Slug) || seen[m.Slug] {
			return fail()
		}
		seen[m.Slug] = true
		if !domain.ValidModelCatalogName(m.DisplayName) || secret != "" && (strings.Contains(m.Slug, secret) || strings.Contains(m.DisplayName, secret)) {
			return fail()
		}
		if m.Visibility != "list" {
			continue
		}
		if len(entries) >= 128 {
			return fail()
		}
		entries = append(entries, domain.ModelAcceptanceCatalogEntry{ID: m.Slug, DisplayName: m.DisplayName})
	}
	return entries, nil
}
