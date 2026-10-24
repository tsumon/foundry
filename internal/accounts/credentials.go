package accounts

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"foundry/internal/domain"
	"foundry/internal/store"
)

func (s *Service) AddCredential(ctx context.Context, accountID string, expectedRevision int64, operationID, key string) (domain.ModelCredentialVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.ModelCredentialVersion{}, safeError(err)
	}
	if accountID == "" || len(accountID) > 256 {
		return domain.ModelCredentialVersion{}, ErrInvalid
	}
	if err := domain.ValidateModelCredentialOperation(operationID); err != nil {
		return domain.ModelCredentialVersion{}, safeError(err)
	}
	// Terminal replay is a read of metadata only. In particular, do not inspect
	// the replacement key, read the old key, or compare key hashes.
	old, err := s.store.GetModelCredentialOperation(operationID)
	if err == nil {
		if old.AccountID != accountID {
			return domain.ModelCredentialVersion{}, store.ErrConflict
		}
		if old.State == "pending" {
			return domain.ModelCredentialVersion{}, ErrInProgress
		}
		return old, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return domain.ModelCredentialVersion{}, safeError(err)
	}
	if expectedRevision < 1 || len(key) < 1 || len(key) > 8192 || !utf8.ValidString(key) || strings.IndexFunc(key, unicode.IsControl) >= 0 {
		return domain.ModelCredentialVersion{}, ErrInvalid
	}
	if s.secrets == nil {
		return domain.ModelCredentialVersion{}, ErrUnavailable
	}
	ref, err := opaqueID("foundry-model-account-")
	if err != nil {
		return domain.ModelCredentialVersion{}, safeError(err)
	}
	intent, err := s.store.BeginModelCredential(domain.ModelCredentialVersion{AccountID: accountID, OperationID: operationID, SecretRef: ref}, expectedRevision)
	if err != nil {
		return domain.ModelCredentialVersion{}, safeError(err)
	}
	if intent.State != "pending" {
		return intent, nil
	}
	// Another Service may have won the reservation between the lookup and Begin.
	// Only the creator of the unpredictable, persisted reference owns the write.
	if intent.SecretRef != ref {
		return domain.ModelCredentialVersion{}, ErrInProgress
	}
	if err := ctx.Err(); err != nil {
		s.failCredential(intent, "interrupted")
		return domain.ModelCredentialVersion{}, safeError(err)
	}
	if err := s.secrets.Set(intent.SecretRef, key); err != nil {
		s.failCredential(intent, "failed")
		return domain.ModelCredentialVersion{}, safeError(err)
	}
	available, err := s.store.FinishModelCredential(operationID, "available")
	if err != nil {
		s.failCredential(intent, "failed")
		return domain.ModelCredentialVersion{}, safeError(err)
	}
	return available, nil
}

// Failure finalization is best effort, but the durable intent is never removed.
// If SQLite is unavailable, leave the exact reference for startup recovery.
// Never delete until metadata confirms a non-available terminal state.
func (s *Service) failCredential(intent domain.ModelCredentialVersion, state string) {
	terminal, err := s.store.FinishModelCredential(intent.OperationID, state)
	if err == nil {
		_ = s.cleanupCredential(terminal)
	}
}

func (s *Service) cleanupCredential(v domain.ModelCredentialVersion) error {
	if !v.CleanupPending || (v.State != "failed" && v.State != "interrupted" && v.State != "revoked") {
		return nil
	}
	if s.secrets == nil {
		return ErrUnavailable
	}
	if err := s.secrets.Delete(v.SecretRef); err != nil {
		return safeError(err)
	}
	return safeError(s.store.CompleteModelCredentialCleanup(v.AccountID, v.Version))
}

func (s *Service) RevokeCredential(ctx context.Context, accountID string, version, expectedRevision int64) (domain.ModelCredentialVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return domain.ModelCredentialVersion{}, safeError(err)
	}
	if accountID == "" || len(accountID) > 256 || version < 1 || expectedRevision < 1 {
		return domain.ModelCredentialVersion{}, ErrInvalid
	}
	revoked, err := s.store.RevokeModelCredential(accountID, version, expectedRevision)
	if err != nil {
		return domain.ModelCredentialVersion{}, safeError(err)
	}
	if err := s.cleanupCredential(revoked); err != nil {
		return domain.ModelCredentialVersion{}, err
	}
	result, err := s.store.GetModelCredential(accountID, version)
	return result, safeError(err)
}

// Recover is a startup readiness gate, before serving credential mutations. It
// never reads or writes a secret: only abandoned intents and local deletions are
// reconciled. A failed deletion remains durable and prevents successful startup.
func (s *Service) Recover(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return safeError(err)
	}
	if err := s.store.RecoverModelAuthorizations(); err != nil {
		return safeError(err)
	}
	if err := s.store.RecoverSessionRenewals(); err != nil {
		return safeError(err)
	}
	if err := s.cleanupAuthorizations(ctx); err != nil {
		return err
	}
	pending, err := s.store.PendingModelCredentialCleanup()
	if err != nil {
		return safeError(err)
	}
	for _, v := range pending {
		if err := ctx.Err(); err != nil {
			return safeError(err)
		}
		if v.State == "pending" {
			v, err = s.store.FinishModelCredential(v.OperationID, "interrupted")
			if err != nil {
				return safeError(err)
			}
		}
		if err := s.cleanupCredential(v); err != nil {
			return err
		}
	}
	return nil
}
