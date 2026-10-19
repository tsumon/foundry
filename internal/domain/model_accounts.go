package domain

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type ModelAccount struct {
	ID                       string `json:"id"`
	Revision                 int64  `json:"revision"`
	Name                     string `json:"name"`
	Provider                 string `json:"provider"`
	BaseURL                  string `json:"base_url"`
	AuthMethod               string `json:"auth_method"`
	BillingMode              string `json:"billing_mode"`
	Enabled                  bool   `json:"enabled"`
	CurrentCredentialVersion int64  `json:"current_credential_version"`
	CreatedAt                string `json:"created_at"`
	UpdatedAt                string `json:"updated_at"`
}

type ModelCredentialVersion struct {
	AccountID      string `json:"account_id"`
	Version        int64  `json:"version"`
	OperationID    string `json:"operation_id"`
	State          string `json:"state"`
	SecretRef      string `json:"-"`
	CleanupPending bool   `json:"cleanup_pending"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

var ErrInvalidModelAccount = errors.New("invalid_model_account")
var modelCredentialOperationPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

func ValidateModelCredentialOperation(id string) error {
	if !modelCredentialOperationPattern.MatchString(id) {
		return ErrInvalidModelAccount
	}
	return nil
}

func ValidateModelAccount(a ModelAccount) error {
	if !utf8.ValidString(a.Name) || strings.TrimSpace(a.Name) == "" || utf8.RuneCountInString(a.Name) > 200 || strings.IndexFunc(a.Name, unicode.IsControl) >= 0 {
		return ErrInvalidModelAccount
	}
	switch a.Provider {
	case "openai", "anthropic", "zhipu", "kimi", "custom":
	default:
		return ErrInvalidModelAccount
	}
	if a.AuthMethod != "api_key" && !(a.AuthMethod == "openai_siwc" && a.Provider == "openai") {
		return ErrInvalidModelAccount
	}
	switch a.BillingMode {
	case "subscription", "api_credits", "metered_api", "unknown":
	default:
		return ErrInvalidModelAccount
	}
	if strings.ContainsAny(a.BaseURL, "?#") {
		return ErrInvalidModelAccount
	}
	if _, _, err := ParseProviderBaseURL(a.BaseURL); err != nil {
		return ErrInvalidModelAccount
	}
	return nil
}
