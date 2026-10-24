package domain

import (
	"encoding/json"
	"errors"
	"reflect"
	"time"
)

var ErrInvalidModelContinuation = errors.New("invalid_model_continuation")

// Material fields never change. Approval and application are independent,
// single-use facts; application alone selects the replacement snapshot.
type ModelContinuation struct {
	SchemaVersion           int                    `json:"schema_version"`
	ID                      string                 `json:"id"`
	MissionID               string                 `json:"mission_id"`
	PreparedRevision        int64                  `json:"prepared_revision"`
	PredecessorDigest       string                 `json:"predecessor_digest"`
	Replacement             MissionModelSnapshot   `json:"replacement"`
	AuthorizationGeneration int64                  `json:"authorization_generation"`
	AuthorizationExpiresAt  string                 `json:"authorization_expires_at"`
	AffectedRoles           []Role                 `json:"affected_roles"`
	CreatedAt               string                 `json:"created_at"`
	ExpiresAt               string                 `json:"expires_at"`
	Digest                  string                 `json:"digest"`
	Approval                *ModelContinuationFact `json:"approval,omitempty"`
	Application             *ModelContinuationFact `json:"application,omitempty"`
}

type ModelContinuationFact struct {
	Revision  int64  `json:"revision"`
	Digest    string `json:"digest"`
	CreatedAt string `json:"created_at"`
}

func ModelContinuationDigest(c ModelContinuation) string {
	c.Digest = ""
	c.Approval = nil
	c.Application = nil
	return AcceptanceDigest(c)
}

// ValidateModelContinuationReplacement permits only the local grant/catalog
// binding and account revision to advance. No scope, identity or spend drift.
func ValidateModelContinuationReplacement(old, next MissionModelSnapshot, roles []Role) error {
	if old.SchemaVersion != 2 || next.SchemaVersion != 2 || len(roles) == 0 || len(old.Roles) != 7 || len(next.Roles) != 7 {
		return ErrInvalidModelContinuation
	}
	seen := map[Role]bool{}
	authorizationID := ""
	for _, role := range roles {
		if seen[role] {
			return ErrInvalidModelContinuation
		}
		seen[role] = true
		a, ok := old.Roles[role]
		b, present := next.Roles[role]
		if !ok || !present || a.Account == nil || b.Account == nil || a.Route == nil || b.Route == nil || a.Authorization == nil || b.Authorization == nil || b.Catalog == nil {
			return ErrInvalidModelContinuation
		}
		if a.Account.AuthMethod != "openai_siwc" || a.Account.ID != b.Account.ID || a.Account.Provider != b.Account.Provider || a.Account.AuthMethod != b.Account.AuthMethod || a.Account.BaseURL != b.Account.BaseURL || a.Account.BillingMode != b.Account.BillingMode || !b.Account.Enabled || *a.Route != *b.Route || b.Route.SpendClass != "subscription_only" || b.Route.ExtraUsage != "off" {
			return ErrInvalidModelContinuation
		}
		binding := *b.Authorization
		binding.ID = a.Authorization.ID
		if !SameModelAuthorizationBinding(*a.Authorization, binding) || a.Authorization.ID == b.Authorization.ID || !ModelAuthorizationAllowsInference(*b.Authorization) {
			return ErrInvalidModelContinuation
		}
		if authorizationID != "" && authorizationID != b.Authorization.ID {
			return ErrInvalidModelContinuation
		}
		authorizationID = b.Authorization.ID
		profile := b.Profile
		profile.AccountRevision = a.Profile.AccountRevision
		profile.CatalogOperationID = a.Profile.CatalogOperationID
		if profile != a.Profile {
			return ErrInvalidModelContinuation
		}
	}
	for role, a := range old.Roles {
		if !seen[role] && !reflect.DeepEqual(a, next.Roles[role]) {
			return ErrInvalidModelContinuation
		}
	}
	return nil
}

