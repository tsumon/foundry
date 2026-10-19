package capability

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"foundry/internal/domain"
	"foundry/internal/mcp"
	"foundry/internal/store"
)

type MCPInput struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Name             string `json:"name"`
	Endpoint         string `json:"endpoint"`
	Enabled          bool   `json:"enabled"`
	APIKey           string `json:"api_key"`
	ClearKey         bool   `json:"clear_key"`
}

func (s *Service) SaveMCP(in MCPInput) (domain.MCPConnection, error) {
	zero := domain.MCPConnection{}
	if in.ExpectedRevision < 0 || !utf8.ValidString(in.Name) || strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 || strings.ContainsRune(in.Name, 0) || mcp.ValidateEndpoint(in.Endpoint) != nil || len(in.APIKey) > 8192 || strings.ContainsFunc(in.APIKey, unicode.IsControl) || (in.APIKey != "" && in.ClearKey) {
		return zero, domain.ErrInvalidCapability
	}
	var old domain.MCPConnection
	var err error
	if in.ID == "" {
		if in.ExpectedRevision != 0 {
			return zero, domain.ErrInvalidCapability
		}
		in.ID, err = newID()
		if err != nil {
			return zero, err
		}
	} else {
		old, err = s.store.GetMCPConnection(in.ID)
		if err != nil && (!errors.Is(err, store.ErrNotFound) || in.ExpectedRevision != 0) {
			return zero, err
		}
	}
	if old.Revision != in.ExpectedRevision {
		return zero, store.ErrConflict
	}
	if old.Revision > 0 && old.Endpoint != in.Endpoint && in.APIKey == "" && !in.ClearKey {
		return zero, domain.ErrInvalidCapability
	}
	c := domain.MCPConnection{ID: in.ID, Name: in.Name, Endpoint: in.Endpoint, Enabled: in.Enabled, Protocol: domain.MCPProtocol, CredentialRef: old.CredentialRef, CredentialRevision: old.CredentialRevision}
	newRef := ""
	if in.APIKey != "" {
		newRef, err = newID()
		if err != nil {
			return zero, err
		}
		if s.secrets == nil {
			return zero, errors.New("credential storage unavailable")
		}
		if err = s.secrets.Set(newRef, in.APIKey); err != nil {
			_ = s.secrets.Delete(newRef)
			return zero, errors.New("credential storage unavailable")
		}
		c.CredentialRef = newRef
		c.CredentialRevision++
	} else if in.ClearKey {
		c.CredentialRef = ""
		c.CredentialRevision++
	}
	saved, err := s.store.SaveMCPConnection(c, in.ExpectedRevision)
	if err == nil {
		return saved, nil
	}
	// A commit error can mean the write succeeded. Never erase a reference that
	// authoritative state owns, and never delete an old credential on rotation.
	authoritative, readErr := s.store.GetMCPConnection(c.ID)
	if !errors.Is(err, store.ErrConflict) && readErr == nil && authoritative.Revision == in.ExpectedRevision+1 && authoritative.CredentialRef == c.CredentialRef && authoritative.Endpoint == c.Endpoint && authoritative.Name == c.Name && authoritative.Enabled == c.Enabled {
		return authoritative, nil
	}
	if newRef != "" && (errors.Is(readErr, store.ErrNotFound) || (readErr == nil && authoritative.CredentialRef != newRef)) {
		_ = s.secrets.Delete(newRef)
	}
	return zero, err
}

func (s *Service) Discover(ctx context.Context, id string, expectedRevision int64, requestID string, consent bool) (domain.DiscoveryOperation, error) {
	zero := domain.DiscoveryOperation{}
	if !consent || expectedRevision < 1 {
		return zero, domain.ErrInvalidCapability
	}
	// Replay is evaluated against original client intent, not mutable settings.
	prior, err := s.store.GetDiscovery(requestID)
	if err == nil {
		if prior.ConnectionID != id || prior.ConnectionRevision != expectedRevision || prior.Consent != consent {
			return zero, store.ErrConflict
		}
		return prior, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return zero, err
	}
	c, err := s.store.GetMCPConnection(id)
	if err != nil {
		return zero, err
	}
	if c.Revision != expectedRevision || !c.Enabled {
		return zero, store.ErrConflict
	}
	intent := domain.DiscoveryOperation{ID: requestID, ConnectionID: id, ConnectionRevision: expectedRevision, Endpoint: c.Endpoint, CredentialRevision: c.CredentialRevision, Consent: true, AuthorizedAt: time.Now().UTC().Format(time.RFC3339Nano), Budget: domain.FixedDiscoveryBudget()}
	op, created, err := s.store.BeginDiscovery(intent)
	if err != nil {
		if errors.Is(err, store.ErrConflict) || errors.Is(err, domain.ErrInvalidCapability) {
			return zero, err
		}
		authoritative, readErr := s.reconcile(requestID, err)
		if readErr == nil && (authoritative.ConnectionID != id || authoritative.ConnectionRevision != expectedRevision || authoritative.Endpoint != c.Endpoint || authoritative.CredentialRevision != c.CredentialRevision || !authoritative.Consent) {
			return zero, store.ErrConflict
		}
		return authoritative, readErr
	}
	if !created {
		return op, nil
	}
	op, err = s.store.ClaimDiscovery(requestID)
	if err != nil {
		return s.reconcile(requestID, err)
	}
	credential := ""
	if c.CredentialRef != "" {
		if s.secrets == nil {
			err = errors.New("credential storage unavailable")
		} else {
			credential, err = s.secrets.Get(c.CredentialRef)
		}
		if err != nil {
			return s.finish(requestID, "failed", nil, 0, 0, "storage_error")
		}
	}
	result, discoverErr := mcp.Discover(ctx, c.Endpoint, credential)
	if discoverErr != nil {
		code, status := "transport_error", "outcome_unknown"
		var e *mcp.Error
		if errors.As(discoverErr, &e) {
			code = e.Code
			if !e.Unknown {
				status = "failed"
			}
		}
		return s.finish(requestID, status, nil, result.Requests, result.ResponseBytes, code)
	}
	snapshotID, err := newID()
	if err != nil {
		return s.finish(requestID, "outcome_unknown", nil, result.Requests, result.ResponseBytes, "storage_error")
	}
	snapshot := &domain.MCPToolSnapshot{ID: snapshotID, ConnectionID: id, ConnectionRevision: expectedRevision, Protocol: result.ProtocolVersion, ServerName: result.ServerName, ServerVersion: result.ServerVersion, Tools: result.Tools, Digest: result.Digest, ObservedAt: result.ObservedAt, ExecutionVersion: 1}
	return s.finish(requestID, "succeeded", snapshot, result.Requests, result.ResponseBytes, "")
}

func (s *Service) finish(id, status string, snapshot *domain.MCPToolSnapshot, requests int, bytes int64, code string) (domain.DiscoveryOperation, error) {
	op, err := s.store.FinishDiscovery(id, status, snapshot, requests, bytes, code)
	if err != nil {
		return s.reconcile(id, err)
	}
	return op, nil
}
func (s *Service) reconcile(id string, cause error) (domain.DiscoveryOperation, error) {
	op, err := s.store.GetDiscovery(id)
	if err == nil {
		return op, nil
	} // pending/dispatching is truthful; never redispatch.
	return domain.DiscoveryOperation{}, cause
}
