package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"foundry/internal/c4fixture"
	"foundry/internal/domain"
	"foundry/internal/model"
	"foundry/internal/sandbox"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// C4Activation is trusted startup authority, never a request payload or consent.
type C4Activation struct {
	SchemaVersion int              `json:"schema_version"`
	Enabled       bool             `json:"enabled"`
	Profile       domain.C4Profile `json:"profile"`
	SourceRoot    string           `json:"source_root"`
	BaseCommit    string           `json:"base_commit"`
	BaseDigest    string           `json:"base_digest"`
	ExpiresAt     string           `json:"expires_at"`
}

func ValidateC4Activation(c C4Activation, at time.Time) error {
	deadline, err := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	if len(c.SourceRoot) > 4096 || !utf8.ValidString(c.SourceRoot) || strings.ContainsFunc(c.SourceRoot, unicode.IsControl) {
		return ErrC4Unavailable
	}
	if err != nil || !deadline.After(at) || deadline.After(at.Add(24*time.Hour)) || c.SchemaVersion != 1 || !c.Enabled || c.Profile.ControlVersion != 1 || c.Profile.Validate() != nil || !filepath.IsAbs(c.SourceRoot) || filepath.Clean(c.SourceRoot) != c.SourceRoot || !regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`).MatchString(c.BaseCommit) || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(c.BaseDigest) {
		return ErrC4Unavailable
	}
	return nil
}
func (c C4Activation) Digest() string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(append([]byte("FOUNDRY C4 activation v1\x00"), b...))
	return hex.EncodeToString(sum[:])
}

type C4ControlledCapability struct {
	config    C4Activation
	source    string
	models    model.Gateway
	driver    *sandbox.C4SyntheticDriver
	readiness func(context.Context) (sandbox.C4ReadinessResult, error)
}

// Production accepts only the accountless frozen local fixture, never a router.
// Construction copies declarations and does not inspect executable/socket paths.
func NewC4DockerCapability(c C4Activation, g *c4fixture.Gateway) (*C4ControlledCapability, error) {
	if ValidateC4Activation(c, time.Now()) != nil || c.Profile.Mode != "docker" || g == nil {
		return nil, ErrC4Unavailable
	}
	return &C4ControlledCapability{config: c, source: "docker_c4_control", models: g, readiness: func(ctx context.Context) (sandbox.C4ReadinessResult, error) {
		return sandbox.CheckC4DockerReadiness(ctx, c.Profile, sandbox.C4DockerConfig{Enabled: true, Trust: sandbox.C4Trust{DockerPath: c.Profile.DockerPath, Endpoint: c.Profile.Endpoint}})
	}}, nil
}

// The synthetic seam cannot claim Docker provenance or construct real transport.
func NewC4ControlledSyntheticCapability(c C4Activation, d *sandbox.C4SyntheticDriver, g model.Gateway, ready func(context.Context) (sandbox.C4ReadinessResult, error)) (*C4ControlledCapability, error) {
	if ValidateC4Activation(c, time.Now()) != nil || c.Profile.Mode != "synthetic" || d == nil || g == nil || ready == nil {
		return nil, ErrC4Unavailable
	}
	return &C4ControlledCapability{config: c, source: "synthetic_c4_control", models: g, driver: d, readiness: ready}, nil
}

func (e *Engine) c4Time() time.Time {
	if e.c4Clock != nil {
		return e.c4Clock().UTC()
	}
	return time.Now().UTC()
}
func (e *Engine) c4Timestamp() string { return e.c4Time().Format(time.RFC3339Nano) }

// Display only: availability never grants action consent.
type C4ControlledStatus struct {
	ActivationDigest      string `json:"activation_digest,omitempty"`
	Available             bool   `json:"available"`
	StructurallyAvailable bool   `json:"structurally_available"`
	SourceKind            string `json:"source_kind,omitempty"`
	ExpiresAt             string `json:"expires_at,omitempty"`
	Expired               bool   `json:"expired"`
}

func (e *Engine) C4ControlledStatus(m domain.Mission) C4ControlledStatus {
	c := e.options.C4Controlled
	if c == nil {
		return C4ControlledStatus{}
	}
	deadline, _ := time.Parse(time.RFC3339Nano, c.config.ExpiresAt)
	return C4ControlledStatus{ActivationDigest: c.config.Digest(), Available: e.c4ControlledGate(m, true) == nil, StructurallyAvailable: e.c4ControlledGate(m, false) == nil, SourceKind: c.source, ExpiresAt: c.config.ExpiresAt, Expired: !e.c4Time().Before(deadline)}
}
func (e *Engine) c4ControlledGate(m domain.Mission, fresh bool) error {
	c := e.options.C4Controlled
	if c == nil || e.options.C4 != nil || e.c4Root == "" || c.models == nil || c4Structure(m) != nil || m.Spec.C4 == nil || m.Spec.C4.ControlVersion != 1 || m.Spec.C4.ProfileDigest() != c.config.Profile.ProfileDigest() || m.RepositoryImport == nil || m.RepositoryImport.Root != c.config.SourceRoot || m.RepoPath != c.config.SourceRoot || m.BaseCommit != c.config.BaseCommit || m.RepositoryImport.BaseDigest != c.config.BaseDigest {
		return ErrC4Unavailable
	}
	deadline, err := time.Parse(time.RFC3339Nano, c.config.ExpiresAt)
	if err != nil || fresh && !e.c4Time().Before(deadline) {
		return ErrC4Unavailable
	}
	return nil
}
