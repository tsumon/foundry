// Package accounts manages metadata and local credential writes only. It does
// not authenticate accounts, inspect providers, or enable inference.
package accounts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"

	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
)

var (
	ErrInvalid     = errors.New("invalid_model_account")
	ErrInProgress  = errors.New("model_credential_in_progress")
	ErrUnavailable = errors.New("model_account_storage_unavailable")
)

type Service struct {
	mu      sync.Mutex
	store   *store.Store
	secrets model.Secrets
}

func New(s *store.Store, secrets model.Secrets) *Service { return &Service{store: s, secrets: secrets} }

func (s *Service) Save(a domain.ModelAccount, expectedRevision int64) (domain.ModelAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := domain.ValidateModelAccount(a); err != nil {
		return domain.ModelAccount{}, safeError(err)
	}
	if expectedRevision == 0 {
		if a.ID != "" {
			return domain.ModelAccount{}, ErrInvalid
		}
		id, err := opaqueID("model-account-")
		if err != nil {
			return domain.ModelAccount{}, safeError(err)
		}
		a.ID = id
	} else if a.ID == "" || expectedRevision < 0 {
		return domain.ModelAccount{}, ErrInvalid
	}
	result, err := s.store.SaveModelAccount(a, expectedRevision)
	return result, safeError(err)
}

func opaqueID(prefix string) (string, error) {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(random[:]), nil
}

// safeError deliberately never wraps storage or Keychain text: errors may
// contain a submitted key, endpoint, or private Keychain reference.
func safeError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrConflict):
		return store.ErrConflict
	case errors.Is(err, store.ErrNotFound):
		return store.ErrNotFound
	case errors.Is(err, domain.ErrInvalidModelAccount):
		return ErrInvalid
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	default:
		return ErrUnavailable
	}
}
