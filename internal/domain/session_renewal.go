package domain

import "errors"

var ErrSessionRenewal = errors.New("session_renewal_unavailable")

// SessionRenewalMaterial is metadata only. It grants credential maintenance,
// never inference, catalog access, task continuation or paid API fallback.
type SessionRenewalMaterial struct {
	ID              string                    `json:"id"`
	AccountID       string                    `json:"account_id"`
	AccountRevision int64                     `json:"account_revision"`
	Revision        int64                     `json:"revision"`
	Binding         ModelAuthorizationBinding `json:"binding"`
	Resource        string                    `json:"resource"`
	TokenEndpoint   string                    `json:"token_endpoint"`
	ExpiresAt       string                    `json:"expires_at"`
	CredentialStore string                    `json:"credential_store"`
	Scope           string                    `json:"approval_scope"`
	Schedule        string                    `json:"schedule"`
	FailureBehavior string                    `json:"failure_behavior"`
}

type SessionRenewalPolicy struct {
	AccountID string                 `json:"account_id"`
	Revision  int64                  `json:"revision"`
	Enabled   bool                   `json:"enabled"`
	Material  SessionRenewalMaterial `json:"material"`
}

type SessionRenewalReceipt struct {
	AuthorizationID string `json:"authorization_id"`
	OldGeneration   int64  `json:"old_generation"`
	NewGeneration   int64  `json:"new_generation"`
	Stage           string `json:"stage"`
	Category        string `json:"category"`
	Certainty       string `json:"certainty"`
	StartedAt       string `json:"started_at"`
	FinishedAt      string `json:"finished_at"`
}

type SessionRenewalStatus struct {
	AccountID            string                  `json:"account_id"`
	AccountRevision      int64                   `json:"account_revision"`
	Revision             int64                   `json:"revision"`
	Enabled              bool                    `json:"enabled"`
	RegistrationRetained bool                    `json:"registration_retained"`
	AccessState          string                  `json:"access_state"`
	State                string                  `json:"state"`
	DueAt                string                  `json:"due_at,omitempty"`
	Material             *SessionRenewalMaterial `json:"material,omitempty"`
	Receipt              *SessionRenewalReceipt  `json:"receipt,omitempty"`
}
