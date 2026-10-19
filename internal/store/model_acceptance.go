package store

import (
	"context"
	"database/sql"
	"errors"
	"foundry/internal/domain"
	"foundry/internal/model"
	"regexp"
	"time"
)

// The diagnostic projection is narrower than legacy account/route validation.
var acceptanceIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

func (s *Store) ReadModelAccountAcceptance(ctx context.Context, accountID string) (domain.ModelAccountAcceptance, error) {
	var zero domain.ModelAccountAcceptance
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	a, err := scanModelAccount(tx.QueryRowContext(ctx, `SELECT `+modelAccountColumns+` FROM model_accounts WHERE id=?`, accountID))
	if err != nil {
		return zero, err
	}
	if !acceptanceIdentifierPattern.MatchString(a.ID) {
		return zero, errors.New("invalid model acceptance metadata")
	}
	reasons := []string{}
	if !a.Enabled {
		reasons = append(reasons, "account_disabled")
	}
	if a.AuthMethod == "api_key" {
		var available, unresolved bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_credential_versions WHERE account_id=? AND version=? AND state='available' AND cleanup_pending=0),EXISTS(SELECT 1 FROM model_credential_versions WHERE account_id=? AND (state='pending' OR cleanup_pending=1))`, a.ID, a.CurrentCredentialVersion, a.ID).Scan(&available, &unresolved)
		if err != nil {
			return zero, err
		}
		if !available || unresolved {
			reasons = append(reasons, "credential_metadata_unavailable")
		}
	} else if a.AuthMethod == "openai_siwc" {
		auth, readErr := scanModelAuthorization(tx.QueryRowContext(ctx, `SELECT `+authorizationColumns+` FROM model_authorizations WHERE account_id=? ORDER BY rowid DESC LIMIT 1`, a.ID))
		if readErr != nil && !errors.Is(readErr, ErrNotFound) {
			return zero, readErr
		}
		var unresolved bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_authorization_operations WHERE account_id=? AND (state='pending' OR cleanup_pending=1))`, a.ID).Scan(&unresolved)
		if err != nil {
			return zero, err
		}
		expiry, expiryErr := time.Parse(time.RFC3339, auth.ExpiresAt)
		if unresolved || errors.Is(readErr, ErrNotFound) || auth.State != "available" || auth.Binding.AccountID != a.ID || expiryErr != nil {
			reasons = append(reasons, "authorization_unavailable")
		} else if !expiry.After(time.Now()) {
			reasons = append(reasons, "authorization_expired")
		} else if !domain.ModelAuthorizationAllowsInference(auth.Binding) {
			reasons = append(reasons, "inference_scope_missing")
		}
	} else {
		reasons = append(reasons, "credential_metadata_unavailable")
	}
	report := domain.ModelAccountAcceptance{AccountID: a.ID, AccountRevision: a.Revision, Scope: "account_routes_only", LiveAccess: "blocked", Quota: "unknown", Checks: []domain.ModelAcceptanceCheck{}, Routes: []domain.ModelAcceptanceRoute{}}
	if a.Provider == "openai" && a.AuthMethod == "openai_siwc" {
		report.OpenAIPreparation = &domain.OpenAIPreparation{Scope: "openai_subscription_stage0", EndpointPolicy: model.OpenAIPreparationEndpointPolicy(a.BaseURL), CredentialStorage: "not_checked", RemoteBilling: "unverified", Quota: "unknown", RealConnection: "blocked", RealCatalog: "blocked", RealInference: "blocked"}
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+modelRouteColumns+` FROM model_routes WHERE account_id=? ORDER BY created_at,id`, a.ID)
	if err != nil {
		return zero, err
	}
	localReasons := append([]string{}, reasons...)
	for rows.Next() {
		route, readErr := scanModelRoute(rows)
		if readErr != nil {
			rows.Close()
			return zero, readErr
		}
		if !acceptanceIdentifierPattern.MatchString(route.ID) {
			rows.Close()
			return zero, errors.New("invalid model acceptance metadata")
		}
		rr := append([]string{}, reasons...)
		adapter := acceptanceAdapter(route.Adapter)
		environment := route.Environment
		if environment != "live" && environment != "simulation" {
			environment = "unknown"
		}
		extra := route.ExtraUsage
		if extra != "off" {
			extra = "unknown"
		}
		if domain.ValidateModelRoute(route, a) != nil {
			rr = append(rr, "route_invalid")
			localReasons = appendUniqueAcceptanceReason(localReasons, "route_invalid")
		}
		if extra == "unknown" {
			rr = append(rr, "extra_usage_local_setting_unknown")
			localReasons = appendUniqueAcceptanceReason(localReasons, "extra_usage_local_setting_unknown")
		}
		state := "ready"
		if len(rr) > 0 {
			state = "blocked"
		}
		rr = append(rr, "remote_billing_unverified")
		report.Routes = append(report.Routes, domain.ModelAcceptanceRoute{ID: route.ID, Adapter: adapter, Environment: environment, LocalMetadata: state, LocalExtraUsage: extra, RemoteExtraUsage: "unverified", ReasonCodes: rr})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return zero, err
	}
	if len(report.Routes) == 0 {
		localReasons = append(localReasons, "route_missing")
	}
	state := "ready"
	if len(localReasons) > 0 {
		state = "blocked"
	}
	report.Checks = append(report.Checks, domain.ModelAcceptanceCheck{Phase: "local_metadata", State: state, ReasonCodes: localReasons})
	for _, phase := range []string{"login", "catalog", "inference"} {
		report.Checks = append(report.Checks, domain.ModelAcceptanceCheck{Phase: phase, State: "blocked", ReasonCodes: []string{"live_provider_access_pending_confirmation"}})
	}
	report.Checks = append(report.Checks, domain.ModelAcceptanceCheck{Phase: "task", State: "not_evaluated", ReasonCodes: []string{"task_approval_required"}})
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return report, nil
}

func acceptanceAdapter(adapter string) string {
	switch adapter {
	case "openai_api_chat", "openai_api_responses", "openai_siwc_responses", "anthropic_api_messages", "zhipu_api_chat", "kimi_api_chat", "kimi_code_chat":
		return adapter
	}
	return "unknown"
}
func appendUniqueAcceptanceReason(reasons []string, reason string) []string {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}
