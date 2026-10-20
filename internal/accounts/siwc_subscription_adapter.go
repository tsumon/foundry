package accounts

import (
	"strings"
	"time"

	"foundry/internal/domain"
)

// SubscriptionAdapterPreparation is a read-only implementation diagnostic,
// not provider eligibility, credential readiness or an execution permission.
func (s *SIWCService) SubscriptionAdapterPreparation() *domain.OpenAISubscriptionAdapterPreparation {
	if s == nil {
		return nil
	}
	return &domain.OpenAISubscriptionAdapterPreparation{State: "implemented_disabled", Validation: "local_simulation_only"}
}

// validSubscriptionProfile separates the fixed protocol profile from entry
// admission. The owned production policy can enter the official core only
// after reserving an approved dispatch; ordinary production starts stay denied.
func (s *SIWCService) validSubscriptionProfile() bool {
	if s == nil || s.options.FlowTimeout < 10*time.Millisecond || s.options.FlowTimeout > 10*time.Minute {
		return false
	}
	origins := []string{s.options.AuthorizationOrigin, s.options.TokenOrigin, s.options.OIDCOrigin, s.options.JWKSOrigin, s.options.RevocationOrigin, s.options.Issuer}
	for _, origin := range origins {
		if s.simulation {
			if !validSIWCLoopbackOrigin(origin) {
				return false
			}
		} else if origin != officialSIWCOrigin {
			return false
		}
	}
	return true
}

func (s *SIWCService) subscriptionAccountEligible(account domain.ModelAccount) bool {
	if s.simulation {
		return s.LegacySimulationEligible(account)
	}
	return account.Enabled && account.Provider == "openai" && account.AuthMethod == "openai_siwc" && account.BillingMode == "subscription" && account.BaseURL == officialSIWCResource
}

func validSubscriptionClientID(clientID string) bool {
	if !strings.HasPrefix(clientID, "oaiapp_") || len(clientID) <= len("oaiapp_") || len(clientID) > 2048 {
		return false
	}
	for _, ch := range clientID[len("oaiapp_"):] {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-') {
			return false
		}
	}
	return true
}