func validateModelContinuationHistory(m Mission) (*MissionModelSnapshot, error) {
	if m.ModelContinuationsInvalid {
		return nil, ErrInvalidModelContinuation
	}
	current := m.ModelSnapshot
	seen := map[string]bool{}
	var lastRevision int64
	for _, c := range m.ModelContinuations {
		if current == nil || c.SchemaVersion != 1 || ValidateModelCredentialOperation(c.ID) != nil || seen[c.ID] || c.MissionID != m.ID || c.PreparedRevision < 1 || c.PreparedRevision >= m.Revision || c.PreparedRevision < lastRevision || c.PredecessorDigest != current.Digest || c.Digest != ModelContinuationDigest(c) || c.AuthorizationGeneration < 1 {
			return nil, ErrInvalidModelContinuation
		}
		seen[c.ID] = true
		created, e := time.Parse(time.RFC3339Nano, c.CreatedAt)
		expiry, ee := time.Parse(time.RFC3339Nano, c.ExpiresAt)
		grantExpiry, ge := time.Parse(time.RFC3339Nano, c.AuthorizationExpiresAt)
		if e != nil || ee != nil || ge != nil || !expiry.After(created) || expiry.After(created.Add(10*time.Minute)) || expiry.After(grantExpiry) {
			return nil, ErrInvalidModelContinuation
		}
		copy := m
		copy.ModelSnapshot = &c.Replacement
		copy.ModelPolicySnapshot = nil
		copy.ModelPolicySnapshotInvalid = false
		copy.Profiles = map[Role]Profile{}
		for role, selection := range c.Replacement.Roles {
			copy.Profiles[role] = selection.Profile
		}
		if validateOriginalMissionModelSnapshot(copy) != nil || ValidateModelContinuationReplacement(*current, c.Replacement, c.AffectedRoles) != nil {
			return nil, ErrInvalidModelContinuation
		}
		for _, role := range c.AffectedRoles {
			proof := c.Replacement.Roles[role].Catalog
			if proof.AuthorizationGeneration != c.AuthorizationGeneration || proof.ExpiresAt != c.AuthorizationExpiresAt {
				return nil, ErrInvalidModelContinuation
			}
		}
		lastRevision = c.PreparedRevision + 1
		for i, fact := range []*ModelContinuationFact{c.Approval, c.Application} {
			if fact == nil {
				if i == 0 && c.Application != nil {
					return nil, ErrInvalidModelContinuation
				}
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, fact.CreatedAt)
			if err != nil || at.Before(created) || !at.Before(expiry) || fact.Digest != c.Digest || fact.Revision > m.Revision || fact.Revision != c.PreparedRevision+int64(i)+2 {
				return nil, ErrInvalidModelContinuation
			}
			if i == 1 {
				approved, _ := time.Parse(time.RFC3339Nano, c.Approval.CreatedAt)
				if at.Before(approved) {
					return nil, ErrInvalidModelContinuation
				}
			}
			lastRevision = fact.Revision
		}
		if c.Application != nil {
			snapshot := c.Replacement
			current = &snapshot
		}
	}
	return current, nil
}

func EffectiveModelSnapshot(m Mission) (*MissionModelSnapshot, error) {
	if err := validateOriginalMissionModelSnapshot(m); err != nil {
		return nil, err
	}
	return validateModelContinuationHistory(m)
}

// HistoricalModelSnapshot resolves evidence only, never current permission.
func HistoricalModelSnapshot(m Mission, digest string) (*MissionModelSnapshot, error) {
	if _, err := EffectiveModelSnapshot(m); err != nil {
		return nil, err
	}
	if m.ModelSnapshot != nil && m.ModelSnapshot.Digest == digest {
		return m.ModelSnapshot, nil
	}
	for _, c := range m.ModelContinuations {
		if c.Application != nil && c.Replacement.Digest == digest {
			snapshot := c.Replacement
			return &snapshot, nil
		}
	}
	return nil, ErrInvalidModelContinuation
}

func ModelContinuationAppliedRevision(m Mission) int64 {
	var revision int64
	for _, c := range m.ModelContinuations {
		if c.Application != nil {
			revision = c.Application.Revision
		}
	}
	return revision
}

func ApprovalPostdatesModelContinuation(m Mission, a Approval) bool {
	for i := len(m.ModelContinuations) - 1; i >= 0; i-- {
		if fact := m.ModelContinuations[i].Application; fact != nil {
			applied, err := time.Parse(time.RFC3339Nano, fact.CreatedAt)
			approved, e := time.Parse(time.RFC3339Nano, a.CreatedAt)
			return err == nil && e == nil && a.Revision >= fact.Revision && approved.After(applied)
		}
	}
	return true
}

// Detached effective selections are useful at request boundaries; callers must
// never persist this projection over the original task snapshot or profiles.
func EffectiveModelProfiles(m Mission) (map[Role]Profile, error) {
	snapshot, err := EffectiveModelSnapshot(m)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return m.Profiles, nil
	}
	profiles := map[Role]Profile{}
	for role, s := range snapshot.Roles {
		profiles[role] = s.Profile
	}
	return profiles, nil
}

func SameModelContinuations(a, b []ModelContinuation) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
