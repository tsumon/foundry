package capability

import (
	"crypto/rand"
	"encoding/hex"
	"errors"

	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/store"
)

type Service struct {
	store   *store.Store
	secrets model.Secrets
}

func New(s *store.Store, secrets model.Secrets) *Service { return &Service{store: s, secrets: secrets} }
func (s *Service) Snapshot() (domain.CapabilitySnapshot, error) {
	state, err := s.store.Capabilities()
	if err != nil {
		return domain.CapabilitySnapshot{}, err
	}
	for _, revision := range state.Skills {
		if err = verifySkill(revision); err != nil {
			return domain.CapabilitySnapshot{}, err
		}
	}
	return state, nil
}
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *Service) Discovery(id string) (domain.DiscoveryOperation, error) {
	return s.store.GetDiscovery(id)
}

// SnapshotForOperation never substitutes a connection's newer/older snapshot.
// Historic operations remain readable even when their snapshot is no longer in
// the latest-success view; in that case HTTP omits the optional snapshot.
func (s *Service) SnapshotForOperation(op domain.DiscoveryOperation) (*domain.MCPToolSnapshot, error) {
	if op.SnapshotID == "" {
		return nil, nil
	}
	snapshot, err := s.store.GetMCPToolSnapshot(op.SnapshotID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if snapshot.ConnectionID != op.ConnectionID || snapshot.ConnectionRevision != op.ConnectionRevision {
		return nil, store.ErrConflict
	}
	return &snapshot, nil
}
