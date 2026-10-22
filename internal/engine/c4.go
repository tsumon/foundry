package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"foundry/internal/domain"
	"foundry/internal/sandbox"
	"foundry/internal/store"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

func c4PlanRevision(m domain.Mission) int64 {
	if m.Plan != nil {
		return m.Plan.Revision
	}
	return 0
}
func c4Operation(m domain.Mission, id, digest string) (domain.C4Operation, error) {
	for _, o := range m.C4Operations {
		if o.ID == id && o.MaterialDigest == digest {
			return o, nil
		}
	}
	return domain.C4Operation{}, store.ErrConflict
}
func currentC4Baseline(m domain.Mission) (domain.C4Operation, error) {
	if m.Spec.C4 == nil || m.RepositoryImport == nil {
		return domain.C4Operation{}, ErrC4Unavailable
	}
	// A completed baseline attests immutable frozen source, not a planning epoch.
	// Preserve its historical epoch; current stages are fenced separately.
	for i := len(m.C4Operations) - 1; i >= 0; i-- {
		o := m.C4Operations[i]
		if o.Kind != "baseline" {
			continue
		}
		if o.Passed() && o.BaseCommit == m.BaseCommit && o.BaseDigest == m.RepositoryImport.BaseDigest && o.ProfileDigest == m.Spec.C4.ProfileDigest() {
			return o, nil
		}
		break
	}
	return domain.C4Operation{}, errors.New("C4 clean successful baseline approval required before planning")
}
func c4PlanningGate(m domain.Mission) error {
	if !isC4(m) {
		return nil
	}
	if _, err := currentC4Baseline(m); err != nil {
		return err
	}
	for _, o := range m.C4Operations {
		if o.Result == nil || o.Dispatch != nil && o.CleanupState() != "confirmed" {
			return errors.New("C4 unresolved operation or cleanup blocks planning; inspect and clean exact ownership explicitly")
		}
	}
	return nil
}
func c4PreparedRevision(o domain.C4Operation) int64 {
	prefix, _, ok := strings.Cut(o.ID, "-")
	if !ok {
		return -1
	}
	n, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil {
		return -1
	}
	return n
}
func c4ExecutionAuthority(m domain.Mission, base string) (string, error) {
	baseline, err := currentC4Baseline(m)
	if err != nil {
		return "", err
	}
	inner := m
	inner.Spec.C4 = nil
	inner.C4Operations = nil
	inner.C4Controls = nil
	seal, err := authority(inner, base, true)
	if err != nil {
		return "", err
	}
	if m.Spec.C4.ControlVersion == 1 {
		for i := len(m.C4Controls) - 1; i >= 0; i-- {
			if m.C4Controls[i].Kind == "readiness" {
				seal += "\x00" + m.C4Controls[i].MaterialDigest
				break
			}
		}
	}
	h := sha256.Sum256([]byte("FOUNDRY C4 execution authority v1\x00" + m.Spec.C4.ProfileDigest() + "\x00" + baseline.MaterialDigest + "\x00" + seal))
	return hex.EncodeToString(h[:]), nil
}
func (e *Engine) newC4Operation(m domain.Mission, kind string) (domain.C4Operation, error) {
	nonce, err := opaque()
	if err != nil {
		return domain.C4Operation{}, err
	}
	// Revision is part of opaque material identity, without changing the domain
	// schema or rewriting historical evidence when the mission advances.
	id := fmt.Sprintf("%d-%s", m.Revision, nonce)
	// A distinct namespace also keeps a legacy owner sweep from selecting C4
	// objects in a mixed disposable runtime. C4 itself only cleans exact IDs.
	owner := sha256.Sum256([]byte("foundry:c4:owner:v1\x00" + e.options.DataDir))
	o := domain.C4Operation{SchemaVersion: 1, ID: id, Kind: kind, MissionID: m.ID, Epoch: m.Epoch, PlanRevision: c4PlanRevision(m), BaseCommit: m.BaseCommit, BaseDigest: m.RepositoryImport.BaseDigest, ProfileDigest: m.Spec.C4.ProfileDigest(), ContainerName: "foundry-c4-" + id, Owner: hex.EncodeToString(owner[:12]), State: "prepared", PreparedAt: now()}
	if m.Spec.C4.ControlVersion == 1 {
		r, err := e.currentC4Readiness(m)
		if err != nil {
			return o, err
		}
		o.ReadinessID, o.ReadinessDigest = r.ID, r.MaterialDigest
		o.PreparedAt = e.c4Timestamp()
	}
	if kind == "candidate" {
		ref := *m.CandidateGeneration
		o.CandidateGeneration = &ref
		o.CandidateDigest = m.CandidateDigest
	}
	o.MaterialDigest = domain.C4MaterialDigest(o)
	return o, nil
}

