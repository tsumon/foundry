package store

import (
	"context"
	"reflect"
	"testing"

	"foundry/internal/domain"
)

func TestOpenAIPreparationProjectionIsHonestAndImmutable(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		auth     string
		base     string
		want     string
	}{
		{"official", "openai", "openai_siwc", "https://api.openai.com/v1", "exact_official"},
		{"loopback is not ownership", "openai", "openai_siwc", "http://127.0.0.1:43123/v1", "owned_simulation_required"},
		{"official suffix", "openai", "openai_siwc", "https://api.openai.com/v1/extra", "invalid"},
		{"api key excluded", "openai", "api_key", "https://api.openai.com/v1", ""},
		{"kimi excluded", "kimi", "api_key", "https://api.kimi.com/coding/v1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := openTestStore(t)
			a, err := s.SaveModelAccount(domain.ModelAccount{ID: "preparation", Name: "Synthetic", Provider: tt.provider, BaseURL: tt.base, AuthMethod: tt.auth, BillingMode: "subscription", Enabled: true}, 0)
			if err != nil {
				// Invalid legacy metadata is inserted directly so the read projection
				// remains honest even if persisted state predates validation.
				_, err = s.db.Exec(`INSERT INTO model_accounts VALUES('preparation',1,'Synthetic',?,?,?,'subscription',1,0,'now','now')`, tt.provider, tt.base, tt.auth)
				a.ID = "preparation"
			}
			if err != nil {
				t.Fatal(err)
			}
			before := acceptanceSnapshot(t, s)
			report, err := s.ReadModelAccountAcceptance(context.Background(), a.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == "" {
				if report.OpenAIPreparation != nil {
					t.Fatalf("unexpected preparation %+v", report.OpenAIPreparation)
				}
			} else {
				want := &domain.OpenAIPreparation{Scope: "openai_subscription_stage0", EndpointPolicy: tt.want, CredentialStorage: "not_checked", RemoteBilling: "unverified", Quota: "unknown", RealConnection: "blocked", RealCatalog: "blocked", RealInference: "blocked"}
				if !reflect.DeepEqual(report.OpenAIPreparation, want) {
					t.Fatalf("preparation=%+v want %+v", report.OpenAIPreparation, want)
				}
			}
			if !reflect.DeepEqual(before, acceptanceSnapshot(t, s)) {
				t.Fatal("projection mutated account storage")
			}
		})
	}
}