// Must run inside the mission CAS, without a nested store query. Checks exact
// stage input plus current epoch/plan/generation and independent live consent.
func (e *Engine) c4Current(m domain.Mission, o domain.C4Operation) error {
	if err := e.c4Gate(m); err != nil {
		return err
	}
	if m.Spec.C4.ControlVersion == 1 && o.Dispatch == nil {
		r, err := e.currentC4Readiness(m)
		if err != nil {
			return err
		}
		if o.ReadinessID != r.ID || o.ReadinessDigest != r.MaterialDigest {
			return ErrC4Unavailable
		}
	}
	for _, prior := range m.C4Operations {
		if prior.ID != o.ID && (prior.Result == nil || prior.Dispatch != nil && prior.CleanupState() != "confirmed") {
			return ErrC4Unavailable
		}
	}
	if err := e.checkRepositoryImportBase(m); err != nil {
		return err
	}
	if err := e.checkFrozenAuthority(m); err != nil {
		return err
	}
	if o.Epoch != m.Epoch || o.PlanRevision != c4PlanRevision(m) || o.BaseCommit != m.BaseCommit || o.BaseDigest != m.RepositoryImport.BaseDigest || o.ProfileDigest != m.Spec.C4.ProfileDigest() {
		return store.ErrConflict
	}
	if o.Kind == "candidate" {
		if !reflect.DeepEqual(o.CandidateGeneration, m.CandidateGeneration) || o.CandidateDigest != m.CandidateDigest {
			return store.ErrConflict
		}
		if _, err := e.candidate(m); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) c4Persist(id string) sandbox.C4Persist {
	return func(ctx context.Context, expected domain.C4Operation, change sandbox.C4Change) (domain.C4Operation, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.c4PersistLocked(id)(ctx, expected, change)
	}
}
func (e *Engine) c4PersistLocked(id string) sandbox.C4Persist {
	return func(ctx context.Context, expected domain.C4Operation, change sandbox.C4Change) (domain.C4Operation, error) {
		if err := ctx.Err(); err != nil {
			return expected, err
		}
		m, err := e.s.GetMission(id)
		if err != nil {
			return expected, err
		}
		var committed domain.C4Operation
		_, err = e.s.UpdateMission(id, m.Revision, "c4."+change.Stage, func(current *domain.Mission) error {
			for i, o := range current.C4Operations {
				if o.ID != expected.ID {
					continue
				}
				if !reflect.DeepEqual(o, expected) {
					return store.ErrConflict
				}
				if change.Stage == "dispatch" && o.Kind == "baseline" && (o.Approval == nil || current.Revision != o.Approval.Revision+1) {
					return store.ErrConflict
				}
				if change.Stage == "cleanup" {
					if err := e.c4Gate(*current); err != nil {
						return err
					}
				} else if err := e.c4Current(*current, o); err != nil {
					return err
				}
				if err := change.Apply(&current.C4Operations[i], domain.C4Fact{Revision: current.Revision, At: e.c4Timestamp()}); err != nil {
					return err
				}
				committed = current.C4Operations[i]
				return nil
			}
			return store.ErrConflict
		})
		return committed, err
	}
}
func (e *Engine) c4Trust() sandbox.C4Trust {
	if c := e.options.C4Controlled; c != nil {
		return sandbox.C4Trust{DockerPath: c.config.Profile.DockerPath, Endpoint: c.config.Profile.Endpoint, ManagedRoot: filepath.Join(e.c4Root, "missions")}
	}
	return sandbox.C4Trust{DockerPath: e.options.C4.dockerPath, Endpoint: e.options.C4.endpoint, ManagedRoot: filepath.Join(e.c4Root, "missions")}
}

func (e *Engine) executeC4(ctx context.Context, m domain.Mission, o domain.C4Operation, path string) (domain.C4Operation, error) {
	if c := e.options.C4Controlled; c != nil {
		if c.source == "docker_c4_control" {
			return sandbox.ExecuteC4Docker(ctx, *m.Spec.C4, o, sandbox.C4DockerConfig{Enabled: true, Trust: e.c4Trust()}, path, e.c4Persist(m.ID))
		}
		return sandbox.ExecuteC4(ctx, *m.Spec.C4, o, e.c4Trust(), path, c.driver, e.c4Persist(m.ID))
	}
	return sandbox.ExecuteC4(ctx, *m.Spec.C4, o, e.c4Trust(), path, e.options.C4.driver, e.c4Persist(m.ID))
}
func (e *Engine) c4Path(path string) (string, error) {
	rel, err := filepath.Rel(e.options.DataDir, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || e.c4Root == "" {
		return "", ErrC4Unavailable
	}
	// Only the trusted configured data-root alias is normalized (e.g. /var on
	// macOS). Never follow a mission/generation symlink supplied after startup.
	return filepath.Join(e.c4Root, rel), nil
}

func c4Delivery(m domain.Mission) error {
	if !isC4(m) {
		return nil
	}
	if c4Structure(m) != nil {
		return ErrC4Unavailable
	}
	for i := len(m.C4Operations) - 1; i >= 0; i-- {
		o := m.C4Operations[i]
		if o.Kind == "candidate" && o.Epoch == m.Epoch && o.PlanRevision == c4PlanRevision(m) && reflect.DeepEqual(o.CandidateGeneration, m.CandidateGeneration) && o.CandidateDigest == m.CandidateDigest {
			if o.Passed() && len(m.Evidence) > 0 && reflect.DeepEqual(m.Evidence[len(m.Evidence)-1], c4Verification(m, o)) {
				return nil
			}
			break
		}
	}
	return errors.New("current clean C4 candidate verification evidence is missing")
}
